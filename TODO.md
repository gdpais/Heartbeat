# TODO

Implementation task list for Heartbeat, derived from `.hermes/plans/2026-05-30_180810-heartbeat-monitoring-mvp.md`.

## 0. Cross-cutting foundations

### 0.1 Product and architecture baseline
- [x] Write `docs/product/requirements.md`
- [x] Write `docs/product/phased-roadmap.md`
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

## 2. Core system: API / control plane (`apps/api`)

### 2.1 Service bootstrap
- [ ] Create Go service entrypoint `apps/api/cmd/api/`
- [ ] Create idiomatic Go package layout under `apps/api/internal/`
- [ ] Add config loading, logging, health endpoints, metrics, graceful shutdown
- [ ] Add PostgreSQL and Redis connectivity

### 2.2 Identity and access
- [ ] Implement local auth for MVP
- [ ] Implement users/roles/user_roles management
- [ ] Add minimal admin/wallboard access model
- [ ] Defer OIDC/SSO unless required early

### 2.3 Environment/application inventory
- [ ] Implement environments CRUD
- [ ] Implement applications CRUD
- [ ] Implement application components CRUD
- [ ] Enforce app -> environment ownership path

### 2.4 Telemetry source management
- [ ] Implement telemetry sources CRUD
- [ ] Support OutSystems telemetry source registration
- [ ] Associate telemetry source to application
- [ ] Derive environment through application
- [ ] Validate ingest mode and required config
- [ ] Store secret refs only

### 2.5 Database target management
- [ ] Implement database targets CRUD
- [ ] Implement probe definition management/versioning
- [ ] Implement probe assignment management
- [ ] Validate target config and safe probe policies
- [ ] Store credential refs only

### 2.6 Investigations API
- [ ] Implement create investigation endpoint
- [ ] Implement read investigation status/results endpoint
- [ ] Implement evidence links endpoint
- [ ] Persist durable investigation metadata in PostgreSQL
- [ ] Queue jobs through Redis

### 2.7 Alerting API
- [ ] Implement alert policies CRUD
- [ ] Implement notification routes CRUD
- [ ] Implement adaptive baseline metadata endpoints
- [ ] Implement rule rendering/provisioning interface to Prometheus/Alertmanager

### 2.8 Reporting API
- [ ] Implement report templates CRUD
- [ ] Implement report schedules CRUD
- [ ] Implement report run status/read endpoints
- [ ] Support on-demand report triggering

### 2.9 Log search / Grafana deep links
- [ ] Implement deep-link generation from YAML templates
- [ ] Implement log-search helper endpoints if needed for UI flows
- [ ] Keep Grafana/Loki integration template-driven, not DB-driven

### 2.10 Audit output
- [ ] Append admin/operator mutations to JSONL audit files
- [ ] Include request ID, actor, entity type/id, before/after, config version when relevant
- [ ] Ensure secrets never enter audit logs

---

## 3. Core system: Web UI (`apps/web`)

### 3.1 UI bootstrap
- [ ] Create React/TypeScript app
- [ ] Set up routing, API client, auth/session handling, shared UI components

### 3.2 Primary operator screens
- [ ] Environments/applications screen
- [ ] Application details/components screen
- [ ] Telemetry sources screen
- [ ] Database targets onboarding screen
- [ ] Investigations screen
- [ ] Alert policies screen
- [ ] Reports screen
- [ ] Integrations/admin informational screen

### 3.3 MVP UX rules
- [ ] Make session investigation app-first: filter by application + user/IP + time range
- [ ] Derive/show environment from application
- [ ] Provide drill-down links to Grafana/Loki
- [ ] Keep assets out of phase-1 UI unless topology scope is approved

---

## 4. Core system: OTel gateway (`services/otel-gateway`)

### 4.1 Service bootstrap
- [x] Create Go service entrypoint `services/otel-gateway/cmd/otel-gateway/`
- [x] Create internal packages for parsers and normalization
- [x] Add health/metrics/logging/graceful shutdown

### 4.2 Collector-first integration
- [x] Configure stock OpenTelemetry Collector first
- [x] Keep custom app code thin
- [x] Ensure custom code outputs shared Heartbeat telemetry contract
- [x] Avoid rebuilding OTel Collector behavior in service code

### 4.3 OutSystems normalization
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

### 4.4 Generic telemetry expansion
- [ ] Harden generic OTLP ingest after OutSystems path works
- [ ] Add MuleSoft parser later
- [ ] Add generic JSON/plaintext normalization later

---

## 5. Core system: DB collector (`services/db-collector`)

### 5.1 Service bootstrap
- [x] Create Go service entrypoint `services/db-collector/cmd/db-collector/`
- [x] Create internal packages for config, collectors, SQL Server connectors, probes, export
- [x] Add health/metrics/logging/graceful shutdown

### 5.2 SQL Server connectivity and safety
- [x] Implement secure SQL Server connector manager
- [ ] Enforce least-privilege credentials
- [x] Enforce query timeout/budget guards
- [ ] Review all production queries for non-blocking behavior
- [ ] Define safe probe review/versioning process

### 5.3 Probe implementation
- [x] Implement waits probes
- [x] Implement locks/blocking probes
- [x] Implement sessions/connections probes
- [x] Implement memory pressure probes
- [x] Implement storage probes
- [x] Implement throughput/latency probes as needed
- [x] Replace generic column-to-metric decoding with explicit per-probe metric descriptors

### 5.4 Metrics and evidence output
- [x] Normalize SQL Server outputs into Prometheus-friendly metrics
- [x] Expose scrape endpoint
- [x] Publish investigation evidence snapshots where useful
- [x] Keep DB collector metric output stateless and Prometheus-scraped instead of persisted in PostgreSQL
- [ ] Add collector self-observability

### 5.5 Runtime config model
- [x] Read desired runtime collector config from `config/integrations.yaml`
- [x] Read active target/probe runtime config from YAML/Kubernetes convention
- [ ] Reintroduce API/PostgreSQL-driven probe assignments only after the control-plane workflow exists
- [x] Keep desired state out of PostgreSQL

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
- [x] Define `config/integrations.yaml` schema
- [ ] Cover:
  - [x] Grafana base URL
  - [x] Loki endpoint
  - [x] Alertmanager endpoint
  - [x] SMTP/webhook/email delivery config
  - [x] dashboard URL templates
  - [x] collector definitions
  - [x] credential refs

### 8.2 Go config manager
- [x] Create shared config manager package
- [x] Parse YAML into typed structs
- [x] Validate schema + business rules
- [x] Compute `config_version` hash
- [x] Support redacted rendering for diagnostics
- [x] Fail closed on invalid candidate config

### 8.3 Hot reload and reconciliation
- [x] Implement immutable active config snapshots (`atomic.Value` / copy-on-write)
- [x] Diff desired collectors by stable ID
- [x] Add collector lifecycle interface:
  - [x] `Start`
  - [x] `Update`
  - [x] `Drain`
  - [x] `Stop`
  - [x] `Status`
- [x] Handle:
  - [x] add collector
  - [x] remove collector
  - [x] safe live update
  - [x] restart when live update unsupported
- [x] Preserve old active config on failed reload

### 8.4 K8s delivery and reload triggers
- [x] Deliver config via ConfigMap + secret refs
- [x] Mount projected config volume
- [x] Watch parent directory + debounce for dev/local if fsnotify is used
- [x] Support explicit `SIGHUP`
- [x] Support authenticated `POST /admin/config/reload`
- [ ] Optionally integrate reloader sidecar/controller
- [x] Expose config version + reload status in health/admin endpoints and metrics

---

## 9. Core system: Grafana / Prometheus / Loki / Alertmanager / OTel infra

### 9.1 Prometheus
- [x] Provision scrape config for services and collectors
- [x] Add OutSystems recording rules
- [x] Add SQL Server recording rules
- [x] Add alert rule output path from Heartbeat rendering

### 9.2 Loki
- [x] Provision Loki for app logs
- [x] Enforce low-cardinality label strategy
- [x] Keep high-cardinality investigation fields in log body/payload

### 9.3 Grafana
- [x] Provision datasources as code
- [x] Provision dashboards as code
- [x] Add deep-link templates for UI -> Grafana/Loki flows

### 9.4 Alertmanager
- [x] Provision routing configuration
- [x] Support grouping/dedupe/silence/delivery
- [x] Integrate rendered routes from Heartbeat metadata

### 9.5 OpenTelemetry Collector
- [x] Provision collector config
- [x] Route metrics/logs correctly to Prometheus/Loki paths
- [x] Prefer collector processors/config before custom service code

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
- [ ] non-SQL-Server engine parity
- [ ] richer integration CRUD UI backed by DB
- [ ] DB-backed audit/compliance search
- [ ] custom dashboard engine
- [ ] autonomous RCA
