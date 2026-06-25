#!/bin/sh
# Fails if any tracked source file exceeds MAX_LINES, unless it is allowlisted.
#
# The allowlist is the current debt registry. Each entry pins a file to its
# recorded line count, so allowlisted files may only ever SHRINK (a real
# ratchet) — and every other source file is capped at MAX_LINES. When a file
# drops under MAX_LINES, delete its allowlist line and the global cap takes over.
#
# Covers every source language uniformly (Go, TS/TSX, Astro, Python, PHP) so the
# two giant web/*.ts files are gated the same way as Go — something golangci-lint
# alone cannot do.
set -eu

MAX_LINES=500

# Baseline offenders (lines as of 2026-06-25). Shrink-only; refactor & remove.
# Format: <path> <recorded_lines>
ALLOWLIST="
web/src/app.ts 2741
web/src/components.ts 1913
internal/api/server.go 577
"

fail=0
for f in $(git ls-files '*.go' '*.ts' '*.tsx' '*.astro' '*.py' '*.php'); do
	[ -f "$f" ] || continue
	lines=$(wc -l < "$f" | tr -d ' ')
	allowed_for=$(printf '%s\n' "$ALLOWLIST" | awk -v p="$f" '$1 == p { print $2 }')
	if [ -n "$allowed_for" ]; then
		# Allowlisted: must not exceed its recorded size (shrink-only).
		if [ "$lines" -gt "$allowed_for" ]; then
			echo "FAIL $f: $lines lines > baseline $allowed_for (allowlisted file grew; shrink it)"
			fail=1
		fi
	elif [ "$lines" -gt "$MAX_LINES" ]; then
		echo "FAIL $f: $lines lines > $MAX_LINES limit"
		fail=1
	fi
done

if [ "$fail" -eq 0 ]; then
	echo "file-length: OK (limit $MAX_LINES)"
fi
exit $fail
