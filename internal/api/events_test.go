package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// publish sends a raw JSON body to POST /v1/events, with an optional
// Idempotency-Key.
func (c *testClient) publish(key, idemKey, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest("POST", "/v1/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

// deliveredEndpoints returns the endpoints an event fanned out to, after
// checking every delivery starts pending, due now, with no attempts.
func deliveredEndpoints(t *testing.T, eventID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := testPool.Query(context.Background(),
		`SELECT endpoint_id, status, attempt_count, next_attempt_at FROM deliveries WHERE event_id = $1 ORDER BY endpoint_id`, eventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var (
			id       uuid.UUID
			status   string
			attempts int
			next     time.Time
		)
		if err := rows.Scan(&id, &status, &attempts, &next); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || attempts != 0 || time.Until(next) > 0 {
			t.Errorf("delivery to %s: status %s, attempts %d, next %v; want pending, 0, due", id, status, attempts, next)
		}
		ids = append(ids, id)
	}
	return ids
}

func countRows(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPublishEvent(t *testing.T) {
	c := newClient(t)
	key := c.producer()

	rec := c.publish(key, "", `{"event_type":"invoice.paid","payload":{"invoice":"in_1","amount":1999}}`)
	wantStatus(t, rec, http.StatusAccepted)
	ev := decode[eventResponse](t, rec)

	if ev.ID.Version() != 7 || ev.EventType != "invoice.paid" || ev.IdempotencyKey != nil {
		t.Errorf("unexpected event %+v", ev)
	}
	// jsonb sorted the keys; encoding/json compacts the RawMessage on output.
	if string(ev.Payload) != `{"amount":1999,"invoice":"in_1"}` {
		t.Errorf("payload = %s, want it as normalized by jsonb", ev.Payload)
	}
}

func TestPublishPreservesLargeNumbers(t *testing.T) {
	c := newClient(t)
	// 2^63 + 1 does not fit in an int64 and is not exact in a float64.
	rec := c.publish(c.producer(), "", `{"event_type":"t","payload":{"id":9223372036854775809,"price":0.1}}`)
	wantStatus(t, rec, http.StatusAccepted)
	if p := string(decode[eventResponse](t, rec).Payload); !strings.Contains(p, "9223372036854775809") || !strings.Contains(p, "0.1") {
		t.Errorf("payload = %s, numbers were altered", p)
	}
}

// TestFanOut is FR-12: one delivery per active endpoint of the application
// subscribed to the event type.
func TestFanOut(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	all := c.createEndpoint(key, map[string]any{"url": "https://all.example.com"})
	paid := c.createEndpoint(key, map[string]any{"url": "https://paid.example.com", "event_types": []string{"invoice.paid", "invoice.failed"}})
	c.createEndpoint(key, map[string]any{"url": "https://users.example.com", "event_types": []string{"user.created"}})
	disabled := c.createEndpoint(key, map[string]any{"url": "https://disabled.example.com"})
	if _, err := testPool.Exec(context.Background(),
		`UPDATE endpoints SET status = 'disabled', disabled_reason = 'test' WHERE id = $1`, disabled.ID); err != nil {
		t.Fatal(err)
	}
	other := c.createKey(c.createApp("other").ID).Key
	c.createEndpoint(other, map[string]any{"url": "https://other-app.example.com"})

	ev := decode[eventResponse](t, c.publish(key, "", `{"event_type":"invoice.paid","payload":{}}`))

	got := deliveredEndpoints(t, ev.ID)
	want := []uuid.UUID{all.ID, paid.ID}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(got, want) {
		t.Errorf("delivered to %v, want %v (all-types and invoice.paid endpoints only)", got, want)
	}
}

func TestPublishWithoutEndpoints(t *testing.T) {
	c := newClient(t)
	wantStatus(t, c.publish(c.producer(), "", `{"event_type":"t","payload":{}}`), http.StatusAccepted)
	if n := countRows(t, "deliveries"); n != 0 {
		t.Errorf("%d deliveries without endpoints", n)
	}
}

// TestIdempotency is FR-11.
func TestIdempotency(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	c.createEndpoint(key, map[string]any{"url": "https://example.com"})

	first := c.publish(key, "order-42", `{"event_type":"order.created","payload":{"id":42,"items":[1,2]}}`)
	wantStatus(t, first, http.StatusAccepted)
	original := decode[eventResponse](t, first)

	t.Run("retry with the same JSON returns the original", func(t *testing.T) {
		// Different key order and whitespace: the same JSON object.
		rec := c.publish(key, "order-42", `{ "payload": { "items": [1, 2], "id": 42 }, "event_type": "order.created" }`)
		wantStatus(t, rec, http.StatusOK)
		if rec.Header().Get("Idempotent-Replayed") != "true" {
			t.Error("replay without Idempotent-Replayed: true")
		}
		if got := decode[eventResponse](t, rec); got.ID != original.ID || !got.CreatedAt.Equal(original.CreatedAt) {
			t.Errorf("replay returned %+v, want the original %+v", got, original)
		}
	})

	t.Run("different payload is a conflict", func(t *testing.T) {
		wantProblem(t, c.publish(key, "order-42", `{"event_type":"order.created","payload":{"id":43}}`), http.StatusConflict)
	})
	t.Run("array order matters", func(t *testing.T) {
		wantProblem(t, c.publish(key, "order-42", `{"event_type":"order.created","payload":{"id":42,"items":[2,1]}}`), http.StatusConflict)
	})
	t.Run("different event type is a conflict", func(t *testing.T) {
		wantProblem(t, c.publish(key, "order-42", `{"event_type":"order.updated","payload":{"id":42,"items":[1,2]}}`), http.StatusConflict)
	})

	if events, deliveries := countRows(t, "events"), countRows(t, "deliveries"); events != 1 || deliveries != 1 {
		t.Errorf("after retries and conflicts: %d events, %d deliveries; want 1 and 1", events, deliveries)
	}

	t.Run("keys are per application", func(t *testing.T) {
		other := c.createKey(c.createApp("other").ID).Key
		wantStatus(t, c.publish(other, "order-42", `{"event_type":"x","payload":{}}`), http.StatusAccepted)
	})
	t.Run("without a key every request is a new event", func(t *testing.T) {
		before := countRows(t, "events")
		c.publish(key, "", `{"event_type":"t","payload":{}}`)
		c.publish(key, "", `{"event_type":"t","payload":{}}`)
		if n := countRows(t, "events") - before; n != 2 {
			t.Errorf("%d events, want 2", n)
		}
	})
}

// TestIdempotencyUnderConcurrency sends the same request many times at once,
// as a client retrying aggressively would. Exactly one must create the event;
// the others must see it as a replay, never as an error or a duplicate.
func TestIdempotencyUnderConcurrency(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	for range 3 {
		c.createEndpoint(key, map[string]any{"url": "https://example.com"})
	}

	const n = 20
	statuses := make([]int, n)
	ids := make([]uuid.UUID, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			req := httptest.NewRequest("POST", "/v1/events", strings.NewReader(`{"event_type":"t","payload":{"n":1}}`))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Idempotency-Key", "same-key")
			rec := httptest.NewRecorder()
			c.h.ServeHTTP(rec, req)
			statuses[i] = rec.Code
			if rec.Code < 300 {
				ids[i] = decode[eventResponse](t, rec).ID
			}
		})
	}
	close(start)
	wg.Wait()

	created := 0
	for i, s := range statuses {
		switch s {
		case http.StatusAccepted:
			created++
		case http.StatusOK:
		default:
			t.Errorf("request %d: status %d", i, s)
		}
		if ids[i] != ids[0] {
			t.Errorf("request %d got event %s, request 0 got %s", i, ids[i], ids[0])
		}
	}
	if created != 1 {
		t.Errorf("%d requests created the event, want exactly 1", created)
	}
	if events, deliveries := countRows(t, "events"), countRows(t, "deliveries"); events != 1 || deliveries != 3 {
		t.Errorf("%d events and %d deliveries, want 1 and 3", events, deliveries)
	}
}

func TestPublishValidation(t *testing.T) {
	c := newClient(t)
	key := c.producer()

	tests := []struct {
		name    string
		idemKey string
		body    string
		status  int
		field   string
	}{
		{"missing event type", "", `{"payload":{}}`, http.StatusUnprocessableEntity, "event_type"},
		{"bad event type", "", `{"event_type":"has space","payload":{}}`, http.StatusUnprocessableEntity, "event_type"},
		{"missing payload", "", `{"event_type":"t"}`, http.StatusUnprocessableEntity, "payload"},
		{"null payload", "", `{"event_type":"t","payload":null}`, http.StatusUnprocessableEntity, "payload"},
		{"array payload", "", `{"event_type":"t","payload":[1]}`, http.StatusUnprocessableEntity, "payload"},
		{"string payload", "", `{"event_type":"t","payload":"hi"}`, http.StatusUnprocessableEntity, "payload"},
		{"NUL character jsonb cannot store", "", `{"event_type":"t","payload":{"s":"a\u0000b"}}`, http.StatusUnprocessableEntity, "payload"},
		{"idempotency key too long", strings.Repeat("k", 256), `{"event_type":"t","payload":{}}`, http.StatusUnprocessableEntity, "Idempotency-Key"},
		{"malformed payload", "", `{"event_type":"t","payload":{"a":}}`, http.StatusBadRequest, ""},
		{"body over 256 KiB", "", `{"event_type":"t","payload":{"s":"` + strings.Repeat("x", maxEventBodyBytes) + `"}}`, http.StatusRequestEntityTooLarge, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := wantProblem(t, c.publish(key, tt.idemKey, tt.body), tt.status)
			if tt.field != "" && (len(p.Errors) != 1 || p.Errors[0].Field != tt.field) {
				t.Errorf("errors = %+v, want one on %q", p.Errors, tt.field)
			}
		})
	}
	if n := countRows(t, "events"); n != 0 {
		t.Errorf("invalid requests created %d events", n)
	}

	t.Run("payload between the default and the event limit is accepted", func(t *testing.T) {
		big := `{"event_type":"t","payload":{"s":"` + strings.Repeat("x", 200<<10) + `"}}`
		wantStatus(t, c.publish(key, "", big), http.StatusAccepted)
	})
}

func TestPublishRequiresAPIKey(t *testing.T) {
	c := newClient(t)
	wantProblem(t, c.publish(adminToken, "", `{"event_type":"t","payload":{}}`), http.StatusUnauthorized)
}
