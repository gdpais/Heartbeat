package tests

import (
	"path/filepath"
	"testing"

	"heartbeat/internal/config"
)

// The schema's timeout_ms maximum must match the limit the services enforce,
// or Helm would accept a probe timeout the collector rejects (or the reverse).
func TestIntegrationsSchemaProbeTimeoutMatchesConfigLimit(t *testing.T) {
	schema := mustReadJSONSchema(t, filepath.Join(repoRoot(t), "packages/config-schema/src/integrations.schema.json"))
	defs, _ := schema["$defs"].(map[string]any)
	probe, _ := defs["probe"].(map[string]any)
	properties, _ := probe["properties"].(map[string]any)
	timeout, ok := properties["timeout_ms"].(map[string]any)
	if !ok {
		t.Fatal("integrations schema has no $defs.probe.properties.timeout_ms")
	}
	maximum, ok := timeout["maximum"].(float64)
	if !ok {
		t.Fatal("integrations schema timeout_ms has no maximum")
	}
	if want := config.MaxProbeTimeout.Milliseconds(); int64(maximum) != want {
		t.Fatalf("schema timeout_ms maximum = %v, want %d (config.MaxProbeTimeout)", maximum, want)
	}
}
