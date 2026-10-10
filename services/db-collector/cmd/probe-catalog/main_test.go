package main

import (
	"strings"
	"testing"

	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

func TestListShowsEveryMetric(t *testing.T) {
	catalog := catalogsqlserver.DefaultCatalog()
	var out strings.Builder
	if err := run(&out, catalog, ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range catalog.Names() {
		probe, _ := catalog.Get(name)
		for _, metric := range probe.Metrics {
			if !strings.Contains(out.String(), metric.Name) {
				t.Errorf("listing lacks %s (probe %s)", metric.Name, name)
			}
		}
	}
	want := "waits heartbeat_sqlserver_wait_seconds_total counter wait_time_ms 0.001 wait_type"
	for line := range strings.Lines(out.String()) {
		if strings.Join(strings.Fields(line), " ") == want {
			return
		}
	}
	t.Errorf("no line %q:\n%s", want, out.String())
}

// The printed batch must be the one the collector sends, so what a developer
// runs by hand behaves like the probe, lock timeout included.
func TestSQLPrintsTheCollectorBatch(t *testing.T) {
	catalog := catalogsqlserver.DefaultCatalog()
	probe, _ := catalog.Get("sessions")
	var out strings.Builder
	if err := run(&out, catalog, "sessions"); err != nil {
		t.Fatal(err)
	}
	if want := connector.WithSessionSettings(probe.QueryTemplate) + "\n"; out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestSQLRejectsAnUnknownProbe(t *testing.T) {
	var out strings.Builder
	err := run(&out, catalogsqlserver.DefaultCatalog(), "nope")
	if err == nil || !strings.Contains(err.Error(), "waits") {
		t.Fatalf("got %v, want an error listing the probes", err)
	}
}
