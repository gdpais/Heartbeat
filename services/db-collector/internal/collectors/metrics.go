package collectors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	collectorconfig "heartbeat/internal/config"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

// Per-probe self-observability metrics, maintained by [ProbeMetrics].
const (
	// MetricProbeDuration is a histogram of probe execution time, failed and
	// timed-out executions included.  An abandoned execution is observed with
	// the time until it was abandoned.  Probes that never started are not
	// observed.  Labels: collector, environment, target, probe.
	MetricProbeDuration = "heartbeat_collector_probe_duration_seconds"
	// MetricProbeErrors counts failed probe executions by reason (see
	// [ProbeErrorReasons]).  Labels: collector, environment, target, probe,
	// reason.
	MetricProbeErrors = "heartbeat_collector_probe_errors_total"
)

// Values of the reason label of [MetricProbeErrors].  The set is fixed so
// the label stays bounded; error text never becomes a label value.
const (
	// ReasonTimeout is a probe that exceeded its own timeout or the cycle
	// deadline while running, including one the Runner abandoned because it
	// did not stop after its context ended.
	ReasonTimeout = "timeout"
	// ReasonError is any other probe failure: connection, login, query, or
	// result decoding errors.  Probes interrupted because the poller is
	// stopping (shutdown or reload) are not counted.
	ReasonError = "error"
	// ReasonNotStarted is a probe that could not start before the cycle
	// deadline because earlier probes of its target used the time, or
	// because an abandoned probe of its target is still running.
	ReasonNotStarted = "not_started"
	// ReasonPanic is a probe whose executor panicked.
	ReasonPanic = "panic"
)

// ProbeErrorReasons lists every reason label value, in a stable order.
var ProbeErrorReasons = []string{ReasonTimeout, ReasonError, ReasonNotStarted, ReasonPanic}

// probeDurationBuckets cover 5ms to 10s, the default probe timeout cap, at
// roughly two buckets per decade, plus 30s and 60s because a probe's
// timeout_ms may extend to the scrape interval.  Catalog DMV queries usually
// finish in the first buckets; the upper ones show probes approaching their
// timeout.  Few buckets keep each probe of each target at 13 histogram series.
var probeDurationBuckets = []float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60}

// probeLabels are the label names shared by both per-probe metrics.
var probeLabels = []string{"collector", "environment", "target", "probe"}

// ProbeMetrics records per-probe duration and error counts.  The series of a
// collector live as long as its [Poller]: they are created, with error
// counters at 0, when the Poller starts and deleted when it stops, so a
// reload that changes or removes targets or probes leaves no stale series.
//
// Recording never panics: a label value Prometheus rejects (invalid UTF-8 in
// a configured name) skips the observation instead of crashing the target's
// goroutine.  The zero value and a nil *ProbeMetrics record nothing.
// ProbeMetrics is safe for concurrent use.
type ProbeMetrics struct {
	duration *prometheus.HistogramVec
	errors   *prometheus.CounterVec
}

// NewProbeMetrics creates the per-probe metrics and registers them in reg.
func NewProbeMetrics(reg prometheus.Registerer) (*ProbeMetrics, error) {
	m := &ProbeMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    MetricProbeDuration,
			Help:    "Probe execution time in seconds, failed and timed-out executions included.",
			Buckets: probeDurationBuckets,
		}, probeLabels),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricProbeErrors,
			Help: "Failed probe executions by reason (timeout, error, not_started, panic).",
		}, append(append([]string(nil), probeLabels...), "reason")),
	}
	for _, collector := range []prometheus.Collector{m.duration, m.errors} {
		if err := reg.Register(collector); err != nil {
			return nil, fmt.Errorf("register probe metrics: %w", err)
		}
	}
	return m, nil
}

// initCollector creates the error counters of every probe collector
// schedules, at 0 for every reason, so rate() and increase() see the first
// error instead of a series that starts at 1.
func (m *ProbeMetrics) initCollector(collector collectorconfig.CollectorRuntimeConfig) {
	if m == nil || m.errors == nil || !collector.Enabled {
		return
	}
	for _, item := range scheduledProbes(collector) {
		for _, reason := range ProbeErrorReasons {
			_, _ = m.errors.GetMetricWithLabelValues(item.CollectorID, item.Target.EnvironmentSlug, item.Target.Name, item.Definition.Name, reason)
		}
	}
}

// observe records the execution time of one probe.
func (m *ProbeMetrics) observe(item collectormetadata.ScheduledProbe, duration time.Duration) {
	if m == nil || m.duration == nil {
		return
	}
	observer, err := m.duration.GetMetricWithLabelValues(item.CollectorID, item.Target.EnvironmentSlug, item.Target.Name, item.Definition.Name)
	if err == nil {
		observer.Observe(duration.Seconds())
	}
}

// failed counts one failed execution of a probe.
func (m *ProbeMetrics) failed(item collectormetadata.ScheduledProbe, reason string) {
	if m == nil || m.errors == nil {
		return
	}
	counter, err := m.errors.GetMetricWithLabelValues(item.CollectorID, item.Target.EnvironmentSlug, item.Target.Name, item.Definition.Name, reason)
	if err == nil {
		counter.Inc()
	}
}

// failedUnlessCanceled counts a failed probe unless cycleCtx was canceled.
// Cancellation means the poller is stopping for shutdown or a reload; the
// probe did not fail on its own, and the collector's series are about to be
// deleted.  Cycle deadlines still count.
func (m *ProbeMetrics) failedUnlessCanceled(cycleCtx context.Context, item collectormetadata.ScheduledProbe, reason string) {
	if errors.Is(cycleCtx.Err(), context.Canceled) {
		return
	}
	m.failed(item, reason)
}

// forgetCollector deletes every series of collectorID.
func (m *ProbeMetrics) forgetCollector(collectorID string) {
	if m == nil || m.duration == nil || m.errors == nil {
		return
	}
	labels := prometheus.Labels{"collector": collectorID}
	m.duration.DeletePartialMatch(labels)
	m.errors.DeletePartialMatch(labels)
}

// failureReason classifies a failed probe execution.  probeCtx is the
// probe's own context, derived from the cycle context, so it also expires at
// the cycle deadline.
func failureReason(probeCtx context.Context) string {
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return ReasonTimeout
	}
	return ReasonError
}
