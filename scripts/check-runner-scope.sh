#!/bin/sh
# Fails when a workflow step reaches outside its own runner's workspace.
#
# The defect this locks down: every self-hosted job began with a "Reclaim
# workspace" step that looped over $HOME/github-runners/*/_work/... and deleted
# the .cache of every runner on the host. A job starting on one runner wiped the
# Go module cache of a job still building on another, and the host runs CI for
# the whole estate.
#
# The check reads the workflow text and never runs a step: running the old step
# would delete the caches of every runner on the machine running the check.
#
# Usage: sh scripts/check-runner-scope.sh [workflows-dir]   (default .github/workflows)
set -eu

DIR="${1:-.github/workflows}"
bad=0

# 1. No glob over the runners root: github-runners/*, github-runners/runner-*, ...
if grep -nE 'github-runners/[^[:space:]"'"'"']*[*?[]' "$DIR"/*.yml "$DIR"/*.yaml 2>/dev/null; then
	echo "check-runner-scope: a step globs over other runners' directories (above)" >&2
	bad=1
fi

# 2. Every "Reclaim workspace" step clears only its own checkout's .cache.
steps=$(grep -hc 'name: Reclaim workspace' "$DIR"/*.yml "$DIR"/*.yaml 2>/dev/null | awk '{n += $1} END {print n + 0}')
own=$(grep -h 'c="${GITHUB_WORKSPACE:?}/.cache"' "$DIR"/*.yml "$DIR"/*.yaml 2>/dev/null | wc -l | tr -d ' ')
if [ "$steps" -eq 0 ]; then
	echo "check-runner-scope: no reclaim steps found; update this check if they were removed on purpose" >&2
	bad=1
elif [ "$steps" -ne "$own" ]; then
	echo "check-runner-scope: $steps reclaim steps but $own target \${GITHUB_WORKSPACE:?}/.cache" >&2
	bad=1
fi

[ "$bad" -eq 0 ] && echo "check-runner-scope: $steps reclaim steps, all scoped to their own workspace"
exit "$bad"
