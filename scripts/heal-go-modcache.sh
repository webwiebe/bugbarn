#!/bin/sh
# Heals a corrupted Go module cache on the shared self-hosted runners.
#
# The runners keep a persistent GOMODCACHE in $HOME. A disk-full incident can
# truncate files in there — both extracted module dirs AND the downloaded
# zips. Go re-extracts from the local zip without re-checking go.sum (it only
# trusts the sibling ziphash written at download time), so a truncated zip
# keeps reproducing "expected 'package', found 'EOF'" typecheck failures on
# every host-side build until the cache entry is destroyed.
#
# `go mod verify` recomputes the hashes of every dependency's cached zip and
# extracted dir, so it detects exactly this class of damage. When it fails,
# nuking the module cache is the only reliable recovery: the next download
# re-fetches everything and verifies it against go.sum. That costs one cold
# `go mod download` (~a minute on these runners) and only happens after an
# actual corruption event.
set -eu

# Fill any gaps first so verify sees a complete cache; fresh downloads are
# verified against go.sum on the way in.
go mod download

if go mod verify; then
	echo "heal-go-modcache: module cache OK"
	exit 0
fi

echo "heal-go-modcache: corruption detected — rebuilding the module cache"
go clean -modcache
go mod download
go mod verify
echo "heal-go-modcache: module cache rebuilt"
