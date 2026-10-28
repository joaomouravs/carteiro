# ADR 0005: Go, the standard library, and SQL-first data access

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

Carteiro is a network service whose core work is concurrent I/O: many outbound HTTP
requests, a database connection pool, and long-running worker loops that must shut down
cleanly. It should be easy to deploy as one artifact.

## Decision

- **Language:** Go. Goroutines and `context` cancellation suit the worker model, and the
  output is a single static binary in a small container image.
- **HTTP:** the standard library `net/http`, whose router has supported methods and path
  parameters since Go 1.22. No web framework.
- **Logging:** `log/slog` from the standard library.
- **Database:** `pgx` as the driver and pool; `sqlc` generates type-safe Go from
  hand-written SQL. No ORM: the queue queries depend on PostgreSQL-specific features
  (`SKIP LOCKED`, partial indexes, `RETURNING`) that should be written and reviewed as SQL.
- **Migrations:** `goose`, with migrations embedded in the binary.
- **Testing:** the standard `testing` package and `testcontainers-go` for PostgreSQL.
- **Metrics:** `prometheus/client_golang`.

## Consequences

- Every dependency is one that would be hard to replace with a few dozen lines of code.
- The SQL is visible and reviewable in the repository.
- `sqlc` requires a generation step; CI checks that the generated code is up to date.
