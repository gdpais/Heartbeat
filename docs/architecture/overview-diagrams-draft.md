# Architecture diagrams — revision draft

Draft replacements for the two diagrams in [overview.md](overview.md). Not yet
adopted; review before replacing the originals.

**Conventions (both diagrams)**

- Arrows follow the direction data moves. Labels say whether it is *pulled*
  (scraped/queried by the receiver) or *pushed* by the sender.
- Colour encodes status only: **blue** implemented, **amber** partial,
  **dashed gray** planned or idle. Shape encodes type: cylinder = store,
  parallelogram = configuration, rounded = person.
- Solid edges are implemented/configured; dashed edges are planned; the
  **amber edge** marks a known gap in otherwise-implemented components.

## Current runtime

```mermaid
flowchart LR
    sql[("SQL Server targets<br/>no Heartbeat agent")]
    otlp["OTLP clients"]

    subgraph env["Heartbeat monitoring environment · Compose"]
        config[/"integrations.yaml<br/>targets · probes · credential_ref"/]
        secrets[/"Environment secrets<br/>HEARTBEAT_CREDENTIAL_*"/]
        db["DB collector · :8082<br/>/metrics · /readyz<br/>/admin/config/reload"]
        otel["OTel Collector<br/>OTLP :4317 / :4318<br/>Prometheus export :8889"]
        gateway["OTel gateway · :8083<br/>normalize endpoint: no caller yet<br/>alert intake: count only"]
        prom[("Prometheus<br/>15s scrape · rule files")]
        loki[("Loki")]
        am["Alertmanager<br/>webhook route"]
        grafana["Grafana<br/>provisioned dashboards"]
        idle[("PostgreSQL · Redis<br/>provisioned, no consumers")]
    end

    operator(["Operator"])

    sql -->|"probe results · pulled"| db
    otlp -->|"OTLP · pushed"| otel
    config -->|"load + hot reload"| db
    config -->|"load"| gateway
    secrets -->|"resolve credential_ref"| db
    db -->|"scraped"| prom
    otel -->|"scraped"| prom
    gateway -->|"scraped"| prom
    otel -->|"logs · pushed"| loki
    prom -.->|"GAP: no alerting block"| am
    am -->|"webhook · pushed"| gateway
    prom -->|"PromQL"| grafana
    loki -->|"LogQL"| grafana
    grafana -->|"dashboards · Explore"| operator

    classDef ok fill:#e0f2fe,stroke:#0369a1,color:#0c4a6e
    classDef partial fill:#fef3c7,stroke:#b45309,color:#78350f
    classDef idle fill:#f8fafc,stroke:#64748b,color:#334155,stroke-dasharray:5 5
    classDef external fill:#f1f5f9,stroke:#64748b,color:#0f172a
    class db,otel,prom,loki,am,grafana ok
    class gateway partial
    class idle idle
    class sql,otlp,config,secrets,operator external
    linkStyle 9 stroke:#d97706,stroke-width:2px,color:#b45309
```

Self-scrapes (Prometheus, Loki, Alertmanager) are omitted. The collector also
reloads on SIGHUP and, when `HEARTBEAT_CONFIG_WATCH_INTERVAL` is set, on file
change.

## Target platform

The implemented collection pipeline and the Prometheus/Loki pair are collapsed
into single nodes; see the diagram above for their internals. Columns follow the
planes in [Core systems](overview.md#core-systems).

```mermaid
flowchart TB
    sql[("SQL Server targets")]
    apps["OutSystems apps"]

    subgraph telemetry["Telemetry · see Current runtime"]
        direction TB
        pipeline["Collection pipeline<br/>DB collector · OTel · gateway"]
        stores[("Prometheus · Loki<br/>metrics · logs · rules")]
        am["Alertmanager"]
    end

    subgraph analysis["Analysis jobs"]
        direction TB
        analyzer["Session analyzer<br/>correlation · baselines"]
        reporting["Reporting<br/>report generation"]
        artifacts[("Report storage<br/>backend TBD")]
    end

    subgraph control["Control plane"]
        direction TB
        config[/"YAML / Kubernetes config"/]
        api["Go API<br/>inventory · policies · investigations"]
        pg[("PostgreSQL<br/>durable metadata")]
        redis[("Redis<br/>queues · locks · retries")]
    end

    subgraph present["Presentation"]
        direction TB
        grafana["Grafana"]
        web["React web UI"]
    end

    delivery["Email / webhooks"]
    operator(["Operator / SRE"])

    sql -->|"probes · pulled"| pipeline
    apps -.->|"source ingestion"| pipeline
    pipeline --> stores
    stores -.->|"GAP: alerts"| am
    am -.->|"notify"| delivery
    am -->|"alert webhook · role TBD"| pipeline
    stores -->|"PromQL / LogQL"| grafana
    stores -.->|"PromQL / LogQL"| analysis
    analysis -.->|"summaries · run metadata"| pg
    reporting -.->|"report files"| artifacts
    redis -.->|"jobs"| analysis
    api -.->|"enqueue"| redis
    api <-.->|"metadata"| pg
    config -.->|"endpoints · templates"| api
    api -.->|"policy · baseline rules"| stores
    api -.->|"notification routes"| am
    api <-.->|"HTTPS API"| web
    web -.->|"deep links"| grafana
    grafana -->|"dashboards"| operator
    web <-.->|"manage · investigate"| operator

    classDef ok fill:#e0f2fe,stroke:#0369a1,color:#0c4a6e
    classDef partial fill:#fef3c7,stroke:#b45309,color:#78350f
    classDef planned fill:#f8fafc,stroke:#64748b,color:#334155,stroke-dasharray:5 5
    classDef external fill:#f1f5f9,stroke:#64748b,color:#0f172a
    class grafana,pg,redis,stores,am ok
    class pipeline partial
    class web,api,analyzer,reporting,artifacts,apps,delivery planned
    class operator,sql,config external
    linkStyle 3 stroke:#d97706,stroke-width:2px,color:#b45309
```

Open questions surfaced by the redraw:

- What does the gateway's alert intake become — persisted investigation
  events (via the API into PostgreSQL), or removed once Alertmanager delivers
  directly?
- Should the gateway export normalized events to the OTel Collector, and should
  its Compose `depends_on: otel-collector` wait for that?
- Who renders `infra/prometheus/rules/generated/` — the API (as drawn) or a
  build step?
