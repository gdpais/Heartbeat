// Package export converts probe result samples into observable metrics.
//
// The [Recorder] interface is the basic write surface.  [ScopedRecorder]
// extends it with scoped replacement semantics: every series is owned by the
// [Scope] (collector, target, probe) that wrote it, and a new write for the
// same scope deletes the series that scope no longer reports.  This keeps
// label sets such as blocking session IDs, dropped databases, or removed
// targets from being exported forever with stale values.
//
// Two implementations are provided:
//
//   - [PrometheusExporter] registers a GaugeVec per unique metric name in a
//     Prometheus registry and updates it on every write.  It is used in
//     production and is safe for concurrent use.
//
//   - [InMemoryExporter] stores recorded values in memory.  It is intended
//     for unit tests.
package export

import (
	"errors"
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

// Scope identifies the producer of a group of series: one probe of one
// target of one collector.  Collector-level series use an empty Target.
type Scope struct {
	Collector string
	Target    string
	Probe     string
}

// ScopedRecorder is a [Recorder] that tracks which series each [Scope] wrote
// so series that disappear from a probe's result are deleted instead of
// being exported with their last value forever.
//
// A series written by several scopes is deleted only once no scope reports
// it any more.  Series written through plain Record are not owned by any
// scope and are never deleted.
type ScopedRecorder interface {
	Recorder
	// RecordScope replaces the series owned by scope with samples: series
	// written by scope previously but absent from samples are deleted.
	// Samples that fail validation are skipped and reported in the joined
	// error; the remaining samples are still recorded.
	RecordScope(scope Scope, samples []Sample) error
	// ClearScope deletes every series owned by scope.  It is used when a
	// probe fails, because an unknown value is better than a stale one.
	ClearScope(scope Scope)
	// ForgetCollector deletes every series owned by any scope of the given
	// collector.  It is used when a collector is stopped or replaced.
	ForgetCollector(collectorID string)
}

// seriesRef identifies a single exported series.
type seriesRef struct {
	metric string
	labels map[string]string
}

// seriesKey returns a stable identity for metric plus its label set.
func seriesKey(metric string, labels map[string]string) string {
	var b strings.Builder
	b.WriteString(metric)
	for _, key := range sortedKeys(labels) {
		b.WriteByte(0)
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(labels[key])
	}
	return b.String()
}

// newSeriesRef copies labels so later caller mutations cannot corrupt the
// tracked identity.
func newSeriesRef(sample Sample) seriesRef {
	labels := make(map[string]string, len(sample.Labels))
	for key, value := range sample.Labels {
		labels[key] = value
	}
	return seriesRef{metric: sample.Metric, labels: labels}
}

// scopeTracker records which series each scope owns and how many scopes own
// each series.  It is not safe for concurrent use; owners guard it with their
// own mutex.
type scopeTracker struct {
	scopes map[Scope]map[string]seriesRef
	owners map[string]int
}

func newScopeTracker() scopeTracker {
	return scopeTracker{scopes: map[Scope]map[string]seriesRef{}, owners: map[string]int{}}
}

// replace makes current the set of series owned by scope and returns the
// series that are no longer owned by any scope and must be deleted.
func (t *scopeTracker) replace(scope Scope, current map[string]seriesRef) []seriesRef {
	previous := t.scopes[scope]
	for key := range current {
		if _, ok := previous[key]; !ok {
			t.owners[key]++
		}
	}
	var stale []seriesRef
	for key, ref := range previous {
		if _, ok := current[key]; ok {
			continue
		}
		t.owners[key]--
		if t.owners[key] <= 0 {
			delete(t.owners, key)
			stale = append(stale, ref)
		}
	}
	if len(current) == 0 {
		delete(t.scopes, scope)
	} else {
		t.scopes[scope] = current
	}
	return stale
}

// forgetCollector releases every scope of collectorID and returns the series
// that are no longer owned by any scope.
func (t *scopeTracker) forgetCollector(collectorID string) []seriesRef {
	var stale []seriesRef
	for scope := range t.scopes {
		if scope.Collector == collectorID {
			stale = append(stale, t.replace(scope, nil)...)
		}
	}
	return stale
}

// PrometheusExporter implements [ScopedRecorder] by registering a
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
	scopes     scopeTracker
}

// NewPrometheusExporter returns an exporter that registers and updates gauges
// in reg.
func NewPrometheusExporter(reg prometheus.Registerer) *PrometheusExporter {
	return &PrometheusExporter{
		reg:        reg,
		gauges:     map[string]*prometheus.GaugeVec{},
		labelNames: map[string][]string{},
		scopes:     newScopeTracker(),
	}
}

// Record implements [Recorder].  For each sample it lazily registers a
// GaugeVec on first encounter and then sets the gauge to sample.Value.
// An error is returned if Prometheus rejects the registration or if a
// subsequent call presents a different label set for an already-registered
// metric name.  Series written through Record are never deleted; prefer
// [PrometheusExporter.RecordScope] for probe results.
func (e *PrometheusExporter) Record(samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sample := range samples {
		if err := e.setLocked(sample); err != nil {
			return err
		}
	}
	return nil
}

// RecordScope implements [ScopedRecorder].
func (e *PrometheusExporter) RecordScope(scope Scope, samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	current := make(map[string]seriesRef, len(samples))
	var errs []error
	for _, sample := range samples {
		if err := e.setLocked(sample); err != nil {
			errs = append(errs, err)
			continue
		}
		key := seriesKey(sample.Metric, sample.Labels)
		if _, dup := current[key]; dup {
			errs = append(errs, duplicateSeriesError(sample))
		}
		current[key] = newSeriesRef(sample)
	}
	e.deleteLocked(e.scopes.replace(scope, current))
	return errors.Join(errs...)
}

// duplicateSeriesError reports two samples with identical labels in one
// scoped batch.  The last value wins, so earlier rows are silently lost; this
// usually means a probe's label columns do not uniquely identify its rows.
func duplicateSeriesError(sample Sample) error {
	return fmt.Errorf("metric %s: duplicate series %v in one batch; only the last value is kept", sample.Metric, sample.Labels)
}

// ClearScope implements [ScopedRecorder].
func (e *PrometheusExporter) ClearScope(scope Scope) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleteLocked(e.scopes.replace(scope, nil))
}

// ForgetCollector implements [ScopedRecorder].
func (e *PrometheusExporter) ForgetCollector(collectorID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleteLocked(e.scopes.forgetCollector(collectorID))
}

// setLocked registers the gauge for sample on first use, validates the label
// set, and sets the value.  e.mu must be held.
func (e *PrometheusExporter) setLocked(sample Sample) error {
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
	return nil
}

// deleteLocked removes the given series from their gauges.  e.mu must be held.
func (e *PrometheusExporter) deleteLocked(stale []seriesRef) {
	for _, ref := range stale {
		if gauge, ok := e.gauges[ref.metric]; ok {
			gauge.Delete(prometheus.Labels(ref.labels))
		}
	}
}

// InMemoryExporter implements [ScopedRecorder] by storing the most-recently
// recorded value for each metric name and for each individual series.  It is
// intended for use in unit tests.
//
// InMemoryExporter is safe for concurrent use.
type InMemoryExporter struct {
	mu     sync.Mutex
	values map[string]float64
	series map[string]float64
	scopes scopeTracker
}

// NewInMemoryExporter returns an empty in-memory exporter.
func NewInMemoryExporter() *InMemoryExporter {
	return &InMemoryExporter{values: map[string]float64{}, series: map[string]float64{}, scopes: newScopeTracker()}
}

// Record implements [Recorder].  Each sample overwrites any previously stored
// value for the same metric name and series.
func (e *InMemoryExporter) Record(samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sample := range samples {
		e.setLocked(sample)
	}
	return nil
}

// RecordScope implements [ScopedRecorder].
func (e *InMemoryExporter) RecordScope(scope Scope, samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	current := make(map[string]seriesRef, len(samples))
	var errs []error
	for _, sample := range samples {
		e.setLocked(sample)
		key := seriesKey(sample.Metric, sample.Labels)
		if _, dup := current[key]; dup {
			errs = append(errs, duplicateSeriesError(sample))
		}
		current[key] = newSeriesRef(sample)
	}
	e.deleteLocked(e.scopes.replace(scope, current))
	return errors.Join(errs...)
}

// ClearScope implements [ScopedRecorder].
func (e *InMemoryExporter) ClearScope(scope Scope) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleteLocked(e.scopes.replace(scope, nil))
}

// ForgetCollector implements [ScopedRecorder].
func (e *InMemoryExporter) ForgetCollector(collectorID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleteLocked(e.scopes.forgetCollector(collectorID))
}

// Value returns the current value of the series identified by metric and
// labels, and whether that series currently exists.
func (e *InMemoryExporter) Value(metric string, labels map[string]string) (float64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	value, ok := e.series[seriesKey(metric, labels)]
	return value, ok
}

// setLocked stores sample.  e.mu must be held.
func (e *InMemoryExporter) setLocked(sample Sample) {
	e.values[sample.Metric] = sample.Value
	e.series[seriesKey(sample.Metric, sample.Labels)] = sample.Value
}

// deleteLocked removes the given series.  e.mu must be held.
func (e *InMemoryExporter) deleteLocked(stale []seriesRef) {
	for _, ref := range stale {
		delete(e.series, seriesKey(ref.metric, ref.labels))
	}
}

// LastValue returns the most recently recorded value for metric, or 0 if no
// value has been recorded yet.  It ignores scoped deletions; use
// [InMemoryExporter.Value] to check whether a series is currently exported.
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

var (
	_ ScopedRecorder = (*PrometheusExporter)(nil)
	_ ScopedRecorder = (*InMemoryExporter)(nil)
)
