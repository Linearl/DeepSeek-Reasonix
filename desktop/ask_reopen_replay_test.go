package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
)

// 任务 178（重开重弹）：活跃 turn 关 tab（keep_running → detach）时挂起的
// ask，在重开会话后必须重放到新视图。本测试钉住 backend 半程的投递链：
// detach 时 sink context 置空（事件转缓冲）→ 重开 attach 恢复 context 并
// 重放 pending prompts → 新视图必须再次收到 ask_request 且可作答。
func TestCloseTabDetachThenReopenReplaysPendingAsk(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := filepath.Join(dir, "ask-reopen.jsonl")

	type wireMsg struct {
		tabID  string
		kind   string
		askID  string
	}
	wire := make(chan wireMsg, 32)
	sink := &tabEventSink{tabID: "asktab", ctx: context.Background()}
	sink.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if name != eventChannel || len(payload) == 0 {
			return
		}
		msg := wireMsg{kind: "unknown"}
		switch w := payload[0].(type) {
		case wireEventTab:
			msg.tabID = w.TabID
			msg.kind = w.Kind
			if w.Ask != nil {
				msg.askID = w.Ask.ID
			}
		case correlatedWireEventTab:
			msg.tabID = w.TabID
			msg.kind = w.Kind
			if w.Ask != nil {
				msg.askID = w.Ask.ID
			}
		}
		select {
		case wire <- msg:
		default:
		}
	}

	runner := &desktopAskRuntimeRunner{}
	ctrl := control.New(control.Options{
		Runner:      runner,
		SessionDir:  dir,
		SessionPath: path,
		Label:       "ask-reopen",
		Sink:        sink,
	})
	runner.ask = func(ctx context.Context) error {
		_, err := ctrl.Ask(ctx, []event.AskQuestion{{
			ID:      "choice",
			Prompt:  "Pick one",
			Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}})
		return err
	}

	tab := &WorkspaceTab{
		ID:            "asktab",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		SessionPath:   path,
		Ctrl:          ctrl,
		Ready:         true,
		sink:          sink,
		disabledMCP:   map[string]ServerView{},
	}
	app := NewApp()
	app.ctx = context.Background()
	app.tabs = map[string]*WorkspaceTab{
		"asktab": tab,
		"other":  {ID: "other", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}},
	}
	app.tabOrder = []string{"asktab", "other"}
	app.activeTabID = "asktab"
	app.detachedSessions = map[string]*WorkspaceTab{}

	waitWire := func(step string, wantTab, wantKind string) wireMsg {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case msg := <-wire:
				if msg.tabID == wantTab && strings.Contains(msg.kind, wantKind) {
					return msg
				}
			case <-deadline:
				t.Fatalf("after %s: no %s for tab %s", step, wantKind, wantTab)
				return wireMsg{}
			}
		}
	}

	ctrl.Send("ask user")
	initial := waitWire("initial ask", "asktab", "ask_request")
	if initial.askID == "" {
		t.Fatal("initial ask_request carried no ask id")
	}

	// 关 tab：keep_running 路径 → detach（sink context 置空、turn 不取消）。
	if err := app.CloseTab("asktab"); err != nil {
		t.Fatalf("CloseTab(asktab): %v", err)
	}
	if _, ok := app.detachedSessions[sessionRuntimeKey(path)]; !ok {
		t.Fatalf("detached runtime missing for %q", path)
	}
	if sink.context() != nil {
		t.Fatal("detached sink should have a nil context")
	}
	if st := ctrl.RuntimeStatus(); !st.PendingPrompt {
		t.Fatal("ask should stay pending while the tab is detached")
	}

	// 重开：新 tab 走真实 build/attach 链。
	reopened := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "asktab-reopen")
	reopened.SessionPath = path
	reopened.sink = &tabEventSink{tabID: reopened.ID, app: app}
	app.tabs[reopened.ID] = reopened
	app.tabOrder = append(app.tabOrder, reopened.ID)
	app.activeTabID = reopened.ID
	app.buildTabController(reopened)
	if reopened.Ctrl != ctrl {
		t.Fatalf("reopened controller = %p, want detached %p", reopened.Ctrl, ctrl)
	}

	// 重开后的视图必须收到 ask_request 重放（runtime:rebuilt 先行不计）。
	replay := waitWire("reopen replay", reopened.ID, "ask_request")
	if replay.askID != initial.askID {
		t.Fatalf("replayed ask id = %q, want %q", replay.askID, initial.askID)
	}

	// 面板可答：经重开后的 ask id 作答必须解除挂起。
	ctrl.AnswerQuestion(initial.askID, []event.AskAnswer{{QuestionID: "choice", Selected: []string{"A"}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if !ctrl.RuntimeStatus().PendingPrompt {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("answering the replayed ask did not resolve the prompt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctrl.Close()
}
