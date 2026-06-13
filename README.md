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
- local Docker Compose stack for PostgreSQL and `db-collector`
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

- PostgreSQL -> localhost:5432
- db-collector -> localhost:8082

The DB collector also has a real Go entrypoint at:

- `services/db-collector/cmd/db-collector/main.go`

It reads:

- integration config from `config/integrations.yaml`
- listen address from `HEARTBEAT_DB_COLLECTOR_LISTEN_ADDR`

## Quick start

Prereqs:

- Docker + Docker Compose
- Go 1.26+

1. Start the local stack

```bash
make up
```

2. Validate the compose config

```bash
make config
```

3. Apply the foundation migration

```bash
make migrate
```

4. Run health checks

```bash
make health
```

5. Run tests

```bash
make test
```

## Kubernetes local stack

If you prefer Kubernetes locally, the repo includes a minimal bundle for the only implemented workload: `db-collector`, plus the PostgreSQL instance it needs.

Build the image and apply the bundle:

```bash
make k8s-up
```

If you are using `kind`, load the image first:

```bash
make kind-load-db-collector-image
```

Then expose the services to your laptop:

```bash
make k8s-port-forward
```

## Local config

`config/integrations.yaml`

- This file is read by the `db-collector` application itself - runtime settings
- It tells the collector where related systems live and which collectors should be enabled
- In the current code, the most important part is the `collectors:` section because that describes SQL Server collector runtime settings, enabled probes, and target connection details

`infra/k8s/local/*.yaml`

- These files tell Kubernetes what to create in the cluster
- Kubernetes reads the file and creates objects from it such as a deployment, service, secret, or config map

The local Kubernetes files now mean:

- `namespace.yaml`: creates a separate space named `heartbeat` so these resources stay grouped together
- `secret-postgres.yaml`: stores the Postgres username, password, database name, and connection string
- `configmap-config.yaml`: stores `integrations.yaml` so Kubernetes can mount it into the `db-collector` container
- `postgres.yaml`: starts a PostgreSQL container in Kubernetes and exposes it to other workloads
- `db-collector.yaml`: starts the `db-collector` container, passes its environment variables, mounts the config file, and exposes port `8082`
- `kustomization.yaml`: acts like a small manifest list that says "apply these YAML files together"

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
