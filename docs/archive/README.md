# Archive

Superseded planning documents, kept for history. **Do not treat them as
current.** Their lasting content has moved into the live docs.

| Document | What it was | Where its content lives now |
| --- | --- | --- |
| [original-plan.md](original-plan.md) | The first vision: a database monitoring tool with Grafana, a backoffice and adaptive alerting | Scope: [product overview](../product/overview.md). SQL Server metrics list: [signal coverage](../architecture/database-observability.md#sql-server-signal-coverage). Open questions: product overview. Collector HA and Alloy steps: [database observability](../architecture/database-observability.md). |
| [2026-05-30-mvp-implementation-plan.md](2026-05-30-mvp-implementation-plan.md) | The detailed MVP implementation plan the repository was bootstrapped from (previously `.hermes/plans/`) | Goals, non-goals, exit criteria, risks: [product overview](../product/overview.md). Technology choices: [ADR 0004](../architecture/decisions/0004-core-technology-choices.md). Service responsibilities and data flows: [operator workflows](../architecture/workflows.md). PostgreSQL model and ER diagram: [data model](../architecture/data-model.md) and [ADR 0001](../architecture/decisions/0001-postgresql-metadata-only.md). Config reload design: [ADR 0002](../architecture/decisions/0002-runtime-config-in-yaml.md). Guardrails: [architecture overview](../architecture/overview.md#architectural-guardrails). Phases and tasks: [roadmap](../product/roadmap.md) and [TODO.md](../../TODO.md). |

Known differences from the live design:

- Both plans put OutSystems before SQL Server; the SQL Server collector was
  built first.
- The MVP plan had the collector read targets from the API/PostgreSQL; the
  current design reads them from YAML ([ADR 0002](../architecture/decisions/0002-runtime-config-in-yaml.md)).
- The original plan's MVP slice used PostgreSQL and MySQL connectors and a
  `collector-service/` / `backoffice-api/` layout; neither was adopted.
- The migrated schema adds `investigations.application_id`, which the MVP
  plan's ER diagram lacks.
