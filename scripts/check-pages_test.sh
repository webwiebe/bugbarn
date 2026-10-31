#!/usr/bin/env bash
# Tests for deploy/scripts/check-pages.sh, and for the pipeline wiring it needs.
# The check only protects a release if it fails on a broken page and runs in the
# right place, so this runs the script against a stub curl and greps the pipelines.
#
# Run it with: bash scripts/check-pages_test.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$REPO/deploy/scripts/check-pages.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export PAGE_CHECK_DELAY=0

failures=0
check() { # name, exit status of the assertion just made
  if [ "$2" -eq 0 ]; then echo "ok   $1"; else echo "FAIL $1"; failures=$((failures + 1)); fi
}

mkdir -p "$WORK/bin"
cat > "$WORK/bin/curl" <<'STUB'
#!/bin/sh
# Answers from the environment. DOWN_CALLS requests get no answer first.
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -w|--max-time|-A) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
n=$(cat "$STUB_COUNT" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$STUB_COUNT"
if [ "$n" -le "${DOWN_CALLS:-0}" ]; then printf '000 0 '; exit 7; fi
case "$url" in
  */app/) printf '%s' "$APP_BODY" > "$out"; printf '%s %s text/html' "$APP_CODE" "${#APP_BODY}" ;;
  */dist/*) printf 'x' > "$out"; printf '%s %s %s' "$BUNDLE_CODE" "$BUNDLE_SIZE" "$BUNDLE_TYPE" ;;
  *) printf 'site' > "$out"; printf '%s 4 text/html' "$ROOT_CODE" ;;
esac
STUB
chmod +x "$WORK/bin/curl"

GOOD_APP='<form id="login-form"></form><script type="module" src="./dist/app-ABC123.js"></script>'
run() { # extra VAR=value assignments for this run
  rm -f "$WORK/count"
  env PATH="$WORK/bin:$PATH" STUB_COUNT="$WORK/count" ROOT_CODE=200 APP_CODE=200 APP_BODY="$GOOD_APP" \
    BUNDLE_CODE=200 BUNDLE_SIZE=100 BUNDLE_TYPE=text/javascript PAGE_CHECK_ATTEMPTS=2 "$@" \
    sh "$SCRIPT" https://bugbarn.example/ >"$WORK/out" 2>"$WORK/err"
}
fails_with() { # name, expected text, VAR=value...
  local name="$1" want="$2"; shift 2
  local rc=0; run "$@" || rc=$?
  { [ "$rc" -eq 1 ] && grep -q "$want" "$WORK/err"; } && s=0 || s=1
  check "$name" "$s"
}

rc=0; run || rc=$?
{ [ "$rc" -eq 0 ] && grep -q 'bundle ./dist/app-ABC123.js' "$WORK/out"; } && s=0 || s=1
check "healthy pages pass" "$s"

rc=0; run APP_BODY='<form id="login-form"></form><script src="/app/dist/app-ABC123.js"></script>' || rc=$?
check "a bundle named by absolute path passes" "$rc"

fails_with "root page down fails" "/ answered 502" ROOT_CODE=502
fails_with "app page down fails" "/app/ answered 503" APP_CODE=503
fails_with "app page without the sign-in form fails" "no sign-in form" APP_BODY='<script src="./dist/app-ABC123.js"></script>'
fails_with "app page without a bundle fails" "names no bundle" APP_BODY='<form id="login-form"></form>'
fails_with "bundle not found fails" "answered 404" BUNDLE_CODE=404
fails_with "index.html served in place of the bundle fails" "answered as text/html" BUNDLE_TYPE='text/html; charset=utf-8'
fails_with "empty bundle fails" "is empty" BUNDLE_SIZE=0
fails_with "no answer at all fails" "/ answered 000" DOWN_CALLS=99

rc=0; run DOWN_CALLS=1 || rc=$?
{ [ "$rc" -eq 0 ] && grep -q 'retry' "$WORK/out"; } && s=0 || s=1
check "one dropped request is retried and passes" "$s"

rc=0; run ROOT_CODE=502 PAGE_CHECK_ATTEMPTS=3 || rc=$?
{ [ "$rc" -eq 1 ] && [ "$(cat "$WORK/count")" -eq 3 ]; } && s=0 || s=1
check "attempts are bounded" "$s"

rc=0; sh "$SCRIPT" >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 2 ] && s=0 || s=1
check "wrong argument count is a usage error" "$s"

# Prints the line number of a step in a pipeline, or 0.
step_line() { grep -n "name: $2\$" "$REPO/.woodpecker/$1.yaml" | head -n 1 | cut -d: -f1 || true; }
in_order() { # pipeline, then step names that must appear in this order
  local f="$1" prev=0 line; shift
  for step in "$@"; do
    line="$(step_line "$f" "$step")"
    [ -n "$line" ] && [ "$line" -gt "$prev" ] || return 1
    prev="$line"
  done
}

# Wiring: staging's check fails the workflow production depends on, and production's
# check sits after the deploy and before the rollback that a failed step fires.
{ in_order release deploy-staging page-check \
  && grep -q 'check-pages.sh https://bugbarn.staging.wiebe.xyz' "$REPO/.woodpecker/release.yaml"; } && s=0 || s=1
check "release checks staging's pages after the deploy" "$s"
grep -q '^  - release$' "$REPO/.woodpecker/deploy-production.yaml" && s=0 || s=1
check "production still depends on release" "$s"
{ in_order deploy-production deploy page-check rollback-on-failure release-marker \
  && grep -q 'check-pages.sh https://bugbarn.wiebe.xyz' "$REPO/.woodpecker/deploy-production.yaml"; } && s=0 || s=1
check "production checks its pages between the deploy and the rollback" "$s"

# The check takes no credential and never asks for a setup page.
for pipe in release deploy-production; do
  n=$(awk '/name: page-check$/{f=1; next} f && /- name:/{f=0} f' "$REPO/.woodpecker/$pipe.yaml" | grep -c 'from_secret\|setup' || true)
  [ "$n" -eq 0 ] && s=0 || s=1
  check "$pipe page check uses no secret and no setup path" "$s"
done
n=$(grep -v '^#' "$SCRIPT" | grep -c -i 'setup\|/login\|-X \|--data\|Cookie\|Authorization' || true)
[ "$n" -eq 0 ] && s=0 || s=1
check "the script signs in nowhere, writes nothing and never asks for a setup page" "$s"

[ "$failures" -eq 0 ] || { echo "$failures check(s) failed"; exit 1; }
echo "all checks passed"
