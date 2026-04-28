# BugBarn — Authentication

## Two Auth Modes

BugBarn supports two mutually exclusive authentication paths on any given request.

| Mode | Credential | Use case |
|---|---|---|
| **Session** | `bugbarn_session` cookie | Interactive browser sessions; the Web UI uses this after login |
| **API key** | `X-BugBarn-Api-Key` header | SDKs, CI scripts, automated clients, browser-side error reporting |

The two modes are evaluated in order: session first, then API key. For the ingest endpoint and the log ingest endpoint, only the API key path is checked (sessions are not accepted there).

---

## Session Authentication

### Login and Logout

| Endpoint | Method | Description |
|---|---|---|
| `POST /api/v1/login` | `POST` | Submit `{"username":"…","password":"…"}`. On success sets two cookies and returns `200`. On failure returns `401`. |
| `POST /api/v1/logout` | `POST` | Clears both session cookies. No auth required. |
| `GET /api/v1/me` | `GET` | Returns `{"username":"…","authenticated":true}` for a valid session, or `{"authenticated":false}`. |

### Cookie Format

A successful login sets two cookies:

#### `bugbarn_session`

The session token is a two-part string: `<payload>.<signature>`, where both parts are base64url-encoded (no padding).

- **Payload**: base64url-encoded JSON:
  ```json
  {"u": "admin", "e": 1745826000, "n": "<random-nonce>"}
  ```
  - `u` — username
  - `e` — Unix timestamp of expiry
  - `n` — 32-byte random nonce (prevents signature re-use)

- **Signature**: base64url-encoded HMAC-SHA256 over the raw payload bytes, keyed with `BUGBARN_SESSION_SECRET`

Cookie attributes:
- `HttpOnly: true` — not accessible from JavaScript
- `SameSite: Strict`
- `Secure` — set when the request arrives over HTTPS
- `Path: /`
- `Expires` — set to the session expiry time

#### `bugbarn_csrf`

A companion cookie whose value is a CSRF token derived from the session token (see [CSRF Protection](#csrf-protection) below).

Cookie attributes:
- `HttpOnly: false` — **must** be readable by JavaScript so the UI can attach it as a request header
- `SameSite: Lax`
- `Secure` — set when the request arrives over HTTPS
- `Path: /`

### Session TTL

The default session lifetime is **12 hours**. This can be changed by setting `BUGBARN_SESSION_TTL_SECONDS` to a positive integer number of seconds.

Sessions are **stateless**: there is no server-side session store. Validity is determined entirely by the HMAC signature and the expiry timestamp embedded in the cookie. If `BUGBARN_SESSION_SECRET` is not set, a random secret is generated at startup and sessions will not survive a process restart.

### Password Storage

Passwords are stored as bcrypt hashes in the `users` table (`password_bcrypt` column). BugBarn supports two ways to configure the admin password at startup:

- `BUGBARN_ADMIN_PASSWORD_BCRYPT` — a pre-computed bcrypt hash (preferred for production)
- `BUGBARN_ADMIN_PASSWORD` — a plaintext password; BugBarn hashes it with `bcrypt.DefaultCost` on startup

Verification uses `bcrypt.CompareHashAndPassword` which is timing-safe by design. Username comparison uses `crypto/subtle.ConstantTimeCompare`.

---

## API Key Authentication

### Header

All API key requests must include:

```
X-BugBarn-Api-Key: <plaintext-key>
```

### Scopes

| Scope | Permitted endpoints |
|---|---|
| `full` | All authenticated endpoints |
| `ingest` | `POST /api/v1/events` and `POST /api/v1/logs` only |

An `ingest`-scoped key presented to any other protected endpoint receives `403 Forbidden`.

### Key Storage

The plaintext key is never stored. BugBarn stores only `SHA-256(plaintext_key)` as a hex string in `api_keys.key_sha256`. On each request, the server computes `SHA-256(provided_key)` and performs a constant-time comparison against the stored hash (for the static env-var key) or a database lookup by hash.

The `last_used_at` column in `api_keys` is updated on every successful database-key authentication.

### Static Environment Variable Key

A single global key can be configured without a database record:

- `BUGBARN_API_KEY` — plaintext key; BugBarn hashes it at startup
- `BUGBARN_API_KEY_SHA256` — pre-computed SHA-256 hex digest (preferred)

A static env-var key is always `full`-scoped and is not bound to any project (`project_id = 0`), meaning it has access to all projects.

### Database Keys

Keys created with `bugbarn apikey create` are stored in the `api_keys` table. They are bound to a specific project and can have either scope. The `bugbarn apikey create` CLI command generates a 32-byte random key, prints the plaintext once, and stores only the SHA-256 hash.

When both a static env-var key and database keys are configured, the static key is checked first.

---

## CSRF Protection

### When It Applies

CSRF protection applies to **session-authenticated, state-changing requests** (any method other than `GET`, `HEAD`, or `OPTIONS`) against all protected API endpoints. It does **not** apply to:

- Requests authenticated by API key (API keys are out-of-band credentials that a cross-site attacker cannot access)
- `GET` / `OPTIONS` requests
- The ingest endpoint (`/api/v1/events`) — that endpoint accepts wildcard CORS by design

### Token Format

The CSRF token is derived from the session token:

```
csrf_token = hex( HMAC-SHA256(key="csrf", message=session_token) )[:16 bytes] = 32 hex characters
```

This token is stored in the `bugbarn_csrf` cookie (readable by JavaScript because `HttpOnly=false`) and must be included as the `X-BugBarn-CSRF` request header on every protected state-changing request.

### How to Include in Requests

1. After login, read the `bugbarn_csrf` cookie value from JavaScript.
2. Attach it as a request header: `X-BugBarn-CSRF: <token>`.

The Web UI does this automatically. If the header is absent or the token does not match the one derived from the current session cookie, the server responds with `403 Forbidden`.

---

## Project Resolution

Every authenticated request is resolved to a project (or to "all projects" for GET requests with session auth). The resolution logic is:

```
If X-BugBarn-Project header is present:
  → EnsureProject(slug)   [auto-creates the project if not found]
  → use that project ID

Else if request is authenticated by API key AND the key is bound to a project:
  → use the key's project_id

Else if request is authenticated by session AND method != GET:
  → use the "default" project

Else if request is authenticated by session AND method == GET:
  → project ID = 0  (all-projects mode: queries span all projects)
```

The resolved project ID is stored in the request context so storage methods can distinguish "all projects" (`0`) from "specific project" (non-zero).

---

## CORS

### Ingest and Log Ingest Endpoints

`POST /api/v1/events` and `POST /api/v1/logs` respond with:

```
Access-Control-Allow-Origin: *
Access-Control-Allow-Headers: content-type, x-bugbarn-api-key, x-bugbarn-project
Access-Control-Allow-Methods: POST, OPTIONS
```

The wildcard origin is intentional: browser-side SDKs must be able to POST errors from any origin without requiring the BugBarn host to know every client origin in advance.

### All Other Endpoints

All other API endpoints use a configurable origin allowlist set via the `BUGBARN_ALLOWED_ORIGINS` environment variable (comma-separated list). Only origins present in that list receive a matching `Access-Control-Allow-Origin` response header.

### Why Ingest-Scope Keys Are Safe for Browser Use

Exposing an API key in browser JavaScript is acceptable for `ingest`-scoped keys because:

1. An `ingest`-scoped key is only accepted at `POST /api/v1/events` and `POST /api/v1/logs`. It cannot read issues, events, settings, or any other data.
2. The worst an attacker who intercepts the key can do is submit fabricated error events, which have no impact on application security or confidentiality.
3. The key is bound to a specific project, so any noise is isolated to that project.

`full`-scoped keys should never be used in browser-side code.
