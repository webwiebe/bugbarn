#!/bin/sh
# Go lint gate for both modules (root and sdks/go).
#
# Two legs, and the split is the whole point of this script:
#
#   1. FULL-TREE GATE — runs on every event, including a push to `main`.
#      Counts every finding in both modules and fails on any: the tree is
#      clean, and the count may not rise above zero.
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
# The full-tree count was a ratchet from a baseline file until it reached zero
# (#259). There is no baseline file now: a finding is a failure, and a new
# exception needs a //nolint with a reason on the line itself.
set -eu

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

echo "--- golangci-lint, full tree ---"
run_full . root
run_full sdks/go sdks-go
root_count=$(cat "$REPORTS/root.count")
sdk_count=$(cat "$REPORTS/sdks-go.count")
total=$((root_count + sdk_count))
echo "golangci-lint findings: root=$root_count sdks/go=$sdk_count total=$total"
case "$total" in
'' | *[!0-9]*)
	echo "FAIL (go-lint): measured count '$total' is not a number; the collector did not run"
	exit 1
	;;
esac
if [ "$total" -ne 0 ]; then
	echo "FAIL (go-lint): $total findings; the tree is clean and allows none."
	echo "  The findings are in $REPORT_DIR/root.json and $REPORT_DIR/sdks-go.json."
	exit 1
fi
echo "go-lint: OK (0 findings)"

# Import boundaries (depguard) are enforced at zero, not ratcheted with the
# rest: they encode the error and logging rules in CLAUDE.md, and selflog only
# reports what reaches slog.
boundaries=$(($(python3 scripts/count-findings.py golangci "$REPORTS/root.json" depguard) +
	$(python3 scripts/count-findings.py golangci "$REPORTS/sdks-go.json" depguard)))
if [ "$boundaries" -ne 0 ]; then
	echo "FAIL (boundaries): $boundaries depguard findings; the import boundaries allow none."
	echo "  The rules and their reasons are in .golangci.yml (linters.settings.depguard)."
	exit 1
fi
echo "boundaries (depguard): OK (0 findings)"

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
