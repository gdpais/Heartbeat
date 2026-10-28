# Local Development

How to run Heartbeat: the Heartbeat stack on a local kind cluster, the DB
collector pointed at a throwaway SQL Server, and the tests. This is the only
page you need to run the app. Two optional guides build on it:

| Guide | For |
| --- | --- |
| [Walkthrough](kind-walkthrough.md) | Your first run: installs the tools and shows what each step should look like |
| [Live demo](live-demo.md) | Reviewing what each feature does on a running stack, stage by stage |

Prerequisites: Docker, Go 1.27+, and kind, kubectl and Helm at the pinned
versions (kind v0.33, kubectl within one minor of Kubernetes 1.36, Helm 4.2+).
`make tools-check` verifies all of them and says how to fix any mismatch. Run
`make help` for the full target list.

## Run everything

From the repository root, the whole stack collecting from a local SQL Server:

```bash
make tools-check        # every line should start with ok
make chart-deps         # once, and after Chart.lock changes
make kind-up            # cluster, images, chart
make sqlserver-dev-init # once: random SA and collector passwords in .env.sqlserver-dev
make sqlserver-dev-up   # SQL Server container, collector login, redeploy against it
make health
```

Grafana is then at http://localhost:3001 (`admin`/`admin`). The sections
below explain each step; [Troubleshooting](#troubleshooting) covers the usual
failures. To stop: `make sqlserver-dev-down` and `make kind-down`.

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
| `full` (default) | DB collector, Prometheus, Alertmanager, Grafana, Loki, OpenTelemetry Collector |
| `minimal` | DB collector, Prometheus, Alertmanager, Grafana |

Choose one with `PROFILE=minimal make kind-up` (or `make kind-deploy`).
PostgreSQL and Redis are not deployed until a service uses them.

Services keep the ports Compose used, on 127.0.0.1 only: Grafana
http://localhost:3001 (`admin`/`admin`), Prometheus :9090, Alertmanager :9093,
collector :8082, OTLP :4317/:4318. The
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
| The API's contract (`apps/api/openapi.yaml`) | `make generate`, and commit the regenerated code with it ([below](#change-the-api-contract)) |

Images are tagged by content (`dev-<image id>`), so a code change always rolls
out a new pod and a stale image cannot hide behind a reused tag. Every deploy
rebuilds first when the Go sources changed since the last build, for example
after a pull ([Images](kubernetes-local.md#images)).

## SQL Server sandbox

A throwaway SQL Server runs as a Docker container on the kind network,
outside the cluster, and the collector is pointed at it.

```bash
make sqlserver-dev-init     # create the ignored .env.sqlserver-dev (random SA and collector passwords)
make sqlserver-dev-up       # start SQL Server, create the collector login and its Secret, deploy
make health
curl http://localhost:8082/metrics
make sqlserver-dev-down     # remove the container and its data; redeploy without it
```

| Piece | File | Tracked |
| --- | --- | --- |
| Env template | `.env.sqlserver-dev.example` | yes |
| Env file (random SA and collector passwords, mode 600) | `.env.sqlserver-dev` | no |
| Script that creates the collector login | `scripts/sqlserver-login.sh` | yes |
| Values pointing the collector at the container | `infra/helm/values/sqlserver-dev.yaml` | yes |

The container is named `heartbeat-sqlserver-dev`, uses `MSSQL_PID=Developer`,
publishes 1433 only on `127.0.0.1:11433`, runs the AMD64 image (emulated on
ARM hosts) and must pass a SQL health check before deploying. Its memory is
capped at 2048 MB (`SQLSERVER_MEMORY_LIMIT_MB`, at least 2048, SQL Server's
minimum); uncapped, SQL Server sizes itself to 80% of the Docker VM, and on an
8 GB Docker host a PatientZero load flood or a second kind cluster got it
killed. A container keeps the cap it was created with: `make sqlserver-dev-up`
warns when it differs, and `docker rm -f heartbeat-sqlserver-dev` followed by
`make sqlserver-dev-up` recreates it with the new one, keeping its databases,
which are on the `heartbeat-sqlserver-dev-data` volume. `make test-sqlserver`
caps its disposable container the same way. Once started,
later `make kind-deploy` runs keep the target until `make sqlserver-dev-down`.
The collector logs in as `heartbeat_collector`, which `make sqlserver-dev-up`
creates with only the
[documented grants](database-targets.md#collector-login-permissions); `sa` is
used only to set the container up. An `.env.sqlserver-dev` from before this
login existed has an `sa` collector credential, which `make sqlserver-dev-up`
refuses: run `make sqlserver-dev-init` (it adds the login and keeps the SA
password) and then `make sqlserver-dev-up` again. The collector trusts the
container's self-signed certificate
(`dbCollector.sqlserver.trustServerCertificate`), which is acceptable only
because the container is throwaway; production targets need a trusted
certificate.

### PatientZero

[PatientZero](../../tools/patientzero/README.md), the synthetic OutSystems
application Heartbeat is tested against, runs next to the chart and writes
into its own two databases on the sandbox; the collector reads its logging
database.

```bash
make patientzero-up         # needs make sqlserver-dev-up; adds PatientZero's login to .env.sqlserver-dev
make patientzero-scenario NAME=checkout-errors   # a fault scenario with its load; checks the signals
make patientzero-down       # remove it, its databases and its login
```

## Run tests

| Command | What it runs | Needs |
| --- | --- | --- |
| `make test` | First `make generate-check`: fails when the API's generated code differs from what its OpenAPI spec generates. Then all Go unit and repository contract tests, including the docs generator's and PatientZero's modules | Go, network access the first time (the generator is fetched by `go run`) |
| `make test-race` | The same suite with the race detector | Go |
| `make vet` | Go static checks | Go |
| `make vulncheck` | `govulncheck` on all three Go modules; fails on a known vulnerability the code can reach, standard library included | Go, network access |
| `make rules-check` | promtool rule validation and unit tests | Docker |
| `make test-integration` | PostgreSQL migration and index tests (`-tags=integration`) | Docker (`postgres:17`) |
| `make test-sqlserver` | Every built-in probe against a disposable SQL Server, as a login with only the documented grants; fails on duplicate series or padded label values; checks the session settings and the sysadmin-equivalence flag for that login, `sa` and a `CONTROL SERVER` login. Every OutSystems log query against its fixture tables, views and rows, as a login with only `SELECT` on them, returning every row of the current and the previous week; and the extraction of `oslog_Error` to a fake OTel Collector across cycles, a restart and a weekly log table rotation, each row exactly once (`-tags=sqlserver`). Then PatientZero's live tests (`TestLive*`) as `sa`: its entry points write `oslog_*` rows that the collector's own queries read back, and a small production-shaped log (`make patientzero-scale`'s generator) loads, matches its known answers and is measured | Docker (SQL Server 2022) |
| `make chart-check` | `helm lint`, then renders every values profile and checks it (below); then lints and renders PatientZero's chart | Helm, `make chart-deps` |
| `make kind-e2e` | Acceptance checks on a temporary kind cluster (below) | Docker, kind, kubectl, Helm, jq |

CI (`.github/workflows/ci.yml`) runs all of them, and repeats `chart-check`
with Helm 3; on a pull request the slower ones run only when the change can
affect them ([CONTRIBUTING.md](../../CONTRIBUTING.md#ci)).

`make chart-check` renders the chart for each profile (defaults, kind,
minimal, SQL Server sandbox, production example) and fails if rendering is not
deterministic, an object is cluster-scoped or a Helm hook, the rendered
`integrations.yaml` fails the Go loader, the collector is not a single replica
with a writable state volume at `HEARTBEAT_STATE_DIR` and a read-only root
filesystem, a one-replica StatefulSet is not `Parallel`, a misspelt top-level
value or a misspelt key inside a component (`dbCollector.enabeld`,
`podSecurityContext.runAsNonroot`) renders, the notes `helm install` prints
miss a required Secret, or Prometheus, Alertmanager or Grafana points at a
Service the profile does not deploy.

`make kind-e2e` creates its own cluster (`heartbeat-e2e-<id>`, private
kubeconfig, no host ports) and SQL Server container, so it runs next to your
dev cluster without touching it, and removes only what it created. It checks
the ADR 0003 acceptance criteria: every workload ready; the running collector
is the image just built; the SQL target's metrics are fresh in Prometheus and
visible through Grafana, collected by a login that is not `sysadmin`; a failed
target is isolated; the Watchdog alert reaches Alertmanager; an
`outsystems_logs` collector, added by a hot reload, extracts a fixture
`oslog_Error` table to Loki and counts the rows in Prometheus; a valid ConfigMap
change hot-reloads without a restart; an invalid one (a misspelled probe) is
rejected while collection continues, shows in the reload metric and alert, and
clears with the next valid one; a SQL Server outage shows as a failed target without
restarting the collector; a collector update never runs two
collectors (it prints the rollout time, collection gap and SQL Server session
count); a deploy replaces a collector pod that never became Ready, and moves a
StatefulSet created with `OrderedReady` to `Parallel` without restarting a pod
whose template it does not change; and Prometheus data and Alertmanager
silences survive pod replacement.
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

`make kind-images` builds the collector image for the kind node's own
architecture and loads it. To build by hand: the Dockerfile compiles for
Docker's selected target platform; the build stage runs natively and cross-compiles Go, so no CPU
emulation is needed.

```bash
docker build --platform linux/arm64 -t heartbeat/db-collector:dev -f services/db-collector/Dockerfile .
docker build --platform linux/amd64 -t heartbeat/db-collector:amd64 -f services/db-collector/Dockerfile .
```

BuildKit supplies `TARGETOS` and `TARGETARCH`; do not override them
separately from `--platform`. `.dockerignore` keeps everything but Go sources
out of the build context.

Released images are built by CI, not by hand: see
[releases](../../CONTRIBUTING.md#releases).

## Change collector config without restarting

Edit the `integrations` values and run `make kind-deploy`. The collector is not
restarted: the ConfigMap reaches the pod within about a minute and the
collector polls the file every 5s. To apply it sooner:

```bash
curl -X POST -H "Authorization: Bearer local-admin-token" http://localhost:8082/admin/config/reload
```

`local-admin-token` is the kind-only value of the `heartbeat-runtime` Secret.
Credentials need no restart either: the collector reads the mounted
credentials Secret on every connection, and the kubelet updates it within
about a minute. See the [configuration reference](../reference/configuration.md#credential-resolution).

## Change the API contract

The API is not deployed yet; run it with `go run ./apps/api/cmd/api` (it
listens on `:8081`, see the
[endpoints](../reference/metrics-and-endpoints.md#api-8081)). Its contract
is [`apps/api/openapi.yaml`](../../apps/api/openapi.yaml), written by hand
([ADR 0013](../architecture/decisions/0013-api-design-and-subsystem-boundaries.md)).
After editing it, regenerate the server types and commit them with the spec:

```bash
make generate   # writes apps/api/internal/openapi/openapi.gen.go
```

`make test`, and so CI, fails when the committed code differs from what the
spec generates. The generator, oapi-codegen, is pinned in the Makefile
(`OAPI_CODEGEN_VERSION`) and fetched by `go run`, so there is nothing to
install. The handlers in `apps/api/internal/httpapi` implement the generated
`StrictServerInterface`: a new operation does not compile until it has one,
and a handler can only return the responses the spec declares.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `make tools-check` reports a wrong kubectl | Another kubectl (often Docker Desktop's) is earlier in `PATH`; put Homebrew's first |
| `make kind-up` fails with `port is already allocated` | Something else holds one of the ports in the [port list](../reference/metrics-and-endpoints.md#local-ports-kind) (often an old Compose stack: `docker compose ls`; find the holder with `lsof -nP -iTCP:<port> -sTCP:LISTEN`). Grafana uses 3001, so a Grafana on 3000 does not clash. Stop the holder and rerun, or move that port: delete the half-created cluster (`make kind-down`), then, for example, `sed 's/hostPort: 3001,/hostPort: 3002,/' infra/kind/cluster.yaml > .tmp/kind-cluster.yaml` and `KIND_CONFIG=.tmp/kind-cluster.yaml make kind-up`. `make health` still checks 3001, so check a moved Grafana with `curl localhost:3002/api/health` |
| kind node fails with a cgroup v1 error | kubelet 1.36 needs cgroup v2. Docker Desktop has it; on Linux, use a distribution with cgroup v2 |
| Pods stay `ErrImageNeverPull` | The images were not loaded into this cluster (for example after recreating it by hand). Run `make kind-images kind-deploy` |
| `make health` or the browser get "connection reset" or `Empty reply from server` right after Docker or the laptop restarts, or right after `make sqlserver-dev-up` | kube-proxy inside the node is still starting, or still routing the host port to the collector pod that was just replaced. Wait a few seconds (up to a minute after a restart) and rerun |
| Image pulls fail with `429 Too Many Requests` | Docker Hub's anonymous rate limit. `docker login`, wait, and rerun |
| SQL Server never becomes healthy on Apple silicon | Rosetta emulation is off in Docker Desktop, or Docker has too little memory |
| `make sqlserver-dev-up` says the credential must be a dedicated login, not `sa` | `.env.sqlserver-dev` predates the least-privilege login. Run `make sqlserver-dev-init` (it adds the login and keeps the SA password), then `make sqlserver-dev-up` again |
| `helm upgrade` fails with `resource StatefulSet/heartbeat/db-collector not ready ... 1 pods have failed` | The collector is crash-looping on the image or config just deployed; `kubectl --context kind-heartbeat -n heartbeat logs db-collector-0` says why. Fix it and rerun `make kind-deploy`: the StatefulSet replaces the broken pod (`podManagementPolicy: Parallel`) |
| `make kind-deploy` prints `recreating StatefulSet db-collector with podManagementPolicy Parallel` | Once, on a cluster deployed before the chart moved to `Parallel`, which cannot change in place: it deletes the StatefulSet with `--cascade=orphan` and the deploy creates it again. The pod keeps running until the new StatefulSet replaces it, which it does only if the deploy changes the pod template (after a `kubectl rollout restart` it does) |
| `helm upgrade` fails with a field-manager conflict | Something was edited by hand with `kubectl`. See [Kubernetes delivery](kubernetes-local.md#working-with-it) |
