package app

import (
	"context"
	"runtime"
	"testing"
	"time"

	heartbeatconfig "heartbeat/internal/config"
	"heartbeat/services/db-collector/internal/collectors"
	collectorexport "heartbeat/services/db-collector/internal/export"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

// noopExecutor is a ProbeExecutor whose probes succeed with no samples.
type noopExecutor struct{}

func (noopExecutor) RunProbe(context.Context, collectormetadata.ScheduledProbe) ([]collectorexport.Sample, []collectormetadata.Evidence, error) {
	return nil, nil, nil
}

// The collector's own health must be visible on /metrics: Go runtime and
// process metrics, and the per-probe metrics the Runner records.
func TestNewRegistryExposesRuntimeProcessAndProbeMetrics(t *testing.T) {
	registry, probeMetrics, err := newRegistry()
	if err != nil {
		t.Fatalf("newRegistry: %v", err)
	}
	runner := collectors.NewRunner(noopExecutor{}, collectorexport.NewPrometheusExporter(registry), nil).WithProbeMetrics(probeMetrics)
	collector := heartbeatconfig.CollectorRuntimeConfig{
		ID:             "sql-a",
		Kind:           "sqlserver",
		Enabled:        true,
		ScrapeInterval: time.Minute,
		Targets: []heartbeatconfig.TargetRuntimeConfig{{
			Name:            "db",
			EnvironmentSlug: "prod",
			Probes:          []heartbeatconfig.ProbeRuntimeConfig{{Name: "waits"}},
		}},
	}
	if _, err := runner.RunOnce(context.Background(), collector); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	got := map[string]bool{}
	for _, family := range families {
		got[family.GetName()] = true
	}
	want := []string{"go_goroutines", "go_memstats_heap_alloc_bytes", collectors.MetricProbeDuration, collectors.MetricCycleDuration}
	// The process collector reads /proc on Linux and the kernel on macOS;
	// elsewhere it exports nothing.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		want = append(want, "process_cpu_seconds_total", "process_resident_memory_bytes", "process_start_time_seconds")
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("registry does not export %s", name)
		}
	}
}
