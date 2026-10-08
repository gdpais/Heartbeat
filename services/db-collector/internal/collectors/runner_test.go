package collectors

import (
	"context"
	"errors"
	"log/slog"
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

// backoffScenario drives one target with a healthy "throughput" probe and a
// "blocking" probe that fails until healthy is set, through cycles on a fake
// clock with a 10s interval and no jitter: failed at 0s and 10s (backoff until
// 20s), skipped at 15s.
type backoffScenario struct {
	t        *testing.T
	exporter *collectorexport.InMemoryExporter
	runner   Runner
	tracker  *targetTracker
	clock    *fakeClock
	t0       time.Time
	healthy  bool
}

var errDivideByZero = errors.New("mssql: Divide by zero error encountered")

func newBackoffScenario(t *testing.T) *backoffScenario {
	s := &backoffScenario{
		t:        t,
		exporter: collectorexport.NewInMemoryExporter(),
		clock:    &fakeClock{},
		tracker:  newTargetTracker(),
		t0:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	executor := funcExecutor(func(_ context.Context, item collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error) {
		if item.Definition.Name == "blocking" && !s.healthy {
			return nil, nil, errDivideByZero
		}
		return sampleFor(item), nil, nil
	})
	s.runner = NewRunner(executor, s.exporter, nil).WithLogger(slog.New(slog.DiscardHandler))
	s.tracker.now = s.clock.Now
	s.tracker.jitter = func() float64 { return 0 }
	return s
}

// cycle runs one cycle at offset from t0 and returns the target's result
// after checking its state.
func (s *backoffScenario) cycle(offset time.Duration, want TargetState) TargetResult {
	s.t.Helper()
	s.clock.Set(s.t0.Add(offset))
	collector := testCollector(10*time.Second, testTarget("core-db", "throughput", "blocking"))
	result := s.runner.runCycle(context.Background(), collector, s.tracker)
	if len(result.Targets) != 1 || result.Targets[0].State != want {
		s.t.Fatalf("at %s: expected one %s target, got %+v", offset, want, result.Targets)
	}
	return result.Targets[0]
}

// healthyProbeExported reports whether the healthy probe's series is exported.
func (s *backoffScenario) healthyProbeExported() bool {
	_, ok := s.exporter.Value(testMetric, sampleLabels("core-db", "throughput"))
	return ok
}

// A target backing off runs no probes, so it must not keep exporting the last
// values of the probes that succeeded in its failed cycle: they would stay
// frozen for up to the backoff cap.  Its health series stay.
func TestBackoffClearsSeriesOfHealthyProbes(t *testing.T) {
	s := newBackoffScenario(t)
	s.cycle(0, TargetFailed)
	s.cycle(10*time.Second, TargetFailed)
	if !s.healthyProbeExported() {
		t.Fatalf("expected the healthy probe's fresh sample in a failed cycle")
	}

	s.cycle(15*time.Second, TargetBackoff)
	if s.healthyProbeExported() {
		t.Fatalf("healthy probe's sample stayed exported during backoff")
	}
	if up, ok := s.exporter.Value(MetricTargetUp, healthLabels("core-db")); !ok || up != 0 {
		t.Fatalf("expected target_up=0 during backoff, got %v (present %v)", up, ok)
	}
	if failures, ok := s.exporter.Value(MetricTargetConsecutiveFailures, healthLabels("core-db")); !ok || failures != 2 {
		t.Fatalf("expected consecutive_failures=2 during backoff, got %v (present %v)", failures, ok)
	}

	s.healthy = true
	s.cycle(20*time.Second, TargetOK)
	if !s.healthyProbeExported() {
		t.Fatalf("expected the healthy probe's sample back after recovery")
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
		{
			// Percent from the scheduler monitor record becomes a 0-1 ratio.
			probe: "cpu",
			rows:  []map[string]any{{"sql_process_percent": int64(25), "other_process_percent": int64(10)}},
			want: []want{
				{"heartbeat_sqlserver_cpu_sql_process_ratio", collectorexport.Gauge, 0.25, ""},
				{"heartbeat_sqlserver_cpu_other_process_ratio", collectorexport.Gauge, 0.1, ""},
			},
		},
		{
			// The driver returns the float division as float64.
			probe: "buffer_cache",
			rows:  []map[string]any{{"page_life_expectancy_seconds": int64(3600), "buffer_cache_hit_ratio": 0.995}},
			want: []want{
				{"heartbeat_sqlserver_page_life_expectancy_seconds", collectorexport.Gauge, 3600, ""},
				{"heartbeat_sqlserver_buffer_cache_hit_ratio", collectorexport.Gauge, 0.995, ""},
			},
		},
		{
			// A zero hit ratio base is NULL after NULLIF: no sample rather
			// than a division by zero.
			probe: "buffer_cache",
			rows:  []map[string]any{{"page_life_expectancy_seconds": int64(3600), "buffer_cache_hit_ratio": nil}},
			want:  []want{{"heartbeat_sqlserver_page_life_expectancy_seconds", collectorexport.Gauge, 3600, ""}},
		},
		{
			// One file row yields six counters, stall time in seconds.
			probe: "file_io",
			rows: []map[string]any{{
				"database_name": "sales", "file_name": "sales_log", "file_type": "LOG",
				"num_of_reads": int64(10), "num_of_writes": int64(400),
				"num_of_bytes_read": int64(81920), "num_of_bytes_written": int64(1 << 20),
				"io_stall_read_ms": int64(30), "io_stall_write_ms": int64(1500),
			}},
			want: []want{
				{"heartbeat_sqlserver_database_file_reads_total", collectorexport.Counter, 10, "file_name=sales_log"},
				{"heartbeat_sqlserver_database_file_writes_total", collectorexport.Counter, 400, "file_type=LOG"},
				{"heartbeat_sqlserver_database_file_read_bytes_total", collectorexport.Counter, 81920, "database_name=sales"},
				{"heartbeat_sqlserver_database_file_written_bytes_total", collectorexport.Counter, 1 << 20, "database_name=sales"},
				{"heartbeat_sqlserver_database_file_read_stall_seconds_total", collectorexport.Counter, 0.03, "database_name=sales"},
				{"heartbeat_sqlserver_database_file_write_stall_seconds_total", collectorexport.Counter, 1.5, "database_name=sales"},
			},
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
