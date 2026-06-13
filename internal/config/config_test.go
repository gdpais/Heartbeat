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
opentelemetry:
  endpoint: http://otel-collector:4318
collectors:
  - id: sql-prod
    kind: sqlserver
    enabled: true
    credential_ref: kv/sql-prod
    config:
      environment: prod
      scrape_interval: 30s
      target_names: [core-db]
      probes:
        - name: waits
        - name: sessions
      targets:
        - name: core-db
          host: sql.prod.local
          port: 1433
          database_name: Heartbeat
          credential_ref: kv/core-db
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
	target := collectors[0].Targets[0]
	if target.EnvironmentSlug != "prod" {
		t.Fatalf("unexpected target environment: %s", target.EnvironmentSlug)
	}
	if len(target.Probes) != 2 {
		t.Fatalf("expected target to inherit 2 probes, got %d", len(target.Probes))
	}
}

func TestManagerPreservesActiveSnapshotOnInvalidReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integrations.yaml")
	valid := []byte(`grafana:
  base_url: http://grafana:3000
  dashboard_templates: {}
loki:
  base_url: http://loki:3100
alertmanager:
  base_url: http://alertmanager:9093
opentelemetry:
  endpoint: http://otel-collector:4318
collectors:
  - id: sql-prod
    kind: sqlserver
    enabled: true
    credential_ref: kv/sql-prod
    config:
      environment: prod
      scrape_interval: 30s
`)
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatalf("write valid config: %v", err)
	}
	manager, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	active := manager.Snapshot().Config.Version
	if err := os.WriteFile(path, []byte("grafana: {}\n"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	snapshot, err := manager.Reload()
	if err == nil {
		t.Fatal("expected reload error")
	}
	if snapshot.Config.Version != active {
		t.Fatal("invalid reload replaced the active config")
	}
	if snapshot.LastReloadErr == "" {
		t.Fatal("expected reload error to be recorded")
	}
}

func TestDiffCollectorsUsesStableIDs(t *testing.T) {
	old := RuntimeConfig{Collectors: []CollectorRuntimeConfig{
		{ID: "a", Kind: "sqlserver", Enabled: true},
		{ID: "b", Kind: "sqlserver", Enabled: true},
	}}
	next := RuntimeConfig{Collectors: []CollectorRuntimeConfig{
		{ID: "b", Kind: "sqlserver", Enabled: true, Environment: "prod"},
		{ID: "c", Kind: "sqlserver", Enabled: true},
	}}
	diff := DiffCollectors(old, next, "sqlserver")
	if len(diff.Added) != 1 || diff.Added[0].ID != "c" {
		t.Fatalf("unexpected added collectors: %#v", diff.Added)
	}
	if len(diff.Updated) != 1 || diff.Updated[0].ID != "b" {
		t.Fatalf("unexpected updated collectors: %#v", diff.Updated)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != "a" {
		t.Fatalf("unexpected removed collectors: %#v", diff.Removed)
	}
}
