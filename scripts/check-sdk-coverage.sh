#!/bin/sh
# Ratchet gate for the line coverage of one client SDK.
#
#   check-sdk-coverage.sh <name> <format> <report-file>
#
# <format> says how to read the total out of the test runner's own report:
#
#   node    node --test --experimental-test-coverage   ("all files | 92.98 |")
#   python  pytest --cov --cov-report=term             ("TOTAL  369  14  96.2%")
#   php     phpunit --coverage-text                    ("Lines:   84.21% (128/152)")
#
# The committed floor lives in scripts/sdk-coverage-baseline-<name>.txt. The
# gate fails in three cases, the same contract as scripts/check-coverage.sh:
#
#   total < MIN_COVERAGE (80)       -> FAIL, below the owner's minimum
#   total < baseline                -> FAIL, coverage dropped
#   total > baseline + MAX_DRIFT    -> FAIL, the floor is beatable and must be
#                                      raised in this same commit
#
# MAX_DRIFT is 2.0. The SDK suites run local servers and worker threads but
# their line counts do not depend on timing the way the Go worker loops do, so
# the band can be tighter than the Go gate's 3.0. Set the baseline 1.0pp under
# the measurement so deleting one small tested file does not break the build.
set -eu

name="${1:?usage: check-sdk-coverage.sh <name> <node|python|php> <report-file>}"
format="${2:?missing format}"
report="${3:?missing report file}"

MIN_COVERAGE="${MIN_COVERAGE:-80}"
MAX_DRIFT="${MAX_DRIFT:-2.0}"
BASELINE_DIR="${BASELINE_DIR:-$(cd "$(dirname "$0")" && pwd)}"
baseline_file="$BASELINE_DIR/sdk-coverage-baseline-$name.txt"

if [ ! -f "$report" ]; then
	echo "FAIL: no coverage report at $report for the $name SDK; the test run did not produce one"
	exit 1
fi

case "$format" in
node) total=$(awk -F'|' '/all files/ { gsub(/[ \t]/, "", $2); print $2; exit }' "$report") ;;
python) total=$(awk '$1 == "TOTAL" { gsub(/%/, "", $NF); print $NF; exit }' "$report") ;;
php) total=$(awk '/^[ \t]*Lines:/ { gsub(/%/, "", $2); print $2; exit }' "$report") ;;
*)
	echo "FAIL: unknown report format '$format'"
	exit 1
	;;
esac

case "$total" in
'' | *[!0-9.]*)
	echo "FAIL: could not read a $name coverage total out of $report ($format format)"
	exit 1
	;;
esac
echo "$name SDK line coverage: ${total}%"

if [ ! -f "$baseline_file" ]; then
	echo "No baseline at $baseline_file. Create it with: echo $total > $baseline_file"
	exit 1
fi
baseline=$(tr -d ' \t\n' <"$baseline_file")
case "$baseline" in
'' | *[!0-9.]*)
	echo "FAIL: baseline in $baseline_file is '$baseline', which is not a number"
	exit 1
	;;
esac

if awk -v m="$MIN_COVERAGE" -v t="$total" 'BEGIN { exit !(t < m) }'; then
	echo "FAIL: $name SDK coverage ${total}% is below the ${MIN_COVERAGE}% minimum"
	exit 1
fi

if awk -v b="$baseline" -v t="$total" 'BEGIN { exit !(t < b) }'; then
	echo "FAIL: $name SDK coverage ${total}% dropped below the baseline of ${baseline}%"
	exit 1
fi

if awk -v b="$baseline" -v t="$total" -v d="$MAX_DRIFT" 'BEGIN { exit !(t > b + d) }'; then
	suggested=$(awk -v t="$total" 'BEGIN { printf "%.1f", t - 1.0 }')
	echo "FAIL: $name SDK coverage is ${total}% against a baseline of ${baseline}%; more than ${MAX_DRIFT}pp of the gain is unprotected."
	echo "  Raise the floor in this same commit:"
	echo "    echo $suggested > $baseline_file"
	exit 1
fi

echo "$name SDK coverage: OK (${total}% within [${baseline}%, ${baseline}% + ${MAX_DRIFT}pp], minimum ${MIN_COVERAGE}%)"
