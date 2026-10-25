#!/usr/bin/env bash
#
# secret-scan.test.sh — proves scripts/secret-scan.sh fires on a planted fake secret
# in every mode, and refuses to pass when it could not scan.
#
# The fake secret is built at run time from random characters and lives only in a
# throwaway repository under a temp directory, so it is never committed here.
#
# Needs the pinned gitleaks on PATH (or GITLEAKS_BIN). Run: bash scripts/tests/secret-scan.test.sh
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
SCAN="$HERE/../secret-scan.sh"
CONFIG="$HERE/../../.gitleaks.toml"
export SECRET_SCAN_MIN_COMMITS=2

pass=0
fail=0
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

check() { # name expected-rc actual-rc
  if [ "$2" = "$3" ]; then pass=$((pass + 1)); echo "ok   $1"; else
    fail=$((fail + 1)); echo "FAIL $1: expected exit $2, got $3"
    printf '%s\n' "${OUT:-}" | sed "s/^/     | /" | tail -40; fi
}
has() { # name needle haystack
  case "$3" in *"$2"*) pass=$((pass + 1)); echo "ok   $1" ;; *) fail=$((fail + 1)); echo "FAIL $1: output lacks '$2'" ;; esac
}

run() { # dir mode args...  (sets OUT and RC)
  local dir="$1" mode="$2"; shift 2
  OUT=$(cd "$dir" && SECRET_SCAN_MODE="$mode" bash "$SCAN" "$@" 2>&1)
  RC=$?
}

new_repo() {
  local d="$T/$1"
  mkdir -p "$d"
  (
    cd "$d" && git init -q . && git config user.email t@example.invalid && git config user.name t && git config gc.auto 0 && git config maintenance.auto false
    cp "$CONFIG" .gitleaks.toml
    echo base > a.txt
    git add a.txt .gitleaks.toml && git commit -q -m base
  )
  echo "$d"
}

commit() { (cd "$1" && git add -A && git commit -q -m "$2"); }

# A finite read: `tr </dev/urandom | head` never exits on the runner, where SIGPIPE is ignored.
TOKEN="ghp_$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"

# ---- repo with a secret committed and then removed (history keeps it)
R=$(new_repo planted)
echo "token=$TOKEN" > "$R/leak.env"
commit "$R" "add leak"
BASE_CLEAN=$(cd "$R" && git rev-parse HEAD~1)
(cd "$R" && git rm -q leak.env) && commit "$R" "remove leak"

run "$R" enforce history;            check "history, enforce: planted secret fails" 1 "$RC"
run "$R" report history;             check "history, report: planted secret exits 0" 0 "$RC"
has "history, report: finding is counted" "findings=1" "$OUT"
has "history, report: finding is annotated" "::warning" "$OUT"
case "$OUT" in *"$TOKEN"*) fail=$((fail + 1)); echo "FAIL output leaks the secret value" ;; *) pass=$((pass + 1)); echo "ok   output never shows the secret value" ;; esac

run "$R" enforce diff "$BASE_CLEAN"; check "diff, enforce: planted secret fails" 1 "$RC"
run "$R" report diff "$BASE_CLEAN";  check "diff, report: planted secret exits 0" 0 "$RC"
has "diff, report: finding is counted" "findings=1" "$OUT"

# ---- credentials written into documentation (the specs/016 shape) fire the custom rules
VAL=$(head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n')
SEED=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
RD=$(new_repo docs)
BASE_DOCS=$(cd "$RD" && git rev-parse HEAD)
printf '%s\n' "- \`EXAMPLE_INGEST_KEY\` — \`$VAL\`" > "$RD/spec.md"
commit "$RD" "document a credential bullet"
run "$RD" enforce diff "$BASE_DOCS"; check "diff, enforce: documented credential bullet fails" 1 "$RC"
has "diff: the bullet is one finding" "findings=1" "$OUT"
printf '%s\n' "The default seed is \`$SEED\` in code." > "$RD/spec.md"
commit "$RD" "document a seed in prose"
run "$RD" enforce history;           check "history, enforce: documented credential and seed fail" 1 "$RC"
has "history: bullet and seed are two findings" "findings=2" "$OUT"
case "$OUT" in *"$VAL"*|*"$SEED"*) fail=$((fail + 1)); echo "FAIL output leaks a documented value" ;; *) pass=$((pass + 1)); echo "ok   output never shows the documented values" ;; esac

# ---- a clean range after the leak passes in enforce mode
echo more >> "$R/a.txt"; commit "$R" "clean change"
run "$R" enforce diff HEAD~1;        check "diff, enforce: clean range passes" 0 "$RC"

# ---- the ignore list takes known history hits and can only shrink
FP=$(cd "$R" && git log --all --format=%H -n 1 --diff-filter=A -- leak.env)":leak.env:github-pat:1"
echo "$FP" > "$R/.gitleaksignore"; commit "$R" "baseline known hit"
run "$R" enforce history;            check "history, enforce: ignored known hit passes" 0 "$RC"
R2=$(new_repo growth)
echo "$FP" > "$R2/.gitleaksignore"; commit "$R2" "ignore list"
PRE=$(cd "$R2" && git rev-parse HEAD)
echo "extra:fingerprint:rule:1" >> "$R2/.gitleaksignore"; commit "$R2" "grow ignore list"
run "$R2" enforce diff "$PRE";       check "diff, enforce: ignore-list growth fails" 1 "$RC"
run "$R2" report diff "$PRE";        check "diff, report: ignore-list growth exits 0" 0 "$RC"
has "diff, report: growth is counted" "ignore-list-growth=1" "$OUT"

# ---- a scan that cannot run fails in EVERY mode
for mode in report enforce; do
  OUT=$(cd "$R" && GITLEAKS_BIN=/nonexistent/gitleaks SECRET_SCAN_MODE=$mode bash "$SCAN" history 2>&1); RC=$?
  check "$mode: missing gitleaks is exit 2" 2 "$RC"
  OUT=$(cd "$R" && GITLEAKS_VERSION=0.0.1 SECRET_SCAN_MODE=$mode bash "$SCAN" history 2>&1); RC=$?
  check "$mode: wrong gitleaks version is exit 2" 2 "$RC"
  OUT=$(cd "$R" && SECRET_SCAN_MIN_COMMITS=1000 SECRET_SCAN_MODE=$mode bash "$SCAN" history 2>&1); RC=$?
  check "$mode: history below the commit floor is exit 2" 2 "$RC"
  run "$R" "$mode" diff HEAD;          check "$mode: empty diff range is exit 2" 2 "$RC"
  run "$R" "$mode" diff no-such-ref;   check "$mode: unknown base ref is exit 2" 2 "$RC"
done
git clone -q --depth 1 "file://$R" "$T/shallow" 2>/dev/null
run "$T/shallow" report history;     check "report: shallow clone is exit 2" 2 "$RC"
run "$T/shallow" enforce history;    check "enforce: shallow clone is exit 2" 2 "$RC"
run "$R" bogus history;              check "unknown mode is exit 2" 2 "$RC"

echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
