#!/bin/sh
# Go lint gate for both modules (root and sdks/go).
#
# Two legs, and the split is the whole point of this script:
#
#   1. FULL-TREE RATCHET — runs on every event, including a push to `main`.
#      Counts every finding in both modules and ratchets the total against
#      scripts/golangci-baseline.txt.
#
#   2. DIFF-SCOPED RUN — pull requests only, where a merge base that is not
#      HEAD actually exists. Names the specific findings a branch introduced.
#
# Leg 1 exists because leg 2 alone was a no-op on `main`. The old step ran
# `--new-from-merge-base=origin/main` unconditionally; on a push to `main` the
# merge base IS the commit, so nothing is ever new and the lint leg passed
# without examining a single line. `binary-release` then gated the release on
# that vacuous run. Leg 2 now refuses to run when it would be vacuous, and
# leg 1 does the real work on `main`.
#
# The full-tree count is a SOAK, not yet an enforced clean tree: the baseline is
# today's count and may only fall. Enforce-at target is 0.
set -eu

BASELINE_FILE="scripts/golangci-baseline.txt"
ENFORCE_AT=0
REPORT_DIR=".tools/lint-reports"

sh scripts/ensure-golangci.sh
GCL="$(pwd)/.tools/golangci-lint"
REPORTS="$(pwd)/$REPORT_DIR"
mkdir -p "$REPORTS"

# Runs golangci-lint over one module and writes the finding count to
# "$REPORTS/<name>.count". Returns non-zero if the linter did not run to
# completion, so a broken config or a package that does not compile can never
# be mistaken for a clean tree.
run_full() {
	_dir="$1"
	_name="$2"
	_report="$REPORTS/$_name.json"
	rm -f "$_report"
	set +e
	(
		cd "$_dir" &&
			"$GCL" run --allow-parallel-runners --timeout=10m \
				--max-issues-per-linter=0 --max-same-issues=0 \
				--output.json.path="$_report" ./...
	)
	_rc=$?
	set -e
	# 0 = clean, 1 = findings. Every other code (bad config, typecheck failure,
	# timeout) means the linter did not produce a verdict.
	if [ "$_rc" -ne 0 ] && [ "$_rc" -ne 1 ]; then
		echo "FAIL: golangci-lint exited $_rc in $_dir — it did not run to completion"
		return 1
	fi
	python3 scripts/count-findings.py golangci "$_report" >"$REPORTS/$_name.count"
}

echo "--- golangci-lint, full tree (soak) ---"
run_full . root
run_full sdks/go sdks-go
root_count=$(cat "$REPORTS/root.count")
sdk_count=$(cat "$REPORTS/sdks-go.count")
total=$((root_count + sdk_count))
echo "golangci-lint findings: root=$root_count sdks/go=$sdk_count total=$total"
sh scripts/ratchet.sh "go-lint" "$BASELINE_FILE" "$total" "$ENFORCE_AT"

echo "--- golangci-lint, diff-scoped ---"
event="${CI_PIPELINE_EVENT:-${GITHUB_EVENT_NAME:-}}"
base="${CI_COMMIT_TARGET_BRANCH:-${GITHUB_BASE_REF:-}}"
case "$event" in
pull_request | pull_request_closed)
	if [ -z "$base" ]; then
		echo "FAIL: event is '$event' but no target branch was provided; the diff-scoped lint cannot be scoped"
		exit 1
	fi
	if ! git rev-parse --verify -q "origin/$base" >/dev/null; then
		echo "FAIL: origin/$base is not in this clone; fetch it before linting"
		exit 1
	fi
	if [ "$(git merge-base HEAD "origin/$base")" = "$(git rev-parse HEAD)" ]; then
		echo "FAIL: the merge base with origin/$base is HEAD itself, so a diff-scoped run would examine nothing"
		exit 1
	fi
	"$GCL" run --allow-parallel-runners --timeout=10m --new-from-merge-base="origin/$base" ./...
	(cd sdks/go && "$GCL" run --allow-parallel-runners --timeout=10m --new-from-merge-base="origin/$base" ./...)
	echo "diff-scoped lint: OK (against origin/$base)"
	;;
*)
	echo "diff-scoped lint: not applicable to event '${event:-<none>}'; the full-tree ratchet above examined this commit"
	;;
esac
