# Deployment

Plain Kustomize, one directory per environment under `deploy/k8s/`. No Helm. Each
`kustomization.yaml` maps images to `ghcr.io/webwiebe/bugbarn/{service,web}` with the
placeholder tag `local`; the pipelines replace it with `kubectl set image` (by digest on
staging and production). `deploy/README.md` predates the reader/writer split; trust the
manifests and this file.

## Topology

| | testing | staging | production |
|---|---|---|---|
| Host | k3s1 | k3s1 | layer7 (`layer7-prod`) |
| Shape | monolith `deployment/bugbarn` | writer + readers + Redis | writer + readers + Redis |
| Readers | none | 2, HPA to 5 | 2, HPA to 6 |
| Reader spool | none | none (synchronous proxy) | `emptyDir` 512Mi at `BUGBARN_SPOOL_DIR` |
| Extra | | | ingest-only `bb.<domain>` ingresses, hourly settings snapshot CronJob |

The role comes from `BUGBARN_MODE` (`writer`, `reader`, unset for the monolith); see
`internal/ingest/CLAUDE.md` for what each role runs.

- **Writer** (`writer-deployment.yaml`): `replicas: 1`, `strategy: Recreate`, mounts PVC
  `bugbarn-data` read-write. SQLite allows one writer and the PVC is `ReadWriteOnce`;
  a RollingUpdate would start a second writer against the same file. Keep it at 1 and
  Recreate. The startupProbe allows 600s because migrations on the production database
  (over 10 GB) take minutes, and the pipelines wait 600s on the writer rollout to match.
- **Reader** (`reader-deployment.yaml`): RollingUpdate with `maxUnavailable: 0`, mounts
  the same PVC `readOnly`, forwards writes to `http://bugbarn-writer:8080`. The HPA
  scales on 60% CPU. In production `terminationGracePeriodSeconds: 75` must stay above
  the reader's 45s spool drain on shutdown.
- **Redis** (`redis-queue-deployment.yaml`): the write queue between readers and the
  writer. `noeviction` on purpose: a full Redis rejects publishes and the reader spools
  keep the events.
- **Services**: `bugbarn` selects reader pods only, `bugbarn-writer` the writer. Ingress
  sends `/api` to `bugbarn` and `/` to `bugbarn-web`.

Staging and production have no `deployment.yaml`/`service.yaml`. The pipelines still run
`kubectl delete deployment/bugbarn --ignore-not-found` to clear the pre-split Deployment.
A new `service.yaml` named `bugbarn` would collide with `reader-service.yaml`.

## Secrets

SOPS with age (`.sops.yaml`), applied by the pipelines with
`sops -d ... | kubectl apply -f -`, never through Kustomize:

- `k8s/<env>/secret.yaml`: `bugbarn-secrets`
- `k8s/production/smtp-secret.yaml`: SMTP and digest settings, production only (other
  envs reference it `optional: true`)
- `ci-secrets.yaml`, `woodpecker-secrets.yaml`: CI credentials (`make woodpecker-secrets-sync`)

Pods do not restart on a Secret change; the deploy restarts them.

## Pipeline chain (Woodpecker, `.woodpecker/`)

1. Push to `main`: `ci` (tests and quality gates). `build-and-test` and `binary-release`
   both `depends_on: ci`, so a red `ci` ships nothing.
2. `build-and-test` builds `:$CI_COMMIT_SHA` images and deploys **testing**.
3. `binary-release` bumps the patch version from `git describe` and pushes a
   **lightweight** tag to Gitea and GitHub, then builds the debs, tarballs and Homebrew
   formula. An annotated tag makes Woodpecker report the tag-object sha as
   `CI_COMMIT_SHA` and breaks the retag.
4. The tag runs `release`: wait for the `:SHA` images, retag to semver, deploy
   **staging** by digest.
5. `deploy-production` (`depends_on: release`): preflight checks the semver images in
   GHCR, saves the running images, deploys **production** by digest. On failure
   `rollback-on-failure` restores the saved images, and exits without doing anything
   when no snapshot was saved. `kubectl rollout undo` would land on the `:local`
   revision that `apply -k` leaves behind, which is why the rollback sets images
   explicitly.

`ghcr-prune` is a daily cron. `.github/workflows/` still holds mirrors; only `ci.yml` and
`deploy-site.yml` are enabled on GitHub, and the deploy chain there is disabled.

## Changing a manifest

- An env var or volume added to the writer usually belongs on the reader too, and in all
  three environments (the testing monolith is `testing/deployment.yaml`).
- Check `kubectl kustomize deploy/k8s/<env>` renders before pushing; there is no other
  local check.
