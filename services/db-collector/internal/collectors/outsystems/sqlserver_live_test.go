//go:build sqlserver

package outsystems

import (
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	collectorconfig "heartbeat/internal/config"
	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	"heartbeat/services/db-collector/internal/export/otlp"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogoutsystems "heartbeat/services/db-collector/internal/probes/outsystems"
)

// Credential references resolved through EnvCredentialResolver (see
// scripts/sqlserver-test.sh).
const (
	// liveOSLogCredentialRef is HEARTBEAT_CREDENTIAL_SQLSERVER_TEST_OSLOG, a
	// login with no server permissions.  setupFixtures maps it to a user of
	// the fixture database with SELECT on the fixture tables only.
	liveOSLogCredentialRef = "sqlserver-test-oslog"
	// liveSACredentialRef is HEARTBEAT_CREDENTIAL_SQLSERVER_TEST_SA, used only
	// to create the fixtures and insert rows.
	liveSACredentialRef = "sqlserver-test-sa"
	// fixtureDatabase is created, and dropped first, by setupFixtures.
	fixtureDatabase = "heartbeat_oslog_test"
)

// liveServer is the disposable test server.
type liveServer struct {
	host    string
	port    int
	manager connector.Manager
}

// newLiveServer returns the test server, skipping the test outside make
// test-sqlserver.
func newLiveServer(t *testing.T) liveServer {
	t.Helper()
	addr := os.Getenv("HEARTBEAT_TEST_SQLSERVER_ADDR")
	if addr == "" {
		t.Skip("HEARTBEAT_TEST_SQLSERVER_ADDR is not set; run make test-sqlserver")
	}
	for _, env := range []string{"HEARTBEAT_CREDENTIAL_SQLSERVER_TEST_SA", "HEARTBEAT_CREDENTIAL_SQLSERVER_TEST_OSLOG"} {
		if os.Getenv(env) == "" {
			t.Fatalf("%s is not set; run make test-sqlserver", env)
		}
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("HEARTBEAT_TEST_SQLSERVER_ADDR: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("HEARTBEAT_TEST_SQLSERVER_ADDR port: %v", err)
	}
	manager := connector.NewManager(connector.EnvCredentialResolver{})
	// The disposable test server uses a self-signed certificate.
	manager.TrustServerCertificate = true
	t.Cleanup(func() { _ = manager.Close() })
	return liveServer{host: host, port: port, manager: manager}
}

// target returns the server's database as a target logged in with ref.
func (s liveServer) target(database, ref string) collectormetadata.DatabaseTarget {
	return collectormetadata.DatabaseTarget{
		EnvironmentSlug: "test", Name: "logging-db", Engine: collectorconfig.KindOutSystemsLogs,
		Host: s.host, Port: s.port, DatabaseName: database, CredentialRef: ref,
	}
}

// sa opens database as sa.
func (s liveServer) sa(t *testing.T, database string) *sql.DB {
	t.Helper()
	db, cleanup, err := s.manager.Open(context.Background(), s.target(database, liveSACredentialRef))
	if err != nil {
		t.Fatalf("open %s as sa: %v", database, err)
	}
	t.Cleanup(cleanup)
	return db
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(query), "\n", 2)[0], err)
	}
}

// setupFixtures recreates the fixture database with the tables and views of
// every log type's fixture, without rows, and gives the test login a user in
// it with SELECT on each of those tables and views and nothing else.
func setupFixtures(t *testing.T, server liveServer) {
	t.Helper()
	master := server.sa(t, "master")
	exec(t, master, fmt.Sprintf(`IF DB_ID(N'%[1]s') IS NOT NULL
BEGIN
	ALTER DATABASE [%[1]s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
	DROP DATABASE [%[1]s];
END
CREATE DATABASE [%[1]s];`, fixtureDatabase))
	db := server.sa(t, fixtureDatabase)
	for _, logType := range catalogoutsystems.DefaultCatalog().Names() {
		ddl, err := os.ReadFile(filepath.Join(fixtureDir, logType+".sql"))
		if err != nil {
			t.Fatalf("log type %q has no fixture (see TestEveryLogTypeHasASQLServerFixture): %v", logType, err)
		}
		exec(t, db, string(ddl))
	}
	login, _, _ := strings.Cut(os.Getenv("HEARTBEAT_CREDENTIAL_SQLSERVER_TEST_OSLOG"), ":")
	exec(t, db, `DECLARE @sql nvarchar(max) = N'CREATE USER ' + QUOTENAME(@login) + N' FOR LOGIN ' + QUOTENAME(@login) + N';';
SELECT @sql += N'GRANT SELECT ON ' + QUOTENAME(SCHEMA_NAME(schema_id)) + N'.' + QUOTENAME(name) + N' TO ' + QUOTENAME(@login) + N';'
FROM sys.objects WHERE type IN ('U', 'V');
EXEC sys.sp_executesql @sql;`, sql.Named("login", login))
	// A table the login was not granted, to show the grants are all it has.
	exec(t, db, "CREATE TABLE dbo.not_granted (Id int)")
}

// loadFixtureRows runs the log type's <log type>.rows.sql and returns how
// many rows it added to the log tables: the tables with an Instant column,
// which leaves the detail tables out.
func loadFixtureRows(t *testing.T, db *sql.DB, logType string) int {
	t.Helper()
	rows, err := os.ReadFile(filepath.Join(fixtureDir, logType+".rows.sql"))
	if err != nil {
		t.Fatalf("log type %q has no fixture rows (see TestEveryLogTypeHasASQLServerFixture): %v", logType, err)
	}
	before := countLogRows(t, db)
	exec(t, db, string(rows))
	return countLogRows(t, db) - before
}

// countLogRows counts the rows of every table that has an Instant column.
func countLogRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var count int
	err := db.QueryRowContext(ctx, `DECLARE @sql nvarchar(max) = N'SELECT 0';
SELECT @sql += N' + (SELECT COUNT(*) FROM ' + QUOTENAME(SCHEMA_NAME(t.schema_id)) + N'.' + QUOTENAME(t.name) + N')'
FROM sys.tables AS t
WHERE EXISTS (SELECT 1 FROM sys.columns AS c WHERE c.object_id = t.object_id AND c.name = 'Instant');
EXEC sys.sp_executesql @sql;`).Scan(&count)
	if err != nil {
		t.Fatalf("count log rows: %v", err)
	}
	return count
}

// rotateLogTables does what the platform does every Friday at 23:45: the
// current week's view moves to an empty cycle table and the previous week's
// view to the table the current one used.  With two fixture tables the
// views swap.
func rotateLogTables(t *testing.T, db *sql.DB, view string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tables := map[string]string{}
	for _, name := range []string{view, view + "_Previous"} {
		var table string
		if err := db.QueryRowContext(ctx, `SELECT referenced_entity_name FROM sys.sql_expression_dependencies WHERE referencing_id = OBJECT_ID(@view)`,
			sql.Named("view", "dbo."+name)).Scan(&table); err != nil {
			t.Fatalf("table behind dbo.%s: %v", name, err)
		}
		tables[name] = table
	}
	for name, table := range map[string]string{view: tables[view+"_Previous"], view + "_Previous": tables[view]} {
		exec(t, db, `DECLARE @sql nvarchar(max) = N'ALTER VIEW dbo.' + QUOTENAME(@view) + N' AS SELECT * FROM dbo.' + QUOTENAME(@table);
EXEC sys.sp_executesql @sql;`, sql.Named("view", name), sql.Named("table", table))
	}
}

// Runs every embedded query against its fixture tables and rows, logged in
// with only SELECT on them, and checks the result against the query
// contract.  Over a window that covers every fixture row, the query must
// return each of them: the rows of the previous week's table as well as the
// current week's.
func TestLogQueriesAgainstSQLServer(t *testing.T) {
	server := newLiveServer(t)
	setupFixtures(t, server)
	sa := server.sa(t, fixtureDatabase)
	source := SQLServerSource{Manager: server.manager}
	target := server.target(fixtureDatabase, liveOSLogCredentialRef)
	catalog := catalogoutsystems.DefaultCatalog()
	from := Wall(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	to := Wall(time.Now(), time.UTC).Truncate(time.Second)
	const limit = 10000
	for _, logType := range catalog.Names() {
		t.Run(logType, func(t *testing.T) {
			want := loadFixtureRows(t, sa, logType)
			if want == 0 {
				t.Fatalf("%s.rows.sql inserted no rows into a table with an Instant column", logType)
			}
			query, _ := catalog.Get(logType)
			for _, dateTime := range []bool{false, true} {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				batch, err := source.Query(ctx, target, query.SQL, QueryParams{From: from, To: to, Limit: limit, DateTime: dateTime})
				cancel()
				if err != nil {
					t.Fatalf("query (datetime bounds %v): %v", dateTime, err)
				}
				if _, _, err := checkContract(batch, from, to, limit); err != nil {
					t.Fatal(err)
				}
				if len(batch.Rows) != want {
					t.Fatalf("query returned %d rows, want the %d rows of %s.rows.sql (the current and the previous week's tables)", len(batch.Rows), want, logType)
				}
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := source.Query(ctx, target, "SELECT TOP (@limit) Id FROM dbo.not_granted WHERE @from < @to", QueryParams{From: from, To: to, Limit: 1})
	if err == nil || !strings.Contains(err.Error(), "SELECT permission was denied") {
		t.Fatalf("query of a table that was not granted: %v; want a permission error", err)
	}
}

// Reads the session's lock timeout and deadlock priority from inside a log
// query, as a query_template override would, with its parameters bound: the
// collector's session settings must hold there too.
func TestLogQuerySessionSettingsAgainstSQLServer(t *testing.T) {
	server := newLiveServer(t)
	setupFixtures(t, server)
	source := SQLServerSource{Manager: server.manager}
	const query = `SELECT TOP (@limit) CAST(@to AS datetime2) AS Instant, @@LOCK_TIMEOUT AS LockTimeout,
	(SELECT deadlock_priority FROM sys.dm_exec_sessions WHERE session_id = @@SPID) AS DeadlockPriority`
	to := Wall(time.Now(), time.UTC).Truncate(time.Second)
	for run := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		batch, err := source.Query(ctx, server.target(fixtureDatabase, liveOSLogCredentialRef), query, QueryParams{From: to.Add(-time.Minute), To: to, Limit: 1})
		cancel()
		if err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
		row := batch.Rows[0]
		if row[1] != connector.LockTimeout.Milliseconds() || row[2] != int64(-5) {
			t.Fatalf("run %d: lock timeout %v, deadlock priority %v; want %d and -5 (LOW)", run+1, row[1], row[2], connector.LockTimeout.Milliseconds())
		}
	}
}

// otlpReceiver is a fake OTel Collector that records the Request_Key of
// every record it receives.
type otlpReceiver struct {
	mu   sync.Mutex
	keys []string
}

func (r *otlpReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	zr, err := gzip.NewReader(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	raw, _ := io.ReadAll(zr)
	var data logspb.LogsData
	if err := proto.Unmarshal(raw, &data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rl := range data.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, record := range sl.LogRecords {
				key, _ := attributes(record.Attributes)["Request_Key"].(string)
				r.keys = append(r.keys, key)
			}
		}
	}
}

func (r *otlpReceiver) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.keys)
}

// Extracts dbo.oslog_Error through the production path (connector, the
// embedded error.sql, OTLP/HTTP, watermark file) over several cycles and a
// restart, with ties on Instant at batch boundaries, datetime's 1/300 s
// ticks, and a row committed after its Instant.  Every row must arrive
// exactly once.
func TestExtractionAgainstSQLServer(t *testing.T) {
	server := newLiveServer(t)
	setupFixtures(t, server)
	sa := server.sa(t, fixtureDatabase)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// insertInto writes a row through a view (dbo.oslog_Error is the current
	// week's cycle table) or into a cycle table by name.
	insertInto := func(object, key string, offset time.Duration) {
		t.Helper()
		exec(t, sa, `DECLARE @sql nvarchar(max) = N'INSERT INTO dbo.' + QUOTENAME(@object) + N' (Instant, Message, Module_Name, Request_Key, User_Id, Stack)
VALUES (@instant, @message, N''ShopCore'', @key, 7, N''at Shop.Checkout()'')';
EXEC sys.sp_executesql @sql, N'@instant datetime, @message nvarchar(2000), @key char(36)', @instant, @message, @key;`,
			sql.Named("object", object), sql.Named("instant", mssql.DateTime1(base.Add(offset))), sql.Named("message", "Order failed: "+key), sql.Named("key", key))
	}
	insert := func(key string, offset time.Duration) {
		t.Helper()
		insertInto("oslog_Error", key, offset)
	}
	ms := time.Millisecond
	insert("a", time.Minute)
	insert("b", 2*time.Minute+3*ms) // b and c share a datetime tick
	insert("c", 2*time.Minute+3*ms)
	insert("d", 2*time.Minute+7*ms) // the next tick (.00667 s)
	insert("e", 3*time.Minute)
	insert("f", 4*time.Minute) // f and g share an Instant
	insert("g", 4*time.Minute)
	insert("h", 8*time.Minute+59*time.Second+997*ms)

	receiver := &otlpReceiver{}
	collector := httptest.NewServer(receiver)
	defer collector.Close()
	dir := t.TempDir()
	newExecutor := func(now *time.Time) *Executor {
		manager := connector.NewManager(connector.EnvCredentialResolver{})
		manager.TrustServerCertificate = true
		t.Cleanup(func() { _ = manager.Close() })
		executor := NewExecutor(SQLServerSource{Manager: manager}, NewStore(dir), otlp.NewClient(), nil, slog.New(slog.DiscardHandler))
		executor.now = func() time.Time { return *now }
		return executor
	}
	item := collectormetadata.ScheduledProbe{
		CollectorID: "oslogs",
		Target:      server.target(fixtureDatabase, liveOSLogCredentialRef),
		Definition:  collectormetadata.ProbeDefinition{Name: "error"},
		OutSystemsLogs: collectorconfig.OutSystemsLogsConfig{
			Lag: time.Minute, BatchLimit: 3, InitialLookback: 15 * time.Minute, LogTimezone: "UTC", LogsEndpoint: collector.URL,
		},
	}
	run := func(executor *Executor) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, _, err := executor.RunProbe(ctx, item); err != nil {
			t.Fatalf("RunProbe: %v", err)
		}
	}

	now := base.Add(10 * time.Minute)
	executor := newExecutor(&now)
	// Reads (11:55, 12:09] in batches of 3: a | b c | d e | f g | h.
	run(executor)
	if got := receiver.received(); !slices.Equal(got, []string{"a", "b", "c", "d", "e", "f", "g", "h"}) {
		t.Fatalf("first cycle delivered %v", got)
	}
	watermark, _, _ := executor.Store.Load(WatermarkKey{Collector: "oslogs", Target: "logging-db", LogType: "error"})
	if watermark.InstantType != "DATETIME" || !watermark.Instant.Equal(base.Add(9*time.Minute)) {
		t.Fatalf("watermark %+v", watermark)
	}

	// Committed now, 30 s after its Instant: inside the lag, so not lost.
	insert("late", 9*time.Minute+30*time.Second)
	now = now.Add(time.Minute)
	run(executor)

	// A restart resumes from the watermark file.
	insert("after-restart", 10*time.Minute+30*time.Second)
	now = now.Add(time.Minute)
	restarted := newExecutor(&now)
	run(restarted)
	now = now.Add(time.Minute)
	run(restarted)

	want := []string{"a", "b", "c", "d", "e", "f", "g", "h", "late", "after-restart"}
	if got := receiver.received(); !slices.Equal(got, want) {
		t.Fatalf("delivered %v, want each of %v once", got, want)
	}

	// Friday 23:45: the platform rotates the log tables.  dbo.oslog_Error now
	// reads an empty cycle table and last week's table is behind
	// dbo.oslog_Error_Previous.  A row of the old week committed after the
	// rotation, within the lag, is only in the previous table; the query
	// reads both, so it arrives with the first row of the new week.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	var oldTable string
	if err := sa.QueryRowContext(ctx, `SELECT referenced_entity_name FROM sys.sql_expression_dependencies WHERE referencing_id = OBJECT_ID('dbo.oslog_Error')`).Scan(&oldTable); err != nil {
		t.Fatalf("table behind dbo.oslog_Error: %v", err)
	}
	cancel()
	rotateLogTables(t, sa, "oslog_Error")
	insertInto(oldTable, "before-rotation", 12*time.Minute+30*time.Second)
	insert("after-rotation", 13*time.Minute)
	now = now.Add(2 * time.Minute)
	run(restarted)
	want = append(want, "before-rotation", "after-rotation")
	if got := receiver.received(); !slices.Equal(got, want) {
		t.Fatalf("after the rotation delivered %v, want %v", got, want)
	}
}
