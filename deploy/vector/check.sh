#!/bin/sh
# Runs the Vector unit tests and validates base + sinks for every overlay, with
# the image the DaemonSet pins. Used by .woodpecker/vector.yaml; runs locally too.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
image=$(sed -n 's/^ *image: \(timberio\/vector:.*\)$/\1/p' "$here/k8s/base/daemonset.yaml")
test -n "$image" || { echo "no vector image in daemonset.yaml" >&2; exit 1; }

# The directory secret backend needs a file to read; validation never sends.
work="$here/.check"
rm -rf "$work"
mkdir -p "$work/secrets"
printf 'placeholder' > "$work/secrets/api_key"

run() {
	docker run --rm -e NODE_NAME=check \
		-v "$work/secrets:/etc/vector-secrets:ro" -v "$here:/v:ro" "$image" "$@"
}

run test /v/k8s/base/vector.yaml /v/tests/transforms.yaml
for sinks in "$here"/k8s/*/sinks.yaml; do
	overlay=$(basename "$(dirname "$sinks")")
	echo "== $overlay"
	run validate --no-environment /v/k8s/base/vector.yaml "/v/k8s/$overlay/sinks.yaml"
done
rm -rf "$work"
