package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/tool/builtin"
)

// guardExemptCtrlStub exposes a fixed session path so the test can compute the
// branch identity the heartbeat tools forward (jobs.SessionFromContext ←
// parentSessionID = agent.BranchID(SessionPath)).
type guardExemptCtrlStub struct {
	heartbeatExecuteTaskCtrlStub
	sessionPath string
}

func (s *guardExemptCtrlStub) SessionPath() string { return s.sessionPath }

// noticeRecordingSink captures the events a tab's sink emits; wired as the
// tabEventSink's botSink (every event is forwarded there), it proves the 1062
// exemption notice reaches the panel plane instead of staying silent.
type noticeRecordingSink struct {
	events []event.Event
}

func (s *noticeRecordingSink) Emit(e event.Event) { s.events = append(s.events, e) }

func (s *noticeRecordingSink) noticesWithCode(code string) int {
	n := 0
	for _, e := range s.events {
		if e.Kind == event.Notice && e.Code == code {
			n++
		}
	}
	return n
}

// newGuardExemptFixture builds an adapter over an engine whose app holds one
// tab in the requested autopilot posture, with its sink recording into the
// returned recorder. detached=true registers the tab in the detached pool
// instead of the strip (task 742 background runs must not escape the guard).
func newGuardExemptFixture(t *testing.T, autopilot, detached bool) (heartbeatManagerAdapter, *noticeRecordingSink, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	sessionPath := filepath.Join(t.TempDir(), "sessions", "topic_guardex.jsonl")
	tab := &WorkspaceTab{
		ID:        "tab_guardex",
		TopicID:   "topic_guardex",
		Ctrl:      &guardExemptCtrlStub{sessionPath: sessionPath},
		Scope:     "global",
		Ready:     true,
		autopilot: autopilot,
	}
	rec := &noticeRecordingSink{}
	tab.sink = &tabEventSink{tabID: tab.ID, app: nil, botSink: rec}
	app.mu.Lock()
	if detached {
		app.ensureDetachedSessionsLocked()
		app.detachedSessions[tab.ID] = tab
	} else {
		app.tabs[tab.ID] = tab
	}
	app.mu.Unlock()
	engine := &HeartbeatEngine{app: app, pendingTopics: map[string]heartbeatPendingTopic{}}
	return heartbeatManagerAdapter{engine: engine}, rec, agent.BranchID(sessionPath)
}

func guardExemptCreateReq(caller string, revision uint64) builtin.HeartbeatUpsertRequest {
	return builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"title": true, "prompt": true, "interval": true},
			Title:    "watcher", Prompt: "check and report", Interval: "30m",
		},
		ExpectedRevision: revision,
		CallerSession:    caller,
	}
}

// Acceptance 3, exempted state: an autopilot session creating a NEW scheduled
// task is refused with the recursion-guard error AND the panel-plane notice —
// nothing is written, nothing is silent.
func TestHeartbeatGuardCreateExemptsAutopilotCaller(t *testing.T) {
	a, rec, caller := newGuardExemptFixture(t, true, false)
	view := mustList(t, a)
	_, err := a.UpsertTask(guardExemptCreateReq(caller, view.Revision))
	if err == nil {
		t.Fatal("autopilot caller's creation must be refused")
	}
	if !strings.Contains(err.Error(), "recursion guard") || !strings.Contains(err.Error(), "1062") {
		t.Fatalf("err = %v, want the task-1062 recursion-guard refusal", err)
	}
	if n := rec.noticesWithCode(NoticeCodeAutopilotGuardCreateExempt); n != 1 {
		t.Fatalf("exempt notices = %d, want exactly one on the panel plane (不留静默)", n)
	}
	fresh := mustList(t, a)
	if len(fresh.Tasks) != 0 || fresh.Revision != 0 {
		t.Fatalf("tasks after exempted create = %+v, want nothing written", fresh.Tasks)
	}
}

// The guard reaches background runs too: a detached (parked, task 742)
// autopilot tab is resolved by the same sweep.
func TestHeartbeatGuardCreateExemptsDetachedAutopilotCaller(t *testing.T) {
	a, rec, caller := newGuardExemptFixture(t, true, true)
	view := mustList(t, a)
	if _, err := a.UpsertTask(guardExemptCreateReq(caller, view.Revision)); err == nil {
		t.Fatal("a detached autopilot tab's creation must be refused too")
	}
	if n := rec.noticesWithCode(NoticeCodeAutopilotGuardCreateExempt); n != 1 {
		t.Fatalf("exempt notices = %d, want one", n)
	}
}

// Acceptance 3, unchanged state: a non-autopilot caller (the 守护线 posture)
// creates scheduled tasks exactly as before, with no notice.
func TestHeartbeatGuardCreateAllowsNonAutopilotCaller(t *testing.T) {
	a, rec, caller := newGuardExemptFixture(t, false, false)
	view := mustList(t, a)
	res, err := a.UpsertTask(guardExemptCreateReq(caller, view.Revision))
	if err != nil {
		t.Fatalf("non-autopilot caller's creation must proceed: %v", err)
	}
	if !res.Created {
		t.Fatalf("create receipt wrong: %+v", res)
	}
	if n := rec.noticesWithCode(NoticeCodeAutopilotGuardCreateExempt); n != 0 {
		t.Fatalf("no notice may fire on the allowed path, got %d", n)
	}
}

// An unresolvable caller (CLI/serve — no live tab) is not exempted: the guard
// only narrows the desktop autopilot context.
func TestHeartbeatGuardCreateAllowsUnresolvableCaller(t *testing.T) {
	a, rec, _ := newGuardExemptFixture(t, true, false)
	view := mustList(t, a)
	if _, err := a.UpsertTask(guardExemptCreateReq("no-such-branch", view.Revision)); err != nil {
		t.Fatalf("unresolvable caller must proceed (guard is context-narrow): %v", err)
	}
	if n := rec.noticesWithCode(NoticeCodeAutopilotGuardCreateExempt); n != 0 {
		t.Fatalf("no notice may fire on the allowed path, got %d", n)
	}
}

// Updates to EXISTING tasks stay allowed for an autopilot caller: modifying
// what the user already has is not layer creation.
func TestHeartbeatGuardUpdateAllowsAutopilotCaller(t *testing.T) {
	a, rec, caller := newGuardExemptFixture(t, true, false)
	view := mustList(t, a)
	// Host-side create (no caller identity): lands despite the autopilot tab.
	created, err := a.UpsertTask(guardExemptCreateReq("", view.Revision))
	if err != nil {
		t.Fatalf("host-side create must proceed: %v", err)
	}
	updated, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"id": true, "title": true},
			ID:       created.TaskID, Title: "renamed by autopilot",
		},
		ExpectedRevision: created.Revision,
		CallerSession:    caller,
	})
	if err != nil {
		t.Fatalf("autopilot caller's UPDATE must proceed: %v", err)
	}
	if updated.Created {
		t.Fatal("an id-bearing upsert is an update, not a create")
	}
	if n := rec.noticesWithCode(NoticeCodeAutopilotGuardCreateExempt); n != 0 {
		t.Fatalf("no notice may fire on the update path, got %d", n)
	}
}
