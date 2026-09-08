#!/bin/sh
# TypeScript size/complexity soak for web/.
#
# web/eslint.config.js sets `complexity` 15, `max-lines` 500 and
# `max-lines-per-function` 80, all at 'warn', and no job passes
# --max-warnings=0. So the budget was advisory: a run emitting a function at
# complexity 38 and one at 129 lines went green.
#
# Flipping the three rules to 'error' would break the build on 18 existing
# findings. Instead this counts them and ratchets the count against a committed
# baseline that may only fall. Enforce-at target is 0 — at which point the
# rules move to 'error', web's lint step gets --max-warnings=0, and this script
# and its baseline go away.
#
# Runs in the web node leg rather than the quality leg because it needs
# web/node_modules, which only that leg installs.
set -eu

BASELINE_FILE="scripts/ts-budget-baseline.txt"
ENFORCE_AT=0
RULES="complexity max-lines max-lines-per-function"
REPORT="$(pwd)/.tools/eslint-web.json"

test -d web/node_modules || {
	echo "FAIL: web/node_modules is missing — run npm ci in web/ before this gate"
	exit 1
}
mkdir -p "$(dirname "$REPORT")"
rm -f "$REPORT"

set +e
(cd web && npx --no-install eslint src/ --format json --output-file "$REPORT")
rc=$?
set -e
# 0 = no errors, 1 = errors present (the budget rules are warnings, so this
# gate does not care which). Anything else means eslint did not run.
if [ "$rc" -ne 0 ] && [ "$rc" -ne 1 ]; then
	echo "FAIL: eslint exited $rc — it did not run to completion"
	exit 1
fi

# shellcheck disable=SC2086
count=$(python3 scripts/count-findings.py eslint "$REPORT" $RULES)
echo "TypeScript budget findings (${RULES}): $count"
python3 - "$REPORT" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as fh:
    doc = json.load(fh)
rules = {"complexity", "max-lines", "max-lines-per-function"}
for result in doc:
    path = result["filePath"].split("/web/")[-1]
    for message in result.get("messages", []):
        if message.get("ruleId") in rules:
            print(f"  {path}:{message['line']} {message['ruleId']}: {message['message']}")
PY
sh scripts/ratchet.sh "ts-budget" "$BASELINE_FILE" "$count" "$ENFORCE_AT"
