#!/bin/sh
# Local kind workflow for Heartbeat (ADR 0003). The Makefile calls this; run
# `make help` for the targets.
#
# Every kubectl and helm call names the kind context explicitly, so this script
# never acts on whatever cluster your current context points at.
#
# Usage: scripts/kind.sh <command>
#   up               create the cluster if needed, build and load images, deploy
#   down             delete the kind cluster (and only it)
#   cluster          create the cluster if it does not exist
#   images           build both images, tag them by content, load them into kind
#   deploy           create the local Secrets and helm upgrade --install
#   status           show pods and services
#   sqlserver-up     start the Docker SQL Server target and deploy against it
#   sqlserver-down   remove the SQL Server container and its volume
#
# Environment (defaults in brackets):
#   KIND_NODE_IMAGE      node image, pinned in the Makefile (required for cluster)
#   KIND_CLUSTER         cluster name [heartbeat]
#   KIND_CONFIG          cluster config; empty for none [infra/kind/cluster.yaml]
#   PROFILE              full | minimal [full]
#   EXTRA_VALUES         more values files, space-separated []
#   HEARTBEAT_NAMESPACE  namespace [heartbeat]
#   SQLSERVER_CONTAINER  Docker SQL Server container [heartbeat-sqlserver-dev]
#   SQLSERVER_ENV_FILE   SA password and collector credential file [.env.sqlserver-dev]
#   SQLSERVER_HOST_PORT  loopback host port for SQL Server; empty for none [11433]
set -eu
cd "$(dirname "$0")/.."

CLUSTER=${KIND_CLUSTER:-heartbeat}
KIND_CONFIG=${KIND_CONFIG-infra/kind/cluster.yaml}
PROFILE=${PROFILE:-full}
EXTRA_VALUES=${EXTRA_VALUES:-}
NAMESPACE=${HEARTBEAT_NAMESPACE:-heartbeat}
SQLSERVER_CONTAINER=${SQLSERVER_CONTAINER:-heartbeat-sqlserver-dev}
SQLSERVER_ENV_FILE=${SQLSERVER_ENV_FILE:-.env.sqlserver-dev}
SQLSERVER_HOST_PORT=${SQLSERVER_HOST_PORT-11433}
SQLSERVER_IMAGE=mcr.microsoft.com/mssql/server:2022-latest
RELEASE=heartbeat
CHART=infra/helm/heartbeat
VALUES_DIR=infra/helm/values
STATE_DIR=.tmp/kind/$CLUSTER
CONTEXT=kind-$CLUSTER

kubectl_() { kubectl --context "$CONTEXT" "$@"; }
log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

cluster_exists() { kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; }

cmd_cluster() {
	if cluster_exists; then
		log "kind cluster $CLUSTER exists"
		return
	fi
	: "${KIND_NODE_IMAGE:?set KIND_NODE_IMAGE (the Makefile pins it)}"
	log "creating kind cluster $CLUSTER"
	if [ -n "$KIND_CONFIG" ]; then
		kind create cluster --name "$CLUSTER" --image "$KIND_NODE_IMAGE" --config "$KIND_CONFIG" --wait 120s
	else
		kind create cluster --name "$CLUSTER" --image "$KIND_NODE_IMAGE" --wait 120s
	fi
}

# Maps `uname -m` on the node to Docker's architecture names.
node_arch() {
	machine=$(docker exec "$CLUSTER-control-plane" uname -m)
	case "$machine" in
	x86_64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) die "unsupported node architecture $machine" ;;
	esac
}

# Builds for the node's own architecture and tags each image by its content
# (image ID), so a code change always yields a new tag and a stale image
# cannot hide behind a reused one like :local.
cmd_images() {
	cluster_exists || die "kind cluster $CLUSTER does not exist; run make kind-up"
	arch=$(node_arch)
	mkdir -p "$STATE_DIR"
	: >"$STATE_DIR/images.env.tmp"
	for svc in db-collector otel-gateway; do
		log "building heartbeat/$svc for linux/$arch"
		# --provenance=false keeps a single-platform image that kind can load.
		docker build --quiet --platform "linux/$arch" --provenance=false \
			-t "heartbeat/$svc:build" -f "services/$svc/Dockerfile" . >/dev/null
		id=$(docker image inspect -f '{{.Id}}' "heartbeat/$svc:build")
		built_arch=$(docker image inspect -f '{{.Architecture}}' "heartbeat/$svc:build")
		[ "$built_arch" = "$arch" ] || die "heartbeat/$svc is $built_arch, node is $arch"
		tag=dev-$(printf '%s' "${id#sha256:}" | cut -c1-12)
		docker tag "heartbeat/$svc:build" "heartbeat/$svc:$tag"
		log "loading heartbeat/$svc:$tag into $CLUSTER"
		kind load docker-image --name "$CLUSTER" "heartbeat/$svc:$tag" >/dev/null
		var=$(printf '%s' "$svc" | tr 'a-z-' 'A-Z_')_TAG
		printf '%s=%s\n' "$var" "$tag" >>"$STATE_DIR/images.env.tmp"
	done
	mv "$STATE_DIR/images.env.tmp" "$STATE_DIR/images.env"
}

# Applies a Secret built with --dry-run, so reruns update it in place.
apply_secret() {
	name=$1
	shift
	kubectl_ -n "$NAMESPACE" create secret generic "$name" "$@" --dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
}

# Local-only Secrets. Production creates these outside the chart (ADR 0005).
cmd_secrets() {
	kubectl_ create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
	apply_secret heartbeat-runtime --from-literal=admin-token=local-admin-token
	apply_secret grafana-admin --from-literal=admin-user=admin --from-literal=admin-password=admin
}

chart_deps() {
	if ! helm dependency list "$CHART" 2>/dev/null | awk 'NR > 1 && NF { if ($NF != "ok") bad = 1 } END { exit bad }'; then
		log "fetching chart dependencies"
		scripts/chart-deps.sh
	fi
}

cmd_deploy() {
	cluster_exists || die "kind cluster $CLUSTER does not exist; run make kind-up"
	[ -f "$STATE_DIR/images.env" ] || cmd_images
	# shellcheck disable=SC1090
	. "./$STATE_DIR/images.env"
	chart_deps
	cmd_secrets
	set -- -f "$VALUES_DIR/kind.yaml"
	case "$PROFILE" in
	full) ;;
	minimal) set -- "$@" -f "$VALUES_DIR/minimal.yaml" ;;
	*) die "PROFILE must be full or minimal, not $PROFILE" ;;
	esac
	# sqlserver-up leaves a marker so later deploys keep the SQL Server target.
	if [ -f "$STATE_DIR/sqlserver-dev" ]; then
		set -- "$@" -f "$VALUES_DIR/sqlserver-dev.yaml"
	fi
	for file in $EXTRA_VALUES; do
		set -- "$@" -f "$file"
	done
	log "deploying release $RELEASE ($PROFILE) to $CONTEXT/$NAMESPACE"
	helm upgrade --install "$RELEASE" "$CHART" \
		--kube-context "$CONTEXT" --namespace "$NAMESPACE" \
		"$@" \
		--set "dbCollector.image.tag=$DB_COLLECTOR_TAG" \
		--set "otelGateway.image.tag=$OTEL_GATEWAY_TAG" \
		--wait --timeout 10m
	cmd_status
}

cmd_status() {
	kubectl_ -n "$NAMESPACE" get pods,svc
}

cmd_up() {
	cmd_cluster
	cmd_images
	cmd_deploy
}

cmd_down() {
	if cluster_exists; then
		kind delete cluster --name "$CLUSTER"
	else
		log "kind cluster $CLUSTER does not exist"
	fi
	rm -rf "$STATE_DIR"
}

env_value() {
	grep "^$1=" "$SQLSERVER_ENV_FILE" | head -n 1 | cut -d= -f2-
}

# SQL Server stays a Docker container outside the cluster (ADR 0003). It joins
# the kind network, so pods reach it by container name, and publishes 1433
# only on the host's loopback (or not at all with SQLSERVER_HOST_PORT=).
# The collector logs in as a least-privilege login that this creates with sa;
# the sa password never leaves the env file and the container.
cmd_sqlserver_up() {
	[ -f "$SQLSERVER_ENV_FILE" ] || die "$SQLSERVER_ENV_FILE is missing; run make sqlserver-dev-init"
	password=$(env_value MSSQL_SA_PASSWORD)
	credential=$(env_value HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV)
	[ -n "$password" ] && [ -n "$credential" ] || die "$SQLSERVER_ENV_FILE must set MSSQL_SA_PASSWORD and HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV"
	collector_login=${credential%%:*}
	if [ "$collector_login" = "$credential" ] || [ -z "$collector_login" ] ||
		[ "$(printf '%s' "$collector_login" | tr 'A-Z' 'a-z')" = sa ]; then
		die "HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV in $SQLSERVER_ENV_FILE must be <login>:<password> for a dedicated login, not sa; run make sqlserver-dev-init to add one"
	fi
	cmd_cluster
	if [ -z "$(docker ps -aq --filter "name=^${SQLSERVER_CONTAINER}\$")" ]; then
		log "starting $SQLSERVER_CONTAINER"
		set --
		if [ -n "$SQLSERVER_HOST_PORT" ]; then
			set -- -p "127.0.0.1:$SQLSERVER_HOST_PORT:1433"
		fi
		# The password goes in through the environment, not the command line.
		MSSQL_SA_PASSWORD=$password docker run -d --name "$SQLSERVER_CONTAINER" \
			--platform linux/amd64 --network kind "$@" \
			-e ACCEPT_EULA=Y -e MSSQL_PID=Developer -e MSSQL_SA_PASSWORD \
			-v "${SQLSERVER_CONTAINER}-data:/var/opt/mssql" \
			--health-cmd 'SQLCMDPASSWORD="$MSSQL_SA_PASSWORD" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -Q "SELECT 1" >/dev/null 2>&1 || exit 1' \
			--health-interval 10s --health-timeout 5s --health-retries 20 --health-start-period 30s \
			"$SQLSERVER_IMAGE" >/dev/null
	else
		docker start "$SQLSERVER_CONTAINER" >/dev/null
	fi
	log "waiting for $SQLSERVER_CONTAINER to become healthy"
	i=0
	until [ "$(docker inspect -f '{{.State.Health.Status}}' "$SQLSERVER_CONTAINER")" = healthy ]; do
		i=$((i + 1))
		[ "$i" -le 60 ] || die "$SQLSERVER_CONTAINER did not become healthy"
		sleep 5
	done
	log "granting $collector_login only the documented collector permissions"
	MSSQL_SA_PASSWORD=$password HEARTBEAT_COLLECTOR_LOGIN=$collector_login HEARTBEAT_COLLECTOR_PASSWORD=${credential#*:} \
		scripts/sqlserver-login.sh "$SQLSERVER_CONTAINER"
	cmd_secrets
	apply_secret heartbeat-sqlserver-dev-credentials --from-literal=HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV="$credential"
	mkdir -p "$STATE_DIR"
	touch "$STATE_DIR/sqlserver-dev"
	cmd_deploy
	# Credential variables are read at startup.
	kubectl_ -n "$NAMESPACE" rollout restart statefulset/db-collector >/dev/null
	kubectl_ -n "$NAMESPACE" rollout status statefulset/db-collector --timeout 3m
}

cmd_sqlserver_down() {
	docker rm -f "$SQLSERVER_CONTAINER" >/dev/null 2>&1 || true
	docker volume rm "${SQLSERVER_CONTAINER}-data" >/dev/null 2>&1 || true
	log "removed $SQLSERVER_CONTAINER and its volume"
	if [ -f "$STATE_DIR/sqlserver-dev" ]; then
		rm -f "$STATE_DIR/sqlserver-dev"
		if cluster_exists; then
			log "redeploying without the SQL Server target"
			cmd_deploy
		fi
	fi
}

case "${1:-}" in
up) cmd_up ;;
down) cmd_down ;;
cluster) cmd_cluster ;;
images) cmd_images ;;
deploy) cmd_deploy ;;
status) cmd_status ;;
sqlserver-up) cmd_sqlserver_up ;;
sqlserver-down) cmd_sqlserver_down ;;
*) sed -n '2,/^set -eu/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac
