#!/bin/sh
# Merge the bananasketch per-environment projects into bananasketch.
# Re-runnable: a source that already is an alias of the target reports
# already_merged. Run with DRY_RUN=1 first (the default) to see the counts;
# DRY_RUN=0 does the merge. Needs a bb with --dry-run (PR 323) pointed at the
# environment to change.
set -eu
DRY_RUN="${DRY_RUN:-1}"
flag=""
[ "$DRY_RUN" = "1" ] && flag="--dry-run"

for pair in "bananasketch-dev:dev" "bananasketch-test:test" "bananasketch-staging:staging"; do
  src="${pair%%:*}"
  env="${pair##*:}"
  echo "== $src into bananasketch as $env (DRY_RUN=$DRY_RUN)"
  # shellcheck disable=SC2086
  bb projects --merge "$src" --into bananasketch --env "$env" $flag
done
