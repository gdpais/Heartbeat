# Installing a Release

How to install, upgrade and remove Heartbeat from a published
[GitHub release](https://github.com/gdpais/Heartbeat/releases), with Helm and
no source checkout. Each release publishes:

| Artifact | Where |
| --- | --- |
| Helm chart | `oci://ghcr.io/gdpais/charts/heartbeat`, version `X.Y.Z` |
| DB collector image | `ghcr.io/gdpais/heartbeat/db-collector:X.Y.Z`, for `linux/amd64` and `linux/arm64` |

Both are public: no registry login is needed. The release notes list each
artifact's digest. The chart also installs pinned Prometheus, Alertmanager,
Grafana, Loki and OpenTelemetry Collector from their own registries
([what the chart deploys](kubernetes-local.md#what-the-chart-deploys)).

To work on Heartbeat itself, use [local development](local-development.md)
instead. Production deploys the same chart from ECR through Argo CD
([ADR 0005](../architecture/decisions/0005-production-delivery-and-operations-defaults.md)).

## What you need

- A Kubernetes cluster. CI tests Kubernetes 1.36 on kind.
- Helm 4.2 and kubectl. The chart also renders with Helm 3, but installs are
  tested with Helm 4.
- A default StorageClass. The release asks for five volumes, about 21 GiB in
  all (listed under [Uninstall](#uninstall)).
- Memory: the pods request about 0.8 GiB, and their limits add up to about
  3.3 GiB.
- A CNI that enforces NetworkPolicy (kindnet, Calico, Cilium, Amazon VPC CNI
  with network policy on). Without one the chart's policies are accepted and
  drop nothing.

The commands below use the namespace `heartbeat` and the release name
`heartbeat`. Any names work, but install one release per namespace: the
object names are fixed.

```bash
VERSION=X.Y.Z   # the release to install, without the v
```

## 1. Create the namespace and Secrets

The chart never creates Secrets
([ADR 0005](../architecture/decisions/0005-production-delivery-and-operations-defaults.md)),
so create these two first. Without them the collector and Grafana pods wait in
`CreateContainerConfigError`.

```bash
kubectl create namespace heartbeat
# Bearer token for the collector's admin endpoints.
kubectl -n heartbeat create secret generic heartbeat-runtime \
  --from-env-file=<(printf 'admin-token=%s\n' "$(openssl rand -hex 32)")
# Grafana's admin login.
kubectl -n heartbeat create secret generic grafana-admin \
  --from-env-file=<(printf 'admin-user=admin\nadmin-password=%s\n' "$(openssl rand -base64 24)")
```

The values reach `kubectl` through a pipe, not its command line. To read one
back: `kubectl -n heartbeat get secret grafana-admin -o jsonpath='{.data.admin-password}' | base64 -d`.
[Kubernetes delivery](kubernetes-local.md#secrets) lists every Secret the
chart reads and where kind and production get them.

## 2. Pin the release

Put your values in a file and keep it: every `helm upgrade` passes it again.
Start with the image digest from the release notes' *Published artifacts*
table:

```yaml
# heartbeat.yaml
dbCollector:
  image:
    # The sha256 after the @ in the db-collector row of the release notes.
    digest: sha256:<digest>
```

Without it the chart runs `ghcr.io/gdpais/heartbeat/db-collector:X.Y.Z`, the
image of its own version. The digest names exactly the build the release
lists; a digest wins over a tag.

## 3. Install

```bash
helm install heartbeat oci://ghcr.io/gdpais/charts/heartbeat --version "$VERSION" \
  --namespace heartbeat -f heartbeat.yaml --wait --timeout 10m
```

Helm prints `Pulled: ghcr.io/gdpais/charts/heartbeat:X.Y.Z` and a
`Digest: sha256:...`; check it against the chart row of the release notes.
It then prints the chart's notes: the Secrets it needs, how to check the
install and how to reach Grafana. `helm show readme oci://ghcr.io/gdpais/charts/heartbeat --version "$VERSION"`
prints the chart's README.

If a value was wrong, fix it in `heartbeat.yaml` and run the
[upgrade](#upgrade) command. A collector pod that never became Ready is
replaced by the next upgrade; there is no pod to delete by hand.

## 4. Check that it works

```bash
kubectl -n heartbeat get pods   # every pod Running, every container ready
kubectl -n heartbeat port-forward svc/db-collector 8082 &
curl -s localhost:8082/readyz   # {"status":"ready"}
HEARTBEAT_ADMIN_TOKEN=$(kubectl -n heartbeat get secret heartbeat-runtime -o jsonpath='{.data.admin-token}' | base64 -d)
# The header goes to curl on stdin, so the token stays off its command line.
printf 'Authorization: Bearer %s\n' "$HEARTBEAT_ADMIN_TOKEN" | curl -s -H @- localhost:8082/admin/config |
  jq '{version, status: .readiness.status, warnings}'
kubectl -n heartbeat port-forward svc/grafana 3000:3000 &
# later: kill %1 %2 stops both port-forwards
```

Then open http://localhost:3000 and log in with the `grafana-admin` Secret's
user and password. The *Heartbeat* folder holds the dashboards. They show
data once a SQL Server target is added (next step).

`/readyz` says whether the collector itself works, not the databases: it
stays ready while every SQL Server target fails
([endpoints](../reference/metrics-and-endpoints.md#db-collector-8082)).

| Symptom | Cause and fix |
| --- | --- |
| `CreateContainerConfigError` on `db-collector-0` or the Grafana pod | A Secret from step 1 is missing or lacks a key; `kubectl -n heartbeat describe pod <pod>` names it. Create it; the pod then starts by itself |
| `ImagePullBackOff` or `ErrImagePull` | The digest in `heartbeat.yaml` is wrong, or the cluster cannot reach `ghcr.io` (or Docker Hub, `quay.io` for the engines) |
| Pods `Pending` with an unbound claim | No default StorageClass. Make one the default, or name a class for each claim in the values |
| `values don't meet the specifications of the schema` | A value is misspelt or out of range; the message gives its path. Unknown top-level keys are rejected too |

## 5. Add a SQL Server target

[Onboarding SQL Server targets](database-targets.md) is the reference: the
login and its grants, with the T-SQL to create it (step 1), the target entry
(step 2) and the credential (step 3). With a release install:

```bash
# The collector login's password, not echoed; the Secret key comes from
# credential_ref: env/finance-prod.
read -rs SQL_PASSWORD
kubectl -n heartbeat create secret generic heartbeat-sqlserver-credentials \
  --from-env-file=<(printf 'HEARTBEAT_CREDENTIAL_ENV_FINANCE_PROD=heartbeat_collector:%s\n' "$SQL_PASSWORD")
unset SQL_PASSWORD
```

In a second values file, `targets.yaml`, name that Secret and write the whole
`integrations.collectors` list as in
[step 2](database-targets.md#2-add-the-target), with the target under
`targets`. Lists replace rather than merge, so the list must be complete.

```yaml
# targets.yaml
dbCollector:
  credentials:
    existingSecret: heartbeat-sqlserver-credentials
integrations:
  collectors:
    - id: sqlserver-default
      # ... as in step 2 of the onboarding guide
```

Apply it with every values file, in the same order each time:

```bash
helm upgrade heartbeat oci://ghcr.io/gdpais/charts/heartbeat --version "$VERSION" \
  --namespace heartbeat -f heartbeat.yaml -f targets.yaml --wait --timeout 10m
```

Naming the Secret for the first time restarts the collector. A later change
to the target list hot-reloads without a restart; a change to the Secret's
contents needs `kubectl -n heartbeat rollout restart statefulset/db-collector`.
Then check the target as in [step 5](database-targets.md#5-verify), with the
port-forward from step 4 above.

## Upgrade

Read the release notes first, then set the new version and the new digest in
`heartbeat.yaml`:

```bash
VERSION=X.Y.Z
helm upgrade heartbeat oci://ghcr.io/gdpais/charts/heartbeat --version "$VERSION" \
  --namespace heartbeat -f heartbeat.yaml -f targets.yaml --wait --timeout 10m
```

Pass your values files rather than `--reuse-values`, so the new chart's
defaults apply to everything you did not set.

**From 0.1.0.** The release notes list the values that were renamed or
removed since; update your values files to match. Two changes also need a
step once:

1. The collector's StatefulSet now uses `podManagementPolicy: Parallel`, so it
   can replace a pod that never became Ready. Kubernetes cannot change that
   field in place, and the upgrade fails with
   `StatefulSet.apps "db-collector" is invalid: spec: Forbidden`. Delete the
   StatefulSet first, leaving its pod running:

   ```bash
   kubectl -n heartbeat delete statefulset db-collector --cascade=orphan
   ```

   The upgrade creates it again; the new StatefulSet adopts the running pod
   and replaces it with the new version.
2. The OTel gateway is gone
   ([ADR 0009](../architecture/decisions/0009-remove-the-otel-gateway.md)),
   and unknown top-level values now fail the upgrade. Remove `otelGateway`
   from your values files.

## Uninstall

```bash
helm uninstall heartbeat --namespace heartbeat
```

This deletes every object the release created, but not the claims Kubernetes
created for the StatefulSets' volume templates, nor what you created
yourself:

| Left behind | Size | Holds |
| --- | --- | --- |
| `PersistentVolumeClaim/storage-loki-0` | 10 GiB | The logs in Loki |
| `PersistentVolumeClaim/storage-alertmanager-0` | 1 GiB | Alertmanager's silences and notification log |
| `Secret/heartbeat-runtime`, `Secret/grafana-admin` and your credentials Secret | | Step 1 and step 5 |
| `Namespace/heartbeat` | | |

The other claims are deleted with the release, and their data with them:
`prometheus` (8 GiB, the metric history), `db-collector-state` (256 MiB, the
`outsystems_logs` watermarks) and `otel-collector-queue` (1 GiB). A new
install in the same namespace reuses the two claims left behind, so Loki and
Alertmanager start with their old data.

To remove everything:

```bash
kubectl -n heartbeat delete pvc storage-loki-0 storage-alertmanager-0
kubectl delete namespace heartbeat   # the Secrets and anything else left in it
```

With the usual `Delete` reclaim policy, deleting a claim also deletes its
volume and data.
