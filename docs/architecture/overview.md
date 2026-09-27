# Heartbeat Architecture Overview

Heartbeat collects telemetry from a separate monitoring environment. SQL Server
hosts run no Heartbeat agents. Metrics and logs belong in Prometheus and Loki;
PostgreSQL holds the durable metadata used by the planned control plane.

## Current runtime

This diagram reflects the checked-in services and Compose configuration. Solid
arrows show implemented calls or configured connections, not proof of a successful
live deployment. Dashed arrows identify missing connections. Arrow labels describe
the request or export direction: Prometheus **pulls** metrics and Grafana **queries**
its data sources.

```mermaid
flowchart LR
    operator["Operator"]
    sql[("Remote SQL Server<br/>No Heartbeat agent")]
    otlp["OTLP clients<br/>Logs and metrics"]

    subgraph monitoring["Heartbeat monitoring environment · current Compose definition"]
        direction LR
        config["integrations.yaml<br/>Targets, probes, credential references"]
        db["DB collector · Go<br/>Remote probes / in-memory metrics<br/>:8082 /metrics"]
        otel["OpenTelemetry Collector<br/>OTLP :4317 / :4318<br/>Metrics :8889"]
        gateway["OTel gateway helper · Go<br/>Normalize-and-return endpoint<br/>Alert webhook · :8083"]
        prom[("Prometheus<br/>Metrics and rule evaluation")]
        loki[("Loki<br/>Logs")]
        grafana["Grafana<br/>Provisioned dashboards"]
        am["Alertmanager<br/>Configured webhook route"]
        subgraph foundation["Provisioned foundations · application workflows pending"]
            pg[("PostgreSQL<br/>Metadata schema")]
            redis[("Redis<br/>Future async coordination")]
        end
    end

    config -->|"Load / reload desired state"| db
    config -->|"Load integration settings"| gateway
    db -->|"Remote read-oriented SQL probes"| sql
    otlp -->|"OTLP push"| otel
    otel -->|"Export logs"| loki
    prom -->|"Scrape /metrics"| db
    prom -->|"Scrape :8889"| otel
    prom -->|"Scrape helper metrics"| gateway
    grafana -->|"PromQL"| prom
    grafana -->|"LogQL"| loki
    operator -->|"Dashboards / Explore"| grafana
    prom -.->|"Alert forwarding not configured"| am
    am -->|"POST /v1/heartbeat/alerts"| gateway

    classDef service fill:#e0f2fe,stroke:#0369a1,color:#0c4a6e
    classDef store fill:#dcfce7,stroke:#15803d,color:#14532d
    classDef partial fill:#fef3c7,stroke:#b45309,color:#78350f
    classDef external fill:#f1f5f9,stroke:#64748b,color:#0f172a
    class db,otel,grafana service
    class prom,loki store
    class gateway,am,pg,redis partial
    class operator,sql,otlp,config external
```

The default integration file has no SQL Server targets. Configure a target using
the [database onboarding runbook](../runbooks/database-targets.md), or use the
[local development sandbox](../runbooks/local-dev.md), to collect database metrics.
The SQL metric path has no PostgreSQL dependency, Redis queue, or durable
collector-side sample buffer.

The gateway currently returns a normalized event to its HTTP caller; it does not
forward that event into the OTel Collector. Its alert endpoint accepts and counts
webhook payloads but does not persist alert events or deliver notifications.
Prometheus has scrape/rule configuration but no `alerting` block connecting it to
Alertmanager. OutSystems ingestion is still pending. Routine infrastructure
self-scrapes are omitted above for clarity.

## Target platform

The design below adds the intended operator workflows around the telemetry path.
**Blue/green nodes** exist as services or infrastructure; **amber nodes** are
partial; **dashed gray nodes** are planned. Solid arrows are implemented/configured
connections; dashed arrows are planned integrations. An existing store does not
imply that its planned consumers are implemented.

```mermaid
flowchart TB
    operator["Operator / SRE"]

    subgraph presentation["Presentation"]
        web["React web UI<br/>PLANNED"]
        grafana["Grafana<br/>Dashboards / Explore"]
    end

    subgraph control["Control plane and jobs"]
        api["Go API · PLANNED<br/>Inventory, policies, investigations, schedules"]
        pg[("PostgreSQL<br/>Durable metadata")]
        redis[("Redis<br/>Transient queues, locks, retries")]
        workers["Session analyzer + reporting · PLANNED<br/>Correlation, baselines, report generation"]
        artifacts["Report artifact storage · PLANNED<br/>Location / retention to be decided"]
    end

    subgraph collection["Collection · separate from monitored database hosts"]
        config["YAML / Kubernetes config<br/>Endpoints, templates, desired collector state"]
        db["DB collector · Go<br/>Remote SQL probes"]
        gateway["OTel gateway helper · PARTIAL<br/>OutSystems normalization pending"]
        otel["OpenTelemetry Collector<br/>Ingest, process, route"]
    end

    subgraph telemetry["Telemetry storage and evaluation"]
        prom[("Prometheus<br/>Metrics / executable rules")]
        loki[("Loki<br/>Logs / investigation evidence")]
        am["Alertmanager<br/>Group, deduplicate, route"]
    end

    sql[("SQL Server<br/>Remote monitored targets")]
    apps["Application telemetry<br/>OutSystems first · PLANNED"]
    delivery["Notification destinations<br/>Email / webhooks · PLANNED"]

    operator -.->|"Manage / investigate"| web
    operator -->|"Inspect dashboards"| grafana
    web -.->|"HTTPS API"| api
    web -.->|"Contextual deep links"| grafana
    api -.->|"Read / write metadata"| pg
    api -.->|"Enqueue jobs"| redis
    workers -.->|"Consume jobs / coordinate"| redis
    workers -.->|"Read metadata / save summaries and URIs"| pg
    workers -.->|"Query metrics"| prom
    workers -.->|"Query logs"| loki
    workers -.->|"Write report files"| artifacts
    api -.->|"Provision policy / baseline rules"| prom
    api -.->|"Provision notification routes"| am
    config -.->|"Integration endpoints / deep-link templates"| api
    config -->|"Targets / probes / credential references"| db
    config -->|"Integration settings"| gateway
    db -->|"Read-oriented probes"| sql
    apps -.->|"Source ingestion / parsing"| gateway
    gateway -.->|"Normalized telemetry export"| otel
    prom -->|"Scrape metrics"| db
    prom -->|"Scrape exported metrics"| otel
    otel -->|"Export logs"| loki
    grafana -->|"PromQL"| prom
    grafana -->|"LogQL"| loki
    prom -.->|"Firing / resolved alerts"| am
    am -.->|"Deliver notifications"| delivery

    classDef service fill:#e0f2fe,stroke:#0369a1,color:#0c4a6e
    classDef store fill:#dcfce7,stroke:#15803d,color:#14532d
    classDef partial fill:#fef3c7,stroke:#b45309,color:#78350f
    classDef planned fill:#f8fafc,stroke:#64748b,color:#334155,stroke-dasharray:5 5
    classDef external fill:#f1f5f9,stroke:#64748b,color:#0f172a
    class db,otel,grafana,am service
    class prom,loki,pg,redis store
    class gateway partial
    class web,api,workers,artifacts,apps,delivery planned
    class operator,sql,config external
```

The two worker services are grouped for readability; session analysis owns
correlation/baselines, and reporting owns report generation. Their queue protocol,
scheduling details, artifact backend, and provisioning interfaces still need to
be defined. Baseline metadata lives in PostgreSQL; live rule evaluation belongs
in Prometheus. Audit JSONL output and optional DB evidence snapshots are omitted
from this overview; the collector's current evidence sink does not retain them.

## Data ownership and boundaries

| System | Owns | Boundary |
| --- | --- | --- |
| PostgreSQL | Inventory, policy intent, investigation summaries, evidence links, baseline versions, report-run metadata | No raw telemetry, raw sessions, audit streams, or report binaries |
| YAML / Kubernetes | Integration endpoints, Grafana URL templates, collector desired runtime state, credential references | Active collector targets/probes come from config, not PostgreSQL |
| Prometheus | Metric time series and executable recording/alert rules | Metrics are scraped from collectors; no SQL metric buffering in Redis/PostgreSQL |
| Loki | Logs and queryable operational evidence | High-cardinality investigation identifiers belong in payloads rather than index labels |
| Redis | Transient job queues, retries, locks, short-lived caches | Not the durable source of investigation or report state |
| Artifact storage (planned) | Generated report files | PostgreSQL retains the artifact URI only; backend is undecided |

Application-owned workflows derive environment from `applications.environment_id`.
Remote SQL probes must respect query budgets and least-privilege access. Collector
recovery, replica ownership, fencing, and downstream buffering remain
[planned reliability work](database-observability.md#collector-recovery-and-high-availability-planned).
Oracle is a future target; Grafana Alloy is a later comparison, not a selected
replacement in the current architecture.

## Implementation references

These diagrams were checked against the repository on 2026-09-27; this is a
source/configuration review, not live runtime validation.

- [Compose services](../../infra/docker-compose.yml),
  [Prometheus scrape/rule configuration](../../infra/prometheus/prometheus.yml),
  [OTel pipelines](../../infra/otel-collector/config.yaml), and
  [Alertmanager route](../../infra/alertmanager/alertmanager.yml).
- [DB collector wiring](../../services/db-collector/internal/app/app.go),
  [probe runner and evidence sink](../../services/db-collector/internal/collectors/runner.go),
  and [gateway HTTP handlers](../../services/otel-gateway/internal/app/app.go).
- [Integration configuration](../../config/integrations.yaml),
  [Grafana data sources](../../infra/grafana/provisioning/datasources/datasources.yml),
  [session analysis](session-analysis.md), [alerting](alerting.md), and
  [implementation checklist](../../TODO.md).

## Core systems
- Control plane: Go API, PostgreSQL metadata, Redis for async coordination
- Collection plane: OTel Collector and DB collectors
- Storage/query plane: Prometheus, Loki, PostgreSQL
- Analysis plane: session analyzer, adaptive baselines, reporting jobs
- Presentation plane: React UI and Grafana deep links

## Boundary decisions
- PostgreSQL => durable metadata only
- YAML/Kubernetes => integration endpoints, dashboard URL templates, collector desired runtime state
- Redis => transient queues, locks, retries, short-lived caches
- Loki/Prometheus => operational evidence and telemetry

## Monorepo layout
- `apps/api`
- `apps/web`
- `services/otel-gateway`
- `services/db-collector`
- `services/session-analyzer`
- `services/reporting`
- `packages/config-schema`
- `packages/telemetry-contracts`
- `infra`
- `db/migrations`
- `tests`
