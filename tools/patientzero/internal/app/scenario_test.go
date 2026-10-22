package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"heartbeat/tools/patientzero/internal/inject"
	"heartbeat/tools/patientzero/internal/scenario"
	"heartbeat/tools/patientzero/internal/traffic"
)

// switchOn starts a fault of the given type on s's injector.
func switchOn(t *testing.T, s *Server, typ string, params map[string]any) {
	t.Helper()
	if s.Faults == nil {
		s.Faults = &inject.Injector{Sleep: func(context.Context, time.Duration) error { return nil }}
	}
	reg := scenario.NewRegistry()
	inject.Register(reg, s.Faults, EntryPoints)
	f, err := reg.New(scenario.FaultSpec{Type: typ, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// An injected failure is a real failed request: an error row under the
// request's Request_Key, linked from its other rows, and the entry point's
// usual failure response. Its business SQL does not run.
func TestInjectedErrorWritesAnErrorRow(t *testing.T) {
	store := &fakeStore{}
	s, rec := newServer(t, store)
	switchOn(t, s, "errors", map[string]any{"entry_points": []any{EntryPlaceOrder}, "ratio": 1, "message": "checkout-errors"})

	w := do(t, s, http.MethodPost, PathPlaceOrder, `{"productId":1,"quantity":2}`, maria)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", w.Code, w.Body)
	}
	sameRequest(t, rec, "mobile_request error")
	errRow := rec.byType()["error"][0]
	if errRow["Message"] != "injected by scenario: checkout-errors" {
		t.Errorf("Message %q", errRow["Message"])
	}
	if stack := errRow["Stack"].(string); strings.Contains(stack, "System.Data") {
		t.Errorf("an injected failure is the action's own, not the data provider's:\n%s", stack)
	}
	if got := rec.byType()["mobile_request"][0]["Error_Id"]; got != errRow["Id"] {
		t.Errorf("mobile_request.Error_Id %v, want %v", got, errRow["Id"])
	}

	// Other entry points are untouched.
	rec.rows = nil
	if w := do(t, s, http.MethodGet, PathOrderList, "", maria); w.Code != http.StatusOK {
		t.Fatalf("OrderList %d with a PlaceOrder fault", w.Code)
	}
	sameRequest(t, rec, "screen")
}

// Injected latency is spent before the business SQL, so it is in the row's
// Duration as a slow database would be.
func TestInjectedLatencyIsInTheDuration(t *testing.T) {
	s, rec := newServer(t, &fakeStore{})
	var clockNow = t0
	s.Now = func() time.Time { return clockNow }
	s.Faults = &inject.Injector{Sleep: func(_ context.Context, d time.Duration) error {
		clockNow = clockNow.Add(d)
		return nil
	}}
	switchOn(t, s, "latency", map[string]any{"entry_points": []any{EntryOrderList}, "delay": "3s"})
	if w := do(t, s, http.MethodGet, PathOrderList, "", maria); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := rec.byType()["screen"][0]["Duration"]; got != int64(3000) {
		t.Errorf("Duration %v, want 3000 ms", got)
	}
}

func TestTrafficIsCountedDuringARun(t *testing.T) {
	s, _ := newServer(t, &fakeStore{})
	s.Traffic = &traffic.Counter{}
	switchOn(t, s, "errors", map[string]any{"entry_points": []any{EntryGetOrders}, "ratio": 1})
	s.Traffic.Begin(t0)
	do(t, s, http.MethodGet, PathOrderList, "", maria)
	do(t, s, http.MethodGet, PathOrderList, "", nil)
	do(t, s, http.MethodPost, PathPlaceOrder, `{"productId":99,"quantity":1}`, maria) // refused: failed, not injected
	do(t, s, http.MethodGet, PathAPIOrders, "", maria)                                // injected
	_, run := s.Traffic.End(t0.Add(time.Minute))
	if run.Requests != 4 || run.Failed != 2 || run.Injected != 1 || run.Users != 1 || run.Sessions != 2 {
		t.Errorf("run summary %+v", run)
	}
	if ep := run.EntryPoints[EntryOrderList]; ep.Requests != 2 || ep.AnonymousCalls != 1 {
		t.Errorf("OrderList %+v", ep)
	}
	if ep := run.EntryPoints[EntryGetOrders]; ep.Injected != 1 {
		t.Errorf("GetOrders %+v", ep)
	}
}

func TestAdminIsMountedOnlyWhenGiven(t *testing.T) {
	s, _ := newServer(t, &fakeStore{})
	if w := do(t, s, http.MethodGet, "/admin/scenarios", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("no admin handler: %d, want 404", w.Code)
	}
	s.Admin = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	if w := do(t, s, http.MethodPost, "/admin/run/stop", "", nil); w.Code != http.StatusTeapot {
		t.Fatalf("admin handler: %d", w.Code)
	}
}
