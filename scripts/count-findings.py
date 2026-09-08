#!/usr/bin/env python3
"""Count findings in a linter report, failing loudly when the report is absent.

A gate that counts lines of tool output treats "the tool did not run" as "the
tool found nothing". Every count in this repo goes through here instead: a
missing, truncated or malformed report is an error with a non-zero exit, never
a count of zero.

Usage:
  count-findings.py golangci <report.json> [linter]
  count-findings.py eslint   <report.json> <rule> [rule ...]
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


def count_eslint(argv):
    if len(argv) < 2:
        die("eslint mode needs a report path and at least one rule id")
    doc = load(argv[0])
    if not isinstance(doc, list):
        die(f"report {argv[0]} is not an eslint JSON report")
    rules = set(argv[1:])
    total = 0
    for result in doc:
        for message in result.get("messages", []):
            if message.get("ruleId") in rules:
                total += 1
    return total


def main():
    if len(sys.argv) < 2:
        die("usage: count-findings.py <golangci|eslint> <report.json> [...]")
    mode, argv = sys.argv[1], sys.argv[2:]
    if mode == "golangci":
        print(count_golangci(argv))
    elif mode == "eslint":
        print(count_eslint(argv))
    else:
        die(f"unknown mode '{mode}'")


if __name__ == "__main__":
    main()
