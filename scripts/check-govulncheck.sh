#!/bin/sh
# Blocking vulnerability gate: govulncheck over the root module.
#
# A Go advisory (GO-2026-6859, net/http, 2026-11-11) never turned main red here
# because no step scanned for them (#274). govulncheck reports only
# vulnerabilities in code this build calls, so a finding means the shipped
# binary can reach it; the fix is a newer Go toolchain (go.mod `toolchain` line
# and the Dockerfile builder tag) or a newer dependency.
#
# The version is pinned here, in one committed file, so a new govulncheck
# release cannot change what CI enforces without a commit. The advisory
# database is fetched live, which is the point: an advisory published today
# fails the next run without any commit on our side.
#
# sdks/go is not scanned: it is a library built by its consumers' Go, so its
# stdlib findings describe whichever toolchain runs the scan.
#
# Exit codes: 0 clean, 1 vulnerabilities found, 2 the scan itself failed (no
# network, no module cache). Both non-zero codes block; the message says which.
set -u

VERSION="${GOVULNCHECK_VERSION:-v1.9.0}"

# `go run` reports every child failure as exit 1, which would hide govulncheck's
# own "vulnerabilities found" status (3). Install the binary and run it directly.
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
if ! GOBIN="$bin" go install "golang.org/x/vuln/cmd/govulncheck@$VERSION" >&2; then
	echo "FAIL (govulncheck $VERSION): could not install govulncheck." >&2
	exit 2
fi

out=$("$bin/govulncheck" ./... 2>&1)
status=$?
printf '%s\n' "$out"

case "$status" in
0)
	echo "govulncheck $VERSION: OK (no called vulnerabilities)"
	;;
3)
	echo "FAIL (govulncheck $VERSION): the build calls a vulnerable symbol; see the report above." >&2
	exit 1
	;;
*)
	echo "FAIL (govulncheck $VERSION): the scan did not run to completion (exit $status)." >&2
	exit 2
	;;
esac
