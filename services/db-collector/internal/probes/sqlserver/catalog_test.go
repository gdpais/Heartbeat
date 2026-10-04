package sqlserver

import (
	"regexp"
	"strings"
	"testing"

	collectorexport "heartbeat/services/db-collector/internal/export"
)

func TestCatalogContainsRequiredProbes(t *testing.T) {
	catalog := DefaultCatalog()
	required := []string{"waits", "blocking", "sessions", "memory_pressure", "storage", "throughput"}
	for _, name := range required {
		probe, ok := catalog.Get(name)
		if !ok {
			t.Fatalf("missing required probe %q", name)
		}
		if probe.QueryTemplate == "" {
			t.Fatalf("probe %q has empty query template", name)
		}
		if len(probe.Metrics) == 0 {
			t.Fatalf("probe %q has no metric names", name)
		}
		for _, metric := range probe.Metrics {
			if metric.Name == "" {
				t.Fatalf("probe %q has metric with empty name", name)
			}
			if metric.ValueColumn == "" {
				t.Fatalf("probe %q metric %q has empty value column", name, metric.Name)
			}
		}
	}
}

// Renaming an exported metric breaks rules and dashboards, so names are
// checked against Prometheus conventions once, here: base units only, and
// _total on counters and nowhere else.
func TestCatalogMetricsFollowPrometheusConventions(t *testing.T) {
	validName := regexp.MustCompile(`^heartbeat_sqlserver_[a-z0-9_]+[a-z0-9]$`)
	nonBaseUnit := regexp.MustCompile(`_(ms|milliseconds|kb|mb|gb|kilobytes|megabytes|minutes|hours|percent)(_|$)`)
	seen := map[string]bool{}
	for _, probe := range DefaultCatalog().byName {
		for _, metric := range probe.Metrics {
			if seen[metric.Name] {
				t.Errorf("%s: emitted by more than one descriptor", metric.Name)
			}
			seen[metric.Name] = true
			if !validName.MatchString(metric.Name) {
				t.Errorf("%s: not a heartbeat_sqlserver_ snake_case name", metric.Name)
			}
			if nonBaseUnit.MatchString(metric.Name) {
				t.Errorf("%s: use base units (seconds, bytes, ratio) and Scale", metric.Name)
			}
			isTotal := strings.HasSuffix(metric.Name, "_total")
			switch metric.Type {
			case collectorexport.Counter:
				if !isTotal {
					t.Errorf("%s: counters must end in _total", metric.Name)
				}
			case collectorexport.Gauge:
				if isTotal {
					t.Errorf("%s: only counters end in _total", metric.Name)
				}
			default:
				t.Errorf("%s: unknown type %v", metric.Name, metric.Type)
			}
			if metric.Scale < 0 {
				t.Errorf("%s: negative scale %v", metric.Name, metric.Scale)
			}
			if metric.Help == "" {
				t.Errorf("%s: empty help", metric.Name)
			}
		}
	}
}

func TestMetricConvertAppliesScale(t *testing.T) {
	tests := []struct {
		name  string
		scale float64
		raw   float64
		want  float64
	}{
		{"zero scale is identity", 0, 42, 42},
		{"milliseconds to seconds", 0.001, 1500, 1.5},
		{"KB to bytes", 1024, 2, 2048},
		{"8 KB pages to bytes", 8192, 3, 24576},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Metric{Scale: tt.scale}).Convert(tt.raw); got != tt.want {
				t.Fatalf("Convert(%v) with scale %v = %v, want %v", tt.raw, tt.scale, got, tt.want)
			}
		})
	}
}
