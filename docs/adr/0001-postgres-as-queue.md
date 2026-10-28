# ADR 0001: Use PostgreSQL as the delivery queue

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

Deliveries have to survive restarts, wait for a retry at some future time, be visible to
the producer through the API, and be processed by several workers at once. The usual way
to get that is a message broker (RabbitMQ, Kafka, SQS, Redis Streams). The other way is the
database I already need for everything else.

## Decision

I keep deliveries in a PostgreSQL table, and workers claim them with
`SELECT ... FOR UPDATE SKIP LOCKED`.

## Consequences

**What I get**
- Publishing an event and creating its deliveries is one transaction. With a separate
  broker I would need an outbox to avoid the "saved but never queued" case.
- A delayed retry is just a `next_attempt_at` column. Most brokers need extra machinery
  to schedule a message for later.
- The queue is plain SQL, which the API needs anyway to show producers what happened.
- One system to run, back up and monitor.

**What it costs**
- Throughput is bounded by the database. I aimed at hundreds to low thousands of
  deliveries per second, which PostgreSQL handles; the load tests later put the ceiling
  at about 1,500 per second on a GitHub-hosted runner ([performance](../performance.md)).
- Workers poll, so there is some delay between "due" and "sent". If it ever matters,
  `LISTEN/NOTIFY` can wake them up sooner.
- `deliveries` gets a lot of updates, which leaves dead tuples behind. Autovacuum needs an
  eye on this table, and the retention job keeps it small.

## Alternatives I considered

- **RabbitMQ or SQS**: more raw throughput, but I would need an outbox plus something else
  for scheduled retries. That is a lot of moving parts for the load I expect.
- **Redis sorted sets**: fast, but durability depends on how persistence is configured,
  and the state would live in two places.
