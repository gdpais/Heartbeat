#!/bin/sh
# Creates or updates the least-privilege collector login in a local SQL Server
# container: VIEW SERVER STATE and VIEW ANY DEFINITION, the grants documented
# in docs/guides/database-targets.md, and nothing else. sa is used only here,
# for setup; the collector never logs in as sa. Safe to rerun: an existing
# login gets the new password and the same grants.
#
# Usage: scripts/sqlserver-login.sh <container>
#
# Environment (all required; passed to sqlcmd through the environment, never
# on a command line):
#   MSSQL_SA_PASSWORD             sa password of the container
#   HEARTBEAT_COLLECTOR_LOGIN     collector login name (not sa)
#   HEARTBEAT_COLLECTOR_PASSWORD  collector login password
set -eu

container=${1:?usage: scripts/sqlserver-login.sh <container>}
: "${MSSQL_SA_PASSWORD:?set MSSQL_SA_PASSWORD}"
: "${HEARTBEAT_COLLECTOR_LOGIN:?set HEARTBEAT_COLLECTOR_LOGIN}"
: "${HEARTBEAT_COLLECTOR_PASSWORD:?set HEARTBEAT_COLLECTOR_PASSWORD}"

# sqlcmd substitutes $(NAME) from the environment into the batch as plain
# text, inside N'...' literals, so any quote is doubled first. The quoted
# heredoc keeps the shell from expanding $(NAME) itself.
sql_quote() { printf '%s' "$1" | sed "s/'/''/g"; }
HEARTBEAT_COLLECTOR_LOGIN=$(sql_quote "$HEARTBEAT_COLLECTOR_LOGIN")
HEARTBEAT_COLLECTOR_PASSWORD=$(sql_quote "$HEARTBEAT_COLLECTOR_PASSWORD")
export HEARTBEAT_COLLECTOR_LOGIN HEARTBEAT_COLLECTOR_PASSWORD
SQLCMDPASSWORD=$MSSQL_SA_PASSWORD docker exec -i \
	-e SQLCMDPASSWORD -e HEARTBEAT_COLLECTOR_LOGIN -e HEARTBEAT_COLLECTOR_PASSWORD \
	"$container" /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -C -b -h -1 <<'SQL'
SET NOCOUNT ON;
DECLARE @login sysname = N'$(HEARTBEAT_COLLECTOR_LOGIN)';
DECLARE @password nvarchar(128) = N'$(HEARTBEAT_COLLECTOR_PASSWORD)';
DECLARE @sql nvarchar(max);
IF @login = N'sa' OR @login = N''
	THROW 50000, 'HEARTBEAT_COLLECTOR_LOGIN must name a dedicated login, not sa', 1;
IF SUSER_ID(@login) IS NULL
	SET @sql = N'CREATE LOGIN ' + QUOTENAME(@login) + N' WITH PASSWORD = ' + QUOTENAME(@password, N'''');
ELSE
	SET @sql = N'ALTER LOGIN ' + QUOTENAME(@login) + N' WITH PASSWORD = ' + QUOTENAME(@password, N'''');
EXEC sys.sp_executesql @sql;
SET @sql = N'GRANT VIEW SERVER STATE, VIEW ANY DEFINITION TO ' + QUOTENAME(@login);
EXEC sys.sp_executesql @sql;
IF IS_SRVROLEMEMBER(N'sysadmin', @login) = 1
	THROW 50000, 'the collector login must not be a member of sysadmin', 1;
GO
SQL
