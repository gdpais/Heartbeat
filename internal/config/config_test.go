package config

import (
	"errors"
	"fmt"
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

func TestManagerPreservesActiveSnapshotWhenApplyFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integrations.yaml")
	first := []byte(`grafana:
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
`)
	second := []byte(`grafana:
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
      environment: staging
`)
	if err := os.WriteFile(path, first, 0o600); err != nil {
		t.Fatalf("write first config: %v", err)
	}
	manager, err := NewManager(path)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	active := manager.Snapshot().Config.Version
	if err := os.WriteFile(path, second, 0o600); err != nil {
		t.Fatalf("write second config: %v", err)
	}

	snapshot, err := manager.ReloadApplying(func(previous, next RuntimeConfig) error {
		if previous.Version == next.Version {
			t.Fatal("expected distinct previous and next config versions")
		}
		return fmt.Errorf("collector restart failed")
	})
	if !errors.Is(err, ErrReloadApply) {
		t.Fatalf("expected ErrReloadApply, got %v", err)
	}
	if snapshot.Config.Version != active {
		t.Fatal("apply failure replaced the active config")
	}
	if manager.Snapshot().Config.Version != active {
		t.Fatal("manager active config changed after apply failure")
	}
}

func TestRedactedDoesNotMutateActiveConfig(t *testing.T) {
	cfg := RuntimeConfig{
		Grafana:        Endpoint{DashboardTemplates: map[string]string{"sql": "/d/sql"}},
		CredentialRefs: map[string]string{"sql": "env/SQL_PASSWORD"},
		NotificationChannels: []NotificationChannel{{
			ID:            "email",
			CredentialRef: "env/SMTP_PASSWORD",
			Config:        map[string]string{"smtp_host": "smtp.example.local"},
		}},
		Collectors: []CollectorRuntimeConfig{{
			ID:            "sql",
			CredentialRef: "kv/sql",
			TargetNames:   []string{"primary"},
			Probes:        []ProbeRuntimeConfig{{Name: "waits"}},
			Targets: []TargetRuntimeConfig{{
				Name:          "primary",
				CredentialRef: "kv/target",
				Probes:        []ProbeRuntimeConfig{{Name: "sessions"}},
			}},
		}},
	}

	redacted := cfg.Redacted()
	redacted.Grafana.DashboardTemplates["sql"] = "changed"
	redacted.Collectors[0].TargetNames[0] = "changed"
	redacted.Collectors[0].Targets[0].Probes[0].Name = "changed"
	redacted.NotificationChannels[0].Config["smtp_host"] = "changed"
	redacted.Collectors[0].Targets[0].CredentialRef = "changed"
	redacted.Collectors[0].Probes[0].Name = "changed"

	if cfg.Grafana.DashboardTemplates["sql"] != "/d/sql" || cfg.Collectors[0].TargetNames[0] != "primary" || cfg.Collectors[0].Targets[0].Probes[0].Name != "sessions" {
		t.Fatal("redacted copy aliases nested active config")
	}
	if cfg.CredentialRefs["sql"] != "env/SQL_PASSWORD" {
		t.Fatal("credential_refs value was mutated")
	}
	if cfg.NotificationChannels[0].Config["smtp_host"] != "smtp.example.local" {
		t.Fatal("notification config was mutated")
	}
	if cfg.Collectors[0].Targets[0].CredentialRef != "kv/target" {
		t.Fatal("target credential_ref was mutated")
	}
	if cfg.Collectors[0].Probes[0].Name != "waits" {
		t.Fatal("collector probes were mutated")
	}
}

func TestLoadRuntimeConfigRejectsInvalidEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integrations.yaml")
	content := []byte(`grafana:
  base_url: http://grafana:3000
  dashboard_templates: {}
loki:
  base_url: http://loki:3100
  endpoint: loki:3100/loki/api/v1/push
alertmanager:
  base_url: http://alertmanager:9093
credential_refs:
  sql: env/SQL_PASSWORD
collectors: []
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := LoadRuntimeConfig(path); err == nil {
		t.Fatal("expected invalid endpoint to be rejected")
	}
}

func TestLoadRuntimeConfigRejectsPlaintextCredentialRefs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integrations.yaml")
	content := []byte(`grafana:
  base_url: http://grafana:3000
  dashboard_templates: {}
loki:
  base_url: http://loki:3100
alertmanager:
  base_url: http://alertmanager:9093
credential_refs:
  sql: plaintext-password
collectors: []
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := LoadRuntimeConfig(path); err == nil {
		t.Fatal("expected plaintext credential ref to be rejected")
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
