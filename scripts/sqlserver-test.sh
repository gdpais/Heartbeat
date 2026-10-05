#!/bin/sh
# Runs every built-in SQL Server probe against a disposable SQL Server
# container (make test-sqlserver). The container has a random SA password, no
# volume and a random loopback port, and is removed on exit. It never touches
# the sqlserver-dev sandbox.
#
# The probes run as a least-privilege login with only the documented grants
# (scripts/sqlserver-login.sh), so a probe that needs more fails here. sa is
# used for setup. sa and a CONTROL SERVER login, under their own credential
# references, are used only by the test of the sysadmin-equivalence check.
#
# Environment:
#   SQLSERVER_IMAGE  SQL Server image [mcr.microsoft.com/mssql/server:2022-latest]
set -eu

SQLSERVER_IMAGE=${SQLSERVER_IMAGE:-mcr.microsoft.com/mssql/server:2022-latest}
CONTAINER=heartbeat-sqlserver-test-$$

log() { printf '%s\n' "$*" >&2; }
die() { log "error: $*"; exit 1; }

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

password="Test-$(openssl rand -hex 16)"
collector_login=heartbeat_collector
collector_password="Test-$(openssl rand -hex 16)"
control_password="Test-$(openssl rand -hex 16)"
log "starting $CONTAINER ($SQLSERVER_IMAGE)"
# The password goes in through the environment, not the command line.
MSSQL_SA_PASSWORD=$password docker run -d --name "$CONTAINER" \
	--platform linux/amd64 -p 127.0.0.1::1433 \
	-e ACCEPT_EULA=Y -e MSSQL_PID=Developer -e MSSQL_SA_PASSWORD \
	--health-cmd 'SQLCMDPASSWORD="$MSSQL_SA_PASSWORD" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -Q "SELECT 1" >/dev/null 2>&1 || exit 1' \
	--health-interval 5s --health-timeout 5s --health-retries 40 --health-start-period 20s \
	"$SQLSERVER_IMAGE" >/dev/null

log "waiting for $CONTAINER to become healthy"
i=0
until [ "$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER")" = healthy ]; do
	i=$((i + 1))
	[ "$i" -le 60 ] || die "$CONTAINER did not become healthy"
	sleep 5
done

log "creating the least-privilege login $collector_login"
MSSQL_SA_PASSWORD=$password HEARTBEAT_COLLECTOR_LOGIN=$collector_login HEARTBEAT_COLLECTOR_PASSWORD=$collector_password \
	scripts/sqlserver-login.sh "$CONTAINER"
log "creating heartbeat_control, a CONTROL SERVER login for the sysadmin check"
# sqlcmd substitutes $(CONTROL_PASSWORD) from the environment; the password
# is never on a command line.
SQLCMDPASSWORD=$password CONTROL_PASSWORD=$control_password docker exec -i \
	-e SQLCMDPASSWORD -e CONTROL_PASSWORD "$CONTAINER" \
	/opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -b >/dev/null <<'SQL'
CREATE LOGIN heartbeat_control WITH PASSWORD = N'$(CONTROL_PASSWORD)';
GRANT CONTROL SERVER TO heartbeat_control;
GO
SQL

port=$(docker port "$CONTAINER" 1433/tcp | head -n 1 | sed 's/.*://')
[ -n "$port" ] || die "could not read the published port of $CONTAINER"

HEARTBEAT_TEST_SQLSERVER_ADDR="127.0.0.1:$port" \
	HEARTBEAT_CREDENTIAL_SQLSERVER_TEST="$collector_login:$collector_password" \
	HEARTBEAT_CREDENTIAL_SQLSERVER_TEST_SA="sa:$password" \
	HEARTBEAT_CREDENTIAL_SQLSERVER_TEST_CONTROL="heartbeat_control:$control_password" \
	GOCACHE="${GOCACHE:-$(pwd)/.tmp/gocache}" \
	go test -tags=sqlserver -count=1 -timeout=5m -v \
	-run 'AgainstSQLServer$' ./services/db-collector/internal/collectors
