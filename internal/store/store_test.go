package store

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// testPool is shared by every test in the package. It points at a throwaway
// PostgreSQL container with all migrations applied. It is nil with -short.
var testPool *pgxpool.Pool

// TestMain starts one container for the whole package instead of one per
// test: starting PostgreSQL takes seconds, a test should take milliseconds.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	code, err := runWithDatabase(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "store tests:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runWithDatabase(m *testing.M) (int, error) {
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("carteiro"),
		postgres.WithUsername("carteiro"),
		postgres.WithPassword("carteiro"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return 0, fmt.Errorf("start postgres: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(ctr) }()

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, err
	}
	if err := Migrate(ctx, url, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		return 0, err
	}
	testPool, err = Connect(ctx, url)
	if err != nil {
		return 0, err
	}
	defer testPool.Close()

	return m.Run(), nil
}

// requireDB skips the test when running with -short.
func requireDB(t *testing.T) *Queries {
	t.Helper()
	if testPool == nil {
		t.Skip("needs PostgreSQL; skipped with -short")
	}
	return New(testPool)
}

func TestMigrateIsIdempotent(t *testing.T) {
	requireDB(t)
	url := testPool.Config().ConnString()
	if err := Migrate(context.Background(), url, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestCreateAndGetApplication(t *testing.T) {
	q := requireDB(t)
	ctx := context.Background()

	created, err := q.CreateApplication(ctx, "acme")
	if err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	if created.ID.Version() != 7 {
		t.Errorf("ID version = %d, want 7 (UUIDv7)", created.ID.Version())
	}
	if time.Since(created.CreatedAt) > time.Minute {
		t.Errorf("CreatedAt = %v, want about now", created.CreatedAt)
	}

	got, err := q.GetApplication(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if got != created {
		t.Errorf("got %+v, want %+v", got, created)
	}
}

func TestGetMissingApplication(t *testing.T) {
	q := requireDB(t)
	_, err := q.GetApplication(context.Background(), [16]byte{})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("err = %v, want pgx.ErrNoRows", err)
	}
}

// TestSchemaInvariants checks that the CHECK constraints reject impossible
// delivery states, so a bug in the worker cannot persist them.
func TestSchemaInvariants(t *testing.T) {
	q := requireDB(t)
	ctx := context.Background()

	app, err := q.CreateApplication(ctx, "invariants")
	if err != nil {
		t.Fatal(err)
	}
	var endpointID, eventID string
	err = testPool.QueryRow(ctx,
		`INSERT INTO endpoints (application_id, url) VALUES ($1, 'https://example.com') RETURNING id`,
		app.ID).Scan(&endpointID)
	if err != nil {
		t.Fatal(err)
	}
	err = testPool.QueryRow(ctx,
		`INSERT INTO events (application_id, event_type, payload) VALUES ($1, 'user.created', '{}') RETURNING id`,
		app.ID).Scan(&eventID)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		sql  string
	}{
		{"in_flight without lease", `INSERT INTO deliveries (event_id, endpoint_id, status) VALUES ($1, $2, 'in_flight')`},
		{"lease while pending", `INSERT INTO deliveries (event_id, endpoint_id, lease_expires_at) VALUES ($1, $2, now())`},
		{"pending without schedule", `INSERT INTO deliveries (event_id, endpoint_id, next_attempt_at) VALUES ($1, $2, NULL)`},
		{"unknown status", `INSERT INTO deliveries (event_id, endpoint_id, status) VALUES ($1, $2, 'lost')`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testPool.Exec(ctx, tt.sql, eventID, endpointID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" { // check_violation
				t.Errorf("err = %v, want a check_violation", err)
			}
		})
	}

	// The same pair is valid exactly once.
	insert := `INSERT INTO deliveries (event_id, endpoint_id) VALUES ($1, $2)`
	if _, err := testPool.Exec(ctx, insert, eventID, endpointID); err != nil {
		t.Fatalf("valid delivery rejected: %v", err)
	}
	_, err = testPool.Exec(ctx, insert, eventID, endpointID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" { // unique_violation
		t.Errorf("duplicate delivery: err = %v, want a unique_violation", err)
	}
}
