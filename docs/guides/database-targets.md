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

The built-in probes need SQL Server 2012 or later (the newest feature they use
is `CONCAT`); CI runs them against SQL Server 2022 on Linux. They are read-only
and need:

| Permission | Needed for |
| --- | --- |
| `VIEW SERVER STATE` | `sys.dm_os_wait_stats`, `sys.dm_exec_requests`, `sys.dm_exec_sessions`, `sys.dm_os_performance_counters`, `sys.dm_os_ring_buffers`, `sys.dm_os_sys_info`, `sys.dm_io_virtual_file_stats`. Every built-in probe was verified as a login holding only `VIEW SERVER STATE` and `VIEW ANY DEFINITION`. SQL Server 2022's narrower `VIEW SERVER PERFORMANCE STATE` has not been verified on its own, so grant `VIEW SERVER STATE`. |
| `VIEW ANY DEFINITION` | `sys.master_files` for the `storage` and `file_io` probes. Without it, file sizes and file I/O are silently missing. |
| `VIEW ANY DATABASE` (granted to `public` by default) | Database names (`DB_NAME()`) in the `storage` and `file_io` `database_name` label. If it was revoked, files of databases the login cannot see are labelled `database_id:<id>` instead. |
| Access to the target's `database_name` | Used as the initial database |

No write, `db_owner` or `sysadmin` rights are required. A `query_template`
override runs with the same login; review any override that needs more than
these permissions with the DBAs first.

Connections use `encrypt=true` and verify the server certificate, so the target
needs a certificate the collector trusts.
`HEARTBEAT_DB_COLLECTOR_SQLSERVER_TRUST_SERVER_CERTIFICATE=true` is for the local
dev container only.

## 2. Add the target

Add an entry under `collectors[].config.targets[]` of the environment's
`integrations` values (the chart renders them into `integrations.yaml`; see
[config by environment](../reference/configuration.md#config-files-by-environment)).
Lists replace rather than merge across values files, so restate the whole
`collectors` list:

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
        - name: cpu
        - name: buffer_cache
        - name: file_io
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

Put the variable in a Secret and name it in
`dbCollector.credentials.existingSecret`; the chart loads every key of that
Secret into the collector's environment.

| Where the collector runs | How the Secret is created |
| --- | --- |
| kind | `kubectl -n heartbeat create secret generic <name> --from-literal=HEARTBEAT_CREDENTIAL_ENV_FINANCE_PROD=<user>:<password>` |
| kind SQL Server sandbox | Already wired: `make sqlserver-dev-up` creates it from the ignored `.env.sqlserver-dev` |
| Production | An External Secrets Operator `ExternalSecret` from AWS Secrets Manager (ADR 0005) |

## 4. Apply the change

| Where | Steps |
| --- | --- |
| kind | Put the values in a file and run `EXTRA_VALUES=<file> make kind-deploy`. |
| Production | Commit the values to the `heartbeat-deploy` repository; Argo CD syncs them. |

Helm rejects values that fail the integrations schema before anything changes.
A config-only change does not restart the collector: the ConfigMap reaches the
pod within about a minute (kubelet sync) and the collector picks it up through
file polling (5s). To apply it sooner, `POST /admin/config/reload` with the
admin token. A change to the credentials Secret needs
`kubectl -n heartbeat rollout restart statefulset/db-collector`. An invalid file
is rejected and the previous config keeps running.

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
