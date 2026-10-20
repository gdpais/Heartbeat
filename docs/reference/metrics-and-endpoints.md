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
| Prometheus | 9090 | 15s scrape interval |
| Grafana | 3001 | Local login `admin`/`admin`, anonymous viewer enabled. **Local only.** |
| Alertmanager | 9093 | |
| OpenTelemetry Collector | 4317 (OTLP gRPC), 4318 (OTLP HTTP) | `full` profile |
| Loki | none | In-cluster only (`loki:3100`); query it through Grafana, or `kubectl port-forward svc/loki 3100` |
| SQL Server (dev target only) | 127.0.0.1:11433 | Loopback-bound Docker container; see the [local development guide](../guides/local-development.md#sql-server-sandbox) |

Inside the cluster every component keeps its Compose-era name and port
(`prometheus:9090`, `alertmanager:9093`, `loki:3100`, `otel-collector:4318`,
`db-collector:8082`). PostgreSQL and Redis are not deployed
until a service uses them (ADR 0003).

## DB collector (`:8082`)

| Endpoint | Auth | Behavior |
| --- | --- | --- |
| `GET /metrics` | none | Prometheus exposition: probe metrics and self-observability |
| `GET /healthz` (alias `/healthcheck`) | none | Liveness only; 200 while the process serves HTTP |
| `GET /readyz` | none | 503 until every collector finishes its first cycle, when a collector is failed or crash-looping, when a collector has not completed a cycle within 2× its interval + 10s, or when the runtime diverged after a failed rollback. A monitored database being down does **not** make it unready. The body is only `{"status":"ready"}` or `{"status":"not_ready"}`; the reasons are in `GET /admin/config`. |
| `GET /admin/config` | `Authorization: Bearer $HEARTBEAT_ADMIN_TOKEN` | Diagnostics, see below |
| `POST /admin/config/reload` | `Authorization: Bearer $HEARTBEAT_ADMIN_TOKEN` | 400 invalid config, 500 apply failure, 503 before the first reconcile or while another reload holds the lock. The body includes the error text and the readiness report |

Reload can also be triggered with `SIGHUP` or file polling.

`GET /admin/config` returns:

| Field | Content |
| --- | --- |
| `version`, `loaded_at`, `last_reload_at` | Active config version (SHA-256 of the file) and timestamps |
| `last_reload_err`, `runtime_diverged`, `rollback_err` | Why the last reload was rejected or failed; set until the next successful reload |
| `warnings` | Insecure startup settings, e.g. `sqlserver_trust_server_certificate` |
| `readiness` | What `/readyz` decided and why: `status`, `reasons`, and per collector its phase, cycle age, restarts and targets. Each target has its state, consecutive failures, last success and the raw driver `error` of its last failed cycle, kept while it backs off, which can name the host, port and login but never contains the password |
| `config` | The active config, redacted ([configuration reference](configuration.md#validation-and-reload-behavior)) |

```bash
curl -s -H "Authorization: Bearer local-admin-token" localhost:8082/admin/config | jq .readiness   # kind
```

**Admin authentication.** Both admin endpoints go through one check: exactly
one `Authorization: Bearer <token>` header (scheme case-insensitive), whose
SHA-256 digest is compared in constant time with the digest of
`HEARTBEAT_ADMIN_TOKEN` computed at startup, so neither the token nor its
length leaks through timing. A missing or wrong token gets 401 with a
`WWW-Authenticate: Bearer` challenge before the method is checked (an
authenticated wrong method gets 405). Admin responses are `Cache-Control:
no-store`. With no token configured, both endpoints always answer 401.

**Network access.** In Kubernetes the `db-collector` NetworkPolicy admits only
Prometheus and the configured operator peers to port 8082
([what the chart deploys](../guides/kubernetes-local.md#what-the-chart-deploys)).
Operators reach the admin endpoints with
`kubectl -n heartbeat port-forward pod/db-collector-0 8082`, which
NetworkPolicy does not apply to, or from a listed peer such as a VPN range.

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
| `cpu` | `sys.dm_os_ring_buffers` | `heartbeat_sqlserver_cpu_sql_process_ratio` | gauge | none | 1 |
| `cpu` | `sys.dm_os_ring_buffers` | `heartbeat_sqlserver_cpu_other_process_ratio` | gauge | none | 1 on Windows, none on Linux |
| `buffer_cache` | `sys.dm_os_performance_counters` | `heartbeat_sqlserver_page_life_expectancy_seconds` | gauge | none | 1 |
| `buffer_cache` | `sys.dm_os_performance_counters` | `heartbeat_sqlserver_buffer_cache_hit_ratio` | gauge | none | 1 |
| `file_io` | `sys.dm_io_virtual_file_stats`, `sys.master_files` | `heartbeat_sqlserver_database_file_reads_total`, `heartbeat_sqlserver_database_file_writes_total` | counter | `database_name`, `file_name`, `file_type` | one per database file each |
| `file_io` | `sys.dm_io_virtual_file_stats`, `sys.master_files` | `heartbeat_sqlserver_database_file_read_bytes_total`, `heartbeat_sqlserver_database_file_written_bytes_total` | counter | `database_name`, `file_name`, `file_type` | one per database file each |
| `file_io` | `sys.dm_io_virtual_file_stats`, `sys.master_files` | `heartbeat_sqlserver_database_file_read_stall_seconds_total`, `heartbeat_sqlserver_database_file_write_stall_seconds_total` | counter | `database_name`, `file_name`, `file_type` | one per database file each |

**Counters.** Waits, throughput and file I/O are cumulative in SQL Server (the
`Batch Requests/sec` and `Transactions/sec` performance counters are totals
despite their names), so they are exported as counters with the value SQL
Server reports. Query them with `rate()` or `increase()`, never raw. A SQL
Server restart, a failover, or `DBCC SQLPERF('sys.dm_os_wait_stats', CLEAR)`
lowers the value; `rate()` and `increase()` treat that drop as a counter reset,
so rates stay correct (the increase between the last scrape before the reset
and the reset itself is lost). A failed probe, a target backing off or a
collector restart leaves a gap but no reset: the counter resumes at SQL
Server's value. A wait type appears when its wait time first becomes non-zero,
so its first increase after SQL Server starts is not counted.

The `waits` probe leaves out benign idle waits (system tasks sleeping, queues
waiting for work, timers such as `SLEEP_TASK`, `LAZYWRITER_SLEEP` or
`XE_TIMER_EVENT`), which grow by about a second per second on an idle server
and would dominate top-N panels. The list follows Paul Randal's widely used
wait statistics query and lives in
[`catalog.go`](../../services/db-collector/internal/probes/sqlserver/catalog.go).

**CPU.** SQL Server's scheduler monitor writes one record a minute with the
host's CPU split over that minute: the SQL Server process, other processes,
and idle. The `cpu` probe reads only the newest record, so both ratios (0–1,
of all the CPUs SQL Server sees) change once a minute whatever the scrape
interval, and lag the real load by up to a minute; idle is `1 -` their sum.
SQL Server on Linux reports no idle time (always 0, measured on 2019 and
2022), so `heartbeat_sqlserver_cpu_other_process_ratio` is exported on Windows
only (the probe reads the platform from `@@VERSION`). Nothing is exported in
the first minute after SQL Server starts, nor while the newest record is 3
minutes old or more, so a scheduler monitor that stops writing shows as a gap
rather than a frozen value.

**Buffer cache.** Page life expectancy (seconds) and the buffer cache hit ratio
(0–1) come from the server-wide `Buffer Manager` performance counters, on
default and named instances alike. There is no fixed good page life
expectancy; compare a target with its own baseline. The hit ratio stays close
to 1 on most servers even under memory pressure, so read it together with page
life expectancy and file reads. Both are server-wide: on a server with several
NUMA nodes, a page life expectancy drop on one node can hide in the
`Buffer Manager` value. Per-node values (`Buffer Node` counters) are planned
(TODO §11.2).

**File I/O.** `file_io` exports six counters per database file, labelled like
the `storage` probe's file size: read and write operations, bytes read and
written, and I/O stall seconds (the time SQL Server waited for reads or writes
to complete). Average latency per operation is the stall rate over the
operation rate, e.g.
`rate(heartbeat_sqlserver_database_file_read_stall_seconds_total[5m]) / (rate(heartbeat_sqlserver_database_file_reads_total[5m]) > 0)`,
where `> 0` leaves no value instead of dividing by zero for an idle file. The
counters restart at 0 when SQL Server restarts or a database comes back online,
which `rate()` treats as a reset. A stock instance has about 10 files (60
series); budget 6 series per file, so a server with 100 databases and two files
each exports about 1,200. `storage` and `file_io` label a database whose name
SQL Server does not return (the login lacks `VIEW ANY DATABASE`, or the
database was dropped during the query) `database_id:<id>`.

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
removed collector's series are deleted. One failed probe also fails its whole
target for that cycle (`heartbeat_collector_target_up` is 0), although the
target's other probes still run and export; if it keeps failing, the target
backs off and none of its probes run until the retry. While it backs off
(up to 5m) all its probe series are cleared, so the probes that still work
leave a gap instead of a frozen value; only the
`heartbeat_collector_target_*` series stay. For example, `storage` and
`file_io` read `sys.master_files`, which a long `RESTORE` or schema change
can lock until the probe times out. A probe that returns no rows exports no
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
| `heartbeat_collector_target_login_sysadmin` | gauge | `collector`, `environment`, `target` | 1 if the collector's login for the target is sysadmin-equivalent: a member of `sysadmin`, or holding `CONTROL SERVER` (it should be 0; see [login permissions](../guides/database-targets.md#collector-login-permissions)). Checked when the collector first connects to the target and about every 10 minutes after; absent until the first check, and while SQL Server cannot tell |
| `heartbeat_collector_probe_duration_seconds` | histogram | `collector`, `environment`, `target`, `probe` | Probe execution time, failed and timed-out executions included (an abandoned probe is observed at the time it was abandoned); buckets 5ms, 10ms, 50ms, 100ms, 500ms, 1s, 5s, 10s, 30s, 60s |
| `heartbeat_collector_probe_errors_total` | counter | `collector`, `environment`, `target`, `probe`, `reason` | Failed probe executions. `reason` is one of `timeout` (probe timeout or cycle deadline reached while running, including a probe abandoned because it did not stop within 2s, or a database read or write that timed out), `error` (connection, login, query or decoding error), `not_started` (cycle deadline passed before the probe could start, or an abandoned probe of the target is still running), `panic`. Probes interrupted because the collector is stopping (shutdown or reload) are not counted |
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

Per target, the self-observability series are 4 target gauges plus, per
scheduled probe, 13 histogram series (10 buckets, `+Inf`, sum, count) and 4
error counters: 157 series for a target with the 9 built-in probes. Probe
metric series are listed in the table above.

### OutSystems log extraction

`outsystems_logs` collectors
([configuration](configuration.md#outsystems_logs-collectors)) report these
on the same `/metrics`, labelled `collector`, `environment`, `target` and
`log_type`. Their targets also get the self-observability series above, with
the log type as the `probe` label: one probe execution reads, delivers and
acknowledges up to 10 batches.

| Metric | Type | Meaning |
| --- | --- | --- |
| `heartbeat_outsystems_extract_rows_total` | counter | Rows extracted and accepted by the OTel Collector |
| `heartbeat_outsystems_extract_lag_seconds` | gauge | How far the watermark trails now, in real time: about the lag (the collector's, or the log type's own) plus the poll interval when extraction keeps up; it grows while extraction fails or falls behind |
| `heartbeat_outsystems_export_failures_total` | counter | Batches the OTel Collector did not accept after retries (network errors, 429 and 5xx are retried with backoff, up to 4 attempts within the probe timeout). The watermark stays, so the rows are read again |
| `heartbeat_outsystems_extract_stuck_batches_total` | counter | Cycles that failed because every row of a full batch had the same `Instant`. The log type does not move until `batch_limit` is raised |
| `heartbeat_outsystems_log_delay_seconds` | gauge | Now minus the newest `Instant` extracted, set when rows arrive: how far behind OutSystems' own log service is. Absent until the first row |

The counters are created at 0 when a collector starts and, like the probe
series, deleted when it stops. Error text (contract violations, login and
query errors) is logged and reported per target in `GET /admin/config`.

#### Derived OutSystems metrics

Computed from the rows each batch delivers, labelled `collector`,
`environment` and `application` plus the labels shown. Design, label caps
and the entry-point log types:
[derived metrics](../architecture/application-observability.md#derived-metrics).

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `heartbeat_outsystems_operations_total` | counter | `module`, `log_type`, `operation`, `outcome` | Entry points: screens, Reactive server calls, timers and exposed APIs |
| `heartbeat_outsystems_operation_duration_seconds` | histogram | `module`, `log_type`, `outcome` | Entry-point duration; buckets 0.1 to 60 s |
| `heartbeat_outsystems_operation_seconds_total` | counter | `module`, `log_type`, `operation` | Entry-point seconds, for the mean per operation |
| `heartbeat_outsystems_front_end_operations_total` | counter | `front_end`, `outcome` | Entry points per front-end server |
| `heartbeat_outsystems_integration_duration_seconds` | histogram | `direction`, `type`, `outcome` | Integration calls, consumed and exposed |
| `heartbeat_outsystems_errors_total` | counter | `module` | Error log rows |
| `heartbeat_outsystems_slow_events_total` | counter | `module`, `kind` | Slow SQL and slow extension rows of the general log |
| `heartbeat_outsystems_timer_delay_seconds` | histogram | `operation` | Timer start minus its schedule; buckets 1 s to 1 h |
| `heartbeat_outsystems_screen_state_bytes` | histogram | `module`, `kind` | Traditional web screen session and viewstate size; buckets 1 KiB to 16 MiB |

They are deleted with their collector. Per application, each operation adds
3 counter series (two outcomes and its seconds) and each module, log type and
outcome 13 histogram series (11 buckets, sum, count); `operation` and `module`
are capped at 100 values per application (and log type), so the worst case
is bounded.

### Prometheus scrape jobs and rules

Scrape jobs (`prometheus.scrapeConfigs` in the chart's
[`values.yaml`](../../infra/helm/heartbeat/values.yaml)): `prometheus`,
`otel-collector`, `db-collector`, `alertmanager`, `loki`; the `minimal`
profile drops `otel-collector` and `loki`, which it does not deploy. `db-collector` uses
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
| `heartbeat:outsystems_operations:rate5m`, `heartbeat:outsystems_errors:rate5m` | Recording: OutSystems entry points per second by log type and outcome; error log rows per second by module |
| `heartbeat:outsystems_request_duration_seconds:p95_5m`, `heartbeat:outsystems_integration_duration_seconds:p95_5m` | Recording: p95 request and integration latency |
| `heartbeat:outsystems_request_errors:ratio_rate{5m,30m,1h,6h}`, `heartbeat:outsystems_request_slow:ratio_rate{5m,30m,1h,6h}`, `heartbeat:outsystems_requests:rate{1h,6h}` | Recording: availability and latency (2 s) SLIs per application over screens, Reactive server calls and exposed APIs, and their traffic |
| `OutSystemsErrorBudgetBurnFast`, `OutSystemsLatencyBudgetBurnFast` | Alert (critical): 14.4× burn of a 99.5% availability or 99%-within-2-s objective over 1h and 5m, for applications with at least one request a minute |
| `OutSystemsErrorBudgetBurnSlow`, `OutSystemsLatencyBudgetBurnSlow` | Alert (warning): 6× burn over 6h and 30m |
| `OutSystemsLogExtractionBehind` | Alert (warning): a log type's extraction lag above 30 minutes for 10m; the OutSystems metrics and SLO alerts of that target are stale |
| `HeartbeatServiceDown` | Alert: `up == 0` for the DB collector or OTel Collector |
| `HeartbeatCollectorLoginElevated` | Alert (warning): `heartbeat_collector_target_login_sysadmin == 1` for 15m, the collector's login for a target is sysadmin-equivalent |
| `Watchdog` | Alert: always firing. Alertmanager routes it to the `deadmans-switch` receiver (healthchecks.io in production, ADR 0005), which notifies when it stops arriving |

`files/prometheus/rules/generated/` (in the chart) is loaded but empty; it is
reserved for rules rendered from alert policies. The chart ships all rule files
in the `heartbeat-prometheus-rules` ConfigMap. `make rules-check` validates the
rules and runs their promtool unit tests (`files/prometheus/rules/tests/`). A Go
test fails if a rule, rule test, dashboard, script or metrics doc names a SQL
Server metric the probe catalog does not emit or an OutSystems metric the
collector does not export, or if a rule or dashboard reads a counter without
`rate()` or `increase()`.

## Known gaps

- **Alerts notify no one by default.** Alerts reach Alertmanager, but the
  chart's default receiver has no integrations
  ([ADR 0009](../architecture/decisions/0009-remove-the-otel-gateway.md)).
  Chat and WhatsApp receivers exist only in
  [`production.example.yaml`](../../infra/helm/values/production.example.yaml).
- **Admin token attempts are not rate limited.** The NetworkPolicy limits who
  can try. Tracked in TODO §15.2.
