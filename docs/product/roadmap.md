# Heartbeat Roadmap

The single phase plan for Heartbeat. Phases 0–1 reflect the order in which the
work actually happened: the SQL Server path was built before the OutSystems
path, although the original plan put OutSystems first. The order of later phases
follows the product priorities and can be revisited. Task-level detail lives in
[TODO.md](../../TODO.md) (section numbers in brackets).

| Phase | Theme | Status |
| --- | --- | --- |
| 0 | Foundations | Done |
| 1 | SQL Server collection path | In progress |
| 2 | Kubernetes delivery (kind + Helm) | Planned, next |
| 3 | Control plane and operator UI | Not started |
| 4 | Application observability (OutSystems first) | Not started (gateway partial) |
| 5 | Investigations, alerting and reporting | Not started |
| 6 | Reliability, HA and security hardening | Not started |
| 7 | Fallback file ingestion | Deferred |
| 8 | Custom collector vs Grafana Alloy comparison | Deferred, late roadmap |

## Phase 0 — Foundations (done)

- Monorepo scaffolding, local Docker Compose stack, CI [0.2]
- Shared JSON-schema contracts for config, telemetry, investigations, alerts and
  reports [0.3]
- PostgreSQL schema migrations and tests ([data model](../architecture/data-model.md)) [1]
- Prometheus, Loki, Grafana, Alertmanager and OTel Collector provisioning [9]
- Shared config manager with validation, versioning and hot reload [8]

## Phase 1 — SQL Server collection path (in progress)

Done:

- DB collector with the built-in probe catalog (waits, blocking, sessions,
  memory, storage, throughput), per-target connection pooling and timeouts [5]
- Failure isolation, bounded backoff, freshness metrics, stale-series cleanup,
  readiness that reflects collector state, reload rollback [15.0]
- SQL Server overview dashboard; local SQL Server sandbox

Remaining:

- Fix Prometheus recording-rule names and counter/gauge semantics [15.0]
- Per-probe error counters; least-privilege review of all production queries;
  probe review/versioning process [5.2, 15.0]
- Collector endpoint security: authenticated diagnostics, full redaction,
  constant-time token check, NetworkPolicy, per-target TLS settings [15.2]
- Remaining SQL Server signals and dashboards
  ([signal coverage](../architecture/database-observability.md#sql-server-signal-coverage)) [11]

## Phase 2 — Kubernetes delivery (planned, next)

- One Helm chart for kind, CI and production; SQL Server stays a Docker test
  target; Make and CI adapted; platform Compose retired
  ([ADR 0003](../architecture/decisions/0003-helm-on-kind-and-production.md))
- Connect Prometheus to Alertmanager and add a Watchdog dead-man's switch
- Answer the open production decisions (cloud, registry, deploy mechanism,
  secrets, Grafana SSO)

## Phase 3 — Control plane and operator UI

- API bootstrap: config, logging, health, PostgreSQL and Redis [2.1]
- Local auth with minimal roles [2.2]
- Environments, applications, components, telemetry sources and database
  targets CRUD [2.3–2.5]
- Web UI bootstrap and inventory/onboarding screens [3]
- JSONL audit output [2.10]

## Phase 4 — Application observability (OutSystems first)

- OutSystems parsers and normalization; gateway forwards to the OTel Collector [4.3]
- OutSystems source onboarding and dashboards (overview, errors, latency) with
  drill-down into Loki [10]
- Later: MuleSoft and generic JSON/plaintext sources [4.4]

## Phase 5 — Investigations, alerting and reporting

- Investigation API, session analyzer and investigation UI [2.6, 6, 12]
- Alert policy rendering to Prometheus and Alertmanager; adaptive baselines [2.7, 13]
- Report templates, schedules, generation and email delivery [2.8, 7, 14]
- See [operator workflows](../architecture/workflows.md)

## Phase 6 — Reliability, HA and security hardening

- Collector replica ownership and takeover with fencing; recovery-time and
  data-gap objectives; outage testing
  ([collector HA design](../architecture/database-observability.md#collector-recovery-and-high-availability-planned)) [15.0]
- Authn/authz hardening, query budgets, rate limits, worker idempotency and
  backoff [15.2]
- Audit rotation and shipping; operational runbooks [15.3]
- Integration and end-to-end test suites [16]

## Phase 7 — Fallback file ingestion (deferred)

- Ingest uploaded/exported files only when direct database connectivity is
  impossible, with validation, lineage and freshness warnings [5.4]
- Always lower priority than live collection

## Phase 8 — Custom collector vs Grafana Alloy comparison (late roadmap)

- A parallel, evidence-based comparison on a dedicated branch, after the core
  workflow and reliability baseline are validated; the outcome is open [18]
- See the
  [comparison design](../architecture/database-observability.md#custom-collector-and-alloy-comparison-late-roadmap)

## Only if scope expands

Asset and topology inventory, collector fleet history in PostgreSQL, OIDC/SSO,
Mimir scale-out, other database engines, DB-backed integration CRUD and audit
search, autonomous RCA [17].
