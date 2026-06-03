# Local Development Runbook

## Start the local stack
`docker compose -f infra/docker-compose.yml up -d`

## Validate service config
`docker compose -f infra/docker-compose.yml config`

## Health checks
- PostgreSQL: `docker compose -f infra/docker-compose.yml exec postgres pg_isready -U heartbeat -d heartbeat`
- Redis: `docker compose -f infra/docker-compose.yml exec redis redis-cli ping`
- Prometheus: `curl http://localhost:9090/-/ready`
- Loki: `curl http://localhost:3100/ready`
- Grafana: `curl http://localhost:3000/api/health`
- Alertmanager: `curl http://localhost:9093/-/ready`
- OTel Collector: `curl http://localhost:13133/`

## Apply migrations manually
`docker compose -f infra/docker-compose.yml exec -T postgres psql -U heartbeat -d heartbeat < /migrations/0001_foundations.up.sql`

## Tear down
`docker compose -f infra/docker-compose.yml down -v`
