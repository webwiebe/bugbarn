#!/usr/bin/env python3
"""Count findings in a linter report, failing loudly when the report is absent.

A gate that counts lines of tool output treats "the tool did not run" as "the
tool found nothing". Every count in this repo goes through here instead: a
missing, truncated or malformed report is an error with a non-zero exit, never
a count of zero.

Usage:
  count-findings.py golangci <report.json> [linter]
  count-findings.py oxlint   <report.json> <rule> [rule ...]

oxlint mode reads the array `node web/scripts/oxlint-ratchet.mjs --budget`
prints: one object per finding, with the rule in `code`, e.g.
"eslint(complexity)".
"""

import json
import sys


def die(msg):
    print(f"ERROR: {msg}", file=sys.stderr)
    raise SystemExit(1)


def load(path):
    try:
        with open(path, encoding="utf-8") as fh:
            return json.load(fh)
    except FileNotFoundError:
        die(f"no report at {path} — the linter did not run")
    except OSError as exc:
        die(f"cannot read report {path}: {exc}")
    except ValueError as exc:
        die(f"report {path} is not valid JSON ({exc}) — the linter did not finish")


def count_golangci(argv):
    if not argv:
        die("golangci mode needs a report path")
    doc = load(argv[0])
    if not isinstance(doc, dict) or "Issues" not in doc:
        die(f"report {argv[0]} has no Issues key — not a golangci-lint report")
    issues = doc["Issues"] or []
    if len(argv) > 1:
        issues = [i for i in issues if i.get("FromLinter") == argv[1]]
    return len(issues)


def count_oxlint(argv):
    if len(argv) < 2:
        die("oxlint mode needs a report path and at least one rule code")
    doc = load(argv[0])
    if not isinstance(doc, list):
        die(f"report {argv[0]} is not an oxlint findings array")
    rules = set(argv[1:])
    return sum(1 for finding in doc if isinstance(finding, dict) and finding.get("code") in rules)


def main():
    if len(sys.argv) < 2:
        die("usage: count-findings.py <golangci|oxlint> <report.json> [...]")
    mode, argv = sys.argv[1], sys.argv[2:]
    if mode == "golangci":
        print(count_golangci(argv))
    elif mode == "oxlint":
        print(count_oxlint(argv))
    else:
        die(f"unknown mode '{mode}'")


if __name__ == "__main__":
    main()
