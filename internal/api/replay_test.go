package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

// replayFixture is one endpoint and one event delivered to it.
type replayFixture struct {
	c          *testClient
	key        string
	endpointID uuid.UUID
	deliveryID uuid.UUID
}

func newReplayFixture(t *testing.T) replayFixture {
	t.Helper()
	c := newClient(t)
	key := c.producer()
	ep := c.createEndpoint(key, map[string]any{"url": "https://example.com/hook"})
	return replayFixture{c: c, key: key, endpointID: ep.ID, deliveryID: newDelivery(t, c, key)}
}

// newDelivery publishes an event and returns its only delivery.
func newDelivery(t *testing.T, c *testClient, key string) uuid.UUID {
	t.Helper()
	ev := c.publishEvent(key, "invoice.paid")
	list := decode[listResponse[deliveryResponse]](t, c.do("GET", "/v1/events/"+ev.ID.String()+"/deliveries", key, nil))
	if len(list.Data) != 1 {
		t.Fatalf("got %d deliveries, want 1", len(list.Data))
	}
	return list.Data[0].ID
}

func exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestReplayDeadDelivery(t *testing.T) {
	f := newReplayFixture(t)
	recordAttempts(t, f.deliveryID, 12, "dead")
	path := "/v1/deliveries/" + f.deliveryID.String() + "/replay"

	rec := f.c.do("POST", path, f.key, nil)
	wantStatus(t, rec, http.StatusAccepted)
	d := decode[deliveryResponse](t, rec)
	if d.Status != "pending" || d.NextAttemptAt == nil || time.Until(*d.NextAttemptAt) > time.Second || d.AttemptCount != 12 {
		t.Errorf("replayed delivery = %+v, want pending, due now, attempt count kept", d)
	}
	var replayedAt int
	if err := testPool.QueryRow(context.Background(),
		`SELECT replayed_at_attempt FROM deliveries WHERE id = $1`, f.deliveryID).Scan(&replayedAt); err != nil {
		t.Fatal(err)
	}
	if replayedAt != 12 {
		t.Errorf("replayed_at_attempt = %d, want 12, where the new schedule starts", replayedAt)
	}

	p := wantProblem(t, f.c.do("POST", path, f.key, nil), http.StatusConflict)
	if p.Detail != "the delivery is already queued" {
		t.Errorf("second replay: %q", p.Detail)
	}
}

func TestReplaySucceededDelivery(t *testing.T) {
	f := newReplayFixture(t)
	recordAttempts(t, f.deliveryID, 1, "succeeded")
	wantStatus(t, f.c.do("POST", "/v1/deliveries/"+f.deliveryID.String()+"/replay", f.key, nil), http.StatusAccepted)
}

func TestReplayNeedsAnActiveEndpoint(t *testing.T) {
	f := newReplayFixture(t)
	recordAttempts(t, f.deliveryID, 3, "dead")
	exec(t, `UPDATE endpoints SET status = 'disabled', disabled_reason = 'gone' WHERE id = $1`, f.endpointID)

	p := wantProblem(t, f.c.do("POST", "/v1/deliveries/"+f.deliveryID.String()+"/replay", f.key, nil), http.StatusConflict)
	if p.Detail != "the delivery's endpoint is disabled; enable it before replaying" {
		t.Errorf("detail = %q", p.Detail)
	}
	p = wantProblem(t, f.c.do("POST", "/v1/endpoints/"+f.endpointID.String()+"/replay", f.key,
		map[string]any{"since": "2026-01-01T00:00:00Z"}), http.StatusConflict)
	if p.Detail != "the endpoint is disabled; enable it before replaying" {
		t.Errorf("detail = %q", p.Detail)
	}
}

func TestReplayIsScopedToItsApplication(t *testing.T) {
	f := newReplayFixture(t)
	recordAttempts(t, f.deliveryID, 3, "dead")
	other := f.c.createKey(f.c.createApp("other").ID).Key

	wantProblem(t, f.c.do("POST", "/v1/deliveries/"+f.deliveryID.String()+"/replay", other, nil), http.StatusNotFound)
	wantProblem(t, f.c.do("POST", "/v1/endpoints/"+f.endpointID.String()+"/replay", other,
		map[string]any{"since": "2026-01-01T00:00:00Z"}), http.StatusNotFound)
}

// TestReplayEndpointSince replays only what died since the given time, and
// only dead deliveries.
func TestReplayEndpointSince(t *testing.T) {
	f := newReplayFixture(t)
	recent := []uuid.UUID{f.deliveryID, newDelivery(t, f.c, f.key)}
	old := newDelivery(t, f.c, f.key)
	succeeded := newDelivery(t, f.c, f.key)
	for _, id := range append(recent, old) {
		recordAttempts(t, id, 12, "dead")
	}
	recordAttempts(t, succeeded, 1, "succeeded")
	exec(t, `UPDATE deliveries SET created_at = now() - interval '3 days' WHERE id = $1`, old)

	since := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	rec := f.c.do("POST", "/v1/endpoints/"+f.endpointID.String()+"/replay", f.key, map[string]any{"since": since})
	wantStatus(t, rec, http.StatusAccepted)
	if got := decode[map[string]int](t, rec); got["replayed"] != 2 {
		t.Errorf("replayed = %v, want the 2 recent dead deliveries", got)
	}
	want := map[uuid.UUID]string{recent[0]: "pending", recent[1]: "pending", old: "dead", succeeded: "succeeded"}
	for id, status := range want {
		var got string
		if err := testPool.QueryRow(context.Background(), `SELECT status FROM deliveries WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != status {
			t.Errorf("delivery %s: status %s, want %s", id, got, status)
		}
	}
}

func TestReplayEndpointValidation(t *testing.T) {
	f := newReplayFixture(t)
	path := "/v1/endpoints/" + f.endpointID.String() + "/replay"
	for name, body := range map[string]map[string]any{
		"missing":   {},
		"malformed": {"since": "yesterday"},
		"future":    {"since": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
	} {
		t.Run(name, func(t *testing.T) {
			p := wantProblem(t, f.c.do("POST", path, f.key, body), http.StatusUnprocessableEntity)
			if len(p.Errors) != 1 || p.Errors[0].Field != "since" {
				t.Errorf("errors = %+v, want one on since", p.Errors)
			}
		})
	}
}
