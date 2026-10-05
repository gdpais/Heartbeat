package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestValidateRejectsTargetScrapedByTwoSQLServerCollectors(t *testing.T) {
	target := func(name, env string) TargetRuntimeConfig {
		return TargetRuntimeConfig{Name: name, EnvironmentSlug: env, Host: "db", Port: 1433}
	}
	collector := func(id string, enabled bool, kind string, targetNames []string, targets ...TargetRuntimeConfig) CollectorRuntimeConfig {
		return CollectorRuntimeConfig{ID: id, Kind: kind, Enabled: enabled, ScrapeInterval: time.Minute, TargetNames: targetNames, Targets: targets}
	}
	tests := []struct {
		name       string
		collectors []CollectorRuntimeConfig
		wantErr    bool
	}{
		{"same target in two enabled collectors", []CollectorRuntimeConfig{
			collector("a", true, "sqlserver", nil, target("core-db", "prod")),
			collector("b", true, "sqlserver", nil, target("core-db", "prod")),
		}, true},
		{"same name in another environment", []CollectorRuntimeConfig{
			collector("a", true, "sqlserver", nil, target("core-db", "prod")),
			collector("b", true, "sqlserver", nil, target("core-db", "staging")),
		}, false},
		{"second collector disabled", []CollectorRuntimeConfig{
			collector("a", true, "sqlserver", nil, target("core-db", "prod")),
			collector("b", false, "sqlserver", nil, target("core-db", "prod")),
		}, false},
		{"second collector of another kind", []CollectorRuntimeConfig{
			collector("a", true, "sqlserver", nil, target("core-db", "prod")),
			collector("b", true, "oracle", nil, target("core-db", "prod")),
		}, false},
		{"target_names leaves the duplicate unselected", []CollectorRuntimeConfig{
			collector("a", true, "sqlserver", nil, target("core-db", "prod")),
			collector("b", true, "sqlserver", []string{"other-db"}, target("core-db", "prod"), target("other-db", "prod")),
		}, false},
		{"target_names selects the duplicate", []CollectorRuntimeConfig{
			collector("a", true, "sqlserver", nil, target("core-db", "prod")),
			collector("b", true, "sqlserver", []string{"core-db"}, target("core-db", "prod"), target("other-db", "prod")),
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validate(RuntimeConfig{
				Grafana:      Endpoint{BaseURL: "http://grafana:3000"},
				Loki:         Endpoint{BaseURL: "http://loki:3100"},
				Alertmanager: Endpoint{BaseURL: "http://alertmanager:9093"},
				Collectors:   tt.collectors,
			})
			if tt.wantErr != (err != nil) {
				t.Fatalf("wantErr %v, got %v", tt.wantErr, err)
			}
			if err != nil && !strings.Contains(err.Error(), `target "core-db" in environment "prod" is scraped by collectors a and b`) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateBoundsProbeTimeout(t *testing.T) {
	maxMS := int(MaxProbeTimeout.Milliseconds())
	tests := []struct {
		name      string
		timeoutMS int
		wantErr   string
	}{
		{"unset uses the default", 0, ""},
		{"within the maximum", 5000, ""},
		{"at the maximum", maxMS, ""},
		{"negative", -1, "timeout_ms cannot be negative"},
		{"above the maximum", maxMS + 1, fmt.Sprintf("timeout_ms %d exceeds the maximum of %d", maxMS+1, maxMS)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := RuntimeConfig{
				Grafana:      Endpoint{BaseURL: "http://grafana:3000"},
				Loki:         Endpoint{BaseURL: "http://loki:3100"},
				Alertmanager: Endpoint{BaseURL: "http://alertmanager:9093"},
				Collectors: []CollectorRuntimeConfig{{
					ID: "sql", Kind: "sqlserver", Enabled: true, ScrapeInterval: time.Minute,
					Targets: []TargetRuntimeConfig{{
						Name: "core-db", Host: "db", Port: 1433,
						Probes: []ProbeRuntimeConfig{{Name: "p1", TimeoutMS: tt.timeoutMS}},
					}},
				}}}
			err := validate(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestUserinfoDetectionAndStripping(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		has      bool
		stripped string
	}{
		{raw: "http://loki:3100/loki/api/v1/push", stripped: "http://loki:3100/loki/api/v1/push"},
		{raw: "http://user:s3cret@loki:3100/push", has: true, stripped: "http://loki:3100/push"},
		{raw: "https://token@grafana.example.internal", has: true, stripped: "https://grafana.example.internal"},
		{raw: "HTTPS://a:b@c:d@host/x", has: true, stripped: "HTTPS://host/x"},
		{raw: "https://u:p@${grafana_host}/d/x?var=${target}", has: true, stripped: "https://${grafana_host}/d/x?var=${target}"},
		{raw: "//u:p@host/path", has: true, stripped: "//host/path"},
		{raw: "  http://u:p@host", has: true, stripped: "  http://host"},
		// Browser-lenient forms of special schemes.
		{raw: `http:\\u:p@host`, has: true, stripped: `http:\\host`},
		{raw: "http:u:p@host/x", has: true, stripped: "http:host/x"},
		{raw: "http:/u:p@host", has: true, stripped: "http:/host"},
		{raw: "otlp://u:p@collector:4318", has: true, stripped: "otlp://collector:4318"},
		// Browsers drop tabs and newlines anywhere before parsing.
		{raw: "ht\ttp://u:p@host", has: true, stripped: "http://host"},
		{raw: "http:/\t/u:p@host", has: true, stripped: "http://host"},
		{raw: "http://u:p\r\n@host/x", has: true, stripped: "http://host/x"},
		// '@' outside the authority is not userinfo.
		{raw: "http://host/path@x", stripped: "http://host/path@x"},
		{raw: "http://host?q=a@b", stripped: "http://host?q=a@b"},
		{raw: "http://host#a@b", stripped: "http://host#a@b"},
		{raw: `http://host\a@b`, stripped: `http://host\a@b`},
		{raw: "/explore?orgId=1&left=${loki_query}", stripped: "/explore?orgId=1&left=${loki_query}"},
		{raw: "/d/x?mail=ops@example.com", stripped: "/d/x?mail=ops@example.com"},
		{raw: "mailto:ops@example.com", stripped: "mailto:ops@example.com"},
		{raw: "ops@example.com", stripped: "ops@example.com"},
		{raw: "localhost:3100", stripped: "localhost:3100"},
		{raw: "", stripped: ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if got := hasUserinfo(tc.raw); got != tc.has {
				t.Fatalf("hasUserinfo(%q) = %t, want %t", tc.raw, got, tc.has)
			}
			if got := stripUserinfo(tc.raw); got != tc.stripped {
				t.Fatalf("stripUserinfo(%q) = %q, want %q", tc.raw, got, tc.stripped)
			}
		})
	}
}

func TestRedactedMasksChannelConfigAndURLUserinfo(t *testing.T) {
	cfg := RuntimeConfig{
		Grafana: Endpoint{
			BaseURL:            "https://admin:grafana-pass@grafana.example.internal",
			DashboardTemplates: map[string]string{"sql": "https://u:dash-pass@grafana.example.internal/d/sql"},
			DeepLinkTemplates:  map[string]string{"logs": "/explore?left=${loki_query}"},
		},
		Loki:          Endpoint{BaseURL: "http://loki:3100", Endpoint: "http://push:loki-pass@loki:3100/loki/api/v1/push"},
		Alertmanager:  Endpoint{BaseURL: "http://alertmanager:9093", Endpoint: "http://am:am-pass@alertmanager:9093/api/v2/alerts"},
		OpenTelemetry: Endpoint{Endpoint: "http://otel:otel-pass@otel-collector:4318"},
		NotificationChannels: []NotificationChannel{{
			ID:            "hook",
			ChannelType:   "webhook",
			TargetRef:     "https://hook:target-pass@hooks.example.internal/alerts",
			CredentialRef: "env/HOOK_TOKEN",
			Config:        map[string]string{"smtp_password": "config-pass", "timeout": "10s"},
		}},
	}
	redacted := cfg.Redacted()

	encoded := fmt.Sprintf("%+v", redacted)
	for _, secret := range []string{"grafana-pass", "dash-pass", "loki-pass", "am-pass", "otel-pass", "target-pass", "config-pass", "10s"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("redacted config leaks %q: %s", secret, encoded)
		}
	}
	for _, tc := range []struct{ name, got, want string }{
		{"grafana.base_url", redacted.Grafana.BaseURL, "https://grafana.example.internal"},
		{"grafana dashboard template", redacted.Grafana.DashboardTemplates["sql"], "https://grafana.example.internal/d/sql"},
		{"grafana deep link template", redacted.Grafana.DeepLinkTemplates["logs"], "/explore?left=${loki_query}"},
		{"loki.base_url", redacted.Loki.BaseURL, "http://loki:3100"},
		{"loki.endpoint", redacted.Loki.Endpoint, "http://loki:3100/loki/api/v1/push"},
		{"alertmanager.endpoint", redacted.Alertmanager.Endpoint, "http://alertmanager:9093/api/v2/alerts"},
		{"opentelemetry.endpoint", redacted.OpenTelemetry.Endpoint, "http://otel-collector:4318"},
		{"target_ref", redacted.NotificationChannels[0].TargetRef, "https://hooks.example.internal/alerts"},
		{"channel credential_ref", redacted.NotificationChannels[0].CredentialRef, "env/HOOK_TOKEN:<redacted>"},
		{"channel config value", redacted.NotificationChannels[0].Config["smtp_password"], redactedValue},
		{"channel config value", redacted.NotificationChannels[0].Config["timeout"], redactedValue},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if cfg.NotificationChannels[0].Config["smtp_password"] != "config-pass" || cfg.Loki.Endpoint != "http://push:loki-pass@loki:3100/loki/api/v1/push" {
		t.Fatal("Redacted mutated the active config")
	}
}

func TestLoadRuntimeConfigRejectsCredentialsEmbeddedInURLs(t *testing.T) {
	const base = `grafana:
  base_url: %s
  dashboard_templates:
    sql: %s
loki:
  base_url: %s
  endpoint: %s
alertmanager:
  base_url: %s
  endpoint: %s
opentelemetry:
  endpoint: %s
notification_channels:
  - id: hook
    channel_type: webhook
    target_ref: %s
collectors: []
`
	clean := []string{
		"http://grafana:3000", "/d/sql", "http://loki:3100", "http://loki:3100/loki/api/v1/push",
		"http://alertmanager:9093", "http://alertmanager:9093/api/v2/alerts", "http://otel-collector:4318",
		"http://alertmanager:9093/api/v2/alerts",
	}
	fields := []string{
		"grafana.base_url", "grafana.dashboard_templates.sql", "loki.base_url", "loki.endpoint",
		"alertmanager.base_url", "alertmanager.endpoint", "opentelemetry.endpoint", "notification channel hook target_ref",
	}
	write := func(t *testing.T, values []string) string {
		t.Helper()
		args := make([]any, len(values))
		for i, value := range values {
			args[i] = value
		}
		path := filepath.Join(t.TempDir(), "integrations.yaml")
		if err := os.WriteFile(path, []byte(fmt.Sprintf(base, args...)), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		return path
	}
	if _, err := LoadRuntimeConfig(write(t, clean)); err != nil {
		t.Fatalf("clean config rejected: %v", err)
	}
	for i, field := range fields {
		t.Run(field, func(t *testing.T) {
			values := append([]string(nil), clean...)
			values[i] = strings.Replace(values[i], "://", "://user:hunter2@", 1)
			if !strings.Contains(values[i], "hunter2") {
				values[i] = "https://user:hunter2@grafana.example.internal" + values[i]
			}
			_, err := LoadRuntimeConfig(write(t, values))
			if err == nil {
				t.Fatalf("%s with embedded credentials was accepted", field)
			}
			if !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "must not embed credentials") {
				t.Fatalf("error %q does not name %s", err, field)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("error echoes the credential: %v", err)
			}
		})
	}
}

func TestTargetRefCredentials(t *testing.T) {
	for _, tc := range []struct {
		ref      string
		has      bool
		redacted string
	}{
		{ref: "http://alertmanager:9093/api/v2/alerts", redacted: "http://alertmanager:9093/api/v2/alerts"},
		{ref: "https://hook:secret@hooks.example.internal/x", has: true, redacted: "https://hooks.example.internal/x"},
		// A bare user:password@host is read as credentials by tools like curl.
		{ref: "hook:secret@hooks.example.internal/x", has: true, redacted: "hooks.example.internal/x"},
		{ref: "hook:@hooks.example.internal", has: true, redacted: "hooks.example.internal"},
		{ref: "ops@example.com", redacted: "ops@example.com"},
		{ref: "mailto:ops@example.com", redacted: "mailto:ops@example.com"},
		{ref: "#alerts", redacted: "#alerts"},
		{ref: "hooks.example.internal/x?u=a:b@c", redacted: "hooks.example.internal/x?u=a:b@c"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			if got := targetRefCredentials(tc.ref); got != tc.has {
				t.Fatalf("targetRefCredentials(%q) = %t, want %t", tc.ref, got, tc.has)
			}
			if got := redactTargetRef(tc.ref); got != tc.redacted {
				t.Fatalf("redactTargetRef(%q) = %q, want %q", tc.ref, got, tc.redacted)
			}
		})
	}
}

func TestValidHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"sql.example.internal", true},
		{"heartbeat-sqlserver-dev", true},
		{"sql_01.corp", true},
		{"10.0.0.5", true},
		{"2001:db8::5", true},
		{"::1", true},
		// A port, brackets or IPv4 in IPv6 form would be joined into a broken URL.
		{"db.example:1433", false},
		{"[2001:db8::5]", false},
		{"::ffff:10.0.0.5", false},
		{"fe80::1%eth0", false},
		{"user:secret@db", false},
		{"user@db", false},
		{"db/instance", false},
		{`db\instance`, false},
		{"db example", false},
		{"db\texample", false},
		{"db\x00", false},
		{"db%2f", false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			if got := validHost(tc.host); got != tc.want {
				t.Fatalf("validHost(%q) = %t, want %t", tc.host, got, tc.want)
			}
		})
	}
}

func TestLoadRuntimeConfigRejectsHostThatBreaksTheConnectionURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integrations.yaml")
	content := []byte(`grafana:
  base_url: http://grafana:3000
loki:
  base_url: http://loki:3100
alertmanager:
  base_url: http://alertmanager:9093
collectors:
  - id: sql
    kind: sqlserver
    enabled: true
    config:
      targets:
        - name: core-db
          host: db.example:1433
          port: 1433
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := LoadRuntimeConfig(path)
	if err == nil || !strings.Contains(err.Error(), "collector sql target core-db host must be") {
		t.Fatalf("expected a host error, got %v", err)
	}
	if strings.Contains(err.Error(), "db.example") {
		t.Fatalf("error echoes the host value: %v", err)
	}
}
