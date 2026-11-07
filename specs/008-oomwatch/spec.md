# Feature Specification: OOMKilled reporting (oomwatch)

**Feature Branch**: `feat/oomwatch`
**Created**: 2026-11-07
**Spec**: 008-oomwatch
**Status**: Partial
**Last verified against the tree**: 2026-11-07
**Issues**: bugbarn#266
**Principles touched**: I
**Input**: Containers killed for exceeding their memory limit leave no event in BugBarn. The kubelet records the kill in pod status only.

## Purpose

A small in-cluster watcher lists pods through the Kubernetes API and reports every container terminated with reason `OOMKilled` as an event to BugBarn. One watcher runs per cluster (k3s1 and layer7). It runs as `bugbarn oomwatch` from the BugBarn image.

## Requirements

- **FR-001**: The watcher reads pods cluster-wide (`get`, `list` only) and finds containers and init containers whose current or last terminated state has reason `OOMKilled`. Test: `internal/oomwatch/oomwatch_test.go` (kills).
- **FR-002**: Each termination is reported once, keyed by pod UID, container and finish time. Terminations older than the start-up lookback (10 minutes) are skipped. A failed report is retried on the next scan. Test: tracker and retry tests.
- **FR-003**: The event carries the fingerprint `oomkilled:<namespace>/<container>`, so repeats group into one issue. Attributes: cluster, namespace, pod, container, memory limit, restart count, exit code, terminated time.
- **FR-004**: Events go to the `cluster` project (`OOMWATCH_DEFAULT_PROJECT`) with a project-bound ingest key from `bb key-request`. `OOMWATCH_PROJECTS` can map namespaces to other projects but needs a full-scope key, so the overlays leave it unset. The namespace is in the fingerprint and attributes.
- **FR-005**: Each cluster deploys through `.woodpecker/oomwatch-deploy.yaml` on a version tag after `release`, by image digest.
- **FR-006**: The watcher holds no write verbs on the cluster. `.woodpecker/oomwatch.yaml` rejects write verbs in the ClusterRole.

## Open items

- [gap] The SOPS secrets `deploy/oomwatch/k8s/{k3s1,layer7}/secret.yaml` need an ingest key for `cluster` approved by the owner (bugbarn#267).
- [gap] The kill test on testing (acceptance criterion 4) runs after the first deploy.

## Drift

None.

## Sources

- `internal/oomwatch/`, `cmd/bugbarn/oomwatch.go`, `deploy/oomwatch/`, `.woodpecker/oomwatch*.yaml`
