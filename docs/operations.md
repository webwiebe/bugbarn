# Operations Guide

## Spool

BugBarn uses an append-only NDJSON spool to decouple ingest from storage. Accepted events are written to disk before the HTTP response is sent; a background worker reads and processes them into SQLite.

**Location**: `BUGBARN_SPOOL_DIR` (default `.data/spool/`)

**Active file**: `ingest.ndjson` — appended to by the ingest handler.

**Rotation**: The worker rotates the active file to `ingest-YYYYMMDDTHHMMSSZ.ndjson` once it exceeds 64 MiB. Archived segments can be deleted once the cursor has advanced past them (i.e. the worker has processed them). Set `BUGBARN_MAX_SPOOL_BYTES` to cap total spool growth and trigger 503 backpressure instead.

**Cursor**: `cursor.json` tracks the byte offset of the last successfully processed record. Delete it to force a full replay from the beginning of `ingest.ndjson` on next startup. The cursor is reset to 0 after each rotation.

**Dead-letter**: `deadletter.ndjson` receives records that fail processing 3 times. Inspect and replay manually if needed.

**Sizing**: Budget ~1 KiB per event. 100 MiB (`BUGBARN_MAX_SPOOL_BYTES=104857600`) handles ~100 k events before backpressure kicks in.

## Backpressure

When the spool file reaches `BUGBARN_MAX_SPOOL_BYTES`, the ingest endpoint returns `503 Service Unavailable` with a `Retry-After: 5` header. SDKs and clients should respect this and back off. The default is unlimited (no cap), which is safe for low-traffic personal use but should be set explicitly in production.

## Retention

Events and issues are kept indefinitely — there is no automatic TTL. Disk usage grows with the SQLite database at `BUGBARN_DB_PATH`.

To reclaim space from the spool, delete processed archived segments (any file in `BUGBARN_SPOOL_DIR` matching `ingest-*.ndjson` that predates the current `cursor.json` offset — safe to remove once the cursor is past them).

## Backup and Recovery

**Backup** (safe while running):
```sh
sqlite3 "$BUGBARN_DB_PATH" ".backup /path/to/backup.db"
cp -r "$BUGBARN_SPOOL_DIR" /path/to/spool-backup/
```

**Full restore**:
1. Stop BugBarn.
2. Replace `BUGBARN_DB_PATH` with the backup DB.
3. Replace `BUGBARN_SPOOL_DIR` contents with the spool backup.
4. Start BugBarn. The worker resumes from `cursor.json`.

**Spool-only recovery** (events not yet in DB):
- If the DB is lost but the spool is intact, delete or reset the DB, delete `cursor.json`, and restart. All spooled events will replay.

**DB-only recovery** (spool lost):
- Restore the DB backup. Events processed before the backup point are present; events accepted after the last backup but before the spool was lost are gone.

## Admin Setup

**Bootstrap admin user** — two options:

Option A: environment variables (recommended for containers)
```sh
BUGBARN_ADMIN_USERNAME=admin
BUGBARN_ADMIN_PASSWORD_BCRYPT=$(htpasswd -bnBC 10 "" mypassword | tr -d ':' | sed 's/^bcrypt://')
```

Option B: CLI
```sh
bugbarn user create --username=admin --password=mypassword
```

**Create a project**:
```sh
bugbarn project create --name="My App"
# → prints {"id":1,"name":"My App","slug":"my-app"}
```

**Create an API key** (key is printed once and never stored):
```sh
bugbarn apikey create --project=my-app --name=production
# → API key: bb_live_<hex>
```

Use the key as the `X-BugBarn-Api-Key` header in SDK configuration.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `BUGBARN_ADDR` | `:8080` | Listen address |
| `BUGBARN_DB_PATH` | `.data/bugbarn.db` | SQLite database path |
| `BUGBARN_SPOOL_DIR` | `.data/spool` | Spool directory |
| `BUGBARN_MAX_SPOOL_BYTES` | unlimited | Spool size cap before 503 backpressure |
| `BUGBARN_MAX_BODY_BYTES` | `1048576` | Max ingest request size (1 MiB) |
| `BUGBARN_API_KEY` | — | Plaintext ingest API key (env-var shortcut) |
| `BUGBARN_API_KEY_SHA256` | — | SHA-256 hex of API key (preferred over plaintext) |
| `BUGBARN_ADMIN_USERNAME` | — | Bootstrap admin username |
| `BUGBARN_ADMIN_PASSWORD` | — | Bootstrap admin plaintext password |
| `BUGBARN_ADMIN_PASSWORD_BCRYPT` | — | Bootstrap admin bcrypt hash (preferred) |
| `BUGBARN_SESSION_SECRET` | — | Secret for session token signing (random 32+ chars) |
| `BUGBARN_SESSION_TTL_SECONDS` | `43200` | Session lifetime (default 12h) |
