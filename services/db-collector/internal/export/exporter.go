package export

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

type Sample struct {
	Metric string
	Value  float64
	Labels map[string]string
}

type Recorder interface {
	Record([]Sample) error
}

type PrometheusExporter struct {
	reg        prometheus.Registerer
	mu         sync.Mutex
	gauges     map[string]*prometheus.GaugeVec
	labelNames map[string][]string
}

func NewPrometheusExporter(reg prometheus.Registerer) *PrometheusExporter {
	return &PrometheusExporter{
		reg:        reg,
		gauges:     map[string]*prometheus.GaugeVec{},
		labelNames: map[string][]string{},
	}
}

func (e *PrometheusExporter) Record(samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sample := range samples {
		labelNames := sortedKeys(sample.Labels)
		gauge, ok := e.gauges[sample.Metric]
		if !ok {
			gauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: sample.Metric, Help: sample.Metric}, labelNames)
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

type InMemoryExporter struct {
	mu     sync.Mutex
	values map[string]float64
}

func NewInMemoryExporter() *InMemoryExporter {
	return &InMemoryExporter{values: map[string]float64{}}
}

func (e *InMemoryExporter) Record(samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sample := range samples {
		e.values[sample.Metric] = sample.Value
	}
	return nil
}

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
