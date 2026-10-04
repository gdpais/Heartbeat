# Heartbeat Roadmap

The single phase plan for Heartbeat. It summarizes [TODO.md](../../TODO.md),
which is the source of truth for delivery order and task status (TODO section
numbers in brackets). The core order is **DB collector → OTel gateway →
API/control plane → Web UI**; each phase is tested as it is delivered, using the
shared validation inventory [16] throughout rather than as a final stage.

| Phase | Theme | Status |
| --- | --- | --- |
| 0 | Foundations, including kind + Helm delivery | Done |
| 1 | DB collector (SQL Server) | In progress |
| 2 | OTel gateway (OutSystems ingest) | Not started (gateway scaffold done) |
| 3 | API / control plane | Not started |
| 4 | Web UI | Not started |
| 5 | Production delivery (EKS + Argo CD) | Not started; decisions recorded |
| 6 | Complete application and database product workflows | Not started |
| 7 | Investigations, alerting and reporting | Not started |
| 8 | Advanced reliability, HA and security | Not started |
| 9 | Fallback file ingestion | Deferred |
| 10 | Oracle collector, gated by an Alloy coverage check | Late roadmap |
| 11 | Custom collector vs Grafana Alloy comparison | Last planned phase |

## Phase 0 — Foundations (done)

- Monorepo scaffolding, local platform, CI [0.2]
- Shared JSON-schema contracts for config, telemetry, investigations, alerts and
  reports [0.3]
- PostgreSQL schema migrations and tests ([data model](../architecture/data-model.md)) [1]
- Prometheus, Loki, Grafana, Alertmanager and OTel Collector provisioning;
  Prometheus sends alerts to Alertmanager with a Watchdog route [9]
- Shared config manager with validation, versioning and hot reload [8]
- One Helm chart for kind, CI and production; acceptance checks run on kind in
  CI; platform Compose and the Kustomize bundle retired
  ([ADR 0003](../architecture/decisions/0003-helm-on-kind-and-production.md)) [0.4]

Open housekeeping: install the Renovate app and agree the local loop's time and
resource budget [0.4].

## Phase 1 — DB collector (in progress)

Runtime collection is configured in YAML and needs no API or UI.

Done:

- Probe catalog (waits, blocking, sessions, memory, storage, throughput),
  per-target connection pooling and timeouts [2.1–2.3]
- Failure isolation, bounded backoff, freshness metrics, stale-series cleanup,
  readiness that reflects collector state, reload rollback [2.6]
- SQL Server overview dashboard; local SQL Server sandbox
- Server-wide, unpadded throughput series
  ([#4](https://github.com/gdpais/Heartbeat/issues/4)); every probe runs
  against a real SQL Server in CI [2.3]

Remaining:

- Export cumulative values as counters and show rates in the dashboard [2.4]
- Per-probe error counters; least-privilege review of all production queries;
  probe review/versioning process [2.2, 2.6]
- Collector endpoint security: authenticated diagnostics, full redaction,
  constant-time token check, NetworkPolicy, per-target TLS settings [2.7]
- Publish retrievable evidence snapshots (the default sink discards them) [2.4]

Exit: live SQL Server → collector → Prometheus → Grafana on kind, including
reloads and target outages, against a safe non-production target. API-driven
probe assignments [2.5] join in phase 3 and do not block this phase.

## Phase 2 — OTel gateway (OutSystems ingest)

- OutSystems parsers and normalization (Traditional and Reactive); the gateway
  forwards normalized events to the OTel Collector [3.3]
- Deliver the prerequisites with it: event envelope and representative
  OutSystems field mappings, Loki label conventions, Collector routing [9, 10.2–10.3]
- Use configured application/environment identities until API inventory exists
- Later: MuleSoft and generic JSON/plaintext sources [3.4]

Exit: representative parser fixtures and OTLP → normalized Loki logs and
Prometheus metrics.

## Phase 3 — API / control plane

- Bootstrap, local auth with minimal roles, PostgreSQL and Redis (added to the
  chart here) [4.1–4.2]
- Environments, applications, components, telemetry sources, database targets
  and probe assignments; approved assignments become YAML/Kubernetes runtime
  config [4.3–4.5, 2.5]
- Investigation, alerting and reporting metadata and job APIs; deep links;
  JSONL audit output [4.6–4.10]

Exit: auth/ownership, CRUD persistence, secret refs, audit redaction and queue
contracts. Workflows that need later workers (investigations, baselines,
reports) are validated in phase 7, not from endpoint tests alone.

## Phase 4 — Web UI

- Bootstrap, auth, and inventory/onboarding screens on the phase-3 APIs [5]
- Investigation, alert and report screens start as contract-backed shells and
  are completed with their workers in phase 7

Exit: API-backed login, roles, onboarding and Grafana/Loki deep links.

## Phase 5 — Production delivery (EKS + Argo CD)

The chart already runs on kind and in CI; this phase takes it to production with
the defaults in
[ADR 0005](../architecture/decisions/0005-production-delivery-and-operations-defaults.md) [18].
It comes after the Web UI so the first production deploy carries a usable
product; pull it forward if a production SQL Server must be monitored sooner.

- `heartbeat-deploy` repository with Argo CD Applications and per-environment
  values; optional Argo CD rehearsal on kind
- CI publishes multi-arch images and the chart to ECR through GitHub OIDC
- EKS infrastructure as code; External Secrets; healthchecks.io dead-man's
  switch; Discord, then Slack, Teams and WhatsApp receivers
- Where the monitored SQL Servers sit relative to the EKS VPC

Exit: production networking, identity, TLS, storage, retention and restore
validated in the target environment.

## Phase 6 — Complete application and database product workflows

- OutSystems and SQL Server onboarding, signal coverage, dashboards and
  drill-down [10, 11]
  ([SQL Server signal coverage](../architecture/database-observability.md#sql-server-signal-coverage))

Exit: onboarding in the UI → ingest/collection → Loki/Prometheus → Grafana, with
field/metric semantics, cardinality and database load checked.

## Phase 7 — Investigations, alerting and reporting

- Session analyzer and investigation UI [6, 12]; collector evidence must be
  retrievable first [2.4]
- Alert policy rendering to Prometheus and Alertmanager; adaptive baselines [13]
- Report templates, schedules, generation and email delivery [7, 14]
- See [operator workflows](../architecture/workflows.md)

Exit: deterministic correlation/baseline/report tests; alert
trigger/route/dedupe/resolve and scheduled/on-demand reports end to end.

## Phase 8 — Advanced reliability, HA and security

- Collector replica ownership and takeover with fencing; recovery-time and
  data-gap objectives; outage testing [19]
  ([collector HA design](../architecture/database-observability.md#collector-recovery-and-high-availability-planned))
- Authn/authz hardening, query budgets, rate limits, worker idempotency and
  backoff [15.2]
- Audit rotation and shipping; operational runbooks [15.3]

Query safety and collector endpoint security are phase 1 work, not deferred here.

## Phase 9 — Fallback file ingestion (deferred)

- Ingest uploaded/exported files only when direct database connectivity is
  impossible, with validation, lineage and freshness warnings [20]
- Always lower priority than live collection

## Phase 10 — Oracle collector (late roadmap)

- **Gate first:** a time-boxed check of what Grafana Alloy and established Oracle
  exporters cover, recorded as an ADR. It decides whether to build a custom
  Oracle collector, adopt Alloy for Oracle, or run the phase-11 comparison first,
  so a custom collector is not built only to be replaced [21]
- Then: connectivity, probes, evidence, config, API/Web onboarding, dashboards,
  rules, investigation/report integration, an isolated test target and a runbook

Exit: supported versions, least privilege, metric semantics and query load
validated, plus failure/reload/ownership tests.

## Phase 11 — Custom collector vs Grafana Alloy comparison (last)

- A parallel, evidence-based comparison on a dedicated branch, for SQL Server
  and Oracle separately; the outcome is open [22]
- See the
  [comparison design](../architecture/database-observability.md#custom-collector-and-alloy-comparison-late-roadmap)

## Only if scope expands

Asset and topology inventory, collector fleet history in PostgreSQL, OIDC/SSO,
Mimir scale-out, engines beyond SQL Server and Oracle, DB-backed integration
CRUD and audit search, custom dashboards, autonomous RCA [17].
