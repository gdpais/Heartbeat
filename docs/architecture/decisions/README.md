# Architecture Decisions

Short records of decisions that shape Heartbeat: the context, what was
decided, and the consequences. Add a new record instead of rewriting an old
one; mark the old record *Superseded by ADR NNNN*.

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-postgresql-metadata-only.md) | PostgreSQL stores durable product metadata only, never telemetry | Accepted |
| [0002](0002-runtime-config-in-yaml.md) | Integrations and collector desired state come from YAML/Kubernetes, with hot reload | Accepted |
| [0003](0003-helm-on-kind-and-production.md) | Deliver the platform with Helm on kind (local, CI) and production Kubernetes | Accepted; not implemented |
| [0004](0004-core-technology-choices.md) | Core stack: Go, React, Prometheus, Loki, Grafana, Alertmanager, OTel Collector, PostgreSQL, Redis | Accepted |

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
