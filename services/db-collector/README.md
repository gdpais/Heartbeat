# Heartbeat DB Collector

`db-collector` is the runtime service that connects to SQL Server targets,
runs configured probes on an interval, and exports the results as Prometheus
metrics.

The current implementation is intentionally narrow:

- SQL Server is the only supported engine.
- Collector desired state comes from `config/integrations.yaml`.
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

Reloads are serialized. Invalid candidates and synchronous reconciliation errors
preserve the previous active configuration snapshot and record the reload error.
This is not transactional runtime rollback: earlier lifecycle operations may have
succeeded, and pollers start asynchronously. Replacement startup validation and
rollback remain open reliability work. `/readyz` currently reports config/lifecycle
diagnostics with HTTP 200 even when a poller fails; it is not a collection-health
guarantee.

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

The built-in catalog currently includes these probes:

- `waits`
- `blocking`
- `sessions`
- `memory_pressure`
- `storage`
- `throughput`

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
  pattern
- the DSN enables TLS by default
- each connection is pinged before probe execution begins
- probe execution uses a per-probe timeout

## Current Probe Definitions

The built-in SQL Server probes are implemented as read-only queries against
system views:

- `waits` reads `sys.dm_os_wait_stats`
- `blocking` reads `sys.dm_exec_requests`
- `sessions` reads `sys.dm_exec_sessions`
- `memory_pressure` reads `sys.dm_os_performance_counters`
- `storage` reads `sys.master_files`
- `throughput` reads `sys.dm_os_performance_counters`

Each probe maps the returned rowset into one or more Prometheus gauges.

## Configuration

The service reads `config/integrations.yaml` and exposes the active state
through the HTTP endpoints in `internal/app/app.go`.

Key inputs:

- `collectors:` declares enabled collector instances
- each collector declares its targets and probes
- target names can be filtered with `target_names`
- probe-level `timeout_ms` overrides the default timeout derived from the
  scrape interval

## Extending The Service

To add a new SQL Server probe:

1. Add a `Probe` entry in `internal/probes/sqlserver/catalog.go`.
2. Define the SQL query template.
3. Add one or more `Metric` descriptors with the value column and label
   columns.
4. Add or update tests in `internal/probes/sqlserver/catalog_test.go` and
   `internal/collectors/runner_test.go`.

To add a new collector:

1. Declare it in `config/integrations.yaml`.
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
