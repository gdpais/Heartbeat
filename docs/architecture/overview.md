# Heartbeat Architecture Overview

## Core systems
- Control plane: Go API, PostgreSQL metadata, Redis for async coordination
- Collection plane: OTel Collector and DB collectors
- Storage/query plane: Prometheus, Loki, PostgreSQL
- Analysis plane: session analyzer, adaptive baselines, reporting jobs
- Presentation plane: React UI and Grafana deep links

## Boundary decisions
- PostgreSQL => durable metadata only
- YAML/Kubernetes => integration endpoints, dashboard URL templates, collector desired runtime state
- Redis => transient queues, locks, retries, short-lived caches
- Loki/Prometheus => operational evidence and telemetry

## Monorepo layout
- `apps/api`
- `apps/web`
- `services/otel-gateway`
- `services/db-collector`
- `services/session-analyzer`
- `services/reporting`
- `packages/config-schema`
- `packages/telemetry-contracts`
- `infra`
- `db/migrations`
- `tests`
