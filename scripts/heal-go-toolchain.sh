#!/bin/sh
# Removes a half-extracted Go toolchain from GOMODCACHE.
#
# go.mod pins `toolchain go1.27.4`, and the runners' Go is older, so every
# `go` call switches to the toolchain extracted under
# $GOMODCACHE/golang.org/toolchain@v0.0.1-go<ver>.<os>-<arch>. A disk-full or
# a janitor sweep can leave that directory with bin/, lib/, pkg/ and src/ but
# without go.env and VERSION. Go then runs with no defaults: GOPROXY and GOSUMDB
# are empty and every module command fails with
#
#   go: github.com/XSAM/otelsql@v0.42.0: GOPROXY list is not the empty string,
#   but contains no entries
#
# before any lint or test runs (bugbarn#268 and #269 checks, 2026-11-10). The
# zip next to it in cache/download is intact, so deleting the directory makes
# the next `go` call extract it again.
set -eu

goroot=$(go env GOROOT)
modcache=$(go env GOMODCACHE)

case "$goroot" in
"$modcache"/golang.org/toolchain@*) ;;
*) exit 0 ;; # the host's own Go, nothing cached to heal
esac

if [ -f "$goroot/go.env" ] && [ -f "$goroot/VERSION" ]; then
	exit 0
fi

echo "heal-go-toolchain: $goroot is incomplete (no go.env or VERSION), removing it"
chmod -R u+w "$goroot"
rm -rf "$goroot"
