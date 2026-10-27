# ADR 0003: Claim deliveries with short leases instead of long-held row locks

- **Status:** Proposed
- **Date:** 2026-10-27

## Context

The simplest worker opens a transaction, locks a delivery row with
`FOR UPDATE SKIP LOCKED`, sends the HTTP request, updates the row, and commits. The lock
then lives as long as the HTTP call, which can take up to the request timeout (15 s).

That holds a database connection and an open transaction per in-flight delivery. With
many workers and slow receivers, the connection pool is exhausted by waiting on the
network, and long transactions slow down vacuum for the whole table.

## Decision

Claiming and delivering are separate steps:

1. A short transaction marks a batch of rows as `in_flight` with
   `lease_expires_at = now() + lease_duration` and increments `attempt_count`, then commits.
2. The HTTP request happens with no transaction open.
3. The result is written with a conditional update that matches on `attempt_count`
   (a fencing token).

Rows whose lease expired are reclaimable by any worker. The lease must be longer than
the HTTP timeout plus a safety margin.

## Consequences

- Connections are used only for milliseconds per delivery, so a small pool supports many
  concurrent deliveries.
- A crashed worker's deliveries are recovered automatically once the lease expires.
- A worker that is merely slow (e.g. a long GC pause) can lose its lease while still
  sending. Another worker may then deliver the same event; the fencing token prevents the
  stale result from overwriting the newer state. This is a duplicate delivery, which the
  at-least-once contract allows ([ADR 0004](0004-at-least-once-delivery.md)).
