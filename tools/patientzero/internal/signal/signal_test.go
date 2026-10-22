package signal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"heartbeat/tools/patientzero/internal/scenario"
)

var at = time.Date(2026, 10, 22, 10, 0, 0, 500_000_000, time.UTC)

// fake serves one canned body on path and records the requests it got.
type fake struct {
	t      *testing.T
	path   string
	status int
	body   string
	got    []url.Values
}

func (f *fake) server() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != f.path {
			f.t.Errorf("request %s %s, want GET %s", r.Method, r.URL.Path, f.path)
			http.NotFound(w, r)
			return
		}
		f.got = append(f.got, r.URL.Query())
		if f.status != 0 {
			w.WriteHeader(f.status)
		}
		_, _ = w.Write([]byte(f.body))
	}))
	f.t.Cleanup(srv.Close)
	return srv
}

func promSignal(cond string) scenario.Signal {
	return scenario.Signal{Name: "s", PromQL: `sum(rate(x[1m]))`, Condition: cond}
}

func TestPromQLVector(t *testing.T) {
	f := &fake{t: t, path: "/api/v1/query", body: `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"a":"1"},"value":[1761127200.5,"0.52"]},
		{"metric":{"a":"2"},"value":[1761127200.5,"0.31"]}]}}`}
	src := Sources{Prometheus: f.server().URL + "/"}
	res, err := src.Evaluate(context.Background(), promSignal("> 0.3"), at)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass || res.Series != 2 || !slices.Equal(res.Values, []float64{0.31, 0.52}) {
		t.Fatalf("result %+v, want pass with both series' values", res)
	}
	q := f.got[0]
	if q.Get("query") != `sum(rate(x[1m]))` || q.Get("time") != "1792663200.500" {
		t.Errorf("query params %v", q)
	}
	// Every series must hold.
	res, err = src.Evaluate(context.Background(), promSignal("> 0.4"), at)
	if err != nil || res.Pass {
		t.Fatalf("> 0.4: %+v, %v; want fail (one series is 0.31)", res, err)
	}
}

func TestPromQLScalarAndEmpty(t *testing.T) {
	f := &fake{t: t, path: "/api/v1/query", body: `{"status":"success","data":{"resultType":"scalar","result":[1761127200,"3"]}}`}
	src := Sources{Prometheus: f.server().URL}
	if res, err := src.Evaluate(context.Background(), promSignal("== 3"), at); err != nil || !res.Pass {
		t.Fatalf("scalar: %+v, %v", res, err)
	}
	f.body = `{"status":"success","data":{"resultType":"vector","result":[]}}`
	if res, err := src.Evaluate(context.Background(), promSignal("< 1"), at); err != nil || res.Pass || res.Series != 0 {
		t.Fatalf("no data, < 1: %+v, %v; want fail", res, err)
	}
	if res, err := src.Evaluate(context.Background(), promSignal("absent"), at); err != nil || !res.Pass {
		t.Fatalf("no data, absent: %+v, %v; want pass", res, err)
	}
}

func TestPromQLErrors(t *testing.T) {
	f := &fake{t: t, path: "/api/v1/query", status: http.StatusBadRequest,
		body: `{"status":"error","errorType":"bad_data","error":"parse error"}`}
	src := Sources{Prometheus: f.server().URL}
	if _, err := src.Evaluate(context.Background(), promSignal("> 0"), at); err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("err = %v, want the HTTP status", err)
	}
	f.status, f.body = 0, `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	if _, err := src.Evaluate(context.Background(), promSignal("> 0"), at); err == nil || !strings.Contains(err.Error(), "range vector") {
		t.Fatalf("err = %v, want the range vector refused", err)
	}
	if _, err := (Sources{}).Evaluate(context.Background(), promSignal("> 0"), at); err == nil || !strings.Contains(err.Error(), "no Prometheus URL") {
		t.Fatalf("err = %v, want no URL", err)
	}
}

func TestLogQLMetricQueries(t *testing.T) {
	f := &fake{t: t, path: "/loki/api/v1/query", body: `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{},"value":[1761127200,"12"]}]}}`}
	src := Sources{Loki: f.server().URL}
	sig := scenario.Signal{Name: "l", LogQL: `sum(count_over_time({service_name="PatientZero"}[2m]))`, Condition: "> 0"}
	res, err := src.Evaluate(context.Background(), sig, at)
	if err != nil || !res.Pass || res.Values[0] != 12 {
		t.Fatalf("metric query: %+v, %v", res, err)
	}
	q := f.got[0]
	if q.Get("time") != "1792663200500000000" || q.Get("query") != sig.LogQL {
		t.Errorf("Loki params %v; want nanosecond time", q)
	}
	// A log query has no value to compare; the signal names the fix.
	f.body = `{"status":"success","data":{"resultType":"streams","result":[{"stream":{},"values":[["1","x"]]}]}}`
	sig.LogQL = `{service_name="PatientZero"} |= "boom"`
	if _, err := src.Evaluate(context.Background(), sig, at); err == nil || !strings.Contains(err.Error(), "count_over_time") {
		t.Fatalf("log query: err %v, want a metric query asked for", err)
	}
}

func TestAlerts(t *testing.T) {
	f := &fake{t: t, path: "/api/v2/alerts", body: `[{"labels":{"alertname":"OutSystemsErrorBudgetBurnFast"},"status":{"state":"active"}}]`}
	src := Sources{Alertmanager: f.server().URL}
	sig := scenario.Signal{Name: "a", Alert: "OutSystemsErrorBudgetBurnFast", State: scenario.StateFiring,
		Labels: map[string]string{"severity": "critical", "application": `Patient"Zero`}}
	res, err := src.Evaluate(context.Background(), sig, at)
	if err != nil || !res.Pass || res.Values[0] != 1 {
		t.Fatalf("firing: %+v, %v", res, err)
	}
	q := f.got[0]
	wantFilters := []string{`alertname="OutSystemsErrorBudgetBurnFast"`, `application="Patient\"Zero"`, `severity="critical"`}
	if !slices.Equal(q["filter"], wantFilters) || q.Get("active") != "true" || q.Get("silenced") != "false" || q.Get("inhibited") != "false" {
		t.Errorf("Alertmanager params %v; want filters %v and only active, unsilenced alerts", q, wantFilters)
	}
	sig.State = scenario.StateResolved
	if res, err := src.Evaluate(context.Background(), sig, at); err != nil || res.Pass {
		t.Fatalf("resolved while active: %+v, %v; want fail", res, err)
	}
	f.body = `[]`
	if res, err := src.Evaluate(context.Background(), sig, at); err != nil || !res.Pass {
		t.Fatalf("resolved with no alert: %+v, %v; want pass", res, err)
	}
}

func TestResponseSizeIsCapped(t *testing.T) {
	f := &fake{t: t, path: "/api/v1/query", body: strings.Repeat(" ", maxBody+10)}
	src := Sources{Prometheus: f.server().URL}
	if _, err := src.Evaluate(context.Background(), promSignal("> 0"), at); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v, want the size cap", err)
	}
}
