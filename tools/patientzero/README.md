# PatientZero

A synthetic OutSystems 11 application for Heartbeat's tests and demos
([ADR 0010](../../docs/architecture/decisions/0010-patientzero-test-application.md)).
Each request runs real SQL against PatientZero's business database and
writes the log rows the platform would write for it into its own logging
database, all under one `Request_Key`, with the caller's session, user and
client IP. The db-collector's `outsystems_logs` collector reads those rows
the same way it reads a real logging database.

It is test tooling: its own Go module, image and Helm chart, never part of
the Heartbeat module, chart, images or releases.

> **Status:** first slice [16.5]: the service, its two databases on the
> sandbox SQL Server, and three entry points. Timers, extension calls, the
> fake REST partner, k6 load, scenarios, the compressed calendar and the run
> journal come later. The logging schema is the collector fixtures' stand-in
> until the logging-database survey replaces it.

## Run it on kind

```bash
make sqlserver-dev-up    # the sandbox SQL Server, with Heartbeat deployed against it
make patientzero-up
kubectl --context kind-heartbeat -n patientzero port-forward svc/patientzero 8080
```

```bash
curl -H 'X-PatientZero-User: maria.santos' -H 'X-PatientZero-Session: s1' -H 'X-Forwarded-For: 10.20.30.41' \
  -d '{"productId":2,"quantity":3}' \
  http://localhost:8080/PatientZero_App/screenservices/PatientZero_App/MainFlow/Checkout/ServiceAPIPlaceOrder
```

About a minute later the rows are in Loki as `{service_name="PatientZero"}`
(the screen log's `lag` is longer, because a screen row is stamped with the
request's start).

```bash
make patientzero-down    # remove PatientZero, its databases and its login
```

[`scripts/patientzero.sh`](scripts/patientzero.sh) does the work: it takes
the same `KIND_CLUSTER`, `SQLSERVER_CONTAINER` and `SQLSERVER_ENV_FILE` as
`scripts/kind.sh`, so it also runs against a second, throwaway cluster.
`make patientzero-up` also redeploys the Heartbeat chart with an
`outsystems_logs` collector named `patientzero` that reads `patientzero_log`.
A later `make kind-deploy` drops that collector; run `make patientzero-up`
again to restore it.

## Entry points

The caller's identity comes from headers, so a load generator can give each
virtual user a stable user and session: `X-PatientZero-User` (the username;
without it the caller is anonymous, user 0), `X-PatientZero-Session` (a new
session per request without it) and `X-Forwarded-For` (the client IP; the
connection's peer without it).

| Entry point | Request | Business SQL | Log rows |
| --- | --- | --- | --- |
| Traditional web screen `OrderList` | `GET /PatientZero_Web/OrderList.aspx` | The user's newest orders | `screen`, stamped with the request's start |
| Reactive server action `Checkout.PlaceOrder` | `POST /PatientZero_App/screenservices/PatientZero_App/MainFlow/Checkout/ServiceAPIPlaceOrder` with `{"productId":2,"quantity":3}` | Takes the quantity from stock and records the order, in one transaction | `mobile_request`, stamped with the end |
| Exposed REST API `Orders.GetOrders` | `GET /PatientZero_API/rest/Orders/orders` | The user's newest orders | `integration` (exposed, `Source` is the caller) and `general` (the API's own message, which carries the user and session the integration log has no columns for) |

A failed request adds an `error` row under the same `Request_Key`, and the
other rows' `Error_Id` points at it. An unknown product or a quantity below 1
fails `PlaceOrder` with HTTP 400. Every user is a `Customer` row in the
business database, created on first use; its id is the `User_Id` in the logs.

`GET /healthz` reports that the process runs; `GET /readyz` that both
databases answer and the schemas are in place.

## Databases and privileges

| Database | Holds | Created by |
| --- | --- | --- |
| `patientzero` | `Customer`, `Product` (five seeded products), `SalesOrder` | PatientZero, at startup |
| `patientzero_log` | Ten cycle tables per log type, `oslog_<TYPE>_0` to `_9`, and the views `oslog_<TYPE>` (current week, cycle 1 at first) and `oslog_<TYPE>_Previous` (cycle 0) | PatientZero, at startup |

- PatientZero logs in as `patientzero`, whose password `make patientzero-up`
  generates into `.env.sqlserver-dev` (`PATIENTZERO_SQL_CREDENTIAL`). The
  login is `db_owner` of the two databases and has no server permissions:
  it creates its own schemas and, later, rotates the log views.
- The Heartbeat collector keeps its own least-privilege login. In
  `patientzero_log` it is a member of `heartbeat_oslog_reader`, which has
  `SELECT` on the log views only: what a real logging database grants it.
- Log rows are inserted through the current week's views, so a rotation
  only has to move the views. A request's rows across log types commit in one
  transaction.

## One logging schema

The logging schema is not defined here. PatientZero reads the db-collector's
fixtures,
[`services/db-collector/internal/collectors/outsystems/testdata/oslog/<log type>.sql`](../../services/db-collector/internal/collectors/outsystems/testdata/oslog/),
takes the first cycle table and the current view's columns of each, and
builds the ten cycle tables and two views from them
([`internal/logschema`](internal/logschema/logschema.go)). When the survey's
DDL replaces the fixtures, PatientZero follows without a change here. A
fixture whose shape it cannot read (no cycle table with a `Cycle` default, no
current view with a column list) fails `make test`, and a column PatientZero
writes that the fixture no longer has fails the entry-point tests.

The image copies the fixtures to `/app/oslog`
([`Dockerfile`](Dockerfile), built from the repository root);
`PATIENTZERO_SCHEMA_DIR` points elsewhere.

Where the stand-in schema shows: `mobile_request.Duration` is written in
seconds and its `Error_Id` is an integer, as the OutSystems reference
documents them; the survey will tell whether that holds.

## Configuration

Environment variables (the chart sets them):

| Variable | Default | Meaning |
| --- | --- | --- |
| `PATIENTZERO_SQL_HOST` | required | SQL Server host |
| `PATIENTZERO_SQL_PORT` | `1433` | SQL Server port |
| `PATIENTZERO_SQL_CREDENTIAL` | required | `<login>:<password>`; never `sa` |
| `PATIENTZERO_SQL_TRUST_SERVER_CERTIFICATE` | `false` | `true` only for the sandbox's self-signed certificate |
| `PATIENTZERO_DATABASE` | `patientzero` | Business database |
| `PATIENTZERO_LOG_DATABASE` | `patientzero_log` | Logging database |
| `PATIENTZERO_SCHEMA_DIR` | `/app/oslog` | The oslog fixtures the logging schema is built from |
| `PATIENTZERO_LOG_READER_ROLE` | `heartbeat_oslog_reader` | Role granted `SELECT` on the log views; empty for none |
| `PATIENTZERO_FRONT_END` | the host name (the pod name on kind) | Front-end server in `Executed_By` and `Server` |
| `PATIENTZERO_LISTEN` | `:8080` | Listen address |

## Tests

```bash
go -C tools/patientzero test ./...    # also part of make test, make test-race and make vet
```

`TestLiveSQLServer` runs the entry points against a real SQL Server and reads
the rows back with the collector's own queries from
[`queries/`](../../services/db-collector/internal/probes/outsystems/queries/README.md).
It is skipped unless `PATIENTZERO_TEST_SQLSERVER` is a `sqlserver://` URL for a
login that may create databases, for example the sandbox's `sa`:

```bash
PATIENTZERO_TEST_SQLSERVER="sqlserver://sa:$(grep '^MSSQL_SA_PASSWORD=' .env.sqlserver-dev | cut -d= -f2-)@127.0.0.1:11433?encrypt=true&TrustServerCertificate=true" \
  go -C tools/patientzero test -count=1 -run TestLiveSQLServer ./internal/app/
```

It creates two databases with random names and drops them at the end.

## Why it lives in this repository

PatientZero shares two contracts with Heartbeat that change together with
the product: the logging schema above and, with the scenarios, Heartbeat's
own metric names and queries. In one repository a change to either is one
pull request and one CI run, not coordinated releases of two repositories. It is kept separable: its own module
with no imports from Heartbeat's, its own Dockerfile and chart, and nothing in
the product refers to it. It moves to its own repository if it gains users
outside Heartbeat, its own release cadence, or grows into a general
simulator.
