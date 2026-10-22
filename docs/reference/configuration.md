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

Heartbeat never writes to the observability platform itself: logs go to the
OpenTelemetry Collector, and metrics are scraped from `/metrics`
([ADR 0011](../architecture/decisions/0011-platform-agnostic-telemetry-boundary.md)).
These keys say where the platform's UI and query APIs are, for links and,
later, the API's queries.

| Key | Required | Notes |
| --- | --- | --- |
| `platform` | no | `grafana` (default: Grafana, Prometheus, Loki, Alertmanager) or `generic` (any other platform; `dashboards`, `logs` and `alerting` must then be empty). Anything else is rejected |
| `dashboards.base_url` | no | The platform's UI (Grafana), as seen from the operator's browser |
| `dashboards.dashboard_templates` | no | Map of name → dashboard path, e.g. `sqlserver-overview: /d/sqlserver-overview` |
| `dashboards.deep_link_templates` | no | Map of name → path with `${variables}`, e.g. `/d/sqlserver-overview?var-target=${target}` |
| `logs.base_url` | no | The platform's log query API (Loki) |
| `alerting.base_url` | no | The platform's alert API (Alertmanager) |
| `opentelemetry.endpoint` | for `outsystems_logs` | Absolute base URL of the OTel Collector's OTLP/HTTP receiver, e.g. `http://otel-collector:4318`; logs go to `<endpoint>/v1/logs` |

No URL may embed credentials (`https://user:password@host`): the loader rejects
userinfo in every `base_url`, `endpoint`, dashboard and deep-link template, and
channel `target_ref`, for all four sections. Put secrets in a
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
          max_rows: 1000         # optional; the most rows the probe may return, 1–100000; default 10000
        - name: storage
          query_template: "…"    # optional SQL override of the catalog query: one read-only SELECT
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
within its timeout; only an already-dead connection reaches it.

A probe reads at most `max_rows` rows (default 10000, at most 100000). When
its query returns more, the collector stops reading at the first row past the
limit, cancels the query and fails the probe for that cycle, as for a query
error: the error is logged (`returned more rows than max_rows`), the target is
reported failed, and `heartbeat_collector_probe_errors_total{reason="error"}`
grows ([metrics reference](metrics-and-endpoints.md#collector-self-observability)).
The built-in probes return at most a few hundred rows; the limit stops an
override, or a server with unusually many databases or files, from filling
the collector's memory and the metric series. `max_rows` applies to `sqlserver`
collectors only and is rejected on other kinds.

A `query_template` override runs with the same login and the same session
settings. It must be exactly one read-only `SELECT`, optionally led by `WITH`
(CTEs); the configuration is rejected when it is loaded or reloaded
otherwise, with an error that names the collector, target and probe and the
reason, without quoting the query. Rejected:

- more than one statement: anything after a `;` other than comments or more
  `;`, or a second top-level `SELECT` that does not follow `UNION`, `EXCEPT`
  or `INTERSECT` (T-SQL needs no `;` between statements);
- a first word other than `SELECT` or `WITH`, so a bare procedure name too;
- DML and DDL (`INSERT`, `UPDATE`, `DELETE`, `MERGE`, `TRUNCATE`, `CREATE`,
  `ALTER`, `DROP`, `ADD`), permissions (`GRANT`, `DENY`, `REVOKE`), `EXEC` and
  `EXECUTE`, `SELECT … INTO`, `OPENROWSET`, `OPENQUERY`, `OPENDATASOURCE`,
  `WAITFOR`, `SHUTDOWN`, `DBCC`, `BACKUP`, `RESTORE`, `USE`, and the other
  T-SQL statement keywords: `SET`, `DECLARE`, transactions (`BEGIN`,
  `COMMIT`, `ROLLBACK`, `SAVE`), `KILL`, `RECONFIGURE` and the like (the full
  list is in
  [`query.go`](../../internal/config/query.go)). The query hints
  `MERGE JOIN`, `MERGE UNION`, `USE HINT (…)` and `USE PLAN` are allowed;
- a name starting with `sp_` or `xp_` (system and extended procedures), and an
  `END` without a matching `CASE`;
- unbalanced parentheses, an unterminated string, comment or quoted
  identifier, or a control or Unicode line-separator character other than
  tab, CR and LF.

Keywords inside string literals (`'…'`, `N'…'`), comments (`--`, nested
`/* */`) and quoted identifiers (`[…]`, `"…"`) are ignored, so a column named
`[Backup]` is fine, while `t.Backup` is rejected (a reserved word must be
quoted there too). A statement after a comment, or glued to a number as in
`SELECT 1DROP …`, `SELECT 1.DROP …` or `SELECT 1eDROP …`, is still caught.
The built-in probe and log-type queries pass the same check. It is a safety
net, not the
control: a text check cannot prove a query has no side effects. What keeps
probes read-only is the collector's login, which holds only read grants
([login permissions](../guides/database-targets.md#collector-login-permissions)).
Review every override against the
[probe review checklist](../architecture/database-observability.md#probe-review-checklist)
and with the DBAs.

#### `outsystems_logs` collectors

An `outsystems_logs` collector extracts OutSystems 11 logs from the logging
database and sends them to the OTel Collector as OTLP logs
([ADR 0008](../architecture/decisions/0008-outsystems-11-log-extraction.md)).
It uses the same document as `sqlserver`: `environment`, `credential_ref`,
`target_names`, `targets` (the logging databases) and `probes`. Each probe is
a log type, named after its query file in
[`queries/`](../../services/db-collector/internal/probes/outsystems/queries/README.md)
(`error.sql` is `error`). `query_template` replaces that query for one
collector, under the same contract, and must be one read-only `SELECT` as for
SQL Server probes; `timeout_ms` works as for SQL Server probes. `max_rows`
does not apply: `batch_limit` bounds every read. `scrape_interval` is the poll
interval.

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
| `batch_limit` | `5000` | 1–50000. A full batch is followed at once by the next, up to 10 per cycle. If every row of a full batch has the same `Instant`, the cycle fails and `heartbeat_outsystems_extract_stuck_batches_total` grows: raise `batch_limit`. A batch also ends early at about 8 MiB of row data (below) |
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
  are rejected on other kinds, and `probes[].max_rows` on this one.
- A query reads at most `batch_limit` rows: the collector stops at the first
  row past it and fails the cycle without sending anything, so a
  `query_template` that ignores `@limit` cannot fill the collector's memory.
- A batch stops reading once it holds about 8 MiB of row data, at the next
  row with a new `Instant`, and is then handled as a full batch: rows that
  share an `Instant` are never split. This bounds the collector's memory when
  a backlog of error rows with long stacks builds up, which `batch_limit`
  alone does not; a smaller batch is followed at once by the next.
- The body and every text attribute of a record are cut at 16 KiB, on a
  character boundary and ending in `…[truncated]`, and the record gets
  `outsystems.truncated = true`. `Stack` and `EnvironmentInformation` are
  the usual ones; Loki keeps attributes as structured metadata, 64 KB per
  line by default. `error.fingerprint` is computed from the whole `Message`
  and `Stack`, so it does not change.
- The watermark of each collector, target and log type is a file under
  `HEARTBEAT_STATE_DIR` (`outsystems_logs/<collector>.<target>.<log type>.json`).
  It moves only after the OTel Collector accepted the batch, so delivery is at
  least once. The chart's Collector keeps the batch it accepted on disk (the
  `otelCollectorQueue` volume), so a Collector restart does not lose it, and
  accepts the next one only once Loki has taken it
  ([application observability](../architecture/application-observability.md)).
- A batch the Collector has not accepted by the end of the probe execution
  is not a failure: its export carries on in the background, retried until
  the Collector accepts it, and the log type reads nothing more meanwhile.
  `heartbeat_outsystems_export_blocked` is 1 and the target stays up
  ([metrics](metrics-and-endpoints.md#outsystems-log-extraction)). At most
  64 MiB of row data is held in such batches across all log types; past
  that, a batch reads about 1 MiB.
  Removing a collector leaves its files; a collector re-added with the same
  `id` resumes from them.
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
| `GOMEMLIMIT` | unset | Go's soft memory limit, such as `200MiB`. The chart sets 80% of `dbCollector.resources.limits.memory` (`204MiB` for the default `256Mi`), so the garbage collector works harder before the pod is OOM-killed; `dbCollector.goMemLimit` overrides it, `off` disables it |

### Local development files

| File | Tracked | Purpose |
| --- | --- | --- |
| `.env.sqlserver-dev.example` | yes | Template for the SQL Server sandbox |
| `.env.sqlserver-dev` | no (mode 600) | Random per-machine SA password and least-privilege collector credential (`heartbeat_collector:<password>`), generated by `make sqlserver-dev-init`; `make sqlserver-dev-up` creates that login with `sa` and copies only the collector credential into the `heartbeat-sqlserver-dev-credentials` Secret |
| `infra/helm/values/sqlserver-dev.yaml` | yes | Values that point the collector at the sandbox container |
