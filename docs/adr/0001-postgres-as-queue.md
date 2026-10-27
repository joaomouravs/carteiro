# ADR 0001: Use PostgreSQL as the delivery queue

- **Status:** Proposed
- **Date:** 2026-10-27

## Context

Deliveries must be durable, retried at a future time, inspectable by producers, and
processed by several workers in parallel. The usual options are a dedicated broker
(RabbitMQ, Kafka, SQS, Redis Streams) or the database that already stores the data.

## Decision

Store deliveries in a PostgreSQL table and have workers claim them with
`SELECT ... FOR UPDATE SKIP LOCKED`.

## Consequences

**Positive**
- Publishing an event and creating its deliveries is a single ACID transaction. With a
  separate broker we would need an outbox pattern to avoid "saved but never enqueued".
- Delayed retries are just `next_attempt_at`; most brokers need extra machinery for
  scheduled delivery.
- The queue is queryable with SQL, which the producer-facing API needs anyway.
- One dependency to run, back up, and monitor.

**Negative**
- Throughput is bounded by the database. For this project's target (hundreds to low
  thousands of deliveries per second) PostgreSQL is sufficient; the load tests will
  document where the limit is.
- Polling adds latency between "due" and "sent". It can be reduced with
  `LISTEN/NOTIFY` as a wake-up hint if measurements show it matters.
- High update churn on `deliveries` produces dead tuples; autovacuum settings for that
  table need attention, and the retention job keeps it small.

## Alternatives considered

- **RabbitMQ / SQS**: better raw throughput, but requires an outbox and a second system
  for scheduled retries, which adds complexity the target load does not justify.
- **Redis (e.g. sorted sets)**: fast, but durability depends on persistence settings,
  and state would be split between two stores.
