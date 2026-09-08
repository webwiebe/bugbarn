#!/bin/sh
# Measures total Go statement coverage and ratchets it against a committed
# baseline.
#
# The baseline is a FLOOR, and the gate fails in both directions:
#
#   total < baseline               -> FAIL, coverage dropped
#   total > baseline + MAX_DRIFT   -> FAIL, the baseline is beatable and must
#                                     be raised in this same commit
#
# The second half is what makes this a ratchet. It used to only print
# "NOTE: coverage rose to X%; bump the baseline to lock it in", so the note
# became routine CI noise and the baseline sat 2.9pp below reality for weeks —
# meaning nearly 3pp of real coverage could be deleted without the gate
# noticing. See 03_Patterns/pattern--ratchet-gate, property 3.
#
# MAX_DRIFT is 3.0 and not tighter because this number is not identical across
# machines: the same commit (781a86a) measured 49.9% on a loaded CI runner and
# 51.1% on an idle one, a 1.2pp spread from timing-dependent paths in the
# worker/spool/queue loops. A drift band narrower than that spread would fail
# the build at random, and a gate that fails at random gets switched off.
#
# The baseline sits at least 1.0pp under the lowest observed measurement, so a
# legitimate refactor that deletes tested code does not break the build.
#
# Runs on the runner rather than inside the cache-only Docker build, because the
# coverage profile cannot escape `--output type=cacheonly`.
set -eu

BASELINE_FILE="scripts/coverage-baseline.txt"
MAX_DRIFT="3.0"
PROFILE="coverage/go.out"

mkdir -p coverage

# Measure OUR packages only. `go test ./...` does not skip node_modules, so once
# a node step has run `npm ci` the tree contains vendored third-party Go code
# (e.g. web/node_modules/flatted/golang/...) which lands in the profile at 0%
# and drags the total down. That made the number depend on whether an unrelated
# step ran first: CI reported 46.4% where a clean local tree reported 47.3%, so
# the baseline could not be computed or reproduced locally at all.
pkgs=$(go list ./... | grep -v '/node_modules/')
test -n "$pkgs" || {
	echo "ERROR: no packages to test"
	exit 1
}
# shellcheck disable=SC2086
go test -covermode=atomic -coverprofile="$PROFILE" $pkgs

total=$(go tool cover -func="$PROFILE" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')
case "$total" in
'' | *[!0-9.]*)
	echo "FAIL: could not read a coverage total out of $PROFILE — the test run did not produce one"
	exit 1
	;;
esac
echo "Total Go coverage: ${total}%"

if [ ! -f "$BASELINE_FILE" ]; then
	echo "No baseline at $BASELINE_FILE — create it with: echo $total > $BASELINE_FILE"
	exit 1
fi
baseline=$(tr -d ' \t\n' <"$BASELINE_FILE")
case "$baseline" in
'' | *[!0-9.]*)
	echo "FAIL: baseline in $BASELINE_FILE is '$baseline', which is not a number"
	exit 1
	;;
esac

if awk -v b="$baseline" -v t="$total" 'BEGIN { exit !(t < b) }'; then
	echo "FAIL: coverage ${total}% dropped below the baseline of ${baseline}%"
	exit 1
fi

if awk -v b="$baseline" -v t="$total" -v d="$MAX_DRIFT" 'BEGIN { exit !(t > b + d) }'; then
	suggested=$(awk -v t="$total" 'BEGIN { printf "%.1f", t - 1.0 }')
	echo "FAIL: coverage is ${total}% against a baseline of ${baseline}% — more than ${MAX_DRIFT}pp of the gain is unprotected."
	echo "  Raise the floor in this same commit:"
	echo "    echo $suggested > $BASELINE_FILE"
	exit 1
fi

echo "coverage: OK (${total}% within [${baseline}%, ${baseline}% + ${MAX_DRIFT}pp])"
