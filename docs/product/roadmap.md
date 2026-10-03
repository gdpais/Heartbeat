# Heartbeat Phased Roadmap

Derived from [TODO.md](../../TODO.md), the single source of truth for delivery
order and task status. Bracketed references use TODO section numbers, updated
to match checklist order. Phases group foundations and schema prerequisites
together. Every action is tested as delivered; section 16 is used throughout.

## Phase 0 — Foundations and prerequisites

- Monorepo, local stack, contracts and PostgreSQL migrations/checks [0, 1].
- Supply shared config/schema and observability prerequisites [8.1–8.2, 9]
  with the consuming stage rather than waiting for their checklist position.
- Validate contracts/config, migrations/constraints and isolated test fixtures.

## Phase 1 — DB collector

- SQL Server connectivity, safe probes, telemetry/evidence and self-observability
  [2]. Runtime collection uses YAML without requiring API/UI.
- Validate connector/probes/metrics and collector → Prometheus against a safe
  non-production target. Evidence must be published/retrievable before analyzer use.
- API-driven probe assignments [2.5] integrate in phase 3; they do not block
  YAML-based collection. Query safety is required now, ahead of later hardening.

## Phase 2 — OTel gateway

- Bootstrap, collector-first routing and OutSystems normalization [3.1–3.3].
- Requires event/config contracts, OTel/Loki/Prometheus routing and representative
  OutSystems field mappings [8, 9, 10.2–10.3]. Use configured identities before
  API-managed onboarding exists. Generic source expansion [3.4] remains later.
- Validate representative parser fixtures and OTLP → normalized logs/metrics.

## Phase 3 — API / control plane

- Bootstrap/auth, inventory/source/target management, metadata/job APIs, rule
  provisioning, deep links and audit output [4]. Integrate probe assignments
  with YAML/Kubernetes runtime config [2.5, 8].
- Validate auth/ownership, CRUD/database behavior, secret refs, redaction and
  queue contracts. Results require later workers: investigations [6, 12],
  adaptive baselines [6.3, 13.2] and reports [7, 14]. Rule/route provisioning
  needs [9.1, 9.4, 13.1]; deep links need [8.1, 9.3, 15.1].
- Endpoint/queue tests establish API behavior; complete workflow validation
  follows worker implementation.

## Phase 4 — Web UI

- Bootstrap/auth and inventory/onboarding first [5], consuming phase-3 APIs.
- Investigation, alert and report screens can start with contract-backed shells;
  complete workflows depend on [6, 7, 12–14]. Grafana/Loki links require live
  datasources/templates. Minimal authorization is a prerequisite.
- Validate API-backed login/roles/onboarding and links; run end-to-end screen
  tests when each dependent workflow is available.

## Phase 5 — Kubernetes delivery: kind + Helm

- Shared chart for kind, CI and production, minimal/full profiles and pinned
  dependencies; adapt Make/CI and production deployment [18, 8.4].
- Follow the [migration plan](../plans/2026-09-29-kind-helm-migration.md).
  PostgreSQL/Redis join the deployed platform when consumed by the API;
  monitored SQL Server and isolated database fixtures remain external.
- Wire Prometheus → Alertmanager, implement the external Watchdog, and deliver
  production secrets, access, networking and notification prerequisites.
- Validation: migration acceptance matrix, image/rollout/config behavior,
  pod-to-database connectivity, singleton overlap/gaps and concurrent CI isolation.
  Validate cloud-specific behavior and restore in the target environment before
  production acceptance; retire platform Compose/Kustomize only after parity passes.
- This phase completes Helm/production delivery; earlier services still require
  their local configuration, infrastructure and tests in phases 0–4.

## Phase 6 — Complete application and database product workflows

- Complete OutSystems and SQL Server onboarding, signal coverage, dashboards and
  drill-down [10, 11], including later generic source expansion [3.4].
- Validation: API/Web onboarding → ingest/collection → Loki/Prometheus → Grafana;
  verify field/metric semantics, cardinality and safe database load.

## Phase 7 — Investigations, alerting and reporting

- Implement session analyzer and reporting workers [6, 7]; complete investigation,
  baseline/rule/route and report workflows [12–14] with the earlier API/UI.
- Detailed collector evidence must be published to a retrievable destination
  [2.4] before investigation enrichment. Current default sink discards snapshots;
  exported Prometheus metrics are a separate output.
- Validation: deterministic correlation/baseline/report tests, API-to-worker job
  contracts, retrievable evidence, UI investigation results, alert
  trigger/route/dedupe/resolve and scheduled/on-demand report delivery end to end.
  Test retries/idempotency and safe telemetry escalation.

## Phase 8 — Advanced reliability, HA and security

- Complete product integration/security, audit and operational runbooks [15].
- Define collector ownership/fencing, takeover, recovery/data-gap objectives and
  downstream buffering limits [19]; keep collector singleton until ownership passes.
- Validation: target/process/node/network/downstream failures, reloads during
  outages, measured gaps/duplicates/load, restore and relevant end-to-end suites.
  Required early query safety/auth and phase tests are not deferred to this phase.

## Phase 9 — Fallback file ingestion

- Last-resort uploaded/exported data when direct connectivity is unavailable,
  after live workflows and reliability are validated [20].
- Validation: formats, rejection, lineage, freshness/stale-data handling and
  investigation use end to end. Live collection remains the default.

## Phase 10 — Oracle collector and integration

- Implement Oracle connectivity/probes/evidence, shared configuration, API/Web
  onboarding, dashboards/rules and investigation/report integration [21].
- Add an isolated test target, Helm delivery and operational runbook.
- Validation: supported Oracle versions, least privilege, units/counters/labels,
  query load, complete operator workflows and failure/reload/ownership behavior.
  Retain results before starting the final comparison.

## Phase 11 — Final custom collector versus Grafana Alloy comparison

- Start on a dedicated branch only after phases 0–10 pass their required tests,
  including implemented and validated Oracle integration [22].
- Verify Alloy capabilities for SQL Server and Oracle separately; document
  unsupported coverage explicitly. Use equivalent non-production workloads,
  isolated metrics and controlled combined polling load.
- Validation: reproducible coverage/evidence/semantics, permissions/TLS, database
  and resource load, reload/failure/recovery, gaps/duplicates and operational cost.
  Review the evidence-backed choice before merging; production adoption is separate.

## Conditional scope

Section 17 remains conditional: topology, fleet history, SSO, Mimir, additional
engines beyond SQL Server/Oracle, DB-backed integrations/audit, custom dashboards
and autonomous RCA. It does not block Alloy unless promoted into planned scope.
