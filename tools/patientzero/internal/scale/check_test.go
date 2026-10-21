package scale

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const dashboardDir = "../../../../infra/helm/heartbeat/files/grafana/dashboards"

func TestLoadPanelsResolvesTheChartsDashboards(t *testing.T) {
	panels, err := LoadPanels(dashboardDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(panels) != len(CheckedPanels) {
		t.Fatalf("got %d panels, want %d", len(panels), len(CheckedPanels))
	}
	for _, p := range panels {
		q, err := p.Resolve("scale-20261021t134400z", 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(q, `deployment_environment_name=~"scale-20261021t134400z"`) {
			t.Errorf("%s: environment not set in %s", p.Title, q)
		}
		if d := Distinct(q); !strings.HasPrefix(d, "count(") {
			t.Errorf("%s: %s is not a distinct count", p.Title, d)
		}
	}
}

func TestDistinctCountsATopListsInnerAggregation(t *testing.T) {
	got := Distinct(`topk(20, sum by (a, b) (count_over_time({x="y"} [1h])))`)
	if want := `count(sum by (a, b) (count_over_time({x="y"} [1h])))`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if q := `count(sum by (a) (count_over_time({x="y"} [5m]))) or vector(0)`; Distinct(q) != q {
		t.Fatalf("a distinct count changed: %s", Distinct(q))
	}
}

// fakeRule answers a query that contains all its parts with value, or
// with Loki's series limit error when value is empty.
type fakeRule struct {
	parts []string
	value string
}

// fakeLoki answers the hourly rows query from rows and any other query
// from the first rule that matches it, and records the instants it was
// asked at.
type fakeLoki struct {
	rules []fakeRule
	rows  map[string]string
	times []string
}

func (f *fakeLoki) rule(value string, parts ...string) {
	f.rules = append(f.rules, fakeRule{parts, value})
}

// matchingLoki answers checkFixture's hour as expected.json does, and the
// dashboards' distinct counts with a few more users and sessions.
func matchingLoki() *fakeLoki {
	f := &fakeLoki{rows: map[string]string{"screen": "101", "error": "3"}}
	f.rule("40", "User_Id", `outsystems_log_type="screen"`)
	f.rule("50", "Session_Id", `outsystems_log_type="screen"`)
	f.rule("2", "error_fingerprint")
	f.rule("44", "User_Id")
	f.rule("53", "Session_Id")
	return f
}

func (f *fakeLoki) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")
	if t := r.URL.Query().Get("time"); t != "" {
		f.times = append(f.times, t)
	}
	if strings.Contains(q, "sum by (outsystems_log_type)") {
		var result []map[string]any
		for logType, n := range f.rows {
			result = append(result, map[string]any{"metric": map[string]string{"outsystems_log_type": logType}, "value": []any{0, n}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": result}})
		return
	}
	for _, rule := range f.rules {
		matched := true
		for _, p := range rule.parts {
			matched = matched && strings.Contains(q, p)
		}
		if !matched {
			continue
		}
		if rule.value == "" {
			http.Error(w, "maximum of series (20000) reached for a single query", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/loki/api/v1/query_range" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix",
				"result": []any{map[string]any{"metric": map[string]string{}, "values": []any{[]any{0, "1"}, []any{300, rule.value}}}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector",
			"result": []any{map[string]any{"metric": map[string]string{}, "value": []any{0, rule.value}}}}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{}}})
}

func checkFixture(t *testing.T) (*Expected, []Panel, CheckOptions) {
	t.Helper()
	from := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	hour := Bucket{From: from, To: from.Add(time.Hour), Rows: map[string]int{"screen": 100, "error": 3},
		Users: 40, Sessions: 50, ErrorRows: 3, ErrorKinds: 2}
	exp := &Expected{End: from.Add(time.Hour), Hours: []Bucket{hour}}
	panels, err := LoadPanels(dashboardDir)
	if err != nil {
		t.Fatal(err)
	}
	return exp, panels, CheckOptions{Environment: "scale-x", From: from.Add(-30 * time.Minute)}
}

func TestCheckPassesWhenLokiMatchesExpected(t *testing.T) {
	exp, panels, opts := checkFixture(t)
	f := matchingLoki()
	srv := httptest.NewServer(f)
	defer srv.Close()

	res, err := Check(context.Background(), Loki{URL: srv.URL}, exp, panels, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed() {
		var b strings.Builder
		_ = CheckReport(&b, res)
		t.Fatalf("check failed:\n%s", b.String())
	}
	h := res.Hours[0]
	if h.PanelUsers != 44 || h.PanelSessions != 53 {
		t.Errorf("panel users and sessions %d, %d; want 44, 53", h.PanelUsers, h.PanelSessions)
	}
	if f.times[0] != "2026-10-20T10:59:59.999999999Z" {
		t.Errorf("the hour was read at %s, want a nanosecond before its end", f.times[0])
	}
	// Two 5-minute panels across the log, three range panels over 1 h, 24 h
	// and everything collected.
	if len(res.Panels) != 2+3*3 {
		t.Fatalf("%d panel checks, want 11", len(res.Panels))
	}
	for _, p := range res.Panels {
		if p.Panel.Title == "Active sessions (5m)" && p.Value != 53 {
			t.Errorf("Active sessions (5m) peaked at %v, want 53, the range query's largest value", p.Value)
		}
	}
}

func TestCheckFailsOnAMismatchAnIncompleteHourOrAFailedPanel(t *testing.T) {
	for name, setup := range map[string]func(*fakeLoki){
		"distinct users differ": func(f *fakeLoki) { f.rules[0].value = "39" },
		"rows missing":          func(f *fakeLoki) { f.rows["screen"] = "99" },
		// Only the errors dashboard's panels filter on Espace_Name.
		"panel over the limit": func(f *fakeLoki) { f.rules = append([]fakeRule{{[]string{"Espace_Name"}, ""}}, f.rules...) },
	} {
		t.Run(name, func(t *testing.T) {
			exp, panels, opts := checkFixture(t)
			f := matchingLoki()
			setup(f)
			srv := httptest.NewServer(f)
			defer srv.Close()
			res, err := Check(context.Background(), Loki{URL: srv.URL}, exp, panels, opts)
			if err != nil {
				t.Fatal(err)
			}
			if res.Passed() {
				t.Fatal("check passed")
			}
			var b strings.Builder
			if err := CheckReport(&b, res); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(b.String(), "The check failed.") {
				t.Errorf("report does not say it failed:\n%s", b.String())
			}
		})
	}
}
