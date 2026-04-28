# BugBarn Documentation

BugBarn is a lightweight, self-hosted error tracking system built in Go with SQLite.

---

## Start here

**New user — try BugBarn locally**
- [Getting started](getting-started.md) — run BugBarn and send your first error in under five minutes.

**Business owner — evaluate BugBarn**
- [Overview](overview.md) — what BugBarn does, who it is for, and where it fits (and does not fit).

**Developer — integrate or deploy**
- [SDK overview](sdks/overview.md) — choose an SDK or use the HTTP API directly, understand API keys and event shape.
- [REST API reference](api.md) — full endpoint reference including authentication and payloads.
- [Deployment configuration](deployment/configuration.md) — all environment variables for production deployments.

---

## All documents

| File | Audience | Purpose |
|---|---|---|
| [overview.md](overview.md) | Everyone | What BugBarn is, key capabilities, and what it is not |
| [getting-started.md](getting-started.md) | New users | Run BugBarn locally and send your first error |
| [architecture/overview.md](architecture/overview.md) | Developers | System architecture, data flow, and component responsibilities |
| [architecture/storage.md](architecture/storage.md) | Developers | Database schema, spool format, and WAL replication |
| [architecture/authentication.md](architecture/authentication.md) | Developers | Session cookies, API keys, CSRF, and auth internals |
| [features/issues.md](features/issues.md) | Everyone | Issue grouping, statuses, and lifecycle |
| [features/alerts.md](features/alerts.md) | Everyone | Alert rules, conditions, delivery channels, and cooldowns |
| [features/digest.md](features/digest.md) | Everyone | Weekly digest email and JSON webhook |
| [features/logs.md](features/logs.md) | Everyone | Log ingestion, streaming, and filtering |
| [deployment/configuration.md](deployment/configuration.md) | Operators | All environment variables and their defaults |
| [deployment/kubernetes.md](deployment/kubernetes.md) | Operators | Kubernetes manifests, Litestream, and production deployment |
| [api.md](api.md) | Developers | Full REST API reference with request/response examples |
| [sdks/overview.md](sdks/overview.md) | Developers | Integration options, API key scopes, event shape, and privacy scrubbing |
| [sdks/go.md](sdks/go.md) | Developers | Go SDK installation, initialisation, and usage |
| [sdks/http.md](sdks/http.md) | Developers | Sending events directly over HTTP from any language |

---

## Quick links

- [Send your first error](getting-started.md)
- [All environment variables](deployment/configuration.md)
- [REST API reference](api.md)
- [Go SDK](sdks/go.md)
