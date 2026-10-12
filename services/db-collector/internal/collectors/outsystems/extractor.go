// Package outsystems extracts OutSystems 11 logs from the logging database
// and sends them to the OTel Collector as OTLP logs: the db-collector's
// outsystems_logs collector kind (ADR 0008).
//
// [Executor] is the kind's [collectors.ProbeExecutor]: the Runner calls it
// once per target and log type every poll interval, with the same per-target
// isolation, backoff, hard deadline and probe self-metrics as SQL Server
// probes.  One call reads the log type's rows with Instant in (watermark,
// now − lag], at most batch_limit of them, through the log type's query
// (probes/outsystems), sends them to <opentelemetry.endpoint>/v1/logs, and
// only then moves the watermark, which [Store] keeps in a file.  Delivery is
// therefore at least once: a failed export or a crash before the watermark is
// saved reads the same rows again.
//
// The lag is the collector's, or the log type's own when its probe sets one:
// a screen or cyclic_job row carries the start of the operation as Instant
// but is written when the operation ends, so its lag must cover the longest
// screen request or timer.
//
// A full batch (batch_limit rows) drops its trailing rows that share the last
// Instant, which the next batch reads with the rest of that Instant, and the
// call reads the next batch at once, up to [DefaultMaxBatches] per call while
// the probe's deadline leaves time.  When every row of a full batch shares
// one Instant the call fails and counts [MetricStuckBatches]: no watermark can
// separate those rows, and skipping them would lose logs.  Raising
// batch_limit is the fix.
//
// The query contract (queries/README.md) is checked on every batch: a
// datetime or datetime2 Instant column, at most @limit rows, ascending
// Instant values within (@from, @to].  A violation fails the call without sending anything.
package outsystems

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	collectorconfig "heartbeat/internal/config"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogoutsystems "heartbeat/services/db-collector/internal/probes/outsystems"
)

// DefaultMaxBatches bounds the batches one call reads while batches are
// full, so a large backlog is worked off over several cycles instead of
// holding the target's other log types back.
const DefaultMaxBatches = 10

// ErrBatchStuck reports a full batch whose rows all share one Instant.
var ErrBatchStuck = errors.New("every row of a full batch has the same Instant")

// ErrContract reports a query result that breaks the query contract.
var ErrContract = errors.New("query breaks the outsystems_logs query contract")

// Exporter delivers OTLP logs; [otlp.Client] implements it.
type Exporter interface {
	Export(ctx context.Context, endpoint string, resourceLogs []*logspb.ResourceLogs) error
}

// Executor runs outsystems_logs log types.  Use [NewExecutor].  It is safe
// for concurrent use; the Runner never runs two calls for the same target of
// a collector at once.
type Executor struct {
	// Source runs the queries.
	Source Source
	// Catalog holds the built-in query of each log type.
	Catalog catalogoutsystems.Catalog
	// Store keeps the watermarks.
	Store *Store
	// Exporter sends the logs.
	Exporter Exporter
	// Metrics records the extraction self-metrics; nil records nothing.
	Metrics *Metrics
	// Logger receives watermark warnings.  Nil means [slog.Default].
	Logger *slog.Logger
	// MaxBatches bounds the batches of one call; below 1 means
	// DefaultMaxBatches.
	MaxBatches int

	// now is the clock; tests replace it.
	now func() time.Time

	locationsMu sync.Mutex
	locations   map[string]*time.Location
}

// NewExecutor returns an Executor with the built-in queries.
func NewExecutor(source Source, store *Store, exporter Exporter, metrics *Metrics, logger *slog.Logger) *Executor {
	return &Executor{
		Source:   source,
		Catalog:  catalogoutsystems.DefaultCatalog(),
		Store:    store,
		Exporter: exporter,
		Metrics:  metrics,
		Logger:   logger,
		now:      time.Now,
	}
}

// RunProbe implements collectors.ProbeExecutor.  item.Definition.Name is the
// log type.  It exports no samples: the extraction metrics live in Metrics,
// so they survive the failed cycles they count.
func (e *Executor) RunProbe(ctx context.Context, item collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error) {
	logType := item.Definition.Name
	builtin, ok := e.Catalog.Get(logType)
	if !ok {
		return nil, nil, fmt.Errorf("unknown OutSystems log type %q", logType)
	}
	query := builtin.SQL
	if item.Definition.QueryTemplate != "" {
		query = item.Definition.QueryTemplate
	}
	location, err := e.location(item.OutSystemsLogs.LogTimezone)
	if err != nil {
		return nil, nil, err
	}
	key := WatermarkKey{Collector: item.CollectorID, Target: item.Target.Name, LogType: logType}
	watermark := e.watermark(key, item, location)
	for batch := 1; ; batch++ {
		started := time.Now()
		full, err := e.extractBatch(ctx, item, key, query, location, &watermark)
		if err != nil {
			return nil, nil, err
		}
		if !full || batch >= e.maxBatches() || !timeLeft(ctx, time.Since(started)) {
			return nil, nil, nil
		}
	}
}

// extractBatch reads, delivers and acknowledges one batch, moving watermark.
// full reports a batch of batch_limit rows, after which more rows may wait.
func (e *Executor) extractBatch(ctx context.Context, item collectormetadata.ScheduledProbe, key WatermarkKey, query string, location *time.Location, watermark *Watermark) (full bool, err error) {
	settings := item.OutSystemsLogs
	now := e.clock()
	lag := settings.Lag
	if item.Definition.Lag > 0 {
		lag = item.Definition.Lag
	}
	// Whole seconds, so a datetime column's millisecond rounding can never
	// put a row on the other side of a bound (see checkContract).
	to := Wall(now.Add(-lag), location).Truncate(time.Second)
	if !to.After(watermark.Instant) {
		e.Metrics.setLag(item, now.Sub(Real(watermark.Instant, location)))
		return false, nil
	}
	batch, err := e.Source.Query(ctx, item.Target, query, QueryParams{
		From: watermark.Instant, To: to, Limit: settings.BatchLimit, DateTime: watermark.InstantType == "DATETIME",
	})
	if err != nil {
		return false, fmt.Errorf("log type %s: %w", item.Definition.Name, err)
	}
	instants, instantType, err := checkContract(batch, watermark.Instant, to, settings.BatchLimit)
	if err != nil {
		return false, fmt.Errorf("log type %s: %w", item.Definition.Name, err)
	}
	rows, next := batch.Rows, to
	if len(rows) == settings.BatchLimit {
		full = true
		last := instants[len(instants)-1]
		cut := slices.IndexFunc(instants, func(t time.Time) bool { return t.Equal(last) })
		if cut == 0 {
			e.Metrics.batchStuck(item)
			return false, fmt.Errorf("log type %s: %w (%d rows at %s): raise batch_limit above %d so they fit in one batch",
				item.Definition.Name, ErrBatchStuck, len(rows), last.Format(wallLayout), settings.BatchLimit)
		}
		rows, instants = rows[:cut], instants[:cut]
		next = instants[cut-1]
	}
	if len(rows) > 0 {
		logs := buildLogs(batch, rows, instants, recordContext{
			collector:   item.CollectorID,
			target:      item.Target.Name,
			environment: item.Target.EnvironmentSlug,
			logType:     item.Definition.Name,
			location:    location,
			observed:    now,
		})
		if err := e.Exporter.Export(ctx, settings.LogsEndpoint, logs); err != nil {
			e.Metrics.exportFailed(item)
			return false, fmt.Errorf("log type %s: %w", item.Definition.Name, err)
		}
		e.Metrics.addRows(item, len(rows))
		e.Metrics.setLogDelay(item, now.Sub(Real(instants[len(instants)-1], location)))
	}
	*watermark = Watermark{Instant: next, InstantType: instantType, Source: watermark.Source}
	e.Metrics.setLag(item, now.Sub(Real(next, location)))
	if err := e.Store.Save(key, *watermark, now); err != nil {
		return false, fmt.Errorf("log type %s: rows delivered, but the watermark is only kept in memory: %w", item.Definition.Name, err)
	}
	return full, nil
}

// watermark returns the stored watermark of key, or, when there is none,
// it is unreadable, or it belongs to another database, a new one at now −
// initial_lookback that is kept in memory until the first batch is saved.
func (e *Executor) watermark(key WatermarkKey, item collectormetadata.ScheduledProbe, location *time.Location) Watermark {
	source := fmt.Sprintf("%s:%d/%s", item.Target.Host, item.Target.Port, item.Target.DatabaseName)
	stored, found, err := e.Store.Load(key)
	attrs := []any{"collector", key.Collector, "target", key.Target, "log_type", key.LogType, "initial_lookback", item.OutSystemsLogs.InitialLookback.String()}
	switch {
	case err != nil:
		e.logger().Warn("outsystems_logs watermark file is unreadable; starting from initial_lookback", append(attrs, "error", err)...)
	case !found:
		e.logger().Warn("outsystems_logs has no watermark for this log type; starting from initial_lookback", append(attrs, "state_dir", e.Store.Dir())...)
	case stored.Source != source:
		e.logger().Warn("outsystems_logs watermark belongs to another database; starting from initial_lookback", append(attrs, "previous_source", stored.Source, "source", source)...)
	default:
		return stored
	}
	start := Watermark{
		Instant: Wall(e.clock().Add(-item.OutSystemsLogs.InitialLookback), location).Truncate(time.Second),
		Source:  source,
	}
	e.Store.Remember(key, start)
	return start
}

// checkContract checks a batch against the query contract and returns each
// row's Instant as a wall clock and the Instant column's database type.
// from and to are whole seconds or Instant values read before, so a datetime
// Instant, which the driver rounds to milliseconds, compares to them as it
// does in SQL Server.
func checkContract(batch Batch, from, to time.Time, limit int) ([]time.Time, string, error) {
	column := slices.IndexFunc(batch.Columns, func(c Column) bool { return c.Name == columnInstant })
	if column < 0 {
		return nil, "", fmt.Errorf("%w: no column named exactly %s", ErrContract, columnInstant)
	}
	if len(batch.Rows) > limit {
		return nil, "", fmt.Errorf("%w: more than @limit (%d) rows; use SELECT TOP (@limit)", ErrContract, limit)
	}
	databaseType := batch.Columns[column].DatabaseType
	if !slices.Contains([]string{"DATETIME", "DATETIME2", "SMALLDATETIME"}, databaseType) {
		return nil, "", fmt.Errorf("%w: %s is %s, want datetime or datetime2", ErrContract, columnInstant, databaseType)
	}
	instants := make([]time.Time, len(batch.Rows))
	for i, row := range batch.Rows {
		instant, ok := row[column].(time.Time)
		if !ok {
			return nil, "", fmt.Errorf("%w: row %d has %s %v, want a datetime", ErrContract, i+1, columnInstant, row[column])
		}
		switch {
		case !instant.After(from) || instant.After(to):
			return nil, "", fmt.Errorf("%w: row %d has %s %s outside (@from, @to] = (%s, %s]; filter with Instant > @from AND Instant <= @to",
				ErrContract, i+1, columnInstant, instant.Format(wallLayout), from.Format(wallLayout), to.Format(wallLayout))
		case i > 0 && instant.Before(instants[i-1]):
			return nil, "", fmt.Errorf("%w: row %d is out of order (%s after %s); use ORDER BY Instant",
				ErrContract, i+1, instant.Format(wallLayout), instants[i-1].Format(wallLayout))
		}
		instants[i] = instant
	}
	return instants, databaseType, nil
}

// InitCollector implements collectors.CollectorObserver: it creates the
// collector's counters at 0.
func (e *Executor) InitCollector(collector collectorconfig.CollectorRuntimeConfig) {
	e.Metrics.initCollector(collector)
}

// ForgetCollector implements collectors.CollectorObserver: it deletes the
// collector's series.  Its watermarks stay, so a collector that is changed or
// re-added resumes where it stopped.
func (e *Executor) ForgetCollector(collectorID string) {
	e.Metrics.forgetCollector(collectorID)
}

// TargetSysadmin implements collectors.SysadminReporter when Source can
// tell.
func (e *Executor) TargetSysadmin(target collectormetadata.DatabaseTarget) (sysadmin, ok bool) {
	reporter, ok := e.Source.(interface {
		TargetSysadmin(collectormetadata.DatabaseTarget) (bool, bool)
	})
	if !ok {
		return false, false
	}
	return reporter.TargetSysadmin(target)
}

// location returns the time zone name, loaded once.  Config validation has
// already checked the name.
func (e *Executor) location(name string) (*time.Location, error) {
	e.locationsMu.Lock()
	defer e.locationsMu.Unlock()
	if location, ok := e.locations[name]; ok {
		return location, nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("log_timezone: %w", err)
	}
	if e.locations == nil {
		e.locations = map[string]*time.Location{}
	}
	e.locations[name] = location
	return location, nil
}

func (e *Executor) clock() time.Time {
	if e.now == nil {
		return time.Now()
	}
	return e.now()
}

func (e *Executor) maxBatches() int {
	if e.MaxBatches < 1 {
		return DefaultMaxBatches
	}
	return e.MaxBatches
}

func (e *Executor) logger() *slog.Logger {
	if e.Logger == nil {
		return slog.Default()
	}
	return e.Logger
}

// timeLeft reports whether ctx's deadline leaves room for another batch that
// takes twice as long as the last one.
func timeLeft(ctx context.Context, last time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > 2*last
}

// Wall returns the wall clock of t in location, as a UTC time: the form the
// driver reads datetime and datetime2 values in, and the form watermarks and
// query bounds are compared in.
func Wall(t time.Time, location *time.Location) time.Time {
	local := t.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
}

// Real returns the instant a wall clock in location stands for.  In the hour
// a daylight saving change repeats, Go picks the first occurrence.
func Real(wall time.Time, location *time.Location) time.Time {
	return time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), location)
}
