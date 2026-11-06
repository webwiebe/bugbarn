#!/usr/bin/env bash
# Pin the kustomization's images to release digests before `kubectl apply -k`.
#
#   pin-images.sh KUSTOMIZATION SERVICE_DIGEST WEB_DIGEST
#
# The kustomization maps bugbarn/service and bugbarn/web to the placeholder tag
# `local`. Applied as is, every workload briefly carries `:local` until a later
# `kubectl set image`, and the settings snapshot CronJob creates its hourly Job
# from whatever image it holds at that moment. Deploy 876 (2026-11-06) straddled
# 05:00Z, the 05:00 Job got `:local`, and it blocked the next runs (#262).
#
# This replaces each `newTag: local` with `digest: <digest>`, so the apply sets
# the release digests on the writer, reader, web and the CronJob in one step.
# It fails unless it replaced exactly the two placeholder lines.
set -euo pipefail

file=$1 svc=$2 web=$3
for d in "$svc" "$web"; do
  [[ "$d" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "pin-images: not a digest: '$d'" >&2; exit 1; }
done

tmp="$file.pin.$$"
trap 'rm -f "$tmp"' EXIT
awk -v svc="$svc" -v web="$web" '
  /^  - name: bugbarn\/service$/ { img = svc }
  /^  - name: bugbarn\/web$/     { img = web }
  /^    newTag: local$/ && img != "" { print "    digest: " img; img = ""; n++; next }
  { print }
  END { if (n != 2) { printf "pin-images: replaced %d placeholder tags, want 2\n", n > "/dev/stderr"; exit 1 } }
' "$file" > "$tmp"
mv "$tmp" "$file"
