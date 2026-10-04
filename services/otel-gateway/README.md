# Heartbeat OTel Gateway

A thin Go service for platform-specific telemetry normalization. The stock
OpenTelemetry Collector (`otelCollector.config` in the [Helm chart values](../../infra/helm/heartbeat/values.yaml)) remains responsible
for OTLP ingest, processing and routing. The gateway exists only for parsing
that collector configuration cannot express cleanly, starting with OutSystems.
See [operator workflows](../../docs/architecture/workflows.md#application-telemetry-ingestion-outsystems-first).

> **Status: partial.** The gateway normalizes a single JSON event and returns it
> to the caller; it does not forward anything to the OTel Collector yet.
> OutSystems-specific parsers are not implemented.

## What it does today

| Endpoint | Behavior |
| --- | --- |
| `POST /v1/heartbeat/events` | Decodes one JSON object and maps common aliases into the [application event contract](../../packages/telemetry-contracts/src/application_event.schema.json). Returns the normalized event (202) or 400. |
| `POST /v1/heartbeat/alerts` | Alertmanager webhook receiver: accepts and counts the payload (202). Nothing is stored or delivered. |
| `GET /healthz`, `GET /readyz` | Liveness; readiness with `config_version` and the configured OTel endpoint |
| `GET /metrics` | `heartbeat_otel_gateway_normalized_events_total`, `heartbeat_otel_gateway_alert_deliveries_total` |

Field aliases recognized by `internal/normalization/event.go`:

| Contract field | Accepted input keys |
| --- | --- |
| `environment` | `environment`, `env` |
| `application` | `application`, `app`, `module` |
| `component` | `component`, `action`, `screen`, `operation` |
| `severity` | `severity`, `level` (lower-cased; default `info`) |
| `message` | `message`, `msg`, `text` |
| `user_id` / `session_id` / `request_id` | `user_id`, `userId`, `user` / `session_id`, `sessionId`, `session` / `request_id`, `requestId`, `trace_id`, `traceId` |
| `client_ip` | `client_ip`, `clientIp`, `ip` |
| `timestamp` | `timestamp`, `time` (RFC 3339; default now) |

```bash
curl -s -X POST http://localhost:8083/v1/heartbeat/events \
  -d '{"env":"prod","app":"Claims","level":"ERROR","msg":"timeout","userId":"u1"}'
```

## Configuration

| Variable | Default |
| --- | --- |
| `HEARTBEAT_OTEL_GATEWAY_LISTEN_ADDR` | `:8083` |
| `HEARTBEAT_INTEGRATIONS_PATH` | `config/integrations.yaml` (loaded once at startup; no reload) |

## Code layout

- `cmd/otel-gateway/main.go`: entry point and environment variables
- `internal/app/app.go`: HTTP routes and metrics
- `internal/normalization/event.go`: event parsing into the shared contract

## Next steps

Tracked in [TODO.md](../../TODO.md) §3.3: OutSystems parsers, forwarding
normalized events to the OTel Collector, and deciding what the alert endpoint
becomes ([open questions](../../docs/architecture/overview.md#open-questions)).
