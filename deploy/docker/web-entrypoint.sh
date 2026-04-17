#!/bin/sh
set -e

# Publish the SDK tarball baked into this image to the persistent packages
# volume at /srv/packages. Old tarballs from previous deploys are preserved
# on the volume so lockfiles pinned to earlier content-hash URLs remain valid.
mkdir -p /srv/packages/typescript
cp /tmp/sdk-package/*.tgz /srv/packages/typescript/ 2>/dev/null || true

exec caddy file-server --root /srv --listen :8080
