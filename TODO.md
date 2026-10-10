# TODO

Single source of truth for delivery order and task status. The
[roadmap](docs/product/roadmap.md) summarizes this checklist by phase; the
original plan this list was derived from is
[archived](docs/archive/2026-05-30-mvp-implementation-plan.md).

Core delivery order: **DB collector → OTel gateway → API/control plane → Web UI**,
after foundations and schema prerequisites (sections 2–5). Later-service
integrations are explicitly identified below; they do not block independent
stage work, but they do block end-to-end completion of the dependent feature.

Test each implementation action as it lands. Section 16 is a shared validation
inventory used throughout delivery, not a final testing stage. Remaining
delivery order after the Web UI: production delivery [18] → product workflows
[10–11] → investigations, alerting and reporting [6, 7, 12–14] → advanced
reliability [19] → fallback ingestion [20] → Oracle integration, gated by an
Alloy coverage check [21] → final Alloy comparison [22].

## 0. Cross-cutting foundations

### 0.1 Product and architecture baseline
- [x] Write product and architecture baseline docs ([docs index](docs/README.md))
- [x] Design current-runtime and target-architecture diagrams, link them from the README, and verify them against source/configuration
- [x] Reorganize docs by audience; record key decisions as ADRs; archive superseded plans
- [x] Reorder delivery: collector → gateway → API → Web; production delivery after the Web UI; Oracle after fallback ingestion and gated by an Alloy coverage check; Alloy comparison last
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
- [x] Document local dev health checks in `docs/guides/local-development.md`
- [x] Add GitHub Actions CI for Go tests and Docker Compose validation
- [x] Isolate Docker integration tests from the developer stack and concurrent test runs
- [x] Make both application Dockerfiles platform-aware (ARM64 and AMD64)

### 0.3 Shared contracts and conventions
- [x] Create `packages/config-schema/`
- [x] Create `packages/telemetry-contracts/`
- [x] Define normalized telemetry contract
- [x] Define session investigation query/result contract
- [x] Define alert evidence payload contract
- [x] Define reporting payload contract
- [x] Define integration YAML schema

### 0.4 Kubernetes delivery: kind and CI

Production delivery is section 18.

- [x] Assess the Compose-to-kind migration and choose shared Helm delivery ([ADR 0003](docs/architecture/decisions/0003-helm-on-kind-and-production.md))
- [x] Decide platform (AWS EKS), deploy mechanism (Argo CD), registry (ECR), secrets (AWS Secrets Manager + ESO), version policy, alert channels incl. WhatsApp, dead-man's switch and Grafana access ([ADR 0005](docs/architecture/decisions/0005-production-delivery-and-operations-defaults.md))
- [x] Pin tools to the EKS-supported minor (kind node v1.36.4, kubectl 1.35–1.37, Helm 4.2, Go 1.27.1); add `make tools-check`
- [x] Bump engines to latest stable under Compose first (Prometheus, Grafana, Loki, Alertmanager, otelcol); add Renovate
- [x] Build the Helm chart and kind workflow; adapt Make and CI; pass the ADR 0003 acceptance criteria (`make chart-check`, `make kind-e2e`; evidence in ADR 0003)
- [x] Retire the platform Compose definition and the Kustomize bundle
- [ ] Install the Renovate GitHub App on the repository (config is in `renovate.json`)
- [ ] Agree the local loop's time and resource budget (measured: about 2.0 GiB RAM for the full profile)

### 0.5 Development workflow and releases

Trunk-based development and tagged releases ([ADR 0006](docs/architecture/decisions/0006-trunk-based-development-and-tagged-releases.md));
the rules are in [CONTRIBUTING.md](CONTRIBUTING.md).

- [x] Decide the branching, versioning, release and changelog model; add `CONTRIBUTING.md` and `CHANGELOG.md`
- [x] Retire the `db-collectors` phase branch, its ruleset and its CI push trigger
- [x] Release workflows: release-please keeps a release pull request (`CHANGELOG.md`, `version.txt`, chart `version`/`appVersion`); a `vX.Y.Z` tag on `master` that passed CI publishes multi-arch images and the chart to GHCR and lists their digests in the GitHub release
- [x] Change-based CI: slow jobs run only when a change can affect them, every job on `master`; one required check, `ci-ok` ([ADR 0007](docs/architecture/decisions/0007-change-based-ci-with-one-required-check.md))
- [x] Switch the `master` ruleset's required checks from `test`, `Helm chart` and `kind end-to-end` to `ci-ok`
- [ ] Create the release GitHub App; set `RELEASE_APP_CLIENT_ID` and `RELEASE_APP_PRIVATE_KEY` ([setup](CONTRIBUTING.md#releases))
- [ ] At `1.0.0`, the first production release: remove `"prerelease": true` from `release-please-config.json`
- [x] Tag ruleset on all tags (not only `v*`, so later component tags are covered): no updates or deletions
- [ ] Once the release app exists and `v0.1.0` is tagged: add *Restrict creations* to the tag ruleset, with the release app in its bypass list
- [ ] First release, `v0.1.0`: tag the phase 1 merge commit `64ac1b6`, run the `release` workflow by hand for it, check the release notes list three digests, and make the GHCR packages public

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
collector-stage completion prerequisite. Query safety [2.2] and endpoint
security [2.7] must pass before live validation against a shared target; do not
defer them to general hardening [15.2].

Remaining work, in order: endpoint security [2.7] → least privilege and query
safety [2.2] → metric types and self-metrics [2.4, 2.6] → core CPU, cache and
I/O signals [2.3]. Evidence publication moved to the investigation track [12.4]:
its consumer and storage design live there, and today's evidence is only a row
count.

- [x] Validate this stage: connector/probe/config/metric tests, safe non-production SQL queries, and live SQL Server → collector → Prometheus → Grafana on kind, including reloads and target outages, with the collector logged in as a least-privilege login [16.1–16.2]

### 2.1 Service bootstrap
- [x] Create Go service entrypoint `services/db-collector/cmd/db-collector/`
- [x] Create internal packages for config, collectors, SQL Server connectors, probes, export
- [x] Add health/metrics/logging/graceful shutdown

### 2.2 SQL Server connectivity and safety
- [x] Implement secure SQL Server connector manager
- [x] Enforce least-privilege credentials: run `make test-sqlserver` and `make kind-e2e` as a login holding only the documented grants (`VIEW SERVER STATE`, `VIEW ANY DEFINITION`), not `sa`
- [x] Warn at startup, in the log and a metric, when the collector login is `sysadmin`
- [x] Set `LOCK_TIMEOUT` and `DEADLOCK_PRIORITY LOW` on every collector session so a probe never waits on locks or wins a deadlock against the application
- [x] Enforce query timeout/budget guards
- [x] Pool SQL Server connections per target
- [x] Review all production queries for non-blocking behavior
- [x] Define safe probe review/versioning process: write the probe review checklist now; probe versioning comes with API probe definitions [4.5]

### 2.3 Probe implementation
- [x] Implement waits probes
- [x] Implement locks/blocking probes
- [x] Implement sessions/connections probes
- [x] Implement memory pressure probes
- [x] Implement storage probes
- [x] Implement throughput/latency probes as needed
- [x] Replace generic column-to-metric decoding with explicit per-probe metric descriptors
- [x] Fix the `throughput` probe's duplicate `Transactions/sec` series and padded `counter_name` labels; check `memory_pressure` for the same padding ([#4](https://github.com/gdpais/Heartbeat/issues/4))
- [x] Run every built-in probe against a real SQL Server in CI (`make test-sqlserver`): no duplicate series, no padded label values
- [x] Add core signals before the first production deploy (after counter support [2.4]): CPU utilisation, page life expectancy and buffer cache hit ratio, file I/O from `sys.dm_io_virtual_file_stats`; dashboard panels for each. The rest of the signal set stays in [11.2]

### 2.4 Metrics and evidence output
- [x] Normalize SQL Server outputs into Prometheus-friendly metrics
- [x] Expose scrape endpoint
- [x] Produce structured evidence for blocking/session probes
- [x] Export cumulative SQL Server values (waits, throughput counters) as counters that tolerate SQL Server restarts, renamed to Prometheus conventions (`_total` suffix, seconds rather than ms, e.g. `heartbeat_sqlserver_wait_seconds_total`); update rules, rule tests and dashboards to rates in the same change
- [x] Keep DB collector metric output stateless and Prometheus-scraped instead of persisted in PostgreSQL
- [x] Add collector self-observability: per-probe duration histogram, and Go runtime and process metrics on the collector's registry

### 2.5 Runtime config model
- [x] Read desired runtime collector config from `config/integrations.yaml`
- [x] Read active target/probe runtime config from YAML/Kubernetes convention
- [ ] Reintroduce API/PostgreSQL-driven probe assignments only after the control-plane workflow exists
- [x] Keep desired state out of PostgreSQL

### 2.6 Collector reliability baseline

Replica ownership and outage testing are section 19. Design details:
[Collector recovery and high availability](docs/architecture/database-observability.md#collector-recovery-and-high-availability-planned).

- [x] Align SQL Server Prometheus recording-rule names with the current probe catalog; validate the rules against emitted metrics (promtool unit tests in `make rules-check`, Go test for metric-name drift)
- [x] Document the planned HA improvements and the Alloy comparison in architecture and roadmap docs
- [x] Isolate probe/target failures so one failed target cannot stop unrelated collection
- [x] Retry transient collection failures with bounded exponential backoff and jitter; expose persistent failures without retry storms
- [x] Expose per-target success, consecutive failures, last-success time, and freshness; expire stale/removed metric series
- [x] Add per-probe cumulative error counters
- [x] Make readiness reflect expected collector state and add deployment health probes and restart/recovery policies
- [x] Validate safe reloads, including partial reconciliation failure (rollback) and replacement-poller startup failure (unit-tested)
- [x] Bound every probe with a hard deadline so a probe the driver cannot cancel never stalls the collector cycle; keep at most one abandoned call per target and bound dead connections with a driver socket timeout

### 2.7 Collector endpoint security
- [x] Diagnostics: require the admin token for `GET /admin/config`; keep `/readyz` to status only and move raw driver errors (host, port, login) behind auth
- [x] Redaction: mask notification channel `config` values in `Redacted()`, strip userinfo from endpoint URLs, and reject credentials embedded in `loki`/`alertmanager` URLs at validation
- [x] Admin token: compare in constant time
- [x] Network exposure: add a NetworkPolicy limiting port 8082 to Prometheus and operator access
- [x] SQL Server TLS: log a startup warning and surface in diagnostics when `TrustServerCertificate` is enabled; per-target TLS settings wait until a real target needs them

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
- [x] Create Go service entrypoint `services/otel-gateway/cmd/otel-gateway/`
- [x] Create internal packages for parsers and normalization
- [x] Add health/metrics/logging/graceful shutdown

### 3.2 Collector-first integration
- [x] Configure stock OpenTelemetry Collector first
- [x] Keep custom app code thin
- [x] Ensure custom code outputs shared Heartbeat telemetry contract
- [x] Avoid rebuilding OTel Collector behavior in service code

### 3.3 OutSystems normalization
- [ ] Implement OutSystems parser(s)
- [ ] Implement OutSystems normalization pipeline
- [ ] Forward normalized events to the OTel Collector (the gateway currently normalizes and returns them)
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
- [ ] Add PostgreSQL and Redis to the Helm chart once the API consumes them; keep monitored databases and test fixtures external

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
- [ ] Keep assets out of the first UI release unless topology scope is approved

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
- [x] Cover:
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
- [x] Add alert rule output path (`rules/generated/`, loaded by Prometheus; nothing renders into it yet)
- [x] Add an `alerting` block so Prometheus sends alerts to Alertmanager (plus an always-firing Watchdog routed to a dead-man's switch)

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
- [ ] Integrate rendered routes from Heartbeat metadata (needs the API; routing is static today)

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
- [ ] page life expectancy per NUMA node (`Buffer Node` counters); the `buffer_cache` probe reports only the server-wide `Buffer Manager` value

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

### 12.4 Collector evidence

Moved from the collector stage: the destination depends on the investigation
design, and nothing consumes evidence before the session analyzer [6].

- [ ] Define the evidence content (today only a row count), destination, schema, target/probe identity, timestamps, retention, redaction and bounded delivery/failure behavior; keep raw evidence out of PostgreSQL
- [ ] Publish retrievable investigation evidence snapshots (the default `LoggingEvidenceSink` currently discards them)
- [ ] Test a blocking/session snapshot through publication and subsequent investigation retrieval, separately from Prometheus metric tests; sink wiring alone is not publication

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

Collector reliability and endpoint security are in sections 2.6–2.7; replica
ownership and outage testing are in section 19.

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
- [x] write SQL Server onboarding runbook ([guide](docs/guides/database-targets.md))
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
- [x] collector -> Prometheus (`make kind-e2e`, against a Docker SQL Server)
- [ ] OTLP ingest -> normalized logs/metrics
- [ ] API -> PostgreSQL
- [x] alert rules -> Alertmanager routing (`make kind-e2e`: Watchdog reaches Alertmanager)
- [ ] investigation query -> evidence assembly
- [x] failed reload keeps previous active config (`make kind-e2e`)

### 16.3 End-to-end tests
- [ ] onboard OutSystems telemetry source
- [ ] see logs in Loki
- [ ] see metrics in Prometheus/Grafana
- [ ] create investigation from UI
- [ ] view alert flow end-to-end
- [ ] generate report end-to-end

### 16.4 K8s/runtime tests
- [x] ConfigMap reload semantics (`make kind-e2e`: valid update hot-reloads in place; invalid update rejected while collection continues)
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

## 18. Production delivery: EKS and Argo CD

The shared chart already runs on kind and in CI [0.4]. This stage takes it to
production with the defaults in
[ADR 0005](docs/architecture/decisions/0005-production-delivery-and-operations-defaults.md).
It follows the Web UI so the first production deploy carries a usable product;
pull it forward if a production SQL Server must be monitored sooner.

- [ ] `heartbeat-deploy` config repository with Argo CD Applications and per-environment values
- [ ] The release workflow [0.5] also publishes the images and the chart to ECR through GitHub OIDC
- [ ] EKS infrastructure as code; AWS Secrets Manager with External Secrets
- [ ] Route the Watchdog to healthchecks.io and test loss of the monitoring path
- [ ] Notification receivers: Discord, then Slack, Teams and WhatsApp; set up the WhatsApp Business Account and alert template
- [ ] Document where the monitored SQL Servers sit relative to the EKS VPC; validate pod-to-database DNS/TCP/authentication/TLS from EKS
- [ ] Validate production-specific networking, identity, TLS, storage, retention and restore in the target environment; kind tests alone do not establish production acceptance
- [ ] Optional: rehearse Argo CD on kind before the first production deploy

## 19. Advanced collector reliability and HA

Builds on the single-collector baseline [2.6]. Keep the collector a singleton
until ownership is validated. Design details:
[Collector recovery and high availability](docs/architecture/database-observability.md#collector-recovery-and-high-availability-planned).

- [ ] Define per-target ownership/takeover across replicas with fencing or equivalent duplicate-polling protection
- [ ] Define recovery-time/data-gap objectives and downstream buffering/replay limits separately from collector failover
- [ ] Test target/process/node loss, network partitions, reloads during failure, and storage/downstream outages; measure recovery, gaps, duplicates and database load, and re-check the baseline [2.6] under these failures
- [ ] Validate security, operational runbooks, audit rotation/shipping and restore procedures [15]

## 20. Fallback file ingestion

- [ ] Implement ingestion of uploaded/exported files only after live collection and reliability are validated, for cases where direct database connectivity is unavailable
- [ ] Keep it a last-resort, fallback-only ingest mode, never the default onboarding path; expose it as a telemetry source ingest mode [4.4]
- [ ] Define accepted formats, validation, lineage, source identity and freshness warnings
- [ ] Test malformed/stale inputs, accepted formats and investigation use end to end

## 21. Oracle collector and integration

The coverage check below gates the rest of this section: building a custom
Oracle collector before knowing what Alloy covers risks discarding it after the
comparison [22].

- [ ] Before any Oracle code, time-boxed to about half a day: check what Grafana Alloy (and established Oracle exporters) cover for the planned Oracle signals, permissions, TLS and evidence needs. Record the outcome as an ADR: build the custom Oracle collector, adopt Alloy for Oracle, or run the comparison [22] before this section
- [ ] Define supported Oracle versions, signal coverage, connector/licensing constraints, least-privilege permissions and query budgets
- [ ] Implement secure connectivity, credential refs/TLS, pooling, bounded queries and Oracle probes with explicit metric units/types/labels and structured evidence
- [ ] Extend config/schema, API target/probe management and Web onboarding for Oracle
- [ ] Add Oracle recording/alert rules, Grafana dashboards and investigation/report integration
- [ ] Add an isolated non-production Oracle fixture, Helm delivery support and onboarding/operations runbook
- [ ] Test supported-version queries, permissions, telemetry semantics and database load
- [ ] Validate onboarding → collection → dashboards → alerts/investigations/reports and repeat failure/reload/freshness/ownership tests; retain evidence before section 22

## 22. Final custom collector versus Grafana Alloy comparison

The last planned delivery stage, after all preceding required workflows and
validation gates, including Oracle integration (unless the coverage check [21]
moved it earlier). Conditional section-17 scope is not a prerequisite unless
explicitly promoted into the delivery plan. Evaluation design:
[Custom collector and Alloy comparison](docs/architecture/database-observability.md#custom-collector-and-alloy-comparison-late-roadmap).

- [ ] Create a dedicated comparison branch only after preceding stages pass validation
- [ ] Verify Alloy's SQL Server and Oracle capabilities at evaluation time; document unsupported coverage rather than assuming parity
- [ ] Run equivalent non-production workloads with isolated metric identities and controlled combined query load
- [ ] Compare coverage, custom evidence, metric semantics, permissions/TLS, query load/resources, reloads, failure/recovery, gaps/duplicates, buffering/replay and operational cost, for each engine separately
- [ ] Record reproducible configurations, results and an evidence-backed decision: retain custom collection, adopt Alloy for suitable workloads, or support both with explicit per-target ownership and a shared telemetry contract
- [ ] Review the decision before merging the selected implementation; treat production adoption separately from the experiment
