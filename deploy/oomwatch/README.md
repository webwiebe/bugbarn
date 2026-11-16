# oomwatch

Reports containers killed with reason `OOMKilled` to BugBarn. One Deployment per cluster, running `bugbarn oomwatch`.

- `k8s/base`: namespace, read-only RBAC (pods get/list), Deployment.
- `k8s/k3s1`, `k8s/layer7`: overlays with the endpoint, cluster name and namespace to project map.
- `k8s/<overlay>/secret.yaml`: SOPS-encrypted secret `oomwatch-secrets` with key `api_key`, an ingest key bound to the `cluster` project (`bb key-request cluster`).
- Deploy: `.woodpecker/oomwatch-deploy.yaml` on a version tag after `release`. Checks: `.woodpecker/oomwatch.yaml`.
- Spec: `specs/008-oomwatch/spec.md`.

Config env vars: `OOMWATCH_ENDPOINT`, `OOMWATCH_API_KEY`, `OOMWATCH_CLUSTER`, `OOMWATCH_DEFAULT_PROJECT`, `OOMWATCH_PROJECTS`, `OOMWATCH_INTERVAL`, `OOMWATCH_KUBE_API`.
