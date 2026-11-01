#!/usr/bin/env bash
# Runs the role's macOS tasks against stubs: a made-up key, a stub install.sh,
# sops and launchctl, and HOME in a temp dir, so it touches no agent and decrypts
# nothing. Checks what a failed install prints, that the key prints in no form, and
# that the installer's output reaches no file.
set -euo pipefail

if [ "$(uname -s)" != Darwin ]; then
	echo "skip: the macOS tasks run only on Darwin"
	exit 0
fi

roles=$(cd "$(dirname "$0")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/repo/deploy/vector/host/macos" "$tmp/bin" "$tmp/home" "$tmp/out" "$tmp/tmpdir"
echo 'placeholder: true' > "$tmp/repo/deploy/vector/host/secret.yaml"
echo 'stub: true' > "$tmp/repo/deploy/vector/host/macos/vector.yaml"

key='MadeUp/Key+246abcDEFghiJKLmnoPQRstu'
b64=$(printf '%s' "$key" | base64)
hex=$(printf '%s' "$key" | xxd -p | tr -d '\n')
upper=$(printf '%s' "$key" | tr '[:lower:]' '[:upper:]')
reversed=$(printf '%s' "$key" | rev)
url=$(printf '%s' "$key" | sed -e 's|/|%2F|' -e 's|+|%2B|')
head=${key:0:17}
tail=${key:17}
wrapped=$(printf '%s' "$key" | fold -w 8)
forms=("$key" "$b64" "$hex" "$upper" "$reversed" "$url" "$head" "$tail")
while IFS= read -r part; do forms+=("$part"); done <<< "$wrapped"

# Every form, on lines the task recognises and on lines it does not.
cat > "$tmp/out/recognised.txt" <<EOF
Failed to load $key
Bootstrap failed: 5: $b64
curl: (22) $hex
tar: $upper
unsupported architecture $reversed
install: $url
no API key $head
$tail: Permission denied
sh: $key: command not found
$wrapped
EOF
{
	printf '%s\n' "${forms[@]}"
	echo "some step nobody listed"
} > "$tmp/out/unknown.txt"
marker='installer-output-marker-246'
echo "$marker" > "$tmp/out/marker.txt"

cat > "$tmp/repo/deploy/vector/host/macos/install.sh" <<EOF
#!/bin/sh
wc -c < /dev/stdin | tr -d ' ' > "$tmp/out/stdin_bytes"
echo ran >> "$tmp/out/installer_runs"
scenario=\$(cat "$tmp/out/scenario")
cat "$tmp/out/marker.txt"
[ "\$scenario" = pass ] && exit 0
cat "$tmp/out/\$scenario.txt" >&2
exit 7
EOF
chmod +x "$tmp/repo/deploy/vector/host/macos/install.sh"

cat > "$tmp/bin/sops" <<EOF
#!/bin/sh
echo hit >> "$tmp/out/sops_hits"
cat "$tmp/out/sops_value"
EOF
cat > "$tmp/bin/launchctl" <<EOF
#!/bin/sh
echo hit >> "$tmp/out/launchctl_hits"
exit 1
EOF
chmod +x "$tmp/bin/sops" "$tmp/bin/launchctl"

cat > "$tmp/play.yml" <<'EOF'
- hosts: localhost
  connection: local
  roles:
    - bugbarn-vector-host
EOF

failures=0
fail() { echo "FAIL $*"; failures=$((failures + 1)); }

# Every file a run can leave: the role's own three, nothing else, and none of them
# (the key file aside) holds a form of the key or the installer's output.
no_file_written() {
	local want got form
	want=$(printf '%s\n' .vector/install/install.sh .vector/install/vector.yaml .vector/secrets/api_key | sort)
	got=$(cd "$tmp/home" && find . -type f ! -path './.ansible/*' | sed 's|^\./||' | sort)
	[ "$got" = "$want" ] || fail "$1: unexpected files in HOME: $(printf '%s' "$got" | tr '\n' ' ')"
	for form in "${forms[@]}" "$marker"; do
		# The last wrapped chunk is 3 characters, which ordinary files can hold.
		[ "${#form}" -ge 8 ] || continue
		if grep -rqF --exclude=api_key -- "$form" "$tmp/home" "$tmp/ansible" "$tmp/tmpdir"; then
			fail "$1: a file holds a form of the key or the output (${#form} characters)"
		fi
	done
}

# run NAME SOPS_VALUE WANT_RC
run() {
	local rc=0
	echo "$1" > "$tmp/out/scenario"
	printf '%s\n' "$2" > "$tmp/out/sops_value"
	: > "$tmp/out/installer_runs"
	rm -f "$tmp/home/.vector/install/install.sh"
	HOME="$tmp/home" TMPDIR="$tmp/tmpdir" ANSIBLE_HOME="$tmp/ansible" ANSIBLE_ROLES_PATH="$roles" ANSIBLE_NOCOLOR=1 \
		ANSIBLE_LOCALHOST_WARNING=False PATH="$tmp/bin:$PATH" \
		timeout 120 ansible-playbook -vvv -i 'localhost,' \
		-e "bugbarn_vector_repo=$tmp/repo" -e "bugbarn_vector_age_key_file=$tmp/none" \
		"$tmp/play.yml" > "$tmp/out/$1.log" 2>&1 || rc=$?
	[ "$rc" = "$3" ] || fail "$1: ansible exit $rc, want $3"
	for form in "${forms[@]}"; do
		if grep -qF -- "$form" "$tmp/out/$1.log"; then
			fail "$1: a form of the key printed (${#form} characters)"
		fi
	done
}

expect() {
	grep -qF -- "$2" "$tmp/out/$1.log" || fail "$1: output lacks: $2"
}

run recognised "$key" 2
expect recognised "install.sh exited 7."
expect recognised "vector validate rejected the config"
expect recognised "launchctl bootstrap failed with 5"
expect recognised "curl failed to download Vector"
expect recognised "permission denied on a file"
if grep -qF "run this in a terminal" "$tmp/out/recognised.log"; then
	fail "recognised: printed the by-hand command with a step recognised"
fi
no_file_written recognised
[ "$(cat "$tmp/out/stdin_bytes")" = 0 ] || fail "installer got something on stdin"
[ "$(stat -f '%Lp' "$tmp/home/.vector/secrets/api_key")" = 600 ] || fail "key file is not 0600"
[ "$(cat "$tmp/home/.vector/secrets/api_key")" = "$key" ] || fail "key file does not hold the key"
[ "$(wc -c < "$tmp/home/.vector/secrets/api_key" | tr -d ' ')" = "${#key}" ] || fail "key file has a trailing newline"

run unknown "$key" 2
expect unknown "No known step line matched."
expect unknown "run this in a terminal on the host: \"$tmp/home/.vector/install/install.sh\" < /dev/null"
no_file_written unknown

run pass "$key" 0
no_file_written pass

rm -f "$tmp/home/.vector/secrets/api_key"
run pass "" 2
expect pass "decrypted to nothing"
[ ! -s "$tmp/out/installer_runs" ] || fail "installer ran with an empty key"
[ ! -e "$tmp/home/.vector/secrets/api_key" ] || fail "a key file was written with an empty key"

[ -s "$tmp/out/sops_hits" ] || fail "the sops stub never ran"
[ -s "$tmp/out/launchctl_hits" ] || fail "the launchctl stub never ran"

if [ "$failures" != 0 ]; then
	echo "$failures failure(s)"
	exit 1
fi
echo "ok: ${#forms[@]} forms of the key across 4 runs, none printed, no output in a file"
