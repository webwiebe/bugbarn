#!/bin/sh
# Installs the pinned golangci-lint into ./.tools and verifies the version.
#
# The version lives here, in a committed file, rather than being duplicated in
# every CI file. Two pipelines run these gates (Woodpecker `ci.yaml` and the
# GitHub `ci.yml` mirror) and they had drifted to different golangci-lint
# versions, which means they enforced different linters and produced different
# finding counts for the same commit.
#
# It must be at least as new as the host Go toolchain: the gates run on the
# host and golangci-lint typechecks against that Go's stdlib source. v2.11.4
# (built with go1.26) panicked "file requires newer Go version go1.27" the
# moment the runners moved to Go 1.27. Bump this in step with the runners' Go.
set -eu

VERSION="v2.13.2"
DEST="$(pwd)/.tools"

mkdir -p "$DEST"
if [ ! -x "$DEST/golangci-lint" ] || ! "$DEST/golangci-lint" version 2>/dev/null | grep -q "${VERSION#v}"; then
	curl -sSfL "https://raw.githubusercontent.com/golangci/golangci-lint/$VERSION/install.sh" | sh -s -- -b "$DEST" "$VERSION"
fi
"$DEST/golangci-lint" version
