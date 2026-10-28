#!/usr/bin/env bash
# Delivery throughput (NFR-PERF-02 and 03).
#
# Loads DELIVERIES due deliveries spread over ENDPOINTS endpoints straight
# into the database, starts WORKERS workers, and times how long they take to
# empty the queue. The time comes from the attempts themselves, first start to
# last finish, so worker startup does not count. The receiver is the sink,
# which answers 204 at once, so the numbers are Carteiro's.
#
# Usage: WORKERS=4 ENDPOINTS=1 DELIVERIES=20000 loadtest/throughput.sh
# (more settings in loadtest/lib.sh)
#
# It deletes every application in the local database first.
set -euo pipefail

WORKERS=${WORKERS:-4}
source "$(dirname "$0")/lib.sh"

start_without_workers
load_queue
start_workers "$WORKERS"
wait_until_delivered 600
stop_workers

echo "== result"
sql "
SELECT format('%s deliveries, %s attempts, in %ss: %s deliveries/s',
              (SELECT count(*) FROM deliveries),
              count(*),
              round(extract(epoch FROM max(started_at + duration_ms * interval '1 millisecond') - min(started_at))::numeric, 2),
              round((SELECT count(*) FROM deliveries) / extract(epoch FROM max(started_at + duration_ms * interval '1 millisecond') - min(started_at))::numeric))
FROM attempts"
