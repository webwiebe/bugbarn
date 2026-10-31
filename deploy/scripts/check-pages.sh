#!/bin/sh
# Post-deploy page check through the public host, the path a visitor takes.
#
#   check-pages.sh BASE_URL
#
# Passes when the root page answers 200, /app/ answers 200 with the sign-in form
# in its markup, and the bundle /app/ names answers 200 as non-empty JavaScript.
# The content type matters: Caddy serves index.html with a 200 for any unknown
# path under /app/, so a missing bundle still answers 200.
#
# No sign-in, no setup page, no writes. It runs no JavaScript, so a bundle that
# loads and then throws passes here.
#
# A failure is retried: on production this check fires the rollback, and one
# dropped request through Cloudflare must not. PAGE_CHECK_ATTEMPTS and
# PAGE_CHECK_DELAY (seconds) set the retries.
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: $0 BASE_URL" >&2
	exit 2
fi
base="${1%/}"
attempts="${PAGE_CHECK_ATTEMPTS:-5}"
delay="${PAGE_CHECK_DELAY:-10}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Sets code, size and ctype and leaves the body in $work/body. A request that
# gets no answer reads as code 000. Redirects are not followed: none of these
# pages redirects a visitor without a session.
fetch() {
	reply="$(curl -s -o "$work/body" --max-time 15 -A bugbarn-page-check -w '%{http_code} %{size_download} %{content_type}' "$1")" || reply="000 0 "
	code="${reply%% *}"
	rest="${reply#* }"
	size="${rest%% *}"
	ctype="${rest#* }"
}

check_once() {
	fetch "$base/"
	[ "$code" = 200 ] || { why="/ answered $code"; return 1; }

	fetch "$base/app/"
	[ "$code" = 200 ] || { why="/app/ answered $code"; return 1; }
	grep -q 'id="login-form"' "$work/body" || { why="/app/ has no sign-in form"; return 1; }
	bundle="$(grep -o 'src="[^"]*dist/app-[^"]*\.js"' "$work/body" | head -n 1 | sed 's/^src="//; s/"$//')"
	case "$bundle" in
	"") why="/app/ names no bundle"; return 1 ;;
	/*) url="$base$bundle" ;;
	*) url="$base/app/${bundle#./}" ;;
	esac

	fetch "$url"
	[ "$code" = 200 ] || { why="$bundle answered $code"; return 1; }
	case "$ctype" in
	*javascript*) ;;
	*) why="$bundle answered as $ctype"; return 1 ;;
	esac
	[ "$size" -gt 0 ] || { why="$bundle is empty"; return 1; }
}

why=""
try=1
while ! check_once; do
	if [ "$try" -ge "$attempts" ]; then
		echo "FAIL $base: $why (attempt $try of $attempts)" >&2
		exit 1
	fi
	echo "retry $base: $why (attempt $try of $attempts)"
	try=$((try + 1))
	sleep "$delay"
done
echo "ok   $base: root page, sign-in form and bundle $bundle"
