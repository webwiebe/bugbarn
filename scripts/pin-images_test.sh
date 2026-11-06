#!/usr/bin/env bash
# Tests for deploy/scripts/pin-images.sh against a copy of the production
# kustomization.
#
# Run it with: bash scripts/pin-images_test.sh   (or `make test-gates`)
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$REPO/deploy/scripts/pin-images.sh"
SRC="$REPO/deploy/k8s/production/kustomization.yaml"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

SVC="sha256:$(printf 'a%.0s' $(seq 64))"
WEB="sha256:$(printf 'b%.0s' $(seq 64))"

fail=0
check() { # name, want, got
  if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: want [$2], got [$3]"; fail=1; fi
}
# block <file> <image>: the images entry for one image, as one line.
block() { awk -v n="  - name: $2" '$0 == n {on=1; print; next} on && /^  - name:/ {on=0} on && /^    /{print} /^[a-z]/ && on {on=0}' "$1" | tr -s ' \n' '  ' | sed 's/ $//'; }

# 1. The production kustomization gets both digests and keeps no placeholder.
cp "$SRC" "$WORK/k.yaml"
status=0
bash "$SCRIPT" "$WORK/k.yaml" "$SVC" "$WEB" 2>"$WORK/err" || status=$?
check "production: exit 0" "0" "$status"
check "production: no newTag local left" "0" "$(grep -c 'newTag: local' "$WORK/k.yaml" || true)"
check "production: service digest" " - name: bugbarn/service newName: ghcr.io/webwiebe/bugbarn/service digest: $SVC" "$(block "$WORK/k.yaml" bugbarn/service)"
check "production: web digest" " - name: bugbarn/web newName: ghcr.io/webwiebe/bugbarn/web digest: $WEB" "$(block "$WORK/k.yaml" bugbarn/web)"
check "production: rest unchanged" "$(grep -v -e 'newTag: local' "$SRC")" "$(grep -v -e '    digest: sha256:' "$WORK/k.yaml")"

# 2. A second run finds no placeholder and fails, so a deploy cannot pin twice
#    without noticing.
cp "$WORK/k.yaml" "$WORK/pinned.yaml"
status=0
bash "$SCRIPT" "$WORK/k.yaml" "$SVC" "$WEB" 2>"$WORK/err" || status=$?
check "already pinned: exit 1" "1" "$status"
check "already pinned: says so" "pin-images: replaced 0 placeholder tags, want 2" "$(cat "$WORK/err")"
check "already pinned: file untouched" "" "$(diff "$WORK/pinned.yaml" "$WORK/k.yaml")"

# 3. An empty or malformed digest fails before the file is touched.
cp "$SRC" "$WORK/k.yaml"
status=0
bash "$SCRIPT" "$WORK/k.yaml" "" "$WEB" 2>"$WORK/err" || status=$?
check "empty digest: exit 1" "1" "$status"
check "empty digest: file untouched" "" "$(diff "$SRC" "$WORK/k.yaml")"

if [ "$fail" -ne 0 ]; then exit 1; fi
echo "pin-images tests passed"
