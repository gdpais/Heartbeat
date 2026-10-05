# Architecture Decisions

Short records of decisions that shape Heartbeat: the context, what was
decided, and the consequences. Add a new record instead of rewriting an old
one; mark the old record *Superseded by ADR NNNN*.

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-postgresql-metadata-only.md) | PostgreSQL stores durable product metadata only, never telemetry | Accepted |
| [0002](0002-runtime-config-in-yaml.md) | Integrations and collector desired state come from YAML/Kubernetes, with hot reload | Accepted |
| [0003](0003-helm-on-kind-and-production.md) | Deliver the platform with Helm on kind (local, CI) and production Kubernetes | Accepted; implemented for kind and CI |
| [0004](0004-core-technology-choices.md) | Core stack: Go, React, Prometheus, Loki, Grafana, Alertmanager, OTel Collector, PostgreSQL, Redis | Accepted |
| [0005](0005-production-delivery-and-operations-defaults.md) | Production on AWS EKS via Argo CD GitOps; ECR; AWS Secrets Manager + ESO; pinned latest-stable versions; Discord → Slack/Teams/WhatsApp alerts; healthchecks.io dead-man's switch; Grafana admin auth | Accepted; chart side implemented (version pins, Watchdog route, production example values); production not built |
| [0006](0006-trunk-based-development-and-tagged-releases.md) | Trunk-based development on `master`; one SemVer version per release; `vX.Y.Z` tags from release-please publish multi-arch images and the chart to GHCR; changelog from Conventional Commits; no moving tags | Accepted; in effect from 2026-10-05; workflows built, release app and first release pending |

Template:

```markdown
# NNNN. Title

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context
## Decision
## Consequences
```

Working documents such as dated plans (`docs/plans/`) and reviews
(`docs/reviews/`) stay out of git. When one of them leads to a decision, record
the outcome here.
