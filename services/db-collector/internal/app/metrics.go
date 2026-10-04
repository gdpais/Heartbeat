package app

import (
	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"

	"heartbeat/services/db-collector/internal/collectors"
)

// newRegistry returns the collector's Prometheus registry with Go runtime
// (go_*) and process (process_*) metrics registered, and the per-probe
// duration and error metrics the Runner records.
func newRegistry() (*prometheus.Registry, *collectors.ProbeMetrics, error) {
	registry := prometheus.NewRegistry()
	if err := registry.Register(promcollectors.NewGoCollector()); err != nil {
		return nil, nil, err
	}
	if err := registry.Register(promcollectors.NewProcessCollector(promcollectors.ProcessCollectorOpts{})); err != nil {
		return nil, nil, err
	}
	probeMetrics, err := collectors.NewProbeMetrics(registry)
	if err != nil {
		return nil, nil, err
	}
	return registry, probeMetrics, nil
}
