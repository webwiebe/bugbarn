#!/usr/bin/env bash
# The nightly browser run stays on the cron, on the build agents, against testing:
# never on a pull request or a push, never against production.
#
# Run it with: bash scripts/e2e-nightly_test.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
f="$REPO/.woodpecker/e2e-nightly.yaml"

failures=0
check() { # name, exit status of the assertion just made
  if [ "$2" -eq 0 ]; then echo "ok   $1"; else echo "FAIL $1"; failures=$((failures + 1)); fi
}

{ grep -q 'event: cron' "$f" && grep -q 'cron: e2e-nightly' "$f" && grep -q 'type: build' "$f" \
  && ! grep -q 'event: \(pull_request\|push\|tag\|manual\)' "$f"; } && s=0 || s=1
check "runs on the cron alone, on the build agents" "$s"

n=$(grep 'BUGBARN_[A-Z]*_URL:' "$f" | grep -vc 'https://bugbarn.test.wiebe.xyz/app/' || true)
[ "$n" -eq 0 ] && s=0 || s=1
check "points at testing and nothing else" "$s"

names="$(sed -n 's/.*from_secret: //p' "$f" | sort -u | tr '\n' ' ')"
[ "$names" = "e2e_testing_password gitea_read_token " ] && s=0 || s=1
check "holds only the login password and the read token" "$s"

{ grep -q 'grep -v -e screenshots -e deploy-smoke' "$f" && grep -q -- '--project=desktop --project=tablet --workers=1' "$f"; } && s=0 || s=1
check "leaves out the specs that need more, and runs one worker on Chromium" "$s"

[ "$failures" -eq 0 ] || { echo "$failures check(s) failed"; exit 1; }
echo "all checks passed"
