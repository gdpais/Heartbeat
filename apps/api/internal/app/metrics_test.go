package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"heartbeat/apps/api/internal/httpapi"
)

func TestRequestMetricsLabelRequestsByRoute(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := newRequestMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	failing := httpapi.Check{Name: "postgres", Run: func(context.Context) error { return errors.New("down") }}
	handler := routes(registry, metrics, httpapi.NewServer(nil, []httpapi.Check{failing}, time.Second))
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/healthz"},
		{http.MethodHead, "/healthz"},
		{http.MethodGet, "/readyz"},
		{http.MethodPost, "/readyz"},
		{http.MethodGet, "/metrics"},
		// A scan of made-up paths and methods lands in two series.
		{http.MethodGet, "/wp-login.php"},
		{http.MethodGet, "/api/v1/../../etc/passwd"},
		{"PROPFIND", "/healthz"},
		{"BREW", "/coffee"},
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(req.method, req.path, nil))
	}

	want := `
# HELP heartbeat_api_http_requests_total HTTP requests served, by method, route and status code.
# TYPE heartbeat_api_http_requests_total counter
heartbeat_api_http_requests_total{code="200",method="GET",route="/healthz"} 2
heartbeat_api_http_requests_total{code="200",method="HEAD",route="/healthz"} 1
heartbeat_api_http_requests_total{code="503",method="GET",route="/readyz"} 1
heartbeat_api_http_requests_total{code="405",method="POST",route="unmatched"} 1
heartbeat_api_http_requests_total{code="200",method="GET",route="/metrics"} 1
heartbeat_api_http_requests_total{code="404",method="GET",route="unmatched"} 1
heartbeat_api_http_requests_total{code="307",method="GET",route="unmatched"} 1
heartbeat_api_http_requests_total{code="405",method="other",route="unmatched"} 1
heartbeat_api_http_requests_total{code="404",method="other",route="unmatched"} 1
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(want), MetricHTTPRequests); err != nil {
		t.Fatal(err)
	}
	if got := testutil.CollectAndCount(metrics.duration); got != 7 {
		t.Fatalf("%s has %d series, want 7 (method and route pairs)", MetricHTTPRequestDuration, got)
	}
}

func TestStatusRecorder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    int
	}{
		{"body without a status", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }, http.StatusOK},
		{"nothing written", func(http.ResponseWriter, *http.Request) {}, http.StatusOK},
		{"explicit status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, http.StatusServiceUnavailable},
		{"second status ignored", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.WriteHeader(http.StatusInternalServerError)
		}, http.StatusCreated},
		{"informational status first", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusEarlyHints)
			w.WriteHeader(http.StatusAccepted)
		}, http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
			tc.handler(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			if recorder.status != tc.want {
				t.Fatalf("status = %d, want %d", recorder.status, tc.want)
			}
		})
	}
}

func TestStatusRecorderKeepsTheResponseController(t *testing.T) {
	// http.ResponseController reaches the real writer through Unwrap, so a
	// handler behind the metrics can still flush a streamed response.
	inner := httptest.NewRecorder()
	recorder := &statusRecorder{ResponseWriter: inner, status: http.StatusOK}
	if err := http.NewResponseController(recorder).Flush(); err != nil {
		t.Fatalf("Flush through the recorder: %v", err)
	}
	if !inner.Flushed {
		t.Fatal("the underlying writer was not flushed")
	}
}
