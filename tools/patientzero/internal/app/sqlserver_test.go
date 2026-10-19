package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"heartbeat/tools/patientzero/internal/logschema"
	"heartbeat/tools/patientzero/internal/oslog"
	"heartbeat/tools/patientzero/internal/shop"
)

// queriesDir holds the queries the db-collector's outsystems_logs collector
// runs; the live test runs them unchanged against what PatientZero wrote.
const queriesDir = "../../../../services/db-collector/internal/probes/outsystems/queries"

// TestLiveSQLServer runs the entry points against a real SQL Server and reads
// their rows back with the collector's own queries. It needs
// PATIENTZERO_TEST_SQLSERVER, a sqlserver:// URL for a login that may create
// databases (the sandbox's sa), and is skipped without it. It creates two
// databases with random names and drops them at the end.
func TestLiveSQLServer(t *testing.T) {
	admin := os.Getenv("PATIENTZERO_TEST_SQLSERVER")
	if admin == "" {
		t.Skip("PATIENTZERO_TEST_SQLSERVER is not set")
	}
	ctx := context.Background()
	business := openTestDatabase(t, admin, "pz_test")
	logging := openTestDatabase(t, admin, "pz_test_log")
	types, err := logschema.Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // safe to run again
		if err := Migrate(ctx, business, logging, types, "heartbeat_oslog_reader"); err != nil {
			t.Fatal(err)
		}
	}
	var tables, views int
	if err := logging.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sys.tables WHERE name LIKE 'oslog[_]%'),
		(SELECT COUNT(*) FROM sys.views WHERE name LIKE 'oslog[_]%')`).Scan(&tables, &views); err != nil {
		t.Fatal(err)
	}
	if tables != len(types)*logschema.Cycles || views != len(types)*2 {
		t.Fatalf("%d cycle tables and %d views, want %d and %d", tables, views, len(types)*logschema.Cycles, len(types)*2)
	}

	schema := oslog.NewSchema(types)
	s := &Server{
		Store:    &shop.SQLStore{DB: business},
		Logs:     &oslog.SQLWriter{DB: logging, Schema: schema},
		Schema:   schema,
		FrontEnd: "FE-PZ-1",
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	from := time.Now().UTC().Add(-time.Minute)
	for _, c := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, PathPlaceOrder, `{"productId":2,"quantity":3}`, http.StatusOK},
		{http.MethodGet, PathOrderList, "", http.StatusOK},
		{http.MethodGet, PathAPIOrders, "", http.StatusOK},
		{http.MethodPost, PathPlaceOrder, `{"productId":99,"quantity":1}`, http.StatusBadRequest},
	} {
		w := do(t, s, c.method, c.path, c.body, maria)
		if w.Code != c.status {
			t.Fatalf("%s %s: status %d, want %d: %s", c.method, c.path, w.Code, c.status, w.Body)
		}
		if c.path == PathOrderList && !strings.Contains(w.Body.String(), "Stethoscope") {
			t.Fatalf("the order placed first is not on the screen: %s", w.Body)
		}
	}

	// Every log type PatientZero wrote, read with the collector's query.
	got := map[string][]map[string]any{}
	for _, logType := range []string{"screen", "mobile_request", "integration", "general", "error"} {
		got[logType] = collect(t, logging, logType, from, time.Now().UTC().Add(time.Minute))
	}
	for logType, n := range map[string]int{"screen": 1, "mobile_request": 2, "integration": 1, "general": 1, "error": 1} {
		if len(got[logType]) != n {
			t.Fatalf("%s: the collector's query returned %d rows, want %d: %v", logType, len(got[logType]), n, got[logType])
		}
	}
	for logType, rows := range got {
		for _, row := range rows {
			if row["Request_Key"] == nil || row["Application_Name"] != Application {
				t.Errorf("%s row %v", logType, row)
			}
			for _, col := range []string{"Session_Id", "Username", "Client_IP"} {
				if v, ok := row[col]; ok && v != nil && v != maria[HeaderSession] && v != "maria.santos" && v != "10.20.30.41" {
					t.Errorf("%s.%s = %v", logType, col, v)
				}
			}
		}
	}
	errRow := got["error"][0]
	if errRow["Severity"] != nil && errRow["Severity"] != "ERROR" {
		t.Errorf("error severity %v", errRow["Severity"])
	}
	var failed map[string]any
	for _, row := range got["mobile_request"] {
		if row["Request_Key"] == errRow["Request_Key"] {
			failed = row
		}
	}
	if failed == nil || failed["Severity"] != "ERROR" {
		t.Errorf("the failed server action's mobile request row is not ERROR under the error's Request_Key: %v", got["mobile_request"])
	}
	for _, col := range []string{"Session_Id", "Username", "Client_IP"} {
		if got["screen"][0][col] == nil || got["mobile_request"][0][col] == nil || got["general"][0][col] == nil {
			t.Errorf("%s missing from the screen, mobile request or general row", col)
		}
	}
}

// openTestDatabase creates a database with a random name and drops it when
// the test ends.
func openTestDatabase(t *testing.T, admin, prefix string) *sql.DB {
	t.Helper()
	var b [4]byte
	_, _ = rand.Read(b[:])
	name := fmt.Sprintf("%s_%x", prefix, b)
	root, err := sql.Open("sqlserver", admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	if _, err := root.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("database", name)
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlserver", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if _, err := root.Exec("ALTER DATABASE " + name + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE " + name); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
	})
	return db
}

// collect runs the collector's query for the log type, binding the bounds as
// datetime as the collector does for a datetime Instant.
func collect(t *testing.T, db *sql.DB, logType string, from, to time.Time) []map[string]any {
	t.Helper()
	query, err := os.ReadFile(filepath.Join(queriesDir, logType+".sql"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(string(query),
		sql.Named("from", mssql.DateTime1(from)), sql.Named("to", mssql.DateTime1(to)), sql.Named("limit", 100))
	if err != nil {
		t.Fatalf("%s query: %v", logType, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		row := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s query: %v", logType, err)
	}
	return out
}
