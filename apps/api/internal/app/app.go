// Package app wires together the API's subsystems and runs the service until
// the context is cancelled.
//
// Run is the entry point. It serves the operations of the API's contract
// (package httpapi) and the Prometheus metrics, and drains in-flight requests
// on shutdown.
package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"heartbeat/apps/api/internal/httpapi"
)

// Config holds the top-level runtime parameters for the API service.
type Config struct {
	// ListenAddr is the TCP address on which the HTTP server will listen
	// (e.g. ":8081").
	ListenAddr string
	// Logger receives structured service logs. When nil, Run logs JSON to
	// stderr.
	Logger *slog.Logger
}

// timeouts bounds every blocking step of serving and shutdown.
type timeouts struct {
	// readiness bounds one GET /readyz, every check included.
	readiness time.Duration
	// httpShutdown bounds draining in-flight HTTP requests.
	httpShutdown time.Duration
}

// defaultTimeouts returns the production timeouts.
func defaultTimeouts() timeouts {
	return timeouts{
		readiness:    2 * time.Second,
		httpShutdown: 10 * time.Second,
	}
}

// Run initialises the service and serves HTTP until ctx is cancelled or the
// HTTP server fails.
//
// Shutdown is synchronous: after ctx is cancelled, Run stops accepting
// connections, waits up to 10 seconds for in-flight requests to finish (see
// defaultTimeouts), closes the connections still open, and only then
// returns. Run returns nil on a clean shutdown.
func Run(ctx context.Context, cfg Config) error {
	deps, err := newRunDeps(cfg)
	if err != nil {
		return err
	}
	return run(ctx, deps)
}

// newRunDeps builds the production collaborators of run.
func newRunDeps(cfg Config) (runDeps, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	registry, requestMetrics, err := newRegistry()
	if err != nil {
		return runDeps{}, err
	}
	return runDeps{
		cfg:            cfg,
		logger:         logger,
		registry:       registry,
		requestMetrics: requestMetrics,
		timeouts:       defaultTimeouts(),
	}, nil
}

// runDeps are the collaborators of run, injectable for tests.
type runDeps struct {
	cfg      Config
	logger   *slog.Logger
	registry *prometheus.Registry
	// requestMetrics, when set, records every request.
	requestMetrics *requestMetrics
	// checks are the readiness checks GET /readyz runs. PostgreSQL and Redis
	// join them when the API connects to them [4.1].
	checks   []httpapi.Check
	timeouts timeouts
	// onListen, when set, receives the bound HTTP address.
	onListen func(net.Addr)
}

// run is Run after dependency construction.
func run(ctx context.Context, deps runDeps) error {
	logger := deps.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	listener, err := net.Listen("tcp", deps.cfg.ListenAddr)
	if err != nil {
		return err
	}
	if deps.onListen != nil {
		deps.onListen(listener.Addr())
	}

	api := httpapi.NewServer(logger, deps.checks, deps.timeouts.readiness)
	server := &http.Server{Handler: routes(deps.registry, deps.requestMetrics, api), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("http server listening", "addr", listener.Addr().String())

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-serveErr:
		logger.Error("http server failed", "error", err)
		runErr = err
	}
	shutdown(server, logger, deps.timeouts.httpShutdown)
	return runErr
}

// shutdown stops accepting connections and waits for in-flight requests to
// finish, at most timeout; then it closes the connections still open.
func shutdown(server *http.Server, logger *slog.Logger, timeout time.Duration) {
	start := time.Now()
	logger.Info("shutdown: draining http server", "timeout", timeout.String())
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Warn("shutdown: http drain timed out; closing connections", "error", err)
		_ = server.Close()
	}
	logger.Info("shutdown complete", "duration", time.Since(start).String())
}

// routes builds the HTTP handler tree for the service.
//
// Endpoints:
//   - GET /metrics – Prometheus metrics scrape endpoint.
//   - GET /healthz – Liveness; always 200 while serving.
//   - GET /readyz  – Readiness; 200 or 503 with only the status in the body.
//
// /healthz, /readyz and every later operation come from apps/api/openapi.yaml
// through httpapi.Server; /metrics is not part of the contract. Every request
// is recorded in the request metrics, by the route that served it.
func routes(registry *prometheus.Registry, metrics *requestMetrics, api *httpapi.Server) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	api.Register(mux)
	if metrics == nil {
		return mux
	}
	return metrics.instrument(mux)
}
