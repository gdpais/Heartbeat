package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"
)

// HTTP request metrics, recorded by [requestMetrics] for every request the
// API serves, /metrics and the health endpoints included.
const (
	// MetricHTTPRequests counts requests by their response's status code.
	// Labels: method, route, code.
	MetricHTTPRequests = "heartbeat_api_http_requests_total"
	// MetricHTTPRequestDuration is the time from receiving a request to its
	// handler returning, with the Prometheus client's default buckets.
	// Labels: method, route.
	MetricHTTPRequestDuration = "heartbeat_api_http_request_duration_seconds"
)

// unmatchedRoute is the route label of a request no route matched: a 404,
// or a 405 for a known path with another method.
const unmatchedRoute = "unmatched"

// otherMethod is the method label of a request whose method is not one of
// knownMethods.
const otherMethod = "other"

// knownMethods are the request methods kept as method labels. Any other
// method is labelled otherMethod, so a client sending made-up methods adds no
// series.
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// newRegistry returns the API's Prometheus registry with Go runtime (go_*)
// and process (process_*) metrics registered, and the HTTP request metrics.
func newRegistry() (*prometheus.Registry, *requestMetrics, error) {
	registry := prometheus.NewRegistry()
	if err := registry.Register(promcollectors.NewGoCollector()); err != nil {
		return nil, nil, err
	}
	if err := registry.Register(promcollectors.NewProcessCollector(promcollectors.ProcessCollectorOpts{})); err != nil {
		return nil, nil, err
	}
	metrics, err := newRequestMetrics(registry)
	if err != nil {
		return nil, nil, err
	}
	return registry, metrics, nil
}

// requestMetrics records the count and duration of HTTP requests.
type requestMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// newRequestMetrics registers the HTTP request metrics with registerer.
func newRequestMetrics(registerer prometheus.Registerer) (*requestMetrics, error) {
	m := &requestMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: MetricHTTPRequests,
			Help: "HTTP requests served, by method, route and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    MetricHTTPRequestDuration,
			Help:    "Time to serve an HTTP request, by method and route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
	for _, c := range []prometheus.Collector{m.requests, m.duration} {
		if err := registerer.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// instrument records every request next serves. next must be the
// [http.ServeMux] that routes the request, since the route label is the
// pattern it matched, without the method: "/readyz", later
// "/api/v1/applications/{id}". The labels come only from registered routes
// and knownMethods, so unknown paths and methods add no series.
func (m *requestMetrics) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		method, route := requestLabels(r)
		m.requests.WithLabelValues(method, route, strconv.Itoa(recorder.status)).Inc()
		m.duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	})
}

// requestLabels returns the method and route labels of r after a ServeMux
// served it. ServeMux sets r.Pattern to the pattern it matched, and leaves
// it empty when none did.
func requestLabels(r *http.Request) (method, route string) {
	method = r.Method
	if !knownMethods[method] {
		method = otherMethod
	}
	route = r.Pattern
	if _, path, ok := strings.Cut(route, " "); ok {
		route = path
	}
	if route == "" {
		route = unmatchedRoute
	}
	return method, route
}

// statusRecorder remembers the status code a handler writes; 200 when it
// writes a body without one.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	// A 1xx status is informational; the final one follows.
	if !r.wroteHeader && status >= http.StatusOK {
		r.status, r.wroteHeader = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Unwrap lets [http.ResponseController] reach the underlying writer, to
// flush or set deadlines.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
