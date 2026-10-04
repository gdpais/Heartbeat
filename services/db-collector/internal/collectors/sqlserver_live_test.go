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

	"github.com/prometheus/client_golang/prometheus"

	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

// liveCredentialRef resolves through EnvCredentialResolver to
// HEARTBEAT_CREDENTIAL_SQLSERVER_TEST ("username:password").
const liveCredentialRef = "sqlserver-test"

// Runs every built-in probe against a real SQL Server (make test-sqlserver).
// Fakes cannot catch what the catalog queries return on a live server, such as
// several rows mapping to one label set, nchar padding in label values, or a
// pivoted counter column that is NULL; the exporter keeps only the last of
// duplicate series.  Samples are also exported through a Prometheus registry,
// as in production, so type, value and label-value errors fail here.
func TestCatalogProbesAgainstSQLServer(t *testing.T) {
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
		CredentialRef:   liveCredentialRef,
	}

	// blocking returns rows only while a request is blocked.
	mayBeEmpty := map[string]bool{"blocking": true}
	// cpu returns no row until the scheduler monitor writes its first record,
	// about a minute after SQL Server starts.
	waitForRows := map[string]time.Duration{"cpu": 3 * time.Minute}
	// SQL Server on Linux, which the test server runs, reports no system
	// idle time, so the cpu probe leaves other-process CPU out there.
	mayBeAbsent := map[string]bool{"heartbeat_sqlserver_cpu_other_process_ratio": true}
	registry := prometheus.NewRegistry()
	exporter := collectorexport.NewPrometheusExporter(registry)
	for _, name := range executor.Catalog.Names() {
		t.Run(name, func(t *testing.T) {
			run := func() ([]collectorexport.Sample, time.Duration) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				started := time.Now()
				samples, _, err := executor.RunProbe(ctx, collectormetadata.ScheduledProbe{
					CollectorID: "sqlserver-test",
					Target:      target,
					Definition:  collectormetadata.ProbeDefinition{Name: name},
					Assignment:  collectormetadata.ProbeAssignment{IntervalSeconds: 30},
				})
				if err != nil {
					t.Fatalf("run probe: %v", err)
				}
				return samples, time.Since(started)
			}
			samples, _ := run()
			for deadline := time.Now().Add(waitForRows[name]); len(samples) == 0 && time.Now().Before(deadline); {
				time.Sleep(5 * time.Second)
				samples, _ = run()
			}
			if len(samples) == 0 && !mayBeEmpty[name] {
				t.Fatal("probe returned no samples")
			}
			// A second run on the pooled connection measures the query alone.
			_, elapsed := run()
			t.Logf("%d samples, query round trip %s", len(samples), elapsed)
			probe, _ := executor.Catalog.Get(name)
			emitted := map[string]bool{}
			seen := map[string]bool{}
			for _, sample := range samples {
				emitted[sample.Metric] = true
				if sample.Type == collectorexport.Counter && sample.Value < 0 {
					t.Errorf("counter %s is negative: %v", sample.Metric, sample.Value)
				}
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
			// Every descriptor yields a sample, so no column of a pivoted
			// row is NULL on a stock server.
			for _, metric := range probe.Metrics {
				if !emitted[metric.Name] && !mayBeEmpty[name] && !mayBeAbsent[metric.Name] {
					t.Errorf("no %s sample", metric.Name)
				}
			}
			scope := collectorexport.Scope{Collector: "sqlserver-test", Target: target.Name, Probe: name}
			if err := exporter.RecordScope(scope, samples); err != nil {
				t.Errorf("export: %v", err)
			}
		})
	}
	if _, err := registry.Gather(); err != nil {
		t.Fatalf("Gather: %v", err)
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
