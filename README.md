# Heartbeat

Heartbeat is an SRE-focused monitoring platform. It covers deep SQL Server
observability, application telemetry (OutSystems first), session-centric
investigation, adaptive alerting and operational reporting, built on Grafana,
Prometheus, Loki and OpenTelemetry. It complements commercial APMs rather than
replacing them.

Collection runs from a separate monitoring environment: **no agents are
installed on monitored database hosts.**

**Documentation: [docs/README.md](docs/README.md)**. Start with the
[product overview](docs/product/overview.md) and the
[architecture overview](docs/architecture/overview.md).

## Status

Early MVP construction. See the [roadmap](docs/product/roadmap.md) and
[TODO.md](TODO.md).

| Component | Path | Status |
| --- | --- | --- |
| DB collector (SQL Server) | `services/db-collector` | Working: probes, hot reload, failure isolation, readiness. Hardening in progress. |
| OTel gateway | `services/otel-gateway` | Partial: normalizes events, not yet forwarding |
| Local platform stack | `infra/` | Compose: PostgreSQL, Redis, Prometheus, Loki, Grafana, Alertmanager, OTel Collector, both services |
| Metadata schema | `db/migrations` | Migrated and tested; no service uses it yet |
| Shared contracts | `packages/` | JSON schemas for config and telemetry payloads |
| API, web UI | `apps/api`, `apps/web` | Not started |
| Session analyzer, reporting | `services/session-analyzer`, `services/reporting` | Not started |

## Quick start

Prerequisites: Docker with Compose v2, Go 1.26+.

```bash
make up         # start the local stack
make migrate    # apply the PostgreSQL schema
make health     # check PostgreSQL and the DB collector
make test       # Docker-free Go tests
```

Grafana runs at http://localhost:3000 and Prometheus at http://localhost:9090.
The default config monitors no databases. To collect real SQL Server metrics
locally, use the sandbox:

```bash
make sqlserver-dev-init
make sqlserver-dev-up
curl http://localhost:8082/metrics
```

More in the [local development guide](docs/guides/local-development.md):
integration tests, image builds, reloads and Kubernetes. To monitor a real
database, see [onboarding SQL Server targets](docs/guides/database-targets.md).

## Architecture at a glance

Heartbeat has four planes: **telemetry** (collectors, Prometheus, Loki,
Alertmanager), **analysis jobs** (session analyzer, reporting), the **control
plane** (Go API, PostgreSQL, Redis, YAML config) and **presentation** (Grafana,
web UI). The [core systems](docs/architecture/overview.md#core-systems) table
shows each plane's components and status.

Key boundaries: PostgreSQL stores product metadata only
([ADR 0001](docs/architecture/decisions/0001-postgresql-metadata-only.md));
collectors and integrations are configured in YAML/Kubernetes with hot reload
([ADR 0002](docs/architecture/decisions/0002-runtime-config-in-yaml.md)).

## Repository layout

```text
apps/
  api/                 control-plane API (scaffold)
  web/                 operator UI (scaffold)
services/
  db-collector/        SQL Server collector
  otel-gateway/        telemetry normalization helper
  session-analyzer/    investigation and baseline worker (scaffold)
  reporting/           report worker (scaffold)
internal/config/       shared integrations config manager
packages/
  config-schema/       integrations YAML JSON schema
  telemetry-contracts/ shared payload contracts
db/migrations/         PostgreSQL schema migrations
config/                integrations.yaml and local-dev example
infra/                 Compose stacks, Prometheus, Loki, Grafana, Alertmanager,
                       OTel Collector config, local Kubernetes bundle
tests/                 repository, migration and integration tests
docs/                  documentation (index: docs/README.md)
```

Go services follow `cmd/<service>/main.go` plus `internal/<domain>/`. Empty
scaffold directories hold `.gitkeep` files until their service is implemented.

## CI

GitHub Actions (`.github/workflows/db-collector-ci.yml`) validates the Compose
file and runs `make test`, `make test-race`, `make vet` and
`make test-integration`.
