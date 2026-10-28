# ADR 0003: Claim deliveries with short leases instead of long-held row locks

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

The simplest worker opens a transaction, locks a delivery with `FOR UPDATE SKIP LOCKED`,
sends the HTTP request, updates the row and commits. The lock then lasts as long as the
HTTP call, up to the request timeout (15 s).

That means one open transaction and one database connection per delivery in flight.
With many workers and slow receivers, the pool runs out of connections just waiting on the
network, and long transactions hold back vacuum on the whole table.

## Decision

I split claiming from delivering:

1. A short transaction marks a batch of rows `in_flight`, sets
   `lease_expires_at = now() + lease`, increments `attempt_count`, and commits.
2. The HTTP request happens with no transaction open.
3. The result is written with an update that only matches if `attempt_count` is still the
   same (a fencing token).

Any worker can take a row whose lease expired. The lease has to be longer than the HTTP
timeout plus some margin; I set it to the timeout plus 30 seconds.

## Consequences

- Each delivery holds a connection for milliseconds, so a small pool serves many
  deliveries in flight.
- If a worker crashes, its deliveries come back on their own when the lease expires. I
  test this by killing a worker with `SIGKILL` in the middle of a run
  ([`loadtest/crash.sh`](../../loadtest/crash.sh)).
- A worker that is only slow (a long GC pause, say) can lose its lease while it is still
  sending. Another worker may then deliver the same event, and the fencing token stops the
  late result from overwriting the newer one. That is a duplicate delivery, which the
  at-least-once contract allows ([ADR 0004](0004-at-least-once-delivery.md)).
