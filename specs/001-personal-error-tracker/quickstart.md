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

Testing endpoint:

```bash
https://bugbarn.test.wiebe.xyz/api/v1/events
```

Staging endpoint:

```bash
https://bugbarn.staging.wiebe.xyz/api/v1/events
```

Read the active API keys from the Kubernetes secrets:

```bash
kubectl -n bugbarn-testing get secret bugbarn-api-key -o jsonpath='{.data.BUGBARN_API_KEY}' | base64 -d; echo
kubectl -n bugbarn-staging get secret bugbarn-api-key -o jsonpath='{.data.BUGBARN_API_KEY}' | base64 -d; echo
```

If either command prints a `replace-me-*` value, rotate the secret before connecting a real application.

## Local TypeScript SDK Package

Until packages are published to a registry, build the SDK tarball locally:

```bash
cd sdks/typescript
npm install
npm run build
npm pack
```

Install it from Rapid Root or another local project:

```bash
cd /Users/wiebe/webwiebe/rapid-root
pnpm add /Users/wiebe/webwiebe/temu-sentry/sdks/typescript/bugbarn-typescript-0.1.0.tgz
```

This is suitable for local testing. Rapid Root's CI and Docker builds use pnpm frozen lockfiles, so the testing/staging rollout should vendor the SDK as a Rapid Root workspace package or publish `@bugbarn/typescript` to a package registry before relying on it in automated builds.
