# OutSystems 11 log queries

The queries the db-collector's `outsystems_logs` collector runs against the
OutSystems 11 logging database, one file per log type. Why they live here:
[ADR 0008](../../../../../../docs/architecture/decisions/0008-outsystems-11-log-extraction.md).

> **Status:** the db-collector runs these files (`outsystems_logs`
> collectors). The eight queries are written from the OutSystems 11 Log data
> reference; their fixtures' tables and views under
> [`testdata/oslog/`](../../../collectors/outsystems/testdata/oslog/) are the
> DDL of a surveyed logging database
> ([`scripts/outsystems-log-survey.sql`](../../../../../../scripts/outsystems-log-survey.sql)),
> their rows are synthetic. Each query still needs its review with the probe
> checklist.

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
- Read the current **and** the previous week. The platform keeps ten cycle
  tables per log type (`oslog_<TYPE>_0` to `_9`, one week each) behind two
  views: `oslog_<TYPE>` is the current week's table and `oslog_<TYPE>_Previous`
  last week's, switched every Friday at 23:45, database time. A query on the
  current view alone loses the rows logged just before the switch (they are
  committed after it, into the old table) and can never look back past
  Friday. Filter each view on the `Instant` range, `UNION ALL` them, then
  `TOP` and `ORDER BY`, as `error.sql` does.

## Contract

**Parameters** (bound by the collector, never concatenated):

| Parameter | Type | Meaning |
| --- | --- | --- |
| `@from` | `datetime2`, or `datetime` | Exclusive lower bound: the last `Instant` already delivered |
| `@to` | `datetime2`, or `datetime` | Inclusive upper bound: now minus the lag (the collector's, or the log type's own `lag`), so rows the log service commits late are not skipped |
| `@limit` | `int` | Maximum rows to return |

Both bounds are in the time zone `Instant` is stored in (collector setting
`log_timezone`, default UTC). Once a query has shown that `Instant` is
`datetime`, the bounds are sent as `datetime`: compared with a `datetime2`,
SQL Server converts the column (`.003` becomes `.0033333`), and a watermark
read back from it would no longer exclude its own row. Compare `Instant` with
the parameters directly; do not cast either side.

**Rows:**

- Filter with `Instant > @from AND Instant <= @to`, return at most `@limit`
  rows (`SELECT TOP (@limit)`) and order by `Instant` ascending. The collector
  relies on this order to move its watermark; a query that returns rows out of
  order or above `@limit` is rejected at run time.
- Return a column named exactly `Instant` (`datetime` or `datetime2`). Its
  meaning differs per log type ([OutSystems fields](../../../../../../docs/architecture/application-observability.md#outsystems-fields)):
  for `screen` and `cyclic_job` it is when the operation **started**, and the
  row is written when the operation ends. Those log types need their own
  `lag` in `integrations.yaml`, longer than the longest screen request or
  timer, or rows that finish later than the lag are never read.
- The collector checks each batch: an `Instant` column, at most `@limit`
  rows, ascending `Instant` within (`@from`, `@to`]. A batch that breaks a
  rule fails the cycle and sends nothing.

**Columns:**

| Column | Required | Becomes |
| --- | --- | --- |
| `Instant` | yes | The log record's timestamp |
| `Message` | no | The log body. Without it the body is empty |
| `Severity` | no | The log severity: `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR` or `FATAL` (case-insensitive). Without it, `error` rows are `ERROR` and all others `INFO`. Derive it in SQL with a `CASE` when a log type mixes levels |
| `Application_Name` | recommended | The Loki stream's `service.name`. Without it, `Espace_Name` is used, then `Module_Name`, then `unknown` |
| any other column | no | A log attribute named exactly like the column, for example `Request_Key`, `Session_Id`, `User_Id`, `Client_IP`, `Espace_Name`, `Duration` |

- Keep OutSystems' own column name where the views agree on it, and use the
  [standard name](#standard-columns) where they do not. The derived metrics
  and the request correlation look the standard names up; SREs search Loki
  with one name per concept. The names the extractor understands are listed
  in [application observability](../../../../../../docs/architecture/application-observability.md#outsystems-fields).
- `NULL` values are left out of the record. Other columns keep their type:
  integers, floating point, `bit` and `decimal` become numbers and booleans,
  other date and time columns RFC 3339 strings in UTC (read in
  `log_timezone`), `uniqueidentifier` a GUID string, and binary columns hex
  strings.
- `Duration` is milliseconds on every log type. The OutSystems reference
  documents milliseconds for `screen` and `integration` but seconds for
  `extension`, `mobile_request` and `cyclic_job`; check the real rows, and
  where a log type logs seconds convert in SQL (`Duration * 1000 AS
  Duration`). The extractor converts milliseconds for metrics.
- The Error view spells the key `Request_key` and the Request event view
  `REQUESTKEY`: alias them `AS Request_Key`.

## Standard columns

The log views name the same thing differently. Each query returns it under
one name (alias in SQL); columns the views agree on keep their OutSystems
name.

| Standard column | Meaning | View columns it replaces |
| --- | --- | --- |
| `Request_Key` | The request the row belongs to | `Request_key` (error), `REQUESTKEY` (request event) |
| `Screen` | The entry point: the screen of a Traditional or Reactive request, the entry point an error or message was logged under | `Screen` (screen, mobile request), `Entrypoint_Name` (error, general) |
| `Action_Name` | The operation that ran: a screen action, a server or data action, an integration action, an extension action, a timer, an event | `Action_Name` (screen, extension, error, general), `Endpoint` (mobile request), `Action` (integration), `Cyclic_Job_Name` (cyclic job), `REQUESTEVENTNAME` (request event) |
| `Espace_Name` | The module | `Espace_Name`, `MODULENAME` (request event) |
| `Server` | The front-end server that handled the request | `Executed_By` (screen, integration, extension, cyclic job, mobile request), `Server` (error) |
| `Session_Id` | The platform session | `Session_Id`, `Login_Id` (mobile request) |
| `Client_IP` | The caller's address | `Client_IP` (screen, general), `Source` (mobile request; integration, where it is the caller of an exposed API) |
| `Duration` | Elapsed milliseconds | `Duration` in milliseconds (screen, integration) or seconds (extension, mobile request, cyclic job, converted); parsed from the `SLOWSQL` message (general) |
| `Error_Id` | The related error log row, absent when there is none | `Error_Id` as text (empty for none) or integer (0 for none) |

Not standardized, on purpose: `Module_Name` on the error and general logs
is the free text the app logged with (`SLOWSQL` for the platform's slow
queries), not the module, which is `Espace_Name`. `Endpoint` stays the
consumed URL on the integration log only.

## Review and tests

Each query goes through the
[probe review checklist](../../../../../../docs/architecture/database-observability.md#probe-review-checklist):
it reads only `oslog_*` tables (and joins the extractor's login is granted),
filters on an indexed `Instant` range, returns a bounded row count and needs
no more than `SELECT`. `make test-sqlserver` runs every file against its
fixture, as a login with only `SELECT` on the fixture tables and views:

- [`testdata/oslog/<log type>.sql`](../../../collectors/outsystems/testdata/oslog/)
  creates the cycle tables and the two views the query reads (the tests run
  the file as one batch, so wrap each `CREATE VIEW` in `EXEC (N'...')`).
- `testdata/oslog/<log type>.rows.sql` inserts representative rows into
  both weeks' tables: rows copied from the logging database as they are,
  user ids, sessions and IPs included, since the tests must carry them
  through; keep their `Instant` values and `Request_Key` relationships. Over
  a window that covers
  them all, the query must return every row of every table that has an
  `Instant` column, so the test catches a query that forgets the previous
  week. Detail tables (no `Instant`) may hold rows for joins.

A log type without both files fails `make test`. The fixtures are stand-ins
until they are built from the real DDL and rows.
[PatientZero](../../../../../../tools/patientzero/README.md#one-logging-schema)
builds its logging database from the same `<log type>.sql` files, so a
schema change made here reaches it too; its tests fail if a fixture loses
the shape it reads or a column it writes.
