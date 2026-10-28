package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// producer creates an application and returns an API key for it.
func (c *testClient) producer() string {
	c.t.Helper()
	return c.createKey(c.createApp("acme").ID).Key
}

func (c *testClient) createEndpoint(key string, body map[string]any) endpointResponse {
	c.t.Helper()
	rec := c.do("POST", "/v1/endpoints", key, body)
	wantStatus(c.t, rec, http.StatusCreated)
	return decode[endpointResponse](c.t, rec)
}

// storedSecret decrypts the endpoint's current secret straight from the
// database, as the delivery worker will.
func storedSecret(t *testing.T, endpointID uuid.UUID) []byte {
	t.Helper()
	var ct []byte
	err := testPool.QueryRow(context.Background(),
		`SELECT secret_ciphertext FROM endpoint_secrets WHERE endpoint_id = $1 AND expires_at IS NULL`,
		endpointID).Scan(&ct)
	if err != nil {
		t.Fatalf("load secret: %v", err)
	}
	secret, err := testCipher(t).Decrypt(ct, endpointID[:])
	if err != nil {
		t.Fatalf("decrypt secret: %v", err)
	}
	return secret
}

func TestCreateEndpoint(t *testing.T) {
	c := newClient(t)
	key := c.producer()

	rec := c.do("POST", "/v1/endpoints", key, map[string]any{
		"url":         "https://Example.com/webhooks?source=carteiro",
		"description": "  billing service  ",
		"event_types": []string{"invoice.paid", "invoice.failed", "invoice.paid"},
	})
	wantStatus(t, rec, http.StatusCreated)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("response with a secret must be Cache-Control: no-store")
	}
	ep := decode[endpointResponse](t, rec)

	if ep.URL != "https://Example.com/webhooks?source=carteiro" || ep.Description != "billing service" {
		t.Errorf("unexpected endpoint %+v", ep)
	}
	if !slices.Equal(ep.EventTypes, []string{"invoice.paid", "invoice.failed"}) {
		t.Errorf("event_types = %v, want duplicates removed in order", ep.EventTypes)
	}
	if ep.Status != "active" || ep.DisabledReason != nil {
		t.Errorf("new endpoint is %s (%v), want active", ep.Status, ep.DisabledReason)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/endpoints/"+ep.ID.String() {
		t.Errorf("Location = %q", loc)
	}

	// The returned secret follows Standard Webhooks and is what the database
	// holds, encrypted.
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ep.Secret, "whsec_"))
	if !strings.HasPrefix(ep.Secret, "whsec_") || err != nil || len(raw) != 32 {
		t.Fatalf("secret %q is not whsec_ + base64 of 32 bytes", ep.Secret)
	}
	if !bytes.Equal(storedSecret(t, ep.ID), raw) {
		t.Error("stored secret does not decrypt to the returned one")
	}
	var ct []byte
	_ = testPool.QueryRow(context.Background(),
		"SELECT secret_ciphertext FROM endpoint_secrets WHERE endpoint_id = $1", ep.ID).Scan(&ct)
	if bytes.Contains(ct, raw) {
		t.Error("secret stored in clear")
	}

	// The secret is never shown again.
	rec = c.do("GET", "/v1/endpoints/"+ep.ID.String(), key, nil)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "whsec_") {
		t.Error("GET exposes the secret")
	}
}

func TestCreateEndpointWithoutEventTypesReceivesAll(t *testing.T) {
	c := newClient(t)
	ep := c.createEndpoint(c.producer(), map[string]any{"url": "https://example.com/hook"})
	if ep.EventTypes == nil || len(ep.EventTypes) != 0 {
		t.Errorf("event_types = %#v, want an empty array", ep.EventTypes)
	}
}

func TestEndpointURLValidation(t *testing.T) {
	c := newClient(t)
	key := c.producer()

	invalid := []struct{ name, url string }{
		{"missing", ""},
		{"relative", "/webhooks"},
		{"no host", "https:///webhooks"},
		{"opaque", "https:example.com"},
		{"plain http", "http://example.com/hook"},
		{"other scheme", "ftp://example.com/hook"},
		{"javascript", "javascript:alert(1)"},
		{"credentials", "https://user:pass@example.com/hook"},
		{"fragment", "https://example.com/hook#section"},
		{"too long", "https://example.com/" + strings.Repeat("a", maxURLLen)},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			p := wantProblem(t, c.do("POST", "/v1/endpoints", key, map[string]any{"url": tt.url}), http.StatusUnprocessableEntity)
			if len(p.Errors) != 1 || p.Errors[0].Field != "url" {
				t.Errorf("errors = %+v, want one on url", p.Errors)
			}
		})
	}

	t.Run("http allowed by configuration", func(t *testing.T) {
		dev := newClientWith(t, func(o *Options) { o.AllowHTTPEndpoints = true })
		dev.createEndpoint(dev.producer(), map[string]any{"url": "http://localhost:9000/hook"})
	})
}

func TestEndpointFieldValidation(t *testing.T) {
	c := newClient(t)
	key := c.producer()

	tooMany := make([]string, maxEventTypes+1)
	for i := range tooMany {
		tooMany[i] = "type" + strings.Repeat("x", i%5)
	}

	tests := []struct {
		name   string
		body   map[string]any
		fields []string
	}{
		{"bad event type", map[string]any{"url": "https://example.com", "event_types": []string{"ok.type", "has space", ".leading"}},
			[]string{"event_types[1]", "event_types[2]"}},
		{"too many event types", map[string]any{"url": "https://example.com", "event_types": tooMany}, []string{"event_types"}},
		{"description too long", map[string]any{"url": "https://example.com", "description": strings.Repeat("d", maxDescriptionLen+1)},
			[]string{"description"}},
		{"every field reported", map[string]any{"url": "ftp://x", "description": strings.Repeat("d", maxDescriptionLen+1), "event_types": []string{""}},
			[]string{"url", "description", "event_types[0]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := wantProblem(t, c.do("POST", "/v1/endpoints", key, tt.body), http.StatusUnprocessableEntity)
			var got []string
			for _, e := range p.Errors {
				got = append(got, e.Field)
			}
			if !slices.Equal(got, tt.fields) {
				t.Errorf("error fields = %v, want %v", got, tt.fields)
			}
		})
	}

	var n int
	_ = testPool.QueryRow(context.Background(), "SELECT count(*) FROM endpoints").Scan(&n)
	if n != 0 {
		t.Errorf("invalid requests created %d endpoints", n)
	}
}

func TestUpdateEndpoint(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	ep := c.createEndpoint(key, map[string]any{
		"url": "https://example.com/v1", "description": "old", "event_types": []string{"a.b"},
	})
	path := "/v1/endpoints/" + ep.ID.String()

	// Only the fields sent change.
	rec := c.do("PATCH", path, key, map[string]any{"description": "new"})
	wantStatus(t, rec, http.StatusOK)
	got := decode[endpointResponse](t, rec)
	if got.Description != "new" || got.URL != ep.URL || !slices.Equal(got.EventTypes, ep.EventTypes) {
		t.Errorf("partial update changed other fields: %+v", got)
	}
	if !got.UpdatedAt.After(ep.UpdatedAt) {
		t.Error("updated_at did not move forward")
	}

	// An empty array is a real change: subscribe to everything.
	got = decode[endpointResponse](t, c.do("PATCH", path, key, map[string]any{"event_types": []string{}}))
	if got.EventTypes == nil || len(got.EventTypes) != 0 {
		t.Errorf("event_types = %#v, want []", got.EventTypes)
	}

	// Invalid input changes nothing.
	wantProblem(t, c.do("PATCH", path, key, map[string]any{"url": "http://example.com", "description": "x"}), http.StatusUnprocessableEntity)
	got = decode[endpointResponse](t, c.do("GET", path, key, nil))
	if got.URL != ep.URL || got.Description != "new" {
		t.Errorf("rejected update was applied: %+v", got)
	}

	// The secret survives updates.
	before := storedSecret(t, ep.ID)
	c.do("PATCH", path, key, map[string]any{"url": "https://example.com/v2"})
	if !bytes.Equal(storedSecret(t, ep.ID), before) {
		t.Error("update changed the secret")
	}
}

func TestDeleteEndpoint(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	ep := c.createEndpoint(key, map[string]any{"url": "https://example.com"})
	path := "/v1/endpoints/" + ep.ID.String()

	wantStatus(t, c.do("DELETE", path, key, nil), http.StatusNoContent)
	wantProblem(t, c.do("GET", path, key, nil), http.StatusNotFound)
	wantProblem(t, c.do("DELETE", path, key, nil), http.StatusNotFound)

	var n int
	_ = testPool.QueryRow(context.Background(), "SELECT count(*) FROM endpoint_secrets WHERE endpoint_id = $1", ep.ID).Scan(&n)
	if n != 0 {
		t.Error("deleting the endpoint left its secret behind")
	}
}

func TestListEndpointsPagination(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	for range 5 {
		c.createEndpoint(key, map[string]any{"url": "https://example.com"})
	}

	first := decode[listResponse[endpointResponse]](t, c.do("GET", "/v1/endpoints?limit=3", key, nil))
	if len(first.Data) != 3 || first.NextCursor == nil {
		t.Fatalf("first page: %d items, cursor %v", len(first.Data), first.NextCursor)
	}
	second := decode[listResponse[endpointResponse]](t, c.do("GET", "/v1/endpoints?limit=3&cursor="+*first.NextCursor, key, nil))
	if len(second.Data) != 2 || second.NextCursor != nil {
		t.Errorf("second page: %d items, cursor %v", len(second.Data), second.NextCursor)
	}
}

// TestEndpointsAreScopedToTheirApplication is FR-03: an application cannot
// see or change another application's endpoints, and cannot even tell that
// they exist.
func TestEndpointsAreScopedToTheirApplication(t *testing.T) {
	c := newClient(t)
	keyA := c.createKey(c.createApp("a").ID).Key
	keyB := c.createKey(c.createApp("b").ID).Key
	ep := c.createEndpoint(keyA, map[string]any{"url": "https://a.example.com"})
	path := "/v1/endpoints/" + ep.ID.String()

	wantProblem(t, c.do("GET", path, keyB, nil), http.StatusNotFound)
	wantProblem(t, c.do("PATCH", path, keyB, map[string]any{"url": "https://evil.example.com"}), http.StatusNotFound)
	wantProblem(t, c.do("DELETE", path, keyB, nil), http.StatusNotFound)

	list := decode[listResponse[endpointResponse]](t, c.do("GET", "/v1/endpoints", keyB, nil))
	if len(list.Data) != 0 {
		t.Errorf("application B lists %d endpoints of A", len(list.Data))
	}
	if got := decode[endpointResponse](t, c.do("GET", path, keyA, nil)); got.URL != "https://a.example.com" {
		t.Errorf("A's endpoint was changed by B: %+v", got)
	}
}

func TestEndpointsRequireAPIKey(t *testing.T) {
	c := newClient(t)
	for _, token := range []string{"", adminToken} {
		wantProblem(t, c.do("GET", "/v1/endpoints", token, nil), http.StatusUnauthorized)
	}
}
