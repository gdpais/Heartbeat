package sqlserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	collectorexport "heartbeat/services/db-collector/internal/export"
)

// repoRoot is the repository root relative to this package's directory.
const repoRoot = "../../../../.."

// queryFilePatterns are the Prometheus rules and Grafana dashboards that
// query collector metrics.
var queryFilePatterns = []string{
	"infra/helm/heartbeat/files/prometheus/rules/*.yml",
	"infra/helm/heartbeat/files/prometheus/rules/generated/*.yml",
	"infra/helm/heartbeat/files/grafana/dashboards/*.json",
}

// referenceFilePatterns are every file that names collector metrics: the
// query files, the rule tests, the end-to-end checks, and the docs that list
// metrics.
var referenceFilePatterns = append([]string{
	"infra/helm/heartbeat/files/prometheus/rules/tests/*.yml",
	"scripts/*.sh",
	"docs/reference/*.md",
	"docs/architecture/*.md",
	"services/db-collector/README.md",
}, queryFilePatterns...)

var metricRef = regexp.MustCompile(`\bheartbeat_sqlserver_[a-z0-9_]+`)

// globFiles returns the files matching patterns under the repository root.
func globFiles(t *testing.T, patterns []string) []string {
	t.Helper()
	var files []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(repoRoot, pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	// Moving these files must update the patterns, not silently skip the check.
	if len(files) < len(patterns) {
		t.Fatalf("found only %d files for %d patterns; update the patterns if they moved", len(files), len(patterns))
	}
	return files
}

// catalogMetrics returns every metric the default catalog emits, by name.
func catalogMetrics() map[string]Metric {
	emitted := map[string]Metric{}
	for _, probe := range DefaultCatalog().byName {
		for _, metric := range probe.Metrics {
			emitted[metric.Name] = metric
		}
	}
	return emitted
}

// Prometheus rules, Grafana dashboards, the end-to-end checks and the docs
// name collector metrics, so a renamed probe metric silently leaves them
// empty or wrong. Every SQL Server metric they reference must exist in the
// default catalog.
func TestRulesAndDashboardsReferenceCatalogMetrics(t *testing.T) {
	emitted := catalogMetrics()
	for _, file := range globFiles(t, referenceFilePatterns) {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		unknown := map[string]bool{}
		for _, name := range metricRef.FindAllString(string(content), -1) {
			if _, ok := emitted[name]; !ok {
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

// A raw counter only grows until SQL Server restarts, so rules and dashboards
// must read counters through rate() or increase(), which also handle the
// reset on restart.
func TestQueriesReadCountersThroughRate(t *testing.T) {
	emitted := catalogMetrics()
	rateCall := regexp.MustCompile(`\b(rate|irate|increase)\(\s*$`)
	for _, file := range globFiles(t, queryFilePatterns) {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		for _, loc := range metricRef.FindAllStringIndex(text, -1) {
			name := text[loc[0]:loc[1]]
			if emitted[name].Type != collectorexport.Counter {
				continue
			}
			if !rateCall.MatchString(text[:loc[0]]) {
				rel, _ := filepath.Rel(repoRoot, file)
				line := strings.Count(text[:loc[0]], "\n") + 1
				t.Errorf("%s:%d: counter %s is not read through rate() or increase()", rel, line, name)
			}
		}
	}
}
