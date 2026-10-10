package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// TestCollabOpenDetachedFromConfigTwoModes pins task 264's mode gate (the
// two-mode test): default/off = byte-for-byte baseline (stand-up opens and
// activates a tab), on = detached stand-up (no tab in the bar). Delivery
// semantics live outside this flag — it only picks where the runtime lands.
func TestCollabOpenDetachedFromConfigTwoModes(t *testing.T) {
	if CollabOpenDetachedFromConfig(nil) {
		t.Fatal("nil config must read as baseline (tab stand-up)")
	}
	off := &config.Config{}
	if CollabOpenDetachedFromConfig(off) {
		t.Fatal("default (off) must be the byte-for-byte baseline")
	}
	on := &config.Config{}
	on.Agent.SessionCollabBackground = true
	if !CollabOpenDetachedFromConfig(on) {
		t.Fatal("session_collab_background=true must select the detached stand-up")
	}
}

// TestSetSessionCollabBackgroundRoundTrips pins the panel path: the setter
// writes the same key the drain reads, so a panel flip applies live.
func TestSetSessionCollabBackgroundRoundTrips(t *testing.T) {
	c := &config.Config{}
	if err := c.SetSessionCollabBackground(true); err != nil {
		t.Fatal(err)
	}
	if !CollabOpenDetachedFromConfig(c) {
		t.Fatal("setter must flip the mode the drain reads")
	}
	out := config.RenderTOMLForScope(c, config.RenderScopeUser)
	if !strings.Contains(out, "session_collab_background = true") {
		t.Fatalf("rendered config is missing session_collab_background:\n%s", out)
	}
	if err := c.SetSessionCollabBackground(false); err != nil {
		t.Fatal(err)
	}
	if CollabOpenDetachedFromConfig(c) {
		t.Fatal("setter must turn the mode back off")
	}
}

// TestParkTabAsDetachedRefusesGracefully pins the failure contract: a tab
// that cannot be parked (unknown id, no runtime) stays visible in a.tabs —
// parking may fail toward baseline, never toward an unreachable runtime.
func TestParkTabAsDetachedRefusesGracefully(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}}
	if err := app.parkTabAsDetached("missing"); err == nil {
		t.Fatal("parking a missing tab must fail")
	}

	// A tab without a runtime cannot be parked and must NOT be removed.
	app.tabs["no-ctrl"] = &WorkspaceTab{ID: "no-ctrl"}
	if err := app.parkTabAsDetached("no-ctrl"); err == nil {
		t.Fatal("parking a runtime-less tab must fail")
	}
	if _, ok := app.tabs["no-ctrl"]; !ok {
		t.Fatal("a failed park must leave the tab visible (baseline fallback)")
	}
}

// TestOpenTopicSessionDetachedFallsBackToVisible pins the composite: if the
// session cannot open at all the caller sees the error; parking failure (see
// above) degrades to a visible inactive tab, logged — the message always has
// a home.
func TestOpenTopicSessionDetachedFallsBackToVisible(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := &App{tabs: map[string]*WorkspaceTab{}}
	// An invalid scope/root fails validation before any tab exists — the error
	// must surface so drain() logs and retries next pass (message not acked).
	_, err := app.OpenTopicSessionDetached("project", "", "topic-x", "/nope/nowhere.jsonl")
	if err == nil || !strings.Contains(err.Error(), "workspaceRoot") {
		t.Fatalf("expected scope validation error, got %v", err)
	}
	if len(app.tabs) != 0 {
		t.Fatalf("failed open must not leave a tab behind: %d", len(app.tabs))
	}
}

// ---- 任务 738 门：park 不再与异步 build 赛跑后把空壳 tab 留在栏里 ----

// TestParkTabAsDetachedClassifiesRuntimePending pins the 738 classification:
// a park attempt on a tab whose controller build has not published yet is a
// TIMING state (errParkRuntimePending), not a permanent refusal — the
// deferred-park waiter resolves it. The tab stays visible either way.
func TestParkTabAsDetachedClassifiesRuntimePending(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}}
	app.tabs["no-ctrl"] = &WorkspaceTab{ID: "no-ctrl"}
	err := app.parkTabAsDetached("no-ctrl")
	if !errors.Is(err, errParkRuntimePending) {
		t.Fatalf("nil-Ctrl park must classify as runtime-pending, got %v", err)
	}
	if _, ok := app.tabs["no-ctrl"]; !ok {
		t.Fatal("runtime-pending park must leave the tab visible")
	}
}

// TestParkTabAsDetachedRefusesInUseTab pins the 738 fences: the open chain may
// reuse a live tab (resolveOpenSessionPath prefers the live runtime), so a
// park attempt on the active tab or one with a foreground turn must refuse
// (errParkTabInUse) and keep the tab visible — never yank a surface the user
// is looking at or that is mid-turn out of the bar.
func TestParkTabAsDetachedRefusesInUseTab(t *testing.T) {
	sessionPath := filepath.Join("C:", "ws", "s.jsonl")

	app := &App{tabs: map[string]*WorkspaceTab{}}
	app.tabs["busy"] = &WorkspaceTab{
		ID: "busy", SessionPath: sessionPath,
		Ctrl: &backgroundRuntimeController{status: control.RuntimeStatus{Running: true}},
	}
	if err := app.parkTabAsDetached("busy"); !errors.Is(err, errParkTabInUse) {
		t.Fatalf("parking a mid-turn tab must refuse as in-use, got %v", err)
	}
	if _, ok := app.tabs["busy"]; !ok {
		t.Fatal("in-use park must leave the tab visible")
	}

	focused := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "focused"}
	focused.tabs["focused"] = &WorkspaceTab{
		ID: "focused", SessionPath: sessionPath,
		Ctrl: &backgroundRuntimeController{},
	}
	if err := focused.parkTabAsDetached("focused"); !errors.Is(err, errParkTabInUse) {
		t.Fatalf("parking the active tab must refuse as in-use, got %v", err)
	}
	if _, ok := focused.tabs["focused"]; !ok {
		t.Fatal("in-use park must leave the tab visible")
	}
}

// shortParkWaitWindow shrinks the deferred-park knobs for the gate tests and
// restores them on cleanup.
func shortParkWaitWindow(t *testing.T, timeout, poll time.Duration) {
	t.Helper()
	oldTimeout, oldPoll := collabParkRuntimeWaitTimeout, collabParkRuntimePollInterval
	collabParkRuntimeWaitTimeout, collabParkRuntimePollInterval = timeout, poll
	t.Cleanup(func() {
		collabParkRuntimeWaitTimeout, collabParkRuntimePollInterval = oldTimeout, oldPoll
	})
}

// parkWaiterIdleWait waits until the app has no live deferred-park waiter.
func parkWaiterIdleWait(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		app.mu.RLock()
		idle := len(app.collabParkWaiters) == 0
		app.mu.RUnlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("deferred-park waiter did not settle in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAwaitRuntimeThenParkParksWhenRuntimePublishes is the 738 main gate: a
// stand-up tab whose park attempt lost the async-build race must leave the
// bar the moment its controller publishes — without the park succeeding
// immediately, the old code left an empty shell tab in the bar (the 2026-10-10
// restart blank-tab incident).
func TestAwaitRuntimeThenParkParksWhenRuntimePublishes(t *testing.T) {
	shortParkWaitWindow(t, 2*time.Second, 5*time.Millisecond)
	app := &App{tabs: map[string]*WorkspaceTab{}}
	sessionPath := filepath.Join("C:", "ws", "s.jsonl")
	tab := &WorkspaceTab{ID: "standup", SessionPath: sessionPath}
	app.tabs["standup"] = tab

	app.deferParkUntilRuntime("standup")
	// Simulate the async build publishing a second later (waiter already armed).
	go func() {
		time.Sleep(20 * time.Millisecond)
		app.mu.Lock()
		tab.Ctrl = &backgroundRuntimeController{}
		app.mu.Unlock()
	}()

	parkWaiterIdleWait(t, app)
	app.mu.RLock()
	_, stillVisible := app.tabs["standup"]
	detached := len(app.detachedSessions)
	app.mu.RUnlock()
	if stillVisible {
		t.Fatal("the tab must leave the bar once its runtime publishes")
	}
	if detached != 1 {
		t.Fatalf("the parked runtime must land in detachedSessions, got %d", detached)
	}
}

// TestAwaitRuntimeThenParkSkipsInUse pins the user-intent veto: if the tab is
// activated (or mid-turn) by the time the runtime publishes, the deferred park
// must stand down and keep the tab visible.
func TestAwaitRuntimeThenParkSkipsInUse(t *testing.T) {
	shortParkWaitWindow(t, 2*time.Second, 5*time.Millisecond)
	app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "standup"}
	sessionPath := filepath.Join("C:", "ws", "s.jsonl")
	tab := &WorkspaceTab{ID: "standup", SessionPath: sessionPath, Ctrl: &backgroundRuntimeController{}}
	app.tabs["standup"] = tab

	app.deferParkUntilRuntime("standup")
	parkWaiterIdleWait(t, app)

	app.mu.RLock()
	_, stillVisible := app.tabs["standup"]
	detached := len(app.detachedSessions)
	app.mu.RUnlock()
	if !stillVisible {
		t.Fatal("an activated tab must stay in the bar (user intent outranks the park)")
	}
	if detached != 0 {
		t.Fatalf("an activated tab must not be parked, got %d detached", detached)
	}
}

// TestAwaitRuntimeThenParkTimesOutLeavesTabVisible pins the bounded-wait
// fallback: a runtime that never publishes (lease-blocked rebuild, failed
// build) keeps the tab in the bar instead of vanishing — the message stays
// deliverable and the tab shows its real state rather than disappearing.
func TestAwaitRuntimeThenParkTimesOutLeavesTabVisible(t *testing.T) {
	shortParkWaitWindow(t, 80*time.Millisecond, 5*time.Millisecond)
	app := &App{tabs: map[string]*WorkspaceTab{}}
	app.tabs["never-built"] = &WorkspaceTab{ID: "never-built", SessionPath: filepath.Join("C:", "ws", "s.jsonl")}

	app.deferParkUntilRuntime("never-built")
	parkWaiterIdleWait(t, app)

	app.mu.RLock()
	_, stillVisible := app.tabs["never-built"]
	detached := len(app.detachedSessions)
	app.mu.RUnlock()
	if !stillVisible {
		t.Fatal("a timed-out park must leave the tab visible (fallback, not removal)")
	}
	if detached != 0 {
		t.Fatalf("a timed-out park must not detach anything, got %d", detached)
	}
}
