// Package retention deletes events and attempts older than the retention
// period (FR-21), so the database does not grow forever.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/joaomouravs/carteiro/internal/store"
)

// defaultBatch is how many rows one DELETE removes. Small enough that each
// statement takes milliseconds, large enough that a big backlog clears in a
// reasonable number of round trips.
const defaultBatch = 1000

// Purger deletes old data. Any number of processes can run one: an advisory
// lock lets one purge at a time, and the others skip the round.
type Purger struct {
	logger    *slog.Logger
	pool      *pgxpool.Pool
	retention time.Duration
	batch     int64
}

// New returns a Purger that keeps data for retention.
func New(logger *slog.Logger, pool *pgxpool.Pool, retention time.Duration) *Purger {
	return &Purger{logger: logger, pool: pool, retention: retention, batch: defaultBatch}
}

// Result counts what one round deleted.
type Result struct {
	Events, Attempts, Secrets int64
	// Skipped means another process was purging, so this one did nothing.
	Skipped bool
}

// Run purges once at start and then every interval, until ctx is cancelled.
func (p *Purger) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		res, err := p.Purge(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			p.logger.Error("purge old data", "err", err)
		case res.Events+res.Attempts+res.Secrets > 0:
			p.logger.Info("purged old data", "events", res.Events, "attempts", res.Attempts,
				"secrets", res.Secrets, "retention", p.retention.String())
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// Purge runs one round: old events (with their deliveries and attempts),
// then old attempts of events that are kept, then expired secrets.
func (p *Purger) Purge(ctx context.Context) (Result, error) {
	// The advisory lock belongs to a database session, so every statement
	// must run on the same connection, not on whichever one the pool hands
	// out next.
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	q := store.New(conn)
	locked, err := q.TryPurgeLock(ctx)
	if err != nil {
		conn.Release()
		return Result{}, fmt.Errorf("take purge lock: %w", err)
	}
	if !locked {
		conn.Release()
		return Result{Skipped: true}, nil
	}
	defer func() {
		// Unlock even if ctx was cancelled mid-purge. If that fails, the
		// connection is closed rather than returned to the pool, where it
		// would keep the lock and block every future purge.
		if err := q.ReleasePurgeLock(context.WithoutCancel(ctx)); err != nil {
			_ = conn.Hijack().Close(context.WithoutCancel(ctx))
			return
		}
		conn.Release()
	}()

	var res Result
	ms := p.retention.Milliseconds()
	if res.Events, err = drain(func() (int64, error) {
		return q.PurgeEvents(ctx, store.PurgeEventsParams{RetentionMs: ms, Batch: p.batch})
	}, p.batch); err != nil {
		return res, fmt.Errorf("purge events: %w", err)
	}
	if res.Attempts, err = drain(func() (int64, error) {
		return q.PurgeAttempts(ctx, store.PurgeAttemptsParams{RetentionMs: ms, Batch: p.batch})
	}, p.batch); err != nil {
		return res, fmt.Errorf("purge attempts: %w", err)
	}
	if res.Secrets, err = q.PurgeExpiredSecrets(ctx); err != nil {
		return res, fmt.Errorf("purge expired secrets: %w", err)
	}
	return res, nil
}

// drain runs deleteBatch until a batch comes back short, which means nothing
// old is left, and returns the total deleted.
func drain(deleteBatch func() (int64, error), batch int64) (int64, error) {
	var total int64
	for {
		n, err := deleteBatch()
		total += n
		if err != nil || n < batch {
			return total, err
		}
	}
}
