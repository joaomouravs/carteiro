#!/usr/bin/env bash
# Runs throughput.sh over a matrix of worker and endpoint counts, REPS times
# each, and prints a Markdown table of the median, minimum and maximum. The
# median and the range, not the mean: one slow run must not hide, and must not
# be hidden by, the others (an earlier lock convoy showed up as runs of either
# about 450 or about 1,400 deliveries/s for the same setup).
#
# Usage: WORKERS_LIST="1 2 4 8" ENDPOINTS_LIST="1 16" REPS=3 DELIVERIES=20000 loadtest/matrix.sh
set -euo pipefail

WORKERS_LIST=${WORKERS_LIST:-1 2 4 8}
ENDPOINTS_LIST=${ENDPOINTS_LIST:-1 16}
REPS=${REPS:-3}
export DELIVERIES=${DELIVERIES:-20000}
here=$(dirname "$0")

echo "| Workers | Endpoints | Median deliveries/s | Min | Max | Attempts per delivery |"
echo "|---:|---:|---:|---:|---:|---:|"
for endpoints in $ENDPOINTS_LIST; do
  for workers in $WORKERS_LIST; do
    rates=() extra=0
    for _ in $(seq "$REPS"); do
      # Last line: "N deliveries, M attempts, in Xs: R deliveries/s".
      line=$(WORKERS=$workers ENDPOINTS=$endpoints bash "$here/throughput.sh" 2>&1 | tail -1)
      read -r n attempts rate < <(sed -E 's/^([0-9]+) deliveries, ([0-9]+) attempts, .*: ([0-9]+) deliveries\/s$/\1 \2 \3/' <<<"$line")
      if ! [[ $rate =~ ^[0-9]+$ ]]; then
        echo "run failed (workers=$workers endpoints=$endpoints): $line" >&2
        exit 1
      fi
      rates+=("$rate")
      extra=$((extra + attempts - n))
    done
    sorted=($(printf '%s\n' "${rates[@]}" | sort -n))
    median=${sorted[$((REPS / 2))]}
    per_delivery=$(awk -v e="$extra" -v n="$DELIVERIES" -v r="$REPS" 'BEGIN { printf "%.3f", 1 + e / (n * r) }')
    echo "| $workers | $endpoints | $median | ${sorted[0]} | ${sorted[$((REPS - 1))]} | $per_delivery |"
  done
done
