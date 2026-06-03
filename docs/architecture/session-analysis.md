# Session Analysis Architecture

## Investigation entrypoint
Investigations start from application context and correlate user/IP/request/session signals over a time window. Environment is retained as grouping context and can be derived from the application relationship.

## Durable metadata in PostgreSQL
- `investigations`
- `investigation_jobs`
- `investigation_results`
- `evidence_links`

## Non-goals
- No PostgreSQL session table in MVP
- No session identity mapping table in MVP
- No raw evidence blobs in PostgreSQL

## Evidence sources
- Grafana deep links
- Loki queries
- Prometheus queries
- DB collector snapshots when available
