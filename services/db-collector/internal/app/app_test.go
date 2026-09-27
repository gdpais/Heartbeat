package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	heartbeatconfig "heartbeat/internal/config"
	"heartbeat/services/db-collector/internal/collectors"
)

func TestRoutesExposeHealthzAndCompatibilityHealthcheck(t *testing.T) {
	configManager, _ := newTestConfigManager(t)
	handler := routes(prometheus.NewRegistry(), configManager, "", newPollerLifecycle(collectors.Runner{}))

	for _, path := range []string{"/healthz", "/healthcheck"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected %s to return 200, got %d", path, rec.Code)
		}
	}
}

func newTestConfigManager(t *testing.T) (*heartbeatconfig.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "integrations.yaml")
	content := []byte(`grafana:
  base_url: http://grafana:3000
  dashboard_templates: {}
loki:
  base_url: http://loki:3100
alertmanager:
  base_url: http://alertmanager:9093
collectors: []
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	manager, err := heartbeatconfig.NewManager(path)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}
	return manager, path
}

func TestReloadRouteRejectsUnauthorizedAndInvalidCandidates(t *testing.T) {
	manager, path := newTestConfigManager(t)
	handler := routes(prometheus.NewRegistry(), manager, "test-token", newPollerLifecycle(collectors.Runner{}))
	active := manager.Snapshot().Config.Version
	for _, tc := range []struct {
		method, token string
		want          int
	}{
		{http.MethodGet, "Bearer test-token", http.StatusMethodNotAllowed},
		{http.MethodPost, "", http.StatusUnauthorized},
		{http.MethodPost, "Bearer wrong", http.StatusUnauthorized},
		{http.MethodPost, "Bearer test-token", http.StatusOK},
	} {
		req := httptest.NewRequest(tc.method, "/admin/config/reload", nil)
		req.Header.Set("Authorization", tc.token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("reload returned %d, want %d", rec.Code, tc.want)
		}
	}
	if err := os.WriteFile(path, []byte("loki: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/reload", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || manager.Snapshot().Config.Version != active {
		t.Fatalf("invalid candidate: status=%d active=%s", rec.Code, manager.Snapshot().Config.Version)
	}
	if manager.Snapshot().LastReloadErr == "" {
		t.Fatal("missing failed reload diagnostic")
	}
}
