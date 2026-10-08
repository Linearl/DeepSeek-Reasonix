package main

import (
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
)

// foregroundApp builds a minimal App with initialized tab maps — the same
// shape the autosave and background-runtime tests use — so a real tabEventSink
// can route lifecycle and turn-boundary events through it.
func foregroundApp(t *testing.T) (*App, *WorkspaceTab) {
	t.Helper()
	app := &App{
		tabs:             map[string]*WorkspaceTab{},
		detachedSessions: map[string]*WorkspaceTab{},
		activeTabID:      "tab-fg",
	}
	tab := &WorkspaceTab{ID: "tab-fg", TopicTitle: "前台子代理页", Label: "fallback-label"}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	return app, tab
}

func lifecycleInfo(phase string, background bool) event.SubagentLifecycleInfo {
	return event.SubagentLifecycleInfo{
		Phase: phase, Ref: "sa_" + phase, Skill: "调研子代理",
		StartUnixMs: 1700000000000, Background: background,
	}
}

func runningForegroundCount(app *App) int {
	return len(app.RunningSubagents())
}

// TestForegroundSubagentRegistryCountsRunningChild pins the core fix: a
// foreground (synchronous) task sub-agent running mid-turn is counted and
// listed by the App-level registry, so the capsule badge can show it (task
// 557 screenshot: a 55-minute foreground child with badge zero).
func TestForegroundSubagentRegistryCountsRunningChild(t *testing.T) {
	app, tab := foregroundApp(t)

	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", false))
	tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_running", Ref: "sa_child_created", Skill: "调研子代理",
		StartUnixMs: 1700000000000,
	})
	views := app.RunningSubagents()
	if len(views) != 1 {
		t.Fatalf("running foreground sub-agents = %d, want 1: %+v", len(views), views)
	}
	view := views[0]
	if view.TabID != "tab-fg" || view.Ref != "sa_child_created" || view.Name != "调研子代理" {
		t.Fatalf("view = %+v, want the running child on its owning tab", view)
	}
	if view.Title != "前台子代理页" {
		t.Fatalf("title = %q, want the tab topic title", view.Title)
	}
	if view.StartedAt != 1700000000000 {
		t.Fatalf("startedAt = %d, want the lifecycle start timestamp", view.StartedAt)
	}
}

// TestForegroundSubagentTerminalAndTurnBoundaryClear pins the no-hang rule:
// the terminal phase removes the entry, and the turn boundary clears whatever
// survived — an interrupted or abnormally ended turn must drop the count to
// zero, not leave it hanging (task 510 lesson).
func TestForegroundSubagentTerminalAndTurnBoundaryClear(t *testing.T) {
	app, tab := foregroundApp(t)

	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", false))
	tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_running", Ref: "sa_child_created", Skill: "task",
	})
	if runningForegroundCount(app) != 1 {
		t.Fatalf("running count = %d, want 1 before termination", runningForegroundCount(app))
	}

	// Terminal phase removes the entry (removal ignores the Background flag:
	// self-healing for any ref shape).
	tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_cancelled", Ref: "sa_child_created",
	})
	if runningForegroundCount(app) != 0 {
		t.Fatalf("running count = %d, want 0 after terminal phase", runningForegroundCount(app))
	}

	// Missed terminal: TurnDone is the backstop.
	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", false))
	if runningForegroundCount(app) != 1 {
		t.Fatalf("running count = %d, want 1 before turn end", runningForegroundCount(app))
	}
	tab.sink.Emit(event.Event{Kind: event.TurnDone})
	if runningForegroundCount(app) != 0 {
		t.Fatalf("running count = %d, want 0 after turn done", runningForegroundCount(app))
	}

	// Missed TurnDone (abnormal end): TurnStarted defensively clears residue
	// so it can never leak into the next turn's count.
	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", false))
	tab.sink.Emit(event.Event{Kind: event.TurnStarted})
	if runningForegroundCount(app) != 0 {
		t.Fatalf("running count = %d, want 0 after next turn started", runningForegroundCount(app))
	}
}

// TestForegroundSubagentBackgroundEventsNeverCount is the job+lifecycle
// dual-source dedupe assertion (task 557 坑 1): a background sub-agent is
// represented by its job row AND emits lifecycle events through the same tab
// sink. The combined running-work surface must count it exactly once.
func TestForegroundSubagentBackgroundEventsNeverCount(t *testing.T) {
	app, tab := foregroundApp(t)
	ctrl := &backgroundRuntimeController{
		status: control.RuntimeStatus{BackgroundJobs: 1},
		jobs:   []jobs.View{{ID: "job-1", Kind: "task", Label: "后台子代理", Status: "running", StartedAt: 1}},
	}
	tab.Ctrl = ctrl

	// The background child's lifecycle events flow through the same sink.
	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", true))
	tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_running", Ref: "sa_child_created", Background: true,
	})

	jobRows := len(activeWorkForController(ctrl).Jobs)
	foregroundRows := runningForegroundCount(app)
	if jobRows != 1 || foregroundRows != 0 {
		t.Fatalf("combined running rows = job:%d + foreground:%d, want the background child counted once (job:1, foreground:0)", jobRows, foregroundRows)
	}

	// A genuinely foreground child alongside the background one adds exactly
	// one more row — no cross-contamination in either direction.
	tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_running", Ref: "sa_foreground", Skill: "task", Background: false,
	})
	if got := len(activeWorkForController(ctrl).Jobs) + runningForegroundCount(app); got != 2 {
		t.Fatalf("combined running rows = %d, want 2 (one job + one foreground child)", got)
	}
}

// TestForegroundSubagentFleetChildrenParallelTasksCount pins the multi-child
// breadth: parallel_tasks (and foreground fleet) children each carry a ref,
// so the count equals the actual running children.
func TestForegroundSubagentFleetChildrenParallelTasksCount(t *testing.T) {
	app, tab := foregroundApp(t)
	for _, ref := range []string{"sa_a", "sa_b", "sa_c"} {
		tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
			Phase: "child_running", Ref: ref, Skill: "task",
		})
	}
	if got := runningForegroundCount(app); got != 3 {
		t.Fatalf("running count = %d, want 3 concurrent foreground children", got)
	}
	tab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{Phase: "child_failed", Ref: "sa_b"})
	if got := runningForegroundCount(app); got != 2 {
		t.Fatalf("running count = %d, want 2 after one child failed", got)
	}
}

// TestForegroundSubagentMoveOnRebind pins the detach/reattach rule: a runtime
// rebound onto another tab carries its running foreground children with it,
// so the old tab cannot keep a phantom row no turn will ever clear.
func TestForegroundSubagentMoveOnRebind(t *testing.T) {
	app, tab := foregroundApp(t)
	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", false))
	if runningForegroundCount(app) != 1 {
		t.Fatalf("running count = %d, want 1 before rebind", runningForegroundCount(app))
	}

	app.detachedSessions["detached-1"] = &WorkspaceTab{ID: "detached-1", TopicTitle: "分离会话"}
	tab.sink.setBinding("detached-1", nil)

	views := app.RunningSubagents()
	if len(views) != 1 {
		t.Fatalf("views after rebind = %+v, want exactly the moved child", views)
	}
	if views[0].TabID != "detached-1" || views[0].Title != "分离会话" {
		t.Fatalf("moved view = %+v, want the child under the detached binding with its title", views[0])
	}
}

// TestForegroundSubagentTitleFallbackAndUnknownTab pins the view contract:
// Label is the title fallback (same as BackgroundRuntimes), and an unknown
// tab (rebound runtime whose tab is gone) still lists with an empty title.
func TestForegroundSubagentTitleFallbackAndUnknownTab(t *testing.T) {
	app, tab := foregroundApp(t)
	app.tabs["tab-fg"].TopicTitle = ""
	tab.sink.RecordSubagentLifecycle(lifecycleInfo("child_created", false))

	views := app.RunningSubagents()
	if len(views) != 1 || views[0].Title != "fallback-label" {
		t.Fatalf("views = %+v, want the Label fallback as title", views)
	}

	app.noteSubagentLifecycle("tab-gone", event.SubagentLifecycleInfo{
		Phase: "child_running", Ref: "sa_orphan", Skill: "task",
	})
	views = app.RunningSubagents()
	if len(views) != 2 {
		t.Fatalf("views = %+v, want the known child plus the unknown-tab child", views)
	}
	for _, view := range views {
		if view.TabID == "tab-gone" && view.Title != "" {
			t.Fatalf("unknown tab must list with an empty title, got %+v", view)
		}
	}
}
