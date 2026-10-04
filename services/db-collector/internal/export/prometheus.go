package export

import (
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
)

// labelValueSeparator joins label values into a series key.  0xff never
// occurs in valid UTF-8, and label values are validated as UTF-8 before a
// series is created, so distinct label value lists never share a key.
const labelValueSeparator = 0xff

// PrometheusExporter implements [ScopedRecorder] on top of a Prometheus
// registry.  Each unique metric name becomes a family that is registered as
// its own [prometheus.Collector] on first use; samples set the current value
// of a series, and scrapes emit every series as a constant metric of the
// family's [MetricType].  Counters are therefore settable: a probe reports
// the source's cumulative value, and a lower value after the source restarts
// is exported as is, which rate() and increase() treat as a counter reset.
//
// The label set and type of a metric are fixed on first registration;
// subsequent samples with a different label set or type are rejected.
//
// Recording a sample for an existing series does not allocate.  Scrapes copy
// a family's series under a read lock and build the exposition after
// releasing it, so a scrape never blocks collection for longer than that
// copy.
//
// PrometheusExporter is safe for concurrent use.
type PrometheusExporter struct {
	reg prometheus.Registerer
	// mu guards every field below and the series of every family.
	mu       sync.RWMutex
	families map[string]*family
	scopes   map[Scope]*scopeSeries
	// generation numbers RecordScope calls; see [series].
	generation uint64
	// keyBuf is reused to build series keys without allocating.
	keyBuf []byte
}

// family is one registered metric name and its current series.
type family struct {
	desc       *prometheus.Desc
	name       string
	metricType MetricType
	valueType  prometheus.ValueType
	// labelNames are the sorted label names every series of the family has.
	labelNames []string
	// series is keyed by the label values in labelNames order, joined with
	// labelValueSeparator.
	series map[string]*series
}

// series is one exported series of a family.
type series struct {
	family *family
	key    string
	// labelValues follow family.labelNames and are never modified after the
	// series is created, so scrapes share them without copying.
	labelValues []string
	value       float64
	// owners counts the scopes that currently report the series.
	owners int
	// written and owned hold the generation of the last RecordScope call that
	// wrote the series and that found it owned by the recorded scope.  They
	// replace per-call sets, so steady-state RecordScope calls allocate
	// nothing.
	written uint64
	owned   uint64
}

// scopeSeries lists the series one scope owns.  spare is the backing array
// of the previous list, reused by the scope's next RecordScope call.
type scopeSeries struct {
	owned []*series
	spare []*series
}

// NewPrometheusExporter returns an exporter that registers its metric
// families in reg.
func NewPrometheusExporter(reg prometheus.Registerer) *PrometheusExporter {
	return &PrometheusExporter{
		reg:      reg,
		families: map[string]*family{},
		scopes:   map[Scope]*scopeSeries{},
	}
}

// Record implements [Recorder].  For each sample it lazily registers the
// metric family on first encounter and then sets the series to sample.Value.
// An error is returned if Prometheus rejects the registration or if a sample
// does not match the label set or type of its already-registered family.
// Series written through Record are never deleted; prefer
// [PrometheusExporter.RecordScope] for probe results.
func (e *PrometheusExporter) Record(samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, sample := range samples {
		if _, err := e.setLocked(sample); err != nil {
			return err
		}
	}
	return nil
}

// RecordScope implements [ScopedRecorder].
func (e *PrometheusExporter) RecordScope(scope Scope, samples []Sample) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.generation++
	generation := e.generation
	state := e.scopes[scope]
	var previous, next []*series
	if state != nil {
		previous, next = state.owned, state.spare[:0]
	}
	for _, s := range previous {
		s.owned = generation
	}
	var errs []error
	for _, sample := range samples {
		s, err := e.setLocked(sample)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if s.written == generation {
			errs = append(errs, duplicateSeriesError(sample))
			continue
		}
		s.written = generation
		if s.owned != generation {
			s.owners++
		}
		next = append(next, s)
	}
	for _, s := range previous {
		if s.written != generation {
			release(s)
		}
	}
	switch {
	case len(next) == 0:
		delete(e.scopes, scope)
	case state == nil:
		e.scopes[scope] = &scopeSeries{owned: next}
	default:
		// Drop the old pointers so released series can be garbage collected.
		clear(previous)
		state.owned, state.spare = next, previous[:0]
	}
	return errors.Join(errs...)
}

// ClearScope implements [ScopedRecorder].
func (e *PrometheusExporter) ClearScope(scope Scope) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.clearScopeLocked(scope)
}

// ForgetCollector implements [ScopedRecorder].
func (e *PrometheusExporter) ForgetCollector(collectorID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for scope := range e.scopes {
		if scope.Collector == collectorID {
			e.clearScopeLocked(scope)
		}
	}
}

// clearScopeLocked releases every series owned by scope.  e.mu must be held.
func (e *PrometheusExporter) clearScopeLocked(scope Scope) {
	state, ok := e.scopes[scope]
	if !ok {
		return
	}
	for _, s := range state.owned {
		release(s)
	}
	delete(e.scopes, scope)
}

// release drops one scope's ownership of s and deletes s once no scope owns
// it any more.  The exporter's mutex must be held.
func release(s *series) {
	s.owners--
	if s.owners <= 0 {
		delete(s.family.series, s.key)
	}
}

// setLocked registers the family of sample on first use, validates the
// sample against it, and sets the value of its series, creating the series
// if needed.  e.mu must be held.
func (e *PrometheusExporter) setLocked(sample Sample) (*series, error) {
	fam, ok := e.families[sample.Metric]
	if !ok {
		var err error
		if fam, err = e.registerLocked(sample); err != nil {
			return nil, err
		}
	}
	if sample.Type != fam.metricType {
		return nil, fmt.Errorf("metric %s type changed from %s to %s", sample.Metric, fam.metricType, sample.Type)
	}
	// Negated so NaN is rejected too.
	if fam.metricType == Counter && !(sample.Value >= 0) {
		return nil, fmt.Errorf("counter %s: value %v is negative or NaN", sample.Metric, sample.Value)
	}
	if len(sample.Labels) != len(fam.labelNames) {
		return nil, labelSetChangedError(sample.Metric)
	}
	key := e.keyBuf[:0]
	for i, name := range fam.labelNames {
		value, ok := sample.Labels[name]
		if !ok {
			return nil, labelSetChangedError(sample.Metric)
		}
		if i > 0 {
			key = append(key, labelValueSeparator)
		}
		key = append(key, value...)
	}
	e.keyBuf = key
	// The string conversion in a map index expression does not allocate.
	s, ok := fam.series[string(key)]
	if !ok {
		var err error
		if s, err = fam.newSeries(string(key), sample.Labels); err != nil {
			return nil, err
		}
	}
	s.value = sample.Value
	return s, nil
}

// labelSetChangedError reports a sample whose label names differ from its
// registered family.
func labelSetChangedError(metric string) error {
	return fmt.Errorf("metric %s label set changed", metric)
}

// registerLocked creates the family of sample and registers it.  e.mu must
// be held; registration calls Describe, which never takes e.mu.
func (e *PrometheusExporter) registerLocked(sample Sample) (*family, error) {
	help := sample.Help
	if help == "" {
		help = sample.Metric
	}
	valueType := prometheus.GaugeValue
	if sample.Type == Counter {
		valueType = prometheus.CounterValue
	}
	labelNames := sortedKeys(sample.Labels)
	fam := &family{
		desc:       prometheus.NewDesc(sample.Metric, help, labelNames, nil),
		name:       sample.Metric,
		metricType: sample.Type,
		valueType:  valueType,
		labelNames: labelNames,
		series:     map[string]*series{},
	}
	if err := e.reg.Register(familyCollector{exporter: e, family: fam}); err != nil {
		return nil, fmt.Errorf("register %s %s: %w", sample.Type, sample.Metric, err)
	}
	e.families[sample.Metric] = fam
	return fam, nil
}

// newSeries creates and stores the series with the given key and labels.
// Label values must be valid UTF-8, or every later scrape would fail.
func (f *family) newSeries(key string, labels map[string]string) (*series, error) {
	values := make([]string, len(f.labelNames))
	for i, name := range f.labelNames {
		value := labels[name]
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("metric %s: label %s is not valid UTF-8", f.name, name)
		}
		values[i] = value
	}
	s := &series{family: f, key: key, labelValues: values}
	f.series[key] = s
	return s, nil
}

// familyCollector exposes one family of a [PrometheusExporter] to the
// registry.  Registering each family separately lets the registry reject
// conflicting names and label sets at registration, as it would for a
// GaugeVec.
type familyCollector struct {
	exporter *PrometheusExporter
	family   *family
}

// point is a copy of one series taken during a scrape.
type point struct {
	labelValues []string
	value       float64
}

// Describe implements [prometheus.Collector].  It must not take the
// exporter's lock: registration happens while that lock is held.
func (c familyCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.family.desc
}

// Collect implements [prometheus.Collector].  It copies the family's series
// under a read lock and builds the metrics after releasing it.
func (c familyCollector) Collect(ch chan<- prometheus.Metric) {
	c.exporter.mu.RLock()
	points := make([]point, 0, len(c.family.series))
	for _, s := range c.family.series {
		points = append(points, point{labelValues: s.labelValues, value: s.value})
	}
	c.exporter.mu.RUnlock()
	for _, p := range points {
		metric, err := prometheus.NewConstMetric(c.family.desc, c.family.valueType, p.value, p.labelValues...)
		if err != nil {
			metric = prometheus.NewInvalidMetric(c.family.desc, err)
		}
		ch <- metric
	}
}

var _ ScopedRecorder = (*PrometheusExporter)(nil)
