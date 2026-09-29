# Operator Workflows

How the planned services turn telemetry into operator workflows: application
telemetry ingestion, session investigation, alerting and reporting, all driven
through the control-plane API and web UI.

> **Status:** mostly planned. Implemented today: the stock OpenTelemetry
> Collector pipeline, a thin gateway helper that normalizes single JSON events
> and accepts Alertmanager webhooks, Prometheus rules, Alertmanager routing, and
> the PostgreSQL schema. The API, web UI, session analyzer and reporting service
> are empty scaffolds. See the [current runtime](overview.md#current-runtime)
> and the [roadmap](../product/roadmap.md).

All four workflows follow the same pattern:

- **PostgreSQL** stores intent and durable metadata (what was asked, which
  policy, which schedule, what the result was). See the [data model](data-model.md).
- **Prometheus, Loki and Alertmanager** do the runtime work (store, query,
  evaluate, route).
- **Redis** dispatches asynchronous jobs; durable job state stays in PostgreSQL.
- Everything is **application-owned**: the environment is derived through
  `applications.environment_id`.

## Services and responsibilities

| Service | Owns | Status |
| --- | --- | --- |
| API / control plane (`apps/api`) | Inventory CRUD, telemetry source and database target onboarding, alert policy and report schedule CRUD, users/roles, investigation APIs, Grafana deep links from YAML templates, JSONL audit output. Uses PostgreSQL for durable data and Redis for queues, idempotency keys and cached summaries. | Planned |
| Web UI (`apps/web`) | Operator screens: environments/applications, telemetry sources, DB target onboarding, investigations, alert policies, reports, integrations. Investigation is app-first; drill-downs open Grafana/Loki. | Planned |
| OTel gateway (`services/otel-gateway`) | Platform-specific parsing and normalization into the shared telemetry contract; the stock OTel Collector handles OTLP ingest, batching and routing. See its [README](../../services/otel-gateway/README.md). | Partial |
| Session analyzer (`services/session-analyzer`) | Investigation jobs: correlation across Prometheus, Loki, collector evidence and metadata; anomaly windows; adaptive baseline computation. | Planned |
| Reporting (`services/reporting`) | Scheduled and on-demand report jobs, rendering, delivery, run metadata. | Planned |

## Application telemetry ingestion (OutSystems first)

1. OutSystems emits logs, events and metrics through a supported export path or
   an OTLP-compatible bridge.
2. The OpenTelemetry Collector receives and routes what it can with stock
   processors.
3. The gateway normalizes OutSystems-specific fields into the
   [application event contract](../../packages/telemetry-contracts/src/application_event.schema.json).
4. Logs land in Loki with controlled labels: environment, application,
   component, severity, event type. The Collector already deletes `user_id`,
   `session_id`, `request_id` and `client_ip` from log attributes so they cannot
   become labels.
5. Derived metrics (latency, throughput, errors, availability, saturation) land
   in Prometheus.
6. Grafana shows application health and links to Loki log views.

**Normalization targets:** environment; application/module;
component/action/screen/API operation; tenant where available; user, session and
request identifiers; client IP; severity; error category and stack fingerprint;
latency; host/node/runtime metadata; deployment/version context.

**Rules**

- Support Traditional and Reactive OutSystems from the start.
- Map default OutSystems log fields faithfully first, so existing SRE queries
  keep working. Confirm improvements with the SRE team before changing field
  semantics.
- Avoid storing PII unnecessarily; prefer hashed or normalized identifiers.
- MuleSoft and generic JSON/plaintext sources come after the OutSystems path is
  proven.

## Session investigation

**Input:** application, plus user, IP, session or request identifier, plus a
time range. **Output:** timeline, anomaly windows, likely bottlenecks, affected
components, and evidence links into Grafana/Loki.

1. The operator submits the query in the UI.
2. The API derives the environment from the application and writes an
   `investigations` row.
3. The API enqueues a job in Redis and records it in `investigation_jobs`.
4. The session analyzer picks up the job and queries Prometheus, Loki, DB
   collector evidence and the metadata database.
5. The analyzer correlates by application, environment, user, IP,
   request/session IDs, component, host and time window, then stores a bounded
   summary in `investigation_results` plus `evidence_links`. Short-lived results
   are cached in Redis.
6. The UI renders the timeline and links.

**Non-goals in the MVP:** no session table, no session identity mapping table,
no raw evidence blobs in PostgreSQL. Session correlation is recomputed from
Loki/Prometheus at investigation time.

**Risk:** correlation quality depends on stable identifiers in source logs.
Validate which OutSystems fields are reliably present early.

## Alerting

- **Static rules** cover hard limits and always remain in place as guardrails.
- **Adaptive baselines** cover noisy signals: rolling median + MAD or EWMA
  bands, computed per metric, application and period class. Baseline parameters
  and versions are stored in `adaptive_baselines`, and thresholds are rendered
  into Prometheus-compatible rules. Baselines must be explainable, because
  opaque adaptive alerting erodes trust.
- **Evaluation** happens in Prometheus. **Routing, grouping, deduplication,
  silencing and delivery** happen in Alertmanager (email, webhook, later
  PagerDuty/Slack).
- **PostgreSQL** stores policy intent (`alert_policies`), route intent
  (`notification_routes`) and alert history (`alert_events`). It never evaluates
  alerts. Do not build a second alert engine.
- **Grafana links** in alert evidence are generated from YAML URL templates,
  not database tables.

Planned rendering path: API → `infra/prometheus/rules/generated/` (loaded by
Prometheus) and API → Alertmanager routes. Who renders `generated/` (the API or
a build step) is an [open question](overview.md#open-questions).

**Current state:** `infra/prometheus/rules/heartbeat.rules.yml` holds hand-written
rules. Prometheus has no `alerting` block, so alerts do not reach Alertmanager
yet. Alertmanager routes everything to the gateway's
`POST /v1/heartbeat/alerts` endpoint, which only counts deliveries. The SQL
Server recording rules reference metric names the collector no longer emits;
see [metrics reference](../reference/metrics-and-endpoints.md#known-gaps).

**Adaptive telemetry (later):** escalation profiles that raise collection
detail during anomalies or incidents, without ever amplifying database load
unsafely.

## Reporting

1. A schedule (`report_schedules`) or a user triggers a report.
2. The API or reporting service enqueues a job in Redis and records a
   `report_runs` row.
3. The reporting service gathers metrics, logs and investigation summaries.
4. The output is rendered as HTML, CSV or PDF and delivered by email. The
   artifact is stored only if retention requires it, and PostgreSQL keeps
   `artifact_uri`.
5. The UI/API exposes run status and downloads.

**MVP report content:** application/service health summary, top regressions,
recurring wait/lock hotspots, alert noise summary, SLA/SLO deltas where
available. The first target is a weekly summary delivered by email.

## Audit

Every operator or admin mutation appends one JSONL line (timestamp, actor,
action, entity type and ID, before/after metadata, request ID, config version)
to a dated audit file, for example `audit/heartbeat-audit-YYYY-MM-DD.jsonl`.
Config reload results and collector add/remove/change decisions are logged the
same way. Secrets never enter audit logs. Files are rotated and shipped to
Loki/SIEM.
