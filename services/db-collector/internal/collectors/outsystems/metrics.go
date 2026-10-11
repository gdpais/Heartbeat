package outsystems

import (
	"fmt"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	collectorconfig "heartbeat/internal/config"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

// Extraction self-metrics, maintained by [Metrics].  Labels: collector,
// environment, target, log_type.  Probe duration, probe errors and target
// health come from the Runner, with the log type as the probe label.
const (
	// MetricRows counts rows extracted and delivered to the OTel Collector.
	MetricRows = "heartbeat_outsystems_extract_rows_total"
	// MetricLag is how far the watermark trails now: rows newer than it are
	// not extracted yet.  At rest it is about lag plus the poll interval.
	MetricLag = "heartbeat_outsystems_extract_lag_seconds"
	// MetricExportFailures counts batches the OTel Collector did not accept
	// after retries; the watermark stays and the batch is read again.
	MetricExportFailures = "heartbeat_outsystems_export_failures_total"
	// MetricStuckBatches counts cycles that failed because every row of a
	// full batch had the same Instant.  Extraction of the log type cannot
	// move until batch_limit is raised.
	MetricStuckBatches = "heartbeat_outsystems_extract_stuck_batches_total"
	// MetricLogDelay is now minus the newest Instant extracted, set when rows
	// arrive: how far behind the platform's own log service is.
	MetricLogDelay = "heartbeat_outsystems_log_delay_seconds"
)

// metricLabels are the label names of every extraction metric.
var metricLabels = []string{"collector", "environment", "target", "log_type"}

// Metrics records the extraction self-metrics.  The series of a collector
// are created, counters at 0, when its Poller starts and deleted when it
// stops.  A label value Prometheus rejects skips the update instead of
// panicking.  A nil *Metrics records nothing.  Metrics is safe for
// concurrent use.
type Metrics struct {
	rows           *prometheus.CounterVec
	lag            *prometheus.GaugeVec
	exportFailures *prometheus.CounterVec
	stuck          *prometheus.CounterVec
	logDelay       *prometheus.GaugeVec
}

// NewMetrics creates the extraction metrics and registers them in reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricRows,
			Help: "OutSystems log rows extracted and delivered to the OTel Collector.",
		}, metricLabels),
		lag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: MetricLag,
			Help: "Seconds the OutSystems log extraction watermark trails now; about lag plus the poll interval when caught up.",
		}, metricLabels),
		exportFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricExportFailures,
			Help: "OutSystems log batches the OTel Collector did not accept after retries; they are read again.",
		}, metricLabels),
		stuck: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricStuckBatches,
			Help: "Extraction cycles that failed because every row of a full batch had the same Instant; raise batch_limit.",
		}, metricLabels),
		logDelay: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: MetricLogDelay,
			Help: "Seconds between now and the newest OutSystems log Instant extracted, set when rows arrive.",
		}, metricLabels),
	}
	for _, collector := range []prometheus.Collector{m.rows, m.lag, m.exportFailures, m.stuck, m.logDelay} {
		if err := reg.Register(collector); err != nil {
			return nil, fmt.Errorf("register outsystems metrics: %w", err)
		}
	}
	return m, nil
}

// labelValues returns the label values of item, in metricLabels order.
func labelValues(item collectormetadata.ScheduledProbe) []string {
	return []string{item.CollectorID, item.Target.EnvironmentSlug, item.Target.Name, item.Definition.Name}
}

// initCollector creates the counters of every log type collector extracts at
// 0, so increase() sees the first row and the first failure.
func (m *Metrics) initCollector(collector collectorconfig.CollectorRuntimeConfig) {
	if m == nil || !collector.Enabled {
		return
	}
	for _, target := range collector.Targets {
		if len(collector.TargetNames) > 0 && !slices.Contains(collector.TargetNames, target.Name) {
			continue
		}
		for _, probe := range target.Probes {
			values := []string{collector.ID, target.EnvironmentSlug, target.Name, probe.Name}
			for _, vec := range []*prometheus.CounterVec{m.rows, m.exportFailures, m.stuck} {
				_, _ = vec.GetMetricWithLabelValues(values...)
			}
		}
	}
}

// forgetCollector deletes every series of collectorID.
func (m *Metrics) forgetCollector(collectorID string) {
	if m == nil {
		return
	}
	labels := prometheus.Labels{"collector": collectorID}
	m.rows.DeletePartialMatch(labels)
	m.lag.DeletePartialMatch(labels)
	m.exportFailures.DeletePartialMatch(labels)
	m.stuck.DeletePartialMatch(labels)
	m.logDelay.DeletePartialMatch(labels)
}

func (m *Metrics) addRows(item collectormetadata.ScheduledProbe, n int) {
	if m == nil {
		return
	}
	if counter, err := m.rows.GetMetricWithLabelValues(labelValues(item)...); err == nil {
		counter.Add(float64(n))
	}
}

func (m *Metrics) exportFailed(item collectormetadata.ScheduledProbe) {
	if m == nil {
		return
	}
	if counter, err := m.exportFailures.GetMetricWithLabelValues(labelValues(item)...); err == nil {
		counter.Inc()
	}
}

func (m *Metrics) batchStuck(item collectormetadata.ScheduledProbe) {
	if m == nil {
		return
	}
	if counter, err := m.stuck.GetMetricWithLabelValues(labelValues(item)...); err == nil {
		counter.Inc()
	}
}

func (m *Metrics) setLag(item collectormetadata.ScheduledProbe, lag time.Duration) {
	if m == nil {
		return
	}
	if gauge, err := m.lag.GetMetricWithLabelValues(labelValues(item)...); err == nil {
		gauge.Set(lag.Seconds())
	}
}

func (m *Metrics) setLogDelay(item collectormetadata.ScheduledProbe, delay time.Duration) {
	if m == nil {
		return
	}
	if gauge, err := m.logDelay.GetMetricWithLabelValues(labelValues(item)...); err == nil {
		gauge.Set(delay.Seconds())
	}
}
