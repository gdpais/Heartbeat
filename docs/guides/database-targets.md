# Onboarding SQL Server Targets

How to add, change or remove a monitored SQL Server database.

Monitored targets are configured in `config/integrations.yaml`, not in
PostgreSQL or a UI. Onboarding a database is a config change
([ADR 0002](../architecture/decisions/0002-runtime-config-in-yaml.md)). All keys
are described in the [configuration reference](../reference/configuration.md).

SQL Server is the only supported engine. Adding PostgreSQL, MySQL, Oracle or
MongoDB targets requires a new collector kind and connector in code, not just
config.

## 1. Get a login from the DBAs

Production targets use a dedicated observability login, never `sa` or any other
sysadmin login.

### Collector login permissions

The built-in probes are read-only and need:

| Permission | Needed for |
| --- | --- |
| `VIEW SERVER STATE` (or `VIEW SERVER PERFORMANCE STATE` on SQL Server 2022+) | `sys.dm_os_wait_stats`, `sys.dm_exec_requests`, `sys.dm_exec_sessions`, `sys.dm_os_performance_counters` |
| `VIEW ANY DEFINITION` | `sys.master_files` for the `storage` probe. Without it, file sizes are silently missing. |
| Access to the target's `database_name` | Used as the initial database |

No write, `db_owner` or `sysadmin` rights are required. A `query_template`
override runs with the same login; review any override that needs more than
these permissions with the DBAs first.

Connections use `encrypt=true` and verify the server certificate, so the target
needs a certificate the collector trusts.
`HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE=true` is for the local
dev container only.

## 2. Add the target

Add an entry under `collectors[].config.targets[]`:

```yaml
collectors:
  - id: sqlserver-default
    kind: sqlserver
    enabled: true
    config:
      environment: prod
      scrape_interval: 30s
      probes:
        - name: waits
        - name: blocking
        - name: sessions
        - name: memory_pressure
        - name: storage
        - name: throughput
      targets:
        - name: finance-prod          # becomes the `target` label; unique per collector
          host: finance-sql.internal
          port: 1433
          database_name: FinanceDB
          credential_ref: env/finance-prod
```

To run only some targets of a collector, list them in `config.target_names`.

To **change** a target, edit its entry: `host`/`port` for a new server,
`database_name` for another database on the same server, `credential_ref` for a
new login. To **remove** one, delete the entry; the collector stops exporting
its series after the reload.

## 3. Provide the credential

`credential_ref: env/finance-prod` is read from the environment variable
`HEARTBEAT_CREDENTIAL_ENV_FINANCE_PROD` with the value `username:password`
([resolution rules](../reference/configuration.md#credential-resolution)). The
variable must reach the collector **container**. Keep values out of version
control.

| Where the collector runs | How to pass the credential |
| --- | --- |
| Docker Compose | A local Compose override or secret mechanism for the `db-collector` service. Exporting it in your host shell is not enough. |
| SQL Server dev overlay | Already wired from the ignored `.env.sqlserver-dev` |
| Local Kubernetes | An environment variable on the collector pod, from a Secret |

## 4. Apply the change

| Where | Steps |
| --- | --- |
| Docker Compose | Edit `config/integrations.yaml`. Config-only changes apply within about 2s through file polling, `SIGHUP` or `POST /admin/config/reload`. **Recreate** `db-collector` when its environment (credentials) changes. |
| SQL Server dev overlay | Edit `config/integrations.local-dev.yaml` instead |
| Local Kubernetes | Edit the embedded config in `infra/k8s/local/configmap-config.yaml` and run `make k8s-up` ([guide](kubernetes-local.md)) |

The collector validates the file on load; an invalid file is rejected and the
previous config keeps running.

## 5. Verify

```bash
curl -s http://localhost:8082/readyz        # the target appears; failed targets are listed without raw errors
curl -s http://localhost:8082/metrics | grep 'heartbeat_collector_target_up{.*target="finance-prod"'
```

`heartbeat_collector_target_up` should be `1`, and
`heartbeat_sqlserver_*` series should appear with `target="finance-prod"`. In
Grafana, open the *SQL Server overview* dashboard and select the target.

If the target stays down, check the collector's JSON logs (one line per probe
failure, with collector, target and probe). Repeated failures back off
exponentially up to 5 minutes, so a fix can take that long to show up unless
you reload.
