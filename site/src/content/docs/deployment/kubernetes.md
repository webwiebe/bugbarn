# Kubernetes Deployment

## Overview

A full BugBarn deployment consists of the following Kubernetes resources:

| Resource | Name | Purpose |
|---|---|---|
| Deployment | `bugbarn` | BugBarn service (API + background worker) |
| Deployment | `bugbarn-web` | Web frontend (static asset server) |
| PersistentVolumeClaim | `bugbarn-data` | Spool directory and SQLite database (1 Gi) |
| Service | `bugbarn` | ClusterIP for the API pod |
| Service | `bugbarn-web` | ClusterIP for the web pod |
| Ingress | `bugbarn` | Routes `/api/*` to the service pod and `/` to the web pod |
| Secret | `bugbarn-secrets` | Core auth and Litestream credentials |
| Secret | `smtp-secret` | SMTP credentials and digest configuration |

All resources live in a dedicated namespace (e.g., `bugbarn-production`).

---

## Deployment Strategy

Both deployments use `strategy.type: Recreate`.

**Why Recreate and not RollingUpdate?** BugBarn uses SQLite as its database. SQLite allows only one writer at a time, and the database file is stored on a `ReadWriteOnce` PVC. Running two pods simultaneously — even briefly during a rolling update — would cause the second pod to fail to acquire the write lock on the database and the PVC mount. `Recreate` terminates the existing pod before starting the new one, ensuring clean single-writer semantics.

> Never change the strategy to `RollingUpdate` for the `bugbarn` deployment.

---

## Health Checks

Both probes target `GET /api/v1/health`, which returns `{"status":"ok"}` when the server is ready.

| Probe | Initial Delay | Period | Failure Threshold |
|---|---|---|---|
| Liveness | 30 s | 20 s | 3 |
| Readiness | 10 s | 10 s | 3 (default) |

The liveness probe has a longer initial delay to give Litestream time to restore the database from the replica before BugBarn starts serving traffic. The readiness probe uses a shorter delay so the pod is marked ready as soon as the server accepts requests.

---

## Volume Setup

The `bugbarn-data` PVC is mounted at `/var/lib/bugbarn` inside the service container. It stores:

- **SQLite database** — at `/var/lib/bugbarn/bugbarn.db` (set via `BUGBARN_DB_PATH`).
- **Event spool** — at `/var/lib/bugbarn/spool` (set via `BUGBARN_SPOOL_DIR`).

The PVC is provisioned with `storageClassName: local-path` and `accessModes: [ReadWriteOnce]`, which is appropriate for a single-replica SQLite workload.

The web deployment mounts a separate `bugbarn-packages` PVC (256 Mi) at `/srv/packages` for serving source-map packages.

---

## Secrets Management

Secrets are stored as SOPS-encrypted YAML files in the repository and decrypted during the CI/CD deployment workflow before being applied with `kubectl apply`.

### bugbarn-secrets

Contains core authentication and Litestream replication credentials:

- `BUGBARN_API_KEY`
- `BUGBARN_ADMIN_USERNAME`
- `BUGBARN_ADMIN_PASSWORD_BCRYPT`
- `BUGBARN_SESSION_SECRET`
- `LITESTREAM_ACCESS_KEY_ID`
- `LITESTREAM_SECRET_ACCESS_KEY`

### smtp-secret

Contains SMTP credentials and digest configuration:

- `SMTP_HOST`
- `SMTP_PORT`
- `SMTP_USER`
- `SMTP_PASS`
- `SMTP_FROM`
- `BUGBARN_DIGEST_ENABLED`
- `BUGBARN_DIGEST_TO`
- `BUGBARN_DIGEST_WEBHOOK_URL`

> After patching either secret, always run `kubectl rollout restart deployment/bugbarn` so the pod picks up the new values.

---

## Litestream

[Litestream](https://litestream.io) provides continuous streaming replication of the SQLite database to an S3-compatible object store. It runs alongside BugBarn — either as a sidecar container or as a wrapper process — and is configured entirely through environment variables injected from `bugbarn-secrets`.

Litestream is transparent to BugBarn. BugBarn simply opens the SQLite file at the path set by `BUGBARN_DB_PATH`; Litestream watches that file and streams WAL pages to the configured replica path.

The relevant environment variables (consumed by Litestream, not BugBarn):

| Variable | Description |
|---|---|
| `LITESTREAM_REPLICA_PATH` | S3-compatible path for the replica (e.g., `production/bugbarn.db`) |
| `LITESTREAM_ACCESS_KEY_ID` | Object-storage access key |
| `LITESTREAM_SECRET_ACCESS_KEY` | Object-storage secret key |

---

## Image Registry and Tags

Images are published to the GitHub Container Registry:

| Image | GHCR path |
|---|---|
| Service (API + worker) | `ghcr.io/webwiebe/bugbarn/service:VERSION` |
| Web frontend | `ghcr.io/webwiebe/bugbarn/web:VERSION` |

`VERSION` follows semantic versioning (e.g., `v1.2.3`). The `kustomization.yaml` file controls which tag is deployed by overriding the image references. Image pulls require the `ghcr-secret` pull secret to be present in the namespace.

---

## Rolling Upgrades

Because the deployment strategy is `Recreate`, every upgrade causes a brief downtime while the old pod is terminated and the new pod starts. To minimise impact:

1. The liveness probe allows 30 seconds for startup before it begins checking — long enough for Litestream to restore the database on a fresh node.
2. The readiness probe ensures traffic is not sent to the pod until `/api/v1/health` returns `200`.
3. `revisionHistoryLimit: 2` keeps two old ReplicaSets for rollback.

To roll back to a previous image tag, update the image reference in `kustomization.yaml` and re-apply.

---

## Resource Limits

| | CPU | Memory |
|---|---|---|
| Request | 100m | 128Mi |
| Limit | 500m | 256Mi |

These are the limits for the `bugbarn` service container. The `bugbarn-web` frontend container does not define resource limits in the base manifests. See the [configuration reference](configuration.md#resource-sizing-guidance) for guidance on when to adjust these values.

---

## Kustomize Image Override Pattern

The `kustomization.yaml` file in each environment directory uses Kustomize's `images` field to pin the deployed version without modifying the base deployment manifests directly.

```yaml
# deploy/k8s/production/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: bugbarn-production
resources:
  - namespace.yaml
  - pvc.yaml
  - deployment.yaml
  - service.yaml
  - web-deployment.yaml
  - web-service.yaml
  - ingress.yaml
images:
  - name: bugbarn/service
    newName: ghcr.io/webwiebe/bugbarn/service
    newTag: v1.2.3
  - name: bugbarn/web
    newName: ghcr.io/webwiebe/bugbarn/web
    newTag: v1.2.3
```

To deploy a new version, update `newTag` in the relevant environment's `kustomization.yaml` and apply:

```sh
kubectl apply -k deploy/k8s/production/
```
