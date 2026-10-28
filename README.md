# Carteiro

> Reliable webhook delivery as a self-hostable service: signed, retried, observable.

**Status:** foundation done (M0); the ingest API (M1) is next. See the [roadmap](docs/roadmap.md).

Carteiro (Portuguese for *mail carrier*) takes webhook delivery off your application's
plate. You publish an event once; Carteiro fans it out to every subscribed endpoint,
signs it with the [Standard Webhooks](https://www.standardwebhooks.com/) spec, retries
with exponential backoff, disables endpoints that keep failing, and records every attempt
so you can inspect and replay what went wrong.

## Guarantees

- **At-least-once delivery.** An accepted event is never lost, even if a worker crashes
  mid-delivery. Receivers should deduplicate using the `webhook-id` header.
- **No ordering guarantee.** One failing event never blocks the others.
- **No transaction held across network calls.** Workers claim deliveries with short
  leases, so slow receivers cannot exhaust the database.

## Running locally

Requirements: Docker. For development, also Go 1.27 and
[Task](https://taskfile.dev) (`go install github.com/go-task/task/v3/cmd/task@latest`).

Run the whole stack (PostgreSQL, migrations, API) with one command:

```sh
docker compose -f deploy/compose.yaml up --build
curl localhost:8080/readyz   # {"status":"ok"}
```

Development workflow:

```sh
cp .env.example .env
task run        # start PostgreSQL, apply migrations, run the API with go run
task test       # unit and integration tests (integration tests start PostgreSQL in Docker)
task test:unit  # only the fast tests
task lint       # golangci-lint
task sqlc       # regenerate Go code after changing a query or a migration
```

`sqlc` and `golangci-lint` run from their official Docker images, so they need no
installation and use the same versions as CI.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `CARTEIRO_DATABASE_URL` | (required) | PostgreSQL connection string |
| `CARTEIRO_HTTP_ADDR` | `:8080` | Address the API listens on |
| `CARTEIRO_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `CARTEIRO_SHUTDOWN_TIMEOUT` | `15s` | Time in-flight requests get to finish on shutdown |

## Documentation

- [Requirements](docs/requirements.md)
- [Architecture](docs/architecture.md)
- Architecture decision records
  - [0001: PostgreSQL as the delivery queue](docs/adr/0001-postgres-as-queue.md)
  - [0002: Standard Webhooks signing](docs/adr/0002-standard-webhooks-signing.md)
  - [0003: Lease-based claiming](docs/adr/0003-lease-based-claiming.md)
  - [0004: At-least-once delivery, without ordering](docs/adr/0004-at-least-once-delivery.md)
  - [0005: Go with minimal dependencies](docs/adr/0005-go-with-minimal-dependencies.md)

## License

[MIT](LICENSE)
