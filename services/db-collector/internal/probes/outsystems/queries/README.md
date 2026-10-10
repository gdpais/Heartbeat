# OutSystems 11 log queries

The queries the db-collector's `outsystems_logs` collector runs against the
OutSystems 11 logging database, one file per log type. Why they live here:
[ADR 0008](../../../../../../docs/architecture/decisions/0008-outsystems-11-log-extraction.md).

> **Status:** the extractor that runs these files is not built yet.
> `error.sql` is an example of the contract, not a reviewed query: replace it
> and add the other log types.

## Files

- The file name is the log type: `error.sql` is log type `error`, selected in
  `integrations.yaml` with `probes: [{name: error}]` and sent to Loki as
  `outsystems.log_type="error"`. Use lower-case `snake_case` names, such as
  `general`, `error`, `screen`, `integration`, `extension`, `mobile_request`,
  `request_event`, `cyclic_job`.
- Every `.sql` file in this folder is built into the collector. Removing a
  file removes the log type; a config that still lists it is rejected.
- One `SELECT` per file. The collector runs the query in its own session with
  `SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW;` and a hard deadline; do
  not set session options, open transactions or create objects here.

## Contract

**Parameters** (bound by the collector, never concatenated):

| Parameter | Type | Meaning |
| --- | --- | --- |
| `@from` | `datetime2` | Exclusive lower bound: the last `Instant` already delivered |
| `@to` | `datetime2` | Inclusive upper bound: now minus the configured lag, so rows the log service commits late are not skipped |
| `@limit` | `int` | Maximum rows to return |

Both bounds are in the time zone `Instant` is stored in (collector setting
`log_timezone`, default UTC).

**Rows:**

- Filter with `Instant > @from AND Instant <= @to`, return at most `@limit`
  rows (`SELECT TOP (@limit)`) and order by `Instant` ascending. The collector
  relies on this order to move its watermark; a query that returns rows out of
  order or above `@limit` is rejected at run time.
- Return a column named exactly `Instant` (`datetime` or `datetime2`).

**Columns:**

| Column | Required | Becomes |
| --- | --- | --- |
| `Instant` | yes | The log record's timestamp |
| `Message` | no | The log body. Without it the body is empty |
| `Severity` | no | The log severity: `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR` or `FATAL` (case-insensitive). Without it, `error` rows are `ERROR` and all others `INFO`. Derive it in SQL with a `CASE` when a log type mixes levels |
| `Application_Name` | recommended | The Loki stream's `service.name`. Without it, `Module_Name` is used, then `unknown` |
| any other column | no | A log attribute named exactly like the column, for example `Request_Key`, `Session_Id`, `User_Id`, `Client_IP`, `Module_Name`, `Duration` |

- Keep OutSystems' own column names: SREs search Loki with them, and the
  derived metrics and request correlation look them up by name. The names the
  extractor understands are listed in
  [application observability](../../../../../../docs/architecture/application-observability.md#outsystems-fields).
- `NULL` values are left out of the record.
- Durations stay in the unit OutSystems logs them (milliseconds); the
  extractor converts them for metrics.

## Review and tests

Each query goes through the
[probe review checklist](../../../../../../docs/architecture/database-observability.md#probe-review-checklist):
it reads only `oslog_*` tables (and joins the extractor's login is granted),
filters on an indexed `Instant` range, returns a bounded row count and needs
no more than `SELECT`. CI runs every file against a SQL Server fixture with the
`oslog_*` tables, built from the real DDL.
