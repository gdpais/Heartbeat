# Heartbeat DB Collector

`db-collector` is the runtime service that connects to SQL Server targets,
runs configured probes on an interval, and exports the results as Prometheus
metrics.

The current implementation is intentionally narrow:

- SQL Server is the only supported engine.
- Collector desired state comes from `integrations.yaml` (the Helm chart's
  `integrations` values when deployed; `config/integrations.yaml` when run
  directly).
- Probe definitions are implemented as a built-in SQL Server catalog.
- Metrics are exported to Prometheus.
- Structured evidence is produced for blocking/session-style probes, but the
  default wiring currently discards that evidence instead of persisting it.

## Runtime Flow

The implemented path is:

1. `services/db-collector/cmd/db-collector/main.go` starts the app.
2. `internal/app/app.go` loads integration config and creates the shared
   Prometheus registry (with Go runtime, process and per-probe metrics), SQL
   executor, exporter, and collector lifecycle.
3. Each enabled collector becomes a background `Poller`.
4. The `Poller` repeatedly calls `Runner.RunOnce` on the configured scrape
   interval.
5. `Runner` expands the collector into scheduled probes and executes them one
   by one.
6. `SQLExecutor` opens a SQL Server connection, runs the probe query, decodes
   rows into samples, and optionally emits evidence.
7. `PrometheusExporter` records the samples as gauges or counters, as the
   probe's metric descriptors say, exposed on `/metrics`.

## Collector Model

A collector is the unit of runtime configuration defined in
`internal/config/config.go`.

Each collector contains:

- an ID and kind
- an enabled/disabled flag
- a scrape interval
- a collector-level credential reference
- one or more targets
- optional target filtering through `target_names`

The collector lifecycle in `internal/app/lifecycle.go` supports:

- `Start` for new collectors
- `Update` for changed collectors
- `Drain` for graceful shutdown preparation
- `Stop` for full removal

Collectors are reconciled by stable ID, so config reloads can restart only the
collectors that changed.

Reloads are serialized and bounded (30s). Invalid candidates keep the previous
configuration running. A valid candidate is diffed against the collectors that
are actually running, and collectors that are not running (crashed or failed)
are restarted even if their config is unchanged. If applying fails part-way,
the applied steps are rolled back in reverse order; if the rollback also fails,
`runtime_diverged` is set and `/readyz` returns 503 until a later reload
succeeds.

## Failure Isolation And Readiness

- Targets run concurrently (up to 8 per collector); probes within a target run
  one at a time. Each cycle is bounded by the scrape interval, and each probe
  by `min(interval/2, 10s)` unless `timeout_ms` overrides it (capped at the
  interval; at most 25000, a larger value is rejected when the config is
  loaded).
- Every probe also has a hard deadline: if it has not returned 2s after its
  timeout, the cycle deadline or a stop, the collector abandons it, reports a
  `timeout`, and moves on, so a query the SQL Server driver cannot cancel (it
  waits for the server to acknowledge the cancel, which a frozen or vanished
  server never does) never stalls the cycle, other targets, or shutdown. The
  abandoned call finishes in the background and its result is discarded;
  until it returns, the target's probes in that collector are not started
  (`not_started`) and the target counts as failed. The bound is one stuck call
  per collector and target name, not per database: the same database reached
  under another target name (another environment or collector), or a target
  renamed by a reload, can hold one more stuck call each. Stuck calls keep
  their connections, and the pool for one address, database and login holds
  at most 2, so later probes of that database can wait for a connection until
  their own timeout.
  The driver's 30s socket timeout (`connection timeout`, the 25s maximum probe
  timeout plus a 5s margin, so it never cuts a running probe short) bounds
  that wait: a dead connection fails and leaves the pool within about a
  minute. A probe that fails on that socket timeout counts as `timeout`.
- A failing probe only affects its own target. The first failure retries on
  the next cycle; repeated failures back off exponentially (capped at 5m, with
  jitter). Every probe failure is logged as structured JSON with collector,
  target, and probe.
- A failed probe clears its series instead of exporting stale values. While
  a target backs off none of its probes run, so all its probe series are
  cleared, those of the probes that succeeded in the failed cycle included;
  only its `heartbeat_collector_target_*` series stay. A removed collector's
  series are deleted.
- Self-observability series: `heartbeat_collector_target_up`,
  `heartbeat_collector_target_consecutive_failures`,
  `heartbeat_collector_target_last_success_timestamp_seconds`,
  `heartbeat_collector_target_login_sysadmin`,
  `heartbeat_collector_cycle_duration_seconds`, the per-probe
  `heartbeat_collector_probe_duration_seconds` histogram and
  `heartbeat_collector_probe_errors_total` counter (by `reason`: `timeout`,
  `error`, `not_started`, `panic`; created at 0 when the collector starts),
  and Go runtime and process metrics. A stopped collector's probe series are
  deleted with its other series.
- A crashed poller is restarted with backoff (1s doubling to 1m).
- `/healthz` is liveness only. `/readyz` returns 503 before every collector
  has completed its first cycle, when a collector is failed or crash-looping,
  when a collector has not completed a cycle within 2x its interval + 10s, or
  when the runtime diverged after a failed rollback. A monitored database
  being down does not make the pod unready; it shows as a failed target in
  metrics and in the authenticated `GET /admin/config`, which also carries the
  raw driver error of the target's last failed cycle, kept while it backs off.
  The `/readyz` body is only the status.
- The admin endpoints (`GET /admin/config`, `POST /admin/config/reload`) need
  `HEARTBEAT_ADMIN_TOKEN` as a bearer token, compared in constant time, and are
  disabled without it. Startup warns when certificate verification is off
  (`HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE`). Details:
  [endpoints](../../docs/reference/metrics-and-endpoints.md#db-collector-8082).
- SQL Server connections are pooled per target (max 2 open, 10m lifetime);
  idle pools are closed after 15m.
- Shutdown is bounded: HTTP drain 10s, poller stop 20s, then pooled
  connections are closed.

## Probe / Extractor Model

In this codebase, the extractor logic lives in the SQL Server probe catalog:
`internal/probes/sqlserver/catalog.go`.

Each probe definition includes:

- a name
- a category
- a default SQL query template
- a list of metric descriptors

Each metric descriptor tells the runtime how to extract one Prometheus sample
from the query result:

- `ValueColumn` becomes the numeric value; a NULL produces no sample
- `Scale` converts it to the metric's base unit (for example `0.001` for
  milliseconds to seconds, `1024` for KB to bytes); zero means 1
- `Type` is gauge (the zero value) or counter; counters are values SQL Server
  accumulates since startup, exported as reported and read with `rate()`
- `LabelColumns` become Prometheus labels; pick columns with a bounded set of
  values
- `Name` and `Help` define the exported metric

One row can feed several descriptors: `throughput` pivots its two
performance counters into one row with a column per metric. Ratios are
computed in the query and exported as gauges.

The built-in probes, their source views and the metrics they emit are listed
in the [metrics reference](../../docs/reference/metrics-and-endpoints.md#sql-server-probe-metrics).

If a collector overrides `query_template` for a probe, the runtime uses that
SQL instead of the catalog default.

## How Rows Become Metrics

`internal/collectors/runner.go` contains the row decoding path:

- query rows are read into `map[string]any`
- `[]byte` values are normalized to strings
- numeric values are converted with `toFloat64` and multiplied by the
  descriptor's `Scale`
- label keys are taken from the metric descriptor, not from arbitrary columns
- any row that does not contain the configured value column, or holds NULL in
  it, is skipped

`internal/export` then records each probe's samples under its scope. Recording
a known series does not allocate, and scrapes copy the current values under a
short read lock, so a scrape never holds up collection. Samples whose label
set or type differs from the metric's first registration, negative counter
values, and label values that are not valid UTF-8 are rejected and logged.

This means the extractor is schema-driven rather than dynamically inferred.
Only the columns named in the probe catalog contribute to exported metrics.

## Evidence Path

The runtime also produces structured evidence for some probe categories.
Today, evidence is emitted for probes in the `blocking` and `sessions`
categories when at least one row is returned.

Evidence includes:

- `Kind`
- a human-readable `Title`
- metadata such as row count

The default service wiring uses `LoggingEvidenceSink`, which is a no-op sink.
That keeps the collector metrics-only for now while preserving the evidence
API for future alerting or investigation workflows.

## Connection Handling

`internal/connectors/sqlserver/connector.go` owns database access.

Current behavior:

- credentials are resolved through a `CredentialResolver`
- environment-based credentials use the `HEARTBEAT_CREDENTIAL_<REF>` naming
  pattern ([resolution rules](../../docs/reference/configuration.md#credential-resolution))
- the DSN enables TLS by default
- each pool is verified when it is created with one query that also checks
  whether the login is sysadmin-equivalent (`IS_SRVROLEMEMBER('sysadmin')` or
  `HAS_PERMS_BY_NAME(NULL, NULL, 'CONTROL SERVER')`); the check is repeated
  about every 10 minutes by the one `Open` that claims it, without holding the
  pool lock. An elevated or undeterminable login is logged as a warning and
  reported through `Manager.Sysadmin`, which `SQLExecutor` exposes to the
  runner (`SysadminReporter`) for the
  `heartbeat_collector_target_login_sysadmin` health series
- connection errors are scrubbed of the login and password before they are
  wrapped, because go-mssqldb's DSN parse errors quote the whole DSN
- every batch starts with `connector.SessionSettings`
  (`SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW;`) in the same round trip;
  why and how operators see it is in the
  [onboarding guide](../../docs/guides/database-targets.md#what-the-collector-runs-on-the-server)
- probe execution uses a per-probe timeout

## Configuration and Endpoints

- `integrations.yaml` keys, credential resolution and environment variables:
  [configuration reference](../../docs/reference/configuration.md)
- HTTP endpoints and metrics:
  [metrics and endpoints reference](../../docs/reference/metrics-and-endpoints.md#db-collector-8082)
- Adding a monitored database:
  [onboarding guide](../../docs/guides/database-targets.md)

## Extending The Service

To add a new SQL Server probe:

1. Add a `Probe` entry in `internal/probes/sqlserver/catalog.go`.
2. Define the SQL query template.
3. Add one or more `Metric` descriptors with the value column and label
   columns.
4. Add or update tests in `internal/probes/sqlserver/catalog_test.go` and
   `internal/collectors/runner_test.go`.
5. Check the probe against the
   [probe review checklist](../../docs/architecture/database-observability.md#probe-review-checklist)
   and run `make test-sqlserver`.

To add a new collector:

1. Declare it in the environment's `integrations` values (or
   `config/integrations.yaml` when running the binary directly).
2. Ensure the collector kind is `sqlserver`.
3. Provide target credentials through the configured credential reference.
4. Confirm the collector appears under `readiness.collectors` in
   `GET /admin/config` and exports metrics on `/metrics`.

## Related Code

- `services/db-collector/internal/app/app.go`
- `services/db-collector/internal/app/lifecycle.go`
- `services/db-collector/internal/collectors/runner.go`
- `services/db-collector/internal/connectors/sqlserver/connector.go`
- `services/db-collector/internal/export/exporter.go`
- `services/db-collector/internal/metadata/types.go`
- `services/db-collector/internal/probes/sqlserver/catalog.go`
