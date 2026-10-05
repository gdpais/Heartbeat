# Changelog

All notable changes to Heartbeat. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[Semantic Versioning](https://semver.org/). From the first release on, entries
are generated from Conventional Commit messages by release-please; see
[CONTRIBUTING.md](CONTRIBUTING.md#releases) and
[ADR 0006](docs/architecture/decisions/0006-trunk-based-development-and-tagged-releases.md).

## [Unreleased]

Everything up to the end of phase 1, to be released as `0.1.0`, the first
tagged version. Details are in the [roadmap](docs/product/roadmap.md).

### Added

- DB collector for SQL Server, configured in YAML with hot reload and no agent
  on database hosts. Probes for waits, blocking, sessions, memory, storage,
  throughput, CPU, page life expectancy, buffer cache hit ratio and file I/O.
- Collector reliability: per-target failure isolation, bounded backoff,
  freshness metrics, stale-series cleanup, readiness that reflects collector
  state, reload rollback and a hard deadline for stuck probes.
- Collector security: admin token on diagnostics and reload, status-only
  `/readyz`, credential redaction, NetworkPolicy, a least-privilege login with
  `LOCK_TIMEOUT` and low deadlock priority on every probe, and an alert when
  the login is sysadmin-equivalent.
- Collector metrics as counters in base units, with per-probe durations and
  error counters, Go runtime and process metrics.
- One Helm chart for kind, CI and production, with pinned Prometheus,
  Alertmanager, Grafana, Loki and OpenTelemetry Collector; a Watchdog alert
  route; a SQL Server overview dashboard.
- Local kind workflow, a SQL Server sandbox, and acceptance checks on kind in
  CI.
- PostgreSQL metadata schema and migrations; shared JSON-schema contracts for
  configuration and telemetry.
- OTel gateway scaffold that normalizes events but does not forward them yet.
- Documentation by audience, architecture decisions 0001–0006, and an HTML
  docs site published to GitHub Pages.

### Removed

- The platform Docker Compose definition and the Kustomize bundle, replaced by
  the Helm chart (ADR 0003).
