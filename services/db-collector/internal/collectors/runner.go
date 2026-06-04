package collectors

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	collectorconfig "heartbeat/services/db-collector/internal/config"
	connector "heartbeat/services/db-collector/internal/connectors/sqlserver"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

type Repository interface {
	ListScheduledProbes(context.Context, collectormetadata.QueryFilter) ([]collectormetadata.ScheduledProbe, error)
}

type ProbeExecutor interface {
	RunProbe(context.Context, collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error)
}

type EvidenceSink interface {
	Publish([]collectormetadata.Evidence) error
}

type Runner struct {
	repo     Repository
	executor ProbeExecutor
	exporter collectorexport.Recorder
	sink     EvidenceSink
}

func NewRunner(repo Repository, executor ProbeExecutor, exporter collectorexport.Recorder, sink EvidenceSink) Runner {
	return Runner{repo: repo, executor: executor, exporter: exporter, sink: sink}
}

func (r Runner) RunOnce(ctx context.Context, collector collectorconfig.CollectorRuntimeConfig) error {
	if !collector.Enabled {
		return nil
	}
	items, err := r.repo.ListScheduledProbes(ctx, collectormetadata.QueryFilter{
		Engine:          collector.Kind,
		CollectorID:     collector.ID,
		EnvironmentSlug: collector.Environment,
		TargetNames:     collector.TargetNames,
	})
	if err != nil {
		return err
	}
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
	query := item.Definition.QueryTemplate
	if query == "" {
		if probe, ok := e.Catalog.Get(item.Definition.Name); ok {
			query = probe.QueryTemplate
		}
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
	return decodeRows(item, maps), buildEvidence(item, maps), nil
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

func decodeRows(item collectormetadata.ScheduledProbe, rows []map[string]any) []collectorexport.Sample {
	var samples []collectorexport.Sample
	for _, row := range rows {
		labels := map[string]string{
			"environment": item.Target.EnvironmentSlug,
			"target":      item.Target.Name,
		}
		stringKeys := sortedStringKeys(row)
		for _, key := range stringKeys {
			if key == "metric" || key == "metric_name" {
				continue
			}
			labels[key] = fmt.Sprint(row[key])
		}
		for key, value := range row {
			metricValue, ok := toFloat64(value)
			if !ok {
				continue
			}
			metricName := fmt.Sprintf("heartbeat_sqlserver_%s_%s", sanitize(item.Definition.Category), sanitize(key))
			samples = append(samples, collectorexport.Sample{Metric: metricName, Value: metricValue, Labels: labels})
		}
	}
	return samples
}

func buildEvidence(item collectormetadata.ScheduledProbe, rows []map[string]any) []collectormetadata.Evidence {
	if item.Definition.Category != "blocking" && item.Definition.Category != "sessions" {
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	return []collectormetadata.Evidence{{
		Kind:  item.Definition.Category,
		Title: fmt.Sprintf("%s snapshot for %s", item.Definition.Category, item.Target.Name),
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

func sanitize(value string) string {
	replacer := strings.NewReplacer(" ", "_", "/", "_", "-", "_", ".", "_")
	return strings.ToLower(replacer.Replace(value))
}

func sortedStringKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for key, value := range row {
		if _, ok := toFloat64(value); ok {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type Poller struct {
	Runner    Runner
	Collector collectorconfig.CollectorRuntimeConfig
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
