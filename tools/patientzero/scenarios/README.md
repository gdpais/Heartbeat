# PatientZero scenarios

A scenario is one test with an exact expectation
([ADR 0010](../../../docs/architecture/decisions/0010-patientzero-test-application.md)):
it injects a fault into the running system for a while and names the
signals Heartbeat must show for it. PatientZero runs it, checks the signals
against Prometheus, Loki and Alertmanager, and writes everything that
happened to a run journal, the known answer later investigations and
reports are compared with.

```bash
make patientzero-scenario                        # list the scenarios
make patientzero-scenario NAME=checkout-errors   # run one, with its load, and check its signals
```

It needs PatientZero on kind (`make sqlserver-dev-up`, then
`make patientzero-up`). The command starts the k6 load profile the scenario
names (`SCENARIO_LOAD=0` skips it, for a load you run yourself), starts the
scenario through PatientZero's admin endpoint, prints each step and each
check as it settles, saves the journal to
`.tmp/patientzero-scenarios/<run>.jsonl`, and exits 0 only if every check
passed. Interrupting it stops the run, which removes its fault.

| Scenario | Fault | Must show | Takes |
| --- | --- | --- | --- |
| [`checkout-errors`](checkout-errors.yaml) | Half of `PlaceOrder` calls fail for 3 minutes | The `PlaceOrder` error ratio in `heartbeat_outsystems_operations_total` above 0.3, the injected error rows in Loki, the ratio back below 0.2 after the fault | about 6 minutes |
| [`orderlist-latency`](orderlist-latency.yaml) | `OrderList` takes 3 s longer for 3 minutes | The screen p95 from `heartbeat_outsystems_operation_duration_seconds` above the 2 s SLO threshold, then back below 1 s | about 7 minutes |

The ingest scenarios (rotation, log backlog and growth, restarts and
outages of the db-collector, the OTel Collector, Loki and SQL Server) and
the SLO scenarios (`bad-deploy`, `slow-sql`) come next
([TODO 16.5](../../../TODO.md)) and use this format and runner.

## Format

One YAML file per scenario, named after it (`<name>.yaml`), in this
directory. The files are built into the PatientZero binary, which validates
every one at startup and refuses to start on an invalid file; `make test`
loads them too. [`scenario.schema.json`](scenario.schema.json) describes the
format for editors (the first line of each file points at it) and reviews.

```yaml
# yaml-language-server: $schema=scenario.schema.json
version: 1                 # format version
name: checkout-errors      # lower case, digits, dashes; the file's name
description: >-            # what it injects and what it must show
  ...
load: steady               # k6 profile run during the scenario: none, smoke, steady, peak, soak, day [steady]
steps:                     # one after another
  - name: baseline
    duration: 1m           # Go duration; at most 24h
  - name: errors
    duration: 3m
    fault:                 # optional: a step without one is a baseline or recovery
      type: errors         # a type from the fault registry
      params: {entry_points: [PlaceOrder], ratio: 0.5, message: checkout-errors}
signals:
  - name: error-ratio-rises
    promql: sum(rate(...{outcome="error"}[1m])) / sum(rate(...[1m]))
    condition: "> 0.3"
    step: errors           # the step the window is anchored on [the first step with a fault]
    from: start            # the window opens at the step's start or end [start]
    within: 4m             # how long it stays open; at most 6h
    mode: eventually       # eventually or always [eventually]
```

Each signal reads exactly one source:

| Field | Source | Value |
| --- | --- | --- |
| `promql` | Prometheus, `/api/v1/query` at the evaluation time | Every series of the instant vector, or the scalar |
| `logql` | Loki, `/loki/api/v1/query` at the evaluation time | Every series of a metric query. A log query (lines, no metric) is an error: count lines with `sum(count_over_time(... [2m]))` |
| `alert` with `state` and optional `labels` | Alertmanager, `/api/v2/alerts` | The number of active (not silenced or inhibited) alerts with that name and those labels |

`condition` (query signals) is `present`, `absent`, or a comparison
(`>`, `>=`, `<`, `<=`, `==`, `!=` and a number). A comparison must hold for
every series the query returns, and never holds for none: write the query so
it returns the series you mean (`sum(...)` for one). `state` (alert signals)
is `firing` (at least one such alert active) or `resolved` (none).

A signal is evaluated every 10 seconds from the moment its window opens, and
once more when it closes. `eventually` passes at the first evaluation that
holds and fails if none did by the end of the window; `always` fails at the
first evaluation that does not hold (a query error counts) and passes if every
one held. A run ends when its last step has ended and every signal has a
result, so a signal anchored at the end of the last step can keep a run
going past its steps (`max_duration_seconds` in the listing).

## Fault registry

A fault type has a name, parameters and a **scope**, which says which runner
can apply it ([`internal/scenario/registry.go`](../internal/scenario/registry.go)).
PatientZero's runner applies `in-process` faults only.

| Type | Scope | Parameters | Does |
| --- | --- | --- | --- |
| `latency` | in-process | `delay` (required), `jitter`, `ratio` (0 to 1, default 1), `entry_points` | Delays `ratio` of the requests by `delay` plus up to `jitter` (together at most 1m), before their business SQL, so the delay is in the log rows' `Duration` |
| `errors` | in-process | `ratio` (required, 0 to 1), `message` (at most 200 bytes), `entry_points` | Fails `ratio` of the requests before their business SQL: an `error` row with `Message` `injected by scenario: <message>` under the request's `Request_Key`, linked from its other rows, and the entry point's failure response (HTTP 500) |

`entry_points` names entry points as Heartbeat's `operation` label does:
`OrderList`, `PlaceOrder`, `GetOrders`; empty or absent for all three. Two
faults on at once add up: delays sum, and a request fails if any error fault
hits it.

A fault type implements `scenario.Fault` (`Start` and `Stop`) and is
registered with `Registry.Register`. `Stop` is called exactly once after a
successful `Start`, also when the run is stopped, with a fresh context. A
step's fault is built (its parameters checked) before the run starts, so a
bad parameter fails the start request, not half a run.

### Cluster faults

Restarts and outages of the db-collector, the OTel Collector, Loki and SQL
Server are `cluster` faults: they act on other workloads, which needs
Kubernetes RBAC (delete a pod, scale a StatefulSet) or the Docker host (stop
the SQL Server container, which runs outside the cluster). PatientZero has
neither, on purpose: a test application that can delete pods is a risk on
any cluster it is installed on. Its runner lists such scenarios as not
runnable and refuses to start them (HTTP 422).

They will run in a **host-side runner**: the same `internal/runner`,
`internal/scenario` and journal code in a command that `make
patientzero-scenario` and `scripts/kind-e2e.sh` run on the host, with the
kubeconfig and Docker they already use (`kubectl delete pod`, `kubectl
scale`, `docker stop`), registering the cluster fault types with scope
`cluster`. Traffic summaries still come from PatientZero, which counts the
requests it serves. That runner comes with the first ingest scenario that
needs it.

## Admin endpoints

PatientZero serves the runner on its own port (8080), under `/admin/`,
behind a bearer token: `Authorization: Bearer <token>`, compared in constant
time with `PATIENTZERO_ADMIN_TOKEN`, as the db-collector's admin endpoints
do. The chart reads the token from a Secret (`admin.existingSecret`,
default `patientzero-admin`, key `admin-token`); `make patientzero-up`
creates it once with a random token. Without a token every admin request
gets 401. An unauthenticated request gets 401 before routing, so it learns
nothing about the routes or the runner. Scenarios and their queries come
only from the files built into the image; a request can only name one.

| Request | Answers |
| --- | --- |
| `GET /admin/scenarios` | The scenarios (steps, signals, fault types, load, `max_duration_seconds`, whether this runner can run each and why not) and the registered fault types |
| `POST /admin/scenarios/{name}/start` | 202 and the run's status; 404 unknown, 409 while another run is active (one at a time), 422 not runnable here |
| `GET /admin/run` | The active run's status (state, current step, each check's result so far), else the last run's, else `{"state": "idle"}` |
| `POST /admin/run/stop` | Stops the active run and answers once it has ended: its fault removed, open checks `skipped`, the journal closed with `run_end` state `stopped`; 409 if none is active |
| `GET /admin/runs` | The journals on disk, newest first |
| `GET /admin/runs/{run}/journal` | One run's journal (`application/x-ndjson`), everything written so far while it runs |

By hand, through a port-forward:

```bash
kubectl --context kind-heartbeat -n patientzero port-forward svc/patientzero 8080 &
TOKEN=$(kubectl --context kind-heartbeat -n patientzero get secret patientzero-admin -o jsonpath='{.data.admin-token}' | base64 -d)
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/admin/scenarios | jq '.scenarios[] | {name, runnable}'
curl -s -X POST -H "Authorization: Bearer $TOKEN" localhost:8080/admin/scenarios/checkout-errors/start
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/admin/run | jq
```

PatientZero reads the signals at `PATIENTZERO_PROMETHEUS_URL`,
`PATIENTZERO_LOKI_URL` and `PATIENTZERO_ALERTMANAGER_URL`, which `make
patientzero-up` points at the Heartbeat namespace's services (chart values
`signals.*`). A scenario whose signals need a source with no URL is not
runnable.

## Run journal

One JSONL file per run: append-only, one event per line, never rewritten.
Its run id is the UTC start time, the scenario and four random hex digits,
`20261022T101500Z-checkout-errors-3f9a`, so the files sort by age.

**Where.** PatientZero writes it to `PATIENTZERO_JOURNAL_DIR`
(`/var/lib/patientzero/journal`, an `emptyDir` of at most 128 MiB in the
chart) and serves it on the admin endpoint; `make patientzero-scenario`
saves a copy under `.tmp/patientzero-scenarios/`. The file is written as
the run goes, so a stopped or crashed run still leaves what happened up to
that point. The `emptyDir` keeps the newest 20 journals across container
restarts, not across pods: the copy the caller saves is the lasting one. An
endpoint, not only a volume, because the image is distroless (no `kubectl
cp`) and a test or CI job needs the journal through the same port-forward
it already uses.

**Envelope.** Every line:

```json
{"v": 1, "seq": 7, "time": "2026-10-22T10:16:30.12Z", "run": "20261022T101500Z-checkout-errors-3f9a", "event": "fault_start", "data": {...}}
```

| Field | Meaning |
| --- | --- |
| `v` | Journal format version, 1. A change that renames or removes a field, or changes its meaning, makes it 2; new fields and events keep it |
| `seq` | 1, 2, 3, ... with no gap: a gap means a lost line |
| `time` | When the event happened, UTC, RFC 3339 with nanoseconds |
| `run` | The run id |
| `event` | One of the events below |
| `data` | The event's fields |

**Events**, in the order a run writes them (traffic and check lines
interleave with the steps):

| Event | When | `data` |
| --- | --- | --- |
| `run_start` | First line | `scenario`, `description`, `scenario_sha256` (of the definition below), `definition` (the scenario as run, defaults filled in), `load`, `host` (the pod), `sources` (`prometheus`, `loki`, `alertmanager` URLs), `summary_interval`, `poll_interval`, `planned_end` |
| `fault_start` | A step's fault is on | `step`, `step_index`, `type`, `scope`, `params` |
| `fault_end` | The fault is off | As `fault_start`, plus `reason` (`completed`, `stopped`, or `error` with `error`). A fault that failed to start has a `fault_end` with `error` and no `fault_start`, and ends the run |
| `traffic` | Every 15 s, then once more at the end with `scope` `interval`; then one with `scope` `run` | `scope`, `from`, `to`, `requests`, `failed` (requests that wrote an error row, refused orders included), `injected` (failed by a fault), `users` and `sessions` (distinct, anonymous callers not counted as users), `users_capped` / `sessions_capped` when the 50,000-per-interval or 200,000-per-run cap was reached, and `entry_points`: per entry point `requests`, `failed`, `injected`, `anonymous`, `duration_ms_sum`, `duration_ms_max` |
| `check` | A signal has its result | `signal`, `kind` (`promql`, `logql`, `alert`), `query` (the expression or alert name), `condition` or `state` and `labels`, `step`, `from`, `mode`, `within`, `window_start`, `window_end`, `result` (`pass`, `fail`, `skipped` when the run stopped before the window closed), `evaluated_at` (the deciding evaluation), `attempts`, `values` (that evaluation's values, sorted, at most 20), `series` (how many it returned), `error` (that evaluation's error, if any) |
| `run_end` | Last line | `scenario`, `state` (`passed`, `failed` when a check failed, `stopped`, `error`), `error`, `seconds`, `checks` (`pass`, `fail`, `skipped` counts), `requests`, `failed`, `injected`, `users`, `sessions` (the whole run) |

Traffic is counted as PatientZero serves it, from the request's start to
its response, by the same handlers that write the log rows: the known
answer for what Heartbeat should report. It is counted only while a run is
active.

```bash
jq -c 'select(.event == "check") | .data | {signal, result, values}' .tmp/patientzero-scenarios/<run>.jsonl
jq -c 'select(.event == "traffic" and .data.scope == "interval") | .data | {to, requests, injected, users}' .tmp/patientzero-scenarios/<run>.jsonl
```

## Adding a scenario

1. Write `<name>.yaml` here, with the schema line at the top.
2. Use a fault type from the registry, or add one: an in-process fault in
   [`internal/inject`](../internal/inject/inject.go) (registered by
   `inject.Register`), with its parameters in the schema's `fault` section;
   a cluster fault waits for the host-side runner above.
3. Read the expected signals from metrics and logs Heartbeat already
   produces; leave room for the collector's lag (about a minute; screen rows
   another minute) and the query's own range. A query's range also reaches
   back before the run: two runs back to back can see each other's rows, so
   keep ranges short (`[1m]`, `[2m]`).
4. `go -C tools/patientzero test ./...` validates the file and checks that
   the schema still matches the registry; then run it on kind.
