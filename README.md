# Heartbeat

Heartbeat is an SRE-focused monitoring platform for application observability, SQL Server deep observability, investigation workflows, adaptive alerting, and operational reporting.

The project is being built as a Go monorepo around a clear split between control-plane metadata, telemetry systems, collectors, and operator-facing workflows.

## Goal

Heartbeat aims to provide:

- application observability w/ an OutSystems-first ingestion path
- SQL Server monitoring from a separate monitoring environment
- Grafana dashboards backed by Prometheus and Loki
- investigation workflows by application, user/IP, and time range
- adaptive alerting and evidence-backed reporting
- an operator backoffice for managing metadata, targets, policies, and schedules

A hard constraint from the plan: collectors must not be installed on database hosts.

## Current status

This repo is in early MVP construction, not feature-complete.

Implemented now:

- monorepo scaffolding for apps, services, packages, infra, tests, and migrations
- local Docker Compose stack for PostgreSQL, Redis, Prometheus, Loki, Grafana, Alertmanager, and OpenTelemetry Collector
- product and architecture docs under `docs/`
- shared JSON-schema contracts under `packages/`
- PostgreSQL foundation migrations for Heartbeat control-plane metadata
- automated migration/foundation tests
- initial SQL Server DB collector service skeleton + probes + Prometheus export path

Not implemented yet:

- API/control plane
- web UI
- OTel gateway normalization pipeline
- session analyzer
- reporting service
- most integration/runtime reconciliation and K8s delivery work

TODO snapshot from `TODO.md`:

- 96 tasks completed
- 281 tasks pending
- strongest progress so far -> foundations, PostgreSQL schema, initial DB collector work

## Architecture at a glance

Core systems:

- Control plane -> Go API, PostgreSQL metadata, Redis for async coordination
- Collection plane -> OpenTelemetry Collector and DB collectors
- Storage/query plane -> Prometheus, Loki, PostgreSQL
- Analysis plane -> session analyzer, adaptive baselines, reporting jobs
- Presentation plane -> React UI and Grafana deep links

Boundary rules:

- PostgreSQL stores durable control-plane metadata only
- YAML/Kubernetes stores integration endpoints, dashboard templates, and collector desired runtime state
- Redis stores transient queues, retries, locks, and short-lived caches
- Loki/Prometheus store telemetry and operational evidence
- raw telemetry, raw logs, raw sessions, report binaries, and audit streams stay outside PostgreSQL in the MVP

## MVP scope

Phase-1/early-MVP scope from the requirements and roadmap:

- OutSystems-first application observability
- PostgreSQL-backed metadata model
- Loki + Prometheus + Grafana + Alertmanager observability stack
- session investigation by application, user/IP, and time range
- SQL Server observability metadata + safe probe assignment model
- scheduled and on-demand reporting metadata

## Repository layout

```text
apps/
  api/                 control-plane API (mostly scaffolded)
  web/                 operator UI (mostly scaffolded)
services/
  otel-gateway/        log/telemetry normalization gateway
  db-collector/        SQL Server collector service
  session-analyzer/    investigation and baseline analysis worker
  reporting/           scheduled/on-demand reporting worker
packages/
  config-schema/       integration YAML JSON schema
  telemetry-contracts/ shared payload contracts
db/
  migrations/          PostgreSQL schema migrations
infra/
  docker-compose.yml   local platform stack
  prometheus/
  loki/
  grafana/
  alertmanager/
  otel-collector/
  k8s/
docs/
  architecture/        architectural decisions and subsystem docs
  product/             requirements and phased roadmap
  runbooks/            local development instructions
tests/                 foundation, migration, and integration scaffolding
config/
  integrations.yaml    local integration/collector config sample
```

## What is actually runnable today

The local platform stack is runnable now:

```bash
docker compose -f infra/docker-compose.yml up -d
```

Exposed local endpoints from the compose stack:

- Grafana -> http://localhost:3000 (`admin` / `admin`)
- Prometheus -> http://localhost:9090
- Alertmanager -> http://localhost:9093
- Loki -> http://localhost:3100
- OTel Collector health -> http://localhost:13133/
- PostgreSQL -> localhost:5432
- Redis -> localhost:6379

The DB collector also has a real Go entrypoint at:

- `services/db-collector/cmd/db-collector/main.go`

It reads:

- metadata Postgres DSN from `HEARTBEAT_METADATA_POSTGRES_DSN`
- integration config from `config/integrations.yaml`
- listen address from `HEARTBEAT_DB_COLLECTOR_LISTEN_ADDR`

## Quick start

Prereqs:

- Docker + Docker Compose
- Go 1.26+

1. Start the local stack

```bash
docker compose -f infra/docker-compose.yml up -d
```

2. Validate the compose config

```bash
docker compose -f infra/docker-compose.yml config
```

3. Apply the foundation migration

```bash
docker compose -f infra/docker-compose.yml exec -T postgres psql -U heartbeat -d heartbeat < /migrations/0001_foundations.up.sql
```

4. Run health checks

```bash
docker compose -f infra/docker-compose.yml exec postgres pg_isready -U heartbeat -d heartbeat
docker compose -f infra/docker-compose.yml exec redis redis-cli ping
curl http://localhost:9090/-/ready
curl http://localhost:3100/ready
curl http://localhost:3000/api/health
curl http://localhost:9093/-/ready
curl http://localhost:13133/
```

5. Run tests

```bash
go test ./...
```

## Local config

Current sample integration config lives at `config/integrations.yaml` and includes:

- Grafana base URL
- Loki base URL
- Alertmanager base URL
- a sample SQL Server collector definition

This follows the repo rule that integration/runtime desired state lives in YAML, not PostgreSQL.

## Delivery phases

Roadmap summary:

- Phase 0 -> scaffolding, local stack, shared schemas, migrations
- Phase 1 -> API bootstrap, inventory, telemetry source onboarding, investigation metadata
- Phase 2 -> OutSystems normalization, alert policy management, reporting orchestration
- Phase 3 -> SQL Server collector runtime, DB-backed investigation enrichment, hardening, K8s rollout

## Key docs

Start here:

- `Plan.md`
- `TODO.md`
- `docs/product/requirements.md`
- `docs/product/phased-roadmap.md`
- `docs/architecture/overview.md`
- `docs/architecture/database-observability.md`
- `docs/architecture/session-analysis.md`
- `docs/architecture/alerting.md`
- `docs/runbooks/local-dev.md`

## Important design constraints

From the current plan and requirements:

- initial access model is intentionally minimal
- application-owned workflows derive environment via `applications.environment_id`
- PostgreSQL is not the sink for raw telemetry/log/report artifacts
- SQL Server collection must use safe, remote, read-oriented probes
- Kubernetes/runtime config reload behavior must fail closed on invalid config

## CI

GitHub Actions currently validates:

- `docker compose -f infra/docker-compose.yml config`
- `go test ./...`

## Near-term next steps

The highest-leverage unfinished work appears to be:

- freeze subsystem boundaries and ownership rules
- bootstrap `apps/api`
- bootstrap `services/otel-gateway`
- add collector self-observability and finalize SQL Server probe safety
- start session analyzer + reporting orchestration paths

Heartbeat already has the foundations and metadata model in place. What it does not have yet is the end-to-end control plane and user-facing workflow layer.
