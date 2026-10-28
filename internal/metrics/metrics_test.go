package metrics

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/store"
	"github.com/joaomouravs/carteiro/internal/testdb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) { testdb.Run(m, &testPool) }

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// scrape fetches the metrics as Prometheus would.
func scrape(t *testing.T, m *Metrics) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Code, rec.Body.String()
}

// value returns the sample of the series named series (with its labels, as
// written in the text format), or fails the test.
func value(t *testing.T, body, series string) float64 {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(series) + ` (\S+)$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("series %s not found in:\n%s", series, body)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeliveryMetrics(t *testing.T) {
	m := New()
	_, body := scrape(t, m)
	// Every outcome is exported at zero before it first happens, so that
	// rate() over it works from the start.
	for _, o := range []string{"succeeded", "retry", "dead", "stale"} {
		if v := value(t, body, `carteiro_deliveries_total{outcome="`+o+`"}`); v != 0 {
			t.Errorf("%s = %v before any delivery, want 0", o, v)
		}
	}

	m.ObserveAttempt("succeeded", 20*time.Millisecond)
	m.ObserveAttempt("succeeded", 30*time.Millisecond)
	m.ObserveAttempt("retry", 3*time.Second)
	_, body = scrape(t, m)
	if v := value(t, body, `carteiro_deliveries_total{outcome="succeeded"}`); v != 2 {
		t.Errorf("succeeded = %v, want 2", v)
	}
	if v := value(t, body, `carteiro_deliveries_total{outcome="retry"}`); v != 1 {
		t.Errorf("retry = %v, want 1", v)
	}
	if v := value(t, body, `carteiro_delivery_duration_seconds_count`); v != 3 {
		t.Errorf("duration count = %v, want 3", v)
	}
	if v := value(t, body, `carteiro_delivery_duration_seconds_bucket{le="0.05"}`); v != 2 {
		t.Errorf("attempts under 50ms = %v, want 2", v)
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Error("the Go runtime metrics are missing")
	}
	if strings.Contains(body, "carteiro_queue_depth") {
		t.Error("queue metrics are exported without RegisterQueue")
	}
}

func TestQueueMetrics(t *testing.T) {
	testdb.Require(t, testPool)
	testdb.Reset(t, testPool)
	_, err := testPool.Exec(context.Background(), `
		WITH app AS (INSERT INTO applications (name) VALUES ('metrics') RETURNING id),
		     ep  AS (INSERT INTO endpoints (application_id, url) SELECT id, 'https://example.com' FROM app RETURNING id, application_id),
		     ev  AS (INSERT INTO events (application_id, event_type, payload)
		             SELECT application_id, 'invoice.paid', '{}' FROM ep, generate_series(1, 4) RETURNING id),
		     evs AS (SELECT id, row_number() OVER () AS n FROM ev)
		INSERT INTO deliveries (event_id, endpoint_id, status, next_attempt_at, lease_expires_at)
		SELECT evs.id, ep.id,
		       CASE WHEN n = 4 THEN 'in_flight' WHEN n = 3 THEN 'succeeded' ELSE 'pending' END,
		       CASE WHEN n = 1 THEN now() - interval '90 seconds'  -- overdue
		            WHEN n = 2 THEN now() + interval '1 hour'      -- waiting out its backoff
		       END,
		       CASE WHEN n = 4 THEN now() + interval '1 minute' END
		FROM evs, ep`)
	if err != nil {
		t.Fatal(err)
	}

	m := New()
	m.RegisterQueue(store.New(testPool), discard)
	code, body := scrape(t, m)
	if code != http.StatusOK {
		t.Fatalf("scrape status = %d", code)
	}
	if v := value(t, body, `carteiro_queue_depth{status="pending"}`); v != 2 {
		t.Errorf("pending = %v, want 2: the due one and the scheduled one", v)
	}
	if v := value(t, body, `carteiro_queue_depth{status="in_flight"}`); v != 1 {
		t.Errorf("in_flight = %v, want 1", v)
	}
	// The scheduled delivery is not late: only the overdue one counts.
	if v := value(t, body, `carteiro_oldest_pending_age_seconds`); v < 89 || v > 95 {
		t.Errorf("oldest pending age = %vs, want about 90s", v)
	}
}

func TestEmptyQueueHasNoAge(t *testing.T) {
	testdb.Require(t, testPool)
	testdb.Reset(t, testPool)
	m := New()
	m.RegisterQueue(store.New(testPool), discard)
	_, body := scrape(t, m)
	if v := value(t, body, `carteiro_oldest_pending_age_seconds`); v != 0 {
		t.Errorf("oldest pending age = %v on an empty queue, want 0", v)
	}
}

// TestQueueMetricsFailLoudly scrapes while the database is unreachable. The
// scrape must fail, so Prometheus marks the target down; reporting an empty
// queue instead would hide the outage behind a healthy-looking graph.
func TestQueueMetricsFailLoudly(t *testing.T) {
	testdb.Require(t, testPool)
	closed, err := pgxpool.New(context.Background(), testPool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()

	m := New()
	m.RegisterQueue(store.New(closed), discard)
	if code, _ := scrape(t, m); code != http.StatusInternalServerError {
		t.Errorf("scrape status = %d with the database down, want 500", code)
	}
}
