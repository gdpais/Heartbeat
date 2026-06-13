package app

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	heartbeatconfig "heartbeat/internal/config"
	"heartbeat/services/otel-gateway/internal/normalization"
)

// Config holds top-level runtime parameters for the OTel gateway helper.
type Config struct {
	ListenAddr       string
	IntegrationsPath string
}

// Run starts the thin custom gateway helper. The stock OpenTelemetry Collector
// remains responsible for OTLP ingest, batching, and routing.
func Run(ctx context.Context, cfg Config) error {
	configManager, err := heartbeatconfig.NewManager(cfg.IntegrationsPath)
	if err != nil {
		return err
	}
	registry := prometheus.NewRegistry()
	normalisedTotal := promauto.With(registry).NewCounter(prometheus.CounterOpts{
		Name: "heartbeat_otel_gateway_normalized_events_total",
		Help: "Number of application events normalized into the Heartbeat contract.",
	})
	alertDeliveriesTotal := promauto.With(registry).NewCounter(prometheus.CounterOpts{
		Name: "heartbeat_otel_gateway_alert_deliveries_total",
		Help: "Number of Alertmanager webhook deliveries accepted by the gateway.",
	})
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: routes(registry, configManager, normalisedTotal, alertDeliveriesTotal),
	}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func routes(registry *prometheus.Registry, configManager *heartbeatconfig.Manager, normalisedTotal prometheus.Counter, alertDeliveriesTotal prometheus.Counter) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		snapshot := configManager.Snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "ready",
			"config_version": snapshot.Config.Version,
			"otel_endpoint":  snapshot.Config.OpenTelemetry.Endpoint,
		})
	})
	mux.HandleFunc("/v1/heartbeat/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		event, err := normalization.ParseApplicationEvent(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		normalisedTotal.Inc()
		writeJSON(w, http.StatusAccepted, event)
	})
	mux.HandleFunc("/v1/heartbeat/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		alertDeliveriesTotal.Inc()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
