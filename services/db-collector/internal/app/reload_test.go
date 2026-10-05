package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	heartbeatconfig "heartbeat/internal/config"
	"heartbeat/services/db-collector/internal/collectors"
)

// newReloadFixture starts a service with the given collectors declared.
func newReloadFixture(t *testing.T, pollers *fakePollers, collectors ...testCollector) (*service, http.Handler, string) {
	t.Helper()
	manager, path := newTestConfigManager(t)
	writeTestConfig(t, path, collectors...)
	if _, err := manager.Reload(); err != nil {
		t.Fatalf("reload fixture config: %v", err)
	}
	svc := newTestService(t, manager, pollers.run)
	return svc, routes(prometheus.NewRegistry(), svc), path
}

// adminConfig fetches GET /admin/config with the test token.
func adminConfig(t *testing.T, handler http.Handler) (int, adminConfigResponse) {
	t.Helper()
	rec := serve(handler, http.MethodGet, "/admin/config", "Bearer test-token")
	var body adminConfigResponse
	decode(t, rec, &body)
	return rec.Code, body
}

func adminReload(t *testing.T, handler http.Handler) (int, reloadResponse) {
	t.Helper()
	rec := serve(handler, http.MethodPost, "/admin/config/reload", "Bearer test-token")
	var body reloadResponse
	decode(t, rec, &body)
	return rec.Code, body
}

// stuckOnCancel reports healthy cycles until cancelled, then ignores the
// cancellation until release closes (like a hung driver call).
func stuckOnCancel(id string, release <-chan struct{}) pollerBehavior {
	return func(ctx context.Context, report func(collectors.CycleResult)) error {
		_ = healthyPoller(id, collectors.TargetOK)(ctx, report)
		<-release
		return ctx.Err()
	}
}

func TestReloadRestartsFailedButUnchangedCollector(t *testing.T) {
	pollers := newFakePollers()
	var runs atomic.Int32
	pollers.set("sql-a", func(ctx context.Context, report func(collectors.CycleResult)) error {
		if runs.Add(1) == 1 {
			panic("boom")
		}
		return healthyPoller("sql-a", collectors.TargetOK)(ctx, report)
	})
	svc, handler, _ := newReloadFixture(t, pollers, testCollector{id: "sql-a", env: "prod"})
	svc.lifecycle.mu.Lock()
	svc.lifecycle.backoff = restartBackoff{initial: time.Hour, max: time.Hour} // only a reload can restart it
	svc.lifecycle.mu.Unlock()
	eventually(t, time.Second, func() bool { return svc.lifecycle.needsRestart("sql-a") }, "collector did not crash")
	if rec := serve(handler, http.MethodGet, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("crashed collector left the pod ready: %s", rec.Body.String())
	}

	code, body := adminReload(t, handler) // config file unchanged
	if code != http.StatusOK || body.Summary.Restarted != 1 || body.Summary.Unchanged != 0 {
		t.Fatalf("reload: code=%d body=%+v", code, body)
	}
	eventually(t, time.Second, func() bool {
		return serve(handler, http.MethodGet, "/readyz", "").Code == http.StatusOK
	}, "collector not healthy after restart")
	if n := pollers.startCount("sql-a"); n != 2 {
		t.Fatalf("expected 2 poller starts, got %d", n)
	}
}

func TestAdminReloadTimesOutAndReportsFailedRollback(t *testing.T) {
	pollers := newFakePollers()
	release := make(chan struct{})
	pollers.set("sql-a", stuckOnCancel("sql-a", release))
	svc, handler, path := newReloadFixture(t, pollers, testCollector{id: "sql-a", env: "prod"})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	svc.timeouts.reload = 100 * time.Millisecond
	svc.timeouts.rollback = 100 * time.Millisecond
	eventually(t, time.Second, func() bool { return serve(handler, http.MethodGet, "/readyz", "").Code == http.StatusOK }, "fixture not ready")

	writeTestConfig(t, path, testCollector{id: "sql-a", env: "staging"})
	start := time.Now()
	code, body := adminReload(t, handler)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("reload hung for %s", elapsed)
	}
	if code != http.StatusInternalServerError || body.Result != "apply_failed" || body.RolledBack == nil || *body.RolledBack {
		t.Fatalf("expected apply failure with failed rollback: code=%d body=%+v", code, body)
	}
	if rec := serve(handler, http.MethodGet, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("divergence left the pod ready: %d %s", rec.Code, rec.Body.String())
	}
	if _, diag := adminConfig(t, handler); !diag.RuntimeDiverged || !diag.Readiness.RuntimeDiverged || diag.RollbackErr == "" {
		t.Fatalf("divergence not surfaced in diagnostics: %+v", diag)
	}

	// Once the hung poller exits, the next reload converges and clears it.
	pollers.set("sql-a", nil)
	close(release)
	eventually(t, time.Second, func() bool {
		state, _ := stateOf(svc.lifecycle, "sql-a")
		return state.Phase == phaseFailed
	}, "orphaned poller not marked failed: %+v", lazy(func() any { return svc.lifecycle.states() }))
	code, body = adminReload(t, handler)
	if code != http.StatusOK {
		t.Fatalf("recovery reload failed: %d %+v", code, body)
	}
	eventually(t, time.Second, func() bool { return serve(handler, http.MethodGet, "/readyz", "").Code == http.StatusOK }, "not ready after recovery")
	eventually(t, time.Second, func() bool { return pollers.startCount("sql-a") == 2 }, "replacement poller not started")
	pollers.mu.Lock()
	configs := pollers.configs["sql-a"]
	pollers.mu.Unlock()
	if got := configs[len(configs)-1].Environment; got != "staging" {
		t.Fatalf("runtime runs %q, want staging", got)
	}
}

func TestAdminReloadRollsBackToPreviousConfig(t *testing.T) {
	pollers := newFakePollers()
	var runs atomic.Int32
	pollers.set("sql-a", func(ctx context.Context, report func(collectors.CycleResult)) error {
		err := healthyPoller("sql-a", collectors.TargetOK)(ctx, report)
		if runs.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond) // slower than the reload deadline
		}
		return err
	})
	svc, handler, path := newReloadFixture(t, pollers, testCollector{id: "sql-a", env: "prod"}, testCollector{id: "sql-b", env: "prod"})
	svc.timeouts.reload = 100 * time.Millisecond
	svc.timeouts.rollback = 2 * time.Second
	active := svc.configManager.Snapshot().Config.Version

	writeTestConfig(t, path, testCollector{id: "sql-a", env: "staging"}, testCollector{id: "sql-c", env: "prod"})
	code, body := adminReload(t, handler)
	if code != http.StatusInternalServerError || body.RolledBack == nil || !*body.RolledBack {
		t.Fatalf("expected rolled-back apply failure: code=%d body=%+v", code, body)
	}
	if svc.configManager.Snapshot().Config.Version != active || svc.configManager.Snapshot().RuntimeDiverged {
		t.Fatal("snapshot must stay on the old config without divergence")
	}
	var ids []string
	for _, c := range svc.lifecycle.configs() {
		ids = append(ids, c.ID+"@"+c.Environment)
	}
	if len(ids) != 2 || ids[0] != "sql-a@prod" || ids[1] != "sql-b@prod" {
		t.Fatalf("runtime not rolled back: %v", ids)
	}
	eventually(t, time.Second, func() bool { return serve(handler, http.MethodGet, "/readyz", "").Code == http.StatusOK }, "not ready after rollback")
}

func TestPollersSurviveEndOfReloadRequest(t *testing.T) {
	pollers := newFakePollers()
	svc, handler, path := newReloadFixture(t, pollers, testCollector{id: "sql-a", env: "prod"})
	writeTestConfig(t, path, testCollector{id: "sql-a", env: "staging"}, testCollector{id: "sql-b", env: "prod"})

	reqCtx, cancelRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/admin/config/reload", nil).WithContext(reqCtx)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	cancelRequest()
	if rec.Code != http.StatusOK {
		t.Fatalf("reload failed: %d %s", rec.Code, rec.Body.String())
	}
	eventually(t, time.Second, func() bool { return pollers.startCount("sql-a") == 2 && pollers.startCount("sql-b") == 1 }, "pollers not started")
	time.Sleep(20 * time.Millisecond)
	for _, id := range []string{"sql-a", "sql-b"} {
		if err := pollers.lastCtx(id).Err(); err != nil {
			t.Fatalf("poller %s was cancelled with the request: %v", id, err)
		}
		if state, _ := stateOf(svc.lifecycle, id); state.Phase != phaseRunning {
			t.Fatalf("poller %s phase %s", id, state.Phase)
		}
	}
}

func TestAdminReloadBusyWhenLockHeldPastDeadline(t *testing.T) {
	pollers := newFakePollers()
	svc, handler, _ := newReloadFixture(t, pollers)
	svc.timeouts.reload = 50 * time.Millisecond
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = svc.configManager.ReloadApplying(func(heartbeatconfig.RuntimeConfig, heartbeatconfig.RuntimeConfig) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	code, body := adminReload(t, handler)
	close(release)
	<-done
	if code != http.StatusServiceUnavailable || body.Result != "busy" {
		t.Fatalf("expected busy 503, got %d %+v", code, body)
	}
}
