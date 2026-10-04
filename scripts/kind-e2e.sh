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

cleanup() {
	status=$?
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
pod_readyz() { pod_get /readyz 2>/dev/null || true; }

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
config_version() { pod_readyz | jq -r '.config_version // empty'; }
target_up() { [ "$(prom_value "heartbeat_collector_target_up{target=\"$1\"}")" = "$2" ]; }
version_changed_from() { [ "$(config_version)" != "$1" ]; }
reload_error_is_set() { pod_readyz | jq -e '.last_reload_err != ""'; }
reload_error_is_clear() { pod_readyz | jq -e '.last_reload_err == ""'; }
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
# Exact, unpadded label values; one server-wide series each (issue #4).
counters=$(prom "heartbeat_sqlserver_throughput{target=\"sqlserver-dev\"}" |
	jq -r '[.data.result[].metric.counter_name] | sort | join(",")')
[ "$counters" = "Batch Requests/sec,Transactions/sec" ] ||
	fail "throughput counter_name values are [$counters], expected Batch Requests/sec,Transactions/sec"
pass "throughput counters labelled without padding ($counters)"
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

step "Prometheus alerts reach Alertmanager (Watchdog)"
wait_for 120 "Watchdog in Alertmanager" watchdog_active
pass "Watchdog active in Alertmanager"

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
pod_readyz | jq -r '"      last_reload_err: " + .last_reload_err'
[ "$(config_version)" = "$version_good" ] || fail "invalid config replaced the active version"
wait_for 90 "collection still fresh" target_fresh sqlserver-dev
[ "$(collector_restarts)" = "$restarts_before" ] || fail "collector restarted on invalid config"
pass "invalid config rejected, version $version_good kept, still collecting"
# Restore as Helm's field manager: Helm 4 upgrades with server-side apply and
# would otherwise conflict with the out-of-band edit.
jq 'del(.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp)' "$WORK/integrations-good.json" |
	k apply --server-side --field-manager=helm --force-conflicts -f - >/dev/null
wait_for 180 "reload error cleared" reload_error_is_clear

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
		sql=$(docker exec -e SQLCMDPASSWORD="$password" "$SQL_CONTAINER" /opt/mssql-tools18/bin/sqlcmd \
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
