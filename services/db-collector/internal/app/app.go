// Package app wires together all db-collector subsystems and runs the service
// until the context is cancelled.
//
// Run is the single entry-point: it loads configuration, starts per-collector
// pollers, and serves the Prometheus /metrics endpoint along with liveness and
// readiness probes.
package app

import (
	"context"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"heartbeat/services/db-collector/internal/collectors"
	collectorconfig "heartbeat/services/db-collector/internal/config"
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
	runtimeCfg, err := collectorconfig.LoadRuntimeConfig(cfg.IntegrationsPath)
	if err != nil {
		return err
	}

	registry := prometheus.NewRegistry()
	exporter := collectorexport.NewPrometheusExporter(registry)
	executor := collectors.NewSQLExecutor(connector.NewManager(connector.EnvCredentialResolver{}))
	runner := collectors.NewRunner(executor, exporter, collectors.LoggingEvidenceSink{})

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for _, collector := range runtimeCfg.EnabledCollectors("sqlserver") {
		poller := collectors.Poller{Runner: runner, Collector: collector}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := poller.Start(ctx); err != nil && ctx.Err() == nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}()
	}

	server := &http.Server{Addr: cfg.ListenAddr, Handler: routes(registry)}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	go func() {
		wg.Wait()
		select {
		case errCh <- ctx.Err():
		default:
		}
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
//   - GET /readyz   – Readiness probe; always returns 200 OK.
func routes(registry *prometheus.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	return mux
}
