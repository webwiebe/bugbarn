#!/bin/sh
# Dead-code soak.
#
# Dead code was never measured in this repo. It is not installed blocking: the
# finding count is ratcheted against a committed baseline that may only fall
# (scripts/ratchet.sh), with a written enforce-at target of 0. When the count
# reaches 0, move `unused` into .golangci.yml and delete its baseline.
#
# The import-boundary soak (depguard) reached 0 and moved into .golangci.yml;
# scripts/check-go-lint.sh enforces it.
#
# Configuration lives in .golangci-soak.yml.
set -eu

DEADCODE_BASELINE="scripts/deadcode-baseline.txt"
ENFORCE_AT=0
REPORT_DIR=".tools/soak-reports"

sh scripts/ensure-golangci.sh
GCL="$(pwd)/.tools/golangci-lint"
CONFIG="$(pwd)/.golangci-soak.yml"
REPORTS="$(pwd)/$REPORT_DIR"
mkdir -p "$REPORTS"

run_soak() {
	_dir="$1"
	_name="$2"
	_report="$REPORTS/$_name.json"
	rm -f "$_report"
	set +e
	(
		cd "$_dir" &&
			"$GCL" run --allow-parallel-runners --timeout=10m \
				-c "$CONFIG" --output.json.path="$_report" ./...
	)
	_rc=$?
	set -e
	if [ "$_rc" -ne 0 ] && [ "$_rc" -ne 1 ]; then
		echo "FAIL: golangci-lint exited $_rc in $_dir — the soak did not run to completion"
		return 1
	fi
	for _linter in unused; do
		python3 scripts/count-findings.py golangci "$_report" "$_linter" >"$REPORTS/$_name.$_linter.count"
	done
}

run_soak . root
run_soak sdks/go sdks-go

dead=$(($(cat "$REPORTS/root.unused.count") + $(cat "$REPORTS/sdks-go.unused.count")))

echo "dead code (unused): $dead"
sh scripts/ratchet.sh "dead-code" "$DEADCODE_BASELINE" "$dead" "$ENFORCE_AT"
