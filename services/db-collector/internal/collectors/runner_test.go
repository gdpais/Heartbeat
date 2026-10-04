package collectors

import (
	"context"
	"strings"
	"testing"
	"time"

	collectorconfig "heartbeat/internal/config"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
	catalogsqlserver "heartbeat/services/db-collector/internal/probes/sqlserver"
)

type fakeExecutor struct{}

func (fakeExecutor) RunProbe(context.Context, collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error) {
	return []collectorexport.Sample{{
		Metric: "heartbeat_sqlserver_wait_seconds_total",
		Type:   collectorexport.Counter,
		Value:  12,
		Labels: map[string]string{"environment": "prod", "target": "core-db", "wait_type": "LCK_M_X"},
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

	result, err := runner.RunOnce(context.Background(), collectorconfig.CollectorRuntimeConfig{
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
	if !result.Healthy() || len(result.Targets) != 1 || result.Targets[0].State != TargetOK {
		t.Fatalf("unexpected cycle result: %+v", result)
	}

	if got := exporter.LastValue("heartbeat_sqlserver_wait_seconds_total"); got != 12 {
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
			Name:         "heartbeat_sqlserver_wait_seconds_total",
			Help:         "Wait time.",
			Type:         collectorexport.Counter,
			ValueColumn:  "wait_time_ms",
			Scale:        0.001,
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
	if sample.Metric != "heartbeat_sqlserver_wait_seconds_total" || sample.Type != collectorexport.Counter {
		t.Fatalf("unexpected metric: %s (%s)", sample.Metric, sample.Type)
	}
	if sample.Value != 0.042 {
		t.Fatalf("unexpected value: %v", sample.Value)
	}
	if sample.Labels["wait_type"] != "LCK_M_X" {
		t.Fatalf("unexpected wait_type label: %s", sample.Labels["wait_type"])
	}
	if _, ok := sample.Labels["ignored_label"]; ok {
		t.Fatalf("unexpected ignored_label in labels")
	}
}

func TestStorageProbeEmitsOneSeriesPerFile(t *testing.T) {
	probe, ok := catalogsqlserver.DefaultCatalog().Get("storage")
	if !ok {
		t.Fatal("storage probe missing")
	}
	item := collectormetadata.ScheduledProbe{
		Target: collectormetadata.DatabaseTarget{Name: "core-db", EnvironmentSlug: "prod"},
	}
	// A database has at least a data and a log file; both share database_name.
	rows := []map[string]any{
		{"database_name": "sales", "file_name": "sales", "file_type": "ROWS", "size_pages": int64(65536)},
		{"database_name": "sales", "file_name": "sales_log", "file_type": "LOG", "size_pages": int64(8192)},
	}
	samples := decodeRows(item, probe, rows)
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	exporter := collectorexport.NewInMemoryExporter()
	if err := exporter.RecordScope(collectorexport.Scope{Collector: "c", Target: "core-db", Probe: "storage"}, samples); err != nil {
		t.Fatalf("files of one database must not collide: %v", err)
	}
}

// Each catalog probe decodes rows shaped like its query's result into
// samples in base units with the right type.  The live SQL Server test checks
// the queries themselves.
func TestCatalogProbesDecodeToBaseUnitsAndTypes(t *testing.T) {
	type want struct {
		metric     string
		metricType collectorexport.MetricType
		value      float64
		label      string // label=value beyond environment and target, if any
	}
	tests := []struct {
		probe string
		rows  []map[string]any
		want  []want
	}{
		{
			probe: "waits",
			rows:  []map[string]any{{"wait_type": "LCK_M_X", "wait_time_ms": int64(2500)}},
			want:  []want{{"heartbeat_sqlserver_wait_seconds_total", collectorexport.Counter, 2.5, "wait_type=LCK_M_X"}},
		},
		{
			probe: "memory_pressure",
			rows:  []map[string]any{{"total_server_memory_kb": int64(2048)}},
			want:  []want{{"heartbeat_sqlserver_total_server_memory_bytes", collectorexport.Gauge, 2 << 20, ""}},
		},
		{
			probe: "storage",
			rows:  []map[string]any{{"database_name": "sales", "file_name": "sales", "file_type": "ROWS", "size_pages": int64(128)}},
			want:  []want{{"heartbeat_sqlserver_database_file_size_bytes", collectorexport.Gauge, 1 << 20, "database_name=sales"}},
		},
		{
			// One pivoted row yields one counter per column.
			probe: "throughput",
			rows:  []map[string]any{{"batch_requests": int64(1200), "transactions": int64(300)}},
			want: []want{
				{"heartbeat_sqlserver_batch_requests_total", collectorexport.Counter, 1200, ""},
				{"heartbeat_sqlserver_transactions_total", collectorexport.Counter, 300, ""},
			},
		},
		{
			// A counter missing on the server is NULL after the pivot: no
			// sample rather than a fake 0.
			probe: "throughput",
			rows:  []map[string]any{{"batch_requests": int64(1200), "transactions": nil}},
			want:  []want{{"heartbeat_sqlserver_batch_requests_total", collectorexport.Counter, 1200, ""}},
		},
	}
	item := collectormetadata.ScheduledProbe{Target: collectormetadata.DatabaseTarget{Name: "core-db", EnvironmentSlug: "prod"}}
	for _, tt := range tests {
		t.Run(tt.probe, func(t *testing.T) {
			probe, ok := catalogsqlserver.DefaultCatalog().Get(tt.probe)
			if !ok {
				t.Fatalf("probe %s missing", tt.probe)
			}
			samples := decodeRows(item, probe, tt.rows)
			if len(samples) != len(tt.want) {
				t.Fatalf("expected %d samples, got %+v", len(tt.want), samples)
			}
			for i, w := range tt.want {
				got := samples[i]
				if got.Metric != w.metric || got.Type != w.metricType || got.Value != w.value {
					t.Errorf("sample %d = %s %s %v, want %s %s %v", i, got.Metric, got.Type, got.Value, w.metric, w.metricType, w.value)
				}
				if w.label != "" {
					name, value, _ := strings.Cut(w.label, "=")
					if got.Labels[name] != value {
						t.Errorf("sample %d label %s = %q, want %q", i, name, got.Labels[name], value)
					}
				}
				if got.Labels["environment"] != "prod" || got.Labels["target"] != "core-db" {
					t.Errorf("sample %d lacks environment/target labels: %v", i, got.Labels)
				}
			}
		})
	}
}
