# BugBarn deployment

The testing overlay is exposed at `bugbarn.test.wiebe.xyz` and the staging overlay is exposed at `bugbarn.staging.wiebe.xyz` through the cluster's standard K3S Traefik ingress:

```bash
kubectl apply -k deploy/k8s/testing
kubectl apply -k deploy/k8s/staging
```

The ingress sends `/api/*` to the Go service and all other paths to the static web service. The GitHub Actions deploy workflow builds and imports both images before applying the selected overlay.

Both deployments set `revisionHistoryLimit: 1`, and the deploy workflow deletes zero-replica BugBarn ReplicaSets after successful rollouts. This keeps repeated homelab deploys from accumulating stale ReplicaSet objects.

The adjacent `rapid-root` gateway already defines wildcard Caddy routes for `*.test.wiebe.xyz` and `*.staging.wiebe.xyz` on `192.168.4.111`, forwarding TLS-terminated traffic to the K3S Traefik entrypoint. BugBarn does not need a dedicated Caddy block while those wildcard routes remain active.

For direct cluster access, you can still use a port-forward:

```bash
kubectl -n bugbarn-testing port-forward svc/bugbarn 8080:8080
kubectl -n bugbarn-staging port-forward svc/bugbarn 8080:8080
kubectl -n bugbarn-testing port-forward svc/bugbarn-web 8081:8080
kubectl -n bugbarn-staging port-forward svc/bugbarn-web 8081:8080
```
