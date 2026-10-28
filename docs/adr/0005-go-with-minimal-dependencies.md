# ADR 0005: Go, the standard library, and SQL-first data access

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

Most of what Carteiro does is concurrent I/O: lots of outbound HTTP requests, a pool of
database connections, and worker loops that run for a long time and must stop cleanly. I
also wanted it to deploy as a single artifact.

## Decision

- **Language:** Go. Goroutines and `context` cancellation fit the worker model, and the
  build is one static binary in a small image.
- **HTTP:** the standard library's `net/http`. Its router handles methods and path
  parameters since Go 1.22, so I did not need a framework.
- **Logging:** `log/slog`, also from the standard library.
- **Database:** `pgx` as the driver and pool, and `sqlc` to generate typed Go from SQL I
  write by hand. No ORM: the queue depends on PostgreSQL features (`SKIP LOCKED`, partial
  indexes, `RETURNING`) that I want to write and review as SQL.
- **Migrations:** `goose`, with the migrations embedded in the binary.
- **Tests:** the standard `testing` package, and `testcontainers-go` to run a real
  PostgreSQL.
- **Metrics:** `prometheus/client_golang`.

## Consequences

- I only took a dependency when replacing it would have taken more than a few dozen lines.
- The SQL is in the repository where anyone can read and review it.
- `sqlc` adds a generation step, so CI checks that the generated code is up to date.
