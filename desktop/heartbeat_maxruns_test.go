package main

// Task 327 — run-count budget (maxRuns).
//
// The contract under test, in the order the spec states it:
//   - maxRuns: 1 fires once, then flips enabled=false (checked in the JSON).
//   - maxRuns: N charges 触发即计数 — a failed attempt spends a unit too — so
//     the Nth trigger is the last one and nothing fires afterwards.
//   - no maxRuns ⇒ byte-for-byte today's repeating behavior.
//   - re-enabling an exhausted task starts a fresh budget (重开 = 重置计数).
//   - a stale panel save cannot roll the counter back.
//
// Zero SKIP: every precondition is a t.Fatal.

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"reasonix/internal/control"
)

// newMaxRunsEngine builds an isolated App + engine with one legacy-mode task
// (neither fresh-conversation nor resume) so a run reuses the same topic tab
// across attempts — the simplest shape for counting triggers.
func newMaxRunsEngine(t *testing.T, seed HeartbeatTask) (*App, *HeartbeatEngine, *heartbeatExecuteTaskCtrlStub) {
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
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	// Inject a stub controller into the first tab that still has none. Unlike
	// the one-shot goroutines in heartbeat_test.go this loops, because a later
	// attempt may open a tab the real (slow, failing) startup would otherwise
	// win — and it skips tabs that already carry a controller.
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
					app.advanceSessionRuntimeEpochLocked(target)
				}
				app.mu.Unlock()
			}
		}
	}()
	return app, engine, ctrl
}

// setMaxRunsTabReadOnly flips the read-only flag on the task's tab. A
// read-only tab fails turn admission (readOnlyChannelErr), which is exactly
// the post-commitment failure the spec's "含 1 次人为失败" case needs: the
// attempt is charged but no prompt is submitted and no run history is added.
func setMaxRunsTabReadOnly(app *App, readOnly bool) {
	app.mu.Lock()
	defer app.mu.Unlock()
	for _, tab := range app.tabs {
		if tab != nil {
			tab.ReadOnly = readOnly
		}
	}
}

func readMaxRunsConfig(t *testing.T, engine *HeartbeatEngine) heartbeatConfig {
	t.Helper()
	data, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatalf("read heartbeat config: %v", err)
	}
	var cfg heartbeatConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode heartbeat config: %v", err)
	}
	return cfg
}

// runMaxRunsAttempt executes one trigger and returns the task as the engine
// persisted it. Two pieces of stub housekeeping stand in for what a real
// controller does between turns: the stub goes back to idle (so the previous
// submit's Running flag does not read as a busy controller) and the tab sink's
// in-flight turn is closed (TurnDone), otherwise the next admission is
// rejected as an overlapping turn.
func runMaxRunsAttempt(app *App, engine *HeartbeatEngine, ctrl *heartbeatExecuteTaskCtrlStub, task HeartbeatTask) HeartbeatTask {
	ctrl.status = control.RuntimeStatus{}
	app.mu.Lock()
	for _, tab := range app.tabs {
		if tab != nil && tab.sink != nil {
			tab.sink.cancelTurnStart()
		}
	}
	app.mu.Unlock()
	return engine.executeTask(task)
}

func TestHeartbeatMaxRunsSingleRunDisablesAfterFirstTrigger(t *testing.T) {
	seed := HeartbeatTask{
		ID:           "once",
		Title:        "Once",
		Prompt:       "ping",
		ApprovalMode: "yolo",
		MaxRuns:      1,
		Enabled:      true,
	}
	app, engine, ctrl := newMaxRunsEngine(t, seed)

	got := runMaxRunsAttempt(app, engine, ctrl, seed)
	if got.RunsUsed != 1 || got.MaxRuns != 1 {
		t.Fatalf("after one trigger: runsUsed=%d maxRuns=%d, want 1/1", got.RunsUsed, got.MaxRuns)
	}
	if got.Enabled {
		t.Fatal("maxRuns:1 task must disable itself after its single run")
	}
	if len(got.RunHistory) != 1 {
		t.Fatalf("run history = %d entries, want 1", len(got.RunHistory))
	}

	// JSON 实查: the terminal state is what the next process would load.
	cfg := readMaxRunsConfig(t, engine)
	if len(cfg.Tasks) != 1 {
		t.Fatalf("persisted tasks = %d, want 1", len(cfg.Tasks))
	}
	if cfg.Tasks[0].Enabled {
		t.Fatal("persisted config must show enabled=false after the budget ran out")
	}
	if cfg.Tasks[0].RunsUsed != 1 || cfg.Tasks[0].MaxRuns != 1 {
		t.Fatalf("persisted budget = %d/%d, want 1/1", cfg.Tasks[0].RunsUsed, cfg.Tasks[0].MaxRuns)
	}

	// Nothing fires afterwards: the scheduler skips a disabled task, and a
	// manual trigger is refused by the budget check instead of spending a
	// phantom unit.
	submitted := len(ctrl.submitted)
	engine.tick()
	if len(ctrl.submitted) != submitted {
		t.Fatalf("tick ran an exhausted task: submitted %d → %d", submitted, len(ctrl.submitted))
	}
	engine.TriggerNow("once")
	if len(ctrl.submitted) != submitted {
		t.Fatalf("manual trigger ran an exhausted task: submitted %d → %d", submitted, len(ctrl.submitted))
	}
}

func TestHeartbeatMaxRunsChargesFailedAttemptAndStopsAtBudget(t *testing.T) {
	seed := HeartbeatTask{
		ID:           "budget",
		Title:        "Budget",
		Prompt:       "ping",
		ApprovalMode: "yolo",
		MaxRuns:      3,
		Enabled:      true,
	}
	app, engine, ctrl := newMaxRunsEngine(t, seed)

	// Attempt 1 — success.
	task := runMaxRunsAttempt(app, engine, ctrl, seed)
	if task.RunsUsed != 1 || len(task.RunHistory) != 1 {
		t.Fatalf("attempt 1: runsUsed=%d history=%d, want 1/1", task.RunsUsed, len(task.RunHistory))
	}
	if !task.Enabled {
		t.Fatal("budget of 3 must not be spent by the first run")
	}

	// Attempt 2 — forced failure after commitment (read-only tab): charged,
	// but no prompt and no history entry.
	setMaxRunsTabReadOnly(app, true)
	task = runMaxRunsAttempt(app, engine, ctrl, task)
	if task.RunsUsed != 2 {
		t.Fatalf("failed attempt must still be charged, runsUsed=%d want 2", task.RunsUsed)
	}
	if len(task.RunHistory) != 1 {
		t.Fatalf("failed attempt must not append history, got %d entries", len(task.RunHistory))
	}
	if len(ctrl.submitted) != 1 {
		t.Fatalf("read-only tab must not accept the prompt, submitted=%v", ctrl.submitted)
	}

	// Attempt 3 — success, budget reached.
	setMaxRunsTabReadOnly(app, false)
	task = runMaxRunsAttempt(app, engine, ctrl, task)
	if task.RunsUsed != 3 {
		t.Fatalf("attempt 3: runsUsed=%d want 3", task.RunsUsed)
	}
	if task.Enabled {
		t.Fatalf("reaching maxRuns=3 must disable the task, enabled=%v", task.Enabled)
	}
	if len(task.RunHistory) != 2 {
		t.Fatalf("run history = %d entries (two successes), want 2", len(task.RunHistory))
	}

	cfg := readMaxRunsConfig(t, engine)
	if cfg.Tasks[0].Enabled || cfg.Tasks[0].RunsUsed != 3 {
		t.Fatalf("persisted terminal state = enabled=%v runsUsed=%d, want false/3",
			cfg.Tasks[0].Enabled, cfg.Tasks[0].RunsUsed)
	}

	// Fourth trigger: never.
	submitted := len(ctrl.submitted)
	engine.tick()
	engine.TriggerNow("budget")
	if len(ctrl.submitted) != submitted {
		t.Fatalf("the 4th trigger must not run: submitted %d → %d", submitted, len(ctrl.submitted))
	}
}

func TestHeartbeatMaxRunsAbsentKeepsRepeatingBehavior(t *testing.T) {
	seed := HeartbeatTask{
		ID:           "legacy",
		Title:        "Legacy",
		Prompt:       "ping",
		ApprovalMode: "yolo",
		Enabled:      true,
	}
	app, engine, ctrl := newMaxRunsEngine(t, seed)

	task := seed
	for i := 1; i <= 3; i++ {
		task = runMaxRunsAttempt(app, engine, ctrl, task)
		if !task.Enabled {
			t.Fatalf("run %d: a task with no maxRuns must stay enabled", i)
		}
		if task.RunsUsed != 0 {
			t.Fatalf("run %d: runsUsed=%d, an unlimited task must not charge", i, task.RunsUsed)
		}
	}
	if len(task.RunHistory) != 3 {
		t.Fatalf("run history = %d entries, want 3", len(task.RunHistory))
	}
}

func TestHeartbeatMaxRunsReenableResetsBudget(t *testing.T) {
	seed := HeartbeatTask{
		ID:           "reopen",
		Title:        "Reopen",
		Prompt:       "ping",
		ApprovalMode: "yolo",
		MaxRuns:      1,
		Enabled:      true,
	}
	app, engine, ctrl := newMaxRunsEngine(t, seed)

	exhausted := runMaxRunsAttempt(app, engine, ctrl, seed)
	if exhausted.Enabled {
		t.Fatal("precondition: the single-run task should be exhausted")
	}

	// Panel-style full-list save that turns the task back on.
	view := engine.ReloadConfig()
	tasks := append([]HeartbeatTask(nil), view.Tasks...)
	if len(tasks) != 1 {
		t.Fatalf("reloaded tasks = %d, want 1", len(tasks))
	}
	tasks[0].Enabled = true
	if _, err := engine.ReplaceConfig(HeartbeatConfigUpdate{Revision: view.Revision, ETag: view.ETag, Tasks: tasks}); err != nil {
		t.Fatalf("re-enable save: %v", err)
	}

	after := engine.ListTasks()
	if len(after) != 1 {
		t.Fatalf("tasks after re-enable = %d, want 1", len(after))
	}
	if !after[0].Enabled {
		t.Fatal("re-enable must stick")
	}
	if after[0].RunsUsed != 0 {
		t.Fatalf("重开 = 重置计数: runsUsed=%d after re-enable, want 0", after[0].RunsUsed)
	}
	cfg := readMaxRunsConfig(t, engine)
	if cfg.Tasks[0].RunsUsed != 0 {
		t.Fatalf("persisted runsUsed = %d after re-enable, want 0", cfg.Tasks[0].RunsUsed)
	}

	// The fresh budget buys exactly one more run.
	next := runMaxRunsAttempt(app, engine, ctrl, after[0])
	if next.RunsUsed != 1 || next.Enabled {
		t.Fatalf("after reset: runsUsed=%d enabled=%v, want 1/false", next.RunsUsed, next.Enabled)
	}
}

func TestSpendRunBudgetChargesOnlyWhenCapped(t *testing.T) {
	engine := &HeartbeatEngine{}
	unlimited := engine.spendRunBudget(HeartbeatTask{ID: "u", Enabled: true})
	if unlimited.RunsUsed != 0 || !unlimited.Enabled {
		t.Fatalf("no maxRuns must be a no-op: %+v", unlimited)
	}
	charged := engine.spendRunBudget(HeartbeatTask{ID: "c", Enabled: true, MaxRuns: 2})
	if charged.RunsUsed != 1 || !charged.Enabled {
		t.Fatalf("first charge: %+v, want runsUsed=1 and still enabled", charged)
	}
	final := engine.spendRunBudget(HeartbeatTask{ID: "c", Enabled: true, MaxRuns: 2, RunsUsed: 1})
	if final.RunsUsed != 2 || final.Enabled {
		t.Fatalf("last charge: %+v, want runsUsed=2 and disabled", final)
	}
	if !heartbeatBudgetExhausted(final) {
		t.Fatal("a task that hit its budget must report exhausted")
	}
	if heartbeatBudgetExhausted(HeartbeatTask{MaxRuns: 0, RunsUsed: 9}) {
		t.Fatal("an unlimited budget can never be exhausted")
	}
}

func TestMergeHeartbeatRunUpdatesCarriesTerminalDisable(t *testing.T) {
	// The disk list is a stale snapshot: it still shows the task enabled with
	// one run spent. The runtime just spent the budget and disabled it. Merging
	// only timestamps (the pre-327 behavior) would resurrect it on the next tick.
	tasks := []HeartbeatTask{{ID: "a", Enabled: true, MaxRuns: 3, RunsUsed: 1}}
	mergeHeartbeatRunUpdates(tasks, map[string]HeartbeatTask{
		"a": {ID: "a", Enabled: false, MaxRuns: 3, RunsUsed: 3},
	})
	if tasks[0].Enabled {
		t.Fatal("terminal disable from the runtime must survive the merge")
	}
	if tasks[0].RunsUsed != 3 {
		t.Fatalf("runsUsed after merge = %d, want 3", tasks[0].RunsUsed)
	}

	// A budget the user raised concurrently wins: the update is below it, so
	// the task stays enabled.
	raised := []HeartbeatTask{{ID: "a", Enabled: true, MaxRuns: 5, RunsUsed: 1}}
	mergeHeartbeatRunUpdates(raised, map[string]HeartbeatTask{
		"a": {ID: "a", Enabled: false, MaxRuns: 3, RunsUsed: 3},
	})
	if !raised[0].Enabled {
		t.Fatal("a concurrently raised budget must keep the task running")
	}
}

func TestMergeHeartbeatDiskRunHistoryProtectsRunsUsed(t *testing.T) {
	// A stale panel snapshot (counter 1) must not roll back the disk counter (3).
	submitted := []HeartbeatTask{{ID: "a", Enabled: true, MaxRuns: 3, RunsUsed: 1}}
	disk := []HeartbeatTask{{ID: "a", Enabled: true, MaxRuns: 3, RunsUsed: 3}}
	out := mergeHeartbeatDiskRunHistory(submitted, disk)
	if out[0].RunsUsed != 3 {
		t.Fatalf("runsUsed after stale-save merge = %d, want 3", out[0].RunsUsed)
	}
}

func TestHeartbeatConfigSchemaVersionBumpedForMaxRuns(t *testing.T) {
	// Task 327's forward protection: a v3 binary doing a full-table save would
	// drop maxRuns and turn a bounded task into an unbounded one, so the schema
	// must have moved past 3.
	if heartbeatSchemaVersion < 4 {
		t.Fatalf("heartbeatSchemaVersion = %d, want >= 4 now that maxRuns exists", heartbeatSchemaVersion)
	}
}
