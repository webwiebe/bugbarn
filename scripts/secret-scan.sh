#!/usr/bin/env bash
#
# secret-scan.sh — scan git commits for committed secrets with gitleaks.
#
# Usage:
#   scripts/secret-scan.sh diff <base-ref>   # commits in <base-ref>..HEAD (pull requests)
#   scripts/secret-scan.sh history           # every commit reachable from HEAD (main, tags)
#
# Mode, set by the caller in SECRET_SCAN_MODE (default report):
#   report   findings are printed and annotated, the script exits 0.
#   enforce  a finding, or growth of the ignore list, exits 1.
# The mode is the exit code of this script. There is no continue-on-error anywhere.
#
# Exit codes:
#   0  clean, or findings in report mode
#   1  enforce mode: a finding, or a fingerprint added to .gitleaksignore
#   2  the scan could not run or proved nothing, in EVERY mode: gitleaks missing or
#      not the pinned version, shallow clone, unreadable range, gitleaks failing,
#      or fewer commits scanned than the floor / than git lists in the range.
#
# Ignore list: .gitleaksignore holds fingerprints only (commit:file:rule:line), never
# a secret value. It may only shrink: on a pull request, a fingerprint that the base
# branch does not already list is a violation. Known history hits are listed there
# when the file is first created (the base then has no file, which is allowed).
#
# Environment:
#   SECRET_SCAN_MODE         report | enforce
#   GITLEAKS_BIN             gitleaks binary (default: gitleaks on PATH, else the pinned
#                            ghcr.io/gitleaks/gitleaks image through docker)
#   GITLEAKS_VERSION         pinned version (default below)
#   SECRET_SCAN_MIN_COMMITS  floor for history mode (default 100); diff mode needs >= 1
#
# The planted-secret self-test is scripts/tests/secret-scan.test.sh.
set -uo pipefail

PINNED_VERSION="${GITLEAKS_VERSION:-8.30.1}"
GITLEAKS="${GITLEAKS_BIN:-gitleaks}"
MODE="${SECRET_SCAN_MODE:-report}"
MIN_HISTORY="${SECRET_SCAN_MIN_COMMITS:-100}"
LEAK_RC=42
IGNORE_FILE=".gitleaksignore"

die() { echo "secret-scan: CANNOT RUN: $*" >&2; exit 2; }

case "$MODE" in report | enforce) ;; *) die "SECRET_SCAN_MODE must be report or enforce, got '$MODE'" ;; esac

kind="${1:-}"
case "$kind" in
  diff) base="${2:-}"; [ -n "$base" ] || die "diff needs a base ref" ;;
  history) base="" ;;
  *) die "usage: secret-scan.sh diff <base-ref> | history" ;;
esac

root=$(git rev-parse --show-toplevel 2>/dev/null) || die "not inside a git repository"
cd "$root"

# gitleaks: the pinned binary when present, else the pinned image (the same pin
# brandtrace uses). GITLEAKS_BIN set explicitly never falls back.
use_docker=0
if [ -n "${GITLEAKS_BIN:-}" ] || command -v gitleaks >/dev/null 2>&1; then
  command -v "$GITLEAKS" >/dev/null 2>&1 || die "gitleaks not found ($GITLEAKS)"
  have=$("$GITLEAKS" version 2>/dev/null | tr -d 'v[:space:]')
  [ "$have" = "$PINNED_VERSION" ] || die "gitleaks version '$have' is not the pinned $PINNED_VERSION"
else
  command -v docker >/dev/null 2>&1 || die "neither gitleaks nor docker is available"
  use_docker=1
  # An empty config keeps docker away from the keychain credential helper; the image is public.
  DOCKER_CONFIG_DIR=$(mktemp -d)
  echo '{}' > "$DOCKER_CONFIG_DIR/config.json"
  export DOCKER_CONFIG="$DOCKER_CONFIG_DIR"
fi

run_gitleaks() {
  if [ "$use_docker" = 1 ]; then
    docker run --rm -v "$root:/repo" -w /repo --entrypoint sh \
      "ghcr.io/gitleaks/gitleaks:v$PINNED_VERSION" \
      -c 'git config --global --add safe.directory /repo && exec gitleaks git "$@"' sh "$@"
  else
    "$GITLEAKS" git "$@"
  fi
}

[ "$(git rev-parse --is-shallow-repository)" = "false" ] ||
  die "shallow clone: history is incomplete (checkout needs fetch-depth: 0)"

if [ "$kind" = diff ]; then
  range="$base..HEAD"
  git rev-parse --verify --quiet "$base^{commit}" >/dev/null || die "base ref '$base' not found"
  floor=1
else
  range="HEAD"
  floor="$MIN_HISTORY"
fi

expected=$(git rev-list --count "$range" 2>/dev/null) || die "cannot list commits in $range"
[ "$expected" -ge "$floor" ] || die "$expected commits in $range, floor is $floor"

# Ignore-list shrink check (pull requests only). New fingerprints are violations.
grown=0
if [ "$kind" = diff ] && git cat-file -e "$base:$IGNORE_FILE" 2>/dev/null; then
  added=$(comm -13 \
    <(git show "$base:$IGNORE_FILE" | grep -v '^#' | grep -v '^$' | sort -u) \
    <(grep -v '^#' "$IGNORE_FILE" 2>/dev/null | grep -v '^$' | sort -u))
  if [ -n "$added" ]; then
    grown=$(printf '%s\n' "$added" | wc -l | tr -d ' ')
    echo "secret-scan: $IGNORE_FILE gained $grown fingerprint(s); it may only shrink:" >&2
    printf '%s\n' "$added" | sed 's/^/  /' >&2
  fi
fi

log=$(mktemp)
trap 'rm -f "$log"; [ -z "${DOCKER_CONFIG_DIR:-}" ] || rm -rf "$DOCKER_CONFIG_DIR"' EXIT
# gitleaks can exit 0 with "0 commits scanned" after one of its own git calls failed
# ("[git] fatal: ..."), which the CI runner showed under load. Up to four attempts, a second apart, cover that;
# the last failure falls through to the checks below and is exit 2.
for attempt in 1 2 3 4; do
  run_gitleaks --no-banner --redact --exit-code "$LEAK_RC" \
    --config .gitleaks.toml --log-opts="$range" . 2>"$log"
  rc=$?
  grep -q '\[git\] fatal' "$log" || break
  echo "secret-scan: gitleaks reported a git error (attempt $attempt), retrying" >&2
  sleep "$attempt"
done
cat "$log" >&2
grep -q '\[git\] fatal' "$log" && die "gitleaks hit a git error on every attempt"

case "$rc" in
  0 | "$LEAK_RC") ;;
  *) die "gitleaks failed with exit code $rc" ;;
esac

scanned=$(sed -n 's/.*[^0-9]\([0-9][0-9]*\) commits scanned.*/\1/p' "$log" | tail -1)
[ -n "$scanned" ] || die "gitleaks did not report how many commits it scanned"
# gitleaks reads added lines, so a commit that only deletes lines (or only merges)
# is not counted. That makes scanned <= expected legitimate, but scanned == 0
# is only legitimate when no commit in the range adds a line.
added_lines=$(git log --numstat --format= "$range" | awk '{ a += $1 } END { print a + 0 }')
if [ "$added_lines" -gt 0 ]; then
  [ "$scanned" -ge 1 ] || die "range adds $added_lines lines but gitleaks scanned 0 commits"
fi
if [ "$kind" = history ]; then
  [ "$scanned" -ge "$floor" ] || die "gitleaks scanned $scanned commits, floor is $floor"
  [ $((scanned * 2)) -ge "$expected" ] ||
    die "gitleaks scanned $scanned of the $expected commits git lists; less than half"
fi

found=0
if [ "$rc" = "$LEAK_RC" ]; then
  found=$(sed -n 's/.*leaks found: \([0-9][0-9]*\).*/\1/p' "$log" | tail -1)
  found="${found:-1}"
fi

echo "secret-scan: mode=$MODE kind=$kind range=$range commits=$scanned findings=$found ignore-list-growth=$grown"

if [ "$found" -gt 0 ] || [ "$grown" -gt 0 ]; then
  if [ "$MODE" = enforce ]; then
    echo "secret-scan: FAILED ($found finding(s), $grown new ignore entries)" >&2
    exit 1
  fi
  echo "::warning title=secret-scan (report-only)::$found finding(s), $grown new ignore entries; the gate is not enforcing yet"
fi
exit 0
