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

The first measurements, on the development laptop, did not scale: one to eight workers all
delivered about 400 to 500 deliveries per second, and some runs of the very same setup
reached about 1,400. Two signs pointed at the cause:

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

From [run 40785845801](https://github.com/joaomouravs/carteiro/actions/runs/40785845801) of
the Load test workflow, on 2026-10-28: a GitHub-hosted `ubuntu-latest` runner (image
Ubuntu 26.04; GitHub's standard runner for public repositories has 4 vCPUs and 16 GB of
memory), with PostgreSQL, the API, the workers and the receiver all on it, in Docker.
20,000 deliveries per run, 3 runs per configuration; median, with the slowest and fastest
run. Every run delivered each delivery with exactly one attempt.

### Delivery throughput, before and after the lock convoy fix

Deliveries per second, all to **one endpoint**:

| Workers | Before (`41740e9`) | After (`6448ed0`) | Change |
|---:|---:|---:|---:|
| 1 | 1,146 (1,141–1,156) | 1,347 (1,330–1,355) | +18% |
| 2 | 1,078 (1,074–1,086) | 1,533 (1,509–1,550) | +42% |
| 4 | 938 (938–944) | **1,549** (1,548–1,558) | +65% |
| 8 | 774 (768–790) | 1,460 (1,443–1,469) | +89% |

Spread over **16 endpoints**:

| Workers | Before (`41740e9`) | After (`6448ed0`) |
|---:|---:|---:|
| 1 | 1,133 (1,122–1,135) | 1,290 (1,264–1,299) |
| 2 | 1,049 (1,046–1,072) | 1,506 (1,499–1,506) |
| 4 | 928 (912–935) | 1,511 (1,497–1,512) |
| 8 | 766 (759–767) | 1,405 (1,379–1,427) |

Before the fix, every worker added made Carteiro **slower**: more transactions queued on
the same endpoint rows. After it, throughput rises with the second worker and then holds
at about 1,500 deliveries per second. NFR-PERF-02 asks for 500 per second with 4 workers;
it does 1,549.

### Ingest latency

Publishing at a constant 200 events per second for 60 seconds, while the worker delivered
them: **p99 3.54 ms** (p50 1.26 ms, p95 2.39 ms, max 42 ms), and none of the 12,001
requests failed. NFR-PERF-01 asks for a p99 below 50 ms.

### Where the ceiling is now

From two workers on, more workers no longer help, with one endpoint or sixteen. Each
delivery ends in a transaction whose commit is flushed to disk, so the database's commit
rate is the likeliest ceiling: on the development laptop, turning `synchronous_commit` off
as a diagnostic doubled a single worker's rate. That was not measured on the runner, so it
is the next thing to check, along with the two ways to raise the ceiling: recording several
attempts per transaction, or running PostgreSQL on faster storage.
