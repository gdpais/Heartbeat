# Application Observability Architecture

How Heartbeat observes OutSystems applications: where their telemetry comes
from, which fields matter, the metrics derived from it and how requests are
correlated across logs. The decision behind the ingest path is
[ADR 0008](decisions/0008-outsystems-11-log-extraction.md); the operator flow
is in [workflows](workflows.md#application-telemetry-ingestion-outsystems-first).

> **Status: partly built.** Extraction to the OTel Collector and the derived
> metrics (data path steps 1, 2, 4 and 5) run as the db-collector's
> `outsystems_logs` kind, switched off by default
> ([configuration](../reference/configuration.md#outsystems_logs-collectors));
> the chart ships their recording rules and SLO alerts. The Loki index label
> `outsystems.log_type` and request traces are designed, not built. Field names and log types follow
> OutSystems 11; they are confirmed against the SRE team's queries and the
> logging database DDL before the queries are reviewed.

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
| `Instant` | When the screen request or timer **started** (`screen`, `cyclic_job`), when the integration or mobile request **completed** (`integration`, `mobile_request`), or when the row was logged (`error`, `general`, `extension`, `request_event`), in the platform servers' time zone | Record timestamp; watermark. Start-stamped types need their own `lag` |
| `Application_Name`, `Espace_Name` | Owning application and module | `service.name` (application); `Espace_Name` attribute and metric label |
| `Module_Name` (error and general logs) | The free text the row was logged with; `SLOWSQL` marks the platform's slow-query messages | Attribute; selects slow-SQL rows |
| `Request_Key` | One request handled by a front-end server; shared by the rows that request logs, across log types and modules | Request correlation key |
| `Session_Id`, `User_Id`, `Username` | Platform session and user (`Login_Id` on mobile requests is returned as `Session_Id`) | Investigation filters; distinct counts for active users |
| `Client_IP` | Caller address (`Source` on mobile requests and exposed integrations is returned as `Client_IP`) | Investigation filter |
| `Tenant_Id` | Multi-tenant apps | Attribute; optional business breakdown |
| `Server` | Front-end server that handled the request (`Executed_By` on most views is returned as `Server`) | `front_end` metric label |
| `Duration` | Elapsed time of screens, server calls, integrations, extensions, timers and slow queries, in milliseconds (the queries convert the log types the platform logs in seconds and parse the `SLOWSQL` message) | Latency metrics; span duration |
| `Screen`, `Action_Name` | The entry point and the operation that ran; the queries map each view's own column (`Entrypoint_Name`, `Endpoint`, `Action`, `Cyclic_Job_Name`, the event name) to these two | `operation` metric label (capped); span name |
| `Error_Id` | Links a row to its error log entry | Outcome `error` |
| `Message`, `Stack` | Error text and stack | Body; `error.fingerprint` |
| `Session_Bytes`, `Viewstate_Bytes` | Traditional web screen state sizes | Size histograms |
| `Endpoint`, `Type`, `Is_Expose` | Integration URL, protocol and direction | Attributes; dependency breakdown |
| `Should_Have_Run_At` | Timer schedule | Timer delay |

The mapping from each view's own columns to these names is the
[standard columns](../../services/db-collector/internal/probes/outsystems/queries/README.md#standard-columns)
table of the queries README.

Identifiers are never Loki labels or metric labels. They stay structured
metadata, queryable with `| User_Id="…"`. Whether to hash user IDs and client
IPs is a compliance decision still open in the
[product overview](../product/overview.md#open-questions); the extractor can
hash them before export without changing anything else.

## Derived metrics

The collector computes these from every row it delivers to the OTel
Collector and exposes them on its `/metrics`
([code](../../services/db-collector/internal/collectors/outsystems/derived.go)).
They are counted when the row is extracted, so they trail real time by the
lag plus the poll interval (about a minute by default), and a batch delivered
twice (a crash between delivery and the watermark save) is counted twice.

Every metric has the labels `collector`, `environment` and `application`
(the record's `service.name`), plus those shown. Labels are capped so a name
the platform invents cannot grow the series without bound: the first 100
values of `module` per application and of `operation` per application and log
type keep their own series, later ones are counted as `other`; `front_end`
is capped at 50 and integration `type` at 20 per collector, `application` at
1,000. The kept values are the first seen since the collector started, so a
restart can keep different ones. User, session, request and IP never become
labels.

Entry points are the rows that stand for one request or timer run: `screen`
(Traditional and Reactive screens; `operation` is the screen),
`mobile_request` (Reactive server calls; the endpoint), `cyclic_job` (the
timer) and `integration` rows of exposed APIs (the API action).

### SRE: requests, latency and errors

| Metric | Type | Labels | What it answers |
| --- | --- | --- | --- |
| `heartbeat_outsystems_operation_duration_seconds` | histogram | `module`, `log_type`, `outcome` | Latency percentiles and SLOs per module for entry points |
| `heartbeat_outsystems_operations_total` | counter | `module`, `log_type`, `operation`, `outcome` | Throughput and error rate per screen, server call, exposed API or timer |
| `heartbeat_outsystems_operation_seconds_total` | counter | `module`, `log_type`, `operation` | Mean latency per operation (with the counter above); finds the slow screen inside a slow module |
| `heartbeat_outsystems_integration_duration_seconds` | histogram | `direction` (consume/expose), `type` (REST/SOAP), `outcome` | External dependency latency; a slow partner shows here first |
| `heartbeat_outsystems_errors_total` | counter | `module` | Error rate by module, from the error log |
| `heartbeat_outsystems_slow_events_total` | counter | `module`, `kind` (sql/extension) | Slow SQL (`SLOWSQL`) and slow extension (`SLOWEXTENSION`) rows the platform writes to the general log |
| `heartbeat_outsystems_timer_delay_seconds` | histogram | `operation` | How late timers start against `Should_Have_Run_At`: scheduler saturation |
| `heartbeat_outsystems_screen_state_bytes` | histogram | `module`, `kind` (session/viewstate) | Traditional web pages whose state size makes them slow or loads front-end memory |

`outcome` is `error` when the row has an `Error_Id` or a severity of `ERROR` or
above, otherwise `success`; both series exist from an entry point's first
row, so an error ratio reads 0 rather than nothing. Rows without a usable
`Duration` (NULL or negative) are counted but not timed. Latency buckets
include the SLO thresholds (0.1, 0.25, 0.5, 1, 2, 3, 5, 10, 30, 60 s).

### Infrastructure

| Metric or source | What it answers |
| --- | --- |
| `heartbeat_outsystems_front_end_operations_total{front_end, outcome}` | Load balance and error skew per front-end server: one bad node |
| `heartbeat_outsystems_log_delay_seconds{log_type}`: now minus the newest `Instant` seen, while rows arrive | How far behind the platform's own log service is; a growing delay means its queue is backing up |
| `heartbeat_outsystems_extract_lag_seconds`, `heartbeat_outsystems_extract_rows_total`, `heartbeat_outsystems_export_failures_total`, plus the collector's probe duration, target and freshness metrics | Health of Heartbeat's own extraction |
| The `sqlserver` collector kind on the logging and platform databases | Waits, blocking, storage growth and I/O of the databases OutSystems depends on; the log database's growth and the platform database's blocking are common OutSystems incidents |
| Front-end Windows/IIS hosts | Not collected: it needs an agent on the platform servers (for example `windows_exporter`). Optional, the operator's decision |

### Business

| Source | What it answers |
| --- | --- |
| Distinct `User_Id` and `Session_Id` per application over 5m and 1h, from Loki | How many people use each application now. Daily and monthly actives come from Loki in reports (phase 7) |
| `heartbeat_outsystems_operations_total` by `operation` | Feature and screen usage: what people actually use |
| `heartbeat_outsystems_integration_duration_seconds_count` by `direction` and `type`; per endpoint from Loki | Business transaction volume with partner systems |
| `Tenant_Id` (Loki, reports) | Usage per tenant, where apps are multi-tenant; not a metric label unless the tenant count is small |

### Not built, on purpose

- **Active users and sessions as gauges.** A distinct count needs every
  identifier of the window in the collector's memory; it resets on a restart
  and would double when collectors run as replicas (phase 8). Loki counts
  them correctly from the structured metadata already sent, so dashboards
  query Loki instead.
- **New error fingerprints.** "Never seen before" needs a persisted,
  bounded set of fingerprints, or every restart reports all errors as new and
  looks like a bad deployment. It waits until a dashboard or alert needs it;
  until then `error.fingerprint` is on every error record in Loki.

### SLOs and alerts

Recording rules in
[`heartbeat.rules.yml`](../../infra/helm/heartbeat/files/prometheus/rules/heartbeat.rules.yml)
turn the metrics into SLIs per environment and application, over
user-facing requests (screens, Reactive server calls and exposed APIs;
timers are left out):

- Availability: the share of requests with `outcome="success"`
  (`heartbeat:outsystems_request_errors:ratio_rate<window>`).
- Latency: the share of requests answered within 2 s, from the histogram's
  `le="2"` bucket (`heartbeat:outsystems_request_slow:ratio_rate<window>`).

Alerts use multiwindow burn rates against static default objectives, 99.5%
available and 99% within 2 s over 30 days: fast burn (14.4× over 1h and 5m)
is `critical`, slow burn (6× over 6h and 30m) is `warning`. Applications with
less than one request a minute over the long window are left out. Because the
SLIs go quiet, not red, when extraction stops, `OutSystemsLogExtractionBehind`
fires when a log type's extraction lag passes 30 minutes. These are
guardrails; per-application objectives and adaptive baselines come in
phase 7.

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
present in each log type. Until the dashboard exists,
[`scripts/outsystems-log-queries.sql`](../../scripts/outsystems-log-queries.sql)
holds the SQL the SRE team runs against the logging database for the same
timelines.

### Step B: synthesized OTLP traces (after step A)

The extractor turns rows into spans as well as log records:

| Span | From |
| --- | --- |
| Trace ID | A hash of `Request_Key`, so the logs and the trace link both ways without storing a mapping |
| Root span | The entry-point row: screen, server call, timer or exposed API, with its duration |
| Child spans | Integration, extension and slow-SQL rows with the same `Request_Key`, timed from `Instant` and `Duration` (`Instant` is the end for integrations and mobile requests, the start for screens and timers) |
| Span events | Error rows |
| Attributes | Application, module, operation, front-end, outcome; identifiers as on the logs |
| Span links | Calls from one OutSystems app to another one's exposed API, matched by endpoint and time; marked as inferred |

The OTel Collector's `spanmetrics` connector can then produce the same RED
metrics from spans, and a trace store (Grafana Tempo is the natural fit next
to Loki and Grafana) shows waterfalls with links to the logs and the SQL
Server dashboards for the same time. The trace store comes first, for any
OTLP source and with its own ADR ([TODO 3.6](../../TODO.md)); step B only
adds the spans.

Limits, stated in the product: spans exist only where OutSystems logs
something. Fast SQL, internal actions and client-side time do not appear, and
timings are as precise as the platform's millisecond durations. That covers
what SREs triage first (which request, which module, which dependency) without
replacing a code-level profiler.

Not recommended now: OTel .NET auto-instrumentation on the front-end servers.
It needs an agent on the platform servers and produces the same unlabelled
.NET spans an APM does.

## Open questions

Answered from the OutSystems 11 documentation ([Log data reference](https://success.outsystems.com/Documentation/11/Managing_the_Applications_Lifecycle/Monitor_and_Troubleshoot/Logging_database_and_architecture/Log_data_reference),
[The log tables and views](https://success.outsystems.com/Documentation/11/Managing_the_Applications_Lifecycle/Monitor_and_Troubleshoot/Logging_database_and_architecture/The_log_tables_and_views)):

- **Is `Instant` the start or the end of the logged operation?** Per log
  type: the start for `screen` and `cyclic_job`, the completion for
  `integration` and `mobile_request`, the time of logging for `error`,
  `general` and `extension`, the time of the event for `request_event`. The
  start-stamped rows are written when the operation ends, hence the per-log-
  type `lag` ([ADR 0008](decisions/0008-outsystems-11-log-extraction.md)).
- **Which time zone?** The platform servers' own. OutSystems Cloud runs UTC;
  a self-managed installation uses whatever its front-ends and database
  share. `log_timezone` is set per collector.
- **Which log types carry `Request_Key`?** All eight current ones: general,
  error (`Request_key`), screen, integration, extension, mobile request,
  cyclic job and request event (`REQUESTKEY`). Only the deprecated web
  reference, web service and SMS logs and the detail tables lack it.
- **Retention.** Ten weekly cycle tables per log type; the `oslog_<TYPE>`
  view is the current week, `oslog_<TYPE>_Previous` the week before, switched
  every Friday at 23:45 database time. The retention period is set in the
  Configuration Tool (Log tab, Advanced settings); OutSystems Cloud keeps 9
  weeks, 7 with log separation. `initial_lookback` can reach back one week at
  most, since the queries read two weeks' views.

Still to confirm against the logging database, with
[`scripts/outsystems-log-survey.sql`](../../scripts/outsystems-log-survey.sql)
(read-only; its output also supplies the DDL and rows for the test fixtures):

- Whether every request writes an entry-point row (screen or mobile request),
  or only some.
- The rows per minute of each log type at peak, which set the batch size and
  Loki sizing.
- The `Duration` unit per log type: the reference says seconds for extension,
  mobile request and cyclic job, milliseconds for the rest.
- The installation's time zone, from an `Instant` next to the database's
  `SYSDATETIMEOFFSET()`.
