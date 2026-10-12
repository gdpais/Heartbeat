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
| `opentelemetry.endpoint` | for `outsystems_logs` | Absolute base URL of the OTel Collector's OTLP/HTTP receiver, e.g. `http://otel-collector:4318`; logs go to `<endpoint>/v1/logs` |

No URL may embed credentials (`https://user:password@host`): the loader rejects
userinfo in every `base_url`, `endpoint`, dashboard and deep-link template, and
channel `target_ref`, for all four integrations. Put secrets in a
`credential_ref` instead; URLs end up in logs, diagnostics and browser links.
The check also catches templates with `${variables}` in the host and the
forms browsers accept (`http:\\user@host`, `http:user@host`, tabs or newlines
inside the URL). A `target_ref` is also rejected when it is a scheme-less
`user:password@host`, which tools like curl read as credentials; plain
addresses (`ops@example.com`) and `mailto:` are fine.

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
    kind: sqlserver              # required; sqlserver or outsystems_logs (below)
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
          host: finance-sql.internal   # required; name, IPv4 or bare IPv6, no port
          port: 1433             # required, 1–65535
          database_name: FinanceDB     # initial database
          credential_ref: env/finance-prod   # optional; defaults to the collector's
          probes: []             # optional; defaults to config.probes
```

`host` is a DNS name or IPv4 address (letters, digits, `.`, `-`, `_`) or a bare
IPv6 address such as `2001:db8::5`, without brackets. A port (`db:1433`),
credentials, a path, an instance name (`db\INST`) or whitespace is rejected:
the collector builds the connection URL from it, and an unparsable URL would
put the password into the driver's error.

Built-in probe names: `waits`, `blocking`, `sessions`, `memory_pressure`,
`storage`, `throughput`, `cpu`, `buffer_cache`, `file_io`. See the
[metrics reference](metrics-and-endpoints.md#sql-server-probe-metrics).

Probe timeout defaults to `min(scrape_interval / 2, 10s)`; `timeout_ms` overrides
it and is capped at the interval. `timeout_ms` may not exceed 25000 (25s): a
larger value is rejected when the configuration is loaded or reloaded. A probe
that has not stopped 2s after its timeout is abandoned and fails as a timeout.
Every database read also gives up after 30s, the 25s maximum plus a margin for
the cancel round trip, so the driver never cuts off a probe that is still
within its timeout; only an already-dead connection reaches it. A
`query_template` override runs with the same login and the same session
settings, and must be a single `SELECT` query, not a procedure call; review it
against the
[probe review checklist](../architecture/database-observability.md#probe-review-checklist)
and with the DBAs; see the
[login permissions](../guides/database-targets.md#collector-login-permissions).

#### `outsystems_logs` collectors

An `outsystems_logs` collector extracts OutSystems 11 logs from the logging
database and sends them to the OTel Collector as OTLP logs
([ADR 0008](../architecture/decisions/0008-outsystems-11-log-extraction.md)).
It uses the same document as `sqlserver`: `environment`, `credential_ref`,
`target_names`, `targets` (the logging databases) and `probes`. Each probe is
a log type, named after its query file in
[`queries/`](../../services/db-collector/internal/probes/outsystems/queries/README.md)
(`error.sql` is `error`). `query_template` replaces that query for one
collector, under the same contract; `timeout_ms` works as for SQL Server
probes. `scrape_interval` is the poll interval.

```yaml
collectors:
  - id: outsystems-prod
    kind: outsystems_logs
    enabled: true
    credential_ref: env/oslog-prod
    config:
      environment: prod
      scrape_interval: 30s     # poll interval; default 30s
      lag: 60s                 # read up to now minus lag; default 60s
      batch_limit: 5000        # rows per query, 1–50000; default 5000
      initial_lookback: 15m    # where a log type without a watermark starts; default 15m
      log_timezone: UTC        # IANA time zone Instant is stored in; default UTC
      probes:
        - name: error
        - name: cyclic_job
          lag: 25m             # this log type's lag: Instant is the timer's start
      targets:
        - name: oslogging-prod
          host: oslog-sql.internal
          port: 1433
          database_name: OSLogging
```

| Setting | Default | Notes |
| --- | --- | --- |
| `lag` | `60s` | Go duration, > 0. Rows the log service commits later than this after their `Instant` are missed |
| `probes[].lag` | the collector's `lag` | Go duration, > 0. Replaces `lag` for one log type. A `screen` or `cyclic_job` row has the operation's start as `Instant` and is written when it ends, so set it above the longest screen request or timer (the timer timeout, 20 minutes by default) |
| `batch_limit` | `5000` | 1–50000. A full batch is followed at once by the next, up to 10 per cycle. If every row of a full batch has the same `Instant`, the cycle fails and `heartbeat_outsystems_extract_stuck_batches_total` grows: raise `batch_limit` |
| `initial_lookback` | `15m` | Go duration, > 0. Also used when the stored watermark is unreadable or belongs to another database |
| `log_timezone` | `UTC` | IANA name such as `Europe/Lisbon`; `Local` is rejected. With a zone that has daylight saving time, rows logged in the hour that repeats when clocks go back can be missed: `Instant` itself is ambiguous then |

Rules:

- `opentelemetry.endpoint` is required while the collector is enabled.
  Changing it restarts every `outsystems_logs` collector.
- A log type with no query file is rejected when the file is loaded or
  reloaded, and so is a log type listed twice for one target (each has one
  watermark).
- A log type of one logging database (same host, port and database name,
  compared case-insensitively) can be extracted by one enabled collector
  only; two would send every row twice.
- `lag`, `batch_limit`, `initial_lookback`, `log_timezone` and `probes[].lag`
  are rejected on other kinds.
- The watermark of each collector, target and log type is a file under
  `HEARTBEAT_STATE_DIR` (`outsystems_logs/<collector>.<target>.<log type>.json`).
  It moves only after the OTel Collector accepted the batch, so delivery is at
  least once. Removing a collector leaves its files; a collector re-added with
  the same `id` resumes from them.
- The login needs `SELECT` on the `oslog_*` views the queries read (the
  current and the previous week's) and nothing else; see
  [database targets](../guides/database-targets.md#outsystems-logging-database).

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
| `HEARTBEAT_STATE_DIR` | `/var/lib/heartbeat` | Writable directory for state that must survive a restart: the `outsystems_logs` watermarks. Created on the first save; the `sqlserver` kind never uses it. The chart mounts the `dbCollector.persistence` volume there |
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
| `.env.sqlserver-dev` | no (mode 600) | Random per-machine SA password and least-privilege collector credential (`heartbeat_collector:<password>`), generated by `make sqlserver-dev-init`; `make sqlserver-dev-up` creates that login with `sa` and copies only the collector credential into the `heartbeat-sqlserver-dev-credentials` Secret |
| `infra/helm/values/sqlserver-dev.yaml` | yes | Values that point the collector at the sandbox container |
