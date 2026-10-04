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
   Prometheus registry, SQL executor, exporter, and collector lifecycle.
3. Each enabled collector becomes a background `Poller`.
4. The `Poller` repeatedly calls `Runner.RunOnce` on the configured scrape
   interval.
5. `Runner` expands the collector into scheduled probes and executes them one
   by one.
6. `SQLExecutor` opens a SQL Server connection, runs the probe query, decodes
   rows into samples, and optionally emits evidence.
7. `PrometheusExporter` records the samples as gauges exposed on `/metrics`.

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
  interval).
- A failing probe only affects its own target. The first failure retries on
  the next cycle; repeated failures back off exponentially (capped at 5m, with
  jitter). Every probe failure is logged as structured JSON with collector,
  target, and probe.
- A failed probe clears its series instead of exporting stale values, and a
  removed collector's series are deleted.
- Self-observability series: `heartbeat_collector_target_up`,
  `heartbeat_collector_target_consecutive_failures`,
  `heartbeat_collector_target_last_success_timestamp_seconds`,
  `heartbeat_collector_target_login_sysadmin`, and
  `heartbeat_collector_cycle_duration_seconds`.
- A crashed poller is restarted with backoff (1s doubling to 1m).
- `/healthz` is liveness only. `/readyz` returns 503 before every collector
  has completed its first cycle, when a collector is failed or crash-looping,
  when a collector has not completed a cycle within 2x its interval + 10s, or
  when the runtime diverged after a failed rollback. A monitored database
  being down does not make the pod unready; it shows as a failed target in the
  `/readyz` body and metrics. The body never includes raw error text.
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

- `ValueColumn` becomes the numeric gauge value
- `LabelColumns` become Prometheus labels
- `Name` and `Help` define the exported metric

The built-in probes, their source views and the metrics they emit are listed
in the [metrics reference](../../docs/reference/metrics-and-endpoints.md#sql-server-probe-metrics).

If a collector overrides `query_template` for a probe, the runtime uses that
SQL instead of the catalog default.

## How Rows Become Metrics

`internal/collectors/runner.go` contains the row decoding path:

- query rows are read into `map[string]any`
- `[]byte` values are normalized to strings
- numeric values are converted with `toFloat64`
- label keys are taken from the metric descriptor, not from arbitrary columns
- any row that does not contain the configured value column is skipped

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
- each pool is verified once, when it is created, with
  `SELECT IS_SRVROLEMEMBER('sysadmin')`; a sysadmin login is logged as a
  warning and reported through `Manager.Sysadmin`, which `SQLExecutor` exposes
  to the runner (`SysadminReporter`) for the
  `heartbeat_collector_target_login_sysadmin` health series
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
4. Confirm the collector appears in `/readyz` and exports metrics on
   `/metrics`.

## Related Code

- `services/db-collector/internal/app/app.go`
- `services/db-collector/internal/app/lifecycle.go`
- `services/db-collector/internal/collectors/runner.go`
- `services/db-collector/internal/connectors/sqlserver/connector.go`
- `services/db-collector/internal/export/exporter.go`
- `services/db-collector/internal/metadata/types.go`
- `services/db-collector/internal/probes/sqlserver/catalog.go`
