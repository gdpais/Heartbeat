package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeConfigFiltersEnabledSQLServerCollectors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integrations.yaml")
	content := []byte(`grafana:
  base_url: http://grafana:3000
  dashboard_templates:
    logs: /explore
loki:
  base_url: http://loki:3100
alertmanager:
  base_url: http://alertmanager:9093
collectors:
  - id: sql-prod
    kind: sqlserver
    enabled: true
    credential_ref: kv/sql-prod
    config:
      environment: prod
      scrape_interval: 30s
      target_names: [core-db]
  - id: otel-sidecar
    kind: otel
    enabled: true
    config: {}
  - id: sql-disabled
    kind: sqlserver
    enabled: false
    credential_ref: kv/sql-disabled
    config:
      environment: prod
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("LoadRuntimeConfig returned error: %v", err)
	}

	collectors := cfg.EnabledCollectors("sqlserver")
	if len(collectors) != 1 {
		t.Fatalf("expected 1 enabled sqlserver collector, got %d", len(collectors))
	}
	if collectors[0].ID != "sql-prod" {
		t.Fatalf("unexpected collector id: %s", collectors[0].ID)
	}
	if collectors[0].Environment != "prod" {
		t.Fatalf("unexpected environment filter: %s", collectors[0].Environment)
	}
	if collectors[0].ScrapeInterval.String() != "30s" {
		t.Fatalf("unexpected scrape interval: %s", collectors[0].ScrapeInterval)
	}
}

func TestConfigVersionStableForEquivalentContent(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.yaml")
	pathB := filepath.Join(dir, "b.yaml")
	content := []byte(`grafana:
  base_url: http://grafana:3000
  dashboard_templates: {}
loki:
  base_url: http://loki:3100
alertmanager:
  base_url: http://alertmanager:9093
collectors:
  - id: sql-prod
    kind: sqlserver
    enabled: true
    credential_ref: kv/sql-prod
    config:
      environment: prod
      scrape_interval: 30s
`)
	if err := os.WriteFile(pathA, content, 0o600); err != nil {
		t.Fatalf("write config a: %v", err)
	}
	if err := os.WriteFile(pathB, content, 0o600); err != nil {
		t.Fatalf("write config b: %v", err)
	}

	cfgA, err := LoadRuntimeConfig(pathA)
	if err != nil {
		t.Fatalf("load config a: %v", err)
	}
	cfgB, err := LoadRuntimeConfig(pathB)
	if err != nil {
		t.Fatalf("load config b: %v", err)
	}
	if cfgA.Version != cfgB.Version {
		t.Fatalf("expected equal config versions, got %q and %q", cfgA.Version, cfgB.Version)
	}
}
