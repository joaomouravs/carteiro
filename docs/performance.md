# Performance

How Carteiro is load tested, what limits it, and how to reproduce the numbers
(NFR-PERF-01 to 03).

## Method

Two tests, both in [`loadtest/`](../loadtest), against the compose stack:

- **Delivery throughput** ([`throughput.sh`](../loadtest/throughput.sh)): loads N due
  deliveries straight into the database, starts W workers and times how long they take to
  empty the queue. The time comes from the attempts themselves, first start to last
  finish, so container startup does not count. The receiver is an nginx that answers 204
  at once, so the numbers are Carteiro's, not the receiver's.
  [`matrix.sh`](../loadtest/matrix.sh) repeats each configuration and reports the median
  with the minimum and maximum: one slow run must not hide, or be hidden by, the others.
- **Ingest latency** ([`ingest.js`](../loadtest/ingest.js), k6): publishes events at a
  constant arrival rate while the workers deliver them, as in production, and checks
  p99 < 50 ms. A constant arrival rate, rather than looping virtual users, keeps the
  request rate fixed when the server slows down, so slowness shows up as latency.

The published numbers come from the [Load test workflow](../.github/workflows/loadtest.yml),
run on a GitHub-hosted runner: a machine anyone can get, which the workflow describes in
its summary. A laptop running Docker Desktop varied too much between identical runs for its
numbers to mean anything.

```sh
gh workflow run loadtest.yml                             # defaults: 1 to 8 workers, 1 and 16 endpoints
gh workflow run loadtest.yml -f baseline_ref=<commit>    # also measure an older commit, side by side
WORKERS=4 ENDPOINTS=16 task loadtest:throughput          # locally; deletes local data
RATE=200 task loadtest:ingest
```

## What limits throughput

Every delivery ends in a transaction that records the attempt and updates the delivery's
status, and that commit is flushed to disk. The commit rate of the database is the ceiling,
and anything that makes transactions wait on each other lowers it further.

### A lock convoy on the endpoint row (fixed)

The first measurements did not scale: one to eight workers all delivered about 400 to 500
deliveries per second, and some runs of the very same setup reached about 1,400. Two
signs pointed at the cause:

- Sampling `pg_stat_activity` during a slow run, most active sessions were waiting on
  `Lock: transactionid` in the query that updated the endpoint's health.
- Turning `synchronous_commit` off, as a diagnostic only, doubled the rate of a single
  worker. Commit latency mattered, so something was being held across commits.

Every attempt updated its endpoint's row (`consecutive_failures`, `failing_since`), and
each transaction held that row lock until its commit reached disk. Deliveries to one
endpoint therefore went one fsync at a time, and once that queue formed under load it fed
itself: a lock convoy. Whether a run started in the convoy or not decided the bimodal
results.

Most attempts are successes to a healthy endpoint, which change nothing in its health. The
fix ([`endpoints.sql`](../internal/store/queries/endpoints.sql)) clears a failure streak
only when there is one, so those successes no longer write or lock the row; a test checks
that the row's `xmin` stays the same. Failures still update it, which is right: they are
rarer, and failing endpoints are slow anyway.

## A worker killed mid-delivery

[`crash.sh`](../loadtest/crash.sh) loads 20,000 deliveries, starts two workers, and once
about half are delivered kills one with `SIGKILL`: no shutdown, no chance to record what
it was sending. Every delivery must still succeed (NFR-REL-01). A run on the development
laptop:

```
== killed one worker with SIGKILL; 8 deliveries were in flight across both workers
20000 deliveries, all succeeded; 9 reclaimed after the crash (attempt_count > 1), 20000 attempts recorded
```

The 9 deliveries the dead worker held stayed leased until their lease expired, then the
surviving worker reclaimed them as attempt 2. The attempts the dead worker was making were
never recorded, which is why there are as many attempts as deliveries; its receiver may
still have received them, so those 9 could arrive twice, as at-least-once delivery allows
([ADR 0004](adr/0004-at-least-once-delivery.md)). The Load test workflow runs this too.

## Results

The workflow writes a table per run to its summary; the latest are copied here with the
runner they came from.
