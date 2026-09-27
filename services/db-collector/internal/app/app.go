// Package app wires together all db-collector subsystems and runs the service
// until the context is cancelled.
//
// Run is the entry point. It loads configuration, reconciles enabled
// collectors, starts per-collector pollers, and serves metrics plus health
// endpoints.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	heartbeatconfig "heartbeat/internal/config"
	"heartbeat/services/db-collector/internal/collectors"
	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectorexport "heartbeat/services/db-collector/internal/export"
)

// Config holds the top-level runtime parameters for the db-collector service.
type Config struct {
	// ListenAddr is the TCP address on which the HTTP server will listen
	// (e.g. ":8082").
	ListenAddr string
	// IntegrationsPath is the path to the YAML file that declares collectors,
	// targets, and probes (e.g. "config/integrations.yaml").
	IntegrationsPath string
	// AdminToken enables POST /admin/config/reload when set. Requests must send
	// Authorization: Bearer <token>.
	AdminToken string
	// WatchInterval enables dev/local config polling when positive. The watcher
	// checks both the config file and its parent directory.
	WatchInterval time.Duration
	// SQLServerTrustServerCertificate is a dev/test escape hatch for self-signed
	// SQL Server containers. Production should keep this disabled.
	SQLServerTrustServerCertificate bool
}

// Run initialises the service, reconciles the configured collectors, and
// serves HTTP until ctx is cancelled or a fatal error occurs.
//
// Each enabled sqlserver collector is launched as an independent Poller. All
// pollers share the same Runner and export into a single Prometheus registry.
//
// Run returns nil on a clean shutdown and a non-nil error if the HTTP server
// fails to start or a collector returns an unrecoverable error.
func Run(ctx context.Context, cfg Config) error {
	configManager, err := heartbeatconfig.NewManager(cfg.IntegrationsPath)
	if err != nil {
		return err
	}

	registry := prometheus.NewRegistry()
	exporter := collectorexport.NewPrometheusExporter(registry)
	manager := connector.NewManager(connector.EnvCredentialResolver{})
	manager.TrustServerCertificate = cfg.SQLServerTrustServerCertificate
	executor := collectors.NewSQLExecutor(manager)
	runner := collectors.NewRunner(executor, exporter, collectors.LoggingEvidenceSink{})
	lifecycle := newPollerLifecycle(runner)

	errCh := make(chan error, 1)
	if err := heartbeatconfig.ReconcileCollectors(ctx, lifecycle, heartbeatconfig.DiffCollectors(heartbeatconfig.RuntimeConfig{}, configManager.Snapshot().Config, "sqlserver")); err != nil {
		return err
	}

	server := &http.Server{Addr: cfg.ListenAddr, Handler: routes(registry, configManager, cfg.AdminToken, lifecycle)}
	go reloadOnSIGHUP(ctx, configManager, lifecycle)
	if cfg.WatchInterval > 0 {
		go reloadOnConfigChange(ctx, cfg.IntegrationsPath, cfg.WatchInterval, configManager, lifecycle)
	}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
		_ = lifecycle.shutdown(context.Background())
	}()

	go func() {
		errCh <- lifecycle.wait(ctx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			return err
		}
	default:
	}
	return nil
}

// routes builds the HTTP handler tree for the service.
//
// Endpoints:
//   - GET /metrics  – Prometheus metrics scrape endpoint.
//   - GET /healthz  – Liveness probe; always returns 200 OK.
//   - GET /readyz   – Readiness probe with config version and reload status.
//   - GET /admin/config – Redacted active config diagnostics.
//   - POST /admin/config/reload – Authenticated explicit reload trigger.
func routes(registry *prometheus.Registry, configManager *heartbeatconfig.Manager, adminToken string, lifecycle *pollerLifecycle) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	health := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/healthcheck", health)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, readiness(configManager, lifecycle))
	})
	mux.HandleFunc("/admin/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		snapshot := configManager.Snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"version":         snapshot.Config.Version,
			"loaded_at":       snapshot.LoadedAt,
			"last_reload_at":  snapshot.LastReloadAt,
			"last_reload_err": snapshot.LastReloadErr,
			"config":          snapshot.Config.Redacted(),
		})
	})
	mux.HandleFunc("/admin/config/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if adminToken == "" || r.Header.Get("Authorization") != "Bearer "+adminToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if _, err := reloadCollectors(context.Background(), configManager, lifecycle); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, heartbeatconfig.ErrInvalidCandidate) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, readiness(configManager, lifecycle))
			return
		}
		writeJSON(w, http.StatusOK, readiness(configManager, lifecycle))
	})
	return mux
}

// reloadOnSIGHUP reloads collector config whenever the process receives SIGHUP.
func reloadOnSIGHUP(ctx context.Context, configManager *heartbeatconfig.Manager, lifecycle *pollerLifecycle) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	defer signal.Stop(signals)
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			_, _ = reloadCollectors(ctx, configManager, lifecycle)
		}
	}
}

// reloadOnConfigChange polls the config file and its parent directory for
// changes and reloads collectors when the fingerprint changes.
func reloadOnConfigChange(ctx context.Context, path string, interval time.Duration, configManager *heartbeatconfig.Manager, lifecycle *pollerLifecycle) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	parent := filepath.Dir(path)
	last := configFingerprint(path, parent)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next := configFingerprint(path, parent)
			if next == last {
				continue
			}
			last = next
			time.Sleep(500 * time.Millisecond)
			_, _ = reloadCollectors(ctx, configManager, lifecycle)
		}
	}
}

// reloadCollectors applies a collector diff to the active lifecycle.
func reloadCollectors(ctx context.Context, configManager *heartbeatconfig.Manager, lifecycle *pollerLifecycle) (heartbeatconfig.Snapshot, error) {
	return configManager.ReloadApplying(func(previous, next heartbeatconfig.RuntimeConfig) error {
		diff := heartbeatconfig.DiffCollectors(previous, next, "sqlserver")
		return heartbeatconfig.ReconcileCollectors(ctx, lifecycle, diff)
	})
}

// configFingerprint combines the file and parent-directory fingerprints so
// local edits and mount swaps both trigger reloads.
func configFingerprint(path, parent string) string {
	fileInfo, fileErr := os.Stat(path)
	parentInfo, parentErr := os.Stat(parent)
	return statFingerprint(fileInfo, fileErr) + "|" + statFingerprint(parentInfo, parentErr)
}

// statFingerprint converts os.Stat output into a stable string fingerprint.
func statFingerprint(info os.FileInfo, err error) string {
	if err != nil {
		return err.Error()
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano) + ":" + info.Mode().String()
}

// readiness returns the JSON payload served by /readyz.
func readiness(configManager *heartbeatconfig.Manager, lifecycle *pollerLifecycle) map[string]any {
	snapshot := configManager.Snapshot()
	return map[string]any{
		"status":          "ready",
		"config_version":  snapshot.Config.Version,
		"last_reload_at":  snapshot.LastReloadAt,
		"last_reload_err": snapshot.LastReloadErr,
		"collectors":      lifecycle.statuses(),
	}
}

// writeJSON writes value as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
