package outsystems

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	heartbeatconfig "heartbeat/internal/config"
)

func TestDefaultCatalogEmbedsEveryQueryFile(t *testing.T) {
	catalog := DefaultCatalog()
	names := catalog.Names()
	if !slices.Contains(names, "error") {
		t.Fatalf("catalog %v lacks the error log type", names)
	}
	for _, name := range names {
		query, ok := catalog.Get(name)
		if !ok || query.LogType != name || strings.TrimSpace(query.SQL) == "" {
			t.Errorf("log type %s: got %+v, %v", name, query, ok)
		}
	}
	if _, ok := catalog.Get("nope"); ok {
		t.Error("Get found an unknown log type")
	}
}

// Every query must follow the contract in queries/README.md as far as text
// can tell; the SQL Server tests run them.
func TestQueriesFollowTheContract(t *testing.T) {
	sessionSettings := regexp.MustCompile(`(?i)\bSET\s+(LOCK_TIMEOUT|DEADLOCK_PRIORITY|TRANSACTION)\b|\bBEGIN\s+TRAN`)
	catalog := DefaultCatalog()
	for _, name := range catalog.Names() {
		query, _ := catalog.Get(name)
		for _, want := range []string{"TOP (@limit)", "@from", "@to", "Instant", "ORDER BY"} {
			if !strings.Contains(query.SQL, want) {
				t.Errorf("%s.sql does not contain %q", name, want)
			}
		}
		if sessionSettings.MatchString(query.SQL) {
			t.Errorf("%s.sql sets session options or opens a transaction; the collector does that", name)
		}
	}
}

func TestCatalogRejectsInvalidFileNames(t *testing.T) {
	for _, name := range []string{"Error.sql", "mobile-request.sql", "1error.sql"} {
		fsys := fstest.MapFS{"queries/" + name: {Data: []byte("SELECT 1")}}
		if _, err := newCatalog(fsys, "queries"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	fsys := fstest.MapFS{"queries/mobile_request.sql": {Data: []byte("SELECT 1")}, "queries/README.md": {Data: []byte("x")}}
	catalog, err := newCatalog(fsys, "queries")
	if err != nil || !slices.Equal(catalog.Names(), []string{"mobile_request"}) {
		t.Fatalf("got %v, %v", catalog.Names(), err)
	}
}

func TestValidateRejectsUnknownLogTypes(t *testing.T) {
	catalog := DefaultCatalog()
	collector := func(kind string, enabled bool, collectorProbe, targetProbe string) heartbeatconfig.CollectorRuntimeConfig {
		c := heartbeatconfig.CollectorRuntimeConfig{ID: "c", Kind: kind, Enabled: enabled}
		if collectorProbe != "" {
			c.Probes = []heartbeatconfig.ProbeRuntimeConfig{{Name: collectorProbe}}
		}
		c.Targets = []heartbeatconfig.TargetRuntimeConfig{{Name: "t", Probes: []heartbeatconfig.ProbeRuntimeConfig{{Name: targetProbe}}}}
		return c
	}
	tests := []struct {
		name      string
		collector heartbeatconfig.CollectorRuntimeConfig
		wantErr   bool
	}{
		{"known log type", collector(heartbeatconfig.KindOutSystemsLogs, true, "error", "error"), false},
		{"unknown on the target", collector(heartbeatconfig.KindOutSystemsLogs, true, "", "screen_typo"), true},
		{"unknown on the collector", collector(heartbeatconfig.KindOutSystemsLogs, true, "screen_typo", "error"), true},
		{"disabled collectors too", collector(heartbeatconfig.KindOutSystemsLogs, false, "", "screen_typo"), true},
		{"SQL Server probes are not log types", collector(heartbeatconfig.KindSQLServer, true, "waits", "waits"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := catalog.Validate(heartbeatconfig.RuntimeConfig{Collectors: []heartbeatconfig.CollectorRuntimeConfig{tt.collector}})
			if tt.wantErr != (err != nil) {
				t.Fatalf("wantErr %v, got %v", tt.wantErr, err)
			}
			if err != nil && !strings.Contains(err.Error(), `unknown OutSystems log type "screen_typo"; log types: error`) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
