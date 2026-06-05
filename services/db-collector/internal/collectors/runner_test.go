package collectors

import (
	"context"
	"testing"
	"time"

	collectorconfig "heartbeat/services/db-collector/internal/config"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

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
	runner := NewRunner(fakeExecutor{}, exporter, sink)

	err := runner.RunOnce(context.Background(), collectorconfig.CollectorRuntimeConfig{
		ID:             "sql-prod",
		Kind:           "sqlserver",
		Enabled:        true,
		CredentialRef:  "kv/sql-prod",
		ScrapeInterval: 30 * time.Second,
		Environment:    "prod",
		Targets: []collectorconfig.TargetRuntimeConfig{{
			Name:            "core-db",
			EnvironmentSlug: "prod",
			Engine:          "sqlserver",
			Probes: []collectorconfig.ProbeRuntimeConfig{{
				Name:          "waits",
				QueryTemplate: "SELECT 1",
				TimeoutMS:     5000,
			}},
		}},
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

func TestDecodeRowsUsesExplicitMetricDescriptors(t *testing.T) {
	item := collectormetadata.ScheduledProbe{
		Target: collectormetadata.DatabaseTarget{Name: "core-db", EnvironmentSlug: "prod"},
	}
	probe := catalogsqlserver.Probe{
		Metrics: []catalogsqlserver.Metric{{
			Name:         "heartbeat_sqlserver_wait_time_ms",
			Help:         "Wait time.",
			ValueColumn:  "wait_time_ms",
			LabelColumns: []string{"wait_type"},
		}},
	}
	rows := []map[string]any{{
		"wait_type":       "LCK_M_X",
		"wait_time_ms":    int64(42),
		"ignored_numeric": int64(99),
		"ignored_label":   "high-cardinality-value",
	}}

	samples := decodeRows(item, probe, rows)
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	sample := samples[0]
	if sample.Metric != "heartbeat_sqlserver_wait_time_ms" {
		t.Fatalf("unexpected metric: %s", sample.Metric)
	}
	if sample.Value != 42 {
		t.Fatalf("unexpected value: %v", sample.Value)
	}
	if sample.Labels["wait_type"] != "LCK_M_X" {
		t.Fatalf("unexpected wait_type label: %s", sample.Labels["wait_type"])
	}
	if _, ok := sample.Labels["ignored_label"]; ok {
		t.Fatalf("unexpected ignored_label in labels")
	}
}
