# Heartbeat Product Requirements

## Goal
Heartbeat is an SRE-focused monitoring platform for application observability, SQL Server deep observability, investigation workflows, adaptive alerting, and operational reporting.

## MVP scope
- OutSystems-first application observability
- PostgreSQL-backed control-plane metadata
- Loki + Prometheus + Grafana + Alertmanager observability stack
- Session investigation by application, user/IP, and time range
- SQL Server observability metadata model and safe probe assignment model
- Scheduled and on-demand reporting metadata

## Core ownership rules
- Applications belong to environments.
- Application-owned workflows derive environment through `applications.environment_id`.
- PostgreSQL stores durable control-plane metadata only.
- Runtime integrations and collector desired state live in YAML + Kubernetes config.
- Raw telemetry, raw logs, raw sessions, report binaries, and audit streams stay outside PostgreSQL in MVP.

## Explicit PostgreSQL exclusions
- `outsystems_sources`
- `session_identity_mappings`
- `integration_connections`
- `grafana_links`
- `audit_events`
- `collector_instances`
- `collector_assignments`
- `assets`
- `asset_relationships`
