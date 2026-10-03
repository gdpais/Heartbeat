package sqlserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// repoRoot is the repository root relative to this package's directory.
const repoRoot = "../../../../.."

// Prometheus rules and Grafana dashboards query collector metrics by name, so
// a renamed probe metric silently leaves them empty. Every SQL Server metric
// they reference must exist in the default catalog.
func TestRulesAndDashboardsReferenceCatalogMetrics(t *testing.T) {
	emitted := map[string]bool{}
	for _, probe := range DefaultCatalog().byName {
		for _, metric := range probe.Metrics {
			emitted[metric.Name] = true
		}
	}

	patterns := []string{
		"infra/helm/heartbeat/files/prometheus/rules/*.yml",
		"infra/helm/heartbeat/files/prometheus/rules/generated/*.yml",
		"infra/helm/heartbeat/files/prometheus/rules/tests/*.yml",
		"infra/helm/heartbeat/files/grafana/dashboards/*.json",
	}
	var files []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(repoRoot, pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	// Moving these files must update the patterns, not silently skip the check.
	if len(files) < 3 {
		t.Fatalf("found only %d rule/dashboard files; update the patterns if they moved", len(files))
	}

	metricRef := regexp.MustCompile(`\bheartbeat_sqlserver_[a-z0-9_]+`)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		unknown := map[string]bool{}
		for _, name := range metricRef.FindAllString(string(content), -1) {
			if !emitted[name] {
				unknown[name] = true
			}
		}
		if len(unknown) > 0 {
			names := make([]string, 0, len(unknown))
			for name := range unknown {
				names = append(names, name)
			}
			sort.Strings(names)
			rel, _ := filepath.Rel(repoRoot, file)
			t.Errorf("%s references metrics the collector does not emit: %v", rel, names)
		}
	}
}
