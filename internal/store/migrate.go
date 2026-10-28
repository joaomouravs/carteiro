package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver, which goose needs
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/joaomouravs/carteiro/migrations"
)

// Migrate applies every pending migration. goose takes a PostgreSQL advisory
// lock first, so two instances starting at once do not apply the same
// migration twice.
func Migrate(ctx context.Context, databaseURL string, logger *slog.Logger) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	for _, r := range results {
		logger.InfoContext(ctx, "migration applied",
			"version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration.String())
	}
	if len(results) == 0 {
		logger.InfoContext(ctx, "database schema is up to date")
	}
	return nil
}
