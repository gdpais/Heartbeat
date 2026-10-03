# Local Development

Run the Heartbeat stack on a local kind cluster, test it, and point the DB
collector at a throwaway SQL Server.

Prerequisites: Docker, Go 1.27+, and kind, kubectl and Helm at the pinned
versions (kind v0.33, kubectl within one minor of Kubernetes 1.36, Helm 4.2+).
`make tools-check` verifies all of them and says how to fix any mismatch. Run
`make help` for the full target list.

## Start the platform

```bash
make chart-deps    # once, and after Chart.lock changes: fetch the engine charts
make kind-up       # create the cluster, build and load images, deploy
make health        # check the collector, Prometheus, Alertmanager and Grafana
make kind-down     # delete the cluster
```

`make kind-up` deploys the same Helm chart CI and production use
([ADR 0003](../architecture/decisions/0003-helm-on-kind-and-production.md)), with
[`kind.yaml`](../../infra/helm/values/kind.yaml) values. Two profiles:

| Profile | Deploys |
| --- | --- |
| `full` (default) | DB collector, OTel gateway, Prometheus, Alertmanager, Grafana, Loki, OpenTelemetry Collector |
| `minimal` | DB collector, Prometheus, Alertmanager, Grafana |

Choose one with `PROFILE=minimal make kind-up` (or `make kind-deploy`).
PostgreSQL and Redis are not deployed until a service uses them.

Services keep the ports Compose used, on 127.0.0.1 only: Grafana
http://localhost:3000 (`admin`/`admin`), Prometheus :9090, Alertmanager :9093,
collector :8082, gateway :8083, OTLP :4317/:4318. The
[metrics and endpoints reference](../reference/metrics-and-endpoints.md#local-ports-kind)
lists them.

The default config has **no SQL Server targets**, so the collector runs but
collects nothing. Use the sandbox below, or
[configure a real target](database-targets.md).

How the chart, images and cluster fit together, and how to debug them:
[Kubernetes delivery](kubernetes-local.md).

## Daily loop

| You changed | Run |
| --- | --- |
| Go code in a service | `make kind-images kind-deploy` |
| Chart templates or values | `make kind-deploy` |
| Prometheus rules or dashboards (`infra/helm/heartbeat/files/`) | `make kind-deploy` (rules reload in place; run `make rules-check` first) |

Images are tagged by content (`dev-<image id>`), so a code change always rolls
out a new pod and a stale image cannot hide behind a reused tag.

## SQL Server sandbox

A throwaway SQL Server runs as a Docker container on the kind network,
outside the cluster, and the collector is pointed at it.

```bash
make sqlserver-dev-init     # create the ignored .env.sqlserver-dev (random SA password)
make sqlserver-dev-up       # start SQL Server, create the credential Secret, deploy
make health
curl http://localhost:8082/metrics
make sqlserver-dev-down     # remove the container and its data; redeploy without it
```

| Piece | File | Tracked |
| --- | --- | --- |
| Env template | `.env.sqlserver-dev.example` | yes |
| Env file (random SA password, mode 600) | `.env.sqlserver-dev` | no |
| Values pointing the collector at the container | `infra/helm/values/sqlserver-dev.yaml` | yes |

The container is named `heartbeat-sqlserver-dev`, uses `MSSQL_PID=Developer`,
publishes 1433 only on `127.0.0.1:11433`, runs the AMD64 image (emulated on
ARM hosts) and must pass a SQL health check before deploying. Once started,
later `make kind-deploy` runs keep the target until `make sqlserver-dev-down`.
It uses `sa` and trusts the container's self-signed certificate
(`dbCollector.sqlserver.trustServerCertificate`). Both are acceptable only
because the container is throwaway; production targets need a dedicated login
and a trusted certificate
([login permissions](database-targets.md#collector-login-permissions)).

## Run tests

| Command | What it runs | Needs |
| --- | --- | --- |
| `make test` | All Go unit and repository contract tests | Go |
| `make test-race` | The same suite with the race detector | Go |
| `make vet` | Go static checks | Go |
| `make rules-check` | promtool rule validation and unit tests | Docker |
| `make test-integration` | PostgreSQL migration and index tests (`-tags=integration`) | Docker (`postgres:17`) |
| `make chart-check` | `helm lint`, then renders every values profile and checks it (below) | Helm, `make chart-deps` |
| `make kind-e2e` | Acceptance checks on a temporary kind cluster (below) | Docker, kind, kubectl, Helm, jq |

CI (`.github/workflows/db-collector-ci.yml`) runs all of them, and repeats
`chart-check` with Helm 3.

`make chart-check` renders the chart for each profile (defaults, kind,
minimal, SQL Server sandbox, production example) and fails if rendering is not
deterministic, an object is cluster-scoped or a Helm hook, the rendered
`integrations.yaml` fails the Go loader, the collector is not a single replica,
or Prometheus, Alertmanager or Grafana points at a Service the profile does not
deploy.

`make kind-e2e` creates its own cluster (`heartbeat-e2e-<id>`, private
kubeconfig, no host ports) and SQL Server container, so it runs next to your
dev cluster without touching it, and removes only what it created. It checks
the ADR 0003 acceptance criteria: every workload ready; the running collector
is the image just built; the SQL target's metrics are fresh in Prometheus and
visible through Grafana; a failed target is isolated; the Watchdog alert reaches
Alertmanager; a valid ConfigMap change hot-reloads without a restart; an invalid
one is rejected while collection continues; a SQL Server outage shows as a failed
target without restarting the collector; a collector update never runs two
collectors (it prints the rollout time, collection gap and SQL Server session
count); and Prometheus data and Alertmanager silences survive pod replacement.
It takes about 10 minutes.

### How integration tests stay isolated

Integration tests never touch your developer stack:

- Each test uses `infra/docker-compose.test.yml` (a disposable PostgreSQL
  fixture, not the platform) under a random `heartbeat-test-<id>` project, with
  no published ports and tmpfs storage.
- Only `db/migrations` is bind-mounted, read-only.
- `COMPOSE_*` overrides and dotenv files are ignored; your selected Docker
  daemon is kept.
- Cleanup is registered before startup and removes only that test's project.
  Tests run concurrently, and separate test processes are isolated too.

If a test process is killed, find the leftover project with
`docker compose ls` and remove only that `heartbeat-test-<id>` project using the
test Compose file.

## Build images

`make kind-images` builds both images for the kind node's own architecture and
loads them. To build by hand: both Dockerfiles compile for Docker's selected
target platform; the build stage runs natively and cross-compiles Go, so no CPU
emulation is needed.

```bash
docker build --platform linux/arm64 -t heartbeat/db-collector:dev -f services/db-collector/Dockerfile .
docker build --platform linux/amd64 -t heartbeat/db-collector:amd64 -f services/db-collector/Dockerfile .
```

Use `services/otel-gateway/Dockerfile` and `heartbeat/otel-gateway` for the
gateway. BuildKit supplies `TARGETOS` and `TARGETARCH`; do not override them
separately from `--platform`. `.dockerignore` keeps everything but Go sources
out of the build context.

## Change collector config without restarting

Edit the `integrations` values and run `make kind-deploy`. The collector is not
restarted: the ConfigMap reaches the pod within about a minute and the
collector polls the file every 5s. To apply it sooner:

```bash
curl -X POST -H "Authorization: Bearer local-admin-token" http://localhost:8082/admin/config/reload
```

`local-admin-token` is the kind-only value of the `heartbeat-runtime` Secret.
Credential variables are read at startup; restart the collector
(`kubectl -n heartbeat rollout restart statefulset/db-collector`) when they
change. See the [configuration reference](../reference/configuration.md).
