package tests

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The chart validates each environment's embedded integrations.yaml with a copy
// of the integrations schema. A schema change must reach the chart too, or
// Helm would accept configs the services reject (and the reverse).
func TestChartValuesSchemaEmbedsIntegrationsSchema(t *testing.T) {
	root := repoRoot(t)
	source := mustReadJSONSchema(t, filepath.Join(root, "packages/config-schema/src/integrations.schema.json"))
	chart := mustReadJSONSchema(t, filepath.Join(root, "infra/helm/heartbeat/values.schema.json"))

	delete(source, "$schema")
	properties, _ := chart["properties"].(map[string]any)
	embedded, ok := properties["integrations"].(map[string]any)
	if !ok {
		t.Fatal("values.schema.json has no properties.integrations")
	}
	if !reflect.DeepEqual(source, embedded) {
		t.Fatal("infra/helm/heartbeat/values.schema.json properties.integrations differs from " +
			"packages/config-schema/src/integrations.schema.json (minus $schema); copy the schema across")
	}
}
