#!/bin/sh
# Tests for this repo's CI gate scripts.
#
# The gates are the only thing standing between a regression and production,
# and until now none of them had a test. A gate that quietly stops failing is
# indistinguishable from a clean tree — which is exactly how the coverage
# ratchet came to sit 2.9pp below reality without anyone noticing.
#
# Every count-based gate is checked against all four cases the ratchet contract
# requires: violation present (fails), violation baselined (passes), baseline
# beatable (fails), clean tree (passes).
#
# Run it with: sh scripts/gates_test.sh   (or `make test-gates`)
set -eu

REPO="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT INT TERM

pass=0
fail=0

# expect <expected-exit> <name> -- <command...>
expect() {
	_want="$1"
	_name="$2"
	shift 3 # drop expected, name, and the literal --
	set +e
	_out=$("$@" 2>&1)
	_rc=$?
	set -e
	if [ "$_rc" -eq "$_want" ]; then
		pass=$((pass + 1))
		printf 'ok   %s\n' "$_name"
	else
		fail=$((fail + 1))
		printf 'FAIL %s (exit %s, wanted %s)\n' "$_name" "$_rc" "$_want"
		printf '%s\n' "$_out" | sed 's/^/       | /'
	fi
}

# ---------------------------------------------------------------- ratchet.sh
RATCHET="$REPO/scripts/ratchet.sh"
b="$WORK/baseline.txt"

echo 5 >"$b"
expect 1 "ratchet: a new violation fails" -- sh "$RATCHET" t "$b" 6 0
expect 0 "ratchet: a baselined violation passes" -- sh "$RATCHET" t "$b" 5 0
expect 1 "ratchet: a beatable baseline fails as stale" -- sh "$RATCHET" t "$b" 4 0
echo 0 >"$b"
expect 0 "ratchet: a clean tree at a zero baseline passes" -- sh "$RATCHET" t "$b" 0 0
expect 1 "ratchet: a missing baseline file fails" -- sh "$RATCHET" t "$WORK/absent.txt" 3 0
expect 1 "ratchet: a non-numeric count fails (collector did not run)" -- sh "$RATCHET" t "$b" "" 0
printf 'nonsense\n' >"$WORK/bad-baseline.txt"
expect 1 "ratchet: a non-numeric baseline fails" -- sh "$RATCHET" t "$WORK/bad-baseline.txt" 1 0

# The refresh command a stale baseline prints must be the literal command that
# fixes it — a message naming the wrong file is how a ratchet gets ignored.
echo 5 >"$b"
if sh "$RATCHET" t "$b" 4 0 2>&1 | grep -q "echo 4 > $b"; then
	pass=$((pass + 1))
	echo "ok   ratchet: names the exact refresh command"
else
	fail=$((fail + 1))
	echo "FAIL ratchet: does not name the exact refresh command"
fi

# --------------------------------------------------------- count-findings.py
COUNT="$REPO/scripts/count-findings.py"

printf '{"Issues":[{"FromLinter":"unused"},{"FromLinter":"depguard"},{"FromLinter":"unused"}]}\n' >"$WORK/gl.json"
printf '{"Issues":null}\n' >"$WORK/gl-clean.json"
printf 'not json at all\n' >"$WORK/gl-broken.json"
printf '{"Report":{}}\n' >"$WORK/gl-wrong.json"
printf '[{"code":"eslint(complexity)"},{"code":"eslint(no-undef)"},{"code":"eslint(max-lines)"}]\n' >"$WORK/ox.json"
printf '{"diagnostics":[]}\n' >"$WORK/ox-wrong.json"

expect 1 "count: a missing report fails (did not run != clean)" -- python3 "$COUNT" golangci "$WORK/absent.json"
expect 1 "count: a malformed report fails" -- python3 "$COUNT" golangci "$WORK/gl-broken.json"
expect 1 "count: a report with no Issues key fails" -- python3 "$COUNT" golangci "$WORK/gl-wrong.json"
expect 0 "count: a clean report is accepted" -- python3 "$COUNT" golangci "$WORK/gl-clean.json"
expect 1 "count: a missing oxlint report fails" -- python3 "$COUNT" oxlint "$WORK/absent.json" "eslint(complexity)"
expect 1 "count: an oxlint report that is not an array fails" -- python3 "$COUNT" oxlint "$WORK/ox-wrong.json" "eslint(complexity)"

check_value() {
	_name="$1"
	_want="$2"
	shift 2
	_got=$("$@" 2>&1 || true)
	if [ "$_got" = "$_want" ]; then
		pass=$((pass + 1))
		printf 'ok   %s\n' "$_name"
	else
		fail=$((fail + 1))
		printf 'FAIL %s (got "%s", wanted "%s")\n' "$_name" "$_got" "$_want"
	fi
}

check_value "count: golangci total" 3 python3 "$COUNT" golangci "$WORK/gl.json"
check_value "count: golangci filtered by linter" 2 python3 "$COUNT" golangci "$WORK/gl.json" unused
check_value "count: golangci clean report is zero" 0 python3 "$COUNT" golangci "$WORK/gl-clean.json"
check_value "count: oxlint counts only the named rules" 2 python3 "$COUNT" oxlint "$WORK/ox.json" "eslint(complexity)" "eslint(max-lines)"

# ------------------------------------------------------- check-file-length.sh
# The 500-line cap is already blocking with an empty allowlist, so the
# allowlist branch cannot be exercised against the committed script. The two
# allowlist cases below run against a copy with one entry injected, which is
# the only way to cover shrink-only behaviour without weakening the real gate.
FL="$WORK/filelength"
mkdir -p "$FL/scripts"
cp "$REPO/scripts/check-file-length.sh" "$FL/scripts/"
(
	cd "$FL"
	git init -q .
	git config user.email t@example.invalid
	git config user.name t
	printf 'package main\n' >small.go
	git add -A
) >/dev/null 2>&1

expect 0 "file-length: a clean tree passes" -- sh -c "cd '$FL' && sh scripts/check-file-length.sh"

(
	cd "$FL"
	i=0
	while [ "$i" -lt 501 ]; do
		echo "// line $i" >>big.go
		i=$((i + 1))
	done
	git add -A
) >/dev/null 2>&1
expect 1 "file-length: an over-limit file fails" -- sh -c "cd '$FL' && sh scripts/check-file-length.sh"

sed 's|^ALLOWLIST="$|ALLOWLIST="big.go 501|' "$FL/scripts/check-file-length.sh" >"$FL/scripts/allowlisted.sh"
grep -q 'big.go 501' "$FL/scripts/allowlisted.sh" || {
	echo "FAIL file-length: could not inject an allowlist entry (script shape changed)"
	fail=$((fail + 1))
}
expect 0 "file-length: an allowlisted file at its recorded size passes" -- sh -c "cd '$FL' && sh scripts/allowlisted.sh"
(
	cd "$FL"
	echo '// one more' >>big.go
	git add -A
) >/dev/null 2>&1
expect 1 "file-length: an allowlisted file that grew fails" -- sh -c "cd '$FL' && sh scripts/allowlisted.sh"

# --------------------------------------------------------- check-coverage.sh
# A throwaway module with exactly ten statements, nine of them covered, so the
# measured total is a stable 90.0% and the band cases are exact. The package
# minimum (80.0) holds for its only package.
CV="$WORK/coverage"
mkdir -p "$CV/scripts"
cp "$REPO/scripts/check-coverage.sh" "$CV/scripts/"
cat >"$CV/go.mod" <<'EOF'
module gatetest

go 1.21
EOF
# writepkg DIR NAME STATEMENTS COVERED: a package of one-statement functions
# whose test calls the first COVERED of them.
writepkg() {
	mkdir -p "$1"
	{
		echo "package $2"
		i=1
		while [ "$i" -le "$3" ]; do
			echo "func F$i() int { return $i }"
			i=$((i + 1))
		done
	} >"$1/lib.go"
	{
		printf 'package %s\n\nimport "testing"\n\nfunc TestCovered(t *testing.T) {\n' "$2"
		i=1
		while [ "$i" -le "$4" ]; do
			printf '\tif F%s() != %s {\n\t\tt.Fatal("F%s")\n\t}\n' "$i" "$i" "$i"
			i=$((i + 1))
		done
		echo "}"
	} >"$1/lib_test.go"
}
writepkg "$CV" gatetest 10 9

measured=$(cd "$CV" && go test -covermode=atomic -coverprofile=c.out ./... >/dev/null 2>&1 && go tool cover -func=c.out | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')
if [ "$measured" != "90.0" ]; then
	fail=$((fail + 1))
	echo "FAIL coverage: fixture measured ${measured}%, expected 90.0%; the rest of this section is meaningless"
else
	pass=$((pass + 1))
	echo "ok   coverage: fixture measures a stable 90.0%"

	echo 89.0 >"$CV/scripts/coverage-baseline.txt"
	expect 0 "coverage: inside the band passes" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	echo 95.0 >"$CV/scripts/coverage-baseline.txt"
	expect 1 "coverage: below the floor fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	echo 80.0 >"$CV/scripts/coverage-baseline.txt"
	expect 1 "coverage: a beatable baseline fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	echo 80.0 >"$CV/scripts/coverage-baseline.txt"
	if (cd "$CV" && sh scripts/check-coverage.sh 2>&1) | grep -q 'echo 89.0 > scripts/coverage-baseline.txt'; then
		pass=$((pass + 1))
		echo "ok   coverage: names the exact refresh command"
	else
		fail=$((fail + 1))
		echo "FAIL coverage: does not name the exact refresh command"
	fi

	rm -f "$CV/scripts/coverage-baseline.txt"
	expect 1 "coverage: a missing baseline fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	printf 'nonsense\n' >"$CV/scripts/coverage-baseline.txt"
	expect 1 "coverage: a non-numeric baseline fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	# Per-package minimum. A statement-less package and a package sitting
	# exactly on 80.0% (4 of 5) pass; a package at 75.0% (3 of 4) fails and is
	# named with its number. The total stays inside the band in both cases.
	mkdir -p "$CV/empty"
	printf 'package empty\n\ntype T struct{}\n' >"$CV/empty/types.go"
	writepkg "$CV/edge" edge 5 4
	echo 86.0 >"$CV/scripts/coverage-baseline.txt"
	expect 0 "coverage: packages at the minimum and statement-less packages pass" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	writepkg "$CV/low" low 4 3
	echo 84.0 >"$CV/scripts/coverage-baseline.txt"
	expect 1 "coverage: a package below the minimum fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"
	if (cd "$CV" && sh scripts/check-coverage.sh 2>&1) | grep -q 'gatetest/low 75.0%'; then
		pass=$((pass + 1))
		echo "ok   coverage: names the failing package with its number"
	else
		fail=$((fail + 1))
		echo "FAIL coverage: does not name the failing package with its number"
	fi

	# A nested module is measured too.
	rm -rf "$CV/low"
	echo 86.0 >"$CV/scripts/coverage-baseline.txt"
	mkdir -p "$CV/nested"
	printf 'module gatenested\n\ngo 1.21\n' >"$CV/nested/go.mod"
	writepkg "$CV/nested" nested 4 3
	expect 1 "coverage: a nested module package below the minimum fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	# A module in the in-checkout module cache is skipped.
	rm -rf "$CV/nested"
	mkdir -p "$CV/.cache/go-mod/example.com/dep@v1.0.0"
	printf 'module example.com/dep\n\ngo 1.21\n' >"$CV/.cache/go-mod/example.com/dep@v1.0.0/go.mod"
	writepkg "$CV/.cache/go-mod/example.com/dep@v1.0.0" dep 4 0
	expect 0 "coverage: a module under .cache/go-mod is not measured" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"
fi

# Runner scope: no workflow step may reach into another runner's directory. The
# repo's own workflows must pass; fixtures with the old glob, a missing scope
# and no reclaim step at all must fail. Text only, no step is ever executed.
expect 0 "runner scope: the repo's workflows are scoped" -- sh "$REPO/scripts/check-runner-scope.sh" "$REPO/.github/workflows"

RS="$WORK/runner-scope"
mkdir -p "$RS/glob" "$RS/unscoped" "$RS/none"
cat >"$RS/glob/w.yml" <<'EOF'
jobs:
  a:
    steps:
      - name: Reclaim workspaces (clear read-only Go cache from prior runs)
        run: |
          for c in "$HOME"/github-runners/*/_work/bugbarn/bugbarn/.cache; do
            rm -rf "$c"
          done
EOF
expect 1 "runner scope: a glob over the runners root fails" -- sh "$REPO/scripts/check-runner-scope.sh" "$RS/glob"

cat >"$RS/unscoped/w.yml" <<'EOF'
jobs:
  a:
    steps:
      - name: Reclaim workspace (clear read-only Go cache from prior runs)
        run: rm -rf .cache
EOF
expect 1 "runner scope: a reclaim step without the workspace scope fails" -- sh "$REPO/scripts/check-runner-scope.sh" "$RS/unscoped"

cat >"$RS/none/w.yml" <<'EOF'
jobs:
  a:
    steps:
      - run: echo hi
EOF
expect 1 "runner scope: no reclaim step at all fails" -- sh "$REPO/scripts/check-runner-scope.sh" "$RS/none"

# ------------------------------------------------- check-typed-boundaries.sh
# Report-only before 2026-10-31, failing from that date. The input floor fails
# in both modes. The fixture has 30 clean files, the floor.
TB="$WORK/typed-boundaries"
TBS="$REPO/scripts/check-typed-boundaries.sh"
mkdir -p "$TB/scripts" "$TB/internal/service" "$TB/internal/api"
i=0
while [ "$i" -lt 30 ]; do
	printf 'package service\n' >"$TB/internal/service/f$i.go"
	i=$((i + 1))
done
echo 0 >"$TB/scripts/typed-boundaries-baseline.txt"
expect 0 "typed-boundaries: a clean tree passes before the date" -- sh "$TBS" "$TB" 2026-10-17
expect 0 "typed-boundaries: a clean tree passes after the date" -- sh "$TBS" "$TB" 2026-11-01

printf 'package api\n\nimport "database/sql"\n\nvar _ = sql.ErrNoRows\n' >"$TB/internal/api/leak.go"
expect 0 "typed-boundaries: a raw DB leak is only reported before the date" -- sh "$TBS" "$TB" 2026-10-30
expect 1 "typed-boundaries: a raw DB leak fails from the date" -- sh "$TBS" "$TB" 2026-10-31
if sh "$TBS" "$TB" 2026-10-30 2>&1 | grep -q 'REPORT (typed-boundaries'; then
	pass=$((pass + 1))
	echo "ok   typed-boundaries: report mode names the finding"
else
	fail=$((fail + 1))
	echo "FAIL typed-boundaries: report mode printed no REPORT line"
fi
echo 2 >"$TB/scripts/typed-boundaries-baseline.txt"
expect 0 "typed-boundaries: a baselined leak passes after the date" -- sh "$TBS" "$TB" 2026-11-01
rm -f "$TB/internal/api/leak.go"
expect 1 "typed-boundaries: a stale baseline fails from the date" -- sh "$TBS" "$TB" 2026-11-01

echo 0 >"$TB/scripts/typed-boundaries-baseline.txt"
printf 'package service\n\nfunc f(err error) bool { return strings.Contains(err.Error(), "no rows") }\n' >"$TB/internal/service/str.go"
expect 1 "typed-boundaries: matching on err.Error() text fails from the date" -- sh "$TBS" "$TB" 2026-11-01
rm -f "$TB/internal/service/str.go"

printf 'package service\n\nimport "database/sql"\n\nvar _ = sql.ErrNoRows\n' >"$TB/internal/service/f0_test.go"
expect 0 "typed-boundaries: test files are exempt" -- sh "$TBS" "$TB" 2026-11-01
rm -f "$TB/internal/service/f0_test.go"

rm -f "$TB/internal/service/f0.go"
expect 1 "typed-boundaries: below the input floor fails before the date" -- sh "$TBS" "$TB" 2026-10-17
expect 1 "typed-boundaries: below the input floor fails after the date" -- sh "$TBS" "$TB" 2026-11-01
printf 'package service\n' >"$TB/internal/service/f0.go"

rm -f "$TB/scripts/typed-boundaries-baseline.txt"
expect 1 "typed-boundaries: a missing baseline fails" -- sh "$TBS" "$TB" 2026-10-17

# ----------------------------------------------------- check-claude-layout.sh
CL="$WORK/claudelayout"
mkdir -p "$CL/internal/alpha" "$CL/internal/beta/sub"
(
	cd "$CL"
	git init -q .
	printf '# X\n\n## Project Layout\n\n```\ninternal/\n    alpha/   A\n    beta/    B\n```\n\n## Next\n\n    gamma/   outside the block\n' >CLAUDE.md
	printf 'package alpha\n' >internal/alpha/a.go
	printf 'package sub\n' >internal/beta/sub/s.go
	git add -A
) >/dev/null 2>&1
expect 0 "claude-layout: every package has a row" -- sh "$REPO/scripts/check-claude-layout.sh" "$CL"
(
	cd "$CL"
	mkdir -p internal/gamma
	printf 'package gamma\n' >internal/gamma/g.go
	git add -A
) >/dev/null 2>&1
expect 1 "claude-layout: a package with a row only outside the block fails" -- sh "$REPO/scripts/check-claude-layout.sh" "$CL"
(
	cd "$CL"
	sed '/^## Project Layout/,$d' CLAUDE.md >C2 && mv C2 CLAUDE.md
) >/dev/null 2>&1
expect 1 "claude-layout: a missing layout section fails" -- sh "$REPO/scripts/check-claude-layout.sh" "$CL"

# --------------------------------------------------- check-sdk-coverage.sh
SDKCOV="$REPO/scripts/check-sdk-coverage.sh"
SB="$WORK/sdkbaseline"
mkdir -p "$SB"
printf '# file | line %% | branch %% | funcs %% | uncovered lines\n# all files           |  91.50 |    90.09 |   94.03 |\n' >"$WORK/node-report.txt"
printf 'src/a.py   10   1  90.0%%\nTOTAL   369  14  91.5%%\n' >"$WORK/py-report.txt"
printf ' Summary:\n  Classes: 60.00%% (3/5)\n  Lines:   91.50%% (128/152)\n\nBugBarn\\Client\n  Methods:  92.31%% (12/13)   Lines:  75.00%% ( 63/ 84)\n' >"$WORK/php-report.txt"
printf '# all files           |  79.90 |    90.09 |   94.03 |\n' >"$WORK/node-low.txt"
sdkcov() { BASELINE_DIR="$SB" sh "$SDKCOV" "$@"; }

echo 90.5 >"$SB/sdk-coverage-baseline-x.txt"
expect 0 "sdk-coverage: node total inside the band passes" -- sdkcov x node "$WORK/node-report.txt"
expect 0 "sdk-coverage: python total inside the band passes" -- sdkcov x python "$WORK/py-report.txt"
expect 0 "sdk-coverage: php summary line is read, not a per-class line" -- sdkcov x php "$WORK/php-report.txt"
echo 92.0 >"$SB/sdk-coverage-baseline-x.txt"
expect 1 "sdk-coverage: a drop below the baseline fails" -- sdkcov x node "$WORK/node-report.txt"
echo 80.0 >"$SB/sdk-coverage-baseline-x.txt"
expect 1 "sdk-coverage: a beatable baseline fails as stale" -- sdkcov x node "$WORK/node-report.txt"
echo 70.0 >"$SB/sdk-coverage-baseline-x.txt"
expect 1 "sdk-coverage: a total under 80 fails whatever the baseline" -- sdkcov x node "$WORK/node-low.txt"
expect 1 "sdk-coverage: a missing baseline fails" -- sdkcov y node "$WORK/node-report.txt"
expect 1 "sdk-coverage: a missing report fails" -- sdkcov x node "$WORK/absent.txt"
printf 'no totals here\n' >"$WORK/empty-report.txt"
expect 1 "sdk-coverage: a report without a total fails" -- sdkcov x python "$WORK/empty-report.txt"
expect 1 "sdk-coverage: an unknown format fails" -- sdkcov x lcov "$WORK/node-report.txt"
for sdk in typescript python php; do
	expect 0 "sdk-coverage: committed $sdk baseline is at least the 80 minimum" -- \
		awk 'BEGIN { exit !('"$(tr -d ' \t\n' <"$REPO/scripts/sdk-coverage-baseline-$sdk.txt")"' >= 80) }'
done

echo
echo "gates_test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
