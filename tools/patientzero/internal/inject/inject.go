// Package inject holds PatientZero's in-process faults: latency and errors
// on its entry points, switched on and off by the scenario runner. The entry
// points ask the Injector before their business SQL, so an injected delay
// shows in the log rows' Duration and an injected failure writes an error row
// under the request's Request_Key, as a real failure would.
package inject

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"heartbeat/tools/patientzero/internal/scenario"
)

// ErrInjected marks a failure a scenario injected.
var ErrInjected = errors.New("injected by scenario")

// Limits on the parameters, repeated in scenarios/scenario.schema.json.
const (
	MaxDelay      = time.Minute
	MaxMessageLen = 200
)

type rule struct {
	entryPoints []string // empty: every entry point
	ratio       float64
	delay       time.Duration
	jitter      time.Duration
	message     string // non-empty for an error rule
}

func (r *rule) applies(entryPoint string) bool {
	return len(r.entryPoints) == 0 || slices.Contains(r.entryPoints, entryPoint)
}

// Injector is the set of active faults. The zero value has none; a nil
// *Injector is valid and injects nothing.
type Injector struct {
	mu    sync.RWMutex
	rules map[*rule]struct{}
	// Float returns a number in [0, 1); math/rand/v2 when nil.
	Float func() float64
	// Sleep waits d or until ctx ends; a timer when nil.
	Sleep func(ctx context.Context, d time.Duration) error
}

func (i *Injector) add(r *rule) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.rules == nil {
		i.rules = map[*rule]struct{}{}
	}
	i.rules[r] = struct{}{}
}

func (i *Injector) remove(r *rule) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.rules, r)
}

// Active is how many faults are switched on.
func (i *Injector) Active() int {
	if i == nil {
		return 0
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.rules)
}

// Apply runs the active faults for one request to entryPoint: it waits out
// every latency fault that hits the request, then returns an error wrapping
// ErrInjected if an error fault hits it. A request that ends while waiting
// returns its context's error.
func (i *Injector) Apply(ctx context.Context, entryPoint string) error {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	var delay time.Duration
	var failure string
	for r := range i.rules {
		if !r.applies(entryPoint) || i.float() >= r.ratio {
			continue
		}
		if r.message != "" {
			if failure == "" {
				failure = r.message
			}
			continue
		}
		d := r.delay
		if r.jitter > 0 {
			d += time.Duration(i.float() * float64(r.jitter))
		}
		delay += d
	}
	i.mu.RUnlock()
	if delay > 0 {
		if err := i.sleep(ctx, delay); err != nil {
			return err
		}
	}
	if failure != "" {
		return fmt.Errorf("%w: %s", ErrInjected, failure)
	}
	return nil
}

func (i *Injector) float() float64 {
	if i.Float != nil {
		return i.Float()
	}
	return rand.Float64()
}

func (i *Injector) sleep(ctx context.Context, d time.Duration) error {
	if i.Sleep != nil {
		return i.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fault switches one rule on and off.
type fault struct {
	inj  *Injector
	rule *rule
}

func (f *fault) Start(context.Context) error { f.inj.add(f.rule); return nil }
func (f *fault) Stop(context.Context) error  { f.inj.remove(f.rule); return nil }

// Register adds the latency and errors fault types, acting on inj, to reg.
// entryPoints are the names a fault may target: PatientZero's entry points,
// as Heartbeat's operation label names them.
func Register(reg *scenario.Registry, inj *Injector, entryPoints []string) {
	reg.Register(scenario.FaultType{
		Name:        "latency",
		Scope:       scenario.ScopeInProcess,
		Description: "Delays requests to PatientZero's entry points before their business SQL",
		New: func(params map[string]any) (scenario.Fault, error) {
			var p struct {
				EntryPoints []string `json:"entry_points"`
				Delay       string   `json:"delay"`
				Jitter      string   `json:"jitter"`
				Ratio       *float64 `json:"ratio"`
			}
			if err := scenario.DecodeParams(params, &p); err != nil {
				return nil, err
			}
			r := &rule{ratio: 1}
			var err error
			if r.entryPoints, err = checkEntryPoints(p.EntryPoints, entryPoints); err != nil {
				return nil, err
			}
			if r.delay, err = duration("delay", p.Delay, true); err != nil {
				return nil, err
			}
			if r.jitter, err = duration("jitter", p.Jitter, false); err != nil {
				return nil, err
			}
			if r.delay+r.jitter > MaxDelay {
				return nil, fmt.Errorf("delay plus jitter must be at most %s", MaxDelay)
			}
			if p.Ratio != nil {
				if r.ratio, err = ratio(*p.Ratio); err != nil {
					return nil, err
				}
			}
			return &fault{inj: inj, rule: r}, nil
		},
	})
	reg.Register(scenario.FaultType{
		Name:        "errors",
		Scope:       scenario.ScopeInProcess,
		Description: "Fails a share of the requests to PatientZero's entry points, with an error row",
		New: func(params map[string]any) (scenario.Fault, error) {
			var p struct {
				EntryPoints []string `json:"entry_points"`
				Ratio       *float64 `json:"ratio"`
				Message     string   `json:"message"`
			}
			if err := scenario.DecodeParams(params, &p); err != nil {
				return nil, err
			}
			r := &rule{message: strings.TrimSpace(p.Message)}
			var err error
			if r.entryPoints, err = checkEntryPoints(p.EntryPoints, entryPoints); err != nil {
				return nil, err
			}
			if p.Ratio == nil {
				return nil, errors.New("ratio is required")
			}
			if r.ratio, err = ratio(*p.Ratio); err != nil {
				return nil, err
			}
			if r.message == "" {
				r.message = "scenario fault"
			}
			if len(r.message) > MaxMessageLen {
				return nil, fmt.Errorf("message must be at most %d bytes", MaxMessageLen)
			}
			return &fault{inj: inj, rule: r}, nil
		},
	})
}

func checkEntryPoints(got, known []string) ([]string, error) {
	for _, e := range got {
		if !slices.Contains(known, e) {
			return nil, fmt.Errorf("entry point %q is not one of %s", e, strings.Join(known, ", "))
		}
	}
	return slices.Clone(got), nil
}

func duration(name, s string, required bool) (time.Duration, error) {
	if s == "" {
		if required {
			return 0, fmt.Errorf("%s is required", name)
		}
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 || (required && d == 0) {
		return 0, fmt.Errorf("%s %q must be a positive duration such as 500ms", name, s)
	}
	return d, nil
}

func ratio(v float64) (float64, error) {
	if !(v > 0 && v <= 1) {
		return 0, fmt.Errorf("ratio %v must be more than 0 and at most 1", v)
	}
	return v, nil
}
