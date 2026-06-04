package sqlserver

import "testing"

func TestCatalogContainsRequiredProbes(t *testing.T) {
	catalog := DefaultCatalog()
	required := []string{"waits", "blocking", "sessions", "memory_pressure", "storage", "throughput"}
	for _, name := range required {
		probe, ok := catalog.Get(name)
		if !ok {
			t.Fatalf("missing required probe %q", name)
		}
		if probe.QueryTemplate == "" {
			t.Fatalf("probe %q has empty query template", name)
		}
		if len(probe.MetricNames) == 0 {
			t.Fatalf("probe %q has no metric names", name)
		}
	}
}
