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
# Every Go module in the repository counts (the root and sdks/go). PROFILE
# holds the profiles of all of them, concatenated; each profile path is mapped
# back to a repository path through the module path in its go.mod.
#
# Report only until --enforce is passed: low coverage prints a warning and exits 0.
# A measurement that cannot be made exits 3 in both modes. That covers no
# profile, an unknown base ref and a git error, and two input floors, because
# a gate that measured nothing would otherwise print "Exempt":
#   - the profile lists fewer than PROFILE_FILE_FLOOR repository files (a cut
#     short profile, or a module path that no longer matches go.mod);
#   - MIN_LINES or more changed lines sit in files that add a function with a
#     body but have no block in the profile at all (a module whose profile is
#     missing). Fewer such lines are printed as not measured and pass, so a
#     small fix in a module without a profile does not go red.
#
# Usage: scripts/diff-coverage.sh [--enforce] PROFILE BASE
#   PROFILE  coverage/diff.out, written by scripts/diff-coverage-profile.sh
#            from check-coverage.sh's coverage/go.out plus sdks/go
#   BASE     ref to diff against, e.g. origin/main
#
# Portable POSIX sh, same as the other gates.
set -eu

THRESHOLD="${DIFF_COVERAGE_THRESHOLD:-80}"
MIN_LINES="${DIFF_COVERAGE_MIN_LINES:-10}"
# Measured on 2026-10-17: the root module has 229 production files with a
# function and sdks/go has 6, so a full profile lists about 235. Raise this as
# the code grows; lowering it shows in the diff.
PROFILE_FILE_FLOOR="${DIFF_COVERAGE_PROFILE_FILE_FLOOR:-210}"

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

# A measurement that cannot be made is an error in every mode, report only
# included: it exits 3 and never reads as an exempt diff. Only a low coverage
# result is softened without --enforce.
unmeasurable() {
  echo "::error::Diff coverage could not be measured: $1" >&2
  exit 3
}

cd "$(git rev-parse --show-toplevel)" || unmeasurable "not inside a git work tree"
MODULE=$(awk '$1 == "module" {print $2; exit}' go.mod 2>/dev/null) || MODULE=""
[ -n "$MODULE" ] || unmeasurable "no module path in go.mod"
[ -s "$PROFILE" ] || unmeasurable "coverage profile $PROFILE is missing or empty"
case "$(head -n 1 "$PROFILE")" in
  mode:*) ;;
  *) unmeasurable "coverage profile $PROFILE has no mode header" ;;
esac
git rev-parse --verify --quiet "$BASE^{commit}" > /dev/null ||
  unmeasurable "base ref $BASE does not resolve to a commit"

CHANGED=$(mktemp)
DIFF=$(mktemp)
MODULES=$(mktemp)
FUNCS=$(mktemp)
trap 'rm -f "$CHANGED" "$DIFF" "$MODULES" "$FUNCS"' EXIT

# "<module path> <directory>" for every go.mod in the repository.
git ls-files -- go.mod '*/go.mod' | while IFS= read -r gomod; do
  mod=$(awk '$1 == "module" {print $2; exit}' "$gomod")
  [ -n "$mod" ] && printf '%s %s\n' "$mod" "$(dirname "$gomod")"
done > "$MODULES"

git diff -U0 --no-color --no-ext-diff "$BASE...HEAD" -- '*.go' > "$DIFF" ||
  unmeasurable "git diff $BASE...HEAD failed (no merge base?)"

# "<file> <line>" for every added line of a production Go file.
awk '
    /^\+\+\+ / { file = ($2 == "/dev/null") ? "" : substr($2, 3); next }
    /^@@ / {
      if (file == "") next
      plus = $3; sub(/^\+/, "", plus)
      n = split(plus, p, ",")
      start = p[1] + 0; count = (n > 1) ? p[2] + 0 : 1
      for (i = 0; i < count; i++) print file, start + i
    }
  ' "$DIFF" |
  grep -vE '^[^ ]*(_test\.go|_templ\.go|\.pb\.go|_gen\.go) ' |
  grep -vE '^([^ ]*/)?(testdata|migrations|vendor)/' > "$CHANGED" || true

# Files whose added lines open a function with a body. Such a file has at
# least one statement, so a full profile has a block for it.
awk '
    /^\+\+\+ / { file = ($2 == "/dev/null") ? "" : substr($2, 3); next }
    file != "" && /^\+func .*\{/ && !/\{\}[[:space:]]*$/ { print file }
  ' "$DIFF" | sort -u > "$FUNCS"

# The change list is read in BEGIN: an empty one (no Go file changed) must
# still leave every PROFILE line to the main rules.
RESULT=$(awk -v modules="$MODULES" -v funcs="$FUNCS" -v changed="$CHANGED" '
  BEGIN {
    nm = 0
    while ((getline line < modules) > 0) {
      split(line, f, " "); nm++; mpath[nm] = f[1] "/"; mdir[nm] = f[2]
    }
    while ((getline line < funcs) > 0) hasfunc[line] = 1
    while ((getline line < changed) > 0) { split(line, f, " "); chg[f[1], f[2]] = 1; chgfile[f[1]]++ }
  }
  /^mode:/ { next } # header of each concatenated profile
  {
    split($1, a, ":"); file = a[1]
    best = 0
    for (i = 1; i <= nm; i++)
      if (index(file, mpath[i]) == 1 && (best == 0 || length(mpath[i]) > length(mpath[best]))) best = i
    if (best == 0) next # not a file of this repository
    file = substr(file, length(mpath[best]) + 1)
    if (mdir[best] != ".") file = mdir[best] "/" file
    profiled[file] = 1
    split(a[2], r, ","); split(r[1], s, "."); split(r[2], e, ".")
    for (l = s[1] + 0; l <= e[1] + 0; l++) {
      if (!((file, l) in chg)) continue
      meas[file, l] = 1
      if ($3 + 0 > 0) cov[file, l] = 1
    }
  }
  END {
    m = 0; c = 0; p = 0; u = 0
    for (k in meas) {
      m++
      if (k in cov) c++
      else { split(k, kk, SUBSEP); print "MISS " kk[1] ":" kk[2] }
    }
    for (k in profiled) p++
    for (k in chgfile)
      if ((k in hasfunc) && !(k in profiled)) { u += chgfile[k]; print "UNLISTED " k " " chgfile[k] }
    print "TOTAL " m " " c " " p " " u
  }
' "$PROFILE")

echo "$RESULT" | grep '^MISS ' | sort -t: -k1,1 -k2,2n | sed 's/^MISS /  uncovered: /' || true
set -- $(echo "$RESULT" | awk '/^TOTAL/ {print $2, $3, $4, $5}')
measurable="$1"
covered="$2"
profiled="$3"
unlisted="$4"

if [ "$profiled" -lt "$PROFILE_FILE_FLOOR" ]; then
  unmeasurable "the profile lists $profiled repository files, floor $PROFILE_FILE_FLOOR (a cut short profile, or a module path that no longer matches go.mod)"
fi
echo "Profile: $profiled repository files (floor $PROFILE_FILE_FLOOR)."
if [ "$unlisted" -gt 0 ]; then
  echo "$RESULT" | awk '/^UNLISTED/ {print "  not measured: " $2 " (" $3 " changed lines, no block in the profile)"}' | sort
  if [ "$unlisted" -ge "$MIN_LINES" ]; then
    unmeasurable "$unlisted changed lines are in files that add a function but have no block in the profile (is a module missing from PROFILE?)"
  fi
fi

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
