package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx runs fn inside a transaction: it commits if fn returns nil and rolls
// back otherwise, including when fn panics.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(q *Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so deferring it covers
	// every early return and panic.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
