# BugBarn Configuration Reference

## Config File Locations

BugBarn reads configuration from files before applying environment variables. Files are processed in order; values already present in the process environment always take precedence over file values.

| Location | Purpose |
|---|---|
| `/etc/bugbarn/bugbarn.conf` | System-wide (e.g., set by `systemd EnvironmentFile`) |
| `~/.config/bugbarn/bugbarn.conf` | Per-user (XDG config dir, Linux and macOS) |

Environment variables have the highest precedence and override both config files.

### File Format

Plain `KEY=VALUE` pairs, one per line. Blank lines and lines starting with `#` are ignored. Values may be wrapped in single or double quotes.

```
# BugBarn configuration
BUGBARN_ADMIN_USERNAME=admin
BUGBARN_ADMIN_PASSWORD_BCRYPT=$2y$10$...
BUGBARN_SESSION_SECRET=somelong32bytesecret
BUGBARN_PUBLIC_URL=https://bugbarn.example.com
BUGBARN_ALLOWED_ORIGINS=https://app.example.com,https://staging.example.com
```

---

## Environment Variable Reference

### Core

| Variable | Default | Required | Description |
|---|---|---|---|
| `BUGBARN_ADDR` | `:8080` | No | TCP address the HTTP server listens on. |
| `BUGBARN_DB_PATH` | `.data/bugbarn.db` | No | Path to the SQLite database file. |
| `BUGBARN_SPOOL_DIR` | `.data/spool` | No | Directory used for the event spool (write-ahead buffer). |
| `BUGBARN_MAX_BODY_BYTES` | `1048576` (1 MiB) | No | Maximum size of an ingest request body in bytes. Requests larger than this value are rejected with `413`. |
| `BUGBARN_MAX_SPOOL_BYTES` | `0` (unlimited) | No | Maximum total size of the spool directory in bytes. When set and exceeded, ingest requests return `429 Too Many Requests` with a `Retry-After` header. Set this to protect disk space on constrained nodes. |
| `BUGBARN_PUBLIC_URL` | — | No | Base URL of the BugBarn instance (e.g., `https://bugbarn.example.com`). Used to build links in alert notifications and digest emails. |

#### BUGBARN_MAX_SPOOL_BYTES

Set this variable when you want a hard ceiling on disk usage by the event spool. Useful in production where disk is shared with other workloads or where you prefer to drop events over crashing. When the limit is exceeded, the ingest endpoint returns `429` with `Retry-After: 60`. SDKs and clients that respect `Retry-After` will back off and retry automatically.

---

### Authentication

| Variable | Default | Required | Description |
|---|---|---|---|
| `BUGBARN_API_KEY` | — | No | Static ingest API key in plaintext. BugBarn hashes it with SHA-256 at startup. Mutually exclusive with `BUGBARN_API_KEY_SHA256`. |
| `BUGBARN_API_KEY_SHA256` | — | No | Static ingest API key pre-hashed as a hex-encoded SHA-256 digest. Use this instead of `BUGBARN_API_KEY` to avoid storing the plaintext value. |
| `BUGBARN_ADMIN_USERNAME` | — | No | Username for the web UI admin account. |
| `BUGBARN_ADMIN_PASSWORD` | — | No | Admin password in plaintext. BugBarn bcrypts it at startup. Mutually exclusive with `BUGBARN_ADMIN_PASSWORD_BCRYPT`. |
| `BUGBARN_ADMIN_PASSWORD_BCRYPT` | — | No | Admin password pre-hashed as a bcrypt string. Use this to avoid storing the plaintext password. |
| `BUGBARN_SESSION_SECRET` | — | No | HMAC key used to sign session tokens. Must be a long, random string (at minimum 32 bytes of entropy). **If unset, a random key is generated at startup — sessions will not survive process restarts.** In production, always set this to a stable value so users are not logged out on redeploy. |
| `BUGBARN_SESSION_TTL_SECONDS` | `43200` (12 h) | No | Session lifetime in seconds. After expiry the user must log in again. |

#### BUGBARN_SESSION_SECRET

Without a stable `BUGBARN_SESSION_SECRET`, every restart of the BugBarn process invalidates all existing sessions. In a Kubernetes environment this means every pod restart logs every user out. Generate a value with:

```sh
openssl rand -hex 32
```

Store the result in your secrets manager and inject it as an environment variable.

---

### CORS

| Variable | Default | Required | Description |
|---|---|---|---|
| `BUGBARN_ALLOWED_ORIGINS` | — | No | Comma-separated list of origins permitted to make credentialed (session-cookie) requests. Example: `https://app.example.com,https://staging.example.com`. If unset, only same-origin requests are allowed with credentials. The ingest endpoints (`POST /api/v1/events`, `POST /api/v1/logs`) always allow all origins regardless of this setting. |

---

### Digest

| Variable | Default | Required | Description |
|---|---|---|---|
| `BUGBARN_DIGEST_ENABLED` | `false` | No | Set to `true` to enable weekly email digests. SMTP must also be configured. |
| `BUGBARN_DIGEST_TO` | — | Yes (if digest enabled) | Recipient email address for digest emails. |
| `BUGBARN_DIGEST_DAY` | `0` (Sunday) | No | Day of the week to send the digest (0 = Sunday, 1 = Monday, …, 6 = Saturday). |
| `BUGBARN_DIGEST_HOUR` | `8` | No | Hour of the day (UTC, 0–23) to send the digest. |
| `BUGBARN_DIGEST_WEBHOOK_URL` | — | No | Optional webhook URL. When set, the digest payload is also `POST`ed to this URL as JSON. |

---

### SMTP

These variables do not use the `BUGBARN_` prefix, matching conventions used by other services (e.g., `rapid-root`).

| Variable | Default | Required | Description |
|---|---|---|---|
| `SMTP_HOST` | — | Yes (if digest enabled) | SMTP server hostname. |
| `SMTP_PORT` | `587` | No | SMTP server port. |
| `SMTP_USER` | — | Yes (if digest enabled) | SMTP authentication username. |
| `SMTP_PASS` | — | Yes (if digest enabled) | SMTP authentication password. |
| `SMTP_FROM` | value of `SMTP_USER` | No | From address used in outgoing digest emails. Defaults to `SMTP_USER` if not set. |

---

### Self-Reporting

BugBarn can report its own unhandled errors to a BugBarn instance (including itself).

| Variable | Default | Required | Description |
|---|---|---|---|
| `BUGBARN_SELF_ENDPOINT` | — | No | URL of the BugBarn instance to report own errors to (e.g., `https://bugbarn.example.com`). |
| `BUGBARN_SELF_API_KEY` | — | No | API key for the self-reporting instance. Both `BUGBARN_SELF_ENDPOINT` and `BUGBARN_SELF_API_KEY` must be set to enable self-reporting. |

---

### Litestream

These variables are consumed by the Litestream process that runs alongside BugBarn (as a sidecar or wrapper). BugBarn itself does not read them.

| Variable | Default | Required | Description |
|---|---|---|---|
| `LITESTREAM_REPLICA_PATH` | — | Yes (if Litestream enabled) | S3-compatible path for the database replica (e.g., `s3://bucket/production/bugbarn.db`). |
| `LITESTREAM_ACCESS_KEY_ID` | — | Yes (if Litestream enabled) | Access key ID for the object-storage backend. |
| `LITESTREAM_SECRET_ACCESS_KEY` | — | Yes (if Litestream enabled) | Secret access key for the object-storage backend. |

---

## CLI Commands

```
bugbarn [flags]                                          # Start the server
bugbarn version                                          # Print version and build time
bugbarn worker-once                                      # Process the spool once and exit (useful for debugging)
bugbarn user create --username=X --password=Y            # Create or update an admin user in the database
bugbarn apikey create --project=SLUG --name=NAME \
        --scope=ingest|full                              # Create a new API key
bugbarn project create --slug=SLUG --name=NAME           # Create a new project
```

`bugbarn worker-once` reads all pending spool records, processes them into the database, and exits. It is useful for replaying events after a spool backup restore or for debugging ingestion issues without running the full server.

---

## Resource Sizing Guidance

The default resource requests (100m CPU / 128 Mi RAM) are appropriate for:

- Low-to-medium traffic (up to a few hundred events per minute).
- A single project with up to tens of thousands of issues in the database.
- Running Litestream in the same pod without heavy replication load.

**Consider increasing memory limits when:**

- The issue list query is slow and you see SQLite page-cache pressure.
- You are ingesting many events per second with large payloads close to the 1 MiB limit.
- You are running source-map upload and lookup for many releases simultaneously.

**Consider increasing CPU limits when:**

- You see throttling (`cpu_cfs_throttled_seconds_total` rising) during peak ingest bursts.
- bcrypt login operations are noticeably slow (bcrypt is CPU-intensive by design).

A typical small production deployment fits comfortably within the defaults. Start there and adjust based on observed usage.
