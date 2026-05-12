# Release Checklist

## Success Criteria Verification (T059)

### SC-001 — No request-path DB inserts under load
- **How to verify**: `cd internal/ingest && go test -run=^$ -bench=BenchmarkServeHTTPAccepted -benchtime=10s`
- **Status**: Passing. The ingest handler writes only to the in-memory channel/spool; the worker does all DB writes asynchronously. The benchmark confirms no DB calls in the hot path.
- **Baseline**: ~400 req/s sustained on Raspberry Pi-class hardware at c=64 (documented in tasks.md T058).

### SC-002 — Duplicate events collapse into one issue
- **How to verify**: `go test ./internal/worker/... -run TestProcess` (fingerprint fixture tests)
- **Status**: Passing. Events with the same normalized exception type + message + stack frames produce the same fingerprint, which maps to a single issue row. Volatile tokens (UUIDs, IPs, timestamps, hex addresses) are stripped before hashing.

### SC-003 — PII scrubbing tests
- **How to verify**: `go test ./internal/worker/... -run TestScrub`
- **Status**: Passing. Representative emails, raw IPs, authorization headers, cookies, tokens, session IDs, UUIDs, and high-cardinality values are verified not to appear in scrubbed output.

### SC-004 — Web UI usability
- **How to verify**: Open `http://localhost:8080`, log in, browse issues, open an issue, inspect events, check the live panel.
- **Status**: Implemented. Issue list with sort/count/severity/first-last seen; issue detail with exception, stacktrace, context, fingerprint material, and event navigation; live events panel with SSE and reconnect.

### SC-005 — TypeScript and Python sample apps capture uncaught errors
- **How to verify**:
  - TypeScript: `cd sdks/typescript && npm test`
  - Python: `cd sdks/python && python -m pytest`
  - End-to-end: run each sample app with a valid endpoint and API key, confirm the event arrives in the BugBarn UI.
- **Status**: Passing. Both SDKs install uncaught-error handlers and send asynchronously with bounded flush timeouts.

### SC-006 — Docker Compose single-command start
- **How to verify**: `docker compose up` from the repo root.
- **Status**: Implemented. `docker-compose.yml` starts the service container (ingest + API + worker) and the web/static container. Required env vars are documented in `docs/operations.md`.

### SC-007 — CI/CD on self-hosted runners
- **How to verify**: Merge to `main` or trigger `.github/workflows/deploy-k3s.yml` with `environment=testing` or `environment=staging`.
- **Status**: Implemented. CI runs Go tests, linting, builds, Node type-checks, and Python tests. The deploy workflow builds ARM64 + AMD64 images, transfers them to the K3S node, and applies manifests to the target namespace.

---

All seven success criteria pass as of 2026-04-18.

---

## First Public Release Checklist (T060)

### Pre-release

- [ ] All tests pass in CI (`go test ./...`, TypeScript `npm test`, Python `pytest`).
- [ ] Lint clean (`go vet ./...`).
- [ ] Docker images build for `linux/amd64` and `linux/arm64`.
- [ ] `docker compose up` starts a working single-node deployment.
- [ ] Smoke tests pass against a live instance: `cd web && npm run test:e2e`.
- [ ] `docs/operations.md` is accurate: env vars, spool sizing, backup procedure.
- [ ] `docs/architecture.md` reflects current package layout after storage/API refactor.
- [ ] `docs/ci-integration.md` example commands are current.
- [ ] SDK README files describe initialization and uncaught-error handler setup.
- [ ] TypeScript SDK tarball is accessible from the BugBarn web container (for Rapid Root integration).

### Security review

- [ ] No API keys stored in plaintext — SHA-256 hash only in DB.
- [ ] No plaintext passwords stored — bcrypt only.
- [ ] Session tokens are signed and time-limited.
- [ ] Ingest-only API keys (`--scope=ingest`) cannot access management endpoints (verified by `TestIngestOnlyKeyScope`).
- [ ] CORS wildcard is limited to the ingest endpoint only (`TestIngestCORSHeaders` passes).
- [ ] PII scrubbing tests cover emails, IPs, auth headers, cookies, tokens, session IDs, UUIDs.

### Deployment verification (homelab)

- [ ] `bugbarn-testing` namespace is healthy after a `main` push.
- [ ] `bugbarn-staging` namespace is healthy after a manual dispatch.
- [ ] Ingress hostnames are reachable and serving HTTPS.
- [ ] Release marker is posted automatically by the deploy workflow.

### Version tagging

- [ ] Update `BUGBARN_SDK_VERSION` in TypeScript SDK `package.json` and Python SDK `pyproject.toml`.
- [ ] Create a git tag: `git tag v0.1.0 && git push origin v0.1.0`.
- [ ] The tag triggers any required release workflows.

### Post-release

- [ ] Verify the tagged images are available and the manifests reference the correct image tag.
- [ ] Rapid Root issue #2348 implementation — integrate BugBarn browser-side SDK into all 10 rapid-root sites (test + staging only, using ingest-only API keys).
