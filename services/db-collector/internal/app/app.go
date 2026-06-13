// Package app wires together all db-collector subsystems and runs the service
// until the context is cancelled.
//
// Run is the single entry-point: it loads configuration, starts per-collector
// pollers, and serves the Prometheus /metrics endpoint along with liveness and
// readiness probes.
package app

import (
	"context"
	"encoding/json"
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
}

// Run initialises the service, starts all enabled collectors as background
// goroutines, and serves HTTP until ctx is cancelled or a fatal error occurs.
//
// Each enabled "sqlserver" collector in the integrations file is launched as
// an independent [collectors.Poller]. All pollers share the same
// [collectors.Runner] and write metrics to a single Prometheus registry that
// is exposed at /metrics.
//
// Run returns nil on a clean shutdown (context cancelled) and a non-nil error
// if the HTTP server fails to start or a collector returns an unrecoverable
// error.
func Run(ctx context.Context, cfg Config) error {
	configManager, err := heartbeatconfig.NewManager(cfg.IntegrationsPath)
	if err != nil {
		return err
	}

	registry := prometheus.NewRegistry()
	exporter := collectorexport.NewPrometheusExporter(registry)
	executor := collectors.NewSQLExecutor(connector.NewManager(connector.EnvCredentialResolver{}))
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
//   - GET /healthcheck  – Liveness probe; always returns 200 OK.
//   - GET /readyz   – Readiness probe with config version and reload status.
//   - GET /admin/config – Redacted active config diagnostics.
//   - POST /admin/config/reload – Authenticated explicit reload trigger.
func routes(registry *prometheus.Registry, configManager *heartbeatconfig.Manager, adminToken string, lifecycle *pollerLifecycle) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthcheck", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
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
		previous := configManager.Snapshot()
		next, err := configManager.Reload()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, readiness(configManager, lifecycle))
			return
		}
		diff := heartbeatconfig.DiffCollectors(previous.Config, next.Config, "sqlserver")
		if err := heartbeatconfig.ReconcileCollectors(context.Background(), lifecycle, diff); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, readiness(configManager, lifecycle))
	})
	return mux
}

func reloadOnSIGHUP(ctx context.Context, configManager *heartbeatconfig.Manager, lifecycle *pollerLifecycle) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	defer signal.Stop(signals)
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			reloadCollectors(ctx, configManager, lifecycle)
		}
	}
}

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
			reloadCollectors(ctx, configManager, lifecycle)
		}
	}
}

func reloadCollectors(ctx context.Context, configManager *heartbeatconfig.Manager, lifecycle *pollerLifecycle) {
	previous := configManager.Snapshot()
	next, err := configManager.Reload()
	if err != nil {
		return
	}
	_ = heartbeatconfig.ReconcileCollectors(ctx, lifecycle, heartbeatconfig.DiffCollectors(previous.Config, next.Config, "sqlserver"))
}

func configFingerprint(path, parent string) string {
	fileInfo, fileErr := os.Stat(path)
	parentInfo, parentErr := os.Stat(parent)
	return statFingerprint(fileInfo, fileErr) + "|" + statFingerprint(parentInfo, parentErr)
}

func statFingerprint(info os.FileInfo, err error) string {
	if err != nil {
		return err.Error()
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano) + ":" + info.Mode().String()
}

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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
