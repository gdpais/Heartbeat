# Configuration Reference

Heartbeat services are configured by one integrations file plus environment
variables. Why config lives in YAML rather than PostgreSQL is explained in
[ADR 0002](../architecture/decisions/0002-runtime-config-in-yaml.md).

Sources of truth:

- Loader and validation: [`internal/config/config.go`](../../internal/config/config.go)
- JSON schema: [`packages/config-schema/src/integrations.schema.json`](../../packages/config-schema/src/integrations.schema.json)
- Deployed config: the `integrations` key of the Helm chart's values
  ([defaults](../../infra/helm/heartbeat/values.yaml), no SQL Server targets),
  overridden per environment by files in [`infra/helm/values/`](../../infra/helm/values/)
- Binary default for running a service outside Kubernetes:
  [`config/integrations.yaml`](../../config/integrations.yaml)

## Config files by environment

The chart renders `integrations` into the `heartbeat-integrations` ConfigMap,
mounted at `/app/config/integrations.yaml`. Helm merges maps across values
files but **replaces lists**, so an environment that changes `collectors`
restates the whole list.

| Environment | Where `integrations` comes from |
| --- | --- |
| kind | Chart defaults (`values.yaml`) plus [`kind.yaml`](../../infra/helm/values/kind.yaml) |
| kind with the SQL Server sandbox | Adds [`sqlserver-dev.yaml`](../../infra/helm/values/sqlserver-dev.yaml) |
| Production | Values in the `heartbeat-deploy` config repository; [`production.example.yaml`](../../infra/helm/values/production.example.yaml) shows the shape |
| Service run directly (`go run`) | `config/integrations.yaml`, or `HEARTBEAT_INTEGRATIONS_PATH` |

Helm validates `integrations` against the integrations schema (embedded in the
chart's `values.schema.json`) before anything is applied, and `make chart-check`
loads each profile's rendered file with the Go loader.

## `integrations.yaml`

### Endpoints

| Key | Required | Notes |
| --- | --- | --- |
| `grafana.base_url` | yes | Absolute URL, as seen from the operator's browser |
| `grafana.dashboard_templates` | no | Map of name → dashboard path, e.g. `sqlserver-overview: /d/sqlserver-overview` |
| `grafana.deep_link_templates` | no | Map of name → path with `${variables}`, e.g. `/d/sqlserver-overview?var-target=${target}` |
| `loki.base_url` | yes | Absolute URL |
| `loki.endpoint` | no | Absolute URL (push API) |
| `alertmanager.base_url` | yes | Absolute URL |
| `alertmanager.endpoint` | no | Absolute URL (alerts API) |
| `opentelemetry.endpoint` | no | Absolute URL of the OTel Collector (OTLP HTTP) |

No URL may embed credentials (`https://user:password@host`): the loader rejects
userinfo in every `base_url`, `endpoint`, dashboard and deep-link template, and
channel `target_ref`, for all four integrations. Put secrets in a
`credential_ref` instead; URLs end up in logs, diagnostics and browser links.
The check also catches templates with `${variables}` in the host and the
forms browsers accept (`http:\\user@host`, `http:user@host`).

### Notification channels

```yaml
notification_channels:
  - id: default-webhook          # required, unique
    channel_type: webhook        # required
    target_ref: http://…         # required
    credential_ref: env/HEARTBEAT_WEBHOOK_TOKEN   # optional secret reference
    config:                      # optional string map
      timeout: 10s
```

### Credential references

`credential_refs` is an optional map of name → secret reference. Every
credential reference in the file (`credential_refs.*`, collector, target and
channel `credential_ref`) must start with `env/`, `kv/` or `secret/`. Values
are never stored in the file.

### Collectors

```yaml
collectors:
  - id: sqlserver-default        # required, unique; stable ID used for reload diffs
    kind: sqlserver              # required; sqlserver is the only implemented kind
    enabled: true
    credential_ref: kv/sqlserver-default   # default for targets without their own
    config:
      environment: local         # default environment label for targets
      scrape_interval: 30s       # Go duration, > 0; default 30s
      target_names: []           # optional subset of targets to run; empty = all
      probes:                    # default probe set for targets without their own
        - name: waits
        - name: blocking
          timeout_ms: 5000       # optional per-probe timeout override
        - name: storage
          query_template: "…"    # optional SQL override of the catalog query
      targets:
        - name: finance-prod     # required, unique within the collector; becomes the `target` label
          environment: prod      # optional; defaults to config.environment
          host: finance-sql.internal   # required
          port: 1433             # required, 1–65535
          database_name: FinanceDB     # initial database
          credential_ref: env/finance-prod   # optional; defaults to the collector's
          probes: []             # optional; defaults to config.probes
```

Built-in probe names: `waits`, `blocking`, `sessions`, `memory_pressure`,
`storage`, `throughput`. See the
[metrics reference](metrics-and-endpoints.md#sql-server-probe-metrics).

Probe timeout defaults to `min(scrape_interval / 2, 10s)`; `timeout_ms` overrides
it and is capped at the interval. A `query_template` override runs with the
same login, so review it with the DBAs; see the
[login permissions](../guides/database-targets.md#collector-login-permissions).

### Validation and reload behavior

- The file is validated on startup and on every reload. Values that fail the
  schema never render; a ConfigMap edited by hand can still be invalid, and is
  then rejected as below.
- An invalid candidate is rejected and the previous config keeps running.
  `POST /admin/config/reload` returns 400 for invalid config and 500 for a
  failed apply.
- Collectors are diffed by `id`; only changed, added or removed collectors are
  restarted. A failed apply is rolled back. If the rollback also fails,
  `runtime_diverged` is reported and `/readyz` returns 503.
- Reload triggers: `SIGHUP`, authenticated `POST /admin/config/reload`, and file
  polling when `HEARTBEAT_CONFIG_WATCH_INTERVAL` is set.
- `GET /admin/config` shows the active config redacted: credential values and
  every notification channel `config` value are masked, and userinfo is
  stripped from URLs.

## Credential resolution

The DB collector resolves a `credential_ref` from an environment variable:

```text
HEARTBEAT_CREDENTIAL_<REF>
```

`<REF>` is the full reference upper-cased, with `/`, `-` and `.` replaced by
`_`. The value must be `username:password`.

| `credential_ref` | Environment variable |
| --- | --- |
| `env/sqlserver-b` | `HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_B` |
| `kv/sqlserver-default` | `HEARTBEAT_CREDENTIAL_KV_SQLSERVER_DEFAULT` |

The variable must be present in the **container's** environment; exporting it
in your host shell does not pass it into Kubernetes. The chart loads every key
of the Secret named by `dbCollector.credentials.existingSecret` as an
environment variable. Changing it requires restarting the collector
(`kubectl rollout restart statefulset/db-collector`).

## Environment variables

### DB collector

| Variable | Default | Purpose |
| --- | --- | --- |
| `HEARTBEAT_DB_COLLECTOR_LISTEN_ADDR` | `:8082` | HTTP listen address |
| `HEARTBEAT_INTEGRATIONS_PATH` | `config/integrations.yaml` | Integrations file path |
| `HEARTBEAT_ADMIN_TOKEN` | unset | Bearer token for `GET /admin/config` and `POST /admin/config/reload`; both answer 401 without it, and startup logs a warning. Surrounding whitespace is ignored |
| `HEARTBEAT_CONFIG_WATCH_INTERVAL` | unset (off) | Go duration; poll the config file for changes (the chart sets `5s`, `dbCollector.configWatchInterval`) |
| `HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE` | `false` | Skip TLS certificate verification. **Local dev container only**; applies to every target. When set, startup logs a warning and `GET /admin/config` lists it under `warnings` |
| `HEARTBEAT_CREDENTIAL_<REF>` | — | Target credentials, see above |

### OTel gateway

| Variable | Default | Purpose |
| --- | --- | --- |
| `HEARTBEAT_OTEL_GATEWAY_LISTEN_ADDR` | `:8083` | HTTP listen address |
| `HEARTBEAT_INTEGRATIONS_PATH` | `config/integrations.yaml` | Integrations file path; the gateway loads it once, with no reload |

### Local development files

| File | Tracked | Purpose |
| --- | --- | --- |
| `.env.sqlserver-dev.example` | yes | Template for the SQL Server sandbox |
| `.env.sqlserver-dev` | no (mode 600) | Random per-machine SA password and collector credential, generated by `make sqlserver-dev-init`; `make sqlserver-dev-up` copies the credential into the `heartbeat-sqlserver-dev-credentials` Secret |
| `infra/helm/values/sqlserver-dev.yaml` | yes | Values that point the collector at the sandbox container |
