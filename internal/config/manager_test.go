package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// recordingLifecycle is a CollectorLifecycle that records every call and can
// be told to fail specific calls.
type recordingLifecycle struct {
	mu    sync.Mutex
	calls []string
	// configs records the config passed to the latest Start/Update per ID.
	configs map[string]CollectorRuntimeConfig
	// fail maps "method id" (e.g. "stop c") to the error to return.
	fail map[string]error
	// ctxErrs records ctx.Err() observed by each call.
	ctxErrs map[string]error
	// hook, when set, runs before each call returns.
	hook func(call string)
}

func newRecordingLifecycle() *recordingLifecycle {
	return &recordingLifecycle{configs: map[string]CollectorRuntimeConfig{}, fail: map[string]error{}, ctxErrs: map[string]error{}}
}

func (l *recordingLifecycle) record(ctx context.Context, call string) error {
	if l.hook != nil {
		l.hook(call)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
	l.ctxErrs[call] = ctx.Err()
	return l.fail[call]
}

func (l *recordingLifecycle) Start(ctx context.Context, c CollectorRuntimeConfig) error {
	err := l.record(ctx, "start "+c.ID)
	if err == nil {
		l.mu.Lock()
		l.configs[c.ID] = c
		l.mu.Unlock()
	}
	return err
}

func (l *recordingLifecycle) Update(ctx context.Context, c CollectorRuntimeConfig) error {
	err := l.record(ctx, "update "+c.ID+"@"+c.Environment)
	if err == nil {
		l.mu.Lock()
		l.configs[c.ID] = c
		l.mu.Unlock()
	}
	return err
}

func (l *recordingLifecycle) Drain(ctx context.Context, id string) error {
	return l.record(ctx, "drain "+id)
}

func (l *recordingLifecycle) Stop(ctx context.Context, id string) error {
	err := l.record(ctx, "stop "+id)
	if err == nil {
		l.mu.Lock()
		delete(l.configs, id)
		l.mu.Unlock()
	}
	return err
}

func (l *recordingLifecycle) Status(id string) CollectorStatus {
	return CollectorStatus{ID: id, Phase: "running"}
}

func sqlCollector(id, env string) CollectorRuntimeConfig {
	return CollectorRuntimeConfig{ID: id, Kind: "sqlserver", Enabled: true, Environment: env, ScrapeInterval: time.Second}
}

func TestDiffCollectorsRecordsPreviousConfigs(t *testing.T) {
	old := RuntimeConfig{Collectors: []CollectorRuntimeConfig{sqlCollector("a", "prod"), sqlCollector("b", "prod")}}
	next := RuntimeConfig{Collectors: []CollectorRuntimeConfig{sqlCollector("b", "staging"), sqlCollector("c", "prod")}}
	diff := DiffCollectors(old, next, "sqlserver")
	if diff.Previous["a"].Environment != "prod" || diff.Previous["b"].Environment != "prod" {
		t.Fatalf("previous configs not recorded: %#v", diff.Previous)
	}
	if _, ok := diff.Previous["c"]; ok {
		t.Fatal("added collector must not have a previous config")
	}
}

func TestReconcileWithRollbackUndoesTouchedStepsInReverse(t *testing.T) {
	lifecycle := newRecordingLifecycle()
	lifecycle.configs["b"] = sqlCollector("b", "old")
	lifecycle.configs["c"] = sqlCollector("c", "old")
	lifecycle.fail["stop c"] = errors.New("poller did not stop")
	diff := CollectorDiff{
		Added:    []CollectorRuntimeConfig{sqlCollector("a", "new")},
		Updated:  []CollectorRuntimeConfig{sqlCollector("b", "new")},
		Removed:  []string{"c"},
		Previous: map[string]CollectorRuntimeConfig{"b": sqlCollector("b", "old"), "c": sqlCollector("c", "old")},
	}

	err := ReconcileWithRollback(context.Background(), lifecycle, diff, time.Second)
	var reconcileErr *ReconcileError
	if !errors.As(err, &reconcileErr) {
		t.Fatalf("expected *ReconcileError, got %v", err)
	}
	if !reconcileErr.RolledBack() {
		t.Fatalf("expected successful rollback, got %v", reconcileErr.RollbackErr)
	}
	want := []string{
		"start a", "update b@new", "drain c", "stop c", // forward
		"start c", "update b@old", "stop a", // undo, reverse phase order
	}
	if !reflect.DeepEqual(lifecycle.calls, want) {
		t.Fatalf("calls = %v, want %v", lifecycle.calls, want)
	}
	wantConfigs := map[string]CollectorRuntimeConfig{"b": sqlCollector("b", "old"), "c": sqlCollector("c", "old")}
	if !reflect.DeepEqual(lifecycle.configs, wantConfigs) {
		t.Fatalf("runtime not restored: %#v", lifecycle.configs)
	}
}

func TestReconcileWithRollbackStopsAtFirstFailingPhase(t *testing.T) {
	lifecycle := newRecordingLifecycle()
	lifecycle.fail["start b"] = errors.New("lifecycle closed")
	diff := CollectorDiff{
		Added:    []CollectorRuntimeConfig{sqlCollector("a", "new"), sqlCollector("b", "new")},
		Removed:  []string{"z"},
		Previous: map[string]CollectorRuntimeConfig{"z": sqlCollector("z", "old")},
	}
	err := ReconcileWithRollback(context.Background(), lifecycle, diff, time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	want := []string{"start a", "start b", "stop b", "stop a"}
	if !reflect.DeepEqual(lifecycle.calls, want) {
		t.Fatalf("calls = %v, want %v", lifecycle.calls, want)
	}
}

func TestReconcileWithRollbackReportsRollbackFailure(t *testing.T) {
	lifecycle := newRecordingLifecycle()
	lifecycle.fail["update b@new"] = context.DeadlineExceeded
	lifecycle.fail["update b@old"] = errors.New("still stopping")
	diff := CollectorDiff{
		Updated:  []CollectorRuntimeConfig{sqlCollector("b", "new")},
		Previous: map[string]CollectorRuntimeConfig{"b": sqlCollector("b", "old")},
	}
	err := ReconcileWithRollback(context.Background(), lifecycle, diff, time.Second)
	var reconcileErr *ReconcileError
	if !errors.As(err, &reconcileErr) || reconcileErr.RolledBack() {
		t.Fatalf("expected failed rollback, got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forward error not wrapped: %v", err)
	}
}

func TestReconcileRollbackRunsAfterForwardContextExpires(t *testing.T) {
	lifecycle := newRecordingLifecycle()
	ctx, cancel := context.WithCancel(context.Background())
	lifecycle.hook = func(call string) {
		if call == "update b@new" {
			cancel()
		}
	}
	lifecycle.fail["update b@new"] = context.Canceled
	diff := CollectorDiff{
		Updated:  []CollectorRuntimeConfig{sqlCollector("b", "new")},
		Previous: map[string]CollectorRuntimeConfig{"b": sqlCollector("b", "old")},
	}
	_ = ReconcileWithRollback(ctx, lifecycle, diff, time.Second)
	if err := lifecycle.ctxErrs["update b@old"]; err != nil {
		t.Fatalf("rollback ran with an expired context: %v", err)
	}
}

func TestReconcileUpdatesCollectorsConcurrently(t *testing.T) {
	lifecycle := newRecordingLifecycle()
	var arrived sync.WaitGroup
	arrived.Add(2)
	release := make(chan struct{})
	go func() {
		arrived.Wait()
		close(release)
	}()
	lifecycle.hook = func(string) {
		arrived.Done()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
	}
	diff := CollectorDiff{
		Updated:  []CollectorRuntimeConfig{sqlCollector("a", "new"), sqlCollector("b", "new")},
		Previous: map[string]CollectorRuntimeConfig{"a": sqlCollector("a", "old"), "b": sqlCollector("b", "old")},
	}
	start := time.Now()
	if err := ReconcileWithRollback(context.Background(), lifecycle, diff, time.Second); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("updates ran sequentially (took %s)", elapsed)
	}
}

func writeManagerConfig(t *testing.T, path, env string) {
	t.Helper()
	content := fmt.Sprintf(`grafana:
  base_url: http://grafana:3000
loki:
  base_url: http://loki:3100
alertmanager:
  base_url: http://alertmanager:9093
collectors:
  - id: sql-prod
    kind: sqlserver
    enabled: true
    credential_ref: kv/sql-prod
    config:
      environment: %s
`, env)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReloadApplyingRecordsDivergenceUntilSuccessfulReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integrations.yaml")
	writeManagerConfig(t, path, "prod")
	manager, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	writeManagerConfig(t, path, "staging")

	rolledBack := func(RuntimeConfig, RuntimeConfig) error {
		return &ReconcileError{Err: errors.New("update failed")}
	}
	if _, err := manager.ReloadApplying(rolledBack); !errors.Is(err, ErrReloadApply) {
		t.Fatalf("expected ErrReloadApply, got %v", err)
	}
	if manager.Snapshot().RuntimeDiverged {
		t.Fatal("successful rollback must not mark the runtime diverged")
	}

	diverged := func(RuntimeConfig, RuntimeConfig) error {
		return &ReconcileError{Err: errors.New("update failed"), RollbackErr: errors.New("restore failed")}
	}
	if _, err := manager.ReloadApplying(diverged); err == nil {
		t.Fatal("expected apply error")
	}
	snapshot := manager.Snapshot()
	if !snapshot.RuntimeDiverged || snapshot.RollbackErr == "" {
		t.Fatalf("divergence not recorded: %#v", snapshot)
	}

	// An invalid candidate keeps the divergence flag: nothing was repaired.
	if err := os.WriteFile(path, []byte("loki: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reload(); !errors.Is(err, ErrInvalidCandidate) {
		t.Fatalf("expected ErrInvalidCandidate, got %v", err)
	}
	if !manager.Snapshot().RuntimeDiverged {
		t.Fatal("invalid candidate cleared the divergence flag")
	}

	writeManagerConfig(t, path, "staging")
	if _, err := manager.ReloadApplying(func(RuntimeConfig, RuntimeConfig) error { return nil }); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if manager.Snapshot().RuntimeDiverged || manager.Snapshot().RollbackErr != "" {
		t.Fatal("successful reload did not clear divergence")
	}
}

func TestReloadApplyingContextHonoursDeadlineWhileLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integrations.yaml")
	writeManagerConfig(t, path, "prod")
	manager, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = manager.ReloadApplying(func(RuntimeConfig, RuntimeConfig) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = manager.ReloadApplyingContext(ctx, func(RuntimeConfig, RuntimeConfig) error {
		t.Error("apply must not run while another reload holds the lock")
		return nil
	})
	if !errors.Is(err, ErrReloadBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected ErrReloadBusy wrapping deadline, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("lock wait ignored the context deadline")
	}
	// Snapshot reads never block on the reload lock.
	_ = manager.Snapshot()
	close(release)
	<-firstDone
}
