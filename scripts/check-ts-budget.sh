#!/bin/sh
# TypeScript size/complexity soak for web/.
#
# web/.oxlintrc.json sets `complexity` 15, `max-lines` 500 and
# `max-lines-per-function` 80, all at 'warn'. The oxlint ratchet
# (web/scripts/oxlint-ratchet.mjs) leaves those three rules out of its own
# baseline and hands them to this script.
#
# Flipping the three rules to 'error' would break the build on 18 existing
# findings. Instead this counts them and ratchets the count against a committed
# baseline that may only fall. Enforce-at target is 0, at which point the
# rules move to 'error', BUDGET_RULES leaves the oxlint ratchet, and this
# script and its baseline go away.
#
# Runs in the web node leg rather than the quality leg because it needs
# web/node_modules, which only that leg installs.
set -eu

BASELINE_FILE="scripts/ts-budget-baseline.txt"
ENFORCE_AT=0
RULES="eslint(complexity) eslint(max-lines) eslint(max-lines-per-function)"
REPORT="$(pwd)/.tools/oxlint-budget.json"

test -d web/node_modules || {
	echo "FAIL: web/node_modules is missing — run pnpm install in web/ before this gate"
	exit 1
}
mkdir -p "$(dirname "$REPORT")"
rm -f "$REPORT"

# --budget exits non-zero only when oxlint itself did not run; the report is
# then absent or empty and count-findings.py fails on it.
(cd web && node scripts/oxlint-ratchet.mjs --budget >"$REPORT") || {
	echo "FAIL: oxlint did not run to completion"
	exit 1
}

# shellcheck disable=SC2086
count=$(python3 scripts/count-findings.py oxlint "$REPORT" $RULES)
echo "TypeScript budget findings (${RULES}): $count"
python3 - "$REPORT" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as fh:
    doc = json.load(fh)
for finding in doc:
    labels = finding.get("labels") or [{}]
    line = labels[0].get("span", {}).get("line", "?")
    print(f"  {finding['filename']}:{line} {finding['code']}: {finding['message']}")
PY
sh scripts/ratchet.sh "ts-budget" "$BASELINE_FILE" "$count" "$ENFORCE_AT"
