// Package testdb provides a throwaway PostgreSQL, with every migration
// applied, for integration tests.
//
// Each test package calls Run from its TestMain, so one container serves the
// whole package: starting PostgreSQL takes seconds, a test should take
// milliseconds.
package testdb

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/joaomouravs/carteiro/internal/store"
)

// Run starts the database, stores its pool in *pool, runs the tests, and
// exits. With -short it runs the tests without a database and leaves *pool
// nil, so tests that need one can skip themselves (see Require).
func Run(m *testing.M, pool **pgxpool.Pool) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	code, err := run(m, pool)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testdb:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M, pool **pgxpool.Pool) (int, error) {
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
	if err := store.Migrate(ctx, url, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		return 0, err
	}
	p, err := store.Connect(ctx, url)
	if err != nil {
		return 0, err
	}
	defer p.Close()
	*pool = p

	return m.Run(), nil
}

// Require skips the test when it runs without a database (-short).
func Require(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if pool == nil {
		t.Skip("needs PostgreSQL; skipped with -short")
	}
}

// Reset deletes every row, so a test starts from an empty database. Deleting
// the applications cascades to everything else.
func Reset(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "TRUNCATE applications CASCADE"); err != nil {
		t.Fatalf("reset database: %v", err)
	}
}
