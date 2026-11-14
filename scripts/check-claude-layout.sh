#!/bin/sh
# Fails when a package directory under internal/ has no row in the Project
# Layout block of the root CLAUDE.md.
#
# The layout block is the map every reader starts from. It drifted twice: #184
# found 13 of 34 packages listed, and a later structure review found silence,
# monitorwatch, keytouch and cronexpr missing. A row is a line in the block
# that starts with four spaces, then the directory name and a slash, for
# example "    spool/". Subpackages (internal/service/issues) are covered by
# their parent's row.
#
# Usage: sh scripts/check-claude-layout.sh [repo-root]
set -eu

ROOT="${1:-.}"
cd "$ROOT"

layout=$(awk '
	/^## Project Layout/ { in_section = 1; next }
	in_section && /^## / { exit }
	in_section { print }
' CLAUDE.md)

if [ -z "$layout" ]; then
	echo "FAIL CLAUDE.md has no '## Project Layout' section"
	exit 1
fi

fail=0
for dir in $(git ls-files 'internal/*.go' | awk -F/ 'NF >= 3 { print $2 }' | sort -u); do
	if ! printf '%s\n' "$layout" | grep -q "^    $dir/"; then
		echo "FAIL internal/$dir has no row in the CLAUDE.md Project Layout block"
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	echo "Add a row '    <dir>/  <one-sentence intent>' under the right group in CLAUDE.md."
	exit 1
fi
echo "claude-layout: every internal/ package has a layout row"
