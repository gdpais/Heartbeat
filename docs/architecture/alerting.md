# Alerting Architecture

## Ownership model
Alerting is application-owned in MVP.

## Durable PostgreSQL metadata
- `alert_policies`
- `adaptive_baselines`
- `notification_routes`
- `alert_events`
- `report_templates`
- `report_schedules`
- `report_runs`

## Runtime model
- Prometheus evaluates rules.
- Alertmanager routes and deduplicates alerts.
- PostgreSQL stores rule intent, baseline metadata, route metadata, and event/report metadata.
- Grafana links are generated from YAML templates, not DB tables.
