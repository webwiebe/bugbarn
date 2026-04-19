#!/bin/sh
set -eu

if [ -z "${LITESTREAM_ACCESS_KEY_ID:-}" ]; then
  exec bugbarn
fi

if [ ! -f "$BUGBARN_DB_PATH" ]; then
  echo "Restoring database from Litestream replica (${LITESTREAM_REPLICA_PATH})..."
  litestream restore \
    -config /etc/litestream.yml \
    -if-replica-exists \
    "$BUGBARN_DB_PATH" || echo "No replica found, starting fresh."
fi

exec litestream replicate \
  -config /etc/litestream.yml \
  -exec "bugbarn"
