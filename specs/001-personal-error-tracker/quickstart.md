# Quickstart: Personal Error Tracker Foundation

This is the intended first runnable path once implementation starts.

## Local Development

```bash
make setup
make test
make dev
```

Expected local services:

- Ingest/API service on `http://localhost:8080`
- Web UI on `http://localhost:5173`
- Worker process consuming the local durable spool
- Local database and spool under `.data/`

## First Admin User

Either configure environment variables:

```bash
BUGBARN_ADMIN_USERNAME=admin
BUGBARN_ADMIN_PASSWORD=change-me
```

Or run the setup CLI:

```bash
bugbarn admin create --username admin
```

## First Project Key

```bash
bugbarn projects create --name local-dev
bugbarn keys create --project local-dev --name sample-app
```

## Send a Test Event

```bash
curl -X POST http://localhost:8080/api/v1/events \
  -H 'content-type: application/json' \
  -H 'x-bugbarn-api-key: bb_live_example' \
  --data @specs/001-personal-error-tracker/fixtures/example-event.json
```

## Homelab Environments

Target namespaces:

- `bugbarn-testing`
- `bugbarn-staging`

Production is intentionally deferred.
