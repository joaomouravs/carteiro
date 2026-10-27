# Carteiro

> Reliable webhook delivery as a self-hostable service: signed, retried, observable.

**Status:** design phase. See the [roadmap](docs/roadmap.md).

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
