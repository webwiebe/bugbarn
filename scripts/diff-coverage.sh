#!/bin/sh
#
# diff-coverage.sh — coverage of the Go lines a change touches.
#
# Reads a Go text-format coverage profile and the lines added between the merge
# base of BASE and HEAD (`git diff BASE...HEAD`, so every commit of a
# multi-commit PR counts). Of the added lines that sit inside a coverable
# statement block, the share that a test executed must reach THRESHOLD percent.
# Diffs with fewer than MIN_LINES coverable lines are exempt: one or two
# changed lines swing the percentage too much to judge.
#
# Test files, generated code, migrations and testdata are not measured.
#
# Report only until --enforce is passed: a miss prints a warning and exits 0.
#
# Usage: scripts/diff-coverage.sh [--enforce] PROFILE BASE
#   PROFILE  merged text profile (check-coverage.sh writes coverage/go.out;
#            use that)
#   BASE     ref to diff against, e.g. origin/main
#
# Portable POSIX sh, same as the other gates.
set -eu

THRESHOLD="${DIFF_COVERAGE_THRESHOLD:-80}"
MIN_LINES="${DIFF_COVERAGE_MIN_LINES:-10}"

enforce=0
if [ "${1:-}" = "--enforce" ]; then
  enforce=1
  shift
fi
if [ "$#" -ne 2 ]; then
  echo "usage: $0 [--enforce] PROFILE BASE" >&2
  exit 2
fi
PROFILE="$1"
BASE="$2"

cd "$(git rev-parse --show-toplevel)"
MODULE=$(awk 'NR==1 {print $2}' go.mod)

CHANGED=$(mktemp)
trap 'rm -f "$CHANGED"' EXIT

# "<file> <line>" for every added line of a production Go file.
git diff -U0 --no-color --no-ext-diff "$BASE...HEAD" -- '*.go' |
  awk '
    /^\+\+\+ / { file = ($2 == "/dev/null") ? "" : substr($2, 3); next }
    /^@@ / {
      if (file == "") next
      plus = $3; sub(/^\+/, "", plus)
      n = split(plus, p, ",")
      start = p[1] + 0; count = (n > 1) ? p[2] + 0 : 1
      for (i = 0; i < count; i++) print file, start + i
    }
  ' |
  grep -vE '^[^ ]*(_test\.go|_templ\.go|\.pb\.go|_gen\.go) ' |
  grep -vE '^([^ ]*/)?(testdata|migrations|vendor)/' > "$CHANGED" || true

RESULT=$(awk -v mod="${MODULE}/" '
  FNR == NR { chg[$1, $2] = 1; next }
  FNR == 1 { next } # "mode:" header
  {
    split($1, a, ":"); file = a[1]
    if (index(file, mod) == 1) file = substr(file, length(mod) + 1)
    split(a[2], r, ","); split(r[1], s, "."); split(r[2], e, ".")
    for (l = s[1] + 0; l <= e[1] + 0; l++) {
      if (!((file, l) in chg)) continue
      meas[file, l] = 1
      if ($3 + 0 > 0) cov[file, l] = 1
    }
  }
  END {
    m = 0; c = 0
    for (k in meas) {
      m++
      if (k in cov) c++
      else { split(k, kk, SUBSEP); print "MISS " kk[1] ":" kk[2] }
    }
    print "TOTAL " m " " c
  }
' "$CHANGED" "$PROFILE")

echo "$RESULT" | grep '^MISS ' | sort -t: -k1,1 -k2,2n | sed 's/^MISS /  uncovered: /' || true
set -- $(echo "$RESULT" | awk '/^TOTAL/ {print $2, $3}')
measurable="$1"
covered="$2"

if [ "$measurable" -lt "$MIN_LINES" ]; then
  echo "Diff coverage: $measurable coverable changed line(s), under the $MIN_LINES-line minimum. Exempt."
  exit 0
fi

pct=$(awk -v c="$covered" -v m="$measurable" 'BEGIN { printf "%.1f", c * 100.0 / m }')
echo "Diff coverage: ${pct}% ($covered of $measurable changed lines), threshold ${THRESHOLD}%."
if awk -v p="$pct" -v t="$THRESHOLD" 'BEGIN { exit !(p + 0 < t + 0) }'; then
  if [ "$enforce" -eq 1 ]; then
    echo "::error::Diff coverage ${pct}% is below ${THRESHOLD}%. Add tests for the uncovered lines above."
    exit 1
  fi
  echo "::warning::Diff coverage ${pct}% is below ${THRESHOLD}% (report only, not failing)."
fi
exit 0
