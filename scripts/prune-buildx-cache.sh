#!/bin/sh
# Cap a persistent buildx builder's local BuildKit cache.
#
# Usage: scripts/prune-buildx-cache.sh <builder> [limit]   (limit default 3gb)
#
# The bugbarn-ci docker-container builder keeps its cache in a volume inside the
# Docker Desktop VM on the build host, which every runner and repo on that host
# shares. Unpruned it reached 15 GB and filled the VM (issue #195). The registry
# cache on GHCR is the cache CI relies on across machines, so trimming the local
# copy only costs a re-pull of layers.
#
# buildx 0.33 replaced --keep-storage with --max-used-space; older runners only
# know --keep-storage, so try the new flag first. A failed prune prints a
# warning and exits 0: it must never turn a green build red.
set -u
builder="${1:?usage: prune-buildx-cache.sh <builder> [limit]}"
limit="${2:-3gb}"

if docker buildx prune --help 2>/dev/null | grep -q -- '--max-used-space'; then
	flag=--max-used-space
else
	flag=--keep-storage
fi

if ! docker buildx prune --builder "$builder" --force "$flag" "$limit"; then
	echo "warning: could not prune buildx builder $builder" >&2
fi
docker buildx du --builder "$builder" 2>/dev/null | tail -n 1 || true
exit 0
