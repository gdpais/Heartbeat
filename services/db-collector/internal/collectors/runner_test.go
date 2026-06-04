package collectors

import (
	"context"
	"testing"
	"time"

	collectorconfig "heartbeat/services/db-collector/internal/config"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

type fakeRepository struct {
	items []collectormetadata.ScheduledProbe
}

func (f fakeRepository) ListScheduledProbes(context.Context, collectormetadata.QueryFilter) ([]collectormetadata.ScheduledProbe, error) {
	return f.items, nil
}

type fakeExecutor struct{}

func (fakeExecutor) RunProbe(context.Context, collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error) {
	return []collectorexport.Sample{{
		Metric: "heartbeat_sqlserver_wait_seconds",
		Value:  12,
		Labels: map[string]string{"environment": "prod", "target": "core-db", "category": "lck_m_x"},
	}}, []collectormetadata.Evidence{{Title: "blocking snapshot", Kind: "blocking"}}, nil
}

type fakeSink struct {
	evidence []collectormetadata.Evidence
}

func (f *fakeSink) Publish(items []collectormetadata.Evidence) error {
	f.evidence = append(f.evidence, items...)
	return nil
}

func TestRunnerExecutesConfiguredCollector(t *testing.T) {
	exporter := collectorexport.NewInMemoryExporter()
	sink := &fakeSink{}
	runner := NewRunner(fakeRepository{items: []collectormetadata.ScheduledProbe{{
		CollectorID: "sql-prod",
		Target:      collectormetadata.DatabaseTarget{Name: "core-db", EnvironmentSlug: "prod", Engine: "sqlserver"},
		Definition:  collectormetadata.ProbeDefinition{Name: "waits", Category: "waits", TimeoutMS: 5000, QueryTemplate: "SELECT 1"},
		Assignment:  collectormetadata.ProbeAssignment{IntervalSeconds: 30},
	}}}, fakeExecutor{}, exporter, sink)

	err := runner.RunOnce(context.Background(), collectorconfig.CollectorRuntimeConfig{
		ID:             "sql-prod",
		Kind:           "sqlserver",
		Enabled:        true,
		CredentialRef:  "kv/sql-prod",
		ScrapeInterval: 30 * time.Second,
		Environment:    "prod",
	})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := exporter.LastValue("heartbeat_sqlserver_wait_seconds"); got != 12 {
		t.Fatalf("unexpected metric value: %v", got)
	}
	if len(sink.evidence) != 1 {
		t.Fatalf("expected 1 evidence item, got %d", len(sink.evidence))
	}
}
