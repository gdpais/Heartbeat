# Kubernetes Delivery (Helm on kind)

Heartbeat is packaged as one Helm chart, used unchanged for kind, CI and
production with environment-specific values
([ADR 0003](../architecture/decisions/0003-helm-on-kind-and-production.md),
[ADR 0005](../architecture/decisions/0005-production-delivery-and-operations-defaults.md)).
To just run it, see [local development](local-development.md). This page
explains what is deployed and how to work with it.

## Layout

| Path | Contents |
| --- | --- |
| `infra/helm/heartbeat/` | The chart: Heartbeat templates, `values.yaml` defaults, `values.schema.json`, `Chart.yaml`/`Chart.lock` |
| `infra/helm/heartbeat/files/` | Prometheus rules (and their promtool tests) and Grafana dashboards, shipped as ConfigMaps |
| `infra/helm/values/` | Environment values: `kind.yaml`, `minimal.yaml`, `sqlserver-dev.yaml`, `production.example.yaml` |
| `infra/kind/cluster.yaml` | kind cluster: one node, host port mappings on 127.0.0.1 |
| `scripts/kind.sh` | Cluster, image and deploy workflow behind the `make kind-*` targets |
| `scripts/kind-e2e.sh` | Acceptance checks on an isolated, temporary cluster (`make kind-e2e`) |

## What the chart deploys

Heartbeat's own objects (fixed names; one release per namespace):

| Object | Notes |
| --- | --- |
| `StatefulSet/db-collector` | Always one replica: a replacement pod starts only after the old one is gone, so SQL targets are never polled twice. Liveness `/healthz` (process only), readiness `/readyz`. No config checksum: ConfigMap changes hot-reload without a restart |
| `Service/db-collector-headless` | Headless, `publishNotReadyAddresses`; Prometheus discovers the collector through it even while it is unready |
| `Service/db-collector` | Operator access (health, admin endpoints); ready pods only. kind: NodePort with `externalTrafficPolicy: Local`, so the NetworkPolicy sees the host's address |
| `NetworkPolicy/db-collector` | Ingress to port 8082 only from the bundled Prometheus and the `dbCollector.networkPolicy.prometheus` (shared Prometheus) and `.operators` peers; no peers means deny all. kind adds every source outside the pod subnet `10.244.0.0/16` (the host through the loopback NodePort, and containers on the Docker `kind` network); the production example adds a VPN range. Kubelet probes and `kubectl port-forward` are not affected. Needs a CNI that enforces NetworkPolicy (kind's kindnet does) |
| `Deployment/otel-gateway`, `Service/otel-gateway` | Rolls on config change (it reads `integrations.yaml` once) |
| `ConfigMap/heartbeat-integrations` | `integrations.yaml`, rendered from the `integrations` values |
| `ConfigMap/heartbeat-prometheus-rules` | Rule files; Prometheus mounts them at `/etc/prometheus-rules` and its config reloader reloads on change |
| `ConfigMap/heartbeat-dashboards` | Dashboards; label `grafana_dashboard: "1"` for shared Grafanas with a dashboard sidecar |

Third-party engines are pinned chart dependencies, each turned off with
`<name>.enabled=false` so an environment can use shared services instead:

| Dependency | Version | Engine | Notes |
| --- | --- | --- | --- |
| `prometheus` (prometheus-community) | 29.35.0 | Prometheus v3.15.0, Alertmanager v0.34.1 | Static and DNS discovery only, no cluster RBAC. Sends alerts to Alertmanager |
| `grafana` (grafana-community) | 13.2.7 | Grafana 13.2.3 | Stateless; datasources and dashboards from source |
| `loki` (grafana-community) | 18.13.7 | Loki 3.7.8 | Monolithic, filesystem storage locally |
| `opentelemetry-collector` (alias `otelCollector`) | 0.175.0 | otelcol-contrib 0.161.0 | Same pipelines as before: metrics to Prometheus, logs to Loki |

Rules the chart keeps (checked by `make chart-check`): only namespaced
objects, no CRDs; deterministic rendering (no random values, lookups or
hooks), because Argo CD renders the chart itself; Secrets created outside the
chart and referenced by name; renders with Helm 3 and Helm 4.

## Secrets

The chart never creates Secrets. `scripts/kind.sh` creates kind-only values:

| Secret | Keys | kind value | Production |
| --- | --- | --- | --- |
| `heartbeat-runtime` | `admin-token` | `local-admin-token` | External Secrets Operator |
| `grafana-admin` | `admin-user`, `admin-password` | `admin` / `admin` | Generated, External Secrets Operator |
| `heartbeat-sqlserver-dev-credentials` | `HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV` | From `.env.sqlserver-dev` | Per-environment credential Secret |

## Images

`make kind-images` builds both service images for the node's CPU architecture
(it refuses a mismatch), tags each `dev-<first 12 hex of the image ID>` and
loads them with `kind load`. Pods use `imagePullPolicy: Never`, so the cluster
can only run what was loaded. The tag changes whenever the binary changes, so a
code change always produces a new pod. Production references ECR images by
digest (`image.digest`).

## The cluster

`make kind-up` creates `heartbeat` from the node image pinned in the Makefile
(`KIND_NODE_IMAGE`, the newest Kubernetes minor EKS supports) and
`infra/kind/cluster.yaml`. NodePorts map to the Compose-era host ports on
127.0.0.1 only. Every `kubectl`/`helm` call in the scripts names the
`kind-heartbeat` context, so they never act on another cluster.

SQL Server is not in the cluster: `make sqlserver-dev-up` runs it as a Docker
container on the `kind` network, and pods reach it by container name.

## Working with it

```bash
make kind-status                                   # pods and services
kubectl --context kind-heartbeat -n heartbeat logs statefulset/db-collector
kubectl --context kind-heartbeat -n heartbeat port-forward svc/loki 3100
helm --kube-context kind-heartbeat -n heartbeat get values heartbeat
EXTRA_VALUES=my-values.yaml make kind-deploy        # layer your own values
```

Change the deployed config through values and `make kind-deploy`. Helm 4
upgrades with server-side apply, so a field you edit by hand with `kubectl`
(for example the integrations ConfigMap) becomes owned by kubectl, and the next
upgrade fails with a conflict. Re-apply the chart's version with
`--server-side --field-manager=helm --force-conflicts`, or delete the object
and redeploy. In production Argo CD reverts such drift.

## Production

Production values live in the `heartbeat-deploy` config repository and Argo CD
renders the chart from them (ADR 0005).
[`production.example.yaml`](../../infra/helm/values/production.example.yaml)
shows the shape (ECR digests, External Secrets, gp3 volumes, S3 for Loki,
internal ALB for Grafana, Slack/Teams/WhatsApp and healthchecks.io receivers)
and is rendered in CI so it stays valid. kind does not test cloud storage,
networking, identity, ingress or TLS; those need testing in the target
environment.
