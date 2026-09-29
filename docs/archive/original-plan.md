# Heartbeat Plan: Database Monitoring Tool with Grafana

> **Archived.** Superseded planning document kept for history; see [docs/archive/README.md](README.md) for where its content lives now.

## Goal
Design and implement a database monitoring platform that visualizes metrics in Grafana, supports adaptive alerting and reporting, and provides a backoffice for operational management (new DB connections, query changes, etc.), while ensuring collectors are not installed on the database server itself.

## Current context and assumptions
- Primary visualization layer: Grafana.
- Non-invasive collection requirement: no agent/collector installed on DB hosts.
- Multiple database engines may need support over time (e.g., PostgreSQL, MySQL, SQL Server, MongoDB).
- Prometheus and/or Mimir are acceptable for metrics storage/querying.
- Backoffice must allow runtime configuration updates without redeploying the whole stack.
- Security and network access to DBs will be managed from a separate monitoring environment.
- File-based ingestion may be supported later as a last-resort fallback when direct database connectivity is not feasible; it is explicitly lower priority than live collection from SQL Server or other database instances.

## Proposed architecture
- Collection layer (off-DB hosts):
  - Dedicated collector services running in a monitoring VPC/cluster.
  - DB metric collection via read-only SQL queries and/or exporter endpoints from a remote host.
  - Pluggable “connector” model per DB type.
- Metrics pipeline:
  - Prometheus for scraping and rule evaluation.
  - Long-term, horizontally scalable metrics storage with Mimir.
  - Alertmanager for routing alerts.
- Visualization and analysis:
  - Grafana dashboards for DB health, performance, and SLOs.
  - Grafana Alerting for dashboard-driven rules where appropriate.
- Adaptive alerting engine:
  - Baseline/anomaly logic using rolling windows (hour/day/week seasonality).
  - Dynamic thresholds (e.g., median + k*MAD, quantile bands, EWMA deviations).
  - Suppression/correlation to reduce alert noise.
- Reporting:
  - Scheduled daily/weekly reports (PDF/HTML/CSV) with KPI trends, incidents, and top regressions.
- Backoffice:
  - Web app + API for CRUD on database connections, query templates, alert policies, notification routing, report schedules, and RBAC.
  - Audit logs for all admin actions.

## Step-by-step implementation plan
1. Define scope and SLOs
   - Decide initial DB engines and mandatory metrics per engine.
    - SQL Server
    - Metrics: CPU, Memory, I/O, IOPs, User Transactions, Wait Events, Locks, Cache Hit Ratio, Connections, Errors, Latency, Availability, Rollbacks, Active Sessions, Sessions, Physical Reads/sec, Physical Writes/sec, Logical Reads/sec, Logical Writes/sec, Free Space, Database Size
   - Define alerting objectives (MTTA, MTTR, false-positive tolerance).
   - Define report consumers and cadence.
    - Weekly summary report generation and email delivery.

2. Define target deployment model
   - Choose runtime: Kubernetes for MVP.
   - Split components: collector-service, Prometheus, Alertmanager, Grafana, backoffice API/UI, Mimir (or consider alternatives like Thanos or VictoriaMetrics).

3. Build metrics collection service (remote)
   - Implement connection manager for DB targets with encrypted secrets.
   - Implement query scheduler and execution workers per DB connector.
   - Export metrics in Prometheus exposition format.
   - Add health metrics for the collector itself (query latency, failures, staleness).
   - Defer file-ingestion adapters until after the primary live-collection path is stable; position them as fallback-only.

4. Implement configuration/backoffice domain model
   - Entities: DatabaseConnection, MetricQuery, QueryTemplate, AlertPolicy, NotificationChannel, ReportSchedule, User/Role, AuditEvent.
   - CRUD APIs with validation and versioning for query changes.
   - UI screens: connection onboarding, query editor/tester, policy editor, schedule management.

5. Integrate Prometheus/Mimir
   - Configure Prometheus scrape jobs for collector instances.
   - Define recording rules for normalized metrics and aggregations.
   - Optionally wire remote_write to Mimir for retention >30 days and HA scale.

6. Build Grafana dashboards
   - Provision dashboards as code (JSON/Terraform) for reproducibility.
   - Create overview + deep-dive dashboards per DB type.
   - Include panels for availability, throughput, errors, latency, locks, replication, storage, connection saturation.

7. Implement adaptive alerting
   - Start with hybrid strategy:
     - Static guardrails for critical hard limits.
     - Dynamic anomaly bands for variable workloads.
   - Build baseline jobs to compute rolling statistics.
   - Add debounce, cooldown, and multi-signal correlation.
   - Route alerts via Alertmanager/Grafana channels (Slack/Email/PagerDuty).

8. Implement reporting subsystem
   - Scheduled report generation using templated queries.
   - Include trend deltas, anomalies, SLO burn, top-N regressions, muted alerts summary.
   - Deliver via email and store report artifacts for backoffice download.

9. Security, compliance, and reliability hardening
   - Secrets management (Vault/KMS/SSM), per-tenant encryption.
   - RBAC, SSO/OIDC for backoffice and Grafana.
   - Audit trails and immutable action logs.
   - Rate-limiting and query sandboxing to protect DB targets.
   - Implement collector failure isolation, bounded retries, freshness signals, safe reloads, and deployment recovery before adding replicas.
   - Define target ownership/takeover and downstream resilience against explicit recovery-time and data-gap objectives; validate failure scenarios before claiming HA.
   - Follow the [planned collector HA design](../architecture/database-observability.md#collector-recovery-and-high-availability-planned).

10. Testing and validation
   - Unit tests for connectors, query parser/validator, anomaly calculations.
   - Integration tests with containerized DB fixtures.
   - Load tests for metric ingestion and alert throughput.
   - Chaos tests for DB unavailability, network partitions, slow queries.

11. Rollout strategy
   - MVP in staging with 1–2 DB engines and a few production-like targets.
   - Observe false-positive/false-negative rates; tune adaptive alerting.
   - Progressive production rollout by team or environment.

12. Late-roadmap custom collector / Alloy comparison
   - Once the core workflow and reliability baseline are validated, run a parallel comparison on a dedicated branch using equivalent non-production SQL Server workloads and isolated telemetry.
   - Compare coverage, custom probes/evidence, safety, database load, recovery, configuration integration, and operational cost; assess future Oracle support separately.
   - Keep both implementations available as an option if distinct needs justify it, with explicit per-target ownership and a shared telemetry contract. No replacement or production adoption is decided yet.
   - Follow the [comparison design and decision criteria](../architecture/database-observability.md#custom-collector-and-alloy-comparison-late-roadmap).

## Files/components likely to change (implementation phase)
- `infra/`
  - `docker-compose.yml` or `k8s/*.yaml`
  - `prometheus/prometheus.yml`
  - `prometheus/rules/*.yml`
  - `alertmanager/alertmanager.yml`
  - `grafana/provisioning/datasources/*.yaml`
  - `grafana/provisioning/dashboards/*.yaml`
  - `grafana/dashboards/*.json`
- `collector-service/`
  - `connectors/*`
  - `scheduler/*`
  - `metrics_exporter/*`
  - `config/*`
- `backoffice-api/`
  - `models/*`
  - `routes/*`
  - `services/*`
  - `migrations/*`
- `backoffice-ui/`
  - `pages/connections/*`
  - `pages/queries/*`
  - `pages/alerts/*`
  - `pages/reports/*`
- `reporting-service/`
  - `jobs/*`
  - `templates/*`
  - `delivery/*`
- `docs/`
  - `architecture.md`
  - `runbooks/*.md`

## Test and verification plan
- Functional
  - Verify a new DB connection can be created and validated from backoffice.
  - Verify query changes are versioned, approved (if needed), and applied safely.
  - Verify dashboards populate within expected scrape intervals.
- Alerting
  - Simulate threshold breach and anomaly patterns.
  - Validate deduplication/suppression and routing behavior.
- Reporting
  - Verify schedules trigger correctly and reports include expected sections/data.
- Non-functional
  - Confirm no software deployment on DB servers.
  - Measure end-to-end latency from metric generation to dashboard/alert.
  - Validate security controls (RBAC, audit, secret handling).

## Risks and tradeoffs
- Adaptive alerting complexity can increase operational overhead; start simple and iterate.
- Remote SQL polling may add DB load if query design is poor; enforce query budgets and sampling limits.
- Multi-DB support increases connector maintenance; prioritize by business impact.
- File-based ingestion reduces dependency on direct connectivity but increases freshness, validation, and operator workflow risks; keep it as a last-resort path.
- Mimir improves scale/retention but adds infra complexity; defer for MVP unless required.

## Open questions
- Which DB engines and versions are in phase 1?
- Expected scale: number of DB instances, scrape interval, retention period?
- Preferred notification channels and on-call tooling?
- Is multi-tenancy required in backoffice/Grafana from day one?
- Compliance constraints (PII handling, retention, audit requirements)?

## Suggested MVP slice (4–6 weeks)
- PostgreSQL + MySQL connectors.
- Prometheus + Grafana + Alertmanager (Mimir optional later).
- Backoffice: connection CRUD, query CRUD, basic alert policy CRUD.
- Adaptive alerting v1: static + simple rolling baseline.
- Weekly summary report generation and email delivery.
