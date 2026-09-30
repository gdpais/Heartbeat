# Local Development

Run the Heartbeat stack on your machine, test it, and point the DB collector at
a throwaway SQL Server.

Prerequisites: Docker with Compose (v2, `up --wait` support) and Go 1.27+.
For the Kubernetes workflow you also need kind, kubectl and Helm at the pinned
versions; `make tools-check` verifies all of them and says how to fix any
mismatch. Run `make help` for the full target list.

## Start the platform stack

```bash
make up        # docker compose -f infra/docker-compose.yml up -d
make config    # validate Compose syntax (does not validate integrations.yaml)
make migrate   # apply the PostgreSQL foundation migration
make health    # check PostgreSQL and the DB collector
make down      # stop the stack
```

The stack runs nine services: PostgreSQL, Redis, Prometheus, Loki,
Alertmanager, Grafana, OpenTelemetry Collector, the OTel gateway and the DB
collector. Ports are listed in the
[metrics and endpoints reference](../reference/metrics-and-endpoints.md#local-ports-docker-compose).

The default `config/integrations.yaml` has **no SQL Server targets**, so the
collector runs but collects nothing. Use the sandbox below, or
[configure a real target](database-targets.md).

## SQL Server sandbox

A dev-only overlay adds a local SQL Server and points the collector at it,
without editing the tracked `infra/docker-compose.yml` or
`config/integrations.yaml`. This keeps local credentials and targets out of the
config that feeds shared and production delivery.

```bash
make sqlserver-dev-init     # create ignored .env.sqlserver-dev and config/integrations.local-dev.yaml
make sqlserver-dev-config   # validate the overlay without printing secrets
make sqlserver-dev-up       # start the stack plus SQL Server
make sqlserver-dev-health   # check SQL Server, collector, Prometheus, Grafana
curl http://localhost:8082/metrics
make sqlserver-dev-down
```

| Piece | File | Tracked |
| --- | --- | --- |
| Compose overlay | `infra/docker-compose.sqlserver-dev.yml` | yes |
| Env template | `.env.sqlserver-dev.example` | yes |
| Env file (random SA password, mode 600) | `.env.sqlserver-dev` | no |
| Collector config template | `config/integrations.local-dev.example.yaml` | yes |
| Collector config | `config/integrations.local-dev.yaml` | no |

The overlay uses `MSSQL_PID=Developer`, binds SQL Server to
`127.0.0.1:11433` only, runs the AMD64 image (emulated on ARM hosts), and gates
startup on a SQL health check. It uses `sa` and trusts the container's
self-signed certificate
(`HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE=true`). Both are
acceptable only because the container is throwaway; production targets need a
dedicated login and a trusted certificate
([login permissions](database-targets.md#collector-login-permissions)).

## Run tests

| Command | What it runs | Needs Docker |
| --- | --- | --- |
| `make test` | All Go unit and repository contract tests | no |
| `make test-race` | The same suite with the race detector | no |
| `make vet` | Go static checks | no |
| `make test-integration` | PostgreSQL migration and index tests (`-tags=integration`) | yes (`postgres:17`) |

CI (`.github/workflows/db-collector-ci.yml`) runs Compose validation plus all
four commands.

### How integration tests stay isolated

Integration tests never touch your developer stack:

- Each test uses `infra/docker-compose.test.yml` under a random
  `heartbeat-test-<id>` project, with no published ports and tmpfs storage.
- Only `db/migrations` is bind-mounted, read-only.
- `COMPOSE_*` overrides and dotenv files are ignored; your selected Docker
  daemon is kept.
- Cleanup is registered before startup and removes only that test's project.
  Tests run concurrently, and separate test processes are isolated too.

If a test process is killed, find the leftover project with
`docker compose ls` and remove only that `heartbeat-test-<id>` project using the
test Compose file. Never use `make down` as test cleanup.

## Build images

Both Dockerfiles compile for Docker's selected target platform. The build stage
runs natively and cross-compiles Go, so no CPU emulation is needed.

```bash
docker build --platform linux/arm64 -t heartbeat/db-collector:local -f services/db-collector/Dockerfile .
docker build --platform linux/amd64 -t heartbeat/db-collector:amd64 -f services/db-collector/Dockerfile .
```

Use `services/otel-gateway/Dockerfile` and `heartbeat/otel-gateway` for the
gateway. BuildKit supplies `TARGETOS` and `TARGETARCH`; do not override them
separately from `--platform`.

## Change collector config without restarting

In Compose, the collector polls its config file every 2s
(`HEARTBEAT_CONFIG_WATCH_INTERVAL`), so edits to `config/integrations.yaml` apply
automatically. To force a reload:

```bash
curl -X POST -H "Authorization: Bearer local-admin-token" http://localhost:8082/admin/config/reload
```

Credential environment variables are read at startup; recreate the container
when they change. See the [configuration reference](../reference/configuration.md).

## Local Kubernetes

To run the collector on a local cluster instead, see
[Kubernetes (local)](kubernetes-local.md).
