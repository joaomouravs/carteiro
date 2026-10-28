# Performance

How I load test Carteiro, what I found limits it, and how to get the same numbers
(NFR-PERF-01 to 03).

## Method

There are two tests, both in [`loadtest/`](../loadtest), and both run against the compose
stack:

- **Delivery throughput** ([`throughput.sh`](../loadtest/throughput.sh)) loads N due
  deliveries straight into the database, starts W workers and times how long they take to
  empty the queue. I take the time from the attempts themselves, from the first start to
  the last finish, so container startup does not count. The receiver is an nginx that
  answers 204 right away, so the numbers measure Carteiro and not the receiver.
  [`matrix.sh`](../loadtest/matrix.sh) repeats each setup and reports the median with the
  minimum and maximum, so one slow run can neither hide nor be hidden by the others.
- **Ingest latency** ([`ingest.js`](../loadtest/ingest.js), with k6) publishes events at a
  constant arrival rate while the workers deliver them, like in production, and checks
  that p99 stays under 50 ms. I used a constant arrival rate instead of looping virtual
  users because it keeps the request rate fixed when the server slows down, so slowness
  shows up as latency instead of as fewer requests.

The numbers I publish come from the [Load test workflow](../.github/workflows/loadtest.yml)
on a GitHub-hosted runner, a machine anyone can get. My laptop, running Docker Desktop with
8 GB of RAM, gave results that varied too much between identical runs to mean anything.

```sh
gh workflow run loadtest.yml                             # defaults: 1 to 8 workers, 1 and 16 endpoints
gh workflow run loadtest.yml -f baseline_ref=<commit>    # also measure an older commit, side by side
WORKERS=4 ENDPOINTS=16 task loadtest:throughput          # locally; deletes local data
RATE=200 task loadtest:ingest
```

## What limits throughput

Every delivery ends in a transaction that records the attempt and updates the delivery,
and that commit is flushed to disk. So the database's commit rate is the ceiling, and
anything that makes those transactions wait for each other pushes it lower.

### A lock convoy on the endpoint row (fixed)

My first measurements, on the laptop, did not scale at all: one to eight workers all
landed at about 400 to 500 deliveries per second, and some runs of the very same setup
reached about 1,400. Two things pointed me at the cause:

- I sampled `pg_stat_activity` during a slow run, and most active sessions were waiting on
  `Lock: transactionid` in the query that updated the endpoint's health.
- As a diagnostic only, I turned `synchronous_commit` off, and a single worker went twice
  as fast. So commit latency mattered, which meant something was being held across
  commits.

Every attempt updated its endpoint's row (`consecutive_failures`, `failing_since`), and each
transaction held that row lock until its commit reached disk. Deliveries to the same
endpoint ended up going one fsync at a time, and once that queue formed under load it kept
itself going: a lock convoy. Whether a run happened to start inside the convoy or not
explained the two very different results.

Most attempts are successes to a healthy endpoint, and those change nothing about its
health. So the fix ([`endpoints.sql`](../internal/store/queries/endpoints.sql)) only clears
a failure streak when there is one, and those successes no longer write or lock the row. A
test checks that the row's `xmin` stays the same after successful deliveries. Failures still
update the row, which is fine: they are rarer, and a failing endpoint is slow anyway.

## A worker killed mid-delivery

[`crash.sh`](../loadtest/crash.sh) loads 20,000 deliveries, starts two workers, and when
about half are done it kills one with `SIGKILL`: no shutdown, no chance to record what it
was sending. Every delivery still has to arrive (NFR-REL-01). A run on my laptop:

```
== killed one worker with SIGKILL; 8 deliveries were in flight across both workers
20000 deliveries, all succeeded; 9 reclaimed after the crash (attempt_count > 1), 20000 attempts recorded
```

The 9 deliveries the dead worker held stayed leased until the lease ran out, and then the
other worker reclaimed them as attempt 2. The attempts the dead worker was in the middle of
never got recorded, which is why there are as many attempts as deliveries. The receiver may
still have gotten them, so those 9 could arrive twice, which at-least-once delivery allows
([ADR 0004](adr/0004-at-least-once-delivery.md)). The Load test workflow runs this too.

## Results

These come from [run 40785845801](https://github.com/joaomouravs/carteiro/actions/runs/40785845801)
of the Load test workflow, on 2026-10-28, on a GitHub-hosted `ubuntu-latest` runner (image
Ubuntu 26.04; GitHub's standard runner for public repositories has 4 vCPUs and 16 GB of
memory). PostgreSQL, the API, the workers and the receiver all ran on it, in Docker. Each
run had 20,000 deliveries and each setup ran 3 times; I show the median, with the slowest
and fastest run. In every run, each delivery took exactly one attempt.

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

Before the fix, every worker I added made Carteiro **slower**, because more transactions
queued on the same endpoint rows. After it, throughput goes up with the second worker and
then holds at about 1,500 deliveries per second. NFR-PERF-02 asks for 500 per second with
4 workers; it does 1,549.

### Ingest latency

Publishing at a steady 200 events per second for 60 seconds, while the worker delivered
them: **p99 3.54 ms** (p50 1.26 ms, p95 2.39 ms, max 42 ms), and none of the 12,001
requests failed. NFR-PERF-01 asks for a p99 under 50 ms.

### Where the ceiling is now

From two workers on, adding more no longer helps, with one endpoint or sixteen. Each
delivery ends in a transaction whose commit is flushed to disk, so my best guess is that
the database's commit rate is the ceiling: on the laptop, turning `synchronous_commit` off
doubled a single worker's rate. I have not measured that on the runner yet, so it is the
next thing I would check, together with the two ways I see to raise the ceiling: recording
several attempts per transaction, or putting PostgreSQL on faster storage.
