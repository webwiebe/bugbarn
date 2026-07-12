#!/bin/sh
# Evicts corrupted extracted modules from the Go module cache.
#
# The self-hosted runners share a persistent GOMODCACHE in $HOME. A disk-full
# incident (or killed extraction) can leave truncated files inside an
# extracted module dir, after which every host-side typecheck fails with
# "expected 'package', found 'EOF'" until the dir is removed. The verified
# zip download cache is untouched, so eviction is cheap: the next build
# re-extracts the module from the zip and re-verifies it against go.sum.
#
# Detection: a valid .go file can never be empty (it needs a package clause),
# so any zero-byte .go file outside testdata marks its module as corrupt.
set -eu

modcache="$(go env GOMODCACHE)"
if [ ! -d "$modcache" ]; then
	echo "heal-go-modcache: no module cache at $modcache"
	exit 0
fi

find "$modcache" -name '*.go' -size 0 -not -path '*/testdata/*' 2>/dev/null |
	sed -E 's|(@[^/]*)/.*|\1|' | sort -u | while read -r dir; do
	case "$dir" in
	"$modcache"/*@*) ;;
	*) continue ;;
	esac
	echo "heal-go-modcache: evicting corrupted module $dir"
	chmod -R u+w "$dir" 2>/dev/null || true
	rm -rf "$dir"
done
echo "heal-go-modcache: OK"
