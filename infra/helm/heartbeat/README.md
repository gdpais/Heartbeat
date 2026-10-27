# Heartbeat Helm chart

Heartbeat's DB collector for SQL Server, its `integrations.yaml`, Prometheus
rules and Grafana dashboards, plus pinned Prometheus, Alertmanager, Grafana,
Loki and OpenTelemetry Collector, each of which an environment can turn off.

**Guide:** [Installing a release](https://github.com/gdpais/Heartbeat/blob/master/docs/guides/install-from-release.md)
covers the Secrets, pinning the image digest, adding SQL Server targets,
upgrades (with a one-time step from 0.1.0) and uninstalling. For the docs that
match your version, open the same path at its `vX.Y.Z` tag.

## Before you install

The chart creates no Secrets. Create these in the release's namespace first,
or the pods that need them wait in `CreateContainerConfigError`:

| Secret | Keys | For |
| --- | --- | --- |
| `heartbeat-runtime` | `admin-token` | The collector's admin endpoints |
| `grafana-admin` | `admin-user`, `admin-password` | Grafana's admin login |

## Install

```bash
helm install heartbeat oci://ghcr.io/gdpais/charts/heartbeat --version X.Y.Z \
  --namespace heartbeat --wait --timeout 10m
```

Install one release per namespace; object names are fixed. The collector
image defaults to `ghcr.io/gdpais/heartbeat/db-collector` at the chart's
version. Pin the digest from the release notes with
`dbCollector.image.digest`.

## Values

`helm show values` prints the defaults with their comments.
`values.schema.json` checks them: a misspelt or unknown key fails the
install instead of being ignored, at the top level and inside the chart's
own components (`dbCollector`, `podSecurityContext`,
`containerSecurityContext`, `integrations`, `prometheusRules`,
`grafanaDashboards`, `backendNetworkPolicies`, `otelCollectorQueue`). The
bundled engines' values (`prometheus`, `grafana`, `loki`, `otelCollector`)
are their charts' own and are checked only as far as those charts check
them.

Source: <https://github.com/gdpais/Heartbeat>
