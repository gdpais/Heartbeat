package config

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrInvalidCandidate identifies reload failures caused by unreadable or
	// invalid candidate config files. The active snapshot is preserved.
	ErrInvalidCandidate = errors.New("invalid candidate config")
	// ErrReloadApply identifies reload failures caused by applying an otherwise
	// valid candidate to runtime systems. The active snapshot is preserved.
	ErrReloadApply = errors.New("apply candidate config")
	// ErrReloadBusy identifies reloads that could not acquire the reload lock
	// before their context ended because another reload was still applying.
	// Nothing was read or applied and the active snapshot is untouched.
	ErrReloadBusy = errors.New("another config reload is in progress")
)

// DefaultRollbackTimeout bounds how long ReconcileCollectors spends undoing a
// partially applied diff.
const DefaultRollbackTimeout = 15 * time.Second

// Snapshot is an immutable active configuration view plus reload diagnostics.
type Snapshot struct {
	Config        RuntimeConfig
	LoadedAt      time.Time
	LastReloadAt  time.Time
	LastReloadErr string
	// RuntimeDiverged is true when a reload failed to apply and rolling the
	// runtime back to Config also failed, so running collectors may no longer
	// match Config. It is cleared by the next successful reload.
	RuntimeDiverged bool
	// RollbackErr describes the most recent failed rollback while
	// RuntimeDiverged is true.
	RollbackErr string
}

// Manager owns the active runtime config. Reloads are fail-closed: an invalid
// candidate never replaces the current snapshot.
type Manager struct {
	path   string
	active atomic.Value
	// reloadLock serialises reloads. It is a one-slot semaphore rather than a
	// sync.Mutex so waiting for it can honour a context deadline.
	reloadLock chan struct{}
}

// NewManager loads the initial config and stores it as the active snapshot.
func NewManager(path string) (*Manager, error) {
	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		return nil, err
	}
	manager := &Manager{path: path, reloadLock: make(chan struct{}, 1)}
	now := time.Now().UTC()
	manager.active.Store(Snapshot{Config: cfg, LoadedAt: now, LastReloadAt: now})
	return manager, nil
}

// Snapshot returns the current immutable active snapshot. It never blocks on
// an in-progress reload.
func (m *Manager) Snapshot() Snapshot {
	return m.active.Load().(Snapshot)
}

// Reload validates and activates the candidate config from disk. If validation
// fails, the old active snapshot is preserved and returned with LastReloadErr.
func (m *Manager) Reload() (Snapshot, error) {
	return m.ReloadApplying(nil)
}

// ReloadApplying is ReloadApplyingContext without a deadline for acquiring the
// reload lock.
func (m *Manager) ReloadApplying(apply func(previous, next RuntimeConfig) error) (Snapshot, error) {
	return m.ReloadApplyingContext(context.Background(), apply)
}

// ReloadApplyingContext validates the candidate config, invokes apply while the
// old snapshot is still active, and only publishes the new snapshot if apply
// succeeds. Apply callbacks must treat both configs as read-only.
//
// Reloads are serialised: the reload lock is held for the whole
// load-apply-publish sequence so two reloads never reconcile the runtime
// concurrently. Waiting for the lock honours ctx; if ctx ends first the reload
// returns ErrReloadBusy without touching anything. ctx does not bound apply
// itself; callbacks should use their own (usually the same) context.
//
// When apply fails the old snapshot is kept. If the error is (or wraps) a
// *ReconcileError whose rollback succeeded, the runtime still matches the old
// snapshot. Any other apply error is treated conservatively as a divergence
// and recorded in Snapshot.RuntimeDiverged until a later reload succeeds.
func (m *Manager) ReloadApplyingContext(ctx context.Context, apply func(previous, next RuntimeConfig) error) (Snapshot, error) {
	select {
	case m.reloadLock <- struct{}{}:
	case <-ctx.Done():
		return m.Snapshot(), fmt.Errorf("%w: %w", ErrReloadBusy, ctx.Err())
	}
	defer func() { <-m.reloadLock }()

	current := m.Snapshot()
	now := time.Now().UTC()
	next, err := LoadRuntimeConfig(m.path)
	if err != nil {
		current.LastReloadAt = now
		current.LastReloadErr = err.Error()
		m.active.Store(current)
		return current, fmt.Errorf("%w: %v", ErrInvalidCandidate, err)
	}
	if apply != nil {
		if err := apply(current.Config, next); err != nil {
			current.LastReloadAt = now
			current.LastReloadErr = err.Error()
			if rollbackErr := unrolledBack(err); rollbackErr != nil {
				current.RuntimeDiverged = true
				current.RollbackErr = rollbackErr.Error()
			}
			m.active.Store(current)
			return current, fmt.Errorf("%w: %w", ErrReloadApply, err)
		}
	}
	snapshot := Snapshot{Config: next, LoadedAt: now, LastReloadAt: now}
	m.active.Store(snapshot)
	return snapshot, nil
}

// unrolledBack returns nil when err reports a successful rollback, and an
// error describing why the runtime may have diverged otherwise.
func unrolledBack(err error) error {
	var reconcileErr *ReconcileError
	if !errors.As(err, &reconcileErr) {
		return fmt.Errorf("apply failed without rollback information: %w", err)
	}
	return reconcileErr.RollbackErr
}

// CollectorDiff describes desired collector changes by stable collector ID.
type CollectorDiff struct {
	Added     []CollectorRuntimeConfig
	Updated   []CollectorRuntimeConfig
	Removed   []string
	Unchanged []string
	// Previous holds the previous config of every Updated, Removed, and
	// Unchanged collector so a partially applied diff can be rolled back.
	Previous map[string]CollectorRuntimeConfig
}

// DiffCollectors compares two configs for one collector kind.
func DiffCollectors(previous, next RuntimeConfig, kind string) CollectorDiff {
	oldByID := collectorsByID(previous.EnabledCollectors(kind))
	newByID := collectorsByID(next.EnabledCollectors(kind))
	diff := CollectorDiff{Previous: oldByID}
	for id, nextCollector := range newByID {
		oldCollector, exists := oldByID[id]
		switch {
		case !exists:
			diff.Added = append(diff.Added, nextCollector)
		case collectorsEqual(oldCollector, nextCollector):
			diff.Unchanged = append(diff.Unchanged, id)
		default:
			diff.Updated = append(diff.Updated, nextCollector)
		}
	}
	for id := range oldByID {
		if _, exists := newByID[id]; !exists {
			diff.Removed = append(diff.Removed, id)
		}
	}
	sortDiff(&diff)
	return diff
}

// CollectorLifecycle is implemented by runtime collector supervisors that can
// apply desired config transitions. Implementations must be safe for
// concurrent calls on distinct collector IDs: reconciliation cancels and waits
// for several collectors in parallel. Start returning nil means the collector
// was launched, not that it has completed a cycle.
type CollectorLifecycle interface {
	Start(context.Context, CollectorRuntimeConfig) error
	Update(context.Context, CollectorRuntimeConfig) error
	Drain(context.Context, string) error
	Stop(context.Context, string) error
	Status(string) CollectorStatus
}

// CollectorStatus is a small status value surfaced by lifecycle implementations.
type CollectorStatus struct {
	ID      string
	Phase   string
	Version string
	Error   string
}

// ReconcileError reports a reconciliation that failed part-way and the
// outcome of rolling the already-touched collectors back.
type ReconcileError struct {
	// Err joins every forward step that failed.
	Err error
	// RollbackErr joins every undo step that failed; nil means the runtime was
	// restored to the previous config.
	RollbackErr error
}

// Error implements error.
func (e *ReconcileError) Error() string {
	if e.RollbackErr == nil {
		return fmt.Sprintf("%v (rolled back to previous config)", e.Err)
	}
	return fmt.Sprintf("%v (rollback failed: %v)", e.Err, e.RollbackErr)
}

// Unwrap exposes the forward and rollback errors to errors.Is and errors.As.
func (e *ReconcileError) Unwrap() []error {
	if e.RollbackErr == nil {
		return []error{e.Err}
	}
	return []error{e.Err, e.RollbackErr}
}

// RolledBack reports whether every undo step succeeded.
func (e *ReconcileError) RolledBack() bool { return e.RollbackErr == nil }

// ReconcileCollectors applies diff with rollback bounded by
// DefaultRollbackTimeout. See ReconcileWithRollback.
func ReconcileCollectors(ctx context.Context, lifecycle CollectorLifecycle, diff CollectorDiff) error {
	return ReconcileWithRollback(ctx, lifecycle, diff, DefaultRollbackTimeout)
}

// reconcileStep is one collector transition and how to undo it.
type reconcileStep struct {
	do   func(context.Context) error
	undo func(context.Context) error
}

// ReconcileWithRollback applies diff to lifecycle in three phases: Added
// collectors are started, then Updated collectors are replaced concurrently
// (all old pollers are cancelled and awaited together), then Removed
// collectors are drained and stopped concurrently. ctx bounds the forward
// phases.
//
// If any step fails, later phases are skipped and every step already touched,
// including the failed ones because they may have partially applied, is
// undone in reverse phase order: Removed collectors are started again with
// their previous config, Updated collectors are updated back to their previous
// config, and Added collectors are stopped. Undo runs under a fresh context
// bounded by rollbackTimeout (it must work even when ctx has expired). The
// returned *ReconcileError reports whether rollback succeeded.
func ReconcileWithRollback(ctx context.Context, lifecycle CollectorLifecycle, diff CollectorDiff, rollbackTimeout time.Duration) error {
	phases := [][]reconcileStep{
		addSteps(lifecycle, diff),
		updateSteps(lifecycle, diff),
		removeSteps(lifecycle, diff),
	}
	var touched [][]reconcileStep
	var forwardErr error
	for i, phase := range phases {
		// Added collectors start sequentially: Start only launches a goroutine,
		// so ordering keeps rollback deterministic at no cost.
		errs := runSteps(ctx, phase, i > 0, func(step reconcileStep) func(context.Context) error { return step.do })
		touched = append(touched, phase)
		if forwardErr = errors.Join(errs...); forwardErr != nil {
			break
		}
	}
	if forwardErr == nil {
		return nil
	}

	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	var undoErrs []error
	for i := len(touched) - 1; i >= 0; i-- {
		phase := reversed(touched[i])
		undoErrs = append(undoErrs, runSteps(rollbackCtx, phase, i > 0, func(step reconcileStep) func(context.Context) error { return step.undo })...)
	}
	return &ReconcileError{Err: forwardErr, RollbackErr: errors.Join(undoErrs...)}
}

// runSteps executes fn(step) for every step, concurrently when parallel is
// set, and returns the non-nil errors in step order.
func runSteps(ctx context.Context, steps []reconcileStep, parallel bool, fn func(reconcileStep) func(context.Context) error) []error {
	results := make([]error, len(steps))
	if parallel {
		var wg sync.WaitGroup
		for i, step := range steps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = fn(step)(ctx)
			}()
		}
		wg.Wait()
	} else {
		for i, step := range steps {
			results[i] = fn(step)(ctx)
		}
	}
	var errs []error
	for _, err := range results {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func addSteps(lifecycle CollectorLifecycle, diff CollectorDiff) []reconcileStep {
	steps := make([]reconcileStep, 0, len(diff.Added))
	for _, collector := range diff.Added {
		steps = append(steps, reconcileStep{
			do: func(ctx context.Context) error {
				return wrapStep("start collector", collector.ID, lifecycle.Start(ctx, collector))
			},
			undo: func(ctx context.Context) error {
				return wrapStep("rollback: stop added collector", collector.ID, lifecycle.Stop(ctx, collector.ID))
			},
		})
	}
	return steps
}

func updateSteps(lifecycle CollectorLifecycle, diff CollectorDiff) []reconcileStep {
	steps := make([]reconcileStep, 0, len(diff.Updated))
	for _, collector := range diff.Updated {
		previous, ok := diff.Previous[collector.ID]
		steps = append(steps, reconcileStep{
			do: func(ctx context.Context) error {
				return wrapStep("update collector", collector.ID, lifecycle.Update(ctx, collector))
			},
			undo: func(ctx context.Context) error {
				if !ok {
					return fmt.Errorf("rollback: restore collector %s: previous config unknown", collector.ID)
				}
				return wrapStep("rollback: restore collector", collector.ID, lifecycle.Update(ctx, previous))
			},
		})
	}
	return steps
}

func removeSteps(lifecycle CollectorLifecycle, diff CollectorDiff) []reconcileStep {
	steps := make([]reconcileStep, 0, len(diff.Removed))
	for _, id := range diff.Removed {
		previous, ok := diff.Previous[id]
		steps = append(steps, reconcileStep{
			do: func(ctx context.Context) error {
				if err := lifecycle.Drain(ctx, id); err != nil {
					return wrapStep("drain collector", id, err)
				}
				return wrapStep("stop collector", id, lifecycle.Stop(ctx, id))
			},
			undo: func(ctx context.Context) error {
				if !ok {
					return fmt.Errorf("rollback: restart removed collector %s: previous config unknown", id)
				}
				return wrapStep("rollback: restart removed collector", id, lifecycle.Start(ctx, previous))
			},
		})
	}
	return steps
}

// wrapStep annotates a lifecycle error with the action and collector ID.
func wrapStep(action, id string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %s: %w", action, id, err)
}

// reversed returns a reversed copy of steps.
func reversed(steps []reconcileStep) []reconcileStep {
	out := make([]reconcileStep, len(steps))
	for i, step := range steps {
		out[len(steps)-1-i] = step
	}
	return out
}

func collectorsByID(collectors []CollectorRuntimeConfig) map[string]CollectorRuntimeConfig {
	out := make(map[string]CollectorRuntimeConfig, len(collectors))
	for _, collector := range collectors {
		out[collector.ID] = collector
	}
	return out
}

func collectorsEqual(a, b CollectorRuntimeConfig) bool {
	return reflect.DeepEqual(a, b)
}

func sortDiff(diff *CollectorDiff) {
	sortCollectors(diff.Added)
	sortCollectors(diff.Updated)
	sortStrings(diff.Removed)
	sortStrings(diff.Unchanged)
}

func sortCollectors(values []CollectorRuntimeConfig) {
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
}

func sortStrings(values []string) {
	sort.Strings(values)
}
