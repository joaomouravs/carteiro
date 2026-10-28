# Roadmap

Each milestone ends in a working, tested, documented state, so the repository can be
shown at any point.

## M0: Foundation
- [x] Go module, project layout, `Taskfile` (cross-platform, unlike `make`)
- [x] Configuration from environment, with validation
- [x] `docker compose` with PostgreSQL
- [x] Migrations (`goose`, embedded) and the initial schema
- [x] `sqlc` setup
- [x] `/healthz`, `/readyz`, graceful shutdown
- [x] CI: `golangci-lint`, `go test -race`, integration tests, `sqlc diff`, Docker build

## M1: Ingest
- [x] Admin token; applications; API key issue and revoke (FR-01–03)
- [x] Endpoints CRUD with URL validation; secret generation and encryption at rest (FR-04, 05, 07).
  The blocked-IP part of FR-07 lands with `netguard` in M2.
- [x] `POST /v1/events` with idempotency and fan-out (FR-10–12)
- [x] Problem+json errors (RFC 9457), cursor pagination

## M2: Delivery
- [x] Standard Webhooks signing, tested against the official library's test vector (FR-13)
- [x] SSRF-safe HTTP client (NFR-SEC-03, 04), and the blocked-IP check of FR-07
- [x] Worker: lease-based claiming, fencing, lease recovery (ADR 0003)
- [x] Backoff with jitter, `Retry-After`, `dead` state (FR-14, 15)
- [x] Attempts log and query endpoints (FR-16, 17)
- [x] Example receiver using the official Standard Webhooks library

## M3: Operations
- [x] Replay, single and bulk (FR-18)
- [x] Automatic endpoint disabling and re-enabling (FR-08, 09)
- [x] Secret rotation with a grace period (FR-06)
- [x] Prometheus metrics; Grafana dashboard provisioned by compose (FR-20)
- [x] Retention job (FR-21)

## M4: Proof
- [x] Concurrency and fault-injection tests (`loadtest/crash.sh` kills a worker with SIGKILL mid-run)
- [x] k6 load tests, with results and the identified bottleneck published ([performance](performance.md))
- [x] Dashboard UI: a separate Next.js app on the public API ([ADR 0007](adr/0007-dashboard-as-a-separate-nextjs-app.md))
- [x] Final README, demo GIF, architecture diagrams
