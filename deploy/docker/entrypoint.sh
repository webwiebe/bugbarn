#!/bin/sh
set -eu

case "${BUGBARN_MODE:-}" in
  reader)
    # Reader mode: restore a snapshot from Litestream, then run read-only.
    # The binary's internal restore loop handles periodic refreshes.
    # The litestream config references /var/lib/bugbarn/bugbarn.db (writer path),
    # so we use -o to restore to the reader's DB path instead.
    if [ -n "${LITESTREAM_ACCESS_KEY_ID:-}" ]; then
      echo "Reader: restoring database from Litestream replica..."
      litestream restore \
        -config /etc/litestream.yml \
        -if-replica-exists \
        -o "$BUGBARN_DB_PATH" \
        /var/lib/bugbarn/bugbarn.db || echo "No replica found, starting with empty DB."
    fi
    exec bugbarn
    ;;
  writer|"")
    # Writer or legacy mode: replicate to S3 via Litestream.
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
    ;;
esac
