package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	heartbeatconfig "heartbeat/internal/config"
	"heartbeat/services/db-collector/internal/collectors"
)

func TestEvaluateReadiness(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	cfg := collectorConfig("sql-a", "prod") // 1s scrape interval
	running := func(started, finished time.Time, targets ...collectors.TargetResult) collectorState {
		state := collectorState{Collector: cfg, Phase: phaseRunning, Started: started}
		if !finished.IsZero() {
			state.HasCycle = true
			state.LastCycle = collectors.CycleResult{CollectorID: "sql-a", Finished: finished, Targets: targets}
		}
		return state
	}
	failedTarget := collectors.TargetResult{Target: "core-db", State: collectors.TargetFailed, Err: errors.New("login failed"), ConsecutiveFailures: 4}
	for _, tc := range []struct {
		name        string
		initialized bool
		warm        bool
		snapshot    heartbeatconfig.Snapshot
		state       collectorState
		wantReady   bool
		wantReason  string
	}{
		{name: "healthy", initialized: true, warm: true, state: running(now.Add(-time.Minute), now.Add(-time.Second)), wantReady: true},
		{name: "not initialized", state: running(now, now), wantReason: "initial collector reconcile not complete"},
		{name: "before first cycle during warm-up", initialized: true, state: running(now, time.Time{}), wantReason: "has not completed its first cycle"},
		{name: "new collector after warm-up within window", initialized: true, warm: true, state: running(now.Add(-5*time.Second), time.Time{}), wantReady: true},
		{name: "stale since start", initialized: true, warm: true, state: running(now.Add(-13*time.Second), time.Time{}), wantReason: "is stale"},
		{name: "stale since last cycle", initialized: true, warm: true, state: running(now.Add(-time.Hour), now.Add(-13*time.Second)), wantReason: "is stale"},
		{name: "target failures stay ready", initialized: true, warm: true, state: running(now.Add(-time.Minute), now, failedTarget), wantReady: true},
		{name: "rejected candidate stays ready", initialized: true, warm: true, snapshot: heartbeatconfig.Snapshot{LastReloadErr: "parse runtime config: bad yaml"}, state: running(now.Add(-time.Minute), now), wantReady: true},
		{name: "diverged runtime", initialized: true, warm: true, snapshot: heartbeatconfig.Snapshot{RuntimeDiverged: true}, state: running(now.Add(-time.Minute), now), wantReason: "rollback failed"},
		{name: "backing off after crash", initialized: true, warm: true, state: collectorState{Collector: cfg, Phase: phaseBackingOff, Crashes: 1}, wantReason: "poller is backing_off"},
		{name: "failed", initialized: true, warm: true, state: collectorState{Collector: cfg, Phase: phaseFailed, Crashes: 5}, wantReason: "poller is failed"},
		{name: "draining is not judged", initialized: true, state: collectorState{Collector: cfg, Phase: phaseDraining, Started: now.Add(-time.Hour)}, wantReady: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, _ := evaluateReadiness(readinessInput{
				initialized: tc.initialized, warm: tc.warm, snapshot: tc.snapshot,
				collectors: []collectorState{tc.state}, grace: 10 * time.Second, now: now,
			})
			if ready := report.httpStatus() == http.StatusOK; ready != tc.wantReady {
				t.Fatalf("ready=%v want %v (reasons %v)", ready, tc.wantReady, report.Reasons)
			}
			if tc.wantReason != "" && !strings.Contains(strings.Join(report.Reasons, "; "), tc.wantReason) {
				t.Fatalf("reasons %v missing %q", report.Reasons, tc.wantReason)
			}
			if tc.wantReady && (report.Status != statusReady || len(report.Reasons) != 0) {
				t.Fatalf("ready report must have status ready and no reasons: %+v", report)
			}
		})
	}
}

func TestReadyzServesOnlyStatusAndDiagnosticsNeedTheToken(t *testing.T) {
	pollers := newFakePollers()
	pollers.set("sql-a", healthyPoller("sql-a", collectors.TargetFailed))
	_, handler, _ := newReloadFixture(t, pollers, testCollector{id: "sql-a", env: "prod"})

	var rec = serve(handler, http.MethodGet, "/readyz", "")
	eventually(t, time.Second, func() bool {
		rec = serve(handler, http.MethodGet, "/readyz", "")
		return rec.Code == http.StatusOK
	}, "target failures made the pod unready: %s", lazy(func() any { return rec.Body.String() }))
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ready"}` {
		t.Fatalf("/readyz must serve only its status, got %s", body)
	}

	// The same details, with the raw driver error, need the admin token.
	if rec := serve(handler, http.MethodGet, "/admin/config", ""); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "login failed") {
		t.Fatalf("diagnostics without the token: %d %s", rec.Code, rec.Body.String())
	}
	code, diag := adminConfig(t, handler)
	if code != http.StatusOK || diag.Readiness.Status != statusReady || len(diag.Readiness.Collectors) != 1 {
		t.Fatalf("unexpected diagnostics: %d %+v", code, diag.Readiness)
	}
	c := diag.Readiness.Collectors[0]
	if c.TargetsTotal != 1 || c.TargetsFailed != 1 || c.Targets[0].State != "failed" || c.Targets[0].ConsecutiveFailures != 3 || c.Targets[0].NextAttempt == nil {
		t.Fatalf("target details missing: %+v", c)
	}
	if !strings.Contains(c.Targets[0].Error, "login failed for user 'heartbeat_svc' on sql.example.internal:1433") {
		t.Fatalf("raw driver error missing from authenticated diagnostics: %q", c.Targets[0].Error)
	}
	if c.LastCycleFinished == nil || c.CycleAgeSeconds == nil || !c.Ready || c.Phase != phaseRunning {
		t.Fatalf("cycle details missing: %+v", c)
	}
}

func TestReadyzUnreadyUntilFirstCycleThenStaleAfterDeadline(t *testing.T) {
	pollers := newFakePollers()
	firstCycle := make(chan struct{})
	pollers.set("sql-a", func(ctx context.Context, report func(collectors.CycleResult)) error {
		<-firstCycle
		now := time.Now()
		report(collectors.CycleResult{CollectorID: "sql-a", Finished: now, Targets: []collectors.TargetResult{{Target: "core-db", State: collectors.TargetOK, LastSuccess: now}}})
		<-ctx.Done() // no further cycles: goes stale
		return ctx.Err()
	})
	svc, handler, _ := newReloadFixture(t, pollers, testCollector{id: "sql-a", env: "prod"})
	// reasons returns the readiness reasons from the authenticated diagnostics.
	reasons := func() string {
		_, diag := adminConfig(t, handler)
		return strings.Join(diag.Readiness.Reasons, "; ")
	}
	if rec := serve(handler, http.MethodGet, "/readyz", ""); rec.Code != http.StatusServiceUnavailable || strings.TrimSpace(rec.Body.String()) != `{"status":"not_ready"}` {
		t.Fatalf("expected a bare 503 before first cycle: %d %s", rec.Code, rec.Body.String())
	}
	if got := reasons(); !strings.Contains(got, "first cycle") {
		t.Fatalf("expected first-cycle reason, got %q", got)
	}
	close(firstCycle)
	eventually(t, time.Second, func() bool { return serve(handler, http.MethodGet, "/readyz", "").Code == http.StatusOK }, "not ready after first cycle")

	svc.now = func() time.Time { return time.Now().Add(time.Minute) } // 2x1s + 10s grace exceeded
	if rec := serve(handler, http.MethodGet, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected stale 503: %d %s", rec.Code, rec.Body.String())
	}
	if got := reasons(); !strings.Contains(got, "is stale") {
		t.Fatalf("expected stale reason, got %q", got)
	}
}
