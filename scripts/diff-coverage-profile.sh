#!/bin/sh
#
# diff-coverage-profile.sh — the profile scripts/diff-coverage.sh reads.
#
# check-coverage.sh profiles the root module into coverage/go.out and ratchets
# its total, so a nested module cannot be appended there without moving that
# total. This writes coverage/diff.out instead: coverage/go.out plus a profile
# of every nested Go module (sdks/go), so a change there is measured like one
# in the root module. Run it after check-coverage.sh.
#
# Portable POSIX sh, same as the other gates.
set -eu

ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"
[ -s coverage/go.out ] || {
  echo "::error::coverage/go.out is missing or empty; run scripts/check-coverage.sh first" >&2
  exit 3
}
cp coverage/go.out coverage/diff.out
for gomod in $(git ls-files -- '*/go.mod'); do
  dir=$(dirname "$gomod")
  echo "[diff-coverage-profile] $dir"
  (cd "$dir" && go test -covermode=atomic -coverprofile="$ROOT/coverage/module.out" ./...)
  cat coverage/module.out >> coverage/diff.out
done
rm -f coverage/module.out
