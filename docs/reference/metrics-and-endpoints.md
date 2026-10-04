# Metrics and Endpoints Reference

Ports, HTTP endpoints and Prometheus metrics exposed by the local stack and
Heartbeat services. For how the pieces connect, see the
[architecture overview](../architecture/overview.md).

## Local ports (kind)

The kind cluster ([`infra/kind/cluster.yaml`](../../infra/kind/cluster.yaml))
maps fixed NodePorts to these ports on 127.0.0.1 only.

| Service | Host port(s) | Notes |
| --- | --- | --- |
| DB collector | 8082 | Heartbeat service |
| OTel gateway | 8083 | Heartbeat service (`full` profile) |
| Prometheus | 9090 | 15s scrape interval |
| Grafana | 3000 | Local login `admin`/`admin`, anonymous viewer enabled. **Local only.** |
| Alertmanager | 9093 | |
| OpenTelemetry Collector | 4317 (OTLP gRPC), 4318 (OTLP HTTP) | `full` profile |
| Loki | none | In-cluster only (`loki:3100`); query it through Grafana, or `kubectl port-forward svc/loki 3100` |
| SQL Server (dev target only) | 127.0.0.1:11433 | Loopback-bound Docker container; see the [local development guide](../guides/local-development.md#sql-server-sandbox) |

Inside the cluster every component keeps its Compose-era name and port
(`prometheus:9090`, `alertmanager:9093`, `loki:3100`, `otel-collector:4318`,
`otel-gateway:8083`, `db-collector:8082`). PostgreSQL and Redis are not deployed
until a service uses them (ADR 0003).

## DB collector (`:8082`)

| Endpoint | Auth | Behavior |
| --- | --- | --- |
| `GET /metrics` | none | Prometheus exposition: probe metrics and self-observability |
| `GET /healthz` (alias `/healthcheck`) | none | Liveness only; 200 while the process serves HTTP |
| `GET /readyz` | none | 503 until every collector finishes its first cycle, when a collector is failed or crash-looping, when a collector has not completed a cycle within 2× its interval + 10s, or when the runtime diverged after a failed rollback. A monitored database being down does **not** make it unready; the database shows as a failed target in the body. The body never includes raw error text. |
| `GET /admin/config` | **none (known gap)** | Redacted active config and reload status |
| `POST /admin/config/reload` | `Authorization: Bearer $HEARTBEAT_ADMIN_TOKEN` | 401 without a valid token (or when no token is configured), 400 invalid config, 500 apply failure |

Reload can also be triggered with `SIGHUP` or file polling.

## OTel gateway (`:8083`)

| Endpoint | Behavior |
| --- | --- |
| `GET /metrics` | Gateway counters |
| `GET /healthz` | Always 200 `ok` |
| `GET /readyz` | 200 with `config_version` and the configured OTel endpoint |
| `POST /v1/heartbeat/events` | Parses one JSON event, normalizes common field aliases into the [application event contract](../../packages/telemetry-contracts/src/application_event.schema.json), and **returns it to the caller** (202). It is not forwarded anywhere yet. |
| `POST /v1/heartbeat/alerts` | Accepts an Alertmanager webhook payload and counts it (202). Nothing is stored or delivered. |

## Metrics

### SQL Server probe metrics

Every probe metric carries `environment` and `target` labels, plus the probe's
own label columns. Names follow Prometheus conventions: base units (seconds,
bytes) and a `_total` suffix on counters only.

| Probe | Source view | Metric | Type | Extra labels | Series per target |
| --- | --- | --- | --- | --- | --- |
| `waits` | `sys.dm_os_wait_stats` | `heartbeat_sqlserver_wait_seconds_total` | counter | `wait_type` | one per wait type with non-zero wait time, benign idle waits excluded: usually tens to low hundreds, at most the ~1,000 wait types SQL Server defines |
| `blocking` | `sys.dm_exec_requests` | `heartbeat_sqlserver_blocked_requests` | gauge | `blocking_session_id` | one per blocking session; none while nothing is blocked |
| `sessions` | `sys.dm_exec_sessions` | `heartbeat_sqlserver_sessions` | gauge | `status` | one per session status (about 5) |
| `memory_pressure` | `sys.dm_os_performance_counters` | `heartbeat_sqlserver_total_server_memory_bytes` | gauge | none | 1 |
| `storage` | `sys.master_files` | `heartbeat_sqlserver_database_file_size_bytes` | gauge | `database_name`, `file_name`, `file_type` | one per database file; tempdb files report their configured startup size, not their current size |
| `throughput` | `sys.dm_os_performance_counters` | `heartbeat_sqlserver_batch_requests_total` | counter | none | 1 |
| `throughput` | `sys.dm_os_performance_counters` | `heartbeat_sqlserver_transactions_total` | counter | none | 1 (server-wide `_Total`) |

**Counters.** Waits and throughput are cumulative in SQL Server (the
`Batch Requests/sec` and `Transactions/sec` performance counters are totals
despite their names), so they are exported as counters with the value SQL
Server reports. Query them with `rate()` or `increase()`, never raw. A SQL
Server restart, a failover, or `DBCC SQLPERF('sys.dm_os_wait_stats', CLEAR)`
lowers the value; `rate()` and `increase()` treat that drop as a counter reset,
so rates stay correct (the increase between the last scrape before the reset
and the reset itself is lost). A failed probe or collector restart leaves a gap
but no reset: the counter resumes at SQL Server's value. A wait type appears
when its wait time first becomes non-zero, so its first increase after SQL
Server starts is not counted.

The `waits` probe leaves out benign idle waits (system tasks sleeping, queues
waiting for work, timers such as `SLEEP_TASK`, `LAZYWRITER_SLEEP` or
`XE_TIMER_EVENT`), which grow by about a second per second on an idle server
and would dominate top-N panels. The list follows Paul Randal's widely used
wait statistics query and lives in
[`catalog.go`](../../services/db-collector/internal/probes/sqlserver/catalog.go).

A target, identified by environment and name, may be enabled in only one
sqlserver collector: configuration that lists it twice is rejected, because
both collectors would write the same series and double the load on the
database. The exporter also keeps each counter series to a single writer and
logs any other write instead of applying it.

Each catalog metric descriptor sets the type and the unit scale applied to the
query column (milliseconds to seconds, KB or 8 KB pages to bytes). A collector
that overrides a probe's `query_template` must return the same columns in the
same units.

A failed probe clears its series instead of exporting stale values, and a
removed collector's series are deleted. A probe that returns no rows exports no
series: `heartbeat_sqlserver_blocked_requests` is absent, not 0, while nothing
is blocked. Queries that need a zero fall back to targets whose last cycle
succeeded (`heartbeat_collector_target_up == 1`), as the dashboard's Blocked
Requests panel does, so an unreachable target never reads as 0. The catalog lives in
[`catalog.go`](../../services/db-collector/internal/probes/sqlserver/catalog.go).

### Collector self-observability

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `heartbeat_collector_target_up` | gauge | `collector`, `environment`, `target` | 1 if the last cycle for the target succeeded |
| `heartbeat_collector_target_consecutive_failures` | gauge | `collector`, `environment`, `target` | Failed cycles in a row |
| `heartbeat_collector_target_last_success_timestamp_seconds` | gauge | `collector`, `environment`, `target` | Unix time of the last successful cycle; use it for freshness alerts |
| `heartbeat_collector_cycle_duration_seconds` | gauge | `collector` | Duration of the last collection cycle |
| `heartbeat_collector_probe_duration_seconds` | histogram | `collector`, `environment`, `target`, `probe` | Probe execution time, failed and timed-out executions included (an abandoned probe is observed at the time it was abandoned); buckets 5ms, 10ms, 50ms, 100ms, 500ms, 1s, 5s, 10s, 30s, 60s |
| `heartbeat_collector_probe_errors_total` | counter | `collector`, `environment`, `target`, `probe`, `reason` | Failed probe executions. `reason` is one of `timeout` (probe timeout or cycle deadline reached while running, including a probe abandoned because it did not stop within 2s), `error` (connection, login, query or decoding error), `not_started` (cycle deadline passed before the probe could start, or an abandoned probe of the target is still running), `panic`. Probes interrupted because the collector is stopping (shutdown or reload) are not counted |
| `go_*`, `process_*` | | none | Go runtime (goroutines, GC, memory) and process (CPU, resident memory, open file descriptors, start time) metrics of the collector itself |

The probe error counters are created at 0 for every scheduled probe and reason
when a collector starts, so `rate()` and `increase()` see the first error.
Targets skipped during backoff run no probes, so they add neither errors nor
durations; `heartbeat_collector_target_up` and
`heartbeat_collector_target_consecutive_failures` cover them. Error text is
logged, never used as a label. The probe series of a collector are deleted when
it stops and recreated when it starts, so a reload that changes a collector
resets its error counters to 0 (a counter reset for `rate()`) and drops the
series of removed targets and probes.

Per target, the self-observability series are 3 target gauges plus, per
scheduled probe, 13 histogram series (10 buckets, `+Inf`, sum, count) and 4
error counters: 105 series for a target with the 6 built-in probes. Probe
metric series are listed in the table above.

### OTel gateway

| Metric | Meaning |
| --- | --- |
| `heartbeat_otel_gateway_normalized_events_total` | Events normalized by `POST /v1/heartbeat/events` |
| `heartbeat_otel_gateway_alert_deliveries_total` | Alertmanager webhook deliveries accepted |

### Prometheus scrape jobs and rules

Scrape jobs (`prometheus.scrapeConfigs` in the chart's
[`values.yaml`](../../infra/helm/heartbeat/values.yaml)): `prometheus`,
`otel-collector`, `db-collector`, `otel-gateway`, `alertmanager`, `loki`; the
`minimal` profile drops the last three it does not deploy. `db-collector` uses
DNS discovery on the headless `db-collector-headless` Service, which lists the
pod even while it is unready, so the collector's failure metrics are still
scraped. Prometheus sends alerts to `alertmanager:9093`.

Rules ([`heartbeat.rules.yml`](../../infra/helm/heartbeat/files/prometheus/rules/heartbeat.rules.yml)):

| Rule | Type |
| --- | --- |
| `heartbeat:up:count`, `heartbeat:service_up:ratio` | Recording |
| `heartbeat:sqlserver_wait_seconds:rate5m` | Recording: wait seconds per second by `wait_type`, `rate()` of `heartbeat_sqlserver_wait_seconds_total` |
| `heartbeat:sqlserver_blocked_requests:sum` | Recording: blocked requests per target; 0 when the target's last cycle succeeded and nothing is blocked |
| `heartbeat:sqlserver_sessions:sum` | Recording: sessions per target, all statuses |
| `heartbeat:outsystems_events:rate5m` | Recording |
| `HeartbeatServiceDown` | Alert: `up == 0` for the collector, gateway or OTel Collector |
| `Watchdog` | Alert: always firing. Alertmanager routes it to the `deadmans-switch` receiver (healthchecks.io in production, ADR 0005), which notifies when it stops arriving |

`files/prometheus/rules/generated/` (in the chart) is loaded but empty; it is
reserved for rules rendered from alert policies. The chart ships all rule files
in the `heartbeat-prometheus-rules` ConfigMap. `make rules-check` validates the
rules and runs their promtool unit tests (`files/prometheus/rules/tests/`). A Go
test fails if a rule, rule test, dashboard, script or metrics doc names a SQL
Server metric the probe catalog does not emit, or if a rule or dashboard reads
a counter without `rate()` or `increase()`.

## Known gaps

- **Alert delivery stops at the gateway.** Alerts reach Alertmanager, but the
  default receiver is the OTel gateway webhook, which only counts them. Chat
  and WhatsApp receivers exist only in
  [`production.example.yaml`](../../infra/helm/values/production.example.yaml).
- **Diagnostic endpoints.** `GET /admin/config` is unauthenticated. Tracked in
  TODO §2.7.
