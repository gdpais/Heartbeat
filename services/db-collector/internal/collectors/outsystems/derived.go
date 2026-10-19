package outsystems

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

// Derived metrics, computed from every row delivered to the OTel Collector
// (docs/architecture/application-observability.md#derived-metrics).  Every
// one has the labels collector, environment and application; application is
// the row's service.name.  They count rows when they are extracted, so they
// trail real time by the lag plus the poll interval.
const (
	// MetricOperationDuration is the duration of entry points: screens,
	// Reactive server calls (mobile requests), timers and exposed APIs.
	// Labels: module, log_type, outcome.
	MetricOperationDuration = "heartbeat_outsystems_operation_duration_seconds"
	// MetricOperations counts entry points.  Labels: module, log_type,
	// operation, outcome.
	MetricOperations = "heartbeat_outsystems_operations_total"
	// MetricOperationSeconds sums entry-point durations, for the mean per
	// operation.  Labels: module, log_type, operation.
	MetricOperationSeconds = "heartbeat_outsystems_operation_seconds_total"
	// MetricIntegrationDuration is the duration of every integration call,
	// consumed or exposed.  Labels: direction, type, outcome.
	MetricIntegrationDuration = "heartbeat_outsystems_integration_duration_seconds"
	// MetricErrors counts error log rows.  Label: module.
	MetricErrors = "heartbeat_outsystems_errors_total"
	// MetricSlowEvents counts the slow SQL and slow extension rows the
	// platform writes to the general log.  Labels: module, kind.
	MetricSlowEvents = "heartbeat_outsystems_slow_events_total"
	// MetricTimerDelay is how late timers start against their schedule.
	// Label: operation.
	MetricTimerDelay = "heartbeat_outsystems_timer_delay_seconds"
	// MetricScreenState is the session and viewstate size of Traditional web
	// screen requests.  Labels: module, kind.
	MetricScreenState = "heartbeat_outsystems_screen_state_bytes"
	// MetricFrontEndOperations counts entry points per front-end server.
	// Labels: front_end, outcome.
	MetricFrontEndOperations = "heartbeat_outsystems_front_end_operations_total"
)

// Label caps.  A value past its cap is counted under [otherValue], so a
// module, screen or server name the platform invents cannot grow the series
// without bound.  The first values seen since the collector started keep
// their own series; a restart can keep different ones.
const (
	// maxApplications caps application per collector.  It is a guard: a
	// factory rarely has more applications.
	maxApplications = 1000
	// maxModules caps module per collector and application.
	maxModules = 100
	// maxOperations caps operation per collector, application and log type.
	maxOperations = 100
	// maxFrontEnds caps front_end per collector.
	maxFrontEnds = 50
	// maxIntegrationTypes caps type per collector.
	maxIntegrationTypes = 20
	// otherValue is the value of a label past its cap.
	otherValue = "other"
)

// Outcomes and other fixed label values.
const (
	outcomeSuccess = "success"
	outcomeError   = "error"
)

// Entry-point log types: one row is one request or timer run.  Integration
// rows are entry points only when they are exposed APIs.
const (
	logTypeScreen        = "screen"
	logTypeMobileRequest = "mobile_request"
	logTypeCyclicJob     = "cyclic_job"
	logTypeIntegration   = "integration"
	logTypeGeneral       = "general"
)

// Columns the derived metrics read besides the ones records.go names.
const (
	columnDuration       = "Duration"
	columnScreen         = "Screen"
	columnActionName     = "Action_Name"
	columnErrorID        = "Error_Id"
	columnServer         = "Server"
	columnIsExpose       = "Is_Expose"
	columnType           = "Type"
	columnShouldHaveRun  = "Should_Have_Run_At"
	columnSessionBytes   = "Session_Bytes"
	columnViewstateBytes = "Viewstate_Bytes"
)

// slowEventKinds maps the general log's Module_Name values for slow events
// to the kind label.
var slowEventKinds = map[string]string{
	"SLOWSQL":       "sql",
	"SLOWEXTENSION": "extension",
}

// Histogram buckets.  The latency buckets hold the SLO thresholds the
// recording rules read (le="2" for requests).
var (
	latencyBuckets    = []float64{0.1, 0.25, 0.5, 1, 2, 3, 5, 10, 30, 60}
	timerDelayBuckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600}
	// stateBuckets run from 1 KiB to 16 MiB, ×4.
	stateBuckets = prometheus.ExponentialBuckets(1024, 4, 8)
)

// derivedLabels are the labels every derived metric starts with.
var derivedLabels = []string{"collector", "environment", "application"}

func withDerivedLabels(extra ...string) []string {
	return append(append([]string{}, derivedLabels...), extra...)
}

// derived holds the derived metric vectors.
type derived struct {
	operationDuration   *prometheus.HistogramVec
	operations          *prometheus.CounterVec
	operationSeconds    *prometheus.CounterVec
	integrationDuration *prometheus.HistogramVec
	errors              *prometheus.CounterVec
	slowEvents          *prometheus.CounterVec
	timerDelay          *prometheus.HistogramVec
	screenState         *prometheus.HistogramVec
	frontEndOperations  *prometheus.CounterVec

	caps labelCaps
}

func newDerived() *derived {
	return &derived{
		operationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    MetricOperationDuration,
			Help:    "Duration of OutSystems entry points (screens, Reactive server calls, timers, exposed APIs), from the logging database.",
			Buckets: latencyBuckets,
		}, withDerivedLabels("module", "log_type", "outcome")),
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricOperations,
			Help: "OutSystems entry points (screens, Reactive server calls, timers, exposed APIs) by operation, from the logging database.",
		}, withDerivedLabels("module", "log_type", "operation", "outcome")),
		operationSeconds: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricOperationSeconds,
			Help: "Total duration of OutSystems entry points by operation; divide by the operations counter for the mean.",
		}, withDerivedLabels("module", "log_type", "operation")),
		integrationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    MetricIntegrationDuration,
			Help:    "Duration of OutSystems integration calls, consumed and exposed, from the logging database.",
			Buckets: latencyBuckets,
		}, withDerivedLabels("direction", "type", "outcome")),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricErrors,
			Help: "OutSystems error log rows by module.",
		}, withDerivedLabels("module")),
		slowEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricSlowEvents,
			Help: "Slow SQL and slow extension calls OutSystems reports in the general log.",
		}, withDerivedLabels("module", "kind")),
		timerDelay: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    MetricTimerDelay,
			Help:    "Seconds OutSystems timers started after their scheduled time.",
			Buckets: timerDelayBuckets,
		}, withDerivedLabels("operation")),
		screenState: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    MetricScreenState,
			Help:    "Session and viewstate size of OutSystems Traditional web screen requests, in bytes.",
			Buckets: stateBuckets,
		}, withDerivedLabels("module", "kind")),
		frontEndOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricFrontEndOperations,
			Help: "OutSystems entry points by front-end server.",
		}, withDerivedLabels("front_end", "outcome")),
	}
}

func (d *derived) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		d.operationDuration, d.operations, d.operationSeconds, d.integrationDuration,
		d.errors, d.slowEvents, d.timerDelay, d.screenState, d.frontEndOperations,
	}
}

// forget deletes every series and label cap of collectorID.
func (d *derived) forget(collectorID string) {
	labels := prometheus.Labels{"collector": collectorID}
	for _, vec := range []interface {
		DeletePartialMatch(prometheus.Labels) int
	}{
		d.operationDuration, d.operations, d.operationSeconds, d.integrationDuration,
		d.errors, d.slowEvents, d.timerDelay, d.screenState, d.frontEndOperations,
	} {
		vec.DeletePartialMatch(labels)
	}
	d.caps.forget(collectorID)
}

// observe updates the derived metrics from rows of batch, which were
// delivered.  instants holds each row's Instant as a wall clock.
func (d *derived) observe(item collectormetadata.ScheduledProbe, batch Batch, rows [][]any, instants []time.Time) {
	columns := indexColumns(batch)
	logType := item.Definition.Name
	collector := item.CollectorID
	for r, row := range rows {
		get := func(name string) any { return columns.value(row, name) }
		application := d.caps.value(maxApplications, firstText(get(columnApplication), get(columnEspace), get(columnModule)), collector, "application")
		module := d.caps.value(maxModules, labelText(get(columnEspace)), collector, "module", application)
		base := []string{collector, item.Target.EnvironmentSlug, application}
		outcome := rowOutcome(get(columnErrorID), get(columnSeverity), logType)
		duration, hasDuration := seconds(get(columnDuration))

		switch logType {
		case errorLogType:
			observeCounter(d.errors, append(base, module))
		case logTypeGeneral:
			if kind, ok := slowEventKinds[strings.ToUpper(strings.TrimSpace(optionalText(get(columnModule))))]; ok {
				observeCounter(d.slowEvents, append(base, module, kind))
			}
		case logTypeIntegration:
			direction := "consume"
			if exposed, _ := get(columnIsExpose).(bool); exposed {
				direction = "expose"
			}
			kind := d.caps.value(maxIntegrationTypes, labelText(get(columnType)), collector, "type")
			if hasDuration {
				observeHistogram(d.integrationDuration, append(base, direction, kind, outcome), duration)
			}
		case logTypeCyclicJob:
			scheduled, ok := get(columnShouldHaveRun).(time.Time)
			if ok && !scheduled.IsZero() {
				delay := max(instants[r].Sub(scheduled).Seconds(), 0)
				operation := d.caps.value(maxOperations, labelText(get(columnActionName)), collector, "operation", application, logType)
				observeHistogram(d.timerDelay, append(base, operation), delay)
			}
		case logTypeScreen:
			for _, state := range []struct{ column, kind string }{{columnSessionBytes, "session"}, {columnViewstateBytes, "viewstate"}} {
				if size, ok := number(get(state.column)); ok && size >= 0 {
					observeHistogram(d.screenState, append(base, module, state.kind), size)
				}
			}
		}

		if !isEntryPoint(logType, get(columnIsExpose)) {
			continue
		}
		operationColumn := columnActionName
		if logType == logTypeScreen {
			operationColumn = columnScreen
		}
		operation := d.caps.value(maxOperations, labelText(get(operationColumn)), collector, "operation", application, logType)
		// Both outcomes exist as soon as one does, so error ratios read 0
		// rather than nothing before the first error.
		for _, o := range []string{outcomeSuccess, outcomeError} {
			if counter, err := d.operations.GetMetricWithLabelValues(append(base, module, logType, operation, o)...); err == nil && o == outcome {
				counter.Inc()
			}
		}
		if hasDuration {
			observeHistogram(d.operationDuration, append(base, module, logType, outcome), duration)
			if counter, err := d.operationSeconds.GetMetricWithLabelValues(append(base, module, logType, operation)...); err == nil {
				counter.Add(duration)
			}
		}
		if server := labelText(get(columnServer)); server != unknownService {
			frontEnd := d.caps.value(maxFrontEnds, server, collector, "front_end")
			for _, o := range []string{outcomeSuccess, outcomeError} {
				if counter, err := d.frontEndOperations.GetMetricWithLabelValues(append(base, frontEnd, o)...); err == nil && o == outcome {
					counter.Inc()
				}
			}
		}
	}
}

// isEntryPoint reports whether a row of logType is a request or timer run:
// screens, Reactive server calls, timers, and integrations that are exposed
// APIs.
func isEntryPoint(logType string, isExpose any) bool {
	switch logType {
	case logTypeScreen, logTypeMobileRequest, logTypeCyclicJob:
		return true
	case logTypeIntegration:
		exposed, _ := isExpose.(bool)
		return exposed
	}
	return false
}

// rowOutcome is error when the row links to an error log row or its
// severity is ERROR or above, otherwise success.
func rowOutcome(errorID, rawSeverity any, logType string) string {
	switch id := errorID.(type) {
	case nil:
	case int64, int32, int16, int:
		if fmt.Sprint(id) != "0" {
			return outcomeError
		}
	default:
		if text := strings.TrimSpace(textOf(id)); text != "" && text != "0" {
			return outcomeError
		}
	}
	if level, _ := severity(rawSeverity, logType); level >= logspb.SeverityNumber_SEVERITY_NUMBER_ERROR {
		return outcomeError
	}
	return outcomeSuccess
}

// seconds returns a Duration value, in milliseconds, as seconds.  NULL,
// non-numeric and negative values give false.
func seconds(v any) (float64, bool) {
	ms, ok := number(v)
	if !ok || ms < 0 {
		return 0, false
	}
	return ms / 1000, true
}

// number returns a numeric column value as a float64: integers, floats,
// and decimal or numeric values, which the driver returns as text bytes.
func number(v any) (float64, bool) {
	switch v := v.(type) {
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case int16:
		return float64(v), true
	case int8:
		return float64(v), true
	case uint8:
		return float64(v), true
	case int:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case []byte:
		f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// labelText returns a column value as a label value: trimmed text, or
// unknownService when it is NULL or blank.
func labelText(v any) string {
	if text := strings.TrimSpace(optionalText(v)); text != "" {
		return text
	}
	return unknownService
}

// observeCounter increments a counter, skipping label values Prometheus
// rejects.
func observeCounter(vec *prometheus.CounterVec, values []string) {
	if counter, err := vec.GetMetricWithLabelValues(values...); err == nil {
		counter.Inc()
	}
}

// observeHistogram observes v, skipping label values Prometheus rejects.
func observeHistogram(vec *prometheus.HistogramVec, values []string, v float64) {
	if histogram, err := vec.GetMetricWithLabelValues(values...); err == nil {
		histogram.Observe(v)
	}
}

// labelCaps keeps the values each capped label has seen, per scope.  It is
// safe for concurrent use.
type labelCaps struct {
	mu   sync.Mutex
	seen map[string]map[string]struct{}
}

// value returns v when it is already known in scope or scope has room for
// it, and otherValue when scope is full.  scope is the collector ID, the
// label name, then whatever the cap is per (application, log type).
func (c *labelCaps) value(limit int, v string, scope ...string) string {
	key := strings.Join(scope, "\x00")
	c.mu.Lock()
	defer c.mu.Unlock()
	values, ok := c.seen[key]
	if !ok {
		if c.seen == nil {
			c.seen = map[string]map[string]struct{}{}
		}
		values = map[string]struct{}{}
		c.seen[key] = values
	}
	if _, ok := values[v]; ok {
		return v
	}
	if len(values) >= limit {
		return otherValue
	}
	values[v] = struct{}{}
	return v
}

// forget drops every scope of collectorID.
func (c *labelCaps) forget(collectorID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := collectorID + "\x00"
	for key := range c.seen {
		if key == collectorID || strings.HasPrefix(key, prefix) {
			delete(c.seen, key)
		}
	}
}

// columnIndex maps a column name to its first position in a batch.
type columnIndex map[string]int

func indexColumns(batch Batch) columnIndex {
	index := columnIndex{}
	for i, column := range batch.Columns {
		if _, seen := index[column.Name]; !seen {
			index[column.Name] = i
		}
	}
	return index
}

// value returns row's value of the column name, or nil when the batch has no
// such column.
func (c columnIndex) value(row []any, name string) any {
	if i, ok := c[name]; ok {
		return row[i]
	}
	return nil
}
