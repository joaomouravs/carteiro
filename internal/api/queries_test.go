package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// publishEvent publishes an event and returns it.
func (c *testClient) publishEvent(key, eventType string) eventResponse {
	c.t.Helper()
	rec := c.publish(key, "", `{"event_type":"`+eventType+`","payload":{"n":1}}`)
	wantStatus(c.t, rec, http.StatusAccepted)
	return decode[eventResponse](c.t, rec)
}

// recordAttempts plays the worker for a delivery: n failed attempts, then the
// given final status.
func recordAttempts(t *testing.T, deliveryID uuid.UUID, n int, status string) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		_, err := testPool.Exec(ctx, `
			INSERT INTO attempts (delivery_id, attempt_number, started_at, duration_ms, response_status, response_body)
			VALUES ($1, $2, now(), 120, 503, 'try later')`, deliveryID, i)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := testPool.Exec(ctx, `
		UPDATE deliveries SET status = $2, attempt_count = $3,
		       next_attempt_at = CASE WHEN $2 = 'pending' THEN now() + interval '1 minute' END
		WHERE id = $1`, deliveryID, status, n)
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueryEventDeliveriesAndAttempts(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	a := c.createEndpoint(key, map[string]any{"url": "https://a.example.com"})
	c.createEndpoint(key, map[string]any{"url": "https://b.example.com"})
	ev := c.publishEvent(key, "invoice.paid")

	got := decode[eventResponse](t, c.do("GET", "/v1/events/"+ev.ID.String(), key, nil))
	if got.ID != ev.ID || got.EventType != "invoice.paid" || string(got.Payload) != `{"n":1}` {
		t.Errorf("event = %+v", got)
	}

	deliveries := decode[listResponse[deliveryResponse]](t, c.do("GET", "/v1/events/"+ev.ID.String()+"/deliveries", key, nil))
	if len(deliveries.Data) != 2 {
		t.Fatalf("event has %d deliveries, want one per endpoint (2)", len(deliveries.Data))
	}
	var toA deliveryResponse
	for _, d := range deliveries.Data {
		if d.EventID != ev.ID || d.Status != "pending" || d.AttemptCount != 0 || d.NextAttemptAt == nil {
			t.Errorf("new delivery = %+v, want pending and due", d)
		}
		if d.EndpointID == a.ID {
			toA = d
		}
	}

	recordAttempts(t, toA.ID, 3, "dead")
	path := "/v1/deliveries/" + toA.ID.String()
	d := decode[deliveryResponse](t, c.do("GET", path, key, nil))
	if d.Status != "dead" || d.AttemptCount != 3 || d.NextAttemptAt != nil {
		t.Errorf("delivery = %+v, want dead after 3 attempts, nothing scheduled", d)
	}

	attempts := decode[listResponse[attemptResponse]](t, c.do("GET", path+"/attempts?limit=2", key, nil))
	if len(attempts.Data) != 2 || attempts.NextCursor == nil {
		t.Fatalf("first page: %d attempts, cursor %v; want 2 and a cursor", len(attempts.Data), attempts.NextCursor)
	}
	first := attempts.Data[0]
	if first.AttemptNumber != 3 || first.ResponseStatus == nil || *first.ResponseStatus != 503 ||
		first.ResponseBody == nil || *first.ResponseBody != "try later" || first.DurationMs != 120 {
		t.Errorf("newest attempt = %+v, want attempt 3 with the 503 response", first)
	}
	rest := decode[listResponse[attemptResponse]](t, c.do("GET", path+"/attempts?cursor="+*attempts.NextCursor, key, nil))
	if len(rest.Data) != 1 || rest.Data[0].AttemptNumber != 1 || rest.NextCursor != nil {
		t.Errorf("second page = %+v, want only attempt 1", rest)
	}
}

func TestInFlightDeliveryShowsNoSchedule(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	c.createEndpoint(key, map[string]any{"url": "https://example.com"})
	ev := c.publishEvent(key, "invoice.paid")
	if _, err := testPool.Exec(context.Background(), `
		UPDATE deliveries SET status = 'in_flight', attempt_count = 1, lease_expires_at = now() + interval '1 minute'
		WHERE event_id = $1`, ev.ID); err != nil {
		t.Fatal(err)
	}
	list := decode[listResponse[deliveryResponse]](t, c.do("GET", "/v1/events/"+ev.ID.String()+"/deliveries", key, nil))
	if len(list.Data) != 1 || list.Data[0].Status != "in_flight" || list.Data[0].NextAttemptAt != nil {
		t.Errorf("deliveries = %+v, want in_flight with no next_attempt_at", list.Data)
	}
}

func TestListEvents(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	var paid []uuid.UUID
	for range 3 {
		paid = append(paid, c.publishEvent(key, "invoice.paid").ID)
		c.publishEvent(key, "user.created")
	}

	all := decode[listResponse[eventResponse]](t, c.do("GET", "/v1/events", key, nil))
	if len(all.Data) != 6 || all.NextCursor != nil {
		t.Errorf("all events: %d, cursor %v; want 6 on one page", len(all.Data), all.NextCursor)
	}

	page := decode[listResponse[eventResponse]](t, c.do("GET", "/v1/events?event_type=invoice.paid&limit=2", key, nil))
	if len(page.Data) != 2 || page.NextCursor == nil || page.Data[0].ID != paid[2] || page.Data[1].ID != paid[1] {
		t.Fatalf("first page = %+v, want the 2 newest invoice.paid", page)
	}
	next := decode[listResponse[eventResponse]](t, c.do("GET", "/v1/events?event_type=invoice.paid&limit=2&cursor="+*page.NextCursor, key, nil))
	if len(next.Data) != 1 || next.Data[0].ID != paid[0] || next.NextCursor != nil {
		t.Errorf("second page = %+v, want the oldest invoice.paid", next)
	}

	p := wantProblem(t, c.do("GET", "/v1/events?event_type=has%20space", key, nil), http.StatusUnprocessableEntity)
	if len(p.Errors) != 1 || p.Errors[0].Field != "event_type" {
		t.Errorf("errors = %+v, want one on event_type", p.Errors)
	}
}

// TestQueriesAreScopedToTheirApplication is FR-03 for events, deliveries and
// attempts: another application gets 404, the same answer as for an id that
// does not exist, so it cannot even learn that the resource exists.
func TestQueriesAreScopedToTheirApplication(t *testing.T) {
	c := newClient(t)
	keyA := c.createKey(c.createApp("a").ID).Key
	keyB := c.createKey(c.createApp("b").ID).Key
	c.createEndpoint(keyA, map[string]any{"url": "https://a.example.com"})
	ev := c.publishEvent(keyA, "invoice.paid")
	deliveries := decode[listResponse[deliveryResponse]](t, c.do("GET", "/v1/events/"+ev.ID.String()+"/deliveries", keyA, nil))
	if len(deliveries.Data) != 1 {
		t.Fatalf("got %d deliveries, want 1", len(deliveries.Data))
	}
	deliveryID := deliveries.Data[0].ID.String()

	for _, path := range []string{
		"/v1/events/" + ev.ID.String(),
		"/v1/events/" + ev.ID.String() + "/deliveries",
		"/v1/deliveries/" + deliveryID,
		"/v1/deliveries/" + deliveryID + "/attempts",
	} {
		wantProblem(t, c.do("GET", path, keyB, nil), http.StatusNotFound)
	}
	if list := decode[listResponse[eventResponse]](t, c.do("GET", "/v1/events", keyB, nil)); len(list.Data) != 0 {
		t.Errorf("application B lists %d events of A", len(list.Data))
	}
}

func TestQueriesNotFound(t *testing.T) {
	c := newClient(t)
	key := c.producer()
	for _, path := range []string{
		"/v1/events/" + uuid.NewString(),
		"/v1/events/not-a-uuid",
		"/v1/events/" + uuid.NewString() + "/deliveries",
		"/v1/deliveries/" + uuid.NewString(),
		"/v1/deliveries/not-a-uuid/attempts",
	} {
		wantProblem(t, c.do("GET", path, key, nil), http.StatusNotFound)
	}
}

func TestQueriesRequireAPIKey(t *testing.T) {
	c := newClient(t)
	for _, path := range []string{"/v1/events", "/v1/events/" + uuid.NewString(), "/v1/deliveries/" + uuid.NewString()} {
		for _, token := range []string{"", adminToken} {
			wantProblem(t, c.do("GET", path, token, nil), http.StatusUnauthorized)
		}
	}
}
