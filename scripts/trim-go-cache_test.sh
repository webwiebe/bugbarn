#!/usr/bin/env bash
# Tests for scripts/trim-go-cache.sh, and for the quality step that runs it.
# A cache under the cap is left alone; one over the cap loses its oldest files
# until it is at 80% of the cap, and Go's README and trim.txt stay. A missing
# directory and a file that cannot be removed both exit 0.
#
# Run it with: bash scripts/trim-go-cache_test.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$REPO/scripts/trim-go-cache.sh"
WORK="$(mktemp -d)"
trap 'chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT

failures=0
check() { # name, exit status of the assertion just made
  if [ "$2" -eq 0 ]; then echo "ok   $1"; else echo "FAIL $1"; failures=$((failures + 1)); fi
}

# entry <dir> <name> <MiB> <touch -t stamp>: a cache file of that size and mtime.
entry() {
  mkdir -p "$1/${2:0:2}"
  dd if=/dev/zero of="$1/${2:0:2}/$2" bs=1048576 count="$3" 2>/dev/null
  touch -t "$4" "$1/${2:0:2}/$2"
}

cache="$WORK/under"
entry "$cache" aa1-d 1 202611010000
entry "$cache" bb1-d 1 202611020000
rc=0; sh "$SCRIPT" "$cache" 4 >"$WORK/out" 2>&1 || rc=$?
{ [ "$rc" -eq 0 ] && [ "$(find "$cache" -type f | wc -l | tr -d ' ')" = 2 ] && grep -q "2 MiB, cap 4 MiB" "$WORK/out"; } && s=0 || s=1
check "a cache under the cap is left alone" "$s"

# Six 1 MiB entries, oldest first, over a 4 MiB cap: trimming stops at 80% of
# the cap (3.2 MiB), so the three newest stay.
cache="$WORK/over"
for i in 1 2 3 4 5 6; do entry "$cache" "a${i}0-d" 1 "20261101000$i"; done
printf 'This directory holds cached build artifacts from the Go build system.\n' >"$cache/README"
printf '1793000000\n' >"$cache/trim.txt"
touch -t 202601010000 "$cache/README" "$cache/trim.txt"
rc=0; sh "$SCRIPT" "$cache" 4 >"$WORK/out" 2>&1 || rc=$?
left="$(cd "$cache" && find . -type f -name '*-d' | sort | tr '\n' ' ')"
{ [ "$rc" -eq 0 ] && [ "$left" = "./a4/a40-d ./a5/a50-d ./a6/a60-d " ]; } && s=0 || s=1
check "a cache over the cap keeps its newest files (left: $left)" "$s"
{ [ -f "$cache/README" ] && [ -f "$cache/trim.txt" ]; } && s=0 || s=1
check "README and trim.txt stay" "$s"
grep -q "trimmed from 6 MiB to 3 MiB, cap 4 MiB" "$WORK/out" && s=0 || s=1
check "the summary names the sizes before and after" "$s"

rc=0; sh "$SCRIPT" "$WORK/missing" 4 >/dev/null 2>&1 || rc=$?
check "a missing directory exits 0" "$rc"

cache="$WORK/locked"
for i in 1 2 3; do entry "$cache" "b${i}0-d" 2 "20261101000$i"; done
chmod a-w "$cache/b1"
rc=0; sh "$SCRIPT" "$cache" 4 >/dev/null 2>"$WORK/err" || rc=$?
{ [ "$rc" -eq 0 ] && grep -q "warning" "$WORK/err"; } && s=0 || s=1
check "a file that cannot be removed warns and exits 0" "$s"
chmod u+w "$cache/b1"

# The quality step trims its GOCACHE on exit, pass or fail.
grep -q "trap 'sh scripts/trim-go-cache.sh \"\$GOCACHE\" " "$REPO/.woodpecker/ci.yaml" && s=0 || s=1
check "the quality step trims GOCACHE on exit" "$s"
grep -q "bash scripts/trim-go-cache_test.sh" "$REPO/.woodpecker/ci.yaml" && s=0 || s=1
check "the quality step runs these tests" "$s"

echo "$(( 8 - failures )) passed, $failures failed"
[ "$failures" -eq 0 ]
