#!/bin/sh
# Runs PatientZero (ADR 0010) on the kind cluster next to the Heartbeat chart,
# against the sandbox SQL Server. The Makefile calls this: make patientzero-up
# and make patientzero-down. Run make sqlserver-dev-up first.
#
# up:
#   1. Adds PATIENTZERO_SQL_CREDENTIAL=patientzero:<random password> to the
#      SQL Server env file if it has none.
#   2. As sa, inside the SQL Server container: creates the patientzero and
#      patientzero_log databases and PatientZero's login, db_owner of those
#      two databases and nothing else, and adds the Heartbeat collector's
#      login to a role in patientzero_log that PatientZero grants SELECT on
#      the log views, nothing more.
#   3. Builds the image, loads it into kind and installs the chart into the
#      patientzero namespace. PatientZero creates its schemas at startup.
#   4. Redeploys the Heartbeat chart with an outsystems_logs collector that
#      reads patientzero_log. A later make kind-deploy drops that collector;
#      run make patientzero-up again to restore it.
# down: uninstalls the chart, redeploys Heartbeat without the collector, and
# drops the two databases and the login.
# load: runs the k6 load (tools/patientzero/load) as a Job next to
# PatientZero, follows its output and exits with k6's result. Interrupting it
# stops following, not the load.
# load-stop: deletes the load Job.
#
# Every kubectl and helm call names the kind context explicitly.
#
# Environment (defaults in brackets), shared with scripts/kind.sh:
#   KIND_CLUSTER           cluster name [heartbeat]
#   HEARTBEAT_NAMESPACE    Heartbeat's namespace [heartbeat]
#   PATIENTZERO_NAMESPACE  PatientZero's namespace [patientzero]
#   SQLSERVER_CONTAINER    sandbox SQL Server container [heartbeat-sqlserver-dev]
#   SQLSERVER_ENV_FILE     SA password and credentials [.env.sqlserver-dev]
#   PROFILE, EXTRA_VALUES  passed on to scripts/kind.sh deploy
#   LOAD                   load profile: smoke, steady, peak or soak [steady]
#   LOAD_DURATION          how long the profile holds its load [the profile's]
#   LOAD_SEED              seed of the virtual users' choices [1]
set -eu
cd "$(dirname "$0")/../../.."

CLUSTER=${KIND_CLUSTER:-heartbeat}
CONTEXT=kind-$CLUSTER
NAMESPACE=${PATIENTZERO_NAMESPACE:-patientzero}
SQLSERVER_CONTAINER=${SQLSERVER_CONTAINER:-heartbeat-sqlserver-dev}
SQLSERVER_ENV_FILE=${SQLSERVER_ENV_FILE:-.env.sqlserver-dev}
STATE_DIR=.tmp/kind/$CLUSTER
CHART=tools/patientzero/chart
RELEASE=patientzero
CREDENTIAL_KEY=PATIENTZERO_SQL_CREDENTIAL
COLLECTOR_KEY=HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV
READER_ROLE=heartbeat_oslog_reader
DATABASE=patientzero
LOG_DATABASE=patientzero_log
HEARTBEAT_VALUES=$STATE_DIR/patientzero-heartbeat.yaml
LOAD_DIR=tools/patientzero/load
LOAD_JOB=patientzero-load
# Renovate keeps this pin current (renovate.json).
K6_IMAGE=grafana/k6:1.7.1@sha256:4fd3a694926b064d3491d9b02b01cde886583c4931f1223816e3d9a7bdfa7e0f

kubectl_() { kubectl --context "$CONTEXT" "$@"; }
log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

cluster_exists() { kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; }
sqlserver_healthy() { [ "$(docker inspect -f '{{.State.Health.Status}}' "$SQLSERVER_CONTAINER" 2>/dev/null)" = healthy ]; }
env_value() { grep "^$1=" "$SQLSERVER_ENV_FILE" | head -n 1 | cut -d= -f2-; }

# Adds PatientZero's credential to the env file once, in the plain KEY=value
# form scripts/kind.sh reads. "Dev-" plus hex digits meets SQL Server's
# password complexity.
ensure_credential() {
	[ -f "$SQLSERVER_ENV_FILE" ] || die "$SQLSERVER_ENV_FILE is missing; run make sqlserver-dev-init and make sqlserver-dev-up"
	if [ -z "$(env_value "$CREDENTIAL_KEY")" ]; then
		(umask 077 && printf '%s=patientzero:Dev-%s\n' "$CREDENTIAL_KEY" "$(openssl rand -hex 16)" >>"$SQLSERVER_ENV_FILE")
		log "added PatientZero's login to $SQLSERVER_ENV_FILE"
	fi
	credential=$(env_value "$CREDENTIAL_KEY")
	pz_login=${credential%%:*}
	pz_password=${credential#*:}
	if [ "$pz_login" = "$credential" ] || [ -z "$pz_login" ] || [ -z "$pz_password" ] ||
		[ "$(printf '%s' "$pz_login" | tr 'A-Z' 'a-z')" = sa ]; then
		die "$CREDENTIAL_KEY in $SQLSERVER_ENV_FILE must be <login>:<password> for PatientZero's own login, not sa"
	fi
	collector=$(env_value "$COLLECTOR_KEY")
	collector_login=${collector%%:*}
	[ -n "$collector_login" ] && [ "$collector_login" != "$collector" ] ||
		die "$COLLECTOR_KEY in $SQLSERVER_ENV_FILE must be <login>:<password>; run make sqlserver-dev-init"
	[ "$(printf '%s' "$collector_login" | tr 'A-Z' 'a-z')" != "$(printf '%s' "$pz_login" | tr 'A-Z' 'a-z')" ] ||
		die "PatientZero and the Heartbeat collector must use different logins"
}

# Runs a batch as sa in the container. sqlcmd substitutes $(NAME) from the
# environment as plain text inside N'...' literals, so quotes are doubled.
sql_quote() { printf '%s' "$1" | sed "s/'/''/g"; }
sa_sql() {
	SQLCMDPASSWORD=$(env_value MSSQL_SA_PASSWORD) \
		PZ_LOGIN=$(sql_quote "$pz_login") PZ_PASSWORD=$(sql_quote "${pz_password:-}") \
		PZ_COLLECTOR_LOGIN=$(sql_quote "${collector_login:-}") PZ_READER_ROLE=$READER_ROLE \
		PZ_DATABASE=$DATABASE PZ_LOG_DATABASE=$LOG_DATABASE \
		docker exec -i -e SQLCMDPASSWORD -e PZ_LOGIN -e PZ_PASSWORD -e PZ_COLLECTOR_LOGIN \
		-e PZ_READER_ROLE -e PZ_DATABASE -e PZ_LOG_DATABASE \
		"$SQLSERVER_CONTAINER" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -b -h -1
}

grant_logins() {
	log "creating $DATABASE, $LOG_DATABASE and the $pz_login login (db_owner of those two only)"
	sa_sql <<'SQL'
SET NOCOUNT ON;
SET XACT_ABORT ON;
DECLARE @login nvarchar(max) = N'$(PZ_LOGIN)';
DECLARE @password nvarchar(max) = N'$(PZ_PASSWORD)';
DECLARE @collector nvarchar(max) = N'$(PZ_COLLECTOR_LOGIN)';
DECLARE @role sysname = N'$(PZ_READER_ROLE)';
DECLARE @sql nvarchar(max), @db sysname;
-- EXEC takes no expressions as parameter values, so the quoted names are
-- computed first.
DECLARE @qlogin nvarchar(300) = QUOTENAME(@login), @qcollector nvarchar(300) = QUOTENAME(@collector),
	@qrole nvarchar(300) = QUOTENAME(@role);

-- Checks, before any change: PatientZero's login holds nothing on the
-- server beyond CONNECT SQL, so its rights are the two databases' only.
IF LEN(@login) = 0 OR LEN(@login) > 128 OR LEN(@password) = 0 OR LEN(@password) > 128
	THROW 50000, 'the PatientZero login and password must be 1 to 128 characters', 1;
IF SUSER_ID(@collector) IS NULL
	THROW 50000, 'the Heartbeat collector login does not exist; run make sqlserver-dev-up first', 1;
IF EXISTS (SELECT 1 FROM sys.server_role_members AS m
	JOIN sys.server_principals AS p ON p.principal_id = m.member_principal_id WHERE p.name = @login)
	THROW 50000, 'the existing PatientZero login is a member of a server role; drop it or use another name', 1;
IF EXISTS (SELECT 1 FROM sys.server_permissions AS sp
	JOIN sys.server_principals AS p ON p.principal_id = sp.grantee_principal_id
	WHERE p.name = @login AND (sp.state <> 'G' OR sp.permission_name <> N'CONNECT SQL'))
	THROW 50000, 'the existing PatientZero login holds server permissions beyond CONNECT SQL; drop it or use another name', 1;

IF SUSER_ID(@login) IS NULL
	SET @sql = N'CREATE LOGIN ' + QUOTENAME(@login) + N' WITH PASSWORD = ' + QUOTENAME(@password, N'''');
ELSE
	SET @sql = N'ALTER LOGIN ' + QUOTENAME(@login) + N' WITH PASSWORD = ' + QUOTENAME(@password, N'''');
EXEC sys.sp_executesql @sql;

DECLARE dbs CURSOR LOCAL FAST_FORWARD FOR SELECT N'$(PZ_DATABASE)' UNION ALL SELECT N'$(PZ_LOG_DATABASE)';
OPEN dbs;
FETCH NEXT FROM dbs INTO @db;
WHILE @@FETCH_STATUS = 0
BEGIN
	IF DB_ID(@db) IS NULL
	BEGIN
		SET @sql = N'CREATE DATABASE ' + QUOTENAME(@db);
		EXEC sys.sp_executesql @sql;
	END
	SET @sql = N'USE ' + QUOTENAME(@db) + N';
IF DATABASE_PRINCIPAL_ID(@login) IS NULL
	EXEC (N''CREATE USER '' + @qlogin + N'' FOR LOGIN '' + @qlogin);
EXEC (N''ALTER ROLE db_owner ADD MEMBER '' + @qlogin);';
	EXEC sys.sp_executesql @sql, N'@login sysname, @qlogin nvarchar(300)', @login = @login, @qlogin = @qlogin;
	FETCH NEXT FROM dbs INTO @db;
END
CLOSE dbs;

-- The collector reads the log views exactly as it reads a real logging
-- database: as its own user, through the role PatientZero grants SELECT on
-- the views to.
SET @sql = N'USE ' + QUOTENAME(N'$(PZ_LOG_DATABASE)') + N';
IF DATABASE_PRINCIPAL_ID(@collector) IS NULL
	EXEC (N''CREATE USER '' + @qcollector + N'' FOR LOGIN '' + @qcollector);
IF DATABASE_PRINCIPAL_ID(@role) IS NULL
	EXEC (N''CREATE ROLE '' + @qrole);
IF IS_ROLEMEMBER(@role, @collector) = 0
	EXEC (N''ALTER ROLE '' + @qrole + N'' ADD MEMBER '' + @qcollector);';
EXEC sys.sp_executesql @sql, N'@collector sysname, @role sysname, @qcollector nvarchar(300), @qrole nvarchar(300)',
	@collector = @collector, @role = @role, @qcollector = @qcollector, @qrole = @qrole;
GO
SQL
}

drop_logins() {
	log "dropping $DATABASE, $LOG_DATABASE and the $pz_login login"
	sa_sql <<'SQL'
SET NOCOUNT ON;
DECLARE @login sysname = N'$(PZ_LOGIN)', @sql nvarchar(max), @db sysname;
DECLARE dbs CURSOR LOCAL FAST_FORWARD FOR SELECT N'$(PZ_DATABASE)' UNION ALL SELECT N'$(PZ_LOG_DATABASE)';
OPEN dbs;
FETCH NEXT FROM dbs INTO @db;
WHILE @@FETCH_STATUS = 0
BEGIN
	IF DB_ID(@db) IS NOT NULL
	BEGIN
		SET @sql = N'ALTER DATABASE ' + QUOTENAME(@db) + N' SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ' + QUOTENAME(@db);
		EXEC sys.sp_executesql @sql;
	END
	FETCH NEXT FROM dbs INTO @db;
END
CLOSE dbs;
IF SUSER_ID(@login) IS NOT NULL
BEGIN
	SET @sql = N'DROP LOGIN ' + QUOTENAME(@login);
	EXEC sys.sp_executesql @sql;
END
GO
SQL
}

node_arch() {
	case "$(docker exec "$CLUSTER-control-plane" uname -m)" in
	x86_64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) die "unsupported node architecture" ;;
	esac
}

# Tags the image by its content, as scripts/kind.sh does, so a stale image
# cannot hide behind a reused tag.
build_image() {
	arch=$(node_arch)
	log "building heartbeat/patientzero for linux/$arch"
	docker build --quiet --platform "linux/$arch" --provenance=false \
		-t heartbeat/patientzero:build -f tools/patientzero/Dockerfile . >/dev/null
	id=$(docker image inspect -f '{{.Id}}' heartbeat/patientzero:build)
	tag=dev-$(printf '%s' "${id#sha256:}" | cut -c1-12)
	docker tag heartbeat/patientzero:build "heartbeat/patientzero:$tag"
	log "loading heartbeat/patientzero:$tag into $CLUSTER"
	kind load docker-image --name "$CLUSTER" "heartbeat/patientzero:$tag" >/dev/null
}

install_chart() {
	kubectl_ create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
	# The credential reaches kubectl in a 0600 file, never on its command line.
	secret_env=$(mktemp "${TMPDIR:-/tmp}/patientzero-credential.XXXXXX")
	trap 'rm -f "$secret_env"' EXIT
	trap 'exit 130' INT TERM
	printf '%s=%s\n' "$CREDENTIAL_KEY" "$credential" >"$secret_env"
	kubectl_ -n "$NAMESPACE" create secret generic patientzero-sql --from-env-file="$secret_env" \
		--dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
	rm -f "$secret_env"
	log "installing release $RELEASE to $CONTEXT/$NAMESPACE"
	helm upgrade --install "$RELEASE" "$CHART" \
		--kube-context "$CONTEXT" --namespace "$NAMESPACE" \
		--set "image.tag=$tag" \
		--set "sql.host=$SQLSERVER_CONTAINER" \
		--set sql.trustServerCertificate=true \
		--set "sql.database=$DATABASE" --set "sql.logDatabase=$LOG_DATABASE" \
		--set "logReaderRole=$READER_ROLE" \
		--wait --timeout 5m
}

# The Heartbeat values that add the PatientZero collector. A values file
# replaces a list instead of merging it, so this copies the sandbox's values
# file, whose last entry is integrations.collectors, and appends to that list.
heartbeat_values() {
	src=infra/helm/values/sqlserver-dev.yaml
	awk '/^  collectors:$/ { found = 1; next }
		found && NF && $0 !~ /^    / && $0 !~ /^[[:space:]]*#/ { bad = 1 }
		END { exit !(found && !bad) }' "$src" ||
		die "$src no longer ends with integrations.collectors; update heartbeat_values in $0"
	mkdir -p "$STATE_DIR"
	{
		cat "$src"
		cat <<EOF
    # PatientZero's logging database (tools/patientzero/scripts/patientzero.sh).
    - id: patientzero
      kind: outsystems_logs
      enabled: true
      config:
        environment: local-dev
        scrape_interval: 15s
        lag: 15s
        probes:
          # Instant is the start of the screen request or timer.
          - name: screen
            lag: 1m
          - name: cyclic_job
            lag: 1m
          - name: mobile_request
          - name: integration
          - name: general
          - name: error
          - name: extension
          - name: request_event
        targets:
          - name: patientzero-log
            host: $SQLSERVER_CONTAINER
            port: 1433
            database_name: $LOG_DATABASE
            credential_ref: env/sqlserver-dev
EOF
	} >"$HEARTBEAT_VALUES"
}

cmd_up() {
	cluster_exists || die "kind cluster $CLUSTER does not exist; run make sqlserver-dev-up"
	sqlserver_healthy || die "$SQLSERVER_CONTAINER is not running and healthy; run make sqlserver-dev-up"
	[ -f "$STATE_DIR/sqlserver-dev" ] || die "Heartbeat on $CLUSTER does not target $SQLSERVER_CONTAINER; run make sqlserver-dev-up"
	ensure_credential
	grant_logins
	build_image
	install_chart
	heartbeat_values
	log "redeploying Heartbeat with the patientzero outsystems_logs collector"
	EXTRA_VALUES="$HEARTBEAT_VALUES ${EXTRA_VALUES:-}" scripts/kind.sh deploy >/dev/null
	kubectl_ -n "$NAMESPACE" get pods,svc
	log "PatientZero is up; try: kubectl --context $CONTEXT -n $NAMESPACE port-forward svc/patientzero 8080"
}

cmd_down() {
	if cluster_exists; then
		if helm status "$RELEASE" --kube-context "$CONTEXT" --namespace "$NAMESPACE" >/dev/null 2>&1; then
			helm uninstall "$RELEASE" --kube-context "$CONTEXT" --namespace "$NAMESPACE" --wait >/dev/null
		fi
		kubectl_ delete namespace "$NAMESPACE" --ignore-not-found >/dev/null
		log "removed $RELEASE and the $NAMESPACE namespace"
		if [ -f "$HEARTBEAT_VALUES" ]; then
			rm -f "$HEARTBEAT_VALUES"
			log "redeploying Heartbeat without the patientzero collector"
			scripts/kind.sh deploy >/dev/null
		fi
	fi
	rm -f "$HEARTBEAT_VALUES"
	if sqlserver_healthy && [ -f "$SQLSERVER_ENV_FILE" ]; then
		credential=$(env_value "$CREDENTIAL_KEY")
		pz_login=${credential%%:*}
		if [ -n "$pz_login" ] && [ "$(printf '%s' "$pz_login" | tr 'A-Z' 'a-z')" != sa ]; then
			drop_logins
		fi
	fi
}

# The load runs inside the cluster: a port-forward would be the bottleneck,
# and the requests reach PatientZero the way kind-e2e's will.
cmd_load() {
	profile=${LOAD:-steady}
	case $profile in
	smoke | steady | peak | soak) ;;
	*) die "LOAD must be smoke, steady, peak or soak, not $profile" ;;
	esac
	cluster_exists || die "kind cluster $CLUSTER does not exist; run make sqlserver-dev-up and make patientzero-up"
	helm status "$RELEASE" --kube-context "$CONTEXT" --namespace "$NAMESPACE" >/dev/null 2>&1 ||
		die "PatientZero is not installed on $CLUSTER; run make patientzero-up"
	run_id=$profile-$(date -u +%Y%m%dT%H%M%SZ)
	kubectl_ -n "$NAMESPACE" create configmap "$LOAD_JOB" --from-file="$LOAD_DIR" \
		--dry-run=client -o yaml | kubectl_ apply -f - >/dev/null
	kubectl_ -n "$NAMESPACE" delete job "$LOAD_JOB" --ignore-not-found --wait >/dev/null
	log "starting the $profile load, run $run_id"
	kubectl_ apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: $LOAD_JOB
  namespace: $NAMESPACE
  labels:
    app.kubernetes.io/name: $LOAD_JOB
    app.kubernetes.io/part-of: patientzero
spec:
  backoffLimit: 0
  # Kept a day for its logs; the next run replaces it.
  ttlSecondsAfterFinished: 86400
  template:
    metadata:
      labels:
        app.kubernetes.io/name: $LOAD_JOB
        app.kubernetes.io/part-of: patientzero
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      enableServiceLinks: false
      securityContext:
        runAsNonRoot: true
        # The k6 image's own user.
        runAsUser: 12345
        runAsGroup: 12345
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: k6
          image: $K6_IMAGE
          args: [run, --no-color, --no-usage-report, /load/patientzero.js]
          env:
            - name: BASE_URL
              value: http://patientzero.$NAMESPACE.svc:8080
            - name: PROFILE
              value: "$profile"
            - name: DURATION
              value: "${LOAD_DURATION:-}"
            - name: RUN_ID
              value: "$run_id"
            - name: SEED
              value: "${LOAD_SEED:-1}"
          volumeMounts:
            - name: load
              mountPath: /load
              readOnly: true
            - name: tmp
              mountPath: /tmp
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              memory: 512Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: [ALL]
      volumes:
        - name: load
          configMap:
            name: $LOAD_JOB
        - name: tmp
          emptyDir: {}
EOF
	# Waits for the pod to leave Pending (the image pull) before following.
	i=0
	until kubectl_ -n "$NAMESPACE" get pods -l "job-name=$LOAD_JOB" -o jsonpath='{.items[*].status.phase}' 2>/dev/null |
		grep -qE 'Running|Succeeded|Failed'; do
		i=$((i + 1))
		[ "$i" -le 150 ] || die "the load pod did not start; kubectl --context $CONTEXT -n $NAMESPACE describe job $LOAD_JOB"
		sleep 2
	done
	kubectl_ -n "$NAMESPACE" logs -f "job/$LOAD_JOB" || true
	i=0
	while :; do
		status=$(kubectl_ -n "$NAMESPACE" get job "$LOAD_JOB" -o jsonpath='{.status.succeeded}/{.status.failed}')
		case $status in
		1/*) log "load run $run_id finished and k6's thresholds held"; return 0 ;;
		*/1) die "load run $run_id failed: a k6 threshold was crossed or k6 could not run; see its output above" ;;
		esac
		i=$((i + 1))
		[ "$i" -le 30 ] || die "load run $run_id has not finished; make patientzero-load-stop stops it"
		sleep 2
	done
}

cmd_load_stop() {
	cluster_exists || return 0
	kubectl_ -n "$NAMESPACE" delete job "$LOAD_JOB" --ignore-not-found --wait >/dev/null
	log "stopped the load"
}

case "${1:-}" in
up) cmd_up ;;
down) cmd_down ;;
load) cmd_load ;;
load-stop) cmd_load_stop ;;
*) sed -n '2,/^set -eu/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//' >&2; exit 2 ;;
esac
