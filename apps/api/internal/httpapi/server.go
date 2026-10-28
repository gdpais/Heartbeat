// Package httpapi implements the operations of the API's contract,
// apps/api/openapi.yaml.
//
// Server implements the strict server interface that oapi-codegen generates
// into package openapi; Register serves it on a ServeMux. Handlers get typed
// requests and return typed responses, so a response the spec does not
// declare does not compile.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"heartbeat/apps/api/internal/openapi"
)

// Server implements [openapi.StrictServerInterface].
type Server struct {
	readiness *readiness
}

// The generated interface lists every operation of the spec, so an
// operation added to openapi.yaml without a handler fails to compile here.
var _ openapi.StrictServerInterface = (*Server)(nil)

// NewServer returns a Server whose GET /readyz runs checks, all of them
// bounded together by readinessTimeout. A nil logger discards logs.
func NewServer(logger *slog.Logger, checks []Check, readinessTimeout time.Duration) *Server {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Server{readiness: &readiness{checks: checks, timeout: readinessTimeout, logger: logger}}
}

// Register serves every operation of the spec on mux, under the method and
// path patterns the spec declares (GET /healthz, GET /readyz).
func (s *Server) Register(mux *http.ServeMux) {
	openapi.HandlerFromMux(openapi.NewStrictHandler(s, nil), mux)
}

// GetHealthz serves GET /healthz: liveness. It checks no dependency, so a
// PostgreSQL or Redis outage never restarts the pod; readiness takes it out
// of its Service instead.
func (s *Server) GetHealthz(context.Context, openapi.GetHealthzRequestObject) (openapi.GetHealthzResponseObject, error) {
	return openapi.GetHealthz200TextResponse("ok"), nil
}

// GetReadyz serves GET /readyz: 200 when every readiness check passes, 503
// otherwise. The endpoint is unauthenticated and kubelet probes only need
// the status code, so the body carries the status alone; which check failed
// and why is logged.
func (s *Server) GetReadyz(ctx context.Context, _ openapi.GetReadyzRequestObject) (openapi.GetReadyzResponseObject, error) {
	if s.readiness.ready(ctx) {
		return openapi.GetReadyz200JSONResponse{Status: openapi.Ready}, nil
	}
	return openapi.GetReadyz503JSONResponse{Status: openapi.NotReady}, nil
}
