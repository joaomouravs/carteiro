# Roadmap

Each milestone ends in a working, tested, documented state, so the repository can be
shown at any point.

## M0: Foundation
- [ ] Go module, project layout, `Makefile` (or `Taskfile`)
- [ ] Configuration from environment, with validation
- [ ] `docker compose` with PostgreSQL
- [ ] Migrations (`goose`, embedded) and the initial schema
- [ ] `sqlc` setup
- [ ] `/healthz`, `/readyz`, graceful shutdown
- [ ] CI: `golangci-lint`, `go test -race`, integration tests, `sqlc diff`, Docker build

## M1: Ingest
- [ ] Admin token; applications; API key issue and revoke (FR-01–03)
- [ ] Endpoints CRUD with URL validation; secret generation and encryption at rest (FR-04, 05, 07)
- [ ] `POST /v1/events` with idempotency and fan-out (FR-10–12)
- [ ] Problem+json errors (RFC 9457), cursor pagination

## M2: Delivery
- [ ] Standard Webhooks signing, tested against the spec's vectors (FR-13)
- [ ] SSRF-safe HTTP client (NFR-SEC-03, 04)
- [ ] Worker: lease-based claiming, fencing, lease recovery (ADR 0003)
- [ ] Backoff with jitter, `Retry-After`, `dead` state (FR-14, 15)
- [ ] Attempts log and query endpoints (FR-16, 17)
- [ ] Example receiver using the official Standard Webhooks library

## M3: Operations
- [ ] Replay, single and bulk (FR-18)
- [ ] Automatic endpoint disabling and re-enabling (FR-08, 09)
- [ ] Secret rotation with a grace period (FR-06)
- [ ] Prometheus metrics; Grafana dashboard provisioned by compose (FR-20)
- [ ] Retention job (FR-21)

## M4: Proof
- [ ] Concurrency and fault-injection tests
- [ ] k6 load tests, with results and the identified bottleneck published in the README
- [ ] Dashboard UI (decision pending: server-rendered Go templates with htmx vs. a separate Next.js app)
- [ ] Final README, demo GIF, architecture diagrams
