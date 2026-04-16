# BugBarn deployment

The testing overlay is exposed at `bugbarn.test.wiebe.xyz` and the staging overlay is exposed at `bugbarn.staging.wiebe.xyz` through the cluster's standard K3S Traefik ingress:

```bash
kubectl apply -k deploy/k8s/testing
kubectl apply -k deploy/k8s/staging
```

The ingress sends `/api/*` to the Go service and all other paths to the static web service. The GitHub Actions deploy workflow builds and imports both images before applying the selected overlay.

Both deployments set `revisionHistoryLimit: 1`, and the deploy workflow deletes zero-replica BugBarn ReplicaSets after successful rollouts. This keeps repeated homelab deploys from accumulating stale ReplicaSet objects.

The adjacent `rapid-root` gateway already defines wildcard Caddy routes for `*.test.wiebe.xyz` and `*.staging.wiebe.xyz` on `192.168.4.111`, forwarding TLS-terminated traffic to the K3S Traefik entrypoint. BugBarn does not need a dedicated Caddy block while those wildcard routes remain active.

## Auth and Secrets

Application ingest accepts `x-bugbarn-api-key`. For compatibility, `BUGBARN_API_KEY` can still hold the plaintext key, but the preferred deployment value is `BUGBARN_API_KEY_SHA256`, a SHA-256 hex digest of the real key:

```bash
printf '%s' "$BUGBARN_API_KEY" | shasum -a 256 | awk '{print $1}'
```

The web/API read endpoints are public unless an admin user is configured. Configure session login by adding these keys to the `bugbarn-api-key` secret:

```bash
BUGBARN_ADMIN_USERNAME=admin
BUGBARN_ADMIN_PASSWORD_BCRYPT=<bcrypt hash>
BUGBARN_SESSION_SECRET=<random high-entropy string>
```

For local-only development, `BUGBARN_ADMIN_PASSWORD` can be used instead of `BUGBARN_ADMIN_PASSWORD_BCRYPT`; the service hashes it in memory at startup. Do not use plaintext admin passwords in committed manifests.

The session cookie is HTTP-only, SameSite=Lax, and marked Secure when BugBarn is reached through HTTPS or a proxy that sets `X-Forwarded-Proto: https`.

For direct cluster access, you can still use a port-forward:

```bash
kubectl -n bugbarn-testing port-forward svc/bugbarn 8080:8080
kubectl -n bugbarn-staging port-forward svc/bugbarn 8080:8080
kubectl -n bugbarn-testing port-forward svc/bugbarn-web 8081:8080
kubectl -n bugbarn-staging port-forward svc/bugbarn-web 8081:8080
```
