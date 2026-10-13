#!/bin/sh
set -e

# Publish the SDK tarball baked into this image to the persistent packages
# volume at /srv/packages. Old tarballs from previous deploys are preserved
# on the volume so lockfiles pinned to earlier content-hash URLs remain valid.
# A tarball that already exists is never overwritten (cp -n): consumers pin its
# sha512 in their lockfiles, so the bytes behind a URL must not change.
mkdir -p /srv/packages/typescript
cp -n /tmp/sdk-package/*.tgz /srv/packages/typescript/ 2>/dev/null || true

# Write latest.json so consumers can discover the current package URL without
# knowing the content-hash suffix in advance. The newest file by modification
# time wins: the hash suffix has no order, so sorting by name picks an arbitrary
# old tarball. cp -n gives each new tarball the mtime of its first deploy.
tarball=$(ls -t /srv/packages/typescript/bugbarn-typescript-*.tgz 2>/dev/null | head -1)
if [ -n "$tarball" ]; then
  filename=$(basename "$tarball")
  version="${filename#bugbarn-typescript-}"
  version="${version%.tgz}"
  printf '{"version":"%s","filename":"%s","url":"/packages/typescript/%s"}\n' \
    "$version" "$filename" "$filename" \
    > /srv/packages/typescript/latest.json
fi

exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
