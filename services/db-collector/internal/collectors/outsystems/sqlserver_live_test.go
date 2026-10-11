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

// setupFixtures recreates the fixture database with the tables of every
// log type's fixture, and gives the test login a user in it with SELECT on
// each of those tables and nothing else.
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
FROM sys.tables;
EXEC sys.sp_executesql @sql;`, sql.Named("login", login))
	// A table the login was not granted, to show the grants are all it has.
	exec(t, db, "CREATE TABLE dbo.not_granted (Id int)")
}

// Runs every embedded query against its fixture tables, logged in with only
// SELECT on them, and checks the result against the query contract.
func TestLogQueriesAgainstSQLServer(t *testing.T) {
	server := newLiveServer(t)
	setupFixtures(t, server)
	source := SQLServerSource{Manager: server.manager}
	target := server.target(fixtureDatabase, liveOSLogCredentialRef)
	catalog := catalogoutsystems.DefaultCatalog()
	from := Wall(time.Now().Add(-24*time.Hour), time.UTC).Truncate(time.Second)
	to := Wall(time.Now(), time.UTC).Truncate(time.Second)
	for _, logType := range catalog.Names() {
		t.Run(logType, func(t *testing.T) {
			query, _ := catalog.Get(logType)
			for _, dateTime := range []bool{false, true} {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				batch, err := source.Query(ctx, target, query.SQL, QueryParams{From: from, To: to, Limit: 10, DateTime: dateTime})
				cancel()
				if err != nil {
					t.Fatalf("query (datetime bounds %v): %v", dateTime, err)
				}
				if _, _, err := checkContract(batch, from, to, 10); err != nil {
					t.Fatal(err)
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
	insert := func(key string, offset time.Duration) {
		t.Helper()
		exec(t, sa, `INSERT INTO dbo.oslog_Error (Instant, Message, Module_Name, Request_Key, User_Id, Stack)
VALUES (@instant, @message, N'ShopCore', @key, 7, N'at Shop.Checkout()')`,
			sql.Named("instant", mssql.DateTime1(base.Add(offset))), sql.Named("message", "Order failed: "+key), sql.Named("key", key))
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
}
