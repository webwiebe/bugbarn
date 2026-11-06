#!/bin/sh
# Cap a Go build cache by size.
#
# Usage: scripts/trim-go-cache.sh <dir> <max MiB>
#
# The quality step keeps GOCACHE on the build host's /tmp (see ci.yaml). Go only
# trims entries that have gone unused for five days, and that cache grew to 12G
# in two days on a host that runs every barn repo's CI. When the cache is over
# the cap, this deletes its least recently used files until it is at 80% of the
# cap. Go refreshes an entry's mtime when it uses one (at most once an hour), so
# mtime order is use order. A deleted entry is a cache miss: a build that is
# running at the same time recompiles that package and goes on.
#
# A failed trim prints a warning and exits 0: it must never turn a green build red.
set -u
dir="${1:?usage: trim-go-cache.sh <dir> <max MiB>}"
max_mib="${2:?usage: trim-go-cache.sh <dir> <max MiB>}"

[ -d "$dir" ] || exit 0
list="$(mktemp)" || { echo "warning: trim-go-cache: no temp file" >&2; exit 0; }
trap 'rm -f "$list"' EXIT

# One line per file: mtime, size in bytes, path. BSD stat on the macOS agents,
# GNU find elsewhere.
if stat -f '%m' / >/dev/null 2>&1; then
	find "$dir" -type f ! -name README ! -name trim.txt -exec stat -f '%m %z %N' {} + >"$list" 2>/dev/null
else
	find "$dir" -type f ! -name README ! -name trim.txt -printf '%T@ %s %p\n' >"$list" 2>/dev/null
fi

max=$((max_mib * 1024 * 1024))
target=$((max / 10 * 8))
total="$(awk '{s += $2} END {printf "%.0f", s}' "$list")"
if [ "$total" -le "$max" ]; then
	echo "go cache $dir: $((total / 1048576)) MiB, cap $max_mib MiB"
	exit 0
fi

# Oldest first, until what is left fits the target. awk writes the size that
# is left to $list.left for the summary line.
sort -n "$list" | awk -v total="$total" -v target="$target" -v left="$list.left" '
	total > target { total -= $2; sub(/^[^ ]+ [^ ]+ /, ""); print; next }
	END { printf "%.0f\n", total > left }
' | tr '\n' '\0' | xargs -0 rm -f || echo "warning: trim-go-cache: some files were not removed" >&2

left="$(cat "$list.left" 2>/dev/null || echo "$total")"
rm -f "$list.left"
echo "go cache $dir: trimmed from $((total / 1048576)) MiB to $((left / 1048576)) MiB, cap $max_mib MiB"
exit 0
