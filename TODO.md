# TODO

Single source of truth for delivery order and task status. The
[roadmap](docs/product/roadmap.md) summarizes this checklist.
Core delivery order: **DB collector → OTel gateway → API/control plane → Web UI**,
after foundations and schema prerequisites. Sections and subsections are numbered
in checklist order. Later-service integrations are explicitly identified
below; they do not block independent stage work, but they do block end-to-end
completion of the dependent feature.

Test each implementation action as it lands. Section 16 is a shared validation
inventory used throughout delivery, not a final testing stage. Kubernetes runtime
acceptance requires section 8.4/16.4; cloud behavior needs target-environment tests.
Remaining delivery order is defined in the roadmap using the task groups below:
Kubernetes delivery → product workflows → investigations/alerts/reports → advanced
reliability → fallback ingestion → Oracle integration → final Alloy comparison.

## 0. Cross-cutting foundations

### 0.1 Product and architecture baseline
- [x] Restore the complete roadmap, record later-stage tasks in TODO, and clarify evidence publication versus metric export (validated phase/section order and file links)
- [x] Align collector → gateway → API → Web delivery order and record later-service dependencies in TODO and roadmap (checked task preservation, order and links)
- [x] Write `docs/product/requirements.md`
- [x] Write `docs/product/roadmap.md`
- [x] Write `docs/architecture/overview.md`
- [x] Write `docs/architecture/database-observability.md`
- [x] Write `docs/architecture/session-analysis.md`
- [x] Write `docs/architecture/alerting.md`
- [ ] Freeze subsystem boundaries, responsibilities, and interfaces
- [ ] Freeze ownership rules:
  - [ ] app-owned workflows derive environment through `applications.environment_id`
  - [ ] PostgreSQL stores control-plane metadata only
  - [ ] YAML/Kubernetes stores integration + collector desired runtime config
  - [ ] Prometheus/Loki store telemetry data
  - [ ] Redis stores transient async coordination only

### 0.2 Repo and local platform bootstrap
- [x] Create monorepo scaffolding
- [x] Add local Docker Compose stack for:
  - [x] PostgreSQL
  - [x] Redis
  - [x] Prometheus
  - [x] Loki
  - [x] Grafana
  - [x] Alertmanager
  - [x] OpenTelemetry Collector
- [x] Document local dev health checks in `docs/runbooks/local-dev.md`
- [x] Add GitHub Actions CI for Go tests and Docker Compose validation

### 0.3 Shared contracts and conventions
- [x] Create `packages/config-schema/`
- [x] Create `packages/telemetry-contracts/`
- [x] Define normalized telemetry contract
- [x] Define session investigation query/result contract
- [x] Define alert evidence payload contract
- [x] Define reporting payload contract
- [x] Define integration YAML schema

---

## 1. Core system: PostgreSQL metadata and schema

### 1.1 Core schema design
- [x] Convert ER diagram into migrations
- [x] Create tables for identity/access:
  - [x] `users`
  - [x] `roles`
  - [x] `user_roles`
- [x] Create tables for environment/application inventory:
  - [x] `environments`
  - [x] `applications`
  - [x] `application_components`
  - [x] `telemetry_sources`
  - [x] `normalization_rules`
- [x] Create tables for DB observability metadata:
  - [x] `database_targets`
  - [x] `probe_definitions`
  - [x] `probe_assignments`
- [x] Create tables for investigations:
  - [x] `investigations`
  - [x] `investigation_jobs`
  - [x] `investigation_results`
  - [x] `evidence_links`
- [x] Create tables for alerting:
  - [x] `alert_policies`
  - [x] `adaptive_baselines`
  - [x] `notification_routes`
  - [x] `alert_events`
- [x] Create tables for reporting:
  - [x] `report_templates`
  - [x] `report_schedules`
  - [x] `report_runs`

### 1.2 Constraints and indexes
- [x] Add FK constraints matching plan delete behavior
- [x] Add unique indexes for core ownership paths
- [x] Add check constraints for statuses/types/severities where stable
- [x] Add investigation lookup indexes
- [x] Add alert/report lookup indexes
- [x] Avoid over-constraining JSONB blobs early

### 1.3 Explicit non-goals in PostgreSQL MVP
- [x] Do **not** create tables for:
  - [x] integration config
  - [x] Grafana links/templates
  - [x] audit streams
  - [x] raw telemetry
  - [x] raw logs
  - [x] raw sessions/session identity mappings
  - [x] report binaries
  - [x] `outsystems_sources`
  - [x] collector desired runtime state
  - [x] `collector_instances`
  - [x] `collector_assignments`
- [x] Defer asset/topology schema unless SQL Server topology work truly needs it:
  - [x] `assets`
  - [x] `asset_relationships`
  - [x] `applications.primary_asset_id`
  - [x] `application_components.asset_id`

### 1.4 Schema verification
- [x] Add migration up/down smoke tests
- [x] Add FK and unique constraint tests
- [x] Add query-plan checks for early workflows

---

## 2. Core system: DB collector (`services/db-collector`)

Delivery stage 1. Runtime collection can proceed using YAML configuration without
API/UI. Requires shared config/schema [0.3, 8.1–8.2] and Prometheus wiring [9.1].
Probe assignments from API/PostgreSQL [2.5] are a stage-3 integration task, not a
collector-stage completion prerequisite. Query safety [2.2] must pass before
live validation; do not defer it to general hardening [15.2].

- [ ] Validate this stage: connector/probe/config/metric tests, safe non-production SQL queries and collector → Prometheus integration [16.1–16.2]
- [ ] Validate evidence publication/retrieval before investigation enrichment; current default evidence sink is a no-op, so sink wiring alone is not publication

### 2.1 Service bootstrap
- [x] Create Go service entrypoint `services/db-collector/cmd/db-collector/`
- [x] Create internal packages for config, collectors, SQL Server connectors, probes, export
- [x] Add health/metrics/logging/graceful shutdown

### 2.2 SQL Server connectivity and safety
- [x] Implement secure SQL Server connector manager
- [ ] Enforce least-privilege credentials
- [x] Enforce query timeout/budget guards
- [ ] Review all production queries for non-blocking behavior
- [ ] Define safe probe review/versioning process

### 2.3 Probe implementation
- [x] Implement waits probes
- [x] Implement locks/blocking probes
- [x] Implement sessions/connections probes
- [x] Implement memory pressure probes
- [x] Implement storage probes
- [x] Implement throughput/latency probes as needed
- [x] Replace generic column-to-metric decoding with explicit per-probe metric descriptors

### 2.4 Metrics and evidence output
- [x] Normalize SQL Server outputs into Prometheus-friendly metrics
- [x] Expose scrape endpoint
- [ ] Publish retrievable investigation evidence snapshots (the default `LoggingEvidenceSink` currently discards them)
- [ ] Define the evidence destination, schema, target/probe identity, timestamps, retention, redaction and bounded delivery/failure behavior; keep raw evidence out of PostgreSQL
- [ ] Test a blocking/session snapshot through publication and subsequent investigation retrieval, separately from Prometheus metric tests
- [x] Keep DB collector metric output stateless and Prometheus-scraped instead of persisted in PostgreSQL
- [ ] Add collector self-observability

### 2.5 Runtime config model
- [x] Read desired runtime collector config from `config/integrations.yaml`
- [x] Read active target/probe runtime config from YAML/Kubernetes convention
- [ ] Reintroduce API/PostgreSQL-driven probe assignments only after the control-plane workflow exists
- [x] Keep desired state out of PostgreSQL

---

## 3. Core system: OTel gateway (`services/otel-gateway`)

Delivery stage 2. Requires shared event/config contracts [0.3, 8.1–8.2],
stock OTel Collector routing [9.5], Loki label/storage conventions [9.2] and
representative OutSystems events/field mappings [10.2–10.3]. Deliver these
prerequisites with gateway work rather than waiting for the later feature track.
Use explicitly configured application/environment identities until API inventory
exists; API-managed onboarding [4.4, 10.1] follows in stage 3.
Generic source expansion [3.4] remains later work and does not block the initial
OutSystems gateway. Grafana dashboards [10.4] follow the ingest path.

- [ ] Validate this stage: representative Traditional/Reactive parser fixtures, normalized contract/labels and OTLP → Loki/Prometheus integration [16.1–16.2]

### 3.1 Service bootstrap
- [ ] Create Go service entrypoint `services/otel-gateway/cmd/otel-gateway/`
- [ ] Create internal packages for parsers and normalization
- [ ] Add health/metrics/logging/graceful shutdown

### 3.2 Collector-first integration
- [ ] Configure stock OpenTelemetry Collector first
- [ ] Keep custom app code thin
- [ ] Ensure custom code outputs shared Heartbeat telemetry contract
- [ ] Avoid rebuilding OTel Collector behavior in service code

### 3.3 OutSystems normalization
- [ ] Implement OutSystems parser(s)
- [ ] Implement OutSystems normalization pipeline
- [ ] Support Traditional + Reactive OutSystems
- [ ] Preserve default OutSystems log field/query compatibility first
- [ ] Normalize:
  - [ ] environment
  - [ ] application/module
  - [ ] component/action/screen/API op
  - [ ] user/session/request identifiers
  - [ ] client IP
  - [ ] severity
  - [ ] error fingerprint
  - [ ] latency/duration
  - [ ] host/node/runtime metadata
- [ ] Keep high-cardinality fields out of Loki labels where possible

### 3.4 Generic telemetry expansion
- [ ] Harden generic OTLP ingest after OutSystems path works
- [ ] Add MuleSoft parser later
- [ ] Add generic JSON/plaintext normalization later

---

## 4. Core system: API / control plane (`apps/api`)

Delivery stage 3. PostgreSQL schema [1], Redis [0.2], runtime config [8]
and collector/gateway contracts are prerequisites. CRUD, metadata persistence,
queue submission and worker interfaces can be delivered before their workers.
Investigation results [4.6] need analyzer/evidence assembly [6, 12]; adaptive
baselines [4.7] need computation [6.3, 13.2]; report execution/status [4.8] needs
reporting workers/delivery [7, 14]. Do not mark these workflows complete from
endpoint or queue tests alone. Rule/route provisioning [4.7] needs [9.1, 9.4,
13.1]; deep links [4.9] need YAML templates/datasources [8.1, 9.3, 15.1].

- [ ] Define/version API-to-worker job/result contracts before queue producers and consumers; integration-test each complete workflow when its worker lands
- [ ] Define how approved metadata probe assignments become YAML/Kubernetes runtime config [2.5, 8]; keep runtime desired state out of PostgreSQL
- [ ] Validate this stage: auth/ownership/secret refs, CRUD persistence, audit redaction, queue contracts and collector/gateway onboarding [16.1–16.2]

### 4.1 Service bootstrap
- [ ] Create Go service entrypoint `apps/api/cmd/api/`
- [ ] Create idiomatic Go package layout under `apps/api/internal/`
- [ ] Add config loading, logging, health endpoints, metrics, graceful shutdown
- [ ] Add PostgreSQL and Redis connectivity

### 4.2 Identity and access
- [ ] Implement local auth for MVP
- [ ] Implement users/roles/user_roles management
- [ ] Add minimal admin/wallboard access model
- [ ] Defer OIDC/SSO unless required early

### 4.3 Environment/application inventory
- [ ] Implement environments CRUD
- [ ] Implement applications CRUD
- [ ] Implement application components CRUD
- [ ] Enforce app -> environment ownership path

### 4.4 Telemetry source management
- [ ] Implement telemetry sources CRUD
- [ ] Support OutSystems telemetry source registration
- [ ] Associate telemetry source to application
- [ ] Derive environment through application
- [ ] Validate ingest mode and required config
- [ ] Store secret refs only

### 4.5 Database target management
- [ ] Implement database targets CRUD
- [ ] Implement probe definition management/versioning
- [ ] Implement probe assignment management
- [ ] Validate target config and safe probe policies
- [ ] Store credential refs only

### 4.6 Investigations API
- [ ] Implement create investigation endpoint
- [ ] Implement read investigation status/results endpoint
- [ ] Implement evidence links endpoint
- [ ] Persist durable investigation metadata in PostgreSQL
- [ ] Queue jobs through Redis

### 4.7 Alerting API
- [ ] Implement alert policies CRUD
- [ ] Implement notification routes CRUD
- [ ] Implement adaptive baseline metadata endpoints
- [ ] Implement rule rendering/provisioning interface to Prometheus/Alertmanager

### 4.8 Reporting API
- [ ] Implement report templates CRUD
- [ ] Implement report schedules CRUD
- [ ] Implement report run status/read endpoints
- [ ] Support on-demand report triggering

### 4.9 Log search / Grafana deep links
- [ ] Implement deep-link generation from YAML templates
- [ ] Implement log-search helper endpoints if needed for UI flows
- [ ] Keep Grafana/Loki integration template-driven, not DB-driven

### 4.10 Audit output
- [ ] Append admin/operator mutations to JSONL audit files
- [ ] Include request ID, actor, entity type/id, before/after, config version when relevant
- [ ] Ensure secrets never enter audit logs

---

## 5. Core system: Web UI (`apps/web`)

Delivery stage 4. Bootstrap/auth/inventory/onboarding screens require API
bootstrap, auth and CRUD [4.1–4.5]. Investigation screens require [4.6, 6, 12];
alert screens require policy/rule/route integration [4.7, 9.4, 13]; reports need
[4.8, 7, 14]. Deliver screen shells against contracts first, then validate real
workflows as later services arrive; shells/mocks are not end-to-end completion.
Grafana/Loki drill-down needs reachable datasources and templates [4.9, 9.3,
15.1]. Minimal authorization [4.2] precedes operator access; later hardening
[15.2] does not replace it.

- [ ] Validate this stage: API-backed login, roles, inventory/onboarding and deep links; validate investigation/alert/report UI end to end with their later workers [16.3]

### 5.1 UI bootstrap
- [ ] Create React/TypeScript app
- [ ] Set up routing, API client, auth/session handling, shared UI components

### 5.2 Primary operator screens
- [ ] Environments/applications screen
- [ ] Application details/components screen
- [ ] Telemetry sources screen
- [ ] Database targets onboarding screen
- [ ] Investigations screen
- [ ] Alert policies screen
- [ ] Reports screen
- [ ] Integrations/admin informational screen

### 5.3 MVP UX rules
- [ ] Make session investigation app-first: filter by application + user/IP + time range
- [ ] Derive/show environment from application
- [ ] Provide drill-down links to Grafana/Loki
- [ ] Keep assets out of phase-1 UI unless topology scope is approved

---

## 6. Core system: Session analyzer (`services/session-analyzer`)

### 6.1 Service bootstrap
- [ ] Create Go service entrypoint `services/session-analyzer/cmd/session-analyzer/`
- [ ] Create internal analysis/evidence/baselines packages
- [ ] Add Redis queue consumption, logging, metrics, health endpoints

### 6.2 Investigation correlation model
- [ ] Define deterministic app-first correlation model
- [ ] Correlate by:
  - [ ] application
  - [ ] derived environment
  - [ ] user ID
  - [ ] IP
  - [ ] request/session IDs
  - [ ] service/component
  - [ ] host
  - [ ] time window
- [ ] Query Loki + Prometheus + DB evidence + metadata DB
- [ ] Produce durable result summaries + evidence links
- [ ] Cache short-lived expensive results in Redis

### 6.3 Adaptive baselines
- [ ] Implement baseline computation package
- [ ] Start w/ rolling median + MAD or EWMA bands
- [ ] Compute per metric/application/period class
- [ ] Persist baseline metadata/version in PostgreSQL
- [ ] Keep live evaluation in Prometheus-compatible rules, not in PostgreSQL

---

## 7. Core system: Reporting (`services/reporting`)

### 7.1 Service bootstrap
- [ ] Create Go service entrypoint `services/reporting/cmd/reporting/`
- [ ] Create internal templates/jobs packages
- [ ] Add Redis queue integration, PostgreSQL access, metrics, health endpoints

### 7.2 Report generation
- [ ] Implement scheduled report jobs
- [ ] Implement on-demand report jobs
- [ ] Gather metrics/logs/investigation summaries
- [ ] Render HTML/CSV/PDF outputs
- [ ] Persist report run metadata
- [ ] Store artifact URI only when persistence is needed

### 7.3 MVP report content
- [ ] application/service health summary
- [ ] top regressions
- [ ] recurring wait/lock hotspots
- [ ] alert noise summary
- [ ] SLA/SLO deltas if available

---

## 8. Core system: Integrations, YAML config, and K8s runtime model

### 8.1 Integration YAML schema
- [ ] Define `config/integrations.yaml` schema
- [ ] Cover:
  - [ ] Grafana base URL
  - [ ] Loki endpoint
  - [ ] Alertmanager endpoint
  - [ ] SMTP/webhook/email delivery config
  - [ ] dashboard URL templates
  - [ ] collector definitions
  - [ ] credential refs

### 8.2 Go config manager
- [ ] Create shared config manager package
- [ ] Parse YAML into typed structs
- [ ] Validate schema + business rules
- [ ] Compute `config_version` hash
- [ ] Support redacted rendering for diagnostics
- [ ] Fail closed on invalid candidate config

### 8.3 Hot reload and reconciliation
- [ ] Implement immutable active config snapshots (`atomic.Value` / copy-on-write)
- [ ] Diff desired collectors by stable ID
- [ ] Add collector lifecycle interface:
  - [ ] `Start`
  - [ ] `Update`
  - [ ] `Drain`
  - [ ] `Stop`
  - [ ] `Status`
- [ ] Handle:
  - [ ] add collector
  - [ ] remove collector
  - [ ] safe live update
  - [ ] restart when live update unsupported
- [ ] Preserve old active config on failed reload

### 8.4 K8s delivery and reload triggers
- [ ] Deliver config via ConfigMap + secret refs
- [ ] Mount projected config volume
- [ ] Watch parent directory + debounce for dev/local if fsnotify is used
- [ ] Support explicit `SIGHUP`
- [ ] Support authenticated `POST /admin/config/reload`
- [ ] Optionally integrate reloader sidecar/controller
- [ ] Expose config version + reload status in health/admin endpoints and metrics

---

## 9. Core system: Grafana / Prometheus / Loki / Alertmanager / OTel infra

### 9.1 Prometheus
- [ ] Provision scrape config for services and collectors
- [ ] Add OutSystems recording rules
- [ ] Add SQL Server recording rules
- [ ] Add alert rule output path from Heartbeat rendering

### 9.2 Loki
- [ ] Provision Loki for app logs
- [ ] Enforce low-cardinality label strategy
- [ ] Keep high-cardinality investigation fields in log body/payload

### 9.3 Grafana
- [ ] Provision datasources as code
- [ ] Provision dashboards as code
- [ ] Add deep-link templates for UI -> Grafana/Loki flows

### 9.4 Alertmanager
- [ ] Provision routing configuration
- [ ] Support grouping/dedupe/silence/delivery
- [ ] Integrate rendered routes from Heartbeat metadata

### 9.5 OpenTelemetry Collector
- [ ] Provision collector config
- [ ] Route metrics/logs correctly to Prometheus/Loki paths
- [ ] Prefer collector processors/config before custom service code

---

## 10. Feature track: OutSystems application observability MVP

### 10.1 Source onboarding
- [ ] Add/edit/disable OutSystems telemetry source
- [ ] Validate required ingest config and labels
- [ ] Associate source to application

### 10.2 Normalization contract
- [ ] Define canonical application event envelope
- [ ] Map OutSystems default logs faithfully first
- [ ] Validate consistent environment/application/session fields

### 10.3 Telemetry wiring
- [ ] Route sample OutSystems telemetry into Loki
- [ ] Generate/derive application metrics in Prometheus
- [ ] Validate Grafana queries against both

### 10.4 Dashboards and drill-down
- [ ] Build OutSystems overview dashboard
- [ ] Build OutSystems errors dashboard
- [ ] Build OutSystems latency dashboard
- [ ] Enable drill-down overview -> latency/errors -> Loki

---

## 11. Feature track: SQL Server observability MVP

### 11.1 Target onboarding
- [ ] Add/edit/disable SQL Server target
- [ ] Validate reachability safely
- [ ] Attach approved probe assignments

### 11.2 Signal coverage
- [ ] waits
- [ ] blocking/locks
- [ ] sessions/connections
- [ ] memory pressure
- [ ] storage pressure
- [ ] throughput/latency
- [ ] error events where available

### 11.3 Dashboards
- [ ] SQL Server overview dashboard
- [ ] waits/locks dashboard
- [ ] sessions dashboard
- [ ] drill-down flows for DB troubleshooting

---

## 12. Feature track: Session investigation

### 12.1 Investigation model
- [ ] No PostgreSQL session table
- [ ] Compute session/user correlation from Loki/Prometheus at investigation time

### 12.2 API + jobs
- [ ] Create investigation
- [ ] queue analysis job
- [ ] assemble evidence
- [ ] return timeline/anomaly summary/evidence links

### 12.3 UI
- [ ] app-first investigation filter
- [ ] event timeline
- [ ] anomaly windows
- [ ] links to Grafana/Loki

---

## 13. Feature track: Alerting and adaptive telemetry

### 13.1 Static alerts
- [ ] Persist alert policy intent in PostgreSQL
- [ ] Render executable rules to Prometheus
- [ ] Render route intent to Alertmanager
- [ ] Validate trigger/route/dedupe/resolve path

### 13.2 Adaptive baselines
- [ ] Compute explainable baselines
- [ ] Persist baseline parameters/version
- [ ] Render thresholds to Prometheus-compatible rules
- [ ] Ensure hard guardrails remain in place

### 13.3 Adaptive telemetry controls
- [ ] Design telemetry escalation profiles
- [ ] Add anomaly/incident-based escalation logic later
- [ ] Ensure extra collection never amplifies DB load unsafely

---

## 14. Feature track: Reporting

### 14.1 Templates and schedules
- [ ] Implement report templates
- [ ] Implement report schedules
- [ ] Implement application-owned report runs

### 14.2 Delivery
- [ ] email delivery
- [ ] downloadable HTML/PDF/CSV
- [ ] object storage retention when needed

---

## 15. Feature track: Integrations and hardening

### 15.1 Grafana/Loki productization
- [ ] validate integration config from YAML
- [ ] validate datasources
- [ ] support deep links
- [ ] support permission-aware sharing if needed

### 15.2 Security and reliability
- [ ] add authn/authz hardening
- [ ] add query budgets
- [ ] add rate limits
- [ ] add retry/idempotency rules
- [ ] add worker backoff behavior

### 15.3 Audit and runbooks
- [ ] rotate audit JSONL files
- [ ] ship audit logs to Loki/SIEM
- [ ] write SQL Server onboarding runbook
- [ ] write alert tuning runbook
- [ ] write ops/runbook docs for reload failures, collector crashes, queue backlogs

---

## 16. Test and validation

### 16.1 Unit tests
- [ ] SQL normalization logic
- [ ] metric labeling/schema validation
- [ ] baseline calculations
- [ ] session correlation rules
- [ ] report rendering
- [ ] YAML config validation
- [ ] config hash stability
- [ ] reconciliation diff logic

### 16.2 Integration tests
- [ ] collector -> Prometheus
- [ ] OTLP ingest -> normalized logs/metrics
- [ ] API -> PostgreSQL
- [ ] alert rules -> Alertmanager routing
- [ ] investigation query -> evidence assembly
- [ ] failed reload keeps previous active config

### 16.3 End-to-end tests
- [ ] onboard OutSystems telemetry source
- [ ] see logs in Loki
- [ ] see metrics in Prometheus/Grafana
- [ ] create investigation from UI
- [ ] view alert flow end-to-end
- [ ] generate report end-to-end

### 16.4 K8s/runtime tests
- [ ] ConfigMap reload semantics
- [ ] fsnotify parent-directory watcher behavior if enabled
- [ ] `SIGHUP` reload path
- [ ] `/admin/config/reload` path
- [ ] collector add/remove/update reconciliation under live traffic

---

## 17. Deferred / only if scope expands
- [ ] asset and topology inventory model
- [ ] historical collector fleet inventory in PostgreSQL
- [ ] OIDC/SSO
- [ ] Mimir scale-out path
- [ ] Additional monitored engines beyond SQL Server and Oracle (Oracle is planned in section 21)
- [ ] richer integration CRUD UI backed by DB
- [ ] DB-backed audit/compliance search
- [ ] custom dashboard engine
- [ ] autonomous RCA


---

## 18. Kubernetes delivery: kind, Helm and production

- [ ] Build one shared chart for kind, CI and production with environment-specific values, pinned dependencies and minimal/full profiles
- [ ] Adapt Make/local workflows and add isolated kind CI with the first chart; include PostgreSQL/Redis when the API consumes them
- [ ] Validate image architecture, chart/schema rendering, ready workloads, code-change rollout and pod-to-external-database DNS/TCP/authentication/TLS
- [ ] Validate ConfigMap/reload behavior, failed-target isolation and singleton rollout gaps/overlap [8.4, 16.4]
- [ ] Wire Prometheus to Alertmanager and validate firing/routing/resolution; add an external Watchdog and test loss of the monitoring path
- [ ] Implement the production defaults and prerequisites recorded in the [migration plan](docs/plans/2026-09-29-kind-helm-migration.md), including deployment, registry, secrets, access, notifications and target network placement
- [ ] Validate production-specific networking/identity/TLS/storage, retention and restore in the target environment; local kind tests alone do not establish production acceptance
- [ ] Pass and retain the migration plan acceptance matrix before retiring platform Compose/Kustomize; retain isolated external database fixtures

## 19. Advanced collector reliability and HA

- [ ] Validate per-target failure isolation, bounded retries, freshness/readiness and stale-series cleanup before replica work
- [ ] Define per-target ownership/takeover with fencing or equivalent duplicate-polling protection; keep collection singleton until validated
- [ ] Define recovery-time/data-gap objectives and downstream buffering/replay limits separately
- [ ] Test target/process/node loss, network partitions, reloads during failure and downstream outages; measure recovery, gaps, duplicates and database load
- [ ] Validate security, operational runbooks, audit rotation/shipping and restore procedures [15]

## 20. Fallback file ingestion

- [ ] Implement uploaded/exported data only after live collection and reliability are validated, for cases where direct database connectivity is unavailable
- [ ] Define formats, validation, lineage, source identity and freshness warnings; keep live collection as the default
- [ ] Test malformed/stale inputs, accepted formats and investigation use end to end

## 21. Oracle collector and integration

- [ ] Define supported Oracle versions, signal coverage, connector/licensing constraints, least-privilege permissions and query budgets
- [ ] Implement secure connectivity, credential refs/TLS, pooling, bounded queries and Oracle probes with explicit metric units/types/labels and structured evidence
- [ ] Extend config/schema, API target/probe management and Web onboarding for Oracle
- [ ] Add Oracle recording/alert rules, Grafana dashboards and investigation/report integration
- [ ] Add an isolated non-production Oracle fixture, Helm delivery support and onboarding/operations runbook
- [ ] Test supported-version queries, permissions, telemetry semantics and database load
- [ ] Validate onboarding → collection → dashboards → alerts/investigations/reports and repeat failure/reload/freshness/ownership tests; retain evidence before section 22

## 22. Final custom collector versus Grafana Alloy comparison

This is the last planned delivery stage, after all preceding required workflows
and validation gates, including Oracle integration. Conditional section-17 scope
is not a prerequisite unless explicitly promoted into the delivery plan.

- [ ] Create a dedicated comparison branch only after preceding stages pass validation
- [ ] Verify Alloy's SQL Server and Oracle capabilities at evaluation time; document unsupported coverage rather than assuming parity
- [ ] Run equivalent non-production workloads with isolated metric identities and controlled combined query load
- [ ] Compare coverage, custom evidence, metric semantics, permissions/TLS, query load/resources, reloads, failure/recovery, gaps/duplicates and operational cost for each engine separately
- [ ] Record reproducible configurations, results and an evidence-backed decision: retain custom collection, adopt Alloy for suitable workloads, or support both with explicit ownership
- [ ] Review the decision before merging the selected implementation; treat production adoption separately from the experiment
