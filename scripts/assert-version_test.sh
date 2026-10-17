#!/usr/bin/env bash
# Tests for deploy/scripts/assert-version.sh, and for the deploy wiring it needs.
# The smoke only proves anything if it fails on a mismatch and every deploy
# pipeline runs it, so this runs the script against a stub kubectl (a match
# passes; a wrong version, a wrong commit and an empty reply each fail) and
# greps the kustomizations and pipelines.
#
# Run it with: bash scripts/assert-version_test.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$REPO/deploy/scripts/assert-version.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SHA=0123456789abcdef0123456789abcdef01234567

failures=0
check() { # name, exit status of the assertion just made
  if [ "$2" -eq 0 ]; then echo "ok   $1"; else echo "FAIL $1"; failures=$((failures + 1)); fi
}

mkdir -p "$WORK/bin"
cat > "$WORK/bin/kubectl" <<'STUB'
#!/bin/sh
printf '%s' "$KUBECTL_REPLY"
STUB
chmod +x "$WORK/bin/kubectl"

run() { # reply, version, commit
  KUBECTL_REPLY="$1" PATH="$WORK/bin:$PATH" sh "$SCRIPT" ns deploy/bugbarn-writer bugbarn "$2" "$3" >"$WORK/out" 2>"$WORK/err"
}

reply="{\"version\":\"v1.2.3\",\"commit\":\"$SHA\",\"built\":\"2026-10-17T12:00:00Z\"}"

rc=0; run "$reply" v1.2.3 "$SHA" || rc=$?
check "matching version and commit pass" "$rc"

rc=0; run "$reply" v1.2.2 "$SHA" || rc=$?
{ [ "$rc" -eq 1 ] && grep -q "wanted version v1.2.2" "$WORK/err"; } && s=0 || s=1
check "wrong version fails" "$s"

rc=0; run "$reply" v1.2.3 ffffffffffffffffffffffffffffffffffffffff || rc=$?
{ [ "$rc" -eq 1 ] && grep -q "wanted commit" "$WORK/err"; } && s=0 || s=1
check "wrong commit fails" "$s"

rc=0; run "" v1.2.3 "$SHA" || rc=$?
[ "$rc" -eq 1 ] && s=0 || s=1
check "empty reply fails" "$s"

rc=0; sh "$SCRIPT" >/dev/null 2>&1 || rc=$?
[ "$rc" -eq 2 ] && s=0 || s=1
check "wrong argument count is a usage error" "$s"

# Wiring: every environment states the version in the pod spec and runs the smoke.
for env in testing staging production; do
  k="$REPO/deploy/k8s/$env/kustomization.yaml"
  want=2; [ "$env" = testing ] && want=1
  n=$(grep -c 'name: BUGBARN_VERSION' "$k" || true)
  { [ "$n" -eq "$want" ] && grep -q 'value: "__VERSION__"' "$k"; } && s=0 || s=1
  check "$env kustomization sets BUGBARN_VERSION on $want deployment(s)" "$s"
done
for pipe in build-and-test:1 release:2 deploy-production:2; do
  f="$REPO/.woodpecker/${pipe%%:*}.yaml"; want="${pipe##*:}"
  n=$(grep -c 'scripts/assert-version.sh' "$f" || true)
  { [ "$n" -eq "$want" ] && grep -q '__VERSION__' "$f"; } && s=0 || s=1
  check "${pipe%%:*} substitutes the version and runs the smoke $want time(s)" "$s"
done
{ grep -q -- '--build-arg COMMIT=' "$REPO/.woodpecker/build-and-test.yaml" \
  && grep -q -- '-X main.Commit=${COMMIT}' "$REPO/deploy/docker/service.Dockerfile"; } && s=0 || s=1
check "image build passes the commit to the binary" "$s"
grep -q 'PREV_VERSION' "$REPO/.woodpecker/deploy-production.yaml" && s=0 || s=1
check "production rollback restores the previous version" "$s"

[ "$failures" -eq 0 ] || { echo "$failures check(s) failed"; exit 1; }
echo "all checks passed"
