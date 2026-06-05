package collectors

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	collectorconfig "heartbeat/services/db-collector/internal/config"
	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

type ProbeExecutor interface {
	RunProbe(context.Context, collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error)
}

type EvidenceSink interface {
	Publish([]collectormetadata.Evidence) error
}

type Runner struct {
	executor ProbeExecutor
	exporter collectorexport.Recorder
	sink     EvidenceSink
}

func NewRunner(executor ProbeExecutor, exporter collectorexport.Recorder, sink EvidenceSink) Runner {
	return Runner{executor: executor, exporter: exporter, sink: sink}
}

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

type LoggingEvidenceSink struct{}

func (LoggingEvidenceSink) Publish([]collectormetadata.Evidence) error { return nil }

type SQLExecutor struct {
	Manager connector.Manager
	Catalog catalogsqlserver.Catalog
}

func NewSQLExecutor(manager connector.Manager) SQLExecutor {
	return SQLExecutor{Manager: manager, Catalog: catalogsqlserver.DefaultCatalog()}
}

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
