#!/bin/sh
# Measures total Go statement coverage and ratchets it against a committed
# baseline.
#
# The baseline is a FLOOR, and the gate fails in both directions:
#
#   total < baseline               -> FAIL, coverage dropped
#   total > baseline + MAX_DRIFT   -> FAIL, the baseline is beatable and must
#                                     be raised in this same commit
#
# The second half is what makes this a ratchet. It used to only print
# "NOTE: coverage rose to X%; bump the baseline to lock it in", so the note
# became routine CI noise and the baseline sat 2.9pp below reality for weeks —
# meaning nearly 3pp of real coverage could be deleted without the gate
# noticing. See 03_Patterns/pattern--ratchet-gate, property 3.
#
# MAX_DRIFT is 3.0 and not tighter because this number is not identical across
# machines: the same commit (781a86a) measured 49.9% on a loaded CI runner and
# 51.1% on an idle one, a 1.2pp spread from timing-dependent paths in the
# worker/spool/queue loops. A drift band narrower than that spread would fail
# the build at random, and a gate that fails at random gets switched off.
#
# The baseline sits at least 1.0pp under the lowest observed measurement, so a
# legitimate refactor that deletes tested code does not break the build.
#
# A second rule applies to every Go package that has statements, in the root
# module and in each nested module (sdks/go): its own coverage must reach
# MIN_PACKAGE, a constant with no allowlist and no override. Each failing
# package is named with its number. Packages without statements are skipped.
# The total ratchet above is unchanged.
#
# Runs on the runner rather than inside the cache-only Docker build, because the
# coverage profile cannot escape `--output type=cacheonly`.
set -eu

BASELINE_FILE="scripts/coverage-baseline.txt"
MAX_DRIFT="3.0"
MIN_PACKAGE="80.0"
PROFILE="coverage/go.out"

mkdir -p coverage

# Measure OUR packages only. `go test ./...` does not skip node_modules, so once
# a node step has run `pnpm install` the tree contains vendored third-party Go code
# (e.g. web/node_modules/flatted/golang/...) which lands in the profile at 0%
# and drags the total down. That made the number depend on whether an unrelated
# step ran first: CI reported 46.4% where a clean local tree reported 47.3%, so
# the baseline could not be computed or reproduced locally at all.
pkgs=$(go list ./... | grep -v '/node_modules/')
test -n "$pkgs" || {
	echo "ERROR: no packages to test"
	exit 1
}
# shellcheck disable=SC2086
go test -covermode=atomic -coverprofile="$PROFILE" $pkgs

total=$(go tool cover -func="$PROFILE" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')
case "$total" in
'' | *[!0-9.]*)
	echo "FAIL: could not read a coverage total out of $PROFILE — the test run did not produce one"
	exit 1
	;;
esac
echo "Total Go coverage: ${total}%"

if [ ! -f "$BASELINE_FILE" ]; then
	echo "No baseline at $BASELINE_FILE — create it with: echo $total > $BASELINE_FILE"
	exit 1
fi
baseline=$(tr -d ' \t\n' <"$BASELINE_FILE")
case "$baseline" in
'' | *[!0-9.]*)
	echo "FAIL: baseline in $BASELINE_FILE is '$baseline', which is not a number"
	exit 1
	;;
esac

if awk -v b="$baseline" -v t="$total" 'BEGIN { exit !(t < b) }'; then
	echo "FAIL: coverage ${total}% dropped below the baseline of ${baseline}%"
	exit 1
fi

if awk -v b="$baseline" -v t="$total" -v d="$MAX_DRIFT" 'BEGIN { exit !(t > b + d) }'; then
	suggested=$(awk -v t="$total" 'BEGIN { printf "%.1f", t - 1.0 }')
	echo "FAIL: coverage is ${total}% against a baseline of ${baseline}% — more than ${MAX_DRIFT}pp of the gain is unprotected."
	echo "  Raise the floor in this same commit:"
	echo "    echo $suggested > $BASELINE_FILE"
	exit 1
fi

# Per-package minimum. Nested modules are profiled separately so their
# statements do not move the root total that the ratchet above measures.
ROOT=$(pwd)
PKG_PROFILES="$PROFILE"
n=0
# Skip module caches: the Makefile keeps GOMODCACHE in .cache/go-mod inside
# the checkout, and walking into it would test third-party modules.
for gomod in $(find . -name go.mod -not -path './go.mod' -not -path '*/node_modules/*' \
	-not -path './.cache/*' -not -path '*/pkg/mod/*'); do
	n=$((n + 1))
	out="coverage/module-$n.out"
	(cd "$(dirname "$gomod")" && go test -covermode=atomic -coverprofile="$ROOT/$out" ./...)
	PKG_PROFILES="$PKG_PROFILES $out"
done

# shellcheck disable=SC2086
low=$(awk -v min="$MIN_PACKAGE" '
	/^mode:/ { next }
	{
		pkg = $1
		sub(/:[^:]*$/, "", pkg)
		sub(/\/[^\/]*$/, "", pkg)
		stmts[pkg] += $2
		if ($3 > 0) covered[pkg] += $2
	}
	END {
		for (p in stmts) {
			if (stmts[p] == 0) continue
			pct = 100 * covered[p] / stmts[p]
			if (pct < min) printf "  %s %.1f%%\n", p, pct
		}
	}' $PKG_PROFILES | sort)
if [ -n "$low" ]; then
	echo "FAIL: packages below the ${MIN_PACKAGE}% minimum:"
	echo "$low"
	exit 1
fi

echo "coverage: OK (${total}% within [${baseline}%, ${baseline}% + ${MAX_DRIFT}pp], every package at or above ${MIN_PACKAGE}%)"
