#!/bin/sh
# Tests scripts/check-govulncheck.sh with a fake `go` that exits with a chosen
# govulncheck status.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin"
cat >"$tmp/bin/go" <<'SHIM'
#!/bin/sh
echo "go $*" >>"$FAKE_GO_LOG"
[ "$1" = install ] || exit 99
[ "${FAKE_INSTALL_FAIL:-}" = 1 ] && exit 1
printf '#!/bin/sh\necho "govulncheck $*" >>"%s"\nexit %s\n' "$FAKE_GO_LOG" "$FAKE_STATUS" >"$GOBIN/govulncheck"
chmod +x "$GOBIN/govulncheck"
SHIM
chmod +x "$tmp/bin/go"

fail() { echo "FAIL: $1"; exit 1; }
run() { PATH="$tmp/bin:$PATH" FAKE_STATUS="$1" FAKE_GO_LOG="$tmp/log" FAKE_INSTALL_FAIL="${FAKE_INSTALL_FAIL:-}" sh "$here/check-govulncheck.sh" >"$tmp/out" 2>&1; }

# 1. A clean scan passes.
run 0 || fail "clean scan failed"

# 2. Called vulnerabilities exit 1, and the gate names govulncheck.
status=0
run 3 || status=$?
[ "$status" = 1 ] || fail "vulnerable scan exited $status, want 1"
grep -q 'calls a vulnerable symbol' "$tmp/out" || fail "vulnerable scan did not say why"

# 3. A scan that could not run is not a pass.
status=0
run 1 || status=$?
[ "$status" = 2 ] || fail "broken scan exited $status, want 2"

# 4. A failed install is a failed scan.
status=0
FAKE_INSTALL_FAIL=1 run 0 || status=$?
[ "$status" = 2 ] || fail "failed install exited $status, want 2"

# 5. The tool is installed at the pinned version and run over the whole module.
grep -q 'install golang.org/x/vuln/cmd/govulncheck@v[0-9]' "$tmp/log" || fail "govulncheck version is not pinned"
grep -q 'govulncheck ./...' "$tmp/log" || fail "scan does not cover ./..."

echo "check-govulncheck_test: OK"
