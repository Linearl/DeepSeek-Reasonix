package main

import (
	"os"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

func TestReapStaleTopicActivityStatus(t *testing.T) {
	isolateDesktopUserDirs(t)
	now := time.Now()
	cases := []struct {
		name       string
		status     string
		age        time.Duration
		wantStatus string
	}{
		{"fresh thinking kept", topicStatusThinking, time.Minute, topicStatusThinking},
		{"fresh streaming kept", topicStatusStreaming, 5 * time.Minute, topicStatusStreaming},
		{"stale thinking reaped", topicStatusThinking, topicActivityStatusTTL + time.Minute, ""},
		{"stale streaming reaped", topicStatusStreaming, topicActivityStatusTTL + time.Minute, ""},
		{"waiting confirmation never reaped", topicStatusWaitingConfirmation, topicActivityStatusTTL + time.Minute, topicStatusWaitingConfirmation},
		{"error status never reaped", topicStatusError, topicActivityStatusTTL + time.Minute, topicStatusError},
		{"zero timestamp never reaped", topicStatusThinking, 0, topicStatusThinking},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp()
			tab := &WorkspaceTab{
				ID:             "tab",
				Scope:          "global",
				TopicID:        "topic_ttl",
				Ready:          true,
				ActivityStatus: tc.status,
				disabledMCP:    map[string]ServerView{},
			}
			if tc.age > 0 {
				app.projectTreeRuntime.setActivityAt(tab.ID, now.Add(-tc.age))
			}
			app.tabs[tab.ID] = tab
			app.reapStaleTopicActivityStatus(now)
			if tab.ActivityStatus != tc.wantStatus {
				t.Fatalf("status after reap = %q, want %q", tab.ActivityStatus, tc.wantStatus)
			}
		})
	}
}

func TestReconcileTabActivityStatus(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeTopicSessionWithPrompt(t, dir, "reconcile.jsonl", "topic_reconcile", "Reconcile", "", "prompt", time.Now())

	newTab := func(ctrl control.SessionAPI) *WorkspaceTab {
		return &WorkspaceTab{
			ID:             "tab",
			Scope:          "global",
			WorkspaceRoot:  globalTabWorkspaceRoot(),
			TopicID:        "topic_reconcile",
			SessionPath:    path,
			Ctrl:           ctrl,
			Ready:          true,
			ActivityStatus: topicStatusThinking,
			disabledMCP:    map[string]ServerView{},
		}
	}

	t.Run("clears status the idle controller does not corroborate", func(t *testing.T) {
		app := NewApp()
		ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "idle", Sink: event.Discard})
		defer ctrl.Close()
		tab := newTab(ctrl)
		app.tabs[tab.ID] = tab
		if !app.reconcileTabActivityStatus(tab) {
			t.Fatal("reconcile should clear the stale status")
		}
		if tab.ActivityStatus != "" {
			t.Fatalf("status after reconcile = %q, want cleared", tab.ActivityStatus)
		}
	})

	t.Run("keeps status while the controller is running", func(t *testing.T) {
		app := NewApp()
		runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
		ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: path, Label: "running", Sink: event.Discard})
		defer ctrl.Close()
		tab := newTab(ctrl)
		app.tabs[tab.ID] = tab
		ctrl.Submit("keep running")
		<-runner.started
		if app.reconcileTabActivityStatus(tab) {
			t.Fatal("reconcile must not clear the status of a running turn")
		}
		if tab.ActivityStatus != topicStatusThinking {
			t.Fatalf("status after reconcile = %q, want %q", tab.ActivityStatus, topicStatusThinking)
		}
		close(runner.release)
		waitNotRunning(t, ctrl)
	})

	t.Run("clears stale waiting_confirmation when no prompt is pending", func(t *testing.T) {
		// 任务 730: an event-driven 待确认 badge whose prompt is gone (the
		// resolution/TurnDone was lost, e.g. while detached) must clear on the
		// open-moment reconcile — the controller is the source of truth.
		app := NewApp()
		ctrl := control.New(control.Options{SessionDir: dir, SessionPath: path, Label: "idle", Sink: event.Discard})
		defer ctrl.Close()
		tab := newTab(ctrl)
		tab.ActivityStatus = topicStatusWaitingConfirmation
		app.tabs[tab.ID] = tab
		if !app.reconcileTabActivityStatus(tab) {
			t.Fatal("reconcile should clear the stale waiting_confirmation badge")
		}
		if tab.ActivityStatus != "" {
			t.Fatalf("status after reconcile = %q, want cleared", tab.ActivityStatus)
		}
	})

	t.Run("keeps waiting_confirmation while a prompt is pending", func(t *testing.T) {
		app := NewApp()
		ctrl := &pendingPromptSessionAPI{stubSessionAPI{}}
		tab := newTab(ctrl)
		tab.ActivityStatus = topicStatusWaitingConfirmation
		app.tabs[tab.ID] = tab
		if app.reconcileTabActivityStatus(tab) {
			t.Fatal("reconcile must not clear a corroborated waiting_confirmation badge")
		}
		if tab.ActivityStatus != topicStatusWaitingConfirmation {
			t.Fatalf("status after reconcile = %q, want %q", tab.ActivityStatus, topicStatusWaitingConfirmation)
		}
	})
}

// pendingPromptSessionAPI fakes a controller holding an unanswered ask/approval
// (task 730): only the two reconcile inputs are overridden; every other
// SessionAPI call would panic, which is exactly the guard the shared
// stubSessionAPI doc comment asks for.
type pendingPromptSessionAPI struct {
	stubSessionAPI
}

func (pendingPromptSessionAPI) Running() bool       { return false }
func (pendingPromptSessionAPI) PendingPrompt() bool { return true }
