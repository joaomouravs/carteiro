package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/crypto"
	"github.com/joaomouravs/carteiro/internal/netguard"
	"github.com/joaomouravs/carteiro/internal/signing"
	"github.com/joaomouravs/carteiro/internal/testdb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) { testdb.Run(m, &testPool) }

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// fixture is one endpoint, with a signing secret, pointing at a receiver.
type fixture struct {
	t          *testing.T
	w          *Worker
	secret     []byte
	appID      uuid.UUID
	endpointID uuid.UUID
}

// testOptions deliver to local test servers, quickly.
func testOptions() Options {
	return Options{
		Concurrency:  4,
		Timeout:      2 * time.Second,
		PollInterval: 10 * time.Millisecond,
		Backoff:      DefaultBackoff,
		NetPolicy:    netguard.Policy{AllowPrivateNetworks: true},
	}
}

func newFixture(t *testing.T, receiverURL string, opts Options) *fixture {
	t.Helper()
	testdb.Require(t, testPool)
	testdb.Reset(t, testPool)
	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{7}, crypto.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, w: New(discard, testPool, cipher, opts), secret: crypto.NewEndpointSecret()}
	ctx := context.Background()
	err = testPool.QueryRow(ctx, `
		WITH app AS (INSERT INTO applications (name) VALUES ('worker') RETURNING id)
		INSERT INTO endpoints (application_id, url) SELECT id, $1 FROM app RETURNING application_id, id`,
		receiverURL).Scan(&f.appID, &f.endpointID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = testPool.Exec(ctx, `INSERT INTO endpoint_secrets (endpoint_id, secret_ciphertext) VALUES ($1, $2)`,
		f.endpointID, cipher.Encrypt(f.secret, f.endpointID[:]))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// publish creates an event and its delivery, due now, and returns their ids.
func (f *fixture) publish(eventType, payload string) (eventID, deliveryID uuid.UUID) {
	f.t.Helper()
	err := testPool.QueryRow(context.Background(), `
		WITH ev AS (
			INSERT INTO events (application_id, event_type, payload) VALUES ($1, $2, $3) RETURNING id
		)
		INSERT INTO deliveries (event_id, endpoint_id) SELECT id, $4 FROM ev RETURNING event_id, id`,
		f.appID, eventType, payload, f.endpointID).Scan(&eventID, &deliveryID)
	if err != nil {
		f.t.Fatal(err)
	}
	return eventID, deliveryID
}

// runOnce claims and sends every due delivery synchronously.
func (f *fixture) runOnce() int {
	f.t.Helper()
	jobs, err := f.w.claim(context.Background(), 100)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, j := range jobs {
		f.w.deliver(context.Background(), j)
	}
	return len(jobs)
}

type deliveryRow struct {
	status       string
	attemptCount int32
	retryIn      *time.Duration // next_attempt_at - now(), if scheduled
}

func (f *fixture) delivery(id uuid.UUID) deliveryRow {
	f.t.Helper()
	var r deliveryRow
	var retryMs *float64
	err := testPool.QueryRow(context.Background(), `
		SELECT status, attempt_count, extract(epoch FROM next_attempt_at - now()) * 1000
		FROM deliveries WHERE id = $1`, id).Scan(&r.status, &r.attemptCount, &retryMs)
	if err != nil {
		f.t.Fatal(err)
	}
	if retryMs != nil {
		d := time.Duration(*retryMs) * time.Millisecond
		r.retryIn = &d
	}
	return r
}

type attemptRow struct {
	number         int32
	responseStatus *int32
	responseBody   *string
	err            *string
}

func (f *fixture) attempts(deliveryID uuid.UUID) []attemptRow {
	f.t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT attempt_number, response_status, response_body, error
		FROM attempts WHERE delivery_id = $1 ORDER BY attempt_number`, deliveryID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []attemptRow
	for rows.Next() {
		var a attemptRow
		if err := rows.Scan(&a.number, &a.responseStatus, &a.responseBody, &a.err); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func receiver(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestDeliversSignedWebhook(t *testing.T) {
	type received struct {
		header http.Header
		body   []byte
	}
	got := make(chan received, 1)
	srv := receiver(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{r.Header.Clone(), body}
		w.WriteHeader(http.StatusNoContent)
	})
	f := newFixture(t, srv.URL+"/hook", testOptions())
	eventID, deliveryID := f.publish("invoice.paid", `{"amount": 4200, "currency": "BRL"}`)

	if n := f.runOnce(); n != 1 {
		t.Fatalf("sent %d deliveries, want 1", n)
	}
	r := <-got

	// What a receiver does with an official library: verify, then parse.
	if err := signing.Verify(f.secret, r.header, r.body, time.Now(), signing.DefaultTolerance); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	if id := r.header.Get(signing.HeaderID); id != eventID.String() {
		t.Errorf("webhook-id = %q, want the event id %s", id, eventID)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var env struct {
		Type      string          `json:"type"`
		Timestamp time.Time       `json:"timestamp"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(r.body, &env); err != nil {
		t.Fatalf("body is not the envelope: %v (%s)", err, r.body)
	}
	if env.Type != "invoice.paid" || time.Since(env.Timestamp) > time.Minute {
		t.Errorf("envelope = %+v", env)
	}
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil || data["currency"] != "BRL" {
		t.Errorf("data = %s, want the published payload", env.Data)
	}

	if d := f.delivery(deliveryID); d.status != statusSucceeded || d.retryIn != nil {
		t.Errorf("delivery = %+v, want succeeded with nothing scheduled", d)
	}
	a := f.attempts(deliveryID)
	if len(a) != 1 || a[0].number != 1 || a[0].responseStatus == nil || *a[0].responseStatus != 204 || a[0].err != nil {
		t.Errorf("attempts = %+v, want one with status 204", a)
	}
}

func TestFailureSchedulesRetryWithBackoff(t *testing.T) {
	srv := receiver(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "database is down", http.StatusInternalServerError)
	})
	f := newFixture(t, srv.URL, testOptions())
	_, id := f.publish("invoice.paid", `{}`)
	f.runOnce()

	d := f.delivery(id)
	// First retry: equal jitter over a 30s ceiling, so 15s to 30s.
	if d.status != statusPending || d.retryIn == nil || *d.retryIn < 14*time.Second || *d.retryIn > 30*time.Second {
		t.Errorf("delivery = %+v, want pending, retried in 15s to 30s", d)
	}
	a := f.attempts(id)
	if len(a) != 1 || a[0].responseStatus == nil || *a[0].responseStatus != 500 ||
		a[0].responseBody == nil || !strings.Contains(*a[0].responseBody, "database is down") {
		t.Errorf("attempts = %+v, want status 500 with the response body", a)
	}
}

func TestRetryAfterIsHonored(t *testing.T) {
	srv := receiver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	f := newFixture(t, srv.URL, testOptions())
	_, id := f.publish("invoice.paid", `{}`)
	f.runOnce()

	if d := f.delivery(id); d.retryIn == nil || *d.retryIn < 59*time.Minute || *d.retryIn > time.Hour {
		t.Errorf("delivery = %+v, want the retry in about an hour, as asked", d)
	}
}

func TestLastFailedAttemptIsDead(t *testing.T) {
	srv := receiver(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	opts := testOptions()
	opts.Backoff = Backoff{Base: time.Millisecond, Cap: time.Millisecond, MaxAttempts: 3}
	f := newFixture(t, srv.URL, opts)
	_, id := f.publish("invoice.paid", `{}`)

	for range 3 {
		time.Sleep(5 * time.Millisecond) // let the 1ms retry come due
		f.runOnce()
	}
	if d := f.delivery(id); d.status != statusDead || d.attemptCount != 3 || d.retryIn != nil {
		t.Errorf("delivery = %+v, want dead after 3 attempts", d)
	}
	if a := f.attempts(id); len(a) != 3 {
		t.Errorf("logged %d attempts, want 3", len(a))
	}
	if n := f.runOnce(); n != 0 {
		t.Errorf("a dead delivery was claimed again")
	}
}

func TestRedirectIsAFailure(t *testing.T) {
	srv := receiver(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	})
	f := newFixture(t, srv.URL, testOptions())
	_, id := f.publish("invoice.paid", `{}`)
	f.runOnce()

	if d := f.delivery(id); d.status != statusPending {
		t.Errorf("delivery = %+v, want a retry: a redirect is not a success", d)
	}
	if a := f.attempts(id); len(a) != 1 || a[0].responseStatus == nil || *a[0].responseStatus != 302 {
		t.Errorf("attempts = %+v, want the 302 recorded", a)
	}
}

func TestBlockedAddressIsAFailedAttempt(t *testing.T) {
	var hit atomic.Bool
	srv := receiver(t, func(http.ResponseWriter, *http.Request) { hit.Store(true) })
	opts := testOptions()
	opts.NetPolicy = netguard.Policy{} // production: loopback is blocked
	f := newFixture(t, srv.URL, opts)
	_, id := f.publish("invoice.paid", `{}`)
	f.runOnce()

	if hit.Load() {
		t.Fatal("the request reached a blocked address")
	}
	a := f.attempts(id)
	if len(a) != 1 || a[0].responseStatus != nil || a[0].err == nil || !strings.Contains(*a[0].err, "blocked") {
		t.Errorf("attempts = %+v, want no response and a blocked error", a)
	}
}

func TestTimeoutIsAFailedAttempt(t *testing.T) {
	release := make(chan struct{})
	srv := receiver(t, func(http.ResponseWriter, *http.Request) { <-release })
	// Registered after receiver, so it runs before srv.Close (cleanups run
	// last in, first out): Close waits for the stuck handler to return.
	t.Cleanup(func() { close(release) })
	opts := testOptions()
	opts.Timeout = 200 * time.Millisecond
	f := newFixture(t, srv.URL, opts)
	_, id := f.publish("invoice.paid", `{}`)
	f.runOnce()

	a := f.attempts(id)
	if len(a) != 1 || a[0].responseStatus != nil || a[0].err == nil {
		t.Errorf("attempts = %+v, want a timeout error and no response", a)
	}
	if d := f.delivery(id); d.status != statusPending {
		t.Errorf("delivery = %+v, want a retry", d)
	}
}

// TestHostileResponseBodyIsStored sends a body PostgreSQL cannot store as is:
// NUL bytes and invalid UTF-8, longer than the 4 KiB kept. The attempt must
// still be recorded.
func TestHostileResponseBodyIsStored(t *testing.T) {
	srv := receiver(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad\x00request\xff\xfe"))
		_, _ = w.Write([]byte(strings.Repeat("ç", 10_000)))
	})
	f := newFixture(t, srv.URL, testOptions())
	_, id := f.publish("invoice.paid", `{}`)
	f.runOnce()

	a := f.attempts(id)
	if len(a) != 1 || a[0].responseBody == nil {
		t.Fatalf("attempts = %+v, want the attempt recorded", a)
	}
	body := *a[0].responseBody
	r := string(utf8.RuneError)
	if !strings.HasPrefix(body, "bad"+r+"request"+r) || len(body) > maxStoredBody || !utf8.ValidString(body) {
		t.Errorf("stored body (%d bytes) = %.40q..., want sanitized and at most %d bytes", len(body), body, maxStoredBody)
	}
}

// TestCrashedWorkerDeliveryIsRecovered plays a worker that claimed a delivery
// and died: nothing happens until the lease expires, then another worker
// reclaims it and delivers it as the next attempt.
func TestCrashedWorkerDeliveryIsRecovered(t *testing.T) {
	srv := receiver(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	f := newFixture(t, srv.URL, testOptions())
	_, id := f.publish("invoice.paid", `{}`)

	if _, err := f.w.claim(context.Background(), 1); err != nil { // claimed, then the worker "crashes"
		t.Fatal(err)
	}
	if n := f.runOnce(); n != 0 {
		t.Fatalf("claimed a delivery under a live lease")
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE deliveries SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if n := f.runOnce(); n != 1 {
		t.Fatalf("recovered %d deliveries, want 1", n)
	}
	if d := f.delivery(id); d.status != statusSucceeded || d.attemptCount != 2 {
		t.Errorf("delivery = %+v, want succeeded on attempt 2", d)
	}
	// Attempt 1 left no record: the worker died before writing it.
	if a := f.attempts(id); len(a) != 1 || a[0].number != 2 {
		t.Errorf("attempts = %+v, want only attempt 2", a)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countDeliveries(t *testing.T, status string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM deliveries WHERE status = $1`, status).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRunDrainsTheQueue runs the real loop over many deliveries: each must be
// delivered exactly once, and never more than Concurrency at a time.
func TestRunDrainsTheQueue(t *testing.T) {
	const total = 60
	var mu sync.Mutex
	seen := map[string]int{}
	var inFlight, peak atomic.Int32
	srv := receiver(t, func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		seen[r.Header.Get(signing.HeaderID)]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	opts := testOptions()
	f := newFixture(t, srv.URL, opts)
	for range total {
		f.publish("invoice.paid", `{}`)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.w.Run(ctx) }()
	waitFor(t, "every delivery to succeed", func() bool { return countDeliveries(t, statusSucceeded) == total })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != total {
		t.Errorf("receiver saw %d distinct webhooks, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("webhook %s delivered %d times", id, n)
		}
	}
	if p := peak.Load(); p > int32(opts.Concurrency) {
		t.Errorf("%d requests in flight at once, want at most %d", p, opts.Concurrency)
	}
}

// TestRunFinishesInFlightDeliveriesOnShutdown cancels Run while a request is
// in progress: Run must wait for it and record its result, not abandon it.
func TestRunFinishesInFlightDeliveriesOnShutdown(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	srv := receiver(t, func(w http.ResponseWriter, _ *http.Request) {
		close(arrived)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	f := newFixture(t, srv.URL, testOptions())
	_, id := f.publish("invoice.paid", `{}`)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.w.Run(ctx) }()
	<-arrived
	cancel()

	select {
	case err := <-done:
		t.Fatalf("Run returned (%v) while a delivery was still in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if d := f.delivery(id); d.status != statusSucceeded {
		t.Errorf("delivery = %+v, want the in-flight result recorded", d)
	}
}
