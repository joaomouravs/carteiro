package retention

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/testdb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) { testdb.Run(m, &testPool) }

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

const retention = 30 * 24 * time.Hour

type fixture struct {
	t          *testing.T
	appID      uuid.UUID
	endpointID uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	testdb.Require(t, testPool)
	testdb.Reset(t, testPool)
	f := &fixture{t: t}
	err := testPool.QueryRow(context.Background(), `
		WITH app AS (INSERT INTO applications (name) VALUES ('retention') RETURNING id)
		INSERT INTO endpoints (application_id, url) SELECT id, 'https://example.com' FROM app
		RETURNING application_id, id`).Scan(&f.appID, &f.endpointID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := testPool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatal(err)
	}
}

// event creates an event aged age, with one delivery in status and one
// attempt made when the event was created.
func (f *fixture) event(age time.Duration, status string) uuid.UUID {
	f.t.Helper()
	var id uuid.UUID
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO events (application_id, event_type, payload, created_at)
		VALUES ($1, 'invoice.paid', '{}', now() - $2 * interval '1 millisecond') RETURNING id`,
		f.appID, age.Milliseconds()).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	lease := "NULL"
	if status == "in_flight" {
		lease = "now() + interval '1 minute'"
	}
	f.exec(`
		WITH d AS (
			INSERT INTO deliveries (event_id, endpoint_id, status, attempt_count, next_attempt_at, lease_expires_at)
			VALUES ($1, $2, $3, 1, CASE WHEN $3 = 'pending' THEN now() END, `+lease+`)
			RETURNING id
		)
		INSERT INTO attempts (delivery_id, attempt_number, started_at, duration_ms, response_status)
		SELECT id, 1, now() - $4 * interval '1 millisecond', 10, 500 FROM d`,
		id, f.endpointID, status, age.Milliseconds())
	return id
}

func (f *fixture) exists(table string, id uuid.UUID) bool {
	f.t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		"SELECT count(*) FROM "+table+" WHERE id = $1", id).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n == 1
}

func (f *fixture) count(table string) int {
	f.t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestPurgeDeletesOnlyOldFinishedEvents(t *testing.T) {
	f := newFixture(t)
	const day = 24 * time.Hour
	oldDead := f.event(31*day, "dead")
	oldSucceeded := f.event(40*day, "succeeded")
	oldPending := f.event(31*day, "pending")
	oldInFlight := f.event(31*day, "in_flight")
	recent := f.event(29*day, "dead")

	res, err := New(discard, testPool, retention).Purge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Events != 2 {
		t.Errorf("purged %d events, want the 2 old finished ones", res.Events)
	}
	for id, want := range map[uuid.UUID]bool{
		oldDead: false, oldSucceeded: false, oldPending: true, oldInFlight: true, recent: true,
	} {
		if got := f.exists("events", id); got != want {
			t.Errorf("event %s exists = %v, want %v", id, got, want)
		}
	}
	// The cascade took the deleted events' deliveries; the kept events
	// keep theirs.
	if n := f.count("deliveries"); n != 3 {
		t.Errorf("%d deliveries left, want the 3 of the kept events", n)
	}
	// The kept old events' attempts are old too, and go on their own.
	if n := f.count("attempts"); n != 1 || res.Attempts != 2 {
		t.Errorf("%d attempts left (%d purged directly), want only the recent event's", n, res.Attempts)
	}
}

func TestPurgeIsBatched(t *testing.T) {
	f := newFixture(t)
	for range 7 {
		f.event(60*24*time.Hour, "dead")
	}
	p := New(discard, testPool, retention)
	p.batch = 3 // 7 rows take 3 batches: 3, 3 and 1

	res, err := p.Purge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Events != 7 || f.count("events") != 0 {
		t.Errorf("purged %d events, %d left; want all 7 across batches", res.Events, f.count("events"))
	}
}

func TestPurgeDeletesExpiredSecrets(t *testing.T) {
	f := newFixture(t)
	f.exec(`
		INSERT INTO endpoint_secrets (endpoint_id, secret_ciphertext, expires_at) VALUES
			($1, 'current', NULL),
			($1, 'in grace', now() + interval '1 hour'),
			($1, 'expired', now() - interval '1 second')`, f.endpointID)

	res, err := New(discard, testPool, retention).Purge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Secrets != 1 || f.count("endpoint_secrets") != 2 {
		t.Errorf("purged %d secrets, %d left; want only the expired one gone", res.Secrets, f.count("endpoint_secrets"))
	}
}

// TestOnlyOneProcessPurgesAtATime holds the purge lock from another session,
// as a second worker in the middle of a purge would.
func TestOnlyOneProcessPurgesAtATime(t *testing.T) {
	f := newFixture(t)
	old := f.event(60*24*time.Hour, "dead")
	ctx := context.Background()

	other, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, `SELECT pg_advisory_lock(483006768997)`); err != nil {
		t.Fatal(err)
	}
	p := New(discard, testPool, retention)
	res, err := p.Purge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || !f.exists("events", old) {
		t.Errorf("result = %+v, want the round skipped while another session holds the lock", res)
	}

	if _, err := other.Exec(ctx, `SELECT pg_advisory_unlock(483006768997)`); err != nil {
		t.Fatal(err)
	}
	other.Release()
	if res, err := p.Purge(ctx); err != nil || res.Skipped || f.exists("events", old) {
		t.Errorf("after the lock is free: %+v, %v; want the purge done", res, err)
	}
	// The purge released its own lock: taking it again succeeds.
	if res, err := p.Purge(ctx); err != nil || res.Skipped {
		t.Errorf("third round: %+v, %v; want the lock free again", res, err)
	}
}
