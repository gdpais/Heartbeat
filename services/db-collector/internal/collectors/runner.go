// Package collectors provides the runtime execution layer for database probes.
//
// The central types are [Runner] and [Poller]:
//
//   - Runner executes a single scrape cycle for one collector: it iterates
//     every scheduled probe, calls [ProbeExecutor.RunProbe], and forwards the
//     resulting metrics samples to a [collectorexport.Recorder] and evidence to
//     an [EvidenceSink].
//
//   - Poller wraps a Runner and calls [Runner.RunOnce] on a fixed interval
//     driven by [CollectorRuntimeConfig.ScrapeInterval].  Poller.Start blocks
//     until the context is cancelled.
//
// SQLExecutor is the default [ProbeExecutor] implementation: it opens a live
// database connection via a [connector.Manager], looks up the probe in the
// [catalogsqlserver.Catalog], executes the SQL query, and decodes the result
// set into typed [collectorexport.Sample] values.
package collectors

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	collectorconfig "heartbeat/internal/config"
	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

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
// all scheduled probes, records the resulting metric samples, and forwards
// evidence to the configured sink.
type Runner struct {
	executor ProbeExecutor
	exporter collectorexport.Recorder
	sink     EvidenceSink
}

// NewRunner constructs a Runner wired to the given executor, exporter, and
// evidence sink.  sink may be nil; evidence is silently discarded in that case.
func NewRunner(executor ProbeExecutor, exporter collectorexport.Recorder, sink EvidenceSink) Runner {
	return Runner{executor: executor, exporter: exporter, sink: sink}
}

// RunOnce executes every scheduled probe for collector and records the
// aggregated samples.  It is a no-op when collector.Enabled is false.
// The first probe error aborts the cycle and is returned to the caller.
func (r Runner) RunOnce(ctx context.Context, collector collectorconfig.CollectorRuntimeConfig) error {
	if !collector.Enabled {
		return nil
	}
	items := scheduledProbes(collector)
	var allSamples []collectorexport.Sample
	var allEvidence []collectormetadata.Evidence
	for _, item := range items {
		samples, evidence, err := r.executor.RunProbe(ctx, item)
		if err != nil {
			return fmt.Errorf("run probe %s for %s: %w", item.Definition.Name, item.Target.Name, err)
		}
		allSamples = append(allSamples, samples...)
		allEvidence = append(allEvidence, evidence...)
	}
	if err := r.exporter.Record(allSamples); err != nil {
		return err
	}
	if r.sink != nil {
		if err := r.sink.Publish(allEvidence); err != nil {
			return err
		}
	}
	return nil
}

// LoggingEvidenceSink is a no-op [EvidenceSink] used in the default wiring.
// It discards all evidence without error.
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

func timeoutFor(item collectormetadata.ScheduledProbe) time.Duration {
	if item.Definition.TimeoutMS > 0 {
		return time.Duration(item.Definition.TimeoutMS) * time.Millisecond
	}
	if item.Assignment.IntervalSeconds > 0 {
		return time.Duration(item.Assignment.IntervalSeconds) * time.Second
	}
	return 5 * time.Second
}

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

func normalizeValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return v
	}
}

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
				Value:  metricValue,
				Labels: labels,
			})
		}
	}
	return samples
}

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

// Poller repeatedly calls Runner.RunOnce for a single collector on the
// interval defined by Collector.ScrapeInterval.
type Poller struct {
	Runner    Runner
	Collector collectorconfig.CollectorRuntimeConfig
}

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

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// Start runs an initial scrape immediately and then ticks at
// Collector.ScrapeInterval until ctx is cancelled.  It returns ctx.Err() on
// clean shutdown and a non-nil error if RunOnce fails.
func (p Poller) Start(ctx context.Context) error {
	ticker := time.NewTicker(p.Collector.ScrapeInterval)
	defer ticker.Stop()
	if err := p.Runner.RunOnce(ctx, p.Collector); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := p.Runner.RunOnce(ctx, p.Collector); err != nil {
				return err
			}
		}
	}
}
