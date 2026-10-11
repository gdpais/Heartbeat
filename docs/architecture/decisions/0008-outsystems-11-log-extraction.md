# 0008. Extract OutSystems 11 logs from the logging database

- Status: Proposed
- Date: 2026-10-10

## Context

The first OutSystems deployment Heartbeat must observe is **OutSystems 11
self-managed** (Traditional and Reactive apps). Unlike OutSystems 11 on
OutSystems Cloud (log streaming add-on) and ODC (App Analytics Stream), it has
no native telemetry export: the platform writes its logs to the `oslog_*`
tables of its logging database, a SQL Server database. The SRE team already
has queries that read those tables well.

The phase 2 plan assumed a push path: something sends JSON events to the OTel
gateway, which normalizes them. Nothing in a self-managed installation sends
those events, so that path does not apply. Heartbeat needs to poll the logging
database, under the same rules as the SQL Server collector: no agents on
database hosts, a least-privilege login, bounded queries that never block the
platform.

The db-collector already has everything such a poller needs except the log
handling itself: SQL Server connections with pooling, credential references,
login checks and session settings (`LOCK_TIMEOUT`, `DEADLOCK_PRIORITY LOW`), a
hard deadline per query, per-target failure isolation and backoff, hot reload,
readiness and self-metrics.

## Decision

| Concern | Choice | Why |
| --- | --- | --- |
| Source | The `oslog_*` tables of the OutSystems 11 logging database, read with a login that has `SELECT` on them and nothing else | The only complete log source in a self-managed installation; no agent on the platform servers. |
| Where the extractor runs | A new collector kind, `outsystems_logs`, in the **db-collector** | Reuses the SQL Server connector, safety settings, reload, backoff and readiness instead of copying them into another service. The logging database is a database; the collector already owns how Heartbeat reads databases. |
| Where the queries live | One `.sql` file per log type in [`services/db-collector/internal/probes/outsystems/queries/`](../../../services/db-collector/internal/probes/outsystems/queries/README.md), embedded in the binary. The file name is the log type (`error.sql` → `error`) | Queries are code: reviewed with the [probe review checklist](../database-observability.md#probe-review-checklist), tested in CI against SQL Server and versioned with the release. Not in Prometheus or OTel Collector config, which never talk to SQL Server. |
| Choosing log types | `probes:` in the collector's YAML lists the log types to extract, as for the `sqlserver` kind. A `query_template` override per environment is allowed under the same contract and review | Same configuration model and hot reload as the SQL Server probes (ADR 0002). |
| Query contract | Parameters `@from` (exclusive), `@to` (inclusive) and `@limit`; at most `@limit` rows ordered by an `Instant` column. Every other column becomes a log attribute **under its OutSystems name**; `Message` becomes the log body; an optional `Severity` column sets the severity. Details in the [queries README](../../../services/db-collector/internal/probes/outsystems/queries/README.md) | Default OutSystems field names stay queryable, so existing SRE queries and habits carry over. The query author, who knows the tables, controls joins, filters and severity. |
| Incremental reads | A watermark per target and log type: read `Instant` in (`watermark`, `now − lag`], advance only after the batch is delivered. When a batch is full, rows sharing its last `Instant` are left for the next cycle. The watermark is stored in a file on the collector's volume; without one, start at `now − initial_lookback` | No gaps from rows the log service commits late (`lag`), none at batch boundaries, and a restart resumes where it stopped. |
| Delivery | OTLP/HTTP logs to the OTel Collector (`opentelemetry.endpoint`), then Loki. At least once: a failed export is retried from the same watermark, so a retried batch can appear twice in Loki | Keeps Loki behind the Collector like every other log source; the Collector stays the place for routing and processing. |
| Loki labels | Resource attributes `service.name` (the application), `deployment.environment.name` and `outsystems.log_type`. User, session and request IDs, client IP, module, screen and the rest stay structured metadata | Few streams per application; high-cardinality fields stay filterable without becoming labels. |
| Derived metrics | Computed by the extractor from the rows it reads and exposed on the collector's `/metrics`, scraped by Prometheus. The catalog is in [application observability](../application-observability.md#derived-metrics) | Exact counts and durations from every row, testable with fixtures, and the same Prometheus-scraped model as the SQL Server metrics. |
| Traces | None in this decision. Synthesizing request traces from `Request_Key` is designed in [application observability](../application-observability.md#request-traces) and needs its own decision (storage) | First prove the identifiers are reliable in real logs. |

Considered and not chosen:

- **The OTel Collector's `sqlquery` receiver.** No custom code, but it tracks
  progress by one column only, so it either loses rows that share an `Instant`
  at a batch boundary or rereads them; it has no lag window for late commits,
  no hard deadline or `LOCK_TIMEOUT` guarantee, and none of the collector's
  readiness and backoff.
- **The OTel gateway.** It would need its own copy of the SQL Server
  connector, safety settings, reload and readiness.
- **A separate extractor service.** The same copy, plus another image and
  deployment, for no isolation the per-collector failure isolation does not
  already give.
- **Queries in `integrations.yaml` only.** Editable without a release, but
  unreviewed SQL against a production database, with no CI test. The
  per-environment `query_template` override covers the exceptions.

## Consequences

- The db-collector is no longer stateless for this kind: it keeps a watermark
  per target and log type, so its StatefulSet gets a small persistent volume.
  The `sqlserver` kind stays stateless.
- The db-collector now pushes logs over OTLP as well as serving metrics, so it
  needs network access to the OTel Collector (NetworkPolicy egress).
- The OTel gateway takes no part in OutSystems 11 ingest. Its JSON event
  endpoint and alert sink are superseded; whether the service is removed or
  kept as the bridge for future push sources (TODO 3.4) is decided before that
  work starts.
- CI needs a SQL Server fixture with the `oslog_*` tables the queries read,
  built from the real DDL.
- OutSystems 11 on OutSystems Cloud and ODC, when needed, send OTLP straight to
  the OTel Collector; the attribute names they use are mapped there, not in
  the extractor.
- `Instant` is stored in the logging database server's local time on some
  installations; the time zone is a per-collector setting (default UTC). In
  a zone with daylight saving time, rows logged in the hour that repeats when
  clocks go back can be missed; UTC avoids it.
- Rows the platform's log service commits later than `lag` after their
  `Instant` are missed. The OutSystems log delay metric shows how close the
  log service gets to `lag`, and an alert on it comes with the derived metrics.
