package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// ---- 任务 865：心跳后台化的 park 不再被自己刚提交的 turn 拒绝 ----
//
// 864 诊断的矛盾链：submitUserTurnToTabWithSink 返回 true 的判定就是
// RuntimeStatus().Running（提交线程内同步置位），而 742 挂在 submit 成功之后的
// parkTabAsDetached 用同一个 Running 作为「用户前景 turn」fence 拒绝 park ——
// submit 成功 ⟺ park 必被拒 ⟺ 标签留条 ⟺ 次轮 visibleBefore 恒 true 自锁。
// 742 的原验收（前端 34 断言）没有覆盖 Go 侧 park 成功路径，本文件把
// 「真实运行态下 submit 成功后 park 成功」钉成回归测试。

// blockingHeartbeatProvider holds the submitted heartbeat turn open, so the
// park attempt below really collides with Running=true — the exact 864
// collision shape. A fast-failing executor would finish the turn before the
// park runs and blur which fence saved the test. A cancelled context ends the
// stream instead of waiting forever (controller Close during cleanup).
type blockingHeartbeatProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingHeartbeatProvider) Name() string { return "blocking-heartbeat" }

func (p *blockingHeartbeatProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
		ch := make(chan provider.Chunk)
		close(ch)
		return ch, nil
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

// TestHeartbeatBackgroundParksOwnTurnAfterSubmit is the task-865 main gate on
// the real controller state machine: a background heartbeat run submits its
// prompt (Running flips true synchronously) and the very same run must take
// its tab back out of the strip — the user never sees a headline tab flash
// open for a background run. The parked runtime lands in the detached pool as
// the same live controller (task 264 semantics: the turn keeps running, the
// sink keeps forwarding). Under the pre-865 plain fence this test fails on
// every run: the submit's own Running flag refused the park.
func TestHeartbeatBackgroundParksOwnTurnAfterSubmit(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	engine.heartbeatBackground = func() bool { return true }
	seed := HeartbeatTask{
		ID:           "bg-park",
		Title:        "Background park",
		Prompt:       "nightly sweep",
		ApprovalMode: "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	// Open the run's tab for real (the same call executeTaskOwned makes), then
	// publish a REAL controller whose session path matches the tab — a
	// mismatching path would trip the workspace rebuild admission instead of
	// reaching the submit. The provider holds the submitted turn open, so the
	// park below really collides with Running=true (the 864 collision shape).
	_, topicID, _, ok := engine.resolveHeartbeatTopic(seed, "global", "", "Heartbeat: "+seed.Title)
	if !ok {
		t.Fatal("the topic must resolve before the run opens its tab")
	}
	meta, err := app.openHeartbeatTabInactive("global", "", topicID)
	if err != nil {
		t.Fatalf("openHeartbeatTabInactive: %v", err)
	}
	app.mu.RLock()
	hbTab := app.tabs[meta.ID]
	sessionPath := hbTab.SessionPath
	app.mu.RUnlock()
	if hbTab == nil || sessionPath == "" {
		t.Fatal("the opened heartbeat tab must carry a session path")
	}

	prov := &blockingHeartbeatProvider{started: make(chan struct{}), release: make(chan struct{})}
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("hb system prompt"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{
		Runner:      exec,
		Executor:    exec,
		SessionDir:  filepath.Dir(sessionPath),
		SessionPath: sessionPath,
		Label:       "heartbeat",
		Sink:        event.Discard,
	})
	t.Cleanup(ctrl.Close)

	// Publish over the async build (same injection shape as the cold-boot
	// harness): stop the real build, then land the real controller as ready.
	app.mu.Lock()
	if hbTab.buildCancel != nil {
		hbTab.buildCancel()
	}
	hbTab.Ctrl = ctrl
	hbTab.Ready = true
	hbTab.StartupErr = ""
	app.advanceSessionRuntimeEpochLocked(hbTab)
	app.mu.Unlock()
	tabID := hbTab.ID

	// The run's tab starts from the detached pool — the steady state of a
	// background task after its first run (the previous run parked it). This
	// keeps the visible-before snapshot false, exactly like production, and
	// exercises the prefer-live promotion inside the run.
	if err := app.parkTabAsDetachedOwned(tabID); err != nil {
		t.Fatalf("pre-run park of the idle runtime: %v", err)
	}

	seed.TopicID = topicID // the run must resolve to the tab's topic
	got := engine.executeTask(seed)

	if got.LastRunAt == 0 {
		t.Fatal("the run must be charged — the submit path must have been reached")
	}
	select {
	case <-prov.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the submitted turn never started — the real controller was not driven")
	}

	app.mu.RLock()
	_, stillVisible := app.tabs[tabID]
	var detachedHere bool
	for _, detached := range app.detachedSessions {
		if detached != nil && detached.Ctrl == ctrl {
			detachedHere = true
			break
		}
	}
	app.mu.RUnlock()
	if stillVisible {
		t.Fatal("background heartbeat left its own tab in the strip — the 864 self-lock (headline tab stays open)")
	}
	if !detachedHere {
		t.Fatal("the parked runtime must land in the detached pool as the same live controller (task 264)")
	}

	close(prov.release) // let the held turn finish before the controller closes
}

// TestParkTabAsDetachedOwnedExemptsOwnTurnButNotActiveTab pins the fence
// split (task 865 fix 1): the owned variant ignores the turn-state fence
// (Running AND a pending approval both belong to the heartbeat's own turn,
// and a pending approval replays when the runtime is promoted back) while the
// active-tab fence stays — a tab the user clicked onto is theirs.
func TestParkTabAsDetachedOwnedExemptsOwnTurnButNotActiveTab(t *testing.T) {
	sessionPath := filepath.Join("C:", "ws", "hb.jsonl")

	// The plain fence still refuses a running tab (task 738 behaviour kept).
	busyApp := &App{tabs: map[string]*WorkspaceTab{}}
	busyApp.tabs["hb"] = &WorkspaceTab{
		ID: "hb", SessionPath: sessionPath,
		Ctrl: &backgroundRuntimeController{status: control.RuntimeStatus{Running: true}},
	}
	if err := busyApp.parkTabAsDetached("hb"); !errors.Is(err, errParkTabInUse) {
		t.Fatalf("plain park of a mid-turn tab must still refuse as in-use, got %v", err)
	}

	// The owned variant parks the same state: the turn is the heartbeat's own.
	ownedApp := &App{tabs: map[string]*WorkspaceTab{}}
	ownedApp.tabs["hb"] = &WorkspaceTab{
		ID: "hb", SessionPath: sessionPath,
		Ctrl: &backgroundRuntimeController{status: control.RuntimeStatus{Running: true}},
	}
	if err := ownedApp.parkTabAsDetachedOwned("hb"); err != nil {
		t.Fatalf("owned park of the heartbeat's own running turn must succeed, got %v", err)
	}
	if _, ok := ownedApp.tabs["hb"]; ok {
		t.Fatal("owned park must take the tab out of the strip")
	}
	if len(ownedApp.detachedSessions) != 1 {
		t.Fatalf("owned park must detach the runtime, got %d detached", len(ownedApp.detachedSessions))
	}

	// A pending approval belongs to the same owned turn — the approval card
	// replays onto the surface when the runtime is promoted back.
	pendingApp := &App{tabs: map[string]*WorkspaceTab{}}
	pendingApp.tabs["hb"] = &WorkspaceTab{
		ID: "hb", SessionPath: sessionPath,
		Ctrl: &backgroundRuntimeController{status: control.RuntimeStatus{PendingPrompt: true}},
	}
	if err := pendingApp.parkTabAsDetachedOwned("hb"); err != nil {
		t.Fatalf("owned park must exempt the owned turn's pending prompt, got %v", err)
	}

	// The active-tab fence survives the owned variant: a tab the user is
	// looking at never gets yanked.
	focused := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "hb"}
	focused.tabs["hb"] = &WorkspaceTab{
		ID: "hb", SessionPath: sessionPath,
		Ctrl: &backgroundRuntimeController{},
	}
	if err := focused.parkTabAsDetachedOwned("hb"); !errors.Is(err, errParkTabInUse) {
		t.Fatalf("owned park of the active tab must refuse as in-use, got %v", err)
	}
	if _, ok := focused.tabs["hb"]; !ok {
		t.Fatal("a refused owned park must leave the tab visible")
	}
}

// TestHeartbeatBackgroundLeavesUserVisibleTabAlone is the zero-regression
// pin: a topic tab the USER has in the strip (visible-before) is never
// parked, in either of two consecutive runs — the baseline behaviour 742
// promised, byte for byte.
func TestHeartbeatBackgroundLeavesUserVisibleTabAlone(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	engine.heartbeatBackground = func() bool { return true }
	seed := HeartbeatTask{
		ID:           "user-tab",
		Title:        "User tab",
		Prompt:       "first",
		ApprovalMode: "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	tabIDCh := publishControllerAfterDelay(t, app, ctrl, 50*time.Millisecond)

	// Run 1: the run opens its own tab, submits, and parks it (owned fence).
	first := engine.executeTask(seed)
	if first.TopicID == "" || len(ctrl.submitted) != 1 {
		t.Fatalf("run 1 must submit once (topic %q, submitted %v)", first.TopicID, ctrl.submitted)
	}
	tabID := <-tabIDCh
	// Simulate the user opening/promoting the very same conversation: the tab
	// goes back into the strip by the user's own hand.
	var tab *WorkspaceTab
	app.mu.Lock()
	for _, detached := range app.detachedSessions {
		if detached != nil && detached.ID == tabID {
			tab = detached
		}
	}
	if tab == nil {
		app.mu.Unlock()
		t.Fatal("run 1 must have parked its runtime into the detached pool")
	}
	delete(app.detachedSessions, sessionRuntimeKey(tab.SessionPath))
	app.tabs[tab.ID] = tab
	app.mu.Unlock()
	ctrl.status = control.RuntimeStatus{} // the turn is over by now
	// The stub controller never emits the TurnDone whose fan-out clears the
	// tab sink's in-flight marker; the user-visible equivalent (a finished
	// turn) is what run 2 needs the surface to look like.
	tab.sink.cancelTurnStart()

	// Run 2: the tab is user-visible before the snapshot — it must stay.
	engine.executeTask(first)
	if len(ctrl.submitted) != 2 {
		t.Fatalf("run 2 must submit again (submitted %v)", ctrl.submitted)
	}
	app.mu.RLock()
	_, stillVisible := app.tabs[tabID]
	detachedCount := len(app.detachedSessions)
	app.mu.RUnlock()
	if !stillVisible {
		t.Fatal("a user-visible topic tab must never be parked (baseline behaviour)")
	}
	if detachedCount != 0 {
		t.Fatalf("a user-visible topic tab must not be detached, got %d detached", detachedCount)
	}
}

// shrinkHeartbeatParkWindow shrinks the heartbeat deferred-park knobs for the
// gate tests and restores them on cleanup.
func shrinkHeartbeatParkWindow(t *testing.T, timeout, poll time.Duration) {
	t.Helper()
	oldTimeout, oldPoll := heartbeatDeferredParkTimeout, collabParkRuntimePollInterval
	heartbeatDeferredParkTimeout, collabParkRuntimePollInterval = timeout, poll
	t.Cleanup(func() {
		heartbeatDeferredParkTimeout, collabParkRuntimePollInterval = oldTimeout, oldPoll
	})
}

// heartbeatParkWaiterIdleWait waits until the app has no live heartbeat
// deferred-park waiter.
func heartbeatParkWaiterIdleWait(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		app.mu.RLock()
		idle := len(app.heartbeatParkWaiters) == 0
		app.mu.RUnlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat deferred-park waiter did not settle in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestHeartbeatDeferredParkTakesTabOutOnceIdle pins the deferred waiter
// (task 865 fix 2): a tab the run could not park yet stays pending — and
// heartbeatTopicTabVisible must NOT count it as user-visible while the waiter
// is alive (that misread is the 864 self-lock) — then the tab leaves the bar
// as soon as its own turn goes idle.
func TestHeartbeatDeferredParkTakesTabOutOnceIdle(t *testing.T) {
	shrinkHeartbeatParkWindow(t, 2*time.Second, 5*time.Millisecond)
	app := &App{tabs: map[string]*WorkspaceTab{}}
	tab := &WorkspaceTab{
		ID: "hb", Scope: "global", TopicID: "topic-x",
		SessionPath: filepath.Join("C:", "ws", "hb.jsonl"),
		Ctrl:        &backgroundRuntimeController{status: control.RuntimeStatus{Running: true}},
	}
	app.tabs["hb"] = tab

	app.deferHeartbeatPark("hb")

	app.mu.RLock()
	pending := app.heartbeatParkWaiters["hb"]
	app.mu.RUnlock()
	if !pending {
		t.Fatal("deferHeartbeatPark must arm the per-tab waiter")
	}
	if app.heartbeatTopicTabVisible("global", "", "topic-x") {
		t.Fatal("a tab carrying a pending heartbeat park must not count as user-visible (864 self-lock)")
	}

	tab.Ctrl.(*backgroundRuntimeController).status = control.RuntimeStatus{}
	heartbeatParkWaiterIdleWait(t, app)

	app.mu.RLock()
	_, stillVisible := app.tabs["hb"]
	detached := len(app.detachedSessions)
	app.mu.RUnlock()
	if stillVisible {
		t.Fatal("once its own turn goes idle the pending tab must leave the bar")
	}
	if detached != 1 {
		t.Fatalf("the deferred park must detach the runtime, got %d", detached)
	}
}

// TestHeartbeatDeferredParkYieldsToUserOpen pins the user-intent veto: if the
// user clicks onto the pending tab while the waiter waits, the deferred park
// stands down and the surface stays theirs.
func TestHeartbeatDeferredParkYieldsToUserOpen(t *testing.T) {
	shrinkHeartbeatParkWindow(t, 2*time.Second, 5*time.Millisecond)
	app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "hb"}
	app.tabs["hb"] = &WorkspaceTab{
		ID: "hb", SessionPath: filepath.Join("C:", "ws", "hb.jsonl"),
		Ctrl: &backgroundRuntimeController{status: control.RuntimeStatus{Running: true}},
	}

	app.deferHeartbeatPark("hb")
	heartbeatParkWaiterIdleWait(t, app)

	app.mu.RLock()
	_, stillVisible := app.tabs["hb"]
	detached := len(app.detachedSessions)
	app.mu.RUnlock()
	if !stillVisible {
		t.Fatal("a tab the user opened must stay in the bar (user intent outranks the park)")
	}
	if detached != 0 {
		t.Fatalf("a user-opened tab must not be detached, got %d", detached)
	}
}

// TestHeartbeatBackgroundBusySkipDefersParkUntilIdle pins the busy-skip
// wiring (task 865 fix 3): the run that promoted a still-busy runtime back
// into the strip skips the tick (LastRunAt untouched, no double submit —
// task 437 semantics kept) AND arms the deferred park, so the tab leaves the
// strip when the turn finishes instead of self-locking into the bar.
func TestHeartbeatBackgroundBusySkipDefersParkUntilIdle(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkHeartbeatParkWindow(t, 2*time.Second, 5*time.Millisecond)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	engine.heartbeatBackground = func() bool { return true }
	seed := HeartbeatTask{
		ID:           "busy-skip",
		Title:        "Busy skip",
		Prompt:       "not delivered this tick",
		ApprovalMode: "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{status: control.RuntimeStatus{Running: true}}
	tabIDCh := publishControllerAfterDelay(t, app, ctrl, 30*time.Millisecond)

	got := engine.executeTask(seed)
	if len(ctrl.submitted) != 0 {
		t.Fatalf("a busy controller must not be injected (submitted %v)", ctrl.submitted)
	}
	if got.LastRunAt != 0 {
		t.Fatal("a skipped run must leave LastRunAt untouched so the next tick retries")
	}
	tabID := <-tabIDCh
	app.mu.RLock()
	pending := app.heartbeatParkWaiters[tabID]
	app.mu.RUnlock()
	if !pending {
		t.Fatal("the busy skip must arm the deferred park for the run's own tab")
	}

	ctrl.status = control.RuntimeStatus{}
	heartbeatParkWaiterIdleWait(t, app)
	app.mu.RLock()
	_, stillVisible := app.tabs[tabID]
	app.mu.RUnlock()
	if stillVisible {
		t.Fatal("the promoted tab must leave the strip once its turn goes idle (no self-lock)")
	}
}

// TestHeartbeatBackgroundSlowBootDefersParkAfterPublish pins the
// controller-not-ready wiring (task 865 fix 3): a run that gives up waiting
// for a slow build skips the tick (task 197 semantics kept) AND arms the
// deferred park, so the tab the run opened leaves the bar once the build
// publishes an idle controller.
func TestHeartbeatBackgroundSlowBootDefersParkAfterPublish(t *testing.T) {
	isolateDesktopUserDirs(t)
	shrinkHeartbeatParkWindow(t, 2*time.Second, 5*time.Millisecond)
	original := heartbeatControllerWaitTimeout
	heartbeatControllerWaitTimeout = 200 * time.Millisecond
	t.Cleanup(func() { heartbeatControllerWaitTimeout = original })

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	engine.heartbeatBackground = func() bool { return true }
	seed := HeartbeatTask{
		ID:           "slow-boot",
		Title:        "Slow boot",
		Prompt:       "delivered next tick",
		ApprovalMode: "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	ctrl := &heartbeatExecuteTaskCtrlStub{}
	tabIDCh := publishControllerAfterDelay(t, app, ctrl, time.Second)

	got := engine.executeTask(seed)
	if len(ctrl.submitted) != 0 {
		t.Fatalf("a run without a controller must not submit (submitted %v)", ctrl.submitted)
	}
	if got.LastRunAt != 0 {
		t.Fatal("a skipped run must leave LastRunAt untouched so the next tick retries")
	}
	app.mu.RLock()
	waiters := len(app.heartbeatParkWaiters)
	app.mu.RUnlock()
	if waiters != 1 {
		t.Fatalf("the skipped run must arm exactly one deferred park, got %d", waiters)
	}

	tabID := <-tabIDCh // the build publishes a second later; the waiter parks it
	heartbeatParkWaiterIdleWait(t, app)
	app.mu.RLock()
	_, stillVisible := app.tabs[tabID]
	app.mu.RUnlock()
	if stillVisible {
		t.Fatal("the run's tab must leave the strip once the controller publishes idle (no self-lock)")
	}
}
