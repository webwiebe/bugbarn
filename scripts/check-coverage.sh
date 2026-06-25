#!/bin/sh
# Measures total Go statement coverage and ratchets it against a committed
# baseline: a PR may raise coverage but never drop it (beyond a small epsilon
# for test-ordering / build-tag noise). When coverage rises, bump the baseline
# file to lock the gain in.
#
# Runs on the runner rather than inside the cache-only Docker build, because the
# coverage profile cannot escape `--output type=cacheonly`.
set -eu

BASELINE_FILE="scripts/coverage-baseline.txt"
EPSILON="0.5"
PROFILE="coverage/go.out"

mkdir -p coverage
go test -covermode=atomic -coverprofile="$PROFILE" ./...

total=$(go tool cover -func="$PROFILE" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')
echo "Total Go coverage: ${total}%"

if [ ! -f "$BASELINE_FILE" ]; then
	echo "No baseline at $BASELINE_FILE — create it with: echo $total > $BASELINE_FILE"
	exit 1
fi
baseline=$(cat "$BASELINE_FILE")

drop=$(awk -v b="$baseline" -v t="$total" -v e="$EPSILON" 'BEGIN { print (t < b - e) ? 1 : 0 }')
if [ "$drop" -eq 1 ]; then
	echo "FAIL: coverage ${total}% dropped below baseline ${baseline}% (epsilon ${EPSILON})"
	exit 1
fi

raise=$(awk -v b="$baseline" -v t="$total" 'BEGIN { print (t > b + 1) ? 1 : 0 }')
if [ "$raise" -eq 1 ]; then
	echo "NOTE: coverage rose to ${total}%; bump $BASELINE_FILE to lock it in."
fi
echo "coverage: OK (${total}% >= ${baseline}% - ${EPSILON})"
