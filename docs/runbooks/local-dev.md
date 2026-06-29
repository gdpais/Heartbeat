# Local Development Runbook

## Start the local stack
`make up`

## Start the local stack with a dev-only SQL Server target
`make sqlserver-dev-init`

`make sqlserver-dev-up`

## Validate service config
`make config`

## Validate the SQL Server dev overlay
`make sqlserver-dev-config`

## Health checks
- PostgreSQL and db-collector: `make health`
- SQL Server dev overlay, collector, Prometheus, and Grafana: `make sqlserver-dev-health`

## Apply migrations manually
`make migrate`

## Tear down
`make down`

## Tear down the SQL Server dev overlay
`make sqlserver-dev-down`
