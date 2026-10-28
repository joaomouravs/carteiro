# Helpers shared by the load test scripts. Source it; do not run it.
#
# Settings, from the environment:
#   ENDPOINTS, DELIVERIES   the queue to load
#   CONCURRENCY             deliveries each worker sends at once
#   DELIVERY_TIMEOUT        per request; the lease is this plus 30s
#   NO_BUILD=1              use the carteiro:dev image as it is (to measure
#                           another commit, see .github/workflows/loadtest.yml)

ENDPOINTS=${ENDPOINTS:-1}
DELIVERIES=${DELIVERIES:-20000}
CONCURRENCY=${CONCURRENCY:-10}
DELIVERY_TIMEOUT=${DELIVERY_TIMEOUT:-15s}
ADMIN=${ADMIN_TOKEN:-dev-admin-token-change-me-0123456789}
API=${API:-http://localhost:8080}

cd "$(dirname "${BASH_SOURCE[0]}")/.."

compose() {
  CARTEIRO_WORKER_CONCURRENCY=$CONCURRENCY CARTEIRO_DELIVERY_TIMEOUT=$DELIVERY_TIMEOUT \
    docker compose -f deploy/compose.yaml --profile loadtest "$@"
}
# client_min_messages hides NOTICEs, such as TRUNCATE's cascade message.
sql() {
  compose exec -T -e PGOPTIONS="-c client_min_messages=warning" postgres \
    psql -U carteiro -d carteiro -qtAX -v ON_ERROR_STOP=1 -c "$1"
}
field() { sed -E "s/.*\"$1\":\"([^\"]+)\".*/\1/"; }

# start_without_workers starts the stack with no worker and an empty database.
start_without_workers() {
  echo "== starting the stack without workers"
  local build=--build
  [ -n "${NO_BUILD:-}" ] && build=--no-build
  compose up -d "$build" --wait postgres sink api >/dev/null
  stop_workers
  sql "TRUNCATE applications CASCADE"
}

stop_workers() { compose up -d --scale worker=0 worker >/dev/null 2>&1 || true; }

start_workers() {
  echo "== starting $1 worker(s), concurrency $CONCURRENCY each"
  compose up -d --scale worker="$1" --no-recreate worker >/dev/null
}

# load_queue creates ENDPOINTS endpoints through the API, where their secrets
# are encrypted, then inserts DELIVERIES due deliveries spread over them.
load_queue() {
  echo "== creating $ENDPOINTS endpoint(s) and $DELIVERIES due deliveries"
  local app key
  app=$(curl -sf "$API/v1/applications" -H "Authorization: Bearer $ADMIN" --json '{"name":"loadtest"}' | field id)
  key=$(curl -sf "$API/v1/applications/$app/api-keys" -H "Authorization: Bearer $ADMIN" --json '{"name":"load"}' | field key)
  for i in $(seq "$ENDPOINTS"); do
    curl -sf "$API/v1/endpoints" -H "Authorization: Bearer $key" --json "{\"url\":\"http://sink/hook/$i\"}" >/dev/null
  done
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
}

# wait_until_delivered waits for every delivery to succeed, up to $1 seconds.
wait_until_delivered() {
  local deadline=$((SECONDS + ${1:-600})) left
  while :; do
    left=$(sql "SELECT count(*) FROM deliveries WHERE status <> 'succeeded'")
    [ "$left" = 0 ] && return 0
    if [ $SECONDS -gt "$deadline" ]; then
      echo "timed out with $left deliveries not delivered" >&2
      return 1
    fi
    sleep 1
  done
}
