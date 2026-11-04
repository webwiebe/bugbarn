#!/usr/bin/env bash
# Delete old bugbarn container versions from GHCR.
#
# A version is kept when any of these hold:
#   - it is younger than KEEP_DAYS
#   - it is one of the KEEP_RELEASES most recently pushed vX.Y.Z versions
#   - it carries a named tag (anything that is not vX.Y.Z or a 40-hex commit
#     sha, e.g. the buildcache's `latest`)
#   - its digest or tag appears in PROTECT_FILE (images running in a cluster)
#   - it is a child manifest of a kept version (a semver tag is an index whose
#     amd64 child is the commit-sha version; deleting the child breaks the tag)
# Everything else is deleted, oldest first, at most MAX_DELETE per run so one
# run never eats a large share of the account's shared API budget.
#
# GitHub answers a delete with a 5xx now and then. A delete is tried up to three
# times; a version that still fails is reported, the run goes on with the rest,
# and the script exits 1 at the end. The next run picks the version up again.
#
# Env:
#   GHCR_TOKEN     PAT with read:packages + delete:packages (required)
#   GHCR_OWNER     package owner (default webwiebe)
#   PACKAGES       space-separated package names (default the three bugbarn ones)
#   KEEP_DAYS      default 7
#   KEEP_RELEASES  default 3
#   MAX_DELETE     default 300 (across all packages)
#   PROTECT_FILE   file with image refs in use, one per line (required, non-empty)
#   DRY_RUN        1 = only print what would be deleted
set -euo pipefail

: "${GHCR_TOKEN:?GHCR_TOKEN is required}"
: "${PROTECT_FILE:?PROTECT_FILE is required}"
GHCR_OWNER="${GHCR_OWNER:-webwiebe}"
PACKAGES="${PACKAGES:-bugbarn/service bugbarn/web bugbarn/buildcache}"
KEEP_DAYS="${KEEP_DAYS:-7}"
KEEP_RELEASES="${KEEP_RELEASES:-3}"
MAX_DELETE="${MAX_DELETE:-300}"
DRY_RUN="${DRY_RUN:-0}"

# Fail closed: without the list of running images we cannot tell what is safe.
grep -q 'ghcr.io/' "$PROTECT_FILE" || { echo "ERROR: $PROTECT_FILE lists no running images; refusing to prune"; exit 1; }

API=https://api.github.com
CUTOFF="$(date -u -v-"$KEEP_DAYS"d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "$KEEP_DAYS days ago" +%Y-%m-%dT%H:%M:%SZ)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
deleted=0
failed=0
RETRY_WAIT="${RETRY_WAIT:-5}"

api() { curl -fsS -H "Authorization: Bearer $GHCR_TOKEN" -H "Accept: application/vnd.github+json" "$@"; }

# Deletes one package version. Sets $status to the last HTTP status, 000 when no
# answer came. A 404 after a retry counts as done: an earlier attempt that
# looked failed went through.
delete_version() {
  local attempt
  for attempt in 1 2 3; do
    status="$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
      -H "Authorization: Bearer $GHCR_TOKEN" -H "Accept: application/vnd.github+json" "$1")" || status=000
    case "$status" in
      2??) return 0 ;;
      404) [ "$attempt" -gt 1 ] && return 0; return 1 ;;
      5??|429|000) [ "$attempt" -lt 3 ] && sleep $((attempt * RETRY_WAIT)) ;;
      *) return 1 ;;
    esac
  done
  return 1
}

for pkg in $PACKAGES; do
  enc="${pkg//\//%2F}"
  image="ghcr.io/$GHCR_OWNER/$pkg"

  # All versions as TSV: id, digest, created_at, comma-joined tags.
  : > "$WORK/versions.tsv"
  page=1
  while :; do
    api "$API/users/$GHCR_OWNER/packages/container/$enc/versions?per_page=100&page=$page" > "$WORK/page.json"
    [ "$(jq length "$WORK/page.json")" -gt 0 ] || break
    jq -r '.[] | [.id, .name, .created_at, (.metadata.container.tags | join(","))] | @tsv' "$WORK/page.json" >> "$WORK/versions.tsv"
    page=$((page + 1))
  done
  total="$(wc -l < "$WORK/versions.tsv" | tr -d ' ')"

  # Refs of this image that are running somewhere: digests and tags.
  grep -o "${image}[@:][^[:space:]]*" "$PROTECT_FILE" | sed "s|^${image}[@:]||" | sort -u > "$WORK/protected.txt" || true

  # The newest releases by push time. Sorting the tags by version would let a
  # stray tag such as v0.237.0 (ahead of the real v0.236.x line) crowd out the
  # releases that are actually deployed.
  newest="$(awk -F'\t' '$4 ~ /(^|,)v[0-9]+\.[0-9]+\.[0-9]+(,|$)/' "$WORK/versions.tsv" | sort -t$'\t' -k3 -r | head -n "$KEEP_RELEASES" | cut -f2)" || true

  # First pass: the versions kept by their own merits.
  : > "$WORK/keep.txt"
  while IFS=$'\t' read -r id digest created tags; do
    keep=0
    [[ "$created" > "$CUTOFF" ]] && keep=1
    grep -qxF "$digest" "$WORK/protected.txt" && keep=1
    grep -qxF "$digest" <<< "$newest" && keep=1
    for t in ${tags//,/ }; do
      grep -qxF "$t" "$WORK/protected.txt" && keep=1
      [[ "$t" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ || "$t" =~ ^[0-9a-f]{40}$ ]] || keep=1
    done
    [ "$keep" = 1 ] && echo "$digest" >> "$WORK/keep.txt"
  done < "$WORK/versions.tsv"

  # Second pass: keep the child manifests of every kept index.
  token="$(curl -fsS -u "$GHCR_OWNER:$GHCR_TOKEN" "https://ghcr.io/token?service=ghcr.io&scope=repository:$GHCR_OWNER/$pkg:pull" | jq -r .token)"
  sort -u "$WORK/keep.txt" | while read -r digest; do
    curl -fsS -H "Authorization: Bearer $token" \
      -H 'Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json' \
      "https://ghcr.io/v2/$GHCR_OWNER/$pkg/manifests/$digest" | jq -r '.manifests[]?.digest'
  done > "$WORK/children.txt"
  sort -u -o "$WORK/keep.txt" "$WORK/keep.txt" "$WORK/children.txt"

  # Delete the rest, oldest first.
  sort -t$'\t' -k3 "$WORK/versions.tsv" | while IFS=$'\t' read -r id digest created tags; do
    grep -qxF "$digest" "$WORK/keep.txt" && continue
    echo "$id $digest $created ${tags:-<untagged>}"
  done > "$WORK/delete.txt"
  candidates="$(wc -l < "$WORK/delete.txt" | tr -d ' ')"
  echo "== $pkg: $total versions, $((total - candidates)) kept, $candidates to delete"

  while read -r id digest created tags; do
    if [ "$deleted" -ge "$MAX_DELETE" ]; then
      echo "   reached MAX_DELETE=$MAX_DELETE; the rest waits for the next run"
      break
    fi
    if [ "$DRY_RUN" = 1 ]; then
      echo "   would delete $created $tags $digest"
    else
      if delete_version "$API/users/$GHCR_OWNER/packages/container/$enc/versions/$id"; then
        echo "   deleted $created $tags $digest"
      else
        echo "   FAILED (HTTP $status) $created $tags $digest"
        failed=$((failed + 1))
      fi
      # GitHub's secondary rate limit caps content-changing requests per minute.
      sleep 1
    fi
    deleted=$((deleted + 1))
  done < "$WORK/delete.txt"
done

echo "done: $((deleted - failed)) versions $([ "$DRY_RUN" = 1 ] && echo 'would be ' || true)deleted, $failed failed"
[ "$failed" = 0 ]
