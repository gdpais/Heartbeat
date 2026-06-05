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

type Config struct {
	ListenAddr       string
	IntegrationsPath string
}

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
