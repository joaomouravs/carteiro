package store_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/joaomouravs/carteiro/internal/store"
	"github.com/joaomouravs/carteiro/internal/testdb"
)

const testLease = 30 * time.Second

// queueFixture is one endpoint with one event, ready for deliveries.
type queueFixture struct {
	endpointID uuid.UUID
	eventID    uuid.UUID
}

func newQueueFixture(t *testing.T) queueFixture {
	t.Helper()
	testdb.Reset(t, testPool)
	ctx := context.Background()
	var f queueFixture
	err := testPool.QueryRow(ctx, `
		WITH app AS (INSERT INTO applications (name) VALUES ('queue') RETURNING id),
		     ep  AS (INSERT INTO endpoints (application_id, url) SELECT id, 'https://example.com/hook' FROM app RETURNING id),
		     ev  AS (INSERT INTO events (application_id, event_type, payload) SELECT id, 'invoice.paid', '{"n": 1}' FROM app RETURNING id)
		SELECT ep.id, ev.id FROM ep, ev`).Scan(&f.endpointID, &f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// addDeliveries inserts n deliveries due at now() + dueIn, each for a new
// event (a delivery is unique per event and endpoint).
func (f queueFixture) addDeliveries(t *testing.T, n int, dueIn time.Duration) []uuid.UUID {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		WITH evs AS (
			INSERT INTO events (application_id, event_type, payload)
			SELECT application_id, 'invoice.paid', '{}' FROM endpoints, generate_series(1, $2)
			WHERE endpoints.id = $1
			RETURNING id
		)
		INSERT INTO deliveries (event_id, endpoint_id, next_attempt_at)
		SELECT evs.id, $1, now() + $3 * interval '1 millisecond' FROM evs
		RETURNING id`, f.endpointID, n, dueIn.Milliseconds())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := collectIDs(rows)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func collectIDs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]uuid.UUID, error) {
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type deliveryState struct {
	Status        string
	AttemptCount  int32
	NextAttemptAt *time.Time
	LeaseExpires  *time.Time
	DBNow         time.Time
}

func getDelivery(t *testing.T, id uuid.UUID) deliveryState {
	t.Helper()
	var s deliveryState
	err := testPool.QueryRow(context.Background(),
		`SELECT status, attempt_count, next_attempt_at, lease_expires_at, now() FROM deliveries WHERE id = $1`, id,
	).Scan(&s.Status, &s.AttemptCount, &s.NextAttemptAt, &s.LeaseExpires, &s.DBNow)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func claim(t *testing.T, q *store.Queries, limit int32) []store.ClaimDueDeliveriesRow {
	t.Helper()
	rows, err := q.ClaimDueDeliveries(context.Background(), store.ClaimDueDeliveriesParams{
		Limit: limit, LeaseMs: testLease.Milliseconds(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestClaimTakesOnlyDueDeliveries(t *testing.T) {
	q := requireDB(t)
	f := newQueueFixture(t)
	due := f.addDeliveries(t, 3, -time.Second)
	f.addDeliveries(t, 2, time.Hour)

	got := claim(t, q, 10)
	if len(got) != 3 {
		t.Fatalf("claimed %d deliveries, want the 3 due ones", len(got))
	}
	for _, row := range got {
		if !slices.Contains(due, row.ID) {
			t.Errorf("claimed %s, which is not due", row.ID)
		}
		if row.AttemptCount != 1 {
			t.Errorf("attempt_count = %d, want 1", row.AttemptCount)
		}
		s := getDelivery(t, row.ID)
		if s.Status != "in_flight" || s.LeaseExpires == nil || s.LeaseExpires.Sub(s.DBNow) < testLease-time.Second {
			t.Errorf("after claim: %+v, want in_flight with a %s lease", s, testLease)
		}
	}
	if again := claim(t, q, 10); len(again) != 0 {
		t.Errorf("second claim took %d leased deliveries, want 0", len(again))
	}
}

func TestClaimRespectsLimitOldestFirst(t *testing.T) {
	q := requireDB(t)
	f := newQueueFixture(t)
	older := f.addDeliveries(t, 2, -time.Minute)
	f.addDeliveries(t, 3, -time.Second)

	got := claim(t, q, 2)
	if len(got) != 2 {
		t.Fatalf("claimed %d, want the limit of 2", len(got))
	}
	for _, row := range got {
		if !slices.Contains(older, row.ID) {
			t.Errorf("claimed %s before an older due delivery", row.ID)
		}
	}
}

// TestConcurrentClaimsNeverOverlap runs workers against one queue at the same
// time: every delivery must be claimed exactly once. FOR UPDATE SKIP LOCKED
// is what makes this hold; without it, two workers could select the same
// rows before either updates them.
func TestConcurrentClaimsNeverOverlap(t *testing.T) {
	q := requireDB(t)
	f := newQueueFixture(t)
	const total, workers = 200, 8
	want := f.addDeliveries(t, total, -time.Second)

	var mu sync.Mutex
	var claimed []uuid.UUID
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				rows, err := q.ClaimDueDeliveries(context.Background(), store.ClaimDueDeliveriesParams{
					Limit: 7, LeaseMs: testLease.Milliseconds(),
				})
				if err != nil {
					t.Error(err)
					return
				}
				if len(rows) == 0 {
					return
				}
				mu.Lock()
				for _, r := range rows {
					claimed = append(claimed, r.ID)
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	slices.SortFunc(claimed, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	slices.SortFunc(want, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	if len(claimed) != total || !slices.Equal(claimed, want) {
		dup := len(claimed) - len(slices.Compact(slices.Clone(claimed)))
		t.Errorf("claimed %d deliveries (%d duplicates), want each of %d exactly once", len(claimed), dup, total)
	}
}

// TestFencingDiscardsStaleResults plays out ADR 0003: worker A claims a
// delivery, stalls past its lease, worker B reclaims it, and then A wakes up
// and tries to record its result.
func TestFencingDiscardsStaleResults(t *testing.T) {
	q := requireDB(t)
	ctx := context.Background()
	f := newQueueFixture(t)
	id := f.addDeliveries(t, 1, -time.Second)[0]

	a := claim(t, q, 1)[0] // worker A, token 1

	// A stalls; its lease runs out.
	if _, err := testPool.Exec(ctx, `UPDATE deliveries SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if got := claim(t, q, 1); len(got) != 0 {
		t.Fatal("an in-flight delivery was claimed as due")
	}
	reclaimed, err := q.ReclaimExpiredDeliveries(ctx, store.ReclaimExpiredDeliveriesParams{Limit: 10, LeaseMs: testLease.Milliseconds()})
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != id || reclaimed[0].AttemptCount != 2 {
		t.Fatalf("reclaimed %+v, want the delivery with token 2", reclaimed)
	}
	b := reclaimed[0] // worker B, token 2

	// A finishes late and reports success: fenced out.
	n, err := q.CompleteDelivery(ctx, store.CompleteDeliveryParams{ID: id, AttemptCount: a.AttemptCount, Status: "succeeded"})
	if err != nil || n != 0 {
		t.Fatalf("stale CompleteDelivery updated %d rows (err %v), want 0", n, err)
	}
	if s := getDelivery(t, id); s.Status != "in_flight" {
		t.Fatalf("stale result changed the status to %s", s.Status)
	}

	// B's result, with the current token, is recorded.
	retryIn := int64(90_000)
	n, err = q.CompleteDelivery(ctx, store.CompleteDeliveryParams{ID: id, AttemptCount: b.AttemptCount, Status: "pending", RetryInMs: &retryIn})
	if err != nil || n != 1 {
		t.Fatalf("current CompleteDelivery updated %d rows (err %v), want 1", n, err)
	}
	s := getDelivery(t, id)
	if s.Status != "pending" || s.LeaseExpires != nil || s.NextAttemptAt == nil {
		t.Fatalf("after retry: %+v, want pending, no lease, a next attempt", s)
	}
	if d := s.NextAttemptAt.Sub(s.DBNow); d < 89*time.Second || d > 91*time.Second {
		t.Errorf("next attempt in %s, want about 90s", d)
	}
}

func TestReclaimLeavesLiveLeasesAlone(t *testing.T) {
	q := requireDB(t)
	f := newQueueFixture(t)
	f.addDeliveries(t, 1, -time.Second)
	claim(t, q, 1)

	got, err := q.ReclaimExpiredDeliveries(context.Background(), store.ReclaimExpiredDeliveriesParams{Limit: 10, LeaseMs: testLease.Milliseconds()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("reclaimed %d deliveries whose lease is still valid", len(got))
	}
}

func TestCompleteDeliveryFinalStatusClearsSchedule(t *testing.T) {
	q := requireDB(t)
	f := newQueueFixture(t)
	f.addDeliveries(t, 2, -time.Second)
	rows := claim(t, q, 2)

	for i, status := range []string{"succeeded", "dead"} {
		n, err := q.CompleteDelivery(context.Background(), store.CompleteDeliveryParams{
			ID: rows[i].ID, AttemptCount: rows[i].AttemptCount, Status: status,
		})
		if err != nil || n != 1 {
			t.Fatalf("CompleteDelivery(%s) = %d, %v", status, n, err)
		}
		if s := getDelivery(t, rows[i].ID); s.Status != status || s.NextAttemptAt != nil || s.LeaseExpires != nil {
			t.Errorf("after %s: %+v, want no schedule and no lease", status, s)
		}
	}
}

func TestGetDeliveryJobsReturnsValidSecretsNewestFirst(t *testing.T) {
	q := requireDB(t)
	ctx := context.Background()
	f := newQueueFixture(t)
	id := f.addDeliveries(t, 1, -time.Second)[0]

	// A rotation in progress: the current secret, the previous one still in
	// its grace period, and an older one whose grace period is over.
	_, err := testPool.Exec(ctx, `
		INSERT INTO endpoint_secrets (endpoint_id, secret_ciphertext, created_at, expires_at) VALUES
			($1, 'expired',  now() - interval '3 days', now() - interval '1 day'),
			($1, 'previous', now() - interval '2 days', now() + interval '1 hour'),
			($1, 'current',  now() - interval '1 hour', NULL)`, f.endpointID)
	if err != nil {
		t.Fatal(err)
	}

	jobs, err := q.GetDeliveryJobs(ctx, []uuid.UUID{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want 1", len(jobs))
	}
	j := jobs[0]
	var secrets []string
	for _, s := range j.SecretCiphertexts {
		secrets = append(secrets, string(s))
	}
	if !slices.Equal(secrets, []string{"current", "previous"}) {
		t.Errorf("secrets = %q, want [current previous]", secrets)
	}
	if j.URL != "https://example.com/hook" || j.EventType != "invoice.paid" || j.EndpointID != f.endpointID {
		t.Errorf("job = %+v", j)
	}
}
