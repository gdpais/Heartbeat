package collectors

import "time"

// CycleResult summarises one Runner.RunOnce cycle for a collector. Pollers
// hand it to their Report callback so lifecycle supervisors can derive
// readiness and freshness without parsing errors.
type CycleResult struct {
	CollectorID string
	Started     time.Time
	Finished    time.Time
	// Targets holds one entry per scheduled target, including targets skipped
	// because they are backing off after earlier failures.
	Targets []TargetResult
}

// TargetState is the outcome of one target within a cycle.
type TargetState string

const (
	TargetOK      TargetState = "ok"
	TargetFailed  TargetState = "failed"
	TargetBackoff TargetState = "backoff"
)

// TargetResult is the per-target outcome of one cycle.
type TargetResult struct {
	Target string
	State  TargetState
	// Err joins every probe error for the target in this cycle when State is
	// TargetFailed, and in its last failed cycle when State is TargetBackoff;
	// nil when State is TargetOK. It is for logs and in-process decisions only
	// and must not be served on unauthenticated endpoints.
	Err                 error
	ConsecutiveFailures int
	LastSuccess         time.Time
	// NextAttempt is set when State is TargetBackoff or TargetFailed.
	NextAttempt time.Time
}

// Healthy reports whether every target in the cycle succeeded.
func (c CycleResult) Healthy() bool {
	for _, target := range c.Targets {
		if target.State != TargetOK {
			return false
		}
	}
	return true
}
