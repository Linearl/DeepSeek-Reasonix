package main

// Task 326 — the autopilot guard task.
//
// What each fixture pins down, in the order the acceptance list states it:
//   - 幂等单例: repeated Ensure (and a planted duplicate) converge on ONE task.
//   - 对称清理: owner leaves autopilot → guard disabled; owner conversation
//     deleted → guard deleted (no orphans).
//   - 权限隔离: the guard records approvalMode "ask", and running it changes
//     neither the owner's approval mode nor its autopilot flag — so a guard can
//     never carry proxy-approval power or trip task 325's reverse linkage.
//   - 自关闭三档: disable / standby / destroy after three quiet checks; a
//     session with work left never reaches the policy.
//   - 面板间隔: the minute dial lands in the guard's interval and re-points
//     existing guards in place (still exactly one).
//
// Zero SKIP: a missing precondition is a t.Fatal.

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

func newGuardTestApp(t *testing.T) (*App, *HeartbeatEngine) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	app.heartbeat = engine
	return app, engine
}

func guardTasks(tasks []HeartbeatTask) []HeartbeatTask {
	var out []HeartbeatTask
	for _, t := range tasks {
		if isAutopilotGuardTask(t) {
			out = append(out, t)
		}
	}
	return out
}

func findGuard(t *testing.T, engine *HeartbeatEngine, topicID string) HeartbeatTask {
	t.Helper()
	for _, task := range guardTasks(engine.ListTasks()) {
		if strings.TrimSpace(task.TopicID) == topicID {
			return task
		}
	}
	t.Fatalf("no guard task for topic %q (tasks: %+v)", topicID, engine.ListTasks())
	return HeartbeatTask{}
}

// setGuardPolicy writes the self-close dial the way Settings does, so the
// fixture exercises the same read path the engine uses at run time.
func setGuardPolicy(t *testing.T, policy string) {
	t.Helper()
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Desktop.AutopilotGuardQuiescent = policy
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("write guard policy: %v", err)
	}
}

func TestAutopilotGuardEnsureIsIdempotentSingleton(t *testing.T) {
	_, engine := newGuardTestApp(t)
	owner := autopilotGuardOwner{TopicID: "owner-a", Scope: "global"}

	// The same session switched on five times (or five entry points firing)
	// must still produce one task.
	for i := 0; i < 5; i++ {
		if err := engine.EnsureAutopilotGuard(owner, 30); err != nil {
			t.Fatalf("ensure #%d: %v", i+1, err)
		}
	}
	guards := guardTasks(engine.ListTasks())
	if len(guards) != 1 {
		t.Fatalf("guards after 5 ensures = %d, want exactly 1", len(guards))
	}
	g := guards[0]
	if g.ID != autopilotGuardID("owner-a") {
		t.Fatalf("guard id = %q, want %q", g.ID, autopilotGuardID("owner-a"))
	}
	if !g.Enabled || g.Interval != "30m" || g.TopicID != "owner-a" {
		t.Fatalf("guard template wrong: enabled=%v interval=%q topic=%q", g.Enabled, g.Interval, g.TopicID)
	}
	if g.ApprovalMode != "ask" {
		t.Fatalf("guard approvalMode = %q, want the strictest gear (ask)", g.ApprovalMode)
	}
	if !g.ReuseSession {
		t.Fatal("guard must watch the owner's own conversation (reuseSession)")
	}
	if !strings.Contains(g.Prompt, "只读检查") {
		t.Fatalf("guard prompt must be a read-only check, got %q", g.Prompt)
	}

	// A duplicate planted by hand (different id, same owner) is swept away.
	tasks := engine.ListTasks()
	dup := g
	dup.ID = "autoguard-planted-duplicate"
	if err := engine.saveTasks(append(tasks, dup)); err != nil {
		t.Fatalf("plant duplicate: %v", err)
	}
	engine.ReloadConfig()
	if n := len(guardTasks(engine.ListTasks())); n != 2 {
		t.Fatalf("duplicate should be planted before the sweep, got %d guards", n)
	}
	if err := engine.EnsureAutopilotGuard(owner, 30); err != nil {
		t.Fatalf("ensure after duplicate: %v", err)
	}
	if n := len(guardTasks(engine.ListTasks())); n != 1 {
		t.Fatalf("guards after re-ensure = %d, want 1 (duplicate must be removed)", n)
	}
}

func TestAutopilotGuardReconcileCleanup(t *testing.T) {
	app, engine := newGuardTestApp(t)

	// (a) The owner tab is open but autopilot is off → the guard is disabled.
	if err := engine.EnsureAutopilotGuard(autopilotGuardOwner{TopicID: "owner-off", Scope: "global"}, 30); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{
		"tab-off": {ID: "tab-off", TopicID: "owner-off", Scope: "global"},
	}
	app.mu.Unlock()
	engine.ReconcileAutopilotGuards()
	if g := findGuard(t, engine, "owner-off"); g.Enabled {
		t.Fatal("a guard whose owner left autopilot must be disabled")
	}

	// (b) The owner conversation is gone (no open tab, not registered) → the
	// guard itself is removed, so nothing orphaned keeps running.
	if err := engine.EnsureAutopilotGuard(autopilotGuardOwner{TopicID: "owner-deleted", Scope: "global"}, 30); err != nil {
		t.Fatal(err)
	}
	engine.ReconcileAutopilotGuards()
	for _, g := range guardTasks(engine.ListTasks()) {
		if strings.TrimSpace(g.TopicID) == "owner-deleted" {
			t.Fatalf("orphan guard survived reconciliation: %+v", g)
		}
	}

	// (c) An owner still in autopilot keeps its guard, and reconciliation both
	// creates the missing one and re-points its interval.
	app.mu.Lock()
	app.tabs["tab-on"] = &WorkspaceTab{ID: "tab-on", TopicID: "owner-on", Scope: "global", autopilot: true}
	app.mu.Unlock()
	engine.ReconcileAutopilotGuards()
	g := findGuard(t, engine, "owner-on")
	if !g.Enabled {
		t.Fatal("an autopilot session must keep an enabled guard")
	}
	if g.Interval != "30m" {
		t.Fatalf("reconciled interval = %q, want 30m", g.Interval)
	}
	// owner-off (disabled) and owner-on survive; owner-deleted was removed.
	if n := len(guardTasks(engine.ListTasks())); n != 2 {
		t.Fatalf("guards = %d, want 2 (the deleted owner's guard is gone): %+v",
			n, guardTasks(engine.ListTasks()))
	}
}

// guardGoalCtrlStub lets a fixture say "the owner is still working through its
// goal" without a real controller.
type guardGoalCtrlStub struct {
	heartbeatExecuteTaskCtrlStub
	status     control.RuntimeStatus
	goalStatus string
}

func (s *guardGoalCtrlStub) RuntimeStatus() control.RuntimeStatus { return s.status }
func (s *guardGoalCtrlStub) GoalStatus() string                   { return s.goalStatus }

func TestAutopilotGuardOwnerQuiescent(t *testing.T) {
	app, _ := newGuardTestApp(t)

	// Unknown conversation: nothing to assess.
	engine := &HeartbeatEngine{app: app}
	if engine.autopilotGuardOwnerQuiescent("nowhere") {
		t.Fatal("a conversation with no open tab cannot be judged quiet")
	}

	tab := &WorkspaceTab{
		ID: "tab-q", TopicID: "owner-q", Scope: "global",
		autopilot: true, Ctrl: &guardGoalCtrlStub{goalStatus: control.GoalStatusComplete},
	}
	app.mu.Lock()
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.mu.Unlock()

	// No objective at all: an unattended run with nothing to do.
	if !engine.autopilotGuardOwnerQuiescent("owner-q") {
		t.Fatal("idle autopilot session with no goal must read as quiet")
	}

	// An objective that already finished reads as quiet too (任务完成).
	tab.goal = "ship the release"
	if !engine.autopilotGuardOwnerQuiescent("owner-q") {
		t.Fatal("a session whose goal reached a terminal state must read as quiet")
	}

	// Still working through the goal → not quiet, guard stays armed.
	tab.Ctrl = &guardGoalCtrlStub{goalStatus: control.GoalStatusRunning}
	if engine.autopilotGuardOwnerQuiescent("owner-q") {
		t.Fatal("a session whose goal is running must not read as quiet")
	}

	// Busy controller → not quiet even with a terminal goal.
	tab.Ctrl = &guardGoalCtrlStub{goalStatus: control.GoalStatusComplete, status: control.RuntimeStatus{Running: true}}
	if engine.autopilotGuardOwnerQuiescent("owner-q") {
		t.Fatal("a busy session must not read as quiet")
	}

	// Autopilot already off → the sweep owns this case, no strike.
	tab.Ctrl = &guardGoalCtrlStub{goalStatus: control.GoalStatusComplete}
	tab.autopilot = false
	if engine.autopilotGuardOwnerQuiescent("owner-q") {
		t.Fatal("a session that is no longer in autopilot must not be judged quiet")
	}
}

func TestAutopilotGuardSelfClosePolicies(t *testing.T) {
	for _, tc := range []struct {
		policy    string
		wantAlive bool // guard still present after the run state is applied
		wantOn    bool // guard still enabled
	}{
		{policy: "disable", wantAlive: true, wantOn: false},
		{policy: "standby", wantAlive: true, wantOn: true},
		{policy: "destroy", wantAlive: false, wantOn: false},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			_, engine := newGuardTestApp(t)
			setGuardPolicy(t, tc.policy)
			owner := autopilotGuardOwner{TopicID: "owner-" + tc.policy, Scope: "global"}
			if err := engine.EnsureAutopilotGuard(owner, 30); err != nil {
				t.Fatal(err)
			}
			guard := findGuard(t, engine, owner.TopicID)

			// Three consecutive checks with nothing to do: the guard pulls each
			// time, and the policy applies on the third.
			updated := guard
			for pull := 1; pull <= heartbeatIdleTerminateStrikes; pull++ {
				updated = engine.evaluateAutopilotGuardClose(updated, true)
				if updated.IdleStreak != pull {
					t.Fatalf("pull %d: idle streak = %d, want %d", pull, updated.IdleStreak, pull)
				}
			}
			if updated.Enabled != tc.wantOn {
				t.Fatalf("policy %q after %d quiet checks: enabled=%v, want %v",
					tc.policy, heartbeatIdleTerminateStrikes, updated.Enabled, tc.wantOn)
			}

			// Mirror the engine: run state is merged, then the destroy policy
			// removes the row.
			if err := engine.mutateTasks(func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error) {
				for i := range tasks {
					if tasks[i].ID == updated.ID {
						tasks[i].Enabled = updated.Enabled
						tasks[i].IdleStreak = updated.IdleStreak
						return tasks, true, nil
					}
				}
				return tasks, false, nil
			}); err != nil {
				t.Fatal(err)
			}
			engine.finishAutopilotGuardRun(updated)

			after := guardTasks(engine.ListTasks())
			alive := false
			for _, g := range after {
				if g.ID == updated.ID {
					alive = true
					if g.Enabled != tc.wantOn {
						t.Fatalf("policy %q: persisted enabled=%v, want %v", tc.policy, g.Enabled, tc.wantOn)
					}
				}
			}
			if alive != tc.wantAlive {
				t.Fatalf("policy %q: guard present=%v, want %v", tc.policy, alive, tc.wantAlive)
			}
		})
	}
}

func TestAutopilotGuardStaysArmedWhileOwnerHasWork(t *testing.T) {
	app, engine := newGuardTestApp(t)
	setGuardPolicy(t, "disable")
	owner := autopilotGuardOwner{TopicID: "owner-busy", Scope: "global"}
	if err := engine.EnsureAutopilotGuard(owner, 30); err != nil {
		t.Fatal(err)
	}
	guard := findGuard(t, engine, owner.TopicID)
	_ = app

	// A check that finds real work resets the streak instead of counting it.
	quiet := guard
	quiet = engine.evaluateAutopilotGuardClose(quiet, true)
	quiet = engine.evaluateAutopilotGuardClose(quiet, true)
	if quiet.IdleStreak != 2 {
		t.Fatalf("two quiet checks should build a streak of 2, got %d", quiet.IdleStreak)
	}
	quiet = engine.evaluateAutopilotGuardClose(quiet, false)
	if quiet.IdleStreak != 0 {
		t.Fatalf("one busy check must reset the streak, got %d", quiet.IdleStreak)
	}
	if !quiet.Enabled {
		t.Fatal("a session with work left must keep its guard armed")
	}
	for i := 0; i < heartbeatIdleTerminateStrikes+2; i++ {
		quiet = engine.evaluateAutopilotGuardClose(quiet, false)
	}
	if !quiet.Enabled {
		t.Fatal("never reaching the ceiling must leave the guard enabled")
	}
}

// guardRunInjector fills in a controller the way the other heartbeat execute
// fixtures do, and stamps the tab as an autopilot session running under yolo —
// the exact combination task 325 protects.
func startGuardInjector(t *testing.T, app *App, ctrl *heartbeatExecuteTaskCtrlStub) {
	t.Helper()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				app.mu.Lock()
				var target *WorkspaceTab
				for _, tab := range app.tabs {
					if tab == nil || tab.Ctrl != nil {
						continue
					}
					target = tab
					break
				}
				if target != nil {
					target.removed = true
					if cancel := target.buildCancel; cancel != nil {
						cancel()
					}
					target.Ctrl = ctrl
					target.Ready = true
					target.StartupErr = ""
					target.autopilot = true
					target.toolApprovalMode = control.ToolApprovalYolo
					app.advanceSessionRuntimeEpochLocked(target)
				}
				app.mu.Unlock()
			}
		}
	}()
}

func TestAutopilotGuardRunKeepsOwnerPermissions(t *testing.T) {
	app, engine := newGuardTestApp(t)
	owner := autopilotGuardOwner{TopicID: "owner-perm", Scope: "global"}
	if err := engine.EnsureAutopilotGuard(owner, 30); err != nil {
		t.Fatal(err)
	}
	guard := findGuard(t, engine, owner.TopicID)
	if guard.ApprovalMode != "ask" {
		t.Fatalf("guard approvalMode = %q, want ask (strictest)", guard.ApprovalMode)
	}

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	startGuardInjector(t, app, ctrl)
	got := engine.executeTask(guard)

	// 1. The guard never pushes an approval mode onto the session it watches.
	if ctrl.approvalMode != "" {
		t.Fatalf("guard run pushed approval mode %q onto the owner — a guard must not touch permissions", ctrl.approvalMode)
	}
	app.mu.Lock()
	var ownerTab *WorkspaceTab
	for _, tab := range app.tabs {
		if tab != nil && strings.TrimSpace(tab.TopicID) == owner.TopicID {
			ownerTab = tab
		}
	}
	autopilotStillOn := ownerTab != nil && ownerTab.autopilot
	ownerApproval := ""
	if ownerTab != nil {
		ownerApproval = ownerTab.toolApprovalMode
	}
	app.mu.Unlock()
	if ownerTab == nil {
		t.Fatal("the guard's run should have opened the owner's conversation")
	}
	if ownerApproval != control.ToolApprovalYolo {
		t.Fatalf("owner approval mode = %q after the guard ran, want yolo untouched", ownerApproval)
	}
	if !autopilotStillOn {
		t.Fatal("a guard run must not trip task 325's reverse linkage and turn autopilot off")
	}

	// 2. The run happened and the quiet check was counted (the owner had
	// nothing to do at sample time).
	if len(got.RunHistory) != 1 {
		t.Fatalf("guard run history = %d entries, want 1", len(got.RunHistory))
	}
	if got.IdleStreak != 1 {
		t.Fatalf("idle streak after one quiet check = %d, want 1", got.IdleStreak)
	}
	if !got.Enabled {
		t.Fatal("one quiet check must not self-close the guard")
	}

	// 3. The persisted template still records the strictest gear.
	persisted := findGuard(t, engine, owner.TopicID)
	if persisted.ApprovalMode != "ask" {
		t.Fatalf("persisted guard approvalMode = %q, want ask", persisted.ApprovalMode)
	}
}

func TestAutopilotGuardIntervalDialUpdatesInPlace(t *testing.T) {
	app, engine := newGuardTestApp(t)
	owner := autopilotGuardOwner{TopicID: "owner-int", Scope: "global"}
	if err := engine.EnsureAutopilotGuard(owner, 30); err != nil {
		t.Fatal(err)
	}
	if g := findGuard(t, engine, owner.TopicID); g.Interval != "30m" {
		t.Fatalf("initial interval = %q, want 30m", g.Interval)
	}

	// The Settings dial: config write + in-place resync of what already exists.
	if err := app.SetDesktopAutopilotGuardInterval(7); err != nil {
		t.Fatalf("set guard interval: %v", err)
	}
	if got := app.autopilotGuardIntervalMinutes(); got != 7 {
		t.Fatalf("configured interval = %d minutes, want 7", got)
	}
	guards := guardTasks(engine.ListTasks())
	if len(guards) != 1 {
		t.Fatalf("guards = %d after an interval change, want 1 (re-pointed, not recreated)", len(guards))
	}
	if guards[0].Interval != "7m" {
		t.Fatalf("guard interval = %q, want 7m", guards[0].Interval)
	}

	// Out-of-range dials are refused instead of silently clamped into a
	// schedule the user did not ask for.
	if err := app.SetDesktopAutopilotGuardInterval(0); err == nil {
		t.Fatal("a zero-minute guard interval must be refused")
	}
	if err := app.SetDesktopAutopilotGuardQuiescent("nonsense"); err == nil {
		t.Fatal("an unknown self-close policy must be refused")
	}
	if err := app.SetDesktopAutopilotGuardQuiescent("destroy"); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.AutopilotGuardQuiescentPolicy() != "destroy" {
		t.Fatalf("persisted policy = %q, want destroy", cfg.AutopilotGuardQuiescentPolicy())
	}
}

func TestAutopilotGuardIntervalDefaults(t *testing.T) {
	cfg := &config.Config{}
	if got := cfg.AutopilotGuardIntervalMinutes(); got != config.AutopilotGuardDefaultIntervalMinutes {
		t.Fatalf("default interval = %d, want %d", got, config.AutopilotGuardDefaultIntervalMinutes)
	}
	cfg.Desktop.AutopilotGuardInterval = 15
	if got := cfg.AutopilotGuardIntervalMinutes(); got != 15 {
		t.Fatalf("configured interval = %d, want 15", got)
	}
	// Empty and unknown policies read as the safe default, never as a policy
	// that leaves a guard running forever by accident.
	for _, raw := range []string{"", "typo"} {
		cfg.Desktop.AutopilotGuardQuiescent = raw
		if got := cfg.AutopilotGuardQuiescentPolicy(); got != "disable" {
			t.Fatalf("policy %q read as %q, want disable", raw, got)
		}
	}
	if got := autopilotGuardInterval(0); got != "30m" {
		t.Fatalf("interval for an unset dial = %q, want 30m", got)
	}
	if got := autopilotGuardInterval(7); got != "7m" {
		t.Fatalf("interval for 7 minutes = %q, want 7m", got)
	}
}
