# Application Observability Architecture

How Heartbeat observes OutSystems applications: where their telemetry comes
from, which fields matter, the metrics derived from it and how requests are
correlated across logs. The decision behind the ingest path is
[ADR 0008](decisions/0008-outsystems-11-log-extraction.md); the operator flow
is in [workflows](workflows.md#application-telemetry-ingestion-outsystems-first).

> **Status: designed, not built.** Nothing on this page runs yet. Field names
> and log types follow OutSystems 11; they are confirmed against the SRE
> team's queries and the logging database DDL before the extractor ships.

## Scope

| Deployment | Ingest path | Status |
| --- | --- | --- |
| OutSystems 11 self-managed (Traditional and Reactive) | The db-collector's `outsystems_logs` kind reads the `oslog_*` tables of the logging database and sends OTLP logs to the OTel Collector | First, phase 2 |
| OutSystems 11 on OutSystems Cloud | Log streaming add-on sends OTLP straight to the OTel Collector | When needed |
| ODC | App Analytics Stream sends OTLP straight to the OTel Collector | When needed |

Data path for self-managed OutSystems 11:

1. The collector runs each enabled log-type query (one `.sql` file per log
   type, see the
   [query contract](../../services/db-collector/internal/probes/outsystems/queries/README.md))
   for each logging-database target, from its watermark to now minus a lag.
2. Each row becomes an OTLP log record: `Instant` is the timestamp, `Message`
   the body, every other column an attribute under its OutSystems name. The
   collector adds `error.fingerprint` to error records.
3. Records go to the OTel Collector, then to Loki. Loki indexes only
   `service.name`, `deployment.environment.name` and `outsystems.log_type`.
4. From the same rows the collector updates the
   [derived metrics](#derived-metrics), which Prometheus scrapes from its
   `/metrics`.
5. The watermark moves only after the batch is delivered.

## OutSystems fields

The extractor and the [request correlation](#request-traces) look these
columns up by name. Other columns pass through unchanged.

| Column | Meaning | In Heartbeat |
| --- | --- | --- |
| `Instant` | When the platform logged the row | Record timestamp; watermark |
| `Application_Name`, `Module_Name` (eSpace) | Owning application and module | `service.name` (application); `Module_Name` attribute and metric label |
| `Request_Key` | One request handled by a front-end server; shared by the rows that request logs, across log types and modules | Request correlation key |
| `Session_Id`, `User_Id` | Platform session and user | Investigation filters; distinct counts for active users |
| `Client_IP` | Caller address | Investigation filter |
| `Tenant_Id` | Multi-tenant apps | Attribute; optional business breakdown |
| `Server` / `Executed_By` | Front-end server that handled the request | `front_end` metric label |
| `Duration` | Elapsed milliseconds for screens, server calls, integrations, extensions and timers | Latency metrics; span duration |
| `Screen`, `Action_Name`, `Entrypoint_Name`, `Endpoint`, `Action` | What ran | `operation` metric label (capped); span name |
| `Error_Id` | Links a row to its error log entry | Outcome `error` |
| `Message`, `Stack` | Error text and stack | Body; `error.fingerprint` |
| `Session_Bytes`, `Viewstate_Bytes` | Traditional web screen state sizes | Size histograms |
| `Should_Have_Run_At` | Timer schedule | Timer delay |

Identifiers are never Loki labels or metric labels. They stay structured
metadata, queryable with `| User_Id="…"`. Whether to hash user IDs and client
IPs is a compliance decision still open in the
[product overview](../product/overview.md#open-questions); the extractor can
hash them before export without changing anything else.

## Derived metrics

The collector computes these from every row it extracts. They are counted when
the row is extracted, so they trail real time by the lag plus the poll interval
(about a minute by default). Labels are bounded: `operation` keeps the 100
busiest values per application and log type and folds the rest into `other`;
`front_end` is the handful of front-end servers. User, session, request and IP
never become labels.

Common labels: `environment`, `application`; `module` and `log_type` where
shown.

### SRE: requests, latency and errors

| Metric | Type | Labels | What it answers |
| --- | --- | --- | --- |
| `heartbeat_outsystems_operation_duration_seconds` | histogram | `module`, `log_type`, `outcome` | Latency percentiles and SLOs per module for screens, server calls (Reactive), exposed APIs and timers |
| `heartbeat_outsystems_operations_total` | counter | `module`, `log_type`, `operation`, `outcome` | Throughput and error rate per screen, server action, endpoint or timer |
| `heartbeat_outsystems_operation_seconds_total` | counter | `module`, `log_type`, `operation` | Mean latency per operation (with the counter above); finds the slow screen inside a slow module |
| `heartbeat_outsystems_integration_duration_seconds` | histogram | `direction` (consume/expose), `type` (REST/SOAP), `outcome` | External dependency latency; a slow partner shows here first |
| `heartbeat_outsystems_errors_total` | counter | `module` | Error rate by module, from the error log |
| `heartbeat_outsystems_error_fingerprints_new_total` | counter | — | Errors never seen before: the first sign of a bad deployment |
| `heartbeat_outsystems_slow_events_total` | counter | `module`, `kind` (sql/extension) | Slow SQL and slow extension calls the platform reports in the general log |
| `heartbeat_outsystems_timer_delay_seconds` | histogram | `operation` | How late timers start against their schedule: scheduler saturation |
| `heartbeat_outsystems_screen_state_bytes` | histogram | `module`, `kind` (session/viewstate) | Traditional web pages whose state size makes them slow or loads front-end memory |

`outcome` is `error` when the row has an `Error_Id` or a severity of `ERROR` or
above, otherwise `success`. Histogram buckets include the latency SLO
thresholds (0.1, 0.25, 0.5, 1, 2, 3, 5, 10, 30, 60 s).

### Infrastructure

| Metric or source | What it answers |
| --- | --- |
| `heartbeat_outsystems_front_end_operations_total{front_end, outcome}` | Load balance and error skew per front-end server: one bad node |
| `heartbeat_outsystems_log_delay_seconds{log_type}`: now minus the newest `Instant` seen, while rows arrive | How far behind the platform's own log service is; a growing delay means its queue is backing up |
| `heartbeat_outsystems_extract_lag_seconds`, `heartbeat_outsystems_extract_rows_total`, `heartbeat_outsystems_export_failures_total`, plus the collector's probe duration, target and freshness metrics | Health of Heartbeat's own extraction |
| The `sqlserver` collector kind on the logging and platform databases | Waits, blocking, storage growth and I/O of the databases OutSystems depends on; the log database's growth and the platform database's blocking are common OutSystems incidents |
| Front-end Windows/IIS hosts | Not collected: it needs an agent on the platform servers (for example `windows_exporter`). Optional, the operator's decision |

### Business

| Metric | What it answers |
| --- | --- |
| `heartbeat_outsystems_active_users{window}` and `heartbeat_outsystems_active_sessions{window}` (gauges, 5m and 1h windows) | How many people use each application now. Daily and monthly actives come from Loki in reports (phase 7), not from Prometheus |
| `heartbeat_outsystems_operations_total` by `operation` | Feature and screen usage: what people actually use |
| `heartbeat_outsystems_integration_duration_seconds_count` by endpoint (via `operations_total`) | Business transaction volume with each partner system |
| `Tenant_Id` (Loki, reports) | Usage per tenant, where apps are multi-tenant; not a metric label unless the tenant count is small |

### SLOs and alerts

Recording rules ([TODO 3.3](../../TODO.md)) turn the metrics above into SLIs per
application: availability as the share of `success` operations, latency as the
share under a threshold from the histogram buckets. Alerts use multiwindow
burn rates (for example 14.4× over 1h and 5m, 6× over 6h and 30m) as static
guardrails; adaptive baselines come in phase 7.

## Request traces

### The gap

APMs such as Dynatrace instrument the .NET processes behind OutSystems: they
see IIS requests, ASP.NET handlers and SQL statements, but not which
OutSystems application, module, screen or action they belong to, and
OutSystems 11 logs carry no trace ID to join them by. The result is traces
that are hard to map to the platform's own concepts.

OutSystems already ties its own logs together: every row a request writes
carries the same `Request_Key`, across log types and modules. Heartbeat can
build request timelines from that key, in OutSystems terms, without any agent.

### Step A: request timeline from logs (with phase 2)

- `Request_Key` is kept as structured metadata on every record.
- A Grafana dashboard shows one request across log types: entry point
  (screen, server call, timer or exposed API) with its duration, the
  integrations and extensions it called, slow SQL, and errors, ordered by
  time.
- Investigations filter by user, session or IP, then open the requests they
  find.

This needs no new storage and proves whether `Request_Key` is reliably
present in each log type.

### Step B: synthesized OTLP traces (after step A)

The extractor turns rows into spans as well as log records:

| Span | From |
| --- | --- |
| Trace ID | A hash of `Request_Key`, so the logs and the trace link both ways without storing a mapping |
| Root span | The entry-point row: screen, server call, timer or exposed API, with its duration |
| Child spans | Integration, extension and slow-SQL rows with the same `Request_Key`, timed from `Instant` and `Duration` |
| Span events | Error rows |
| Attributes | Application, module, operation, front-end, outcome; identifiers as on the logs |
| Span links | Calls from one OutSystems app to another one's exposed API, matched by endpoint and time; marked as inferred |

The OTel Collector's `spanmetrics` connector can then produce the same RED
metrics from spans, and a trace store (Grafana Tempo is the natural fit next
to Loki and Grafana) shows waterfalls with links to the logs and the SQL
Server dashboards for the same time. Adding a trace store is a new component
and needs its own ADR.

Limits, stated in the product: spans exist only where OutSystems logs
something. Fast SQL, internal actions and client-side time do not appear, and
timings are as precise as the platform's millisecond durations. That covers
what SREs triage first (which request, which module, which dependency) without
replacing a code-level profiler.

Not recommended now: OTel .NET auto-instrumentation on the front-end servers.
It needs an agent on the platform servers and produces the same unlabelled
.NET spans an APM does.

## Open questions

Answered from the SRE team's queries and the logging database:

- Is `Instant` the start or the end of the logged operation, per log type?
- Which time zone is `Instant` stored in?
- Does every log type carry `Request_Key`, and does every request write an
  entry-point row (screen or server call), or only some?
- How long does the logging database keep rows (rotation), and how many rows
  per minute does each log type produce at peak? These set `initial_lookback`,
  the batch size and Loki sizing.
