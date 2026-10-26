package app

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"heartbeat/tools/patientzero/internal/shop"
	"heartbeat/tools/patientzero/internal/traffic"
)

// gateStore signals entered when a call reaches it (so a test knows the
// limiter has admitted the request and it is holding its token), then blocks
// until release is closed.
type gateStore struct {
	entered chan struct{}
	release chan struct{}
}

func (g *gateStore) Customer(context.Context, string) (int, error) { return 1000, nil }

func (g *gateStore) gate(ctx context.Context) error {
	g.entered <- struct{}{}
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *gateStore) Orders(ctx context.Context, _, _ int) ([]shop.Order, error) {
	if err := g.gate(ctx); err != nil {
		return nil, err
	}
	return []shop.Order{{ID: 7, Product: "Thermometer", Quantity: 1, Total: 12.5}}, nil
}

func (g *gateStore) PlaceOrder(ctx context.Context, _, _, _ int) (shop.Order, error) {
	if err := g.gate(ctx); err != nil {
		return shop.Order{}, err
	}
	return shop.Order{ID: 8}, nil
}

// When MaxInFlight requests are already running, the next is shed with 503 and
// a Retry-After header rather than queued or allowed to open another
// connection. The three entry points share the one budget, the health and
// admin endpoints are never shed, and each shed request is counted in the
// run's traffic and reported in the server log.
func TestLimiterShedsExcessWith503(t *testing.T) {
	gate := &gateStore{entered: make(chan struct{}, 8), release: make(chan struct{})}
	s, _ := newServer(t, &fakeStore{})
	s.Store = gate
	s.MaxInFlight = 1
	s.Admin = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	var log bytes.Buffer
	s.Log = slog.New(slog.NewTextHandler(&log, nil))
	s.Traffic = &traffic.Counter{}
	s.Traffic.Begin(t0)
	handler := s.Handler()

	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "10.244.0.9:51000"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	// Hold one request in flight (blocked in the store, token taken).
	inflight := make(chan *httptest.ResponseRecorder, 1)
	go func() { inflight <- call(http.MethodGet, PathAPIOrders, "") }()
	<-gate.entered // the request has been admitted and holds the only token

	// While it holds the only token, every entry point is shed.
	for _, r := range []struct{ method, path, body string }{
		{http.MethodGet, PathAPIOrders, ""},
		{http.MethodGet, PathOrderList, ""},
		{http.MethodPost, PathPlaceOrder, `{"productId":1,"quantity":1}`},
	} {
		shed := call(r.method, r.path, r.body)
		if shed.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s while saturated: got %d, want 503", r.method, r.path, shed.Code)
		}
		if shed.Header().Get("Retry-After") == "" {
			t.Errorf("%s %s: shed response has no Retry-After header", r.method, r.path)
		}
	}
	// The health and admin endpoints still answer.
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusOK, "/admin/runs": http.StatusNoContent} {
		if got := call(http.MethodGet, path, "").Code; got != want {
			t.Errorf("GET %s while saturated: got %d, want %d", path, got, want)
		}
	}

	// Release the first; it completes normally, proving the token is returned.
	close(gate.release)
	first := <-inflight
	if first.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", first.Code)
	}
	// A later request now gets the freed token.
	if again := call(http.MethodGet, PathAPIOrders, ""); again.Code != http.StatusOK {
		t.Fatalf("request after release: got %d, want 200", again.Code)
	}

	// The shed requests are counted apart from the two served.
	_, run := s.Traffic.End(t0.Add(time.Minute))
	if run.Shed != 3 || run.Requests != 2 {
		t.Errorf("run traffic: %d shed and %d requests, want 3 and 2", run.Shed, run.Requests)
	}
	for _, name := range EntryPoints {
		if got := run.EntryPoints[name].Shed; got != 1 {
			t.Errorf("%s: %d shed, want 1", name, got)
		}
	}
	// The first shed request is reported at once; the other two fall within
	// shedReportEvery of it.
	if n := strings.Count(log.String(), "shedding requests"); n != 1 || !strings.Contains(log.String(), "shed=1") {
		t.Errorf("want one shedding line with shed=1, got %d:\n%s", n, log.String())
	}
}

// The shedding log line comes at most every shedReportEvery, each with how
// many requests were shed since the line before.
func TestShedReportPaces(t *testing.T) {
	var p shedReport
	for _, step := range []struct {
		at time.Duration
		n  int
		ok bool
	}{
		{0, 1, true},
		{time.Second, 0, false},
		{9 * time.Second, 0, false},
		{10 * time.Second, 3, true},
		{11 * time.Second, 0, false},
	} {
		if n, ok := p.add(t0.Add(step.at)); n != step.n || ok != step.ok {
			t.Errorf("at %s: (%d, %v), want (%d, %v)", step.at, n, ok, step.n, step.ok)
		}
	}
}

// A pool whose every connection is in use counts as answering: Ping must not
// wait for a connection to free up, or a slow SQL Server would fail the
// readiness probe while the entry points shed load.
func TestPingDoesNotWaitOnAFullPool(t *testing.T) {
	db := sql.OpenDB(stubConnector{})
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := Ping(context.Background(), db); err != nil {
		t.Fatalf("ping with a free connection: %v", err)
	}
	held, err := db.Conn(context.Background()) // takes the only connection
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := Ping(ctx, db); err != nil {
		t.Fatalf("ping on a full pool: %v, want nil without waiting for a connection", err)
	}
}

// stubConnector is a database/sql driver whose connections do nothing:
// enough for a pool to open them and count them in use.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) { return stubConn{}, nil }
func (stubConnector) Driver() driver.Driver                        { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return stubConn{}, nil }

type stubConn struct{}

func (stubConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("stub: no statements") }
func (stubConn) Close() error                        { return nil }
func (stubConn) Begin() (driver.Tx, error)           { return nil, errors.New("stub: no transactions") }

// A server-action failure never reflects internal error text to the caller:
// a 5xx carries a generic message, and a refused order a fixed 400 message.
func TestServerActionErrorsAreSanitized(t *testing.T) {
	t.Run("internal error is generic 500", func(t *testing.T) {
		store := &fakeStore{fail: errors.New("mssql: login failed for user 'heartbeat' at tcp:secret-host:1433")}
		s, _ := newServer(t, store)
		req := httptest.NewRequest(http.MethodPost, PathPlaceOrder, strings.NewReader(`{"productId":1,"quantity":1}`))
		req.RemoteAddr = "10.244.0.9:51000"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d, want 500", w.Code)
		}
		msg := serverActionMessage(t, w.Body.Bytes())
		if msg != "Internal Server Error" {
			t.Errorf("message = %q, want generic", msg)
		}
		if strings.Contains(w.Body.String(), "secret-host") || strings.Contains(w.Body.String(), "login failed") {
			t.Errorf("response leaked internal error text: %s", w.Body.String())
		}
	})

	t.Run("malformed body is fixed 400", func(t *testing.T) {
		s, _ := newServer(t, &fakeStore{})
		req := httptest.NewRequest(http.MethodPost, PathPlaceOrder, strings.NewReader(`{"productId":`))
		req.RemoteAddr = "10.244.0.9:51000"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400", w.Code)
		}
		if strings.Contains(w.Body.String(), "unexpected EOF") || strings.Contains(w.Body.String(), "json") {
			t.Errorf("response leaked decoder text: %s", w.Body.String())
		}
	})
}

func serverActionMessage(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Exception struct{ Message string } `json:"exception"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	return out.Exception.Message
}
