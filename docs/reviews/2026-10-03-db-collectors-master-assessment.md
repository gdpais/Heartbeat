# db-collectors update assessment — 2026-10-03

Recommendation: fast-forward `db-collectors` to the freshly fetched
`origin/master`, then finish and validate the collector on that baseline.
Finishing on the old branch would duplicate fixes already present in master
and postpone validation against the actual integration configuration.

## Repository state

- Current branch: `db-collectors`, commit `7c71f4c`.
- Fetched `origin/master`: `edaf421`.
- `git rev-list --left-right --count db-collectors...origin/master`: `0 25`.
  There are no collector-branch-only commits; the update is a fast-forward,
  with no committed-content merge conflicts at these revisions.
- Local `master` is stale at `bc9b1c8`; use `origin/master` for the update.
- No merge, commit or push was performed during this assessment.
- Pre-existing uncommitted edits to TODO.md, docs/product/roadmap.md and
  docs/architecture/database-observability.md were preserved in a named stash:
  `preserve roadmap edits before db-collectors validation` (created on
  `otel-integrations`). Do not drop it. Reapply after updating the baseline,
  or when returning to its original branch, checking for conflicts.
- Existing local SQL Server environment/config files and docs were retained.
  The old branch lacks ignore rules for `.env.sqlserver-dev` and
  `config/integrations.local-dev.yaml`; they now appear untracked. Do not stage
  local credentials. Master includes those ignore rules.

## What updating brings

| Area | Old collector branch | Fetched master |
| --- | --- | --- |
| Probe failures | First error aborts the cycle | Target failure isolation, bounded retry/backoff |
| Readiness | Always returns success | Warm-up, cycle freshness, crashes and runtime divergence evaluated |
| Metric lifetime | No scoped stale-series cleanup | Scoped output, failure/reload stale-series cleanup and freshness metrics |
| SQL connections | Opens/pings/closes for each probe | Bounded per-target pools and credential-rotation-aware keys |
| Lifecycle | Unbounded shutdown context; minimal service wiring | Bounded shutdown, supervised lifecycle and reload rollback |
| Configuration | Collector-specific YAML loader | Shared validation/versioning/redaction manager and hot reload |
| SQL dashboards/rules | No overview dashboard; later rule fixes absent | Overview dashboard, corrected emitted metric names and rule tests |
| Platform | PostgreSQL and collector Compose services | Prometheus, Loki, Grafana, Alertmanager, OTel Collector and gateway scaffold |
| Toolchain | Go 1.26.0 in module/build image | Go 1.27.1, platform-aware builds, updated observability images and Renovate config |
| PostgreSQL tests | Uses developer Compose project; cleanup removes volumes | Explicit integration build tag and disposable projects/volumes |

Not all integrations are finished. The gateway normalizes and returns events
but does not forward them; OutSystems parsers are pending. Prometheus has no
Alertmanager `alerting` block. Helm/kind implementation is also pending.
These gaps do not justify retaining the old collector implementation.

## Security findings

Master improves the baseline but still needs collector security work:

1. `services/db-collector/internal/app/app.go:314`: `GET /admin/config`
   has no authentication check. It exposes the redacted config and reload errors.
2. `internal/config/config.go:174`: `Redacted()` copies endpoint URLs and
   notification config without masking URL userinfo or secret config values.
   Combined with unauthenticated diagnostics, configured secrets can be exposed.
3. `services/db-collector/internal/app/app.go:354`: the reload bearer token is
   compared with ordinary string equality, rather than constant-time comparison.
4. Compose publishes service ports on all host interfaces and contains local
   default credentials/admin tokens and anonymous Grafana access. Kubernetes
   endpoint isolation and per-target SQL TLS settings remain unfinished.
5. Least-privilege SQL credentials/query review, per-probe error counters,
   evidence publication and counter/rate semantics remain open.

The dependency scan and source review are separate: no reachable dependency
advisories does not resolve these application/configuration findings.

## Validation

Master was exported from Git into a temporary directory for checks; its code
was not merged into the working branch.

| Check | Result |
| --- | --- |
| Old branch collector unit/race tests and `go vet` | Passed |
| Old branch `go test ./...` | PostgreSQL tests could not access Docker in the sandbox; not a confirmed functional failure |
| Master `go test -race -timeout=90s ./...` | Passed, with Go 1.27.1 and local socket access |
| Master `go vet ./...` | Passed |
| Master Compose configuration (`config --quiet`) | Passed |
| Master `make rules-check` | Rule validation and promtool tests passed |
| Master isolated PostgreSQL integration tests | Passed (`-tags=integration -count=1 -timeout=3m ./tests`) |
| `git diff --check db-collectors origin/master` | Passed |

Official `govulncheck` v1.8.0 results:

- Old collector branch, built with installed Go 1.26.2: seven reachable
  standard-library advisories, plus seven imported-package and 31 module-level
  advisories without detected calls to the vulnerable symbols. The seven
  reachable IDs are GO-2026-6090, GO-2026-6089, GO-2026-5972,
  GO-2026-5856, GO-2026-5039, GO-2026-5037 and GO-2026-4971.
  Reachability is not proof of exploitation; platform/configuration matters
  (GO-2026-4971 is Windows-specific). This scan describes the installed build
  toolchain, not every deployed image.
- Master, scanner rebuilt with Go 1.27.1: zero reachable or imported-package
  advisories; 24 module-level advisories without detected calls to vulnerable
  symbols. Follow up on older indirect dependencies rather than treating this
  as a complete security audit.

References: [Go release history](https://go.dev/doc/devel/release),
[official Go vulnerability tooling](https://go.dev/doc/security/vuln/),
[HTTP advisory](https://pkg.go.dev/vuln/GO-2026-6089),
[TLS advisory](https://pkg.go.dev/vuln/GO-2026-6090).

No live SQL Server workload, full-stack telemetry delivery, container-image
vulnerability scan or Kubernetes deployment was validated in this assessment.

## Recommended next steps

1. On `db-collectors`, run `git merge --ff-only origin/master`.
2. Restore the preserved roadmap edits onto the updated baseline, checking the
   resulting diff; retain the stash until restoration is verified.
3. Fix diagnostics authentication/redaction and token comparison; secure
   deployment defaults and review SQL permissions/TLS.
4. Review/update flagged indirect dependencies and add vulnerability scanning
   to CI using the selected Go toolchain.
5. Finish collector signal/metric/evidence gaps, then validate live SQL Server
   → collector → Prometheus → Grafana, reloads, outages and database load.
6. Keep unfinished OutSystems forwarding/parsers and Helm delivery in their
   respective workstreams. The collector can be completed against the shared
   integration baseline without waiting for those features.
