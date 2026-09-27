# Database Target Configuration

## Current source of truth

For the current MVP, monitored database targets are configured in
`config/integrations.yaml`.

This is an implementation constraint, not just an example:

- `services/db-collector/README.md` states that collector desired state comes
  from `config/integrations.yaml`
- `docs/architecture/database-observability.md` states that active collector
  runtime desired state is not read from PostgreSQL for MVP

That means adding or changing a monitored database is currently a config change,
not a backoffice or PostgreSQL metadata change.

## Change an existing monitored database

Edit the matching entry under:

- `collectors[].config.targets[]`

The target fields are defined by `internal/config/config.go`:

- `name`: unique target label used in metrics and filtering
- `environment`: environment slug for the target
- `host`: SQL Server hostname or IP
- `port`: SQL Server TCP port
- `database_name`: logical database to connect to
- `credential_ref`: credential reference resolved at runtime

Example:

```yaml
collectors:
  - id: sqlserver-default
    kind: sqlserver
    enabled: true
    config:
      environment: local
      scrape_interval: 30s
      target_names: []
      probes:
        - name: waits
        - name: blocking
      targets:
        - name: finance-prod
          environment: prod
          host: finance-sql.internal
          port: 1433
          database_name: FinanceDB
          credential_ref: env/finance-prod
```

To change the connection, update the relevant target fields:

- new server: change `host` and/or `port`
- new database on the same server: change `database_name`
- new login: change `credential_ref` and provide the new credential

## Add a new database to monitoring

Add a new item to `collectors[].config.targets[]` in
`config/integrations.yaml`.

Example:

```yaml
collectors:
  - id: sqlserver-default
    kind: sqlserver
    enabled: true
    config:
      environment: local
      scrape_interval: 30s
      target_names: []
      probes:
        - name: waits
        - name: blocking
        - name: sessions
        - name: memory_pressure
        - name: storage
        - name: throughput
      targets:
        - name: sqlserver-a
          environment: prod
          host: sql-a.internal
          port: 1433
          database_name: AppA
          credential_ref: env/sqlserver-a
        - name: sqlserver-b
          environment: prod
          host: sql-b.internal
          port: 1433
          database_name: AppB
          credential_ref: env/sqlserver-b
```

The collector will then execute its configured probes against both targets.

If you want only a subset of targets in a collector to run, use
`config.target_names`.

## How credentials work

The SQL Server connector documents the runtime convention in
`services/db-collector/internal/connectors/sqlserver/connector.go`.

`credential_ref` values are resolved from environment variables named:

```text
HEARTBEAT_CREDENTIAL_<REF>
```

`<REF>` is the `credential_ref` converted to upper snake case, with
non-alphanumeric separators replaced by underscores.

Example:

- `credential_ref: env/sqlserver-b`
- environment variable: `HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_B`

The value must be:

```text
username:password
```

## Collector login permissions

Production targets use a dedicated observability login provided by the DBAs,
never `sa` or another sysadmin login. The local dev overlay uses `sa` only
because the container is throwaway.

The built-in probes are read-only and need:

| Permission | Needed for |
|---|---|
| `VIEW SERVER STATE` (or `VIEW SERVER PERFORMANCE STATE` on SQL Server 2022+) | `sys.dm_os_wait_stats`, `sys.dm_exec_requests`, `sys.dm_exec_sessions`, `sys.dm_os_performance_counters` |
| `VIEW ANY DEFINITION` | `sys.master_files` for the `storage` probe; without it, file sizes are silently missing |
| Access to the target's `database_name` | the connection sets it as the initial database |

No write, `db_owner`, or `sysadmin` rights are required. A `query_template`
override runs with the same login, so any override needing more than the
permissions above should be reviewed with the DBAs first.

Connections use `encrypt=true` and verify the server certificate. Targets need
a certificate the collector trusts;
`HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE=true` is for the
local dev container only.

## Apply the change locally

For the Docker Compose local stack:

1. Edit `config/integrations.yaml`.
2. Add the required `HEARTBEAT_CREDENTIAL_<REF>` variable to the
   `db-collector` service's environment through a local Compose override or
   secret delivery mechanism. Exporting it in the host shell alone does not
   pass it into the container. Keep credential values out of version control.
3. Run `make config` to validate Compose syntax. The collector validates
   `integrations.yaml` on startup or reload; Compose does not validate that file.
4. Recreate `db-collector` when its environment changes. A config-only change
   can use the file watcher, SIGHUP, or authenticated `POST /admin/config/reload`.

The SQL Server dev overlay instead reads `config/integrations.local-dev.yaml`
and passes its credential from the ignored `.env.sqlserver-dev` file.

Relevant existing docs:

- `docs/runbooks/local-dev.md`
- `README.md`

## Kubernetes local bundle

For the local Kubernetes manifests in `infra/k8s/local/`:

1. Update the embedded `integrations.yaml` inside
   `infra/k8s/local/configmap-config.yaml`.
2. Ensure the target credential is available to the `db-collector` pod as an
   environment variable following the `HEARTBEAT_CREDENTIAL_<REF>` convention.
3. Re-apply the manifests with `make k8s-up`.

Note that the checked-in local K8s config currently ships with `targets: []`,
so no SQL Server target is monitored until you add one.

## Important limitation

The current implementation supports SQL Server only.

Adding a new PostgreSQL, MySQL, or MongoDB target is not just a config change;
it would require a new collector kind and connector/runtime support in code.
