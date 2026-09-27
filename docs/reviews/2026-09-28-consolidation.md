# Consolidation review — 2026-09-28

Scope: all pending changes on `otel-integrations`, reviewed against `d8fe228`,
plus the test-isolation fixes made during consolidation.

## Changes and purpose

| Files | Behavior and purpose |
| --- | --- |
| `internal/config/config.go`, `config_test.go` | Redaction copies nested collectors, targets, probes, notification maps, and endpoint template maps so diagnostics cannot mutate active configuration. Validate optional Loki/Alertmanager endpoint URLs and top-level secret references. Regression tests cover rejected candidates and copy independence. This does not mask arbitrary channel config values or URL credentials. |
| `internal/config/manager.go` | Serialize candidate validation and application; publish the new snapshot only after the apply callback succeeds. Distinguish invalid candidates from application errors and use structural equality when diffing collectors. The old snapshot survives synchronous failure; runtime rollback is not implemented. |
| `services/db-collector/internal/app/app.go`, `app_test.go` | Add `/healthz` while retaining `/healthcheck`; route HTTP, SIGHUP, and watcher reloads through the same manager apply path. HTTP returns 400 for invalid configuration and 500 for synchronous application failure. Tests verify health aliases, reload authorization/method handling, and invalid-candidate preservation. |
| `services/db-collector/cmd/db-collector/main.go`, `main_test.go`, connector `connector.go` | Wire an explicit certificate-trust opt-in through the app into the SQL Server DSN. TLS remains enabled by default; certificate verification remains enabled unless opted out. Test unset, invalid, false, and true environment values. No live SQL Server TLS test was performed in this review. |
| `services/db-collector/internal/app/lifecycle.go`, `internal/collectors/runner.go`, service README | Explain lifecycle supervision, probe timeouts, row decoding, scheduling, and the no-op evidence sink. Lifecycle/runner changes are comments; they do not add retries, HA, or durable evidence storage. README now states the reload/readiness limitations. |
| `.env.sqlserver-dev.example`, `Makefile`, SQL Server dev Compose overlay | Generate a per-machine random password with mode 0600, preserve existing credentials, fail on generation errors, and avoid printing interpolated secrets during configuration checks. Bind SQL Server to loopback; use an amd64 image, SQL health gating, and dev-only certificate trust. |
| `tests/*`, `infra/docker-compose.test.yml` | Remove developer-stack lifecycle commands from tests. Gate database tests behind `integration`; give every test a random Compose project, tmpfs storage, no host ports, and a read-only migrations mount. Ignore Compose environment overrides/dotenv files. Bound commands, register cleanup before startup, report cleanup failures, and run the database tests concurrently. A Docker-free regression test enforces fixture safety boundaries. |
| `Makefile`, `.github/workflows/db-collector-ci.yml` | Run the entire repository in `make test`; add explicit integration/race/vet targets. CI covers `otel-integrations` pushes and runs each validation stage. |
| Root README, `docs/runbooks/local-dev.md`, `docs/runbooks/database-targets.md` | Explain testing and database onboarding, credential mapping, dev boundaries, and restart/reload behavior. Clarify that Compose validation does not validate integration YAML and host-shell exports alone do not enter containers. |
| `docs/architecture/overview.md`, `overview-diagrams-draft.md` | Document current runtime separately from planned control-plane workflows and storage ownership. Preserve the alternative diagram revision as an explicitly unadopted draft; it uses data-flow arrows while the current overview labels request/export direction. |
| `Plan.md`, `TODO.md`, architecture/database-observability and phased-roadmap docs | Record recovery/HA prerequisites, deferred file ingestion, future Oracle scope, and a late-stage custom collector/Alloy comparison. These are plans, not implemented guarantees or a decision to replace the collector. |

## Findings

1. **Fixed: destructive test coupling.** Migration tests previously used
   `infra/docker-compose.yml` and called `down -v` on its default project. They
   could remove developer-stack volumes and interfere with another test process.
   All migration operations and cleanup now target unique disposable projects.
2. **Fixed: incomplete diagnostic copy.** The pending redaction fix copied
   collector and notification structures but still shared endpoint template maps.
   Consolidation copies those maps and tests nested mutation independence.
3. **Open: runtime rollback and readiness.** `ReloadApplying` protects the config
   snapshot only. Reconciliation can partially succeed, and asynchronous poller
   failure happens after `Start` returns. `/readyz` still returns HTTP 200 after
   collector failure. These remain tasks in TODO section 15.0; this review does
   not claim a transactional reload or healthy collection.
4. **Open: Prometheus recording rules.** `infra/prometheus/rules/heartbeat.rules.yml`
   references `heartbeat_sqlserver_wait_seconds_total`,
   `heartbeat_sqlserver_blocking_sessions`, and `heartbeat_sqlserver_connections`.
   The current catalog emits `heartbeat_sqlserver_wait_time_ms`,
   `heartbeat_sqlserver_blocked_requests`, and `heartbeat_sqlserver_sessions`.
   The SQL Server Grafana dashboard already uses the current catalog names.
   Correct names and gauge/counter semantics before relying on these rules.
5. **Open: diagnostic security.** GET admin config is unauthenticated; readiness
   can include raw errors; arbitrary channel config and URL userinfo are not fully
   redacted. The certificate bypass is process-wide. Existing TODO section 15.2
   tracks these limitations; the dev overlay is not production hardening.

## Validation evidence

- `make test`, `make test-race`, `make vet`, and `git diff --check`: passed.
- `make test-integration`: passed using real Docker/PostgreSQL.
- Two concurrent processes ran `go test -tags=integration -race -count=2
  -shuffle=on -v -timeout=10m ./tests` with conflicting developer-style
  `COMPOSE_PROJECT_NAME=infra` and `COMPOSE_FILE=infra/docker-compose.yml`.
  All twelve independent PostgreSQL projects passed and cleaned up.
- Docker container identities and volume names were identical before and after
  the concurrent run. The existing Kubernetes control-plane container remained.
  This verifies resource isolation; no developer application stack was running.
- Base, integration, and SQL Server overlay Compose syntax validation: passed.
  Overlay validation used disposable placeholder credentials, not local secrets.
- Development credential initialization in a temporary directory: generated
  matching random values, mode 0600, and preserved the file on a second run.
- Live SQL Server collection, dashboard queries, alert delivery, production
  permissions, and HA/failure recovery were not exercised by these checks.

The remaining findings are pre-existing runtime/product gaps, recorded separately
from this consolidation's regression and isolation checks.
