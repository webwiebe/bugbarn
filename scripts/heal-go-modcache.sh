#!/bin/sh
# Evicts corrupted modules from the Go module cache.
#
# The self-hosted runners share a persistent GOMODCACHE in $HOME. A disk-full
# incident can truncate files inside an extracted module dir, after which
# every host-side typecheck fails with "expected 'package', found 'EOF'".
# Evicting only the extracted dir is NOT enough: re-extraction trusts the
# locally cached zip + ziphash pair without re-checking go.sum, so a
# truncated download reproduces the same corruption. Evict the download
# cache entry too — the next build re-downloads the module and verifies it
# against go.sum.
#
# Detection: a valid .go file can never be empty (it needs a package clause),
# so any zero-byte .go file outside testdata marks its module as corrupt.
set -eu

modcache="$(go env GOMODCACHE)"
if [ ! -d "$modcache" ]; then
	echo "heal-go-modcache: no module cache at $modcache"
	exit 0
fi

find "$modcache" -path "$modcache/cache" -prune -o \
	-name '*.go' -size 0 -not -path '*/testdata/*' -print 2>/dev/null |
	sed -E 's|(@[^/]*)/.*|\1|' | sort -u | while read -r dir; do
	case "$dir" in
	"$modcache"/*@*) ;;
	*) continue ;;
	esac
	echo "heal-go-modcache: evicting corrupted module $dir"
	chmod -R u+w "$dir" 2>/dev/null || true
	rm -rf "$dir"
	# Matching download-cache entry (module path is already escaped in the
	# extracted dir name): drop the zip/ziphash/info/mod so the module is
	# re-fetched and re-verified against go.sum instead of re-extracted
	# from a possibly-truncated local zip.
	rel="${dir#"$modcache"/}"
	mod="${rel%@*}"
	ver="${rel##*@}"
	dl="$modcache/cache/download/$mod/@v"
	if [ -d "$dl" ]; then
		echo "heal-go-modcache: evicting download cache $dl/$ver.*"
		chmod -R u+w "$dl" 2>/dev/null || true
		for f in "$dl/$ver".zip "$dl/$ver".ziphash "$dl/$ver".info "$dl/$ver".mod; do
			rm -f "$f"
		done
	fi
done
echo "heal-go-modcache: OK"
