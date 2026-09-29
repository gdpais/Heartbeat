# Local Development Runbook

## Start the local stack
`make up`

## Start the local stack with a dev-only SQL Server target
`make sqlserver-dev-init`

`make sqlserver-dev-up`

## Validate service config
`make config`

This validates Compose syntax; it does not validate `integrations.yaml`.
The service validates integration configuration on startup and reload.

## Application image platforms

Both application Dockerfiles compile for Docker's selected target platform.
The build stage runs natively, so cross-compiling the Go executable does not
require CPU emulation. The default target follows the selected builder; choose
the platform explicitly when building for a different environment.

For an ARM64 development image:

```bash
docker build --platform linux/arm64 -t heartbeat/db-collector:local -f services/db-collector/Dockerfile .
```

For an AMD64 production candidate:

```bash
docker build --platform linux/amd64 -t heartbeat/db-collector:amd64 -f services/db-collector/Dockerfile .
```

Use `services/otel-gateway/Dockerfile` and the corresponding
`heartbeat/otel-gateway` image name for the gateway. BuildKit supplies `TARGETOS`
and `TARGETARCH`; do not override these independently of `--platform`.
The Docker-hosted SQL Server development target retains its separate AMD64
platform setting.

## Test isolation

- `make test`: all Go unit and repository contract tests; no Docker commands.
- `make test-race`: the same Docker-free suite with the race detector.
- `make vet`: Go static checks.
- `make test-integration`: opt-in PostgreSQL migration/index tests; requires Docker
  Compose with `up --wait` support and access to the `postgres:17` image.

Integration tests use `infra/docker-compose.test.yml`, a random
`heartbeat-test-<id>` project per test, no published host ports, and tmpfs database
storage. Only the migrations directory is bind-mounted, read-only. Tests ignore
`COMPOSE_*` overrides and dotenv files while retaining the selected Docker daemon.
Cleanup is registered before startup and removes only that test's project.
The three database tests run concurrently; separate test processes are also
isolated. No tests start or tear down `infra/docker-compose.yml`.

If a test process is forcibly killed, inspect `docker compose ls` and remove only
the abandoned `heartbeat-test-<id>` project with the test Compose file. Never use
the developer stack's `make down` as test cleanup.

## Configure monitored database targets
See `docs/runbooks/database-targets.md`.

## Validate the SQL Server dev overlay
`make sqlserver-dev-config`

## Health checks
- PostgreSQL and db-collector: `make health`
- SQL Server dev overlay, collector, Prometheus, and Grafana: `make sqlserver-dev-health`

## Apply migrations manually
`make migrate`

## Tear down
`make down`

## Tear down the SQL Server dev overlay
`make sqlserver-dev-down`
