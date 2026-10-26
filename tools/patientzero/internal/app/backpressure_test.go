package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"heartbeat/tools/patientzero/internal/shop"
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
// connection.
func TestLimiterShedsExcessWith503(t *testing.T) {
	gate := &gateStore{entered: make(chan struct{}, 8), release: make(chan struct{})}
	s, _ := newServer(t, &fakeStore{})
	s.Store = gate
	s.MaxInFlight = 1
	handler := s.Handler()

	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, PathAPIOrders, nil)
		req.RemoteAddr = "10.244.0.9:51000"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	// Hold one request in flight (blocked in the store, token taken).
	inflight := make(chan *httptest.ResponseRecorder, 1)
	go func() { inflight <- call() }()
	<-gate.entered // the request has been admitted and holds the only token

	// A second request while the first holds the only token is shed.
	shed := call()
	if shed.Code != http.StatusServiceUnavailable {
		t.Fatalf("second request: got %d, want 503", shed.Code)
	}
	if ra := shed.Header().Get("Retry-After"); ra == "" {
		t.Error("shed response has no Retry-After header")
	}

	// Release the first; it completes normally, proving the token is returned.
	close(gate.release)
	first := <-inflight
	if first.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", first.Code)
	}
	// A later request now gets the freed token.
	if again := call(); again.Code != http.StatusOK {
		t.Fatalf("request after release: got %d, want 200", again.Code)
	}
}

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
