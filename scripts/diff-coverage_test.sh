#!/bin/sh
#
# Tests for scripts/diff-coverage.sh.
#
# Each case builds a throwaway git repo with a main branch and a feature branch,
# writes a coverage profile by hand, and checks the gate's exit code and output.
# The cases that matter most are the ones where an uncovered change must fail
# under --enforce, and the multi-commit branch, where a diff against HEAD~1
# would count only the last commit.
#
# Portable POSIX sh, same as the gate it tests.
set -eu

GATE="$(cd "$(dirname "$0")" && pwd)/diff-coverage.sh"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0

ok() { pass=$((pass + 1)); echo "ok   - $1"; }
bad() { fail=$((fail + 1)); echo "FAIL - $1"; cat "$WORK/out"; }

# new_repo <name> — repo with go.mod and an initial commit on main.
new_repo() {
  repo="$WORK/$1"
  mkdir -p "$repo"
  git -C "$repo" init -q -b main
  git -C "$repo" config user.email t@example.test
  git -C "$repo" config user.name t
  printf 'module example.test/m\n\ngo 1.26\n' > "$repo/go.mod"
  printf 'package m\n' > "$repo/a.go"
  git -C "$repo" add -A
  git -C "$repo" commit -q -m init
  git -C "$repo" checkout -q -b feature
  printf '%s\n' "$repo"
}

# add_lines <repo> <file> <count> — append <count> one-line statements and commit.
add_lines() {
  i=0
  tag=$(git -C "$1" rev-list --count HEAD)
  while [ "$i" -lt "$3" ]; do
    printf 'var v%s_%s = %s\n' "$tag" "$i" "$i" >> "$1/$2"
    i=$((i + 1))
  done
  git -C "$1" add -A
  git -C "$1" commit -q -m "add $3 to $2"
}

# profile <repo> <file> <first-line> <last-line> <count> — one block per line.
profile() {
  l="$3"
  {
    echo 'mode: atomic'
    while [ "$l" -le "$4" ]; do
      printf 'example.test/m/%s:%s.1,%s.20 1 %s\n' "$2" "$l" "$l" "$5"
      l=$((l + 1))
    done
  } > "$1/cover.out"
}

# run <repo> [args] — run the gate inside the repo, keep output in $WORK/out.
run() {
  repo="$1"
  shift
  set +e
  (cd "$repo" && sh "$GATE" "$@" cover.out main > "$WORK/out" 2>&1)
  code=$?
  set -e
}

# 1. Fully covered diff passes under --enforce.
r=$(new_repo covered)
add_lines "$r" a.go 12
profile "$r" a.go 2 13 1
run "$r" --enforce
if [ "$code" -eq 0 ] && grep -q '100.0%' "$WORK/out"; then ok "covered diff passes when enforced"; else bad "covered diff passes when enforced"; fi

# 2. Uncovered diff fails under --enforce and names the lines.
r=$(new_repo uncovered)
add_lines "$r" a.go 12
profile "$r" a.go 2 13 0
run "$r" --enforce
if [ "$code" -eq 1 ] && grep -q 'uncovered: a.go:2' "$WORK/out" && grep -q '0.0%' "$WORK/out"; then ok "uncovered diff fails when enforced"; else bad "uncovered diff fails when enforced"; fi

# 3. The same diff is report only without --enforce.
run "$r"
if [ "$code" -eq 0 ] && grep -q '::warning::' "$WORK/out"; then ok "uncovered diff only warns in report mode"; else bad "uncovered diff only warns in report mode"; fi

# 4. A multi-commit branch counts every commit, not just the last one.
# First commit (lines 2-9) uncovered, second (lines 10-17) covered: 50%.
r=$(new_repo multi)
add_lines "$r" a.go 8
add_lines "$r" a.go 8
{
  echo 'mode: atomic'
  l=2
  while [ "$l" -le 17 ]; do
    if [ "$l" -le 9 ]; then c=0; else c=1; fi
    printf 'example.test/m/a.go:%s.1,%s.20 1 %s\n' "$l" "$l" "$c"
    l=$((l + 1))
  done
} > "$r/cover.out"
run "$r" --enforce
if [ "$code" -eq 1 ] && grep -q '50.0% (8 of 16' "$WORK/out"; then ok "multi-commit branch is measured from the merge base"; else bad "multi-commit branch is measured from the merge base"; fi

# 5. Diffs under the minimum are exempt, even when uncovered.
r=$(new_repo small)
add_lines "$r" a.go 3
profile "$r" a.go 2 4 0
run "$r" --enforce
if [ "$code" -eq 0 ] && grep -q 'Exempt' "$WORK/out"; then ok "diff under the minimum is exempt"; else bad "diff under the minimum is exempt"; fi

# 6. Test files are not measured.
r=$(new_repo testfile)
printf 'package m\n' > "$r/a_test.go"
git -C "$r" add -A
git -C "$r" commit -q -m "test file"
add_lines "$r" a_test.go 12
profile "$r" a_test.go 2 13 0
run "$r" --enforce
if [ "$code" -eq 0 ] && grep -q 'Exempt' "$WORK/out"; then ok "test files are not measured"; else bad "test files are not measured"; fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
