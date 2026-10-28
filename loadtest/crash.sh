#!/usr/bin/env bash
# Fault injection: a worker killed mid-delivery loses nothing (NFR-REL-01).
#
# Loads DELIVERIES due deliveries, starts two workers, and once about half are
# delivered kills one of them with SIGKILL: no shutdown, no chance to record
# what it was sending. The deliveries it held stay leased until their lease
# expires (DELIVERY_TIMEOUT + 30s), then the other worker reclaims and sends
# them. The run passes when every delivery succeeds, and reports how many were
# reclaimed: those show attempt_count 2, and the receiver may have seen them
# twice, which at-least-once delivery allows (ADR 0004).
#
# Usage: DELIVERIES=20000 loadtest/crash.sh   (more settings in loadtest/lib.sh)
#
# It deletes every application in the local database first.
set -euo pipefail

DELIVERY_TIMEOUT=${DELIVERY_TIMEOUT:-5s} # a 35s lease, to keep the run short
source "$(dirname "$0")/lib.sh"

start_without_workers
load_queue
start_workers 2

half=$((DELIVERIES / 2))
until [ "$(sql "SELECT count(*) FROM deliveries WHERE status = 'succeeded'")" -ge "$half" ]; do sleep 0.2; done
victim=$(compose ps -q worker | head -1)
held=$(sql "SELECT count(*) FROM deliveries WHERE status = 'in_flight'")
docker kill --signal KILL "$victim" >/dev/null
echo "== killed one worker with SIGKILL; $held deliveries were in flight across both workers"

# The lease must expire before the survivor can take the victim's rows, so
# allow for it on top of the normal delivery time.
wait_until_delivered 300
stop_workers

echo "== result"
sql "
SELECT format('%s deliveries, all succeeded; %s reclaimed after the crash (attempt_count > 1), %s attempts recorded',
              count(*),
              count(*) FILTER (WHERE attempt_count > 1),
              (SELECT count(*) FROM attempts))
FROM deliveries"
