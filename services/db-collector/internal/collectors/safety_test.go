package collectors

import (
	"testing"

	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

func TestProbeQueryAppliesSessionSettings(t *testing.T) {
	probe, ok := catalogsqlserver.DefaultCatalog().Get("sessions")
	if !ok {
		t.Fatal("sessions probe missing")
	}
	override := "SELECT 'x' AS status, 1 AS session_count"
	for _, tc := range []struct {
		name     string
		template string
		want     string
	}{
		{"catalog query", "", probe.QueryTemplate},
		{"query_template override", override, override},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := collectormetadata.ScheduledProbe{Definition: collectormetadata.ProbeDefinition{Name: "sessions", QueryTemplate: tc.template}}
			if got, want := probeQuery(item, probe), connector.SessionSettings+tc.want; got != want {
				t.Fatalf("probe batch = %q, want %q", got, want)
			}
		})
	}
}
