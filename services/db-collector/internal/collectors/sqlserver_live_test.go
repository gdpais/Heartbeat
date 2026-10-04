//go:build sqlserver

package collectors

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

// liveCredentialRef resolves through EnvCredentialResolver to
// HEARTBEAT_CREDENTIAL_SQLSERVER_TEST ("username:password"), the
// least-privilege collector login.
const liveCredentialRef = "sqlserver-test"

// liveExecutor returns an executor for the disposable test server and its
// target logged in with credentialRef.  It skips the test outside make
// test-sqlserver.
func liveExecutor(t *testing.T, credentialRef string) (SQLExecutor, collectormetadata.DatabaseTarget) {
	t.Helper()
	addr := os.Getenv("HEARTBEAT_TEST_SQLSERVER_ADDR")
	if addr == "" {
		t.Skip("HEARTBEAT_TEST_SQLSERVER_ADDR is not set; run make test-sqlserver")
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
	executor := SQLExecutor{Manager: manager, Catalog: catalogsqlserver.DefaultCatalog()}
	target := collectormetadata.DatabaseTarget{
		EnvironmentSlug: "test",
		Name:            "sqlserver-test",
		Engine:          "sqlserver",
		Host:            host,
		Port:            port,
		DatabaseName:    "master",
		CredentialRef:   credentialRef,
	}
	return executor, target
}

// runLiveProbe runs the catalog probe name, with query as its query_template
// override when not empty, through the production executor path.
func runLiveProbe(t *testing.T, executor SQLExecutor, target collectormetadata.DatabaseTarget, name, query string) []collectorexport.Sample {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	samples, _, err := executor.RunProbe(ctx, collectormetadata.ScheduledProbe{
		CollectorID: "sqlserver-test",
		Target:      target,
		Definition:  collectormetadata.ProbeDefinition{Name: name, QueryTemplate: query},
		Assignment:  collectormetadata.ProbeAssignment{IntervalSeconds: 30},
	})
	if err != nil {
		t.Fatalf("run probe %s: %v", name, err)
	}
	return samples
}

// Runs every built-in probe against a real SQL Server (make test-sqlserver),
// logged in as the least-privilege collector login, so a probe that needs
// more than the documented grants fails here.  Fakes cannot catch what the
// catalog queries return on a live server, such as several rows mapping to
// one label set or nchar padding in label values; the exporter keeps only the
// last of duplicate series.
func TestCatalogProbesAgainstSQLServer(t *testing.T) {
	executor, target := liveExecutor(t, liveCredentialRef)

	// blocking returns rows only while a request is blocked.
	mayBeEmpty := map[string]bool{"blocking": true}
	for _, name := range executor.Catalog.Names() {
		t.Run(name, func(t *testing.T) {
			samples := runLiveProbe(t, executor, target, name, "")
			if len(samples) == 0 && !mayBeEmpty[name] {
				t.Fatal("probe returned no samples")
			}
			seen := map[string]bool{}
			for _, sample := range samples {
				for label, value := range sample.Labels {
					if value != strings.TrimSpace(value) {
						t.Errorf("%s label %s=%q has surrounding whitespace", sample.Metric, label, value)
					}
				}
				key := seriesKey(sample.Metric, sample.Labels)
				if seen[key] {
					t.Errorf("duplicate series %s", key)
				}
				seen[key] = true
			}
		})
	}
}

// Reads the session's lock timeout and deadlock priority from inside a probe
// batch, through a query_template override of a catalog probe, so it covers
// the exact path every probe and every operator override takes.  The second
// run reuses the pooled connection after go-mssqldb's session reset.
func TestSessionSettingsAgainstSQLServer(t *testing.T) {
	executor, target := liveExecutor(t, liveCredentialRef)
	// memory_pressure decodes cntr_value labelled by metric.
	const query = `SELECT 'lock_timeout' AS metric, @@LOCK_TIMEOUT AS cntr_value
UNION ALL
SELECT 'deadlock_priority', deadlock_priority FROM sys.dm_exec_sessions WHERE session_id = @@SPID`
	want := map[string]float64{
		"lock_timeout":      float64(connector.LockTimeout.Milliseconds()),
		"deadlock_priority": -5, // LOW
	}
	for run := range 2 {
		got := map[string]float64{}
		for _, sample := range runLiveProbe(t, executor, target, "memory_pressure", query) {
			got[sample.Labels["metric"]] = sample.Value
		}
		if !maps.Equal(got, want) {
			t.Fatalf("run %d: session settings = %v, want %v", run+1, got, want)
		}
	}
}

// seriesKey identifies a series by metric name and sorted labels.
func seriesKey(metric string, labels map[string]string) string {
	var b strings.Builder
	b.WriteString(metric)
	for _, name := range slices.Sorted(maps.Keys(labels)) {
		fmt.Fprintf(&b, " %s=%q", name, labels[name])
	}
	return b.String()
}
