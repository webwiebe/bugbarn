#!/bin/sh
# Tests scripts/heal-go-toolchain.sh with a fake `go` that reports a chosen
# GOROOT and GOMODCACHE.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'chmod -R u+w "$tmp"; rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin" "$tmp/mod/golang.org/toolchain@v0.0.1-go1.27.3.darwin-arm64"
cat >"$tmp/bin/go" <<SHIM
#!/bin/sh
case "\$2" in
GOROOT) echo "\$FAKE_GOROOT" ;;
GOMODCACHE) echo "$tmp/mod" ;;
esac
SHIM
chmod +x "$tmp/bin/go"
tc="$tmp/mod/golang.org/toolchain@v0.0.1-go1.27.3.darwin-arm64"

fail() { echo "FAIL: $1"; exit 1; }
run() { PATH="$tmp/bin:$PATH" FAKE_GOROOT="$1" sh "$here/heal-go-toolchain.sh"; }

# 1. A toolchain without go.env is removed, even when its files are read-only.
mkdir -p "$tc/bin" "$tc/src"
chmod -R a-w "$tc"
run "$tc" >/dev/null
[ ! -e "$tc" ] || fail "incomplete toolchain was kept"

# 2. A complete toolchain stays.
mkdir -p "$tc"
echo x >"$tc/go.env"
echo x >"$tc/VERSION"
run "$tc" >/dev/null
[ -f "$tc/go.env" ] || fail "complete toolchain was removed"

# 3. The host's own Go is never touched, whatever it holds.
mkdir -p "$tmp/hostgo"
run "$tmp/hostgo" >/dev/null
[ -d "$tmp/hostgo" ] || fail "host Go was removed"

echo "heal-go-toolchain_test: ok"
