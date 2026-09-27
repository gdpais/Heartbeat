# Heartbeat Phased Roadmap

## Phase 0
- Establish monorepo scaffolding
- Establish local observability stack
- Define shared schemas and contracts
- Convert PostgreSQL ER model into migrations and automated checks

## Phase 1
- API/control-plane bootstrap
- Environment/application inventory
- Telemetry source onboarding
- Investigation metadata and job orchestration

## Phase 2
- OutSystems normalization and dashboards
- Alert policy management and rule rendering
- Reporting schedule and run orchestration

## Phase 3
- SQL Server collector runtime and safe probe execution
- Investigation enrichment from DB evidence
- Operational hardening and K8s rollout
- Collector endpoint hardening: authenticate admin/diagnostic endpoints, complete config redaction, and restrict network access to the collector port
- Collector recovery and HA: isolate target/probe failures, add bounded retries and freshness signals, validate readiness and safe reloads, and define replica ownership/takeover
- Define recovery/data-gap objectives and validate collector, node, network, and downstream outages before claiming HA
- See [planned collector HA design](../architecture/database-observability.md#collector-recovery-and-high-availability-planned)

## Phase 4
- File-based data ingestion from uploaded/exported files as a fallback when direct database connectivity is not possible
- Validation, lineage, and freshness safeguards for fallback file ingestion
- Keep file ingestion explicitly lower priority than SQL Server and other live database integrations

## Phase 5 — Late-stage collector comparison
- After the core monitoring workflow and reliability baseline are validated, compare the custom collector and Grafana Alloy in parallel on a dedicated comparison branch
- Use equivalent non-production workloads, isolated metric identities, and controlled query load; measure coverage, safety, performance, recovery, integration fit, and maintenance cost
- Evaluate SQL Server first and future Oracle extensibility separately
- Keep the outcome open: custom collector, Alloy for suitable needs, or both available with explicit target ownership and a shared telemetry contract
- Record the evidence and review the adoption decision before merging; the comparison does not block earlier collector hardening
- See [comparison design and decision criteria](../architecture/database-observability.md#custom-collector-and-alloy-comparison-late-roadmap)
