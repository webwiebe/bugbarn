#!/bin/sh
set -e

# Publish the SDK tarball baked into this image to the persistent packages
# volume at /srv/packages. Old tarballs from previous deploys are preserved
# on the volume so lockfiles pinned to earlier content-hash URLs remain valid.
mkdir -p /srv/packages/typescript
cp /tmp/sdk-package/*.tgz /srv/packages/typescript/ 2>/dev/null || true

# Write latest.json so consumers can discover the current package URL without
# knowing the content-hash suffix in advance.
tarball=$(ls /srv/packages/typescript/bugbarn-typescript-*.tgz 2>/dev/null | sort | tail -1)
if [ -n "$tarball" ]; then
  filename=$(basename "$tarball")
  version="${filename#bugbarn-typescript-}"
  version="${version%.tgz}"
  printf '{"version":"%s","filename":"%s","url":"/packages/typescript/%s"}\n' \
    "$version" "$filename" "$filename" \
    > /srv/packages/typescript/latest.json
fi

exec caddy file-server --root /srv --listen :8080
