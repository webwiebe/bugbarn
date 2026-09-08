#!/bin/sh
# Generic count ratchet, shared by every count-based gate in this repo.
#
# Usage: ratchet.sh <label> <baseline-file> <count> <enforce-at>
#
# Implements all four properties of the ratchet contract:
#   1. count > baseline  -> FAIL (new debt)
#   2. a worsened entry is a higher count, so it is property 1
#   3. count < baseline  -> FAIL as STALE, naming the refresh command. Without
#      this the baseline drifts upward of reality and silently re-opens room
#      for a regression. This is the property that makes it a ratchet.
#   4. The baseline file is the only escape hatch. There is no env override and
#      no skip flag: lowering or raising the number leaves a diff.
#
# <enforce-at> is the written exit criterion for a soak: the count this gate is
# being driven towards. It is printed, never enforced here — a soak stops being
# a soak when <enforce-at> is reached and the baseline equals it.
set -eu

if [ "$#" -ne 4 ]; then
	echo "usage: ratchet.sh <label> <baseline-file> <count> <enforce-at>" >&2
	exit 2
fi

label="$1"
file="$2"
count="$3"
target="$4"

case "$count" in
'' | *[!0-9]*)
	echo "FAIL ($label): measured count '$count' is not a number — the collector did not run"
	exit 1
	;;
esac

if [ ! -f "$file" ]; then
	echo "FAIL ($label): no baseline at $file"
	echo "  create it with: echo $count > $file"
	exit 1
fi

baseline=$(tr -d ' \t\n' <"$file")
case "$baseline" in
'' | *[!0-9]*)
	echo "FAIL ($label): baseline in $file is '$baseline', which is not a number"
	exit 1
	;;
esac

if [ "$count" -gt "$baseline" ]; then
	echo "FAIL ($label): $count findings against a baseline of $baseline."
	echo "  This commit adds $((count - baseline)). Fix them; the baseline may only fall."
	exit 1
fi

if [ "$count" -lt "$baseline" ]; then
	echo "FAIL ($label): $count findings against a stale baseline of $baseline."
	echo "  The baseline is beatable and must be lowered in this same commit:"
	echo "    echo $count > $file"
	exit 1
fi

if [ "$count" -eq "$target" ]; then
	echo "$label: OK ($count findings, at the enforce-at target of $target)"
else
	echo "$label: OK (soak at $count findings; enforce-at target is $target)"
fi
