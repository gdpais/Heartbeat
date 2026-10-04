// Package collectors provides the runtime execution layer for database probes.
//
// The central types are [Runner] and [Poller]:
//
//   - Runner executes a single scrape cycle for one collector.  Targets run
//     concurrently (bounded by [DefaultMaxConcurrentTargets]); the probes of
//     one target run serially so each database sees at most one collector
//     query at a time.  A probe error only affects its own target: every
//     other probe and target still runs and records samples.  Samples are
//     recorded per probe through a [collectorexport.ScopedRecorder] when the
//     exporter supports it, so series that disappear from a probe result (or
//     belong to a failed probe) are deleted instead of going stale.  Evidence
//     is forwarded to an [EvidenceSink] as soon as each target finishes.
//
//   - Poller wraps a Runner and runs a cycle on a fixed interval driven by
//     [CollectorRuntimeConfig.ScrapeInterval].  It owns the per-target
//     backoff state, reports every cycle through its Report callback, and
//     keeps running until the context is cancelled, whatever the probes do.
//
// With [Runner.WithProbeMetrics], every probe execution is also timed and
// every probe failure counted by reason (see [ProbeMetrics]).
//
// Every probe failure, recovered panic, backoff transition, recovery, and
// exporter or sink error is logged synchronously through the Runner's
// [slog.Logger] from the goroutine that observed it, so no error is lost to
// concurrency.
//
// SQLExecutor is the default [ProbeExecutor] implementation: it opens a live
// database connection via a [connector.Manager], looks up the probe in the
// [catalogsqlserver.Catalog], executes the SQL query, and decodes the result
// set into typed [collectorexport.Sample] values.
package collectors

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	collectorconfig "heartbeat/internal/config"
	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

// DefaultMaxConcurrentTargets bounds how many targets of one collector are
// scraped at the same time unless [Runner.WithMaxConcurrentTargets] overrides
// it.
const DefaultMaxConcurrentTargets = 8

const (
	// maxBackoff caps the delay before a failing target is retried.
	maxBackoff = 5 * time.Minute
	// backoffJitter is the relative jitter added to every backoff (+0..20%).
	backoffJitter = 0.2
	// maxDefaultProbeTimeout caps the default probe timeout.
	maxDefaultProbeTimeout = 10 * time.Second
	// fallbackProbeTimeout is used when no scrape interval is known.
	fallbackProbeTimeout = 5 * time.Second

	// healthScopeProbe and cycleScopeProbe are reserved exporter scope names
	// for the collector self-observability series.
	healthScopeProbe = "__health__"
	cycleScopeProbe  = "__cycle__"
)

// Self-observability metrics recorded through the Runner's exporter.
const (
	// MetricTargetUp is 1 when the target's last cycle succeeded and 0 when
	// it failed or is backing off.  Labels: collector, environment, target.
	MetricTargetUp = "heartbeat_collector_target_up"
	// MetricTargetLastSuccess is the Unix time of the target's last fully
	// successful cycle.  Absent until the first success.  Labels: collector,
	// environment, target.
	MetricTargetLastSuccess = "heartbeat_collector_target_last_success_timestamp_seconds"
	// MetricTargetConsecutiveFailures counts failed cycles since the target's
	// last success.  Labels: collector, environment, target.
	MetricTargetConsecutiveFailures = "heartbeat_collector_target_consecutive_failures"
	// MetricCycleDuration is the wall time of the collector's last cycle.
	// Labels: collector.
	MetricCycleDuration = "heartbeat_collector_cycle_duration_seconds"
)

// fallbackSinkMu serialises evidence publishing for Runners that were not
// built with [NewRunner].
var fallbackSinkMu sync.Mutex

// ProbeExecutor executes a single scheduled probe against a live database and
// returns the decoded metric samples along with any structured evidence.
type ProbeExecutor interface {
	RunProbe(context.Context, collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error)
}

// EvidenceSink receives structured evidence produced by probes in categories
// such as "blocking" or "sessions" for downstream processing or alerting.
type EvidenceSink interface {
	Publish([]collectormetadata.Evidence) error
}

// Runner orchestrates a single scrape cycle for a collector: it fans out to
// all scheduled targets, records the resulting metric samples, and forwards
// evidence to the configured sink.
//
// Runner is a value type that may be shared by many Pollers; it carries no
// per-collector state.
type Runner struct {
	executor      ProbeExecutor
	exporter      collectorexport.Recorder
	sink          EvidenceSink
	logger        *slog.Logger
	metrics       *ProbeMetrics
	maxConcurrent int
	// sinkMu serialises Publish calls because EvidenceSink implementations
	// need not be safe for concurrent use.  It is shared by every copy of
	// the Runner.
	sinkMu *sync.Mutex
}

// WithLogger returns a copy of r that logs through logger.  A nil logger
// falls back to [slog.Default].
func (r Runner) WithLogger(logger *slog.Logger) Runner {
	r.logger = logger
	return r
}

// WithProbeMetrics returns a copy of r that records probe durations and
// errors in metrics.  A nil metrics records nothing.
func (r Runner) WithProbeMetrics(metrics *ProbeMetrics) Runner {
	r.metrics = metrics
	return r
}

// WithMaxConcurrentTargets returns a copy of r that scrapes at most n targets
// of one collector at the same time.  Values below 1 select
// [DefaultMaxConcurrentTargets].
func (r Runner) WithMaxConcurrentTargets(n int) Runner {
	r.maxConcurrent = n
	return r
}

// NewRunner constructs a Runner wired to the given executor, exporter, and
// evidence sink.  sink may be nil; evidence is silently discarded in that case.
// When exporter implements [collectorexport.ScopedRecorder], stale series are
// deleted per probe scope.
func NewRunner(executor ProbeExecutor, exporter collectorexport.Recorder, sink EvidenceSink) Runner {
	return Runner{executor: executor, exporter: exporter, sink: sink, sinkMu: &sync.Mutex{}}
}

// RunOnce executes one scrape cycle for collector and returns its result.
// Backoff state is not carried between RunOnce calls; use [Poller] for
// repeated scraping.  The returned error joins the errors of every failed
// target and is nil when all targets succeeded or collector.Enabled is false.
// A failing target never prevents the other targets from running.
func (r Runner) RunOnce(ctx context.Context, collector collectorconfig.CollectorRuntimeConfig) (CycleResult, error) {
	result := r.runCycle(ctx, collector, newTargetTracker())
	return result, cycleError(result)
}

// cycleError joins the errors of every failed target in result.
func cycleError(result CycleResult) error {
	var errs []error
	for _, target := range result.Targets {
		if target.Err != nil {
			errs = append(errs, fmt.Errorf("target %s: %w", target.Target, target.Err))
		}
	}
	return errors.Join(errs...)
}

// targetGroup holds the serially executed probes of one target.
type targetGroup struct {
	name        string
	environment string
	items       []collectormetadata.ScheduledProbe
}

// groupByTarget groups scheduled probes by target name, preserving the
// configured target and probe order.
func groupByTarget(items []collectormetadata.ScheduledProbe) []targetGroup {
	var groups []targetGroup
	index := map[string]int{}
	for _, item := range items {
		i, ok := index[item.Target.Name]
		if !ok {
			i = len(groups)
			index[item.Target.Name] = i
			groups = append(groups, targetGroup{name: item.Target.Name, environment: item.Target.EnvironmentSlug})
		}
		groups[i].items = append(groups[i].items, item)
	}
	return groups
}

// targetState is the backoff bookkeeping for one target.
type targetState struct {
	consecutiveFailures int
	lastSuccess         time.Time
	nextAttempt         time.Time
}

// targetTracker holds per-target backoff state for one collector across
// cycles.  It is owned by a single Poller and is safe for concurrent use by
// that Poller's target goroutines.
type targetTracker struct {
	mu      sync.Mutex
	targets map[string]targetState
	now     func() time.Time
	jitter  func() float64 // uniform in [0, 1)
}

func newTargetTracker() *targetTracker {
	return &targetTracker{targets: map[string]targetState{}, now: time.Now, jitter: rand.Float64}
}

// get returns the current state of target.
func (t *targetTracker) get(target string) targetState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.targets[target]
}

// succeed resets target after a successful cycle and returns the state
// before and after the reset.
func (t *targetTracker) succeed(target string, at time.Time) (previous, current targetState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previous = t.targets[target]
	current = targetState{lastSuccess: at}
	t.targets[target] = current
	return previous, current
}

// fail records a failed cycle that started at cycleStart and schedules the
// next attempt.  It returns the new state and the chosen backoff.
func (t *targetTracker) fail(target string, cycleStart time.Time, interval time.Duration) (targetState, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.targets[target]
	state.consecutiveFailures++
	backoff := backoffFor(interval, state.consecutiveFailures, t.jitter())
	state.nextAttempt = cycleStart.Add(backoff)
	t.targets[target] = state
	return state, backoff
}

// backoffFor returns how long a target waits after failures consecutive
// failed cycles.  The first failure retries on the next cycle so a single
// transient error never costs a scrape.  From the second failure on the delay
// is min(interval * 2^(failures-2), maxBackoff) scaled by a jitter factor in
// [1, 1+backoffJitter); the jitter only adds, so each backoff always skips at
// least the next cycle instead of randomly retrying on it.  jitter is uniform
// in [0, 1).
func backoffFor(interval time.Duration, failures int, jitter float64) time.Duration {
	if failures <= 1 {
		return 0
	}
	delay := interval
	if delay <= 0 {
		delay = fallbackProbeTimeout
	}
	for i := 2; i < failures && delay < maxBackoff; i++ {
		delay *= 2
	}
	delay = min(delay, maxBackoff)
	factor := 1 + backoffJitter*jitter
	return time.Duration(float64(delay) * factor)
}

// runCycle executes one cycle for collector using tracker for backoff state.
// Targets run concurrently, bounded by the Runner's concurrency limit, and the
// whole cycle is bounded by the scrape interval so cycles never overlap.
func (r Runner) runCycle(ctx context.Context, collector collectorconfig.CollectorRuntimeConfig, tracker *targetTracker) CycleResult {
	started := tracker.now()
	result := CycleResult{CollectorID: collector.ID, Started: started}
	if !collector.Enabled {
		result.Finished = started
		return result
	}
	cycleCtx, cancel := cycleContext(ctx, collector.ScrapeInterval)
	defer cancel()
	groups := groupByTarget(scheduledProbes(collector))
	result.Targets = make([]TargetResult, len(groups))
	sem := make(chan struct{}, r.concurrency())
	var wg sync.WaitGroup
	// Never leave target goroutines behind, even if this function panics.
	defer wg.Wait()
	for i, group := range groups {
		state := tracker.get(group.name)
		if started.Before(state.nextAttempt) {
			result.Targets[i] = r.skipTarget(collector.ID, group, state)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			result.Targets[i] = r.runTarget(cycleCtx, collector, group, tracker, started, sem)
		}()
	}
	wg.Wait()
	result.Finished = tracker.now()
	r.recordCycle(collector.ID, result.Finished.Sub(started))
	return result
}

// cycleContext bounds a cycle by the scrape interval when one is set.
func cycleContext(ctx context.Context, interval time.Duration) (context.Context, context.CancelFunc) {
	if interval > 0 {
		return context.WithTimeout(ctx, interval)
	}
	return context.WithCancel(ctx)
}

// skipTarget reports a target that is still backing off.
func (r Runner) skipTarget(collectorID string, group targetGroup, state targetState) TargetResult {
	r.log().Debug("target skipped during backoff",
		"collector", collectorID,
		"target", group.name,
		"consecutive_failures", state.consecutiveFailures,
		"next_attempt", state.nextAttempt)
	result := TargetResult{
		Target:              group.name,
		State:               TargetBackoff,
		ConsecutiveFailures: state.consecutiveFailures,
		LastSuccess:         state.lastSuccess,
		NextAttempt:         state.nextAttempt,
	}
	r.recordHealth(collectorID, group, result)
	return result
}

// runTarget runs every probe of one target serially, records its samples and
// evidence, and updates its backoff state.  It never panics.
func (r Runner) runTarget(ctx context.Context, collector collectorconfig.CollectorRuntimeConfig, group targetGroup, tracker *targetTracker, cycleStart time.Time, sem chan struct{}) TargetResult {
	if acquire(ctx, sem) {
		defer func() { <-sem }()
	}
	// Failure count this target will have if this cycle fails; every probe
	// error log carries it.
	failures := tracker.get(group.name).consecutiveFailures + 1
	var errs []error
	var evidence []collectormetadata.Evidence
	for _, item := range group.items {
		items, err := r.runScopedProbe(ctx, collector, item, failures)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		evidence = append(evidence, items...)
	}
	r.publish(collector.ID, group.name, evidence)
	return r.finishTarget(collector, group, tracker, cycleStart, errors.Join(errs...))
}

// acquire takes a concurrency slot, giving up when ctx is done.
func acquire(ctx context.Context, sem chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// runScopedProbe executes one probe and records its samples under the probe's
// exporter scope.  A failed probe is logged and its series are cleared.
func (r Runner) runScopedProbe(ctx context.Context, collector collectorconfig.CollectorRuntimeConfig, item collectormetadata.ScheduledProbe, failures int) ([]collectormetadata.Evidence, error) {
	scope := collectorexport.Scope{Collector: collector.ID, Target: item.Target.Name, Probe: item.Definition.Name}
	samples, evidence, err := r.executeProbe(ctx, item, collector.ScrapeInterval)
	if err != nil {
		r.log().Warn("probe failed",
			"collector", scope.Collector,
			"target", scope.Target,
			"probe", scope.Probe,
			"error", err,
			"consecutive_failures", failures)
		r.clearScope(scope)
		return nil, err
	}
	r.recordScope(scope, samples)
	return evidence, nil
}

// executeProbe runs item under its probe timeout.  Probes that cannot start
// before the cycle deadline and probes that panic are reported as errors.
// Executions are timed and failures counted through the Runner's
// [ProbeMetrics].
func (r Runner) executeProbe(ctx context.Context, item collectormetadata.ScheduledProbe, interval time.Duration) ([]collectorexport.Sample, []collectormetadata.Evidence, error) {
	name := item.Definition.Name
	if err := ctx.Err(); err != nil {
		r.metrics.failed(item, ReasonNotStarted)
		return nil, nil, notStartedError(name, err)
	}
	timeout := probeTimeout(item.Definition.TimeoutMS, interval)
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var samples []collectorexport.Sample
	var evidence []collectormetadata.Evidence
	var err error
	attrs := []any{"collector", item.CollectorID, "target", item.Target.Name, "probe", name}
	started := time.Now()
	panicErr := r.protect("probe "+name, attrs, func() {
		samples, evidence, err = r.executor.RunProbe(probeCtx, item)
	})
	r.metrics.observe(item, time.Since(started))
	if panicErr != nil {
		r.metrics.failed(item, ReasonPanic)
		return nil, nil, panicErr
	}
	if err == nil {
		return samples, evidence, nil
	}
	r.metrics.failed(item, failureReason(probeCtx))
	if ctx.Err() == nil && errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
		return nil, nil, fmt.Errorf("probe %s timed out after %s: %w", name, timeout, err)
	}
	return nil, nil, fmt.Errorf("probe %s: %w", name, err)
}

// notStartedError explains why a probe was never started.
func notStartedError(probe string, cause error) error {
	if errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("probe %s not started before cycle deadline: %w", probe, cause)
	}
	return fmt.Errorf("probe %s not started: %w", probe, cause)
}

// finishTarget updates the backoff state of a target after its probes ran,
// logs recovery or backoff entry, and records its health series.
func (r Runner) finishTarget(collector collectorconfig.CollectorRuntimeConfig, group targetGroup, tracker *targetTracker, cycleStart time.Time, err error) TargetResult {
	if err == nil {
		previous, current := tracker.succeed(group.name, tracker.now())
		if previous.consecutiveFailures > 0 {
			r.log().Info("target recovered",
				"collector", collector.ID,
				"target", group.name,
				"previous_failures", previous.consecutiveFailures)
		}
		result := TargetResult{Target: group.name, State: TargetOK, LastSuccess: current.lastSuccess}
		r.recordHealth(collector.ID, group, result)
		return result
	}
	state, backoff := tracker.fail(group.name, cycleStart, collector.ScrapeInterval)
	r.log().Warn("target failed",
		"collector", collector.ID,
		"target", group.name,
		"consecutive_failures", state.consecutiveFailures,
		"backoff", backoff.String(),
		"next_attempt", state.nextAttempt)
	result := TargetResult{
		Target:              group.name,
		State:               TargetFailed,
		Err:                 err,
		ConsecutiveFailures: state.consecutiveFailures,
		LastSuccess:         state.lastSuccess,
		NextAttempt:         state.nextAttempt,
	}
	r.recordHealth(collector.ID, group, result)
	return result
}

// recordHealth exports the self-observability series of one target.
func (r Runner) recordHealth(collectorID string, group targetGroup, result TargetResult) {
	labels := func() map[string]string {
		return map[string]string{"collector": collectorID, "environment": group.environment, "target": group.name}
	}
	up := 0.0
	if result.State == TargetOK {
		up = 1
	}
	samples := []collectorexport.Sample{
		{Metric: MetricTargetUp, Help: "Whether the collector's last cycle for the target succeeded (1) or failed or is backing off (0).", Value: up, Labels: labels()},
		{Metric: MetricTargetConsecutiveFailures, Help: "Failed collector cycles for the target since its last success.", Value: float64(result.ConsecutiveFailures), Labels: labels()},
	}
	if !result.LastSuccess.IsZero() {
		samples = append(samples, collectorexport.Sample{
			Metric: MetricTargetLastSuccess,
			Help:   "Unix time of the target's last fully successful collector cycle.",
			Value:  float64(result.LastSuccess.UnixNano()) / float64(time.Second),
			Labels: labels(),
		})
	}
	r.recordScope(collectorexport.Scope{Collector: collectorID, Target: group.name, Probe: healthScopeProbe}, samples)
}

// recordCycle exports the duration of one collector cycle.
func (r Runner) recordCycle(collectorID string, duration time.Duration) {
	r.recordScope(collectorexport.Scope{Collector: collectorID, Probe: cycleScopeProbe}, []collectorexport.Sample{{
		Metric: MetricCycleDuration,
		Help:   "Wall time of the collector's last scrape cycle in seconds.",
		Value:  duration.Seconds(),
		Labels: map[string]string{"collector": collectorID},
	}})
}

// recordScope writes samples for scope, replacing the scope's previous series
// when the exporter supports scopes.  Errors are logged, never dropped.
func (r Runner) recordScope(scope collectorexport.Scope, samples []collectorexport.Sample) {
	if r.exporter == nil {
		return
	}
	var err error
	attrs := scopeAttrs(scope)
	panicErr := r.protect("record samples", attrs, func() {
		if scoped, ok := r.exporter.(collectorexport.ScopedRecorder); ok {
			err = scoped.RecordScope(scope, samples)
			return
		}
		err = r.exporter.Record(samples)
	})
	if err != nil && panicErr == nil {
		r.log().Error("record samples failed", append(attrs, "error", err)...)
	}
}

// clearScope deletes the series of scope when the exporter supports scopes.
func (r Runner) clearScope(scope collectorexport.Scope) {
	scoped, ok := r.exporter.(collectorexport.ScopedRecorder)
	if !ok {
		return
	}
	_ = r.protect("clear samples", scopeAttrs(scope), func() { scoped.ClearScope(scope) })
}

// forgetCollector deletes every probe metric series of collectorID, and
// every exported series of it when the exporter supports scopes.
func (r Runner) forgetCollector(collectorID string) {
	attrs := []any{"collector", collectorID}
	_ = r.protect("forget probe metrics", attrs, func() { r.metrics.forgetCollector(collectorID) })
	scoped, ok := r.exporter.(collectorexport.ScopedRecorder)
	if !ok {
		return
	}
	_ = r.protect("forget collector", attrs, func() { scoped.ForgetCollector(collectorID) })
}

// publish forwards evidence of one target to the sink, serialising calls.
// Errors are logged, never dropped.
func (r Runner) publish(collectorID, target string, evidence []collectormetadata.Evidence) {
	if r.sink == nil || len(evidence) == 0 {
		return
	}
	mu := r.sinkMu
	if mu == nil {
		mu = &fallbackSinkMu
	}
	var err error
	attrs := []any{"collector", collectorID, "target", target}
	panicErr := r.protect("publish evidence", attrs, func() {
		mu.Lock()
		defer mu.Unlock()
		err = r.sink.Publish(evidence)
	})
	if err != nil && panicErr == nil {
		r.log().Error("publish evidence failed", append(attrs, "error", err)...)
	}
}

// protect runs fn and converts a panic into an error.  The panic is logged
// with its stack together with attrs.
func (r Runner) protect(operation string, attrs []any, fn func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s panicked: %v", operation, recovered)
			args := append([]any{"operation", operation, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack())}, attrs...)
			r.log().Error("recovered panic", args...)
		}
	}()
	fn()
	return nil
}

// scopeAttrs returns the log attributes identifying scope.
func scopeAttrs(scope collectorexport.Scope) []any {
	return []any{"collector", scope.Collector, "target", scope.Target, "probe", scope.Probe}
}

// log returns the configured logger or [slog.Default].
func (r Runner) log() *slog.Logger {
	if r.logger == nil {
		return slog.Default()
	}
	return r.logger
}

// concurrency returns the effective per-collector target concurrency.
func (r Runner) concurrency() int {
	if r.maxConcurrent < 1 {
		return DefaultMaxConcurrentTargets
	}
	return r.maxConcurrent
}

// LoggingEvidenceSink is a no-op [EvidenceSink] used in the default wiring.
type LoggingEvidenceSink struct{}

// Publish implements [EvidenceSink].  It is a no-op.
func (LoggingEvidenceSink) Publish([]collectormetadata.Evidence) error { return nil }

// SQLExecutor is a [ProbeExecutor] that opens a SQL Server connection via its
// Manager, resolves the probe definition from its Catalog, and executes the
// probe query.
type SQLExecutor struct {
	Manager connector.Manager
	Catalog catalogsqlserver.Catalog
}

// NewSQLExecutor returns a SQLExecutor backed by the given connection manager
// and the default SQL Server probe catalog.
func NewSQLExecutor(manager connector.Manager) SQLExecutor {
	return SQLExecutor{Manager: manager, Catalog: catalogsqlserver.DefaultCatalog()}
}

// RunProbe implements [ProbeExecutor].  It opens a connection to the target
// database, looks up the probe in the catalog, executes the SQL query within
// a per-probe deadline derived from [timeoutFor], and decodes the result rows
// into metric samples and optional evidence records.
func (e SQLExecutor) RunProbe(ctx context.Context, item collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error) {
	db, cleanup, err := e.Manager.Open(ctx, item.Target)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	probe, ok := e.Catalog.Get(item.Definition.Name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown probe %s", item.Definition.Name)
	}
	query := item.Definition.QueryTemplate
	if query == "" {
		query = probe.QueryTemplate
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeoutFor(item))
	defer cancel()
	rows, err := db.QueryContext(probeCtx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("query probe: %w", err)
	}
	defer rows.Close()
	maps, err := readRows(rows)
	if err != nil {
		return nil, nil, err
	}
	return decodeRows(item, probe, maps), buildEvidence(item, probe, maps), nil
}

// timeoutFor returns the effective timeout for one probe execution based on
// the probe's own override and its assignment interval.  See [probeTimeout].
func timeoutFor(item collectormetadata.ScheduledProbe) time.Duration {
	return probeTimeout(item.Definition.TimeoutMS, time.Duration(item.Assignment.IntervalSeconds)*time.Second)
}

// probeTimeout returns the effective timeout for one probe execution.
//
// A probe-specific timeout wins but is capped at the scrape interval, so a
// single probe can never outlive its cycle.  Without an override the default
// is half the interval, capped at maxDefaultProbeTimeout, leaving room for the
// target's other probes.  fallbackProbeTimeout applies when no interval is
// known.
func probeTimeout(timeoutMS int, interval time.Duration) time.Duration {
	if timeoutMS > 0 {
		timeout := time.Duration(timeoutMS) * time.Millisecond
		if interval > 0 && timeout > interval {
			return interval
		}
		return timeout
	}
	if interval <= 0 {
		return fallbackProbeTimeout
	}
	return min(interval/2, maxDefaultProbeTimeout)
}

// readRows materialises the query result set into a slice of column maps.
//
// Values are kept as driver-returned types except for []byte, which is
// normalised to string so downstream decoding can treat text consistently.
func readRows(rows *sql.Rows) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read columns: %w", err)
	}
	var out []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		scans := make([]any, len(columns))
		for i := range values {
			scans[i] = &values[i]
		}
		if err := rows.Scan(scans...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		item := map[string]any{}
		for i, col := range columns {
			item[col] = normalizeValue(values[i])
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return out, nil
}

// normalizeValue converts driver-returned byte slices into strings.
func normalizeValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return v
	}
}

// decodeRows maps the SQL result set into Prometheus samples.
//
// The probe catalog defines which column carries the numeric value, its
// metric type and unit scale, and which columns become labels. Rows that do
// not contain the configured value column, or hold NULL in it, are ignored.
func decodeRows(item collectormetadata.ScheduledProbe, probe catalogsqlserver.Probe, rows []map[string]any) []collectorexport.Sample {
	var samples []collectorexport.Sample
	for _, row := range rows {
		for _, metric := range probe.Metrics {
			rawValue, ok := row[metric.ValueColumn]
			if !ok {
				continue
			}
			metricValue, ok := toFloat64(rawValue)
			if !ok {
				continue
			}
			labels := map[string]string{
				"environment": item.Target.EnvironmentSlug,
				"target":      item.Target.Name,
			}
			for _, labelColumn := range metric.LabelColumns {
				if value, ok := row[labelColumn]; ok {
					labels[labelColumn] = fmt.Sprint(value)
				}
			}
			samples = append(samples, collectorexport.Sample{
				Metric: metric.Name,
				Help:   metric.Help,
				Value:  metric.Convert(metricValue),
				Type:   metric.Type,
				Labels: labels,
			})
		}
	}
	return samples
}

// buildEvidence emits a lightweight snapshot for probe categories that feed
// investigation or alerting workflows.
//
// Evidence is currently produced only for blocking and session probes and only
// when at least one row is returned.
func buildEvidence(item collectormetadata.ScheduledProbe, probe catalogsqlserver.Probe, rows []map[string]any) []collectormetadata.Evidence {
	if probe.Category != "blocking" && probe.Category != "sessions" {
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	return []collectormetadata.Evidence{{
		Kind:  probe.Category,
		Title: fmt.Sprintf("%s snapshot for %s", probe.Category, item.Target.Name),
		Metadata: map[string]string{
			"rows": strconv.Itoa(len(rows)),
		},
	}}
}

// toFloat64 normalises driver values into a float64 when possible.
func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case int:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case []byte:
		parsed, err := strconv.ParseFloat(string(v), 64)
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// Poller repeatedly runs scrape cycles for a single collector on the interval
// defined by Collector.ScrapeInterval.  Per-target backoff state lives for the
// duration of one Start call.
type Poller struct {
	Runner    Runner
	Collector collectorconfig.CollectorRuntimeConfig
	// Report, when set, receives the result of every completed cycle.
	Report func(CycleResult)
}

// scheduledProbes expands a collector into the per-target, per-probe work
// items that Runner executes.
func scheduledProbes(collector collectorconfig.CollectorRuntimeConfig) []collectormetadata.ScheduledProbe {
	var items []collectormetadata.ScheduledProbe
	for _, target := range collector.Targets {
		if len(collector.TargetNames) > 0 && !contains(collector.TargetNames, target.Name) {
			continue
		}
		for _, probe := range target.Probes {
			items = append(items, collectormetadata.ScheduledProbe{
				CollectorID: collector.ID,
				Target: collectormetadata.DatabaseTarget{
					EnvironmentSlug: target.EnvironmentSlug,
					Name:            target.Name,
					Engine:          target.Engine,
					Host:            target.Host,
					Port:            target.Port,
					DatabaseName:    target.DatabaseName,
					CredentialRef:   target.CredentialRef,
				},
				Definition: collectormetadata.ProbeDefinition{
					Name:          probe.Name,
					QueryTemplate: probe.QueryTemplate,
					TimeoutMS:     probe.TimeoutMS,
				},
				Assignment: collectormetadata.ProbeAssignment{
					IntervalSeconds: int(collector.ScrapeInterval / time.Second),
				},
			})
		}
	}
	return items
}

// contains reports whether needle appears in values.
func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// Start runs an initial scrape immediately and then ticks at
// Collector.ScrapeInterval until ctx is cancelled.  Probe errors and panics
// never stop the poller: they are logged, reported, and handled by per-target
// backoff.  Start creates the collector's probe error counters at 0, returns
// ctx.Err() once ctx is cancelled, after deleting every series the collector
// exported, and an error only if the scrape interval is not positive.
func (p Poller) Start(ctx context.Context) error {
	interval := p.Collector.ScrapeInterval
	if interval <= 0 {
		return fmt.Errorf("collector %s: scrape interval must be positive, got %s", p.Collector.ID, interval)
	}
	tracker := newTargetTracker()
	defer p.Runner.forgetCollector(p.Collector.ID)
	_ = p.Runner.protect("init probe metrics", []any{"collector", p.Collector.ID}, func() { p.Runner.metrics.initCollector(p.Collector) })
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.cycle(ctx, tracker)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// cycle runs one scrape cycle and reports it.  A panic that escapes the
// cycle is logged and reported as a failure of every scheduled target.
func (p Poller) cycle(ctx context.Context, tracker *targetTracker) {
	started := tracker.now()
	var result CycleResult
	attrs := []any{"collector", p.Collector.ID}
	if err := p.Runner.protect("collector cycle", attrs, func() {
		result = p.Runner.runCycle(ctx, p.Collector, tracker)
	}); err != nil {
		result = failedCycle(p.Collector, tracker, started, err)
	}
	if p.Report == nil {
		return
	}
	_ = p.Runner.protect("report cycle", attrs, func() { p.Report(result) })
}

// failedCycle builds the result of a cycle that panicked: every scheduled
// target is marked failed with err.
func failedCycle(collector collectorconfig.CollectorRuntimeConfig, tracker *targetTracker, started time.Time, err error) CycleResult {
	result := CycleResult{CollectorID: collector.ID, Started: started, Finished: tracker.now()}
	for _, target := range collector.Targets {
		if len(collector.TargetNames) > 0 && !contains(collector.TargetNames, target.Name) {
			continue
		}
		state := tracker.get(target.Name)
		result.Targets = append(result.Targets, TargetResult{
			Target:              target.Name,
			State:               TargetFailed,
			Err:                 err,
			ConsecutiveFailures: state.consecutiveFailures,
			LastSuccess:         state.lastSuccess,
			NextAttempt:         state.nextAttempt,
		})
	}
	return result
}
