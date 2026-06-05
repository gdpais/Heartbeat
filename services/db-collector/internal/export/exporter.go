// Package export converts probe result samples into observable metrics.
//
// The [Recorder] interface is the single write surface.  Two implementations
// are provided:
//
//   - [PrometheusExporter] registers a GaugeVec per unique metric name in a
//     Prometheus registry and updates it on every [Recorder.Record] call.
//     It is used in production and is safe for concurrent use.
//
//   - [InMemoryExporter] stores the last recorded value per metric name in
//     memory.  It is intended for unit tests.
package export

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Sample represents a single metric observation produced by a probe execution.
type Sample struct {
	// Metric is the fully-qualified Prometheus metric name
	// (e.g. "heartbeat_sqlserver_sessions").
	Metric string
	// Help is the human-readable description registered with the metric.
	// Falls back to Metric when empty.
	Help string
	// Value is the numeric gauge value to record.
	Value float64
	// Labels is the set of label key-value pairs associated with this
	// observation (e.g. {"environment": "production", "target": "db01"}).
	Labels map[string]string
}

// Recorder persists a batch of [Sample] values to an underlying store.
type Recorder interface {
	Record([]Sample) error
}

// PrometheusExporter implements [Recorder] by registering a
// [prometheus.GaugeVec] for each unique metric name encountered and setting
// the current value on every call to Record.
//
// The label set for a metric is fixed on first registration; subsequent calls
// with a different label set return an error.
//
// PrometheusExporter is safe for concurrent use.
type PrometheusExporter struct {
	reg        prometheus.Registerer
	mu         sync.Mutex
	gauges     map[string]*prometheus.GaugeVec
	labelNames map[string][]string
}

// NewPrometheusExporter returns an exporter that registers and updates gauges
// in reg.
func NewPrometheusExporter(reg prometheus.Registerer) *PrometheusExporter {
	return &PrometheusExporter{
		reg:        reg,
		gauges:     map[string]*prometheus.GaugeVec{},
		labelNames: map[string][]string{},
	}
}

// Record implements [Recorder].  For each sample it lazily registers a
// GaugeVec on first encounter and then sets the gauge to sample.Value.
// An error is returned if Prometheus rejects the registration or if a
// subsequent call presents a different label set for an already-registered
// metric name.
func (e *PrometheusExporter) Record(samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sample := range samples {
		labelNames := sortedKeys(sample.Labels)
		gauge, ok := e.gauges[sample.Metric]
		if !ok {
			help := sample.Help
			if help == "" {
				help = sample.Metric
			}
			gauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: sample.Metric, Help: help}, labelNames)
			if err := e.reg.Register(gauge); err != nil {
				return fmt.Errorf("register gauge %s: %w", sample.Metric, err)
			}
			e.gauges[sample.Metric] = gauge
			e.labelNames[sample.Metric] = labelNames
		}
		if strings.Join(e.labelNames[sample.Metric], ",") != strings.Join(labelNames, ",") {
			return fmt.Errorf("metric %s label set changed", sample.Metric)
		}
		gauge.WithLabelValues(labelValues(sample.Labels, labelNames)...).Set(sample.Value)
	}
	return nil
}

// InMemoryExporter implements [Recorder] by storing the most-recently recorded
// value for each metric name.  It is intended for use in unit tests.
//
// InMemoryExporter is safe for concurrent use.
type InMemoryExporter struct {
	mu     sync.Mutex
	values map[string]float64
}

// NewInMemoryExporter returns an empty in-memory exporter.
func NewInMemoryExporter() *InMemoryExporter {
	return &InMemoryExporter{values: map[string]float64{}}
}

// Record implements [Recorder].  Each sample overwrites any previously stored
// value for the same metric name.
func (e *InMemoryExporter) Record(samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sample := range samples {
		e.values[sample.Metric] = sample.Value
	}
	return nil
}

// LastValue returns the most recently recorded value for metric, or 0 if no
// value has been recorded yet.
func (e *InMemoryExporter) LastValue(metric string) float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.values[metric]
}

func sortedKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func labelValues(labels map[string]string, keys []string) []string {
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, labels[key])
	}
	return values
}
