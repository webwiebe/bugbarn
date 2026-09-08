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
printf '[{"filePath":"/web/src/a.ts","messages":[{"ruleId":"complexity","line":1},{"ruleId":"no-undef","line":2},{"ruleId":"max-lines","line":3}]}]\n' >"$WORK/es.json"

expect 1 "count: a missing report fails (did not run != clean)" -- python3 "$COUNT" golangci "$WORK/absent.json"
expect 1 "count: a malformed report fails" -- python3 "$COUNT" golangci "$WORK/gl-broken.json"
expect 1 "count: a report with no Issues key fails" -- python3 "$COUNT" golangci "$WORK/gl-wrong.json"
expect 0 "count: a clean report is accepted" -- python3 "$COUNT" golangci "$WORK/gl-clean.json"
expect 1 "count: a missing eslint report fails" -- python3 "$COUNT" eslint "$WORK/absent.json" complexity

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
check_value "count: eslint counts only the named rules" 2 python3 "$COUNT" eslint "$WORK/es.json" complexity max-lines

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
# A throwaway module with exactly two statements, one of them covered, so the
# measured total is a stable 50.0% and the three band cases are exact.
CV="$WORK/coverage"
mkdir -p "$CV/scripts"
cp "$REPO/scripts/check-coverage.sh" "$CV/scripts/"
cat >"$CV/go.mod" <<'EOF'
module gatetest

go 1.21
EOF
cat >"$CV/lib.go" <<'EOF'
package gatetest

func A() int { return 1 }

func B() int { return 2 }
EOF
cat >"$CV/lib_test.go" <<'EOF'
package gatetest

import "testing"

func TestA(t *testing.T) {
	if A() != 1 {
		t.Fatal("A")
	}
}
EOF

measured=$(cd "$CV" && go test -covermode=atomic -coverprofile=c.out ./... >/dev/null 2>&1 && go tool cover -func=c.out | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')
if [ "$measured" != "50.0" ]; then
	fail=$((fail + 1))
	echo "FAIL coverage: fixture measured ${measured}%, expected 50.0% — the rest of this section is meaningless"
else
	pass=$((pass + 1))
	echo "ok   coverage: fixture measures a stable 50.0%"

	echo 49.0 >"$CV/scripts/coverage-baseline.txt"
	expect 0 "coverage: inside the band passes" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	echo 55.0 >"$CV/scripts/coverage-baseline.txt"
	expect 1 "coverage: below the floor fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	echo 40.0 >"$CV/scripts/coverage-baseline.txt"
	expect 1 "coverage: a beatable baseline fails" -- sh -c "cd '$CV' && sh scripts/check-coverage.sh"

	echo 40.0 >"$CV/scripts/coverage-baseline.txt"
	if (cd "$CV" && sh scripts/check-coverage.sh 2>&1) | grep -q 'echo 49.0 > scripts/coverage-baseline.txt'; then
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
fi

echo
echo "gates_test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
