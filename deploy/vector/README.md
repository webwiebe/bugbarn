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
host/          agents for hosts outside the clusters (macOS, Linux), see below
tests/         `vector test` cases for the transforms
check.sh       runs the tests and validates every overlay and host config
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

## Hosts outside the clusters

`host/` holds agents for machines that run no DaemonSet: the macOS CI mac-minis
and laptop, and any plain Linux box. Both ship to production BugBarn with one
shared ingest key of the `infra` project (`vector-hosts`), SOPS-encrypted in
`host/secret.yaml`.

| File | Host | Security source | `source` |
|---|---|---|---|
| `host/macos/vector.yaml` | macOS, per-user LaunchAgent | `log stream` for sshd, sshd-session, sudo, su | `macos` |
| `host/linux/vector.yaml` | Linux with systemd | journald for sshd, sudo, account changes | `syslog` |

Both also run `host_metrics`. On a mac, from a checkout of this repo:

```
sops -d --extract '["stringData"]["api_key"]' deploy/vector/host/secret.yaml |
  deploy/vector/host/macos/install.sh
```

`install.sh` downloads the pinned Vector into `~/.vector`, writes the key to
`~/.vector/secrets/api_key` (0600), fills in the `__VECTOR_HOME__` placeholder
(Vector does not expand env vars), validates and (re)loads the LaunchAgent
`xyz.wiebe.bugbarn.vector`. Re-run it to upgrade. On Linux, install the Vector
package, copy `host/linux/vector.yaml` to `/etc/vector/vector.yaml`, put the key
in `/etc/vector/secrets/api_key` (0600, owned by the vector user) and add the
vector user to the `systemd-journal` group.

## Deploying

`.woodpecker/vector.yaml` runs `check.sh` on every PR that touches this directory,
and on main applies the secret and `kubectl apply -k` per cluster, then waits on
`rollout status daemonset/vector`. Run `./deploy/vector/check.sh` locally before
pushing; it needs docker.
