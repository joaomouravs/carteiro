package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/testdb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) { testdb.Run(m, &testPool) }

const adminToken = "test-admin-token-0123456789abcdef"

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// testClient sends requests straight to the handler, without a network.
type testClient struct {
	t *testing.T
	h http.Handler
}

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.NewCipher(bytes.Repeat([]byte{7}, crypto.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeResolver answers lookups from a fixed table, so tests never depend on
// real DNS. IP literals resolve to themselves, as with net.Resolver; a host
// listed with no addresses does not exist; any other host resolves to a
// public address from a documentation range (RFC 5737).
type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	addrs, ok := f[host]
	if !ok {
		return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
	}
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return addrs, nil
}

var testResolver = fakeResolver{
	"localhost":            {netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1")},
	"internal.example.com": {netip.MustParseAddr("10.0.0.5")},
	"mixed.example.com":    {netip.MustParseAddr("203.0.113.20"), netip.MustParseAddr("10.0.0.6")},
	"metadata.example.com": {netip.MustParseAddr("169.254.169.254")},
	"missing.example.com":  {},
}

// newClient starts each test from an empty database.
func newClient(t *testing.T) *testClient {
	t.Helper()
	return newClientWith(t, func(*Options) {})
}

func newClientWith(t *testing.T, configure func(*Options)) *testClient {
	t.Helper()
	testdb.Require(t, testPool)
	testdb.Reset(t, testPool)
	opts := Options{AdminToken: adminToken, Cipher: testCipher(t), Resolver: testResolver}
	configure(&opts)
	return &testClient{t: t, h: New(discard, testPool, opts).Handler()}
}

// do sends a request. A string body is sent as is; anything else is encoded
// as JSON.
func (c *testClient) do(method, path, token string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		r = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}
}

// wantProblem checks that an error response follows RFC 9457.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int) problem {
	t.Helper()
	wantStatus(t, rec, status)
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	p := decode[problem](t, rec)
	if p.Status != status || p.Title != http.StatusText(status) || p.Type != "about:blank" {
		t.Errorf("malformed problem: %+v", p)
	}
	return p
}

func (c *testClient) createApp(name string) applicationResponse {
	c.t.Helper()
	rec := c.do("POST", "/v1/applications", adminToken, map[string]string{"name": name})
	wantStatus(c.t, rec, http.StatusCreated)
	return decode[applicationResponse](c.t, rec)
}

func (c *testClient) createKey(appID uuid.UUID) apiKeyResponse {
	c.t.Helper()
	rec := c.do("POST", "/v1/applications/"+appID.String()+"/api-keys", adminToken, map[string]string{"name": "ci"})
	wantStatus(c.t, rec, http.StatusCreated)
	return decode[apiKeyResponse](c.t, rec)
}

func TestAdminAuthentication(t *testing.T) {
	c := newClient(t)
	app := c.createApp("acme")
	apiKey := c.createKey(app.ID).Key

	tests := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong scheme", "Basic " + adminToken},
		{"empty token", "Bearer "},
		{"wrong token", "Bearer not-the-admin-token-0123456789"},
		{"token prefix only", "Bearer " + adminToken[:10]},
		{"api key is not an admin token", "Bearer " + apiKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/applications", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			c.h.ServeHTTP(rec, req)

			wantProblem(t, rec, http.StatusUnauthorized)
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Error("401 without WWW-Authenticate: Bearer")
			}
		})
	}

	t.Run("scheme is case-insensitive", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/applications", nil)
		req.Header.Set("Authorization", "bearer "+adminToken)
		rec := httptest.NewRecorder()
		c.h.ServeHTTP(rec, req)
		wantStatus(t, rec, http.StatusOK)
	})
}

func TestCreateApplication(t *testing.T) {
	c := newClient(t)

	rec := c.do("POST", "/v1/applications", adminToken, map[string]string{"name": "  Acme Inc  "})
	wantStatus(t, rec, http.StatusCreated)
	app := decode[applicationResponse](t, rec)

	if app.Name != "Acme Inc" {
		t.Errorf("name = %q, want it trimmed", app.Name)
	}
	if app.ID.Version() != 7 || app.CreatedAt.IsZero() {
		t.Errorf("unexpected application %+v", app)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/applications/"+app.ID.String() {
		t.Errorf("Location = %q", loc)
	}
}

func TestCreateApplicationRejectsBadInput(t *testing.T) {
	c := newClient(t)

	tests := []struct {
		name        string
		body        string
		contentType string
		status      int
		field       string // expected entry in problem.errors, if any
	}{
		{"empty body", "", "application/json", http.StatusBadRequest, ""},
		{"malformed JSON", `{"name":`, "application/json", http.StatusBadRequest, ""},
		{"unknown field", `{"title":"acme"}`, "application/json", http.StatusBadRequest, ""},
		{"two objects", `{"name":"a"}{"name":"b"}`, "application/json", http.StatusBadRequest, ""},
		{"wrong type", `{"name":42}`, "application/json", http.StatusBadRequest, ""},
		{"not JSON", `name=acme`, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType, ""},
		{"missing name", `{}`, "application/json", http.StatusUnprocessableEntity, "name"},
		{"blank name", `{"name":"   "}`, "application/json", http.StatusUnprocessableEntity, "name"},
		{"name too long", `{"name":"` + strings.Repeat("a", 201) + `"}`, "application/json", http.StatusUnprocessableEntity, "name"},
		{"body too large", `{"name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, "application/json", http.StatusRequestEntityTooLarge, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/applications", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			rec := httptest.NewRecorder()
			c.h.ServeHTTP(rec, req)

			p := wantProblem(t, rec, tt.status)
			if tt.field != "" && (len(p.Errors) != 1 || p.Errors[0].Field != tt.field) {
				t.Errorf("errors = %+v, want one error on %q", p.Errors, tt.field)
			}
		})
	}

	var count int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM applications").Scan(&count); err != nil || count != 0 {
		t.Errorf("rejected requests created %d applications (err %v)", count, err)
	}
}

func TestGetApplication(t *testing.T) {
	c := newClient(t)
	app := c.createApp("acme")

	rec := c.do("GET", "/v1/applications/"+app.ID.String(), adminToken, nil)
	wantStatus(t, rec, http.StatusOK)
	if got := decode[applicationResponse](t, rec); got != app {
		t.Errorf("got %+v, want %+v", got, app)
	}

	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		wantProblem(t, c.do("GET", "/v1/applications/"+id, adminToken, nil), http.StatusNotFound)
	}
}

func TestListApplicationsPagination(t *testing.T) {
	c := newClient(t)
	var created []uuid.UUID
	for i := range 7 {
		created = append(created, c.createApp("app "+string(rune('a'+i))).ID)
	}

	var seen []uuid.UUID
	var pageSizes []int
	path := "/v1/applications?limit=3"
	for {
		rec := c.do("GET", path, adminToken, nil)
		wantStatus(t, rec, http.StatusOK)
		page := decode[listResponse[applicationResponse]](t, rec)
		pageSizes = append(pageSizes, len(page.Data))
		for _, a := range page.Data {
			seen = append(seen, a.ID)
		}
		if page.NextCursor == nil {
			break
		}
		path = "/v1/applications?limit=3&cursor=" + *page.NextCursor
	}

	if len(pageSizes) != 3 || pageSizes[0] != 3 || pageSizes[1] != 3 || pageSizes[2] != 1 {
		t.Errorf("page sizes = %v, want [3 3 1]", pageSizes)
	}
	// Newest first: the reverse of creation order, each id exactly once.
	if len(seen) != len(created) {
		t.Fatalf("saw %d applications, want %d", len(seen), len(created))
	}
	for i, id := range seen {
		if want := created[len(created)-1-i]; id != want {
			t.Errorf("position %d: got %s, want %s", i, id, want)
		}
	}

	t.Run("empty list is an empty array", func(t *testing.T) {
		testdb.Reset(t, testPool)
		rec := c.do("GET", "/v1/applications", adminToken, nil)
		wantStatus(t, rec, http.StatusOK)
		if body := strings.TrimSpace(rec.Body.String()); body != `{"data":[],"next_cursor":null}` {
			t.Errorf("body = %s", body)
		}
	})

	for _, q := range []string{"limit=0", "limit=101", "limit=ten", "cursor=garbage", "cursor=" + encodeCursor(uuid.Nil)[:5]} {
		t.Run("invalid "+q, func(t *testing.T) {
			wantProblem(t, c.do("GET", "/v1/applications?"+q, adminToken, nil), http.StatusUnprocessableEntity)
		})
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	c := newClient(t)
	app := c.createApp("acme")

	rec := c.do("POST", "/v1/applications/"+app.ID.String()+"/api-keys", adminToken, map[string]string{"name": "production"})
	wantStatus(t, rec, http.StatusCreated)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("response with a secret must be Cache-Control: no-store")
	}
	key := decode[apiKeyResponse](t, rec)
	if !strings.HasPrefix(key.Key, "crt_") || !strings.HasPrefix(key.Key, key.Prefix) || key.Name != "production" {
		t.Fatalf("unexpected key %+v", key)
	}

	// The database stores the hash, never the key.
	var hash []byte
	if err := testPool.QueryRow(context.Background(),
		"SELECT hash FROM api_keys WHERE id = $1", key.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, crypto.HashAPIKey(key.Key)) {
		t.Error("stored hash is not the SHA-256 of the key")
	}

	// The key authenticates as its application.
	rec = c.do("GET", "/v1/me", key.Key, nil)
	wantStatus(t, rec, http.StatusOK)
	if me := decode[applicationResponse](t, rec); me.ID != app.ID {
		t.Errorf("/v1/me = %s, want %s", me.ID, app.ID)
	}

	// Listings show the key's metadata but never the key itself.
	rec = c.do("GET", "/v1/applications/"+app.ID.String()+"/api-keys", adminToken, nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), key.Key) {
		t.Error("listing exposes the full key")
	}
	keys := decode[listResponse[apiKeyResponse]](t, rec)
	if len(keys.Data) != 1 || keys.Data[0].LastUsedAt == nil || keys.Data[0].RevokedAt != nil {
		t.Errorf("listing = %+v, want one used, active key", keys.Data)
	}

	// Revocation is immediate and idempotent.
	revoke := "/v1/applications/" + app.ID.String() + "/api-keys/" + key.ID.String()
	wantStatus(t, c.do("DELETE", revoke, adminToken, nil), http.StatusNoContent)
	wantStatus(t, c.do("DELETE", revoke, adminToken, nil), http.StatusNoContent)
	wantProblem(t, c.do("GET", "/v1/me", key.Key, nil), http.StatusUnauthorized)

	keys = decode[listResponse[apiKeyResponse]](t, c.do("GET", "/v1/applications/"+app.ID.String()+"/api-keys", adminToken, nil))
	if keys.Data[0].RevokedAt == nil {
		t.Error("revoked key has no revoked_at")
	}
}

func TestAPIKeysAreScopedToTheirApplication(t *testing.T) {
	c := newClient(t)
	appA, appB := c.createApp("a"), c.createApp("b")
	keyA := c.createKey(appA.ID)

	// Revoking A's key through B's URL does not find it.
	wantProblem(t, c.do("DELETE", "/v1/applications/"+appB.ID.String()+"/api-keys/"+keyA.ID.String(), adminToken, nil),
		http.StatusNotFound)
	wantStatus(t, c.do("GET", "/v1/me", keyA.Key, nil), http.StatusOK)

	missing := "/v1/applications/" + uuid.NewString() + "/api-keys"
	wantProblem(t, c.do("POST", missing, adminToken, map[string]string{}), http.StatusNotFound)
	wantProblem(t, c.do("GET", missing, adminToken, nil), http.StatusNotFound)
}

func TestProducerAuthentication(t *testing.T) {
	c := newClient(t)
	for _, token := range []string{"", "crt_doesnotexist", adminToken} {
		wantProblem(t, c.do("GET", "/v1/me", token, nil), http.StatusUnauthorized)
	}
}
