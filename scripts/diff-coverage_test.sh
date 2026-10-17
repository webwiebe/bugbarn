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

# The fixtures list one or two files, so the profile floor is lowered for every
# case except the one that checks the committed default.
DIFF_COVERAGE_PROFILE_FILE_FLOOR=1
export DIFF_COVERAGE_PROFILE_FILE_FLOOR

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
# First commit (lines 2-13) uncovered, second (lines 14-17) covered: 25%.
# The last commit alone has 4 lines and would be exempt, so a gate that only
# looked at it would pass.
r=$(new_repo multi)
add_lines "$r" a.go 12
add_lines "$r" a.go 4
{
  echo 'mode: atomic'
  l=2
  while [ "$l" -le 17 ]; do
    if [ "$l" -le 13 ]; then c=0; else c=1; fi
    printf 'example.test/m/a.go:%s.1,%s.20 1 %s\n' "$l" "$l" "$c"
    l=$((l + 1))
  done
} > "$r/cover.out"
run "$r" --enforce
if [ "$code" -eq 1 ] && grep -q '25.0% (4 of 16' "$WORK/out"; then ok "multi-commit branch is measured from the merge base"; else bad "multi-commit branch is measured from the merge base"; fi

# 4b. The goal is 80 percent: 9 of 10 passes, 7 of 10 fails.
r=$(new_repo half)
add_lines "$r" a.go 10
{
  echo 'mode: atomic'
  l=2
  while [ "$l" -le 11 ]; do
    if [ "$l" -le 10 ]; then c=1; else c=0; fi
    printf 'example.test/m/a.go:%s.1,%s.20 1 %s\n' "$l" "$l" "$c"
    l=$((l + 1))
  done
} > "$r/cover.out"
run "$r" --enforce
if [ "$code" -eq 0 ] && grep -q '90.0% (9 of 10' "$WORK/out"; then ok "90 percent clears the 80 percent goal"; else bad "90 percent clears the 80 percent goal"; fi
r=$(new_repo low)
add_lines "$r" a.go 10
{
  echo 'mode: atomic'
  l=2
  while [ "$l" -le 11 ]; do
    if [ "$l" -le 8 ]; then c=1; else c=0; fi
    printf 'example.test/m/a.go:%s.1,%s.20 1 %s\n' "$l" "$l" "$c"
    l=$((l + 1))
  done
} > "$r/cover.out"
run "$r" --enforce
if [ "$code" -eq 1 ] && grep -q '70.0% (7 of 10' "$WORK/out"; then ok "70 percent misses the 80 percent goal"; else bad "70 percent misses the 80 percent goal"; fi

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

# run_with <repo> <profile> <base> [args] — like run, with explicit PROFILE and BASE.
run_with() {
  repo="$1"
  prof="$2"
  base="$3"
  shift 3
  set +e
  (cd "$repo" && sh "$GATE" "$@" "$prof" "$base" > "$WORK/out" 2>&1)
  code=$?
  set -e
}

# 7. A measurement that cannot be made fails in report mode too. A broken
# measurement must go red; it must never read as an exempt diff.
r=$(new_repo broken)
add_lines "$r" a.go 12
profile "$r" a.go 2 13 1

run_with "$r" missing.out main
if [ "$code" -eq 3 ] && grep -q 'missing or empty' "$WORK/out"; then ok "missing profile fails in report mode"; else bad "missing profile fails in report mode"; fi

: > "$r/empty.out"
run_with "$r" empty.out main
if [ "$code" -eq 3 ] && grep -q 'missing or empty' "$WORK/out"; then ok "empty profile fails in report mode"; else bad "empty profile fails in report mode"; fi

printf 'not a profile\n' > "$r/junk.out"
run_with "$r" junk.out main
if [ "$code" -eq 3 ] && grep -q 'no mode header' "$WORK/out"; then ok "profile without a mode header fails in report mode"; else bad "profile without a mode header fails in report mode"; fi

run_with "$r" cover.out no-such-ref
if [ "$code" -eq 3 ] && grep -q 'does not resolve' "$WORK/out"; then ok "unknown base ref fails in report mode"; else bad "unknown base ref fails in report mode"; fi

run_with "$r" cover.out ""
if [ "$code" -eq 3 ] && grep -q 'does not resolve' "$WORK/out"; then ok "empty base ref fails in report mode"; else bad "empty base ref fails in report mode"; fi

run_with "$r" missing.out main --enforce
if [ "$code" -eq 3 ]; then ok "the same errors fail when enforced"; else bad "the same errors fail when enforced"; fi

# 8. A merge-queue group gives the base as a commit sha (merge_group.base_sha),
# not as a branch name, and the head is the queued commit. The sha form must
# measure exactly like the branch form.
r=$(new_repo queue)
base_sha=$(git -C "$r" rev-parse main)
add_lines "$r" a.go 12
profile "$r" a.go 2 13 0
run_with "$r" cover.out "$base_sha" --enforce
if [ "$code" -eq 1 ] && grep -q '0.0% (0 of 12' "$WORK/out"; then ok "a base given as a commit sha is measured"; else bad "a base given as a commit sha is measured"; fi
run_with "$r" cover.out "$base_sha"
if [ "$code" -eq 0 ] && grep -q '::warning::' "$WORK/out"; then ok "low coverage on a sha base only warns in report mode"; else bad "low coverage on a sha base only warns in report mode"; fi

# add_func <repo> <file> <body-lines> — new file with one function of <body-lines>
# statements, committed. The file has 4 + <body-lines> lines.
add_func() {
  {
    printf 'package m\n\nfunc f() {\n'
    i=0
    while [ "$i" -lt "$3" ]; do
      printf '\t_ = %s\n' "$i"
      i=$((i + 1))
    done
    printf '}\n'
  } > "$1/$2"
  git -C "$1" add -A
  git -C "$1" commit -q -m "func in $2"
}

# 9. The input floors exit 3 in report mode. A gate that measured nothing must
# go red; it must never read as an exempt diff.

# 9a. The committed floor applies when nothing overrides it.
r=$(new_repo floor)
add_lines "$r" a.go 12
profile "$r" a.go 2 13 1
set +e
(cd "$r" && env -u DIFF_COVERAGE_PROFILE_FILE_FLOOR sh "$GATE" cover.out main > "$WORK/out" 2>&1)
code=$?
set -e
if [ "$code" -eq 3 ] && grep -q 'lists 1 repository files, floor 210' "$WORK/out"; then ok "a profile under the committed file floor fails in report mode"; else bad "a profile under the committed file floor fails in report mode"; fi

# 9b. A module path that no longer matches go.mod maps no file at all.
r=$(new_repo renamed)
add_lines "$r" a.go 12
profile "$r" a.go 2 13 1
sed 's|example.test/m/|example.test/old/|' "$r/cover.out" > "$r/old.out"
run_with "$r" old.out main
if [ "$code" -eq 3 ] && grep -q 'lists 0 repository files' "$WORK/out"; then ok "a profile of another module path fails in report mode"; else bad "a profile of another module path fails in report mode"; fi

# 9c. A changed file with a function and no block in the profile fails once it
# holds MIN_LINES changed lines.
r=$(new_repo unlisted)
add_lines "$r" a.go 12
add_func "$r" b.go 8
profile "$r" a.go 2 13 1
run "$r"
if [ "$code" -eq 3 ] && grep -q 'not measured: b.go (12 changed lines' "$WORK/out" && grep -q '12 changed lines are in files that add a function' "$WORK/out"; then ok "an unprofiled file with a function fails in report mode"; else bad "an unprofiled file with a function fails in report mode"; fi

# 9d. Under MIN_LINES it is named and passes: a small fix in a module without a
# profile stays green.
r=$(new_repo unlisted-small)
add_lines "$r" a.go 12
add_func "$r" b.go 2
profile "$r" a.go 2 13 1
run "$r" --enforce
if [ "$code" -eq 0 ] && grep -q 'not measured: b.go (6 changed lines' "$WORK/out" && grep -q '100.0%' "$WORK/out"; then ok "a small unprofiled change is named and passes"; else bad "a small unprofiled change is named and passes"; fi

# 9e. A file without a function has no statements and no block. It is not a gap.
r=$(new_repo typesonly)
add_lines "$r" a.go 12
add_lines "$r" types.go 12
profile "$r" a.go 2 13 1
run "$r" --enforce
if [ "$code" -eq 0 ] && ! grep -q 'not measured' "$WORK/out"; then ok "a file without a function is not counted as unmeasured"; else bad "a file without a function is not counted as unmeasured"; fi

# 9f. A change with no Go lines at all is exempt, and still checks the profile.
r=$(new_repo nogo)
printf 'x\n' > "$r/README.md"
git -C "$r" add -A
git -C "$r" commit -q -m readme
profile "$r" a.go 1 1 1
run "$r" --enforce
if [ "$code" -eq 0 ] && grep -q 'Exempt' "$WORK/out"; then ok "a change without Go lines is exempt"; else bad "a change without Go lines is exempt"; fi

# 10. A nested module (sdks/go in this repository) is measured through its own
# module path, from profiles concatenated into one file.
r=$(new_repo nested)
mkdir -p "$r/sub"
printf 'module example.test/sub\n\ngo 1.26\n' > "$r/sub/go.mod"
printf 'package sub\n' > "$r/sub/x.go"
git -C "$r" add -A
git -C "$r" commit -q -m "nested module"
add_lines "$r" sub/x.go 12
profile "$r" a.go 1 1 1
{
  echo 'mode: atomic'
  l=2
  while [ "$l" -le 13 ]; do
    printf 'example.test/sub/x.go:%s.1,%s.20 1 0\n' "$l" "$l"
    l=$((l + 1))
  done
} >> "$r/cover.out"
run "$r" --enforce
if [ "$code" -eq 1 ] && grep -q 'uncovered: sub/x.go:2' "$WORK/out" && grep -q '0.0% (0 of 12' "$WORK/out"; then ok "a nested module is measured from its own profile"; else bad "a nested module is measured from its own profile"; fi

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
