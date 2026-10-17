#!/bin/sh
# Typed-boundaries gate (report-only until the enforce date).
#
# CLAUDE.md "Error boundaries": each layer owns its error domain. Storage wraps
# DB errors into apperr types, services pass them through, the API maps apperr
# codes to HTTP status. Two ways to break that are counted here:
#
#   raw-db     a non-test file outside internal/storage and internal/telemetrydb
#              imports database/sql or names sql.Err*. A raw DB error reached a
#              layer that should only see apperr types.
#   stringly   a non-test file in internal/api or internal/service matches on
#              err.Error() text (strings.Contains/HasPrefix/HasSuffix or ==).
#              Callers must use errors.Is/As against apperr, not message text.
#
# Usage: check-typed-boundaries.sh [repo-root] [today]
#   repo-root  defaults to the current directory
#   today      YYYY-MM-DD, defaults to the system date. Only the tests pass it,
#              to exercise both modes of the date switch.
#
# Contract:
#   - Input floor: if fewer than MIN_FILES non-test Go files are found in the
#     scanned layers, the gate checked nothing and FAILS in both modes.
#   - Baseline: scripts/typed-boundaries-baseline.txt holds the accepted count.
#   - Before ENFORCE_ON a count different from the baseline is printed as a
#     REPORT and the exit code is 0. From ENFORCE_ON it fails, as the ratchet
#     does (above baseline = new debt, below baseline = stale baseline).
set -eu

ENFORCE_ON="2026-10-31"
MIN_FILES=30
ROOT="${1:-.}"
TODAY="${2:-$(date +%Y-%m-%d)}"

cd "$ROOT"
BASELINE="scripts/typed-boundaries-baseline.txt"

nontest() { grep -v '_test\.go$' || true; }

scanned=$(find internal/api internal/service internal/storage internal/telemetrydb -name '*.go' 2>/dev/null | nontest | wc -l | tr -d ' ')
if [ "$scanned" -lt "$MIN_FILES" ]; then
	echo "FAIL (typed-boundaries): scanned $scanned non-test Go files, floor is $MIN_FILES — the gate checked nothing"
	exit 1
fi

raw=$(find internal cmd -name '*.go' 2>/dev/null | nontest |
	grep -v -e '^internal/storage/' -e '^internal/telemetrydb/' |
	xargs grep -nE '"database/sql"|\bsql\.Err[A-Za-z]*' 2>/dev/null || true)
str=$(find internal/api internal/service -name '*.go' 2>/dev/null | nontest |
	xargs grep -nE 'strings\.(Contains|HasPrefix|HasSuffix)\([a-zA-Z_.]*[eE]rr\.Error\(\)|[eE]rr\.Error\(\) *[!=]=' 2>/dev/null || true)

count_lines() { if [ -z "$1" ]; then echo 0; else printf '%s\n' "$1" | wc -l | tr -d ' '; fi; }
n_raw=$(count_lines "$raw")
n_str=$(count_lines "$str")
count=$((n_raw + n_str))

echo "typed boundaries: raw-db=$n_raw stringly=$n_str total=$count (scanned $scanned files, enforce on $ENFORCE_ON)"
[ -z "$raw" ] || printf '%s\n' "$raw" | sed 's/^/  raw-db:   /'
[ -z "$str" ] || printf '%s\n' "$str" | sed 's/^/  stringly: /'

if [ ! -f "$BASELINE" ]; then
	echo "FAIL (typed-boundaries): no baseline at $BASELINE"
	echo "  create it with: echo $count > $BASELINE"
	exit 1
fi
base=$(cat "$BASELINE")
case "$base" in
'' | *[!0-9]*)
	echo "FAIL (typed-boundaries): baseline '$base' is not a number"
	exit 1
	;;
esac

if [ "$count" -eq "$base" ]; then
	echo "OK (typed-boundaries): $count matches the baseline"
	exit 0
fi

if [ "$count" -gt "$base" ]; then
	msg="$count findings, baseline $base: new typed-boundary debt"
else
	msg="$count findings, baseline $base: stale baseline, refresh with: echo $count > $BASELINE"
fi

# ISO dates compare correctly as strings.
if [ "$TODAY" \< "$ENFORCE_ON" ]; then
	echo "REPORT (typed-boundaries, report-only until $ENFORCE_ON): $msg"
	exit 0
fi
echo "FAIL (typed-boundaries): $msg"
exit 1
