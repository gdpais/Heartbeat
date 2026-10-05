#!/bin/sh
# End-to-end acceptance checks for the kind/Helm delivery (ADR 0003). CI runs
# it; `make kind-e2e` runs it locally next to your dev cluster without touching
# it.
#
# By default it creates its own uniquely named cluster, kubeconfig and SQL
# Server container, and removes exactly those on exit, so concurrent runs are
# isolated and never delete developer resources. Set E2E_CLUSTER to run against
# an existing kind cluster instead (it is then left in place).
#
# Needs: docker, kind, kubectl, helm, jq. Prints measured evidence as it goes.
set -eu
cd "$(dirname "$0")/.."

: "${KIND_NODE_IMAGE:?set KIND_NODE_IMAGE (the Makefile pins it)}"
run_id=$(date +%s)-$$
if [ -n "${E2E_CLUSTER:-}" ]; then
	CLUSTER=$E2E_CLUSTER
	own_cluster=false
else
	CLUSTER=heartbeat-e2e-$run_id
	own_cluster=true
fi
NAMESPACE=heartbeat
CONTEXT=kind-$CLUSTER
SQL_CONTAINER=$CLUSTER-sqlserver
WORK=$(mktemp -d)

export KIND_CLUSTER=$CLUSTER HEARTBEAT_NAMESPACE=$NAMESPACE
export SQLSERVER_CONTAINER=$SQL_CONTAINER SQLSERVER_ENV_FILE=$WORK/sqlserver.env
# No host port: the test target is reachable only on the kind network.
export SQLSERVER_HOST_PORT=
if $own_cluster; then
	# A private kubeconfig and no host port mappings, so this run cannot clash
	# with the developer cluster or another run.
	export KUBECONFIG=$WORK/kubeconfig
	export KIND_CONFIG=${E2E_KIND_CONFIG-}
fi

port_forward_pid=
cleanup() {
	status=$?
	[ -z "$port_forward_pid" ] || kill "$port_forward_pid" 2>/dev/null || true
	if [ "$status" -ne 0 ] && kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
		echo "--- diagnostics"
		kubectl --context "$CONTEXT" -n "$NAMESPACE" get pods -o wide || true
		kubectl --context "$CONTEXT" -n "$NAMESPACE" logs statefulset/db-collector --tail 50 || true
	fi
	docker rm -f "$SQL_CONTAINER" >/dev/null 2>&1 || true
	docker volume rm "$SQL_CONTAINER-data" >/dev/null 2>&1 || true
	if $own_cluster; then
		kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
		rm -rf ".tmp/kind/$CLUSTER"
	fi
	rm -rf "$WORK"
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

k() { kubectl --context "$CONTEXT" "$@"; }
step() { printf '\n=== %s\n' "$*"; }
pass() { printf 'PASS  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

# GET through the API server's service proxy: no port-forwards or host ports.
svc_get() { k get --raw "/api/v1/namespaces/$NAMESPACE/services/$1/proxy$2"; }
prom() { svc_get prometheus:9090 "/api/v1/query?query=$(printf '%s' "$1" | jq -sRr @uri)"; }
# Prints the first sample value of a PromQL query, or nothing.
prom_value() { prom "$1" | jq -r '.data.result[0].value[1] // empty'; }
# The pod proxy reaches the collector even while it is unready.
pod_get() { k get --raw "/api/v1/namespaces/$NAMESPACE/pods/db-collector-0:8082/proxy$1"; }

# Operator access to the admin endpoints, the documented way: kubectl
# port-forward (not subject to the NetworkPolicy) plus the admin token. The API
# server's proxy cannot be used: it drops the Authorization header.
# kubectl runs directly, not through k, so $! is kubectl itself and kill
# stops it (a backgrounded function would leave it orphaned). The token goes
# to curl in a header file, never on a command line.
admin_forward() {
	kubectl --context "$CONTEXT" -n "$NAMESPACE" port-forward pod/db-collector-0 :8082 >"$WORK/port-forward.log" 2>&1 &
	port_forward_pid=$!
	wait_for 30 "port-forward to the collector" grep -q '^Forwarding from 127.0.0.1:' "$WORK/port-forward.log"
	admin_port=$(sed -n 's/^Forwarding from 127\.0\.0\.1:\([0-9]*\) .*/\1/p' "$WORK/port-forward.log" | head -n 1)
	(
		umask 077
		k -n "$NAMESPACE" get secret heartbeat-runtime -o json |
			jq -r '"Authorization: Bearer " + (.data["admin-token"] | @base64d)' >"$WORK/admin-header"
	)
}
admin_unforward() {
	kill "$port_forward_pid" 2>/dev/null || true
	port_forward_pid=
}
# admin_status <path> [header]: HTTP status of GET <path> through the port-forward.
admin_status() {
	if [ -n "${2:-}" ]; then
		curl -sS -o /dev/null -w '%{http_code}' --max-time 5 -H "$2" "http://127.0.0.1:$admin_port$1"
	else
		curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$admin_port$1"
	fi
}
admin_config() {
	curl -sS --fail --max-time 5 -H "@$WORK/admin-header" "http://127.0.0.1:$admin_port/admin/config"
}

# wait_for <seconds> <description> <command...>: retries until the command succeeds.
wait_for() {
	limit=$1 what=$2
	shift 2
	start=$(date +%s)
	until "$@" >/dev/null 2>&1; do
		[ $(($(date +%s) - start)) -lt "$limit" ] || fail "timed out after ${limit}s waiting for $what"
		sleep 3
	done
	printf '      %s after %ss\n' "$what" $(($(date +%s) - start))
}

collector_pod_uid() { k -n "$NAMESPACE" get pod db-collector-0 -o jsonpath='{.metadata.uid}'; }
collector_restarts() { k -n "$NAMESPACE" get pod db-collector-0 -o jsonpath='{.status.containerStatuses[0].restartCount}'; }
config_version() { admin_config | jq -r '.version // empty'; }
target_up() { [ "$(prom_value "heartbeat_collector_target_up{target=\"$1\"}")" = "$2" ]; }
version_changed_from() { [ "$(config_version)" != "$1" ]; }
reload_error_is_set() { admin_config | jq -e '.last_reload_err != ""'; }
reload_error_is_clear() { admin_config | jq -e '.last_reload_err == ""'; }
watchdog_active() { svc_get alertmanager:9093 /api/v2/alerts | jq -e 'any(.[]; .labels.alertname == "Watchdog")'; }
last_success() { prom_value "heartbeat_collector_target_last_success_timestamp_seconds{target=\"$1\"}"; }
# gt <a> <b>: numeric a > b, for decimals too.
gt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a > b) }'; }
success_after() { now=$(last_success "$1"); [ -n "$now" ] && gt "$now" "$2"; }
replacement_ready() {
	[ "$(collector_pod_uid)" != "$1" ] &&
		[ "$(k -n "$NAMESPACE" get pod db-collector-0 -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ]
}
prometheus_answers() { [ -n "$(prom_value prometheus_tsdb_lowest_timestamp_seconds)" ]; }
# Prints the type Prometheus scraped for a metric (counter, gauge, ...).
metric_type() { svc_get prometheus:9090 "/api/v1/metadata?metric=$1" | jq -r --arg m "$1" '.data[$m][0].type // empty'; }
has_value() { [ -n "$(prom_value "$1")" ]; }
probe_pod_phase() { k -n "$NAMESPACE" get pod np-probe -o jsonpath='{.status.phase}'; }
probe_pod_exit() { k -n "$NAMESPACE" get pod np-probe -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}'; }
probe_pod_done() { case "$(probe_pod_phase)" in Succeeded | Failed) true ;; *) false ;; esac; }
target_fresh() {
	age=$(prom_value "time() - heartbeat_collector_target_last_success_timestamp_seconds{target=\"$1\"}")
	[ -n "$age" ] && [ "$(printf '%.0f' "$age")" -lt 60 ]
}

# ----------------------------------------------------------------------------
step "cluster $CLUSTER, images, SQL Server target"
scripts/kind.sh cluster
scripts/kind.sh images
. "./.tmp/kind/$CLUSTER/images.env"

password="E2e-$(openssl rand -hex 16)"
umask 077
# sa is for setup and the session sampler below; the collector logs in as a
# least-privilege login that kind.sh sqlserver-up creates.
printf 'MSSQL_SA_PASSWORD=%s\nHEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV=heartbeat_collector:%s\n' \
	"$password" "E2e-$(openssl rand -hex 16)" >"$SQLSERVER_ENV_FILE"
umask 022
# Points the collector at this run's container, plus a second target that can
# never connect, to show that one failed target does not stop the others.
sed -e "s/host: heartbeat-sqlserver-dev/host: $SQL_CONTAINER/" infra/helm/values/sqlserver-dev.yaml >"$WORK/e2e.yaml"
cat >>"$WORK/e2e.yaml" <<EOF
          - name: unreachable
            environment: local-dev
            host: $CLUSTER-missing-sqlserver
            port: 1433
            database_name: master
            credential_ref: env/sqlserver-dev
EOF
export EXTRA_VALUES="$WORK/e2e.yaml ${E2E_EXTRA_VALUES:-}"
scripts/kind.sh sqlserver-up

# ----------------------------------------------------------------------------
step "every workload is ready"
for workload in $(k -n "$NAMESPACE" get deployment,statefulset -o name); do
	k -n "$NAMESPACE" rollout status "$workload" --timeout 5m >/dev/null
	pass "$workload ready"
done

step "the running collector is the image just built"
arch=$(docker exec "$CLUSTER-control-plane" uname -m)
running=$(k -n "$NAMESPACE" get pod db-collector-0 -o jsonpath='{.status.containerStatuses[0].image}')
case "$running" in
*"heartbeat/db-collector:$DB_COLLECTOR_TAG") pass "db-collector runs $DB_COLLECTOR_TAG (content-derived tag, node arch $arch)" ;;
*) fail "db-collector runs $running, expected tag $DB_COLLECTOR_TAG" ;;
esac

# ----------------------------------------------------------------------------
step "SQL Server target is collected and visible"
wait_for 180 "target up in Prometheus" target_up sqlserver-dev 1
wait_for 60 "fresh last success" target_fresh sqlserver-dev
sessions=$(prom_value "sum(heartbeat_sqlserver_sessions{target=\"sqlserver-dev\"})")
[ -n "$sessions" ] || fail "no heartbeat_sqlserver_sessions for sqlserver-dev"
pass "probe metrics present (sessions=$sessions)"
sysadmin=$(prom_value "heartbeat_collector_target_login_sysadmin{target=\"sqlserver-dev\"}")
[ "$sysadmin" = 0 ] || fail "heartbeat_collector_target_login_sysadmin for sqlserver-dev is '$sysadmin', expected 0"
pass "the collector logs in without sysadmin (least-privilege login)"
# Cumulative SQL Server values are scraped as counters, so rate() applies.
for metric in heartbeat_sqlserver_wait_seconds_total heartbeat_sqlserver_batch_requests_total heartbeat_sqlserver_transactions_total; do
	type=$(metric_type "$metric")
	[ "$type" = counter ] || fail "$metric has type [$type], expected counter"
done
pass "waits and throughput exported as counters"
# One server-wide series per throughput counter (issue #4).
for metric in heartbeat_sqlserver_batch_requests_total heartbeat_sqlserver_transactions_total; do
	count=$(prom_value "count($metric{target=\"sqlserver-dev\"})")
	[ "$count" = 1 ] || fail "$metric has [$count] series for sqlserver-dev, expected 1"
done
wait_for 120 "batch request rate" has_value "rate(heartbeat_sqlserver_batch_requests_total{target=\"sqlserver-dev\"}[1m])"
pass "one series per throughput counter, and rate() returns a value"
# Core CPU, buffer cache and file I/O signals. The scheduler monitor writes its
# first CPU record about a minute after SQL Server starts.
wait_for 180 "CPU utilisation" has_value "heartbeat_sqlserver_cpu_sql_process_ratio{target=\"sqlserver-dev\"}"
for metric in heartbeat_sqlserver_page_life_expectancy_seconds heartbeat_sqlserver_buffer_cache_hit_ratio; do
	[ -n "$(prom_value "$metric{target=\"sqlserver-dev\"}")" ] || fail "no $metric for sqlserver-dev"
done
type=$(metric_type heartbeat_sqlserver_database_file_reads_total)
[ "$type" = counter ] || fail "heartbeat_sqlserver_database_file_reads_total has type [$type], expected counter"
files=$(prom_value 'count(heartbeat_sqlserver_database_file_reads_total{target="sqlserver-dev"})')
wait_for 120 "file read rate" has_value "rate(heartbeat_sqlserver_database_file_reads_total{target=\"sqlserver-dev\"}[1m])"
pass "cpu, buffer_cache and file_io series present ($files database files)"
# Self-observability: Go runtime and process metrics, probe durations, and
# error counters created at 0 for every scheduled probe (4 reasons each).
for query in 'go_goroutines{job="db-collector"}' 'process_resident_memory_bytes{job="db-collector"}' \
	'heartbeat_collector_probe_duration_seconds_count{target="sqlserver-dev"}'; do
	[ -n "$(prom_value "$query")" ] || fail "no $query"
done
probes=$(prom_value 'count(heartbeat_collector_probe_duration_seconds_count{target="sqlserver-dev"})')
errors=$(prom_value 'count(heartbeat_collector_probe_errors_total{target="sqlserver-dev"})')
[ -n "$probes" ] && [ "$errors" = "$((probes * 4))" ] ||
	fail "probe error counters: [$errors] series for [$probes] probes, expected 4 per probe"
pass "collector self-metrics present ($probes probes timed, $errors error counters)"
svc_get grafana:3000 /api/dashboards/uid/sqlserver-overview | jq -e '.dashboard.uid == "sqlserver-overview"' >/dev/null ||
	fail "Grafana has no sqlserver-overview dashboard"
seen=$(svc_get grafana:3000 "/api/datasources/proxy/uid/prometheus/api/v1/query?query=heartbeat_collector_target_up" |
	jq -r '[.data.result[].metric.target] | sort | join(",")')
case ",$seen," in
*,sqlserver-dev,*) pass "Grafana's Prometheus datasource returns the target ($seen)" ;;
*) fail "Grafana does not see sqlserver-dev (saw: $seen)" ;;
esac

step "a failed target is isolated"
wait_for 120 "unreachable target reported down" target_up unreachable 0
target_up sqlserver-dev 1 || fail "healthy target stopped when another failed"
pass "unreachable target down, sqlserver-dev still up"
wait_for 60 "probe errors counted for the unreachable target" has_value \
	'sum(heartbeat_collector_probe_errors_total{target="unreachable"}) > 0'
pass "probe errors counted for the unreachable target"

step "Prometheus alerts reach Alertmanager (Watchdog)"
wait_for 120 "Watchdog in Alertmanager" watchdog_active
pass "Watchdog active in Alertmanager"

# ----------------------------------------------------------------------------
step "unauthenticated endpoints expose no diagnostics"
readyz=$(pod_get /readyz)
[ "$(printf '%s' "$readyz" | jq -c keys)" = '["status"]' ] || fail "/readyz serves more than its status: $readyz"
admin_forward
for header in "" "Authorization: Bearer wrong-token"; do
	code=$(admin_status /admin/config "$header")
	[ "$code" = 401 ] || fail "GET /admin/config with header '$header' returned $code, want 401"
done
admin_config | jq -e '.readiness.status == "ready" and .version != ""' >/dev/null ||
	fail "GET /admin/config with the admin token did not return diagnostics"
pass "/readyz is status only ($readyz); /admin/config answers 401 without the token"

step "the NetworkPolicy admits only Prometheus to the collector"
collector_url=http://db-collector-headless:8082/healthz
k -n "$NAMESPACE" exec deployment/prometheus -c prometheus-server -- wget -q -T 5 -O /dev/null "$collector_url" ||
	fail "Prometheus cannot reach the collector's port 8082"
# Same image, tool and Service name as from Prometheus, from a pod the policy
# does not list. kindnet drops the connection, so only timeout's kill ends it
# (wget's own -T is longer): exit 124 or 143. Any other failure (DNS,
# refused) is not the policy. sh stays PID 1: busybox timeout execs wget in
# its own process, and PID 1 ignores the SIGTERM it would send.
prom_image=$(k -n "$NAMESPACE" get deployment prometheus -o jsonpath='{.spec.template.spec.containers[?(@.name=="prometheus-server")].image}')
k -n "$NAMESPACE" delete pod np-probe --ignore-not-found --wait=true >/dev/null
k -n "$NAMESPACE" run np-probe --image="$prom_image" --restart=Never --labels=app.kubernetes.io/name=np-probe \
	--command -- sh -c 'timeout 10 wget -q -T 30 -O /dev/null "$0"; exit $?' "$collector_url" >/dev/null
wait_for 120 "probe pod finished" probe_pod_done
probe_log=$(k -n "$NAMESPACE" logs np-probe 2>&1 || true)
phase=$(probe_pod_phase)
exit_code=$(probe_pod_exit)
k -n "$NAMESPACE" delete pod np-probe --wait=false >/dev/null
[ "$phase" = Failed ] || fail "a pod other than Prometheus reached the collector's port 8082"
case "$exit_code" in
124 | 143) ;;
*) fail "probe pod failed with exit code $exit_code, not a connection timeout: $probe_log" ;;
esac
pass "Prometheus reaches port 8082; another pod in the namespace times out (exit $exit_code)"

# ----------------------------------------------------------------------------
step "a valid ConfigMap update is hot-reloaded without a restart"
uid_before=$(collector_pod_uid)
version_before=$(config_version)
sed -e 's/scrape_interval: 15s/scrape_interval: 20s/' "$WORK/e2e.yaml" >"$WORK/e2e-reload.yaml"
EXTRA_VALUES="$WORK/e2e-reload.yaml ${E2E_EXTRA_VALUES:-}" scripts/kind.sh deploy >/dev/null
# kubelet propagates ConfigMap updates within about a minute.
wait_for 180 "new config version active" version_changed_from "$version_before"
[ "$(collector_pod_uid)" = "$uid_before" ] || fail "collector pod was replaced by a config change"
pass "config $version_before -> $(config_version), same pod"

step "an invalid ConfigMap update is rejected while collection continues"
version_good=$(config_version)
restarts_before=$(collector_restarts)
# Valid YAML that the Go loader rejects (no grafana/loki/alertmanager sections),
# written straight to the ConfigMap: Helm's values schema refuses to render it.
k -n "$NAMESPACE" get configmap heartbeat-integrations -o json >"$WORK/integrations-good.json"
jq '.data["integrations.yaml"] = "collectors: [{id: broken, kind: sqlserver, enabled: true}]\n"' \
	"$WORK/integrations-good.json" | k replace -f - >/dev/null
wait_for 180 "reload error reported" reload_error_is_set
admin_config | jq -r '"      last_reload_err: " + .last_reload_err'
[ "$(config_version)" = "$version_good" ] || fail "invalid config replaced the active version"
wait_for 90 "collection still fresh" target_fresh sqlserver-dev
[ "$(collector_restarts)" = "$restarts_before" ] || fail "collector restarted on invalid config"
pass "invalid config rejected, version $version_good kept, still collecting"
# Restore as Helm's field manager: Helm 4 upgrades with server-side apply and
# would otherwise conflict with the out-of-band edit.
jq 'del(.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp)' "$WORK/integrations-good.json" |
	k apply --server-side --field-manager=helm --force-conflicts -f - >/dev/null
wait_for 180 "reload error cleared" reload_error_is_clear
admin_unforward

# ----------------------------------------------------------------------------
step "an unavailable SQL Server shows as failed without restarting the collector"
restarts_before=$(collector_restarts)
docker stop "$SQL_CONTAINER" >/dev/null
wait_for 180 "target reported down" target_up sqlserver-dev 0
pod_get /healthz >/dev/null ||
	fail "collector liveness failed during a database outage"
docker start "$SQL_CONTAINER" >/dev/null
wait_for 300 "target recovered" target_up sqlserver-dev 1
[ "$(collector_restarts)" = "$restarts_before" ] || fail "collector restarted during a database outage"
pass "outage reported, recovered, restartCount unchanged ($restarts_before)"

# ----------------------------------------------------------------------------
step "a collector update drains the old pod before the new one polls"
# Samples, every second: running collector containers in the cluster, and
# collector sessions on SQL Server. One collector holds at most 2 connections
# per target (defaultMaxOpenConns), so more than 2 means two collectors
# overlapped. Pod names and NATed addresses cannot tell old and new apart.
sample_concurrency() {
	max_pods=0
	max_sql=0
	while [ ! -f "$WORK/stop-sampling" ]; do
		pods=$(k -n "$NAMESPACE" get pods -l app.kubernetes.io/name=db-collector -o json |
			jq '[.items[] | select(.status.containerStatuses[0].state.running != null)] | length')
		sql=$(SQLCMDPASSWORD=$password docker exec -e SQLCMDPASSWORD "$SQL_CONTAINER" /opt/mssql-tools18/bin/sqlcmd \
			-S localhost -U sa -C -h -1 -W -Q "SET NOCOUNT ON; SELECT COUNT(*) FROM sys.dm_exec_sessions WHERE program_name = 'HeartbeatDBCollector'" 2>/dev/null | tr -dc '0-9')
		[ "${pods:-0}" -gt "$max_pods" ] && max_pods=$pods
		[ "${sql:-0}" -gt "$max_sql" ] && max_sql=$sql
		printf '%s %s\n' "$max_pods" "$max_sql" >"$WORK/concurrency"
		sleep 1
	done
}
sample_concurrency &
sampler=$!
last_before=$(last_success sqlserver-dev)
uid_before=$(collector_pod_uid)
start=$(date +%s)
k -n "$NAMESPACE" rollout restart statefulset/db-collector >/dev/null
wait_for 180 "replacement pod ready" replacement_ready "$uid_before"
rollout_s=$(($(date +%s) - start))
wait_for 120 "collection resumed" success_after sqlserver-dev "$last_before"
first_after=$(last_success sqlserver-dev)
touch "$WORK/stop-sampling"
wait "$sampler" || true
read -r max_pods max_sql <"$WORK/concurrency"
gap=$(awk -v a="$first_after" -v b="$last_before" 'BEGIN { printf "%.0f", a - b }')
printf '      rollout %ss, collection gap %ss, max running collectors %s, max collector sessions on SQL Server %s\n' \
	"$rollout_s" "$gap" "$max_pods" "$max_sql"
[ "$max_pods" -le 1 ] || fail "$max_pods collector pods ran at once"
[ "$max_sql" -le 2 ] || fail "$max_sql collector sessions on SQL Server: two collectors overlapped"
pass "singleton update: never more than one collector"

# ----------------------------------------------------------------------------
step "stateful restarts keep persistent data"
lowest=$(prom_value 'prometheus_tsdb_lowest_timestamp_seconds')
silence_id=$(k -n "$NAMESPACE" exec alertmanager-0 -c alertmanager -- amtool silence add alertname=E2eProbe \
	--alertmanager.url=http://localhost:9093 --duration=2h --author=kind-e2e --comment="persistence check")
k -n "$NAMESPACE" delete pod -l app.kubernetes.io/name=prometheus --wait=true >/dev/null
k -n "$NAMESPACE" delete pod alertmanager-0 --wait=true >/dev/null
k -n "$NAMESPACE" rollout status deployment/prometheus --timeout 3m >/dev/null
k -n "$NAMESPACE" rollout status statefulset/alertmanager --timeout 3m >/dev/null
wait_for 120 "Prometheus answering" prometheus_answers
lowest_after=$(prom_value 'prometheus_tsdb_lowest_timestamp_seconds')
gt "$lowest_after" "$lowest" &&
	fail "Prometheus lost its data on restart (lowest timestamp $lowest -> $lowest_after)"
svc_get alertmanager:9093 /api/v2/silences | jq -e --arg id "$silence_id" 'any(.[]; .id == $id)' >/dev/null ||
	fail "Alertmanager lost its silence on restart"
pass "Prometheus TSDB and Alertmanager silences survived pod replacement"

printf '\nAll kind e2e checks passed on %s.\n' "$CLUSTER"
