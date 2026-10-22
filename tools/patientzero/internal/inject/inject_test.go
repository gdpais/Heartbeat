package inject

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"heartbeat/tools/patientzero/internal/scenario"
)

var entryPoints = []string{"OrderList", "PlaceOrder", "GetOrders"}

// setup returns an injector whose random numbers come from rnd and whose
// sleeps are recorded instead of waited.
func setup(rnd ...float64) (*Injector, *scenario.Registry, *[]time.Duration) {
	var slept []time.Duration
	i := 0
	inj := &Injector{
		Float: func() float64 {
			v := rnd[i%len(rnd)]
			i++
			return v
		},
		Sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		},
	}
	reg := scenario.NewRegistry()
	Register(reg, inj, entryPoints)
	return inj, reg, &slept
}

func start(t *testing.T, reg *scenario.Registry, typ string, params map[string]any) scenario.Fault {
	t.Helper()
	f, err := reg.New(scenario.FaultSpec{Type: typ, Params: params})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLatencyDelaysOnlyItsEntryPoints(t *testing.T) {
	inj, reg, slept := setup(0.5)
	f := start(t, reg, "latency", map[string]any{"entry_points": []any{"OrderList"}, "delay": "2s", "jitter": "1s"})
	if err := inj.Apply(context.Background(), "PlaceOrder"); err != nil || len(*slept) != 0 {
		t.Fatalf("PlaceOrder: err %v, slept %v; want no delay", err, *slept)
	}
	if err := inj.Apply(context.Background(), "OrderList"); err != nil {
		t.Fatal(err)
	}
	// 2s plus half the 1s jitter.
	if len(*slept) != 1 || (*slept)[0] != 2500*time.Millisecond {
		t.Fatalf("slept %v, want [2.5s]", *slept)
	}
	if err := f.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inj.Active() != 0 {
		t.Fatal("Stop left the fault active")
	}
	_ = inj.Apply(context.Background(), "OrderList")
	if len(*slept) != 1 {
		t.Fatalf("a stopped fault still delays: %v", *slept)
	}
}

func TestErrorsFailTheirShare(t *testing.T) {
	// The ratio is compared with each request's random number: 0.1 < 0.5
	// fails, 0.9 does not.
	inj, reg, _ := setup(0.1, 0.9)
	start(t, reg, "errors", map[string]any{"ratio": 0.5, "message": "checkout down"})
	err := inj.Apply(context.Background(), "PlaceOrder")
	if !errors.Is(err, ErrInjected) || err.Error() != "injected by scenario: checkout down" {
		t.Fatalf("first request: %v, want the injected failure", err)
	}
	if err := inj.Apply(context.Background(), "GetOrders"); err != nil {
		t.Fatalf("second request: %v, want none (no entry_points means all, but 0.9 >= 0.5)", err)
	}
}

func TestNilInjectorInjectsNothing(t *testing.T) {
	var inj *Injector
	if err := inj.Apply(context.Background(), "OrderList"); err != nil || inj.Active() != 0 {
		t.Fatal(err)
	}
}

func TestLatencyEndsWithTheRequest(t *testing.T) {
	inj := &Injector{}
	reg := scenario.NewRegistry()
	Register(reg, inj, entryPoints)
	start(t, reg, "latency", map[string]any{"delay": "1m"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := inj.Apply(ctx, "OrderList"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the request's deadline", err)
	}
	if time.Since(began) > 5*time.Second {
		t.Fatal("the delay outlived the request")
	}
}

func TestParamsAreValidated(t *testing.T) {
	_, reg, _ := setup(0)
	cases := []struct {
		typ    string
		params map[string]any
		want   string
	}{
		{"latency", map[string]any{}, "delay is required"},
		{"latency", map[string]any{"delay": "fast"}, "positive duration"},
		{"latency", map[string]any{"delay": "-1s"}, "positive duration"},
		{"latency", map[string]any{"delay": "50s", "jitter": "20s"}, "at most 1m0s"},
		{"latency", map[string]any{"delay": "1s", "ratio": 0}, "ratio 0 must be"},
		{"latency", map[string]any{"delay": "1s", "entry_points": []any{"Home"}}, "entry point \"Home\""},
		{"latency", map[string]any{"delay": "1s", "speed": 2}, "unknown field \"speed\""},
		{"errors", map[string]any{}, "ratio is required"},
		{"errors", map[string]any{"ratio": 1.5}, "ratio 1.5 must be"},
		{"errors", map[string]any{"ratio": 1, "message": strings.Repeat("x", MaxMessageLen+1)}, "message must be at most"},
	}
	for _, c := range cases {
		_, err := reg.New(scenario.FaultSpec{Type: c.typ, Params: c.params})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %v: error %v, want %q", c.typ, c.params, err, c.want)
		}
	}
}
