# Database Observability Architecture

## Scope
- SQL Server is the first DB engine in scope.
- Oracle is the intended future monitored engine; PostgreSQL is Heartbeat's metadata store, not a monitoring target in the current scope.
- PostgreSQL can store target metadata, probe definitions, and probe assignments for future control-plane workflows.
- Active collector runtime desired state is not read from PostgreSQL for MVP.

## SQL Server signal coverage

Target signal set for the MVP, compared with what the built-in probe catalog
emits today. Metric names and labels are listed in the
[metrics reference](../reference/metrics-and-endpoints.md#sql-server-probe-metrics).

| Signal | Status | Current probe / metric |
| --- | --- | --- |
| Wait statistics | Collected | `waits` → `heartbeat_sqlserver_wait_time_ms` |
| Blocking and locks | Collected (blocked requests) | `blocking` → `heartbeat_sqlserver_blocked_requests` |
| Sessions and connections | Collected | `sessions` → `heartbeat_sqlserver_sessions` |
| Memory pressure | Collected | `memory_pressure` → `heartbeat_sqlserver_memory_kb` |
| Database size / file size | Collected | `storage` → `heartbeat_sqlserver_database_file_size_mb` |
| Throughput counters (batch requests, transactions) | Collected | `throughput` → `heartbeat_sqlserver_throughput` |
| Instance availability | Partial | `heartbeat_collector_target_up` (collector reachability) |
| CPU pressure | Planned | — |
| Buffer/cache hit ratio | Planned | — |
| Physical and logical reads/writes per second, I/O, IOPS | Planned | — |
| Query latency | Planned | — |
| Rollbacks, user transactions | Planned | — |
| Free space and growth trends | Planned | — |
| TempDB pressure | Planned | — |
| Error events | Planned | — |

Output expectations: stable metric names; labels for environment, target,
database, instance and, where safe, application; dashboards for overview,
waits/locks, sessions, storage and regressions. Only
`infra/helm/heartbeat/files/grafana/dashboards/sqlserver-overview.json` exists so far.

## Safety rules
- Credentials are referenced by `credential_ref` only.
- Production probes must be non-blocking.
- Probe definitions are versioned and can be disabled instead of deleted.
- Runtime collector grouping, target activation, probe activation, and scaling live in `config/integrations.yaml` and Kubernetes delivery.

## Collector recovery and high availability (planned)

Status: items 1-3 are implemented for a single collector replica (failure
isolation, backoff, freshness metrics, stale-series cleanup, readiness, reload
rollback); see the [DB collector README](../../services/db-collector/README.md). Items 4-5 remain planned.

The remaining items are planned improvements, not guarantees of the current runtime. The
current SQL path is remote queries -> custom collector's in-memory metrics ->
Prometheus scrape/storage -> Grafana. There is no separate custom loader or
durable collector-side sample queue. PostgreSQL and Redis do not buffer this
metric stream.

Implement recovery before adding replicas:

1. Isolate probe and target failures. A failed query must not permanently stop
   unrelated collection. Retry transient failures with bounded exponential
   backoff and jitter, enforce query/concurrency budgets, and surface persistent
   credential/configuration failures without creating retry storms.
2. Expose per-target/probe success, failures, last successful collection time,
   and freshness. Remove or expire stale series, including series belonging to
   removed targets. An HTTP endpoint being reachable is not proof that SQL
   collection is healthy.
3. Make readiness reflect expected collector state. Define degraded behavior
   separately from process liveness: an unavailable database should not cause
   endless container restarts. Add deployment probes and recovery policies for
   actual process failures, and test reload failure/rollback behavior before
   claiming that the previous working runtime is preserved.
4. Design target ownership and takeover across replicas. Evaluate active/standby
   ownership or sharding with reassignment; use leases plus fencing, or an
   equivalent mechanism, to limit duplicate SQL polling during partitions and
   takeover. Increasing replica count alone is not an HA design. Account for
   rolling updates, node placement, takeover delay, and aggregate database load.
5. Define downstream resilience independently. Agree recovery-time and acceptable
   data-gap objectives, then choose storage availability and any durable
   buffering/replay mechanism needed to meet them. Specify retention, disk
   limits, backpressure, replay ordering, and duplicate handling. Buffering can
   preserve already-collected samples; it cannot reconstruct observations missed
   while collection was stopped.

Validate with non-production target outages, slow queries, collector/process and
node loss, network partitions, configuration reloads during failure, and
Prometheus/downstream outages. Record recovery time, data gaps, stale or duplicate
series, effects on healthy targets, and database load. Keep these reliability
improvements in the core hardening phase; they do not depend on the later Alloy
evaluation.

## Custom collector and Alloy comparison (late roadmap)

The agreed plan is a parallel evaluation, not a decision to replace the custom
collector. Schedule it near the end of the roadmap, after the core monitoring
workflow and reliability baseline are validated. Perform the experiment in a
dedicated comparison branch created at that time; do not mix it with ordinary
collector fixes or introduce Alloy into production as part of planning.

Run the custom collector and Grafana Alloy against equivalent non-production
SQL Server workloads. Isolate their metric identities or storage destinations so
dashboards, recording rules, and alerts cannot accidentally double-count the
same database. Budget the combined polling load; use controlled sequential runs
as well when concurrent collection would distort performance measurements.

Use the same workload, intervals, permissions, and comparable queries wherever
possible. Compare:

- Signal coverage across waits, blocking, sessions, memory, storage, and
  throughput; units, counter/gauge semantics, labels, and cardinality.
- Custom query support, structured investigation evidence, and integration with
  Heartbeat's target/probe configuration and operator workflows.
- Database overhead, collector CPU/memory, connection limits, query timeouts,
  least privilege, credentials, and TLS behavior.
- Reload behavior, error isolation, retries, target ownership/takeover, freshness,
  and behavior during collector, node, network, and downstream outages.
- Buffering/replay limits, recovery time, gaps, duplicates, deployment complexity,
  upgrades, diagnostics, and ongoing maintenance cost.
- SQL Server fit now and Oracle extensibility later, assessed separately rather
  than assuming equivalent engine support.

Record versions/configurations, reproducible tests, measurements, coverage gaps,
and operational tradeoffs in a decision record on the comparison branch. Verify
Alloy's capabilities at evaluation time. The outcome may be to retain the custom
collector, use Alloy for suitable workloads, or keep both available for distinct
needs. Supporting both requires a shared telemetry contract and explicit
per-target/backend selection, ownership, and duplicate-prevention rules; it does
not imply routinely polling every production target twice. Review the evidence
before merging any selected integration or planning production adoption.

## Metadata model

The planned control-plane tables (`database_targets`, `probe_definitions`,
`probe_assignments`) and the deferred asset/topology extension are described in
the [data model](data-model.md#database-observability).
