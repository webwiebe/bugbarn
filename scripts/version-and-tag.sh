#!/usr/bin/env bash
# version-and-tag.sh: pick the release version for HEAD, tag it, push the tag to
# Gitea and to the GitHub mirror, and write .relver for the later build steps.
#
# A rerun is safe. When a v* tag already points at HEAD, the script reuses it:
# it cuts no new tag and only pushes the tag to GitHub if GitHub lacks it. Only
# a HEAD without a tag gets the next patch version. Pipeline 853 (2026-11-05)
# pushed v0.236.327 to Gitea, then GitHub rejected the mirror push; a restart of
# the old script would have tagged the same commit again as v0.236.328.
#
# Environment:
#   GITHUB_REMOTE  where the mirror tag goes; defaults to the GitHub repo URL
#                  with GITHUB_TOKEN. Tests point it at a local bare repo.
#   ORIGIN_REMOTE  the Gitea remote, default "origin". Its tags must already be
#                  fetched; the pipeline fetches them before running this.
set -euo pipefail

ORIGIN_REMOTE="${ORIGIN_REMOTE:-origin}"
GITHUB_REMOTE="${GITHUB_REMOTE:-https://x-access-token:${GITHUB_TOKEN:?GITHUB_TOKEN or GITHUB_REMOTE must be set}@github.com/webwiebe/bugbarn.git}"

EXISTING=$(git tag --points-at HEAD --list 'v*.*.*' --sort=-v:refname | head -n 1)
if [ -n "$EXISTING" ]; then
  NEW="$EXISTING"
  echo "HEAD already carries $NEW; reusing it"
else
  LATEST=$(git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)
  VER=${LATEST#v}
  MAJOR=$(echo "$VER" | cut -d. -f1); MINOR=$(echo "$VER" | cut -d. -f2); PATCH=$(echo "$VER" | cut -d. -f3)
  PATCH=$((PATCH + 1))
  NEW="v$MAJOR.$MINOR.$PATCH"
  while git rev-parse -q --verify "refs/tags/$NEW" >/dev/null; do
    PATCH=$((PATCH + 1)); NEW="v$MAJOR.$MINOR.$PATCH"
  done
  echo "Next version: $NEW"
fi

VER=${NEW#v}
printf 'VERSION=%s\nVERSION_NUM=%s\n' "$NEW" "$VER" > .relver
cat .relver

if [ -z "$EXISTING" ]; then
  # Lightweight tag (NOT -a): an annotated tag makes Woodpecker report the
  # tag-object sha as CI_COMMIT_SHA, which then mismatches the commit-sha
  # the images are built under and breaks release.yaml's retag.
  git tag "$NEW"
  # The Gitea push fires release.yaml (staging), then deploy-production.yaml.
  git push "$ORIGIN_REMOTE" "$NEW"
fi

# The GitHub mirror holds the tag for the GitHub Release. Gitea's push mirror
# (sync on commit) also pushes every new tag to GitHub, so the tag can land
# there between the check and the push below, and the push is then rejected
# (pipeline 875, v0.236.333). A tag on GitHub that points at HEAD counts as
# pushed. A tag that points at another commit fails the step.
WANT=$(git rev-parse "HEAD^{commit}")
github_has_tag() {
  local got
  got=$(git ls-remote --tags "$GITHUB_REMOTE" "refs/tags/$NEW" "refs/tags/$NEW^{}" \
    | awk '{sha=$1} $2 ~ /\^\{\}$/ {peeled=$1} END {print (peeled != "" ? peeled : sha)}')
  [ -n "$got" ] || return 1
  if [ "$got" != "$WANT" ]; then
    echo "GitHub has $NEW at $got, but HEAD is $WANT" >&2
    exit 1
  fi
  echo "GitHub already has $NEW"
}
if ! github_has_tag; then
  if ! git push "$GITHUB_REMOTE" "$NEW"; then
    github_has_tag || { echo "pushing $NEW to GitHub failed" >&2; exit 1; }
  fi
fi
