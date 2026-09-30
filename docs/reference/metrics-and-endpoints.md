# Metrics and Endpoints Reference

Ports, HTTP endpoints and Prometheus metrics exposed by the local stack and
Heartbeat services. For how the pieces connect, see the
[architecture overview](../architecture/overview.md).

## Local ports (Docker Compose)

| Service | Host port(s) | Notes |
| --- | --- | --- |
| DB collector | 8082 | Heartbeat service |
| OTel gateway | 8083 | Heartbeat service |
| Prometheus | 9090 | 15s scrape interval |
| Grafana | 3000 | Local login `admin`/`admin`, anonymous viewer enabled. **Local only.** |
| Loki | 3100 | |
| Alertmanager | 9093 | |
| OpenTelemetry Collector | 4317 (OTLP gRPC), 4318 (OTLP HTTP), 8889 (Prometheus export), 13133 (health) | |
| PostgreSQL | 5432 | Schema only; no service uses it yet |
| Redis | 6379 | No service uses it yet |
| SQL Server (dev overlay only) | 127.0.0.1:11433 | Loopback-bound; see the [local development guide](../guides/local-development.md) |

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
own label columns. All are exported as gauges. `waits` values are cumulative
counters from SQL Server but are exported as gauges; see
[known gaps](#known-gaps).

| Probe | Source view | Metric | Extra labels |
| --- | --- | --- | --- |
| `waits` | `sys.dm_os_wait_stats` | `heartbeat_sqlserver_wait_time_ms` | `wait_type` |
| `blocking` | `sys.dm_exec_requests` | `heartbeat_sqlserver_blocked_requests` | `blocking_session_id` |
| `sessions` | `sys.dm_exec_sessions` | `heartbeat_sqlserver_sessions` | `status` |
| `memory_pressure` | `sys.dm_os_performance_counters` | `heartbeat_sqlserver_memory_kb` | `metric` |
| `storage` | `sys.master_files` | `heartbeat_sqlserver_database_file_size_mb` | `database_name`, `file_name`, `file_type` |
| `throughput` | `sys.dm_os_performance_counters` | `heartbeat_sqlserver_throughput` | `counter_name` |

A failed probe clears its series instead of exporting stale values, and a
removed collector's series are deleted. A probe that returns no rows exports no
series: `heartbeat_sqlserver_blocked_requests` is absent, not 0, while nothing
is blocked. Queries that need a zero fall back to targets whose last cycle
succeeded (`heartbeat_collector_target_up == 1`), as the dashboard's Blocked
Requests panel does, so an unreachable target never reads as 0. The catalog lives in
[`catalog.go`](../../services/db-collector/internal/probes/sqlserver/catalog.go).

### Collector self-observability

| Metric | Labels | Meaning |
| --- | --- | --- |
| `heartbeat_collector_target_up` | `collector`, `environment`, `target` | 1 if the last cycle for the target succeeded |
| `heartbeat_collector_target_consecutive_failures` | `collector`, `environment`, `target` | Failed cycles in a row |
| `heartbeat_collector_target_last_success_timestamp_seconds` | `collector`, `environment`, `target` | Unix time of the last successful cycle; use it for freshness alerts |
| `heartbeat_collector_cycle_duration_seconds` | `collector` | Duration of the last collection cycle |

### OTel gateway

| Metric | Meaning |
| --- | --- |
| `heartbeat_otel_gateway_normalized_events_total` | Events normalized by `POST /v1/heartbeat/events` |
| `heartbeat_otel_gateway_alert_deliveries_total` | Alertmanager webhook deliveries accepted |

### Prometheus scrape jobs and rules

Scrape jobs ([`prometheus.yml`](../../infra/prometheus/prometheus.yml)):
`prometheus`, `otel-collector`, `db-collector`, `otel-gateway`, `alertmanager`,
`loki`.

Rules ([`heartbeat.rules.yml`](../../infra/prometheus/rules/heartbeat.rules.yml)):

| Rule | Type |
| --- | --- |
| `heartbeat:up:count`, `heartbeat:service_up:ratio` | Recording |
| `heartbeat:sqlserver_wait_seconds:rate5m` | Recording: wait seconds per second by `wait_type`, from `heartbeat_sqlserver_wait_time_ms` |
| `heartbeat:sqlserver_blocked_requests:sum` | Recording: blocked requests per target; 0 when the target's last cycle succeeded and nothing is blocked |
| `heartbeat:sqlserver_sessions:sum` | Recording: sessions per target, all statuses |
| `heartbeat:outsystems_events:rate5m` | Recording |
| `HeartbeatServiceDown` | Alert: `up == 0` for the collector, gateway or OTel Collector |

`infra/prometheus/rules/generated/` is loaded but empty; it is reserved for
rules rendered from alert policies. `make rules-check` validates the rules and
runs their promtool unit tests (`infra/prometheus/rules/tests/`), and a Go test
fails if a rule or dashboard references a SQL Server metric the probe catalog
does not emit.

## Known gaps

- **Counter semantics.** Cumulative SQL Server values are exported as gauges:
  `heartbeat_sqlserver_wait_time_ms`, and the `Batch Requests/sec` and
  `Transactions/sec` values of `heartbeat_sqlserver_throughput` (cumulative
  despite their names). `rate()` still handles SQL Server restarts as counter
  resets, and the wait recording rule relies on that, but the metric type is
  wrong for tooling. The dashboard's Top Wait Types and Throughput panels show
  raw cumulative values rather than rates. Tracked in TODO §15.0.
- **No alert delivery.** Prometheus has no `alerting` block, so alerts do not
  reach Alertmanager.
- **Diagnostic endpoints.** `GET /admin/config` is unauthenticated. Tracked in
  TODO §15.2.
