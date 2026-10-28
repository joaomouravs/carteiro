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
#
# It deletes every application in the local database first.
set -euo pipefail

WORKERS=${WORKERS:-4}
ENDPOINTS=${ENDPOINTS:-1}
DELIVERIES=${DELIVERIES:-20000}
CONCURRENCY=${CONCURRENCY:-10}
ADMIN=${ADMIN_TOKEN:-dev-admin-token-change-me-0123456789}
API=${API:-http://localhost:8080}

cd "$(dirname "$0")/.."
compose() { CARTEIRO_WORKER_CONCURRENCY=$CONCURRENCY docker compose -f deploy/compose.yaml --profile loadtest "$@"; }
sql() { compose exec -T postgres psql -U carteiro -d carteiro -qtAX -v ON_ERROR_STOP=1 -c "$1"; }
field() { sed -E "s/.*\"$1\":\"([^\"]+)\".*/\1/"; }

echo "== starting the stack without workers"
# NO_BUILD=1 uses the carteiro:dev image as it is, to measure another
# commit built separately (see .github/workflows/loadtest.yml).
build=--build
[ -n "${NO_BUILD:-}" ] && build=--no-build
compose up -d "$build" --wait postgres sink api >/dev/null
compose up -d --scale worker=0 worker >/dev/null 2>&1 || true
sql "TRUNCATE applications CASCADE"

echo "== creating $ENDPOINTS endpoint(s) through the API (secrets are encrypted there)"
app=$(curl -sf "$API/v1/applications" -H "Authorization: Bearer $ADMIN" --json '{"name":"throughput"}' | field id)
key=$(curl -sf "$API/v1/applications/$app/api-keys" -H "Authorization: Bearer $ADMIN" --json '{"name":"load"}' | field key)
for i in $(seq "$ENDPOINTS"); do
  curl -sf "$API/v1/endpoints" -H "Authorization: Bearer $key" --json "{\"url\":\"http://sink/hook/$i\"}" >/dev/null
done

echo "== loading $DELIVERIES due deliveries"
sql "
WITH eps AS (SELECT id, row_number() OVER (ORDER BY id) - 1 AS n FROM endpoints),
     evs AS (
       INSERT INTO events (application_id, event_type, payload)
       SELECT '$app', 'load.test', '{\"invoice\": \"in_1\", \"amount\": 1999}' FROM generate_series(1, $DELIVERIES)
       RETURNING id
     ),
     numbered AS (SELECT id, row_number() OVER () - 1 AS n FROM evs)
INSERT INTO deliveries (event_id, endpoint_id)
SELECT numbered.id, eps.id FROM numbered JOIN eps ON eps.n = numbered.n % $ENDPOINTS"
sql "ANALYZE"

echo "== starting $WORKERS worker(s), concurrency $CONCURRENCY each"
compose up -d --scale worker="$WORKERS" --no-recreate worker >/dev/null
deadline=$((SECONDS + 600))
while :; do
  left=$(sql "SELECT count(*) FROM deliveries WHERE status <> 'succeeded'")
  [ "$left" = 0 ] && break
  if [ $SECONDS -gt $deadline ]; then echo "timed out with $left deliveries left" >&2; exit 1; fi
  sleep 1
done
compose up -d --scale worker=0 worker >/dev/null 2>&1 || true

echo "== result"
sql "
SELECT format('%s deliveries, %s attempts, in %ss: %s deliveries/s',
              (SELECT count(*) FROM deliveries),
              count(*),
              round(extract(epoch FROM max(started_at + duration_ms * interval '1 millisecond') - min(started_at))::numeric, 2),
              round((SELECT count(*) FROM deliveries) / extract(epoch FROM max(started_at + duration_ms * interval '1 millisecond') - min(started_at))::numeric))
FROM attempts"
