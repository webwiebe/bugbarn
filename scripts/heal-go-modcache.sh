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

# A near-full runner disk is the root cause of this corruption class: module
# extraction gets truncated mid-write and the damage persists. Reclaim the
# regenerable Go build cache before touching modules, and fail loudly rather
# than let the lint/test steps fail cryptically on truncated sources.
free_kb=$(df -k "$HOME" | awk 'NR==2 {print $4}')
echo "heal-go-modcache: free space on \$HOME: $((free_kb / 1024)) MiB"
if [ "$free_kb" -lt $((5 * 1024 * 1024)) ]; then
	echo "heal-go-modcache: low disk — dropping the Go build cache"
	go clean -cache || true
	df -k "$HOME" | awk 'NR==2 {print "heal-go-modcache: free space now: " int($4/1024) " MiB"}'
fi

# Fill any gaps (fresh downloads are verified against go.sum on the way in)
# and force extraction of every dependency so `go mod verify` gets to check
# the extracted dirs too, not just the zips.
go mod download
go list -e -deps ./... >/dev/null 2>&1 || true

if go mod verify; then
	echo "heal-go-modcache: module cache OK"
	exit 0
fi

echo "heal-go-modcache: corruption detected — rebuilding the module cache"
go clean -modcache
# The build cache is downstream of the damage and must go with it. GOCACHE
# holds compiled export data produced FROM the truncated sources, and it
# outlives `go clean -modcache`. golangci-lint's typechecker loads that export
# data directly, so a healed module cache plus a stale build cache still fails
# — and fails in a way that looks nothing like the real cause:
#
#   internal/sourcemap/symbolicate.go:6:14: could not import
#   github.com/go-sourcemap/sourcemap (no required module provides package)
#
# while plain `go build ./...` of the very same package succeeds, because the
# toolchain recompiles instead of trusting the poisoned entry. That mismatch
# cost a full CI debug cycle; drop both caches together.
go clean -cache
go mod download
go list -e -deps ./... >/dev/null 2>&1 || true
go mod verify
echo "heal-go-modcache: module and build caches rebuilt"
