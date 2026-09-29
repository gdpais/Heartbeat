# Architecture diagrams — revision draft

Draft replacements for the two diagrams in [overview.md](overview.md). Not yet
adopted; review before replacing the originals.

Each view is split into two focused diagrams so every diagram stays narrow
enough to read without zooming. Ports, endpoints and other detail live in the
table below the runtime diagrams instead of inside the boxes.

**How to read these diagrams**

- Arrows point the way information moves: telemetry flows toward the operator;
  commands and configuration flow away from the operator.
- Colour shows status. Shape shows type: cylinders are stores, slanted boxes
  are configuration, pills are people.
- Solid lines are implemented; dotted lines are planned. The **orange line**
  marks a known gap between implemented components.

```mermaid
%%{init: {"theme": "base", "fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "themeVariables": {"fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "fontSize": "15px", "lineColor": "#8b95a7", "primaryTextColor": "#1f2937", "clusterBkg": "#f8fafc", "clusterBorder": "#cbd5e1", "titleColor": "#475569", "edgeLabelBackground": "#ffffff"}, "flowchart": {"curve": "basis", "nodeSpacing": 36, "rankSpacing": 52, "padding": 16}}}%%
flowchart LR
    a("<b>Implemented</b>") ~~~ b("<b>Partial</b>") ~~~ c("<b>Planned</b>") ~~~ d("<b>External</b>")
    classDef ok fill:#eaf1ff,stroke:#3b6fd8,stroke-width:1.5px,color:#1e3a8a
    classDef partial fill:#fff4e0,stroke:#d98a1c,stroke-width:1.5px,color:#7a4306
    classDef planned fill:#ffffff,stroke:#94a3b8,stroke-width:1.5px,stroke-dasharray:6 4,color:#475569
    classDef ext fill:#f1f5f9,stroke:#64748b,stroke-width:1.5px,color:#1f2937
    class a ok
    class b partial
    class c planned
    class d ext
```

## Current runtime

### Telemetry path

How SQL Server probes and OTLP data reach the operator. SQL Server hosts run
no Heartbeat agent; the DB collector probes them remotely.

```mermaid
%%{init: {"theme": "base", "fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "themeVariables": {"fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "fontSize": "15px", "lineColor": "#8b95a7", "primaryTextColor": "#1f2937", "clusterBkg": "#f8fafc", "clusterBorder": "#cbd5e1", "titleColor": "#475569", "edgeLabelBackground": "#ffffff"}, "flowchart": {"curve": "basis", "nodeSpacing": 36, "rankSpacing": 52, "padding": 16}}}%%
flowchart TB
    sql[("<b>SQL Server</b><br/>monitored targets")]
    otlp("<b>OTLP clients</b><br/>apps and hosts")

    subgraph env["Heartbeat monitoring environment"]
        db("<b>DB collector</b><br/>remote SQL probes")
        otel("<b>OTel Collector</b><br/>OTLP receiver")
        gateway("<b>OTel gateway</b><br/>no producer yet")
        prom[("<b>Prometheus</b><br/>metrics · rules")]
        loki[("<b>Loki</b><br/>logs")]
        grafana("<b>Grafana</b><br/>dashboards")
    end

    operator(["<b>Operator</b>"])

    sql -->|probed| db
    otlp -->|OTLP push| otel
    db -->|scraped| prom
    otel -->|scraped| prom
    gateway -->|scraped| prom
    otel -->|log push| loki
    prom -->|PromQL| grafana
    loki -->|LogQL| grafana
    grafana --> operator

    classDef ok fill:#eaf1ff,stroke:#3b6fd8,stroke-width:1.5px,color:#1e3a8a
    classDef partial fill:#fff4e0,stroke:#d98a1c,stroke-width:1.5px,color:#7a4306
    classDef planned fill:#ffffff,stroke:#94a3b8,stroke-width:1.5px,stroke-dasharray:6 4,color:#475569
    classDef ext fill:#f1f5f9,stroke:#64748b,stroke-width:1.5px,color:#1f2937
    class db,otel,prom,loki,grafana ok
    class gateway partial
    class sql,otlp,operator ext
```

### Configuration and alerting

How the collectors are configured, and where the alert path currently stops.

```mermaid
%%{init: {"theme": "base", "fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "themeVariables": {"fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "fontSize": "15px", "lineColor": "#8b95a7", "primaryTextColor": "#1f2937", "clusterBkg": "#f8fafc", "clusterBorder": "#cbd5e1", "titleColor": "#475569", "edgeLabelBackground": "#ffffff"}, "flowchart": {"curve": "basis", "nodeSpacing": 36, "rankSpacing": 52, "padding": 16}}}%%
flowchart TB
    operator(["<b>Operator</b>"])
    secrets[/"<b>Env secrets</b><br/>HEARTBEAT_CREDENTIAL_*"/]
    config[/"<b>integrations.yaml</b><br/>targets · probes"/]
    prom[("<b>Prometheus</b><br/>alert rules")]

    db("<b>DB collector</b><br/>applies config diff")
    am("<b>Alertmanager</b><br/>webhook route")
    gateway("<b>OTel gateway</b><br/>counts alerts only")

    operator -->|admin reload| db
    config -->|hot reload| db
    secrets -->|credentials| db
    config -->|load| gateway
    prom -.->|"GAP · no alerting block"| am
    am -->|webhook| gateway

    classDef ok fill:#eaf1ff,stroke:#3b6fd8,stroke-width:1.5px,color:#1e3a8a
    classDef partial fill:#fff4e0,stroke:#d98a1c,stroke-width:1.5px,color:#7a4306
    classDef planned fill:#ffffff,stroke:#94a3b8,stroke-width:1.5px,stroke-dasharray:6 4,color:#475569
    classDef ext fill:#f1f5f9,stroke:#64748b,stroke-width:1.5px,color:#1f2937
    class db,prom,am ok
    class gateway partial
    class operator,config,secrets ext
    linkStyle 4 stroke:#e0781f,stroke-width:2.5px,color:#b45309
```

### Component details

| Component | Endpoints | Notes |
| --- | --- | --- |
| DB collector | `:8082` — `/metrics`, `/healthz`, `/readyz`, `/admin/config`, `POST /admin/config/reload` | Reload also on SIGHUP, and on file change when `HEARTBEAT_CONFIG_WATCH_INTERVAL` is set. Credentials resolved from `HEARTBEAT_CREDENTIAL_*`. |
| OTel Collector | OTLP `:4317` gRPC / `:4318` HTTP; Prometheus export `:8889`; health `:13133` | Drops `user_id`, `session_id`, `request_id`, `client_ip` from log attributes. |
| OTel gateway | `:8083` — `/metrics`, `/healthz`, `/readyz`, `POST /v1/heartbeat/events`, `POST /v1/heartbeat/alerts` | Events are normalized and returned to the caller, not exported. Alerts are counted, not stored or delivered. |
| Prometheus | `:9090` | 15s scrape. Rule files `heartbeat.rules.yml` and `generated/` (currently empty). No `alerting` block. |
| Alertmanager | `:9093` | One `default` webhook receiver pointing at the gateway, `send_resolved: true`. |
| Loki / Grafana | `:3100` / `:3000` | Grafana provisioned with Prometheus (default) and Loki data sources. |
| PostgreSQL / Redis | `:5432` / `:6379` | Provisioned in Compose; no service reads or writes them yet. |

Self-scrapes of Prometheus, Loki and Alertmanager are omitted from the diagrams.

## Target platform

The implemented collection pipeline is shown as a single box; see
[Current runtime](#current-runtime) for its internals.

### Data and analysis

How collected telemetry becomes dashboards, investigations, reports and
notifications.

```mermaid
%%{init: {"theme": "base", "fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "themeVariables": {"fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "fontSize": "15px", "lineColor": "#8b95a7", "primaryTextColor": "#1f2937", "clusterBkg": "#f8fafc", "clusterBorder": "#cbd5e1", "titleColor": "#475569", "edgeLabelBackground": "#ffffff"}, "flowchart": {"curve": "basis", "nodeSpacing": 36, "rankSpacing": 52, "padding": 16}}}%%
flowchart TB
    sql[("<b>SQL Server</b>")]
    apps("<b>OutSystems apps</b>")
    pipeline("<b>Collection pipeline</b><br/>collectors · gateway")
    stores[("<b>Prometheus · Loki</b><br/>metrics · logs")]

    grafana("<b>Grafana</b>")
    analyzer("<b>Session analyzer</b><br/>correlation · baselines")
    reporting("<b>Reporting</b><br/>scheduled reports")
    am("<b>Alertmanager</b>")

    operator(["<b>Operator</b>"])
    pg[("<b>PostgreSQL</b><br/>summaries · URIs")]
    artifacts[("<b>Report storage</b><br/>backend TBD")]
    delivery("<b>Notifications</b><br/>email · webhooks")

    sql -->|probed| pipeline
    apps -.->|ingest| pipeline
    pipeline --> stores
    stores -->|query| grafana
    stores -.->|query| analyzer
    stores -.->|query| reporting
    stores -.->|"GAP · alerts"| am
    grafana --> operator
    analyzer -.-> pg
    reporting -.-> pg
    reporting -.-> artifacts
    am -.-> delivery

    classDef ok fill:#eaf1ff,stroke:#3b6fd8,stroke-width:1.5px,color:#1e3a8a
    classDef partial fill:#fff4e0,stroke:#d98a1c,stroke-width:1.5px,color:#7a4306
    classDef planned fill:#ffffff,stroke:#94a3b8,stroke-width:1.5px,stroke-dasharray:6 4,color:#475569
    classDef ext fill:#f1f5f9,stroke:#64748b,stroke-width:1.5px,color:#1f2937
    class stores,grafana,am,pg ok
    class pipeline partial
    class apps,analyzer,reporting,artifacts,delivery planned
    class sql,operator ext
    linkStyle 6 stroke:#e0781f,stroke-width:2.5px,color:#b45309
```

### Operator workflows and control

How the planned web UI and API manage metadata, schedule jobs, and provision
rules and notification routes.

```mermaid
%%{init: {"theme": "base", "fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "themeVariables": {"fontFamily": "Inter, system-ui, -apple-system, Segoe UI, Helvetica, Arial, sans-serif", "fontSize": "15px", "lineColor": "#8b95a7", "primaryTextColor": "#1f2937", "clusterBkg": "#f8fafc", "clusterBorder": "#cbd5e1", "titleColor": "#475569", "edgeLabelBackground": "#ffffff"}, "flowchart": {"curve": "basis", "nodeSpacing": 36, "rankSpacing": 52, "padding": 16}}}%%
flowchart TB
    operator(["<b>Operator</b>"])
    config[/"<b>YAML / Kubernetes</b><br/>endpoints · templates"/]
    web("<b>React web UI</b>")
    api("<b>Go API</b><br/>inventory · policies")
    grafana("<b>Grafana</b>")

    pg[("<b>PostgreSQL</b><br/>metadata")]
    redis[("<b>Redis</b><br/>job queue")]
    prom[("<b>Prometheus</b><br/>rules")]
    am("<b>Alertmanager</b><br/>routes")

    jobs("<b>Analysis jobs</b><br/>analyzer · reporting")
    gateway("<b>OTel gateway</b><br/>alert intake")

    operator -.->|manage| web
    web -.->|HTTPS| api
    web -.->|deep links| grafana
    config -.->|settings| api
    api -.->|read / write| pg
    api -.->|enqueue| redis
    api -.->|provision| prom
    api -.->|provision| am
    redis -.->|jobs| jobs
    am -->|"webhook · role TBD"| gateway

    classDef ok fill:#eaf1ff,stroke:#3b6fd8,stroke-width:1.5px,color:#1e3a8a
    classDef partial fill:#fff4e0,stroke:#d98a1c,stroke-width:1.5px,color:#7a4306
    classDef planned fill:#ffffff,stroke:#94a3b8,stroke-width:1.5px,stroke-dasharray:6 4,color:#475569
    classDef ext fill:#f1f5f9,stroke:#64748b,stroke-width:1.5px,color:#1f2937
    class grafana,pg,redis,prom,am ok
    class gateway partial
    class web,api,jobs planned
    class operator,config ext
```

## Open questions

- What does the gateway's alert intake become — persisted investigation
  events (via the API into PostgreSQL), or removed once Alertmanager delivers
  notifications directly?
- Should the gateway export normalized events to the OTel Collector, and should
  its Compose `depends_on: otel-collector` wait for that?
- Who renders `infra/prometheus/rules/generated/` — the API (as drawn) or a
  build step?
