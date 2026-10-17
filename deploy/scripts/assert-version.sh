#!/bin/sh
# Deploy smoke: a running pod reports the version and commit that were deployed.
#
#   assert-version.sh NAMESPACE TARGET CONTAINER WANT_VERSION WANT_COMMIT
#
# TARGET is anything `kubectl exec` accepts, e.g. deploy/bugbarn-writer. Reads
# GET /api/v1/version from inside the pod (no ingress or DNS in the way).
# `version` is what the deploy stated (BUGBARN_VERSION, set in the pod spec);
# `commit` is baked into the binary at build time. Checking both is what proves
# the stated version belongs to the code that is running: a deploy that sets the
# tag of one release on the image of another fails here.
set -eu

if [ "$#" -ne 5 ]; then
	echo "usage: $0 NAMESPACE TARGET CONTAINER WANT_VERSION WANT_COMMIT" >&2
	exit 2
fi
ns="$1"
target="$2"
ctr="$3"
want_version="$4"
want_commit="$5"

body="$(kubectl -n "$ns" exec "$target" -c "$ctr" -- wget -qO- http://127.0.0.1:8080/api/v1/version)"

case "$body" in
*"\"version\":\"$want_version\""*) ;;
*)
	echo "FAIL $ns/$target: reported $body, wanted version $want_version" >&2
	exit 1
	;;
esac
case "$body" in
*"\"commit\":\"$want_commit\""*) ;;
*)
	echo "FAIL $ns/$target: reported $body, wanted commit $want_commit" >&2
	exit 1
	;;
esac
echo "ok   $ns/$target reports $want_version at $want_commit"
