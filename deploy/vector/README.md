# Vector agents

A Vector DaemonSet on each cluster ships security logs and host metrics to
BugBarn's telemetry endpoints (`POST /api/v1/telemetry/security` and
`/api/v1/telemetry/metrics`, see `docs/api.md`). BugBarn parses everything in
Go (`internal/secnorm`, `internal/hostmetrics`); Vector only reads, tags each
record with `source` and `host`, batches and buffers.

```
k8s/base/      DaemonSet, RBAC, namespace and the shared pipeline (vector.yaml)
k8s/k3s1/      homelab cluster (k3s1 + k3s2): sinks.yaml, secret.yaml
k8s/layer7/    production node: sinks.yaml, secret.yaml
tests/         `vector test` cases for the transforms
check.sh       runs the tests and validates every overlay
```

## Sources

| Source | Read from | `source` |
|---|---|---|
| sshd, sudo, su, account changes | `/var/log/auth.log` on the host | `syslog` |
| k8s audit log | `/var/lib/rancher/k3s/server/logs/audit.log` | `k8saudit` |
| Traefik access log, JSON lines only | `kubernetes_logs` for the kube-system Traefik pods | `traefik` |
| CPU, load, memory, filesystems, network | `host_metrics` every 60s | metrics |

Traefik lines in the default CLF format are dropped: behind Cloudflare they
carry no client IP. Both the Traefik JSON access log and the k8s audit log are
switched on in the k3s configuration (the k3s-infra repo), and the agents pick
them up once they exist. The agents drop Traefik lines for their own
`/api/v1/telemetry/` POSTs.

## Overlays and keys

Each overlay names its BugBarn in `sinks.yaml` and carries a SOPS-encrypted
`secret.yaml` (Secret `vector-bugbarn`, key `api_key`). The key is an ingest-scoped
key of the `infra` project, created inside the BugBarn pod:

```
kubectl -n <namespace> exec deploy/<bugbarn or bugbarn-writer> -c bugbarn -- \
  bugbarn apikey create --project infra --name vector-<overlay> --scope ingest
```

Encrypt it straight into `secret.yaml` without writing plaintext to disk. Vector
0.58 reads the key with its `directory` secret backend (`SECRET[bugbarn.api_key]`),
which works in headers only, which is why each overlay spells out its URIs.

## Deploying

`.woodpecker/vector.yaml` runs `check.sh` on every PR that touches this directory,
and on main applies the secret and `kubectl apply -k` per cluster, then waits on
`rollout status daemonset/vector`. Run `./deploy/vector/check.sh` locally before
pushing; it needs docker.
