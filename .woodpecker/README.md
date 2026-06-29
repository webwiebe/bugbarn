# Woodpecker CI pipelines

bugbarn's CI/CD, migrated off GitHub Actions onto self-hosted **Woodpecker CI**
(server on the nijmegen k3s cluster, integrated with the Gitea forge). This
escapes GitHub's per-job runner metering entirely.

## What runs where

All pipelines are pinned to the **Mac Mini build agent** (`labels: type: build`,
Woodpecker *local* backend) — the same host that ran the GitHub self-hosted
runner, so host `docker`/`buildx`, the persistent `bugbarn-ci` builder, native
node/python/php, the SSH deploy keys (`root@k3s1`, `deployer@layer7`) and the
keychain-free `DOCKER_CONFIG` trick all work unchanged.

| Pipeline | Trigger | Replaces |
|---|---|---|
| `ci.yaml` | push main, PR, manual | `ci.yml` (spec/go/node/python/php/quality) |
| `build-and-test.yaml` | push main, manual | `build-and-test.yml` (build + deploy testing) |
| `release.yaml` | tag `v*` | `release.yml` (retag + deploy staging) |
| `deploy-production.yaml` | manual | `deploy-production.yml` |
| `tag-check.yaml` | tag | `tag-check.yml` |

### Still on GitHub Actions (intentionally)

`deploy-site.yml` (GitHub **Pages**) and `binary-release.yml` (GitHub
**Releases** + Homebrew tap + rapid-root APT dispatch) are bound to
GitHub-platform features. They keep running on GitHub, fed by the Gitea→GitHub
**push-mirror**. Porting them means re-hosting the docs site off Pages and
re-pointing release distribution — tracked as a follow-up, not part of this cut.

## Required Woodpecker secrets (repo scope)

Create these in the Woodpecker UI (Repo → Settings → Secrets). Restrict the
deploy/registry-write secrets to `push`, `tag`, and `manual` events so PR
pipelines can't read them.

| Secret | Value | Used by |
|---|---|---|
| `ghcr_token` | GHCR PAT with **write:packages** (replaces the old `GITHUB_TOKEN`) | build/retag/preflight |
| `ghcr_pull_pat` | GHCR PAT with **read:packages** (existing) | in-cluster `ghcr-secret` |
| `sops_age_key_testing` | age private key (testing) | build-and-test deploy |
| `sops_age_key_staging` | age private key (staging) | release deploy |
| `sops_age_key_production` | age private key (production) | production deploy |
| `bugbarn_api_key` | BugBarn release-marker API key | all deploys |

SSH keys are **not** secrets — they're already on the Mac Mini host (`~/.ssh`),
reused by the local backend. `BUGBARN_ENDPOINT` is hardcoded (`https://bugbarn.wiebe.xyz`).

## Production deploy (manual params)

Woodpecker has no typed `workflow_dispatch` inputs. Trigger `deploy-production`
manually and add two custom pipeline variables:

- `PRODUCTION_VERSION` = `vX.Y.Z` (must already be on staging)
- `CONFIRMED` = `true`

## Migration steps (run once, when the Woodpecker server is live)

1. **Gitea**: migrate `github.com/webwiebe/bugbarn` into Gitea as a normal repo;
   add a push-mirror Gitea→GitHub (`sync_on_commit`). Point `origin` at Gitea,
   keep GitHub as a second remote.
2. **Woodpecker**: add the repo (installs the Gitea webhook), create the secrets
   above, confirm the `type=build` agent is connected.
3. Push a branch / open a PR → `ci.yaml` runs green.
4. Once validated, **disable** the 5 replaced GitHub workflows (reversible),
   leaving `deploy-site` + `binary-release` enabled:
   ```bash
   for wf in ci.yml build-and-test.yml release.yml deploy-production.yml tag-check.yml; do
     gh workflow disable "$wf" -R webwiebe/bugbarn
   done
   ```

## Notes / caveats

- **Node version**: jobs use the host's `node` (homebrew) rather than a pinned
  `setup-node@22`. Confirm the host node major matches expectations.
- **Parallelism**: CI steps run sequentially on one agent (faithful, safe first
  cut). Offloading the light legs (node/python/php/spec) to the in-cluster
  `type=light` Kubernetes agent is the obvious next optimization.
