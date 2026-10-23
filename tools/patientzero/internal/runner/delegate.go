package runner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Cluster faults act on other workloads (restart a pod, stop SQL Server),
// which PatientZero has no access to, on purpose. A run that has them is
// started with a host agent attached: the command that started it, on the
// host, with the kubeconfig and Docker. When a step's cluster fault starts
// or stops, the runner posts a FaultRequest and waits; the agent sees it in
// the run's status, applies it, and answers on the admin endpoint. The run's
// clock, its checks and its journal stay here.

// Errors of the delegate.
var (
	ErrNoAgent       = errors.New("no host agent is attached to the run")
	ErrNoSuchRequest = errors.New("no such fault request is pending")
)

// DefaultAgentTimeout bounds how long a cluster fault waits for the host
// agent: a restart or a recovery waits for the workload to be ready.
const DefaultAgentTimeout = 5 * time.Minute

// Fault request actions.
const (
	ActionStart = "start"
	ActionStop  = "stop"
)

// FaultRequest is a cluster fault the runner asks the host agent to apply
// (start) or remove (stop).
type FaultRequest struct {
	ID     string         `json:"id"`
	Action string         `json:"action"`
	Type   string         `json:"type"`
	Params map[string]any `json:"params,omitempty"`
	Since  time.Time      `json:"since"`
}

// Delegate hands a run's cluster faults to its host agent, one at a time.
type Delegate struct {
	// Timeout is how long a request waits for the agent; DefaultAgentTimeout
	// when zero.
	Timeout time.Duration
	Now     func() time.Time

	mu       sync.Mutex
	attached bool
	seq      int
	pending  *FaultRequest
	done     chan error
}

// attach makes a host agent responsible for the next run's cluster faults.
func (d *Delegate) attach() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attached = true
}

// Detach says the host agent is gone: the pending request and every later
// one fail at once, so a stopping run does not wait for an agent that
// will not answer. The agent removes the faults it applied itself.
func (d *Delegate) Detach() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attached = false
	if d.pending != nil {
		d.done <- ErrNoAgent
		d.pending, d.done = nil, nil
	}
}

// Pending is the request the agent has to answer, if any.
func (d *Delegate) Pending() *FaultRequest {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending == nil {
		return nil
	}
	r := *d.pending
	return &r
}

// Complete is the agent's answer to request id: applied, or failed with
// msg.
func (d *Delegate) Complete(id, msg string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending == nil || d.pending.ID != id {
		return ErrNoSuchRequest
	}
	var err error
	if msg != "" {
		err = fmt.Errorf("host agent: %s", msg)
	}
	d.done <- err
	d.pending, d.done = nil, nil
	return nil
}

// Do asks the agent to apply or remove a cluster fault and waits for its
// answer, the timeout or ctx.
func (d *Delegate) Do(ctx context.Context, action, typ string, params map[string]any) error {
	if d == nil {
		return ErrNoAgent
	}
	d.mu.Lock()
	if !d.attached {
		d.mu.Unlock()
		return ErrNoAgent
	}
	if d.pending != nil {
		d.mu.Unlock()
		return errors.New("another cluster fault request is pending")
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	d.seq++
	req := &FaultRequest{ID: strconv.Itoa(d.seq), Action: action, Type: typ, Params: params, Since: now().UTC()}
	done := make(chan error, 1)
	d.pending, d.done = req, done
	d.mu.Unlock()

	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultAgentTimeout
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		d.drop(req.ID)
		return fmt.Errorf("the host agent did not %s the %s fault within %s", action, typ, timeout)
	case <-ctx.Done():
		d.drop(req.ID)
		return ctx.Err()
	}
}

func (d *Delegate) drop(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending != nil && d.pending.ID == id {
		d.pending, d.done = nil, nil
	}
}

// end releases the agent when its run ends.
func (d *Delegate) end() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attached = false
	d.pending, d.done = nil, nil
}
