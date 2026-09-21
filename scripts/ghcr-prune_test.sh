#!/usr/bin/env bash
# Tests for scripts/ghcr-prune.sh. A fake `curl` on PATH serves one package's
# version list and manifests, and records every DELETE it receives.
#
# Run it with: bash scripts/ghcr-prune_test.sh   (or `make test-gates`)
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

now() { date -u -v-"$1"d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "$1 days ago" +%Y-%m-%dT%H:%M:%SZ; }

# id, digest, age in days, tags. Each release is an index (sha:rN) whose child
# is the commit-sha version (sha:cN), as `imagetools create` leaves them.
cat > "$WORK/fixture.tsv" <<EOF
1	sha:r5	1	v0.1.5
2	sha:c5	1	aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
3	sha:r4	20	v0.1.4
4	sha:c4	20	bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
5	sha:r3	30	v0.1.3
6	sha:c3	30	cccccccccccccccccccccccccccccccccccccccc
7	sha:r2	40	v0.1.2
8	sha:c2	40	dddddddddddddddddddddddddddddddddddddddd
9	sha:stray	50	v0.9.0
10	sha:old	60	eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
11	sha:untagged	60
12	sha:named	90	latest
13	sha:running	70	ffffffffffffffffffffffffffffffffffffffff
14	sha:runningtag	80	0000000000000000000000000000000000000000
EOF

jq -Rn '[inputs | split("\t") | {id: (.[0]|tonumber), name: .[1], created_at: .[2],
  metadata: {container: {tags: (if .[3] == "" then [] else (.[3] | split(",")) end)}}}]' \
  < <(while IFS=$'\t' read -r id d age tags; do printf '%s\t%s\t%s\t%s\n' "$id" "$d" "$(now "$age")" "$tags"; done < "$WORK/fixture.tsv") \
  > "$WORK/versions.json"

mkdir -p "$WORK/bin"
cat > "$WORK/bin/curl" <<'EOF'
#!/usr/bin/env bash
url="${*: -1}"
case "$*" in
  *"-X DELETE"*) echo "${url##*/}" >> "$FAKE_DIR/deleted"; exit 0 ;;
esac
case "$url" in
  *"/versions?"*"page=1") cat "$FAKE_DIR/versions.json" ;;
  *"/versions?"*) echo '[]' ;;
  *"ghcr.io/token"*) echo '{"token":"t"}' ;;
  *"/manifests/sha:r"[0-9]) n="${url##*sha:r}"; echo "{\"manifests\":[{\"digest\":\"sha:c$n\"}]}" ;;
  *"/manifests/"*) echo '{"layers":[]}' ;;
  *) echo "fake curl: unexpected $url" >&2; exit 1 ;;
esac
EOF
chmod +x "$WORK/bin/curl"

run() {
  : > "$WORK/deleted"
  PATH="$WORK/bin:$PATH" FAKE_DIR="$WORK" GHCR_TOKEN=x PACKAGES=bugbarn/service \
    KEEP_DAYS=7 KEEP_RELEASES=3 "$@" bash "$REPO/scripts/ghcr-prune.sh" > "$WORK/out" 2>&1
}

fail=0
check() {
  if [ "$1" = "$2" ]; then echo "ok   $3"; else echo "FAIL $3: want [$1] got [$2]"; fail=1; fi
}

printf '%s\n' 'ghcr.io/webwiebe/bugbarn/service@sha:running' \
  'ghcr.io/webwiebe/bugbarn/service:0000000000000000000000000000000000000000' \
  'ghcr.io/webwiebe/bugbarn/web@sha:r2' > "$WORK/protect"

run env PROTECT_FILE="$WORK/protect"
# Kept: r5/c5 (young), r4/c4 and r3/c3 (newest releases by push time; the stray
# v0.9.0 is older, so it neither counts nor survives), named, running,
# runningtag. r2/c2 is only protected for the web image.
check "7 8 9 10 11" "$(sort -n "$WORK/deleted" | tr '\n' ' ' | sed 's/ $//')" "deletes exactly the unprotected versions"

run env PROTECT_FILE="$WORK/protect" MAX_DELETE=2
check "10 11" "$(sort -n "$WORK/deleted" | tr '\n' ' ' | sed 's/ $//')" "MAX_DELETE caps the run, oldest first"

run env PROTECT_FILE="$WORK/protect" DRY_RUN=1
check "" "$(cat "$WORK/deleted")" "DRY_RUN deletes nothing"

: > "$WORK/empty"
set +e; run env PROTECT_FILE="$WORK/empty"; rc=$?; set -e
check "1 " "$rc $(cat "$WORK/deleted")" "refuses to run without running images"

exit "$fail"
