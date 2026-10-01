package main

// Task 437: heartbeat "resume an existing conversation" mode (ReuseSession).
// Covers the mode-switch contract (off = existing semantics byte-for-byte,
// on = append to the bound conversation), the busy-conversation mutex (no
// injection into a running turn, retried next tick), and the binding marker
// (persisted topicId + origin stamp only on self-created topics).

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"reasonix/internal/control"
)

// injectHeartbeatCtrl polls app.tabs until a tab exists without a controller,
// cancels its build, and injects ctrl — the same wiring the existing
// executeTask tests use to emulate an async tab boot.
func injectHeartbeatCtrl(t *testing.T, app *App, ctrl *heartbeatExecuteTaskCtrlStub) chan struct{} {
	t.Helper()
	injected := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-injected:
				return
			case <-ticker.C:
				var cancel context.CancelFunc
				var tabToInject *WorkspaceTab
				app.mu.Lock()
				for _, tab := range app.tabs {
					if tab == nil {
						continue
					}
					tab.removed = true
					cancel = tab.buildCancel
					tabToInject = tab
					break
				}
				app.mu.Unlock()
				if tabToInject == nil {
					continue
				}
				if cancel != nil {
					cancel()
				}
				app.mu.Lock()
				if tabToInject.Ctrl == nil {
					tabToInject.Ctrl = ctrl
					tabToInject.Ready = true
					tabToInject.StartupErr = ""
					app.advanceSessionRuntimeEpochLocked(tabToInject)
					app.mu.Unlock()
					close(injected)
					return
				}
				app.mu.Unlock()
			}
		}
	}()
	return injected
}

func newReuseSessionTestEngine(t *testing.T) (*HeartbeatEngine, *App) {
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
	return engine, app
}

func topicOrigin(t *testing.T, app *App, topicID string) (string, bool) {
	t.Helper()
	snap, err := app.topicState.snapshot("")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]json.RawMessage
	record, ok := snap.Records[topicID]
	if !ok || len(record.AutoMeta) == 0 {
		return "", false
	}
	if err := json.Unmarshal(record.AutoMeta, &meta); err != nil {
		t.Fatal(err)
	}
	raw, stamped := meta["origin"]
	if !stamped {
		return "", false
	}
	var origin string
	if err := json.Unmarshal(raw, &origin); err != nil {
		t.Fatal(err)
	}
	return origin, true
}

// idleHeartbeatConversation emulates the submitted turn finishing: the
// controller drops back to idle and the tab's turn reservation clears. This is
// what a real TurnDone fan-out leaves behind, without the async fan-out.
func idleHeartbeatConversation(t *testing.T, app *App, ctrl *heartbeatExecuteTaskCtrlStub) {
	t.Helper()
	app.mu.Lock()
	var sink *tabEventSink
	for _, tab := range app.tabs {
		if tab != nil && tab.sink != nil {
			sink = tab.sink
			break
		}
	}
	app.mu.Unlock()
	if sink != nil {
		sink.cancelTurnStart()
	}
	ctrl.status = control.RuntimeStatus{}
}

// Mode switch: reuse on + bound topic → the same conversation is reused, the
// prompt is appended (no fresh topic per run), and no pending guard latches on.
func TestHeartbeatExecuteTaskReuseSessionResumesBoundConversation(t *testing.T) {
	engine, app := newReuseSessionTestEngine(t)
	ctrl := &heartbeatExecuteTaskCtrlStub{}
	seed := HeartbeatTask{
		ID:           "resume",
		Title:        "Resume",
		Prompt:       "tick one",
		ReuseSession: true,
		// Both flags set: ReuseSession must win, no fresh topic may spawn.
		NewConversationEachRun: true,
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	injected := injectHeartbeatCtrl(t, app, ctrl)
	first := engine.executeTask(seed)
	<-injected

	if first.TopicID == "" {
		t.Fatal("first resume run should create and bind a topic")
	}
	if len(ctrl.submitted) != 1 || ctrl.submitted[0] != "tick one" {
		t.Fatalf("submitted = %v, want [tick one]", ctrl.submitted)
	}
	if _, latched := engine.pendingTopics["resume"]; latched {
		t.Fatal("resume mode must not latch the fresh-conversation pending guard")
	}

	// Second run on the updated task (previous turn finished): same
	// conversation, prompt appended.
	idleHeartbeatConversation(t, app, ctrl)
	second := engine.executeTask(first)
	if second.TopicID != first.TopicID {
		t.Fatalf("resume run created a new topic %q, want bound %q", second.TopicID, first.TopicID)
	}
	if len(ctrl.submitted) != 2 || ctrl.submitted[1] != "tick one" {
		t.Fatalf("submitted = %v, want the prompt appended twice", ctrl.submitted)
	}
	if second.LastRunAt == 0 || second.LastRunAt < first.LastRunAt {
		t.Fatalf("second run LastRunAt = %d, must advance past %d", second.LastRunAt, first.LastRunAt)
	}
}

// Binding marker: a self-created resume topic gets the heartbeat origin stamp
// (sidebar grouping), while a pre-existing bound conversation keeps its own
// origin — the bound session is often the user's working session.
func TestHeartbeatReuseSessionStampsOnlySelfCreatedTopics(t *testing.T) {
	engine, app := newReuseSessionTestEngine(t)

	// Self-created: resolve with no binding stamps the fresh topic.
	task := HeartbeatTask{ID: "resume", Title: "Resume", Prompt: "p", ReuseSession: true}
	updated, topicID, _, ok := engine.resolveHeartbeatTopic(task, "global", "", "Heartbeat: Resume")
	if !ok || topicID == "" || updated.TopicID != topicID {
		t.Fatalf("resume resolve should create and bind, got ok=%v topic=%q", ok, topicID)
	}
	if origin, stamped := topicOrigin(t, app, topicID); !stamped || origin != heartbeatTopicOrigin {
		t.Fatalf("self-created resume topic origin = %q stamped=%v, want %q", origin, stamped, heartbeatTopicOrigin)
	}

	// Pre-existing: create a topic outside resume mode, bind it, resolve again.
	external, err := app.CreateTopic("global", "", "User working session")
	if err != nil {
		t.Fatal(err)
	}
	bound := HeartbeatTask{ID: "resume2", Title: "Resume2", Prompt: "p", ReuseSession: true, TopicID: external.ID}
	if _, got, _, ok := engine.resolveHeartbeatTopic(bound, "global", "", "Heartbeat: Resume2"); !ok || got != external.ID {
		t.Fatalf("bound resolve should reuse %q, got %q", external.ID, got)
	}
	if origin, stamped := topicOrigin(t, app, external.ID); stamped && origin == heartbeatTopicOrigin {
		t.Fatal("pre-existing bound conversation must not be re-stamped as heartbeat origin")
	}
}

// Writer-lease mutex: while the bound conversation's controller is mid-turn,
// the run must not inject (no submit, no approval-mode flip, LastRunAt
// untouched so the schedule retries when the conversation is idle again).
func TestHeartbeatExecuteTaskReuseSessionSkipsWhileConversationBusy(t *testing.T) {
	engine, app := newReuseSessionTestEngine(t)
	ctrl := &heartbeatExecuteTaskCtrlStub{}
	seed := HeartbeatTask{ID: "resume", Title: "Resume", Prompt: "tick", ReuseSession: true}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	injected := injectHeartbeatCtrl(t, app, ctrl)
	first := engine.executeTask(seed)
	<-injected
	if first.LastRunAt == 0 || len(ctrl.submitted) != 1 {
		t.Fatalf("first run should submit once, got LastRunAt=%d submitted=%v", first.LastRunAt, ctrl.submitted)
	}

	// Conversation busy (a turn is running): skip without injecting.
	ctrl.status = control.RuntimeStatus{Running: true}
	busy := engine.executeTask(first)
	if len(ctrl.submitted) != 1 {
		t.Fatalf("busy run injected a prompt: %v", ctrl.submitted)
	}
	if busy.LastRunAt != first.LastRunAt {
		t.Fatalf("busy run advanced LastRunAt to %d, want untouched %d", busy.LastRunAt, first.LastRunAt)
	}

	// Idle again (the turn finished): the next tick's retry appends normally.
	idleHeartbeatConversation(t, app, ctrl)
	retried := engine.executeTask(busy)
	if len(ctrl.submitted) != 2 {
		t.Fatalf("retry after idle should append, submitted = %v", ctrl.submitted)
	}
	if retried.TopicID != first.TopicID {
		t.Fatalf("retry reused %q, want bound %q", retried.TopicID, first.TopicID)
	}
	if retried.LastRunAt <= busy.LastRunAt {
		t.Fatalf("retry LastRunAt = %d, want > %d", retried.LastRunAt, busy.LastRunAt)
	}
}

// Mode switch with the toggle off: an existing topicId binding does not pin
// the conversation — fresh-conversation mode still spawns a new topic, and
// only the resume switch makes resolveHeartbeatTopic honor the binding.
func TestHeartbeatReuseSessionOffKeepsFreshConversationRun(t *testing.T) {
	engine, _ := newReuseSessionTestEngine(t)
	const boundTopic = "pre-existing-bound-topic"

	fresh := HeartbeatTask{ID: "fresh", Title: "Fresh", Prompt: "ping", NewConversationEachRun: true, TopicID: boundTopic}
	updated, topicID, _, ok := engine.resolveHeartbeatTopic(fresh, "global", "", "Heartbeat: Fresh")
	if !ok || topicID == "" || topicID == boundTopic {
		t.Fatalf("fresh mode should create a new topic despite a binding, got ok=%v topic=%q", ok, topicID)
	}
	if updated.TopicID != topicID {
		t.Fatalf("fresh mode should persist the latest topic, got %q want %q", updated.TopicID, topicID)
	}

	resume := HeartbeatTask{ID: "resume", Title: "Resume", Prompt: "p", ReuseSession: true, TopicID: boundTopic}
	if _, got, _, ok := engine.resolveHeartbeatTopic(resume, "global", "", "Heartbeat: Resume"); !ok || got != boundTopic {
		t.Fatalf("resume mode should honor the binding, got ok=%v topic=%q", ok, got)
	}
}
