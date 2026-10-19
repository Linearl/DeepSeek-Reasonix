package main

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

// 任务991 验收①③: the background-tab ask toast chain's backend half.
// ① an AskRequest on a provably-background tab fires exactly one
//    ask:background notice carrying the same-source waitMs; the same ask on
//    the active tab (or with no active tab known) fires nothing;
// ② the toast-click hop (NotifyAskReattachForTab) reaches the tab's
//    controller — the fresh-window re-arm a click must produce, distinct
//    from the stop-the-timer interaction signal.

// reattachProbe records NotifyAskReattached forwards without embedding a real
// controller; the embedded SessionAPI keeps the tab map's interface satisfied.
type reattachProbe struct {
	control.SessionAPI
	got chan string
}

func (p *reattachProbe) NotifyAskReattached(id string) {
	p.got <- id
}

func backgroundAskEvent(waitMs int64) event.Event {
	return event.Event{
		Kind:   event.AskRequest,
		TurnID: "turn-bg",
		ItemID: "ask-bg",
		Ask: event.Ask{
			ID: "ask-bg", TurnID: "turn-bg",
			Questions:        []event.AskQuestion{{ID: "q1", Prompt: "Ship the fix or hold the release?"}},
			AutoDecideWaitMs: waitMs,
		},
	}
}

// ① 后台 tab 到达 → 一条 ask:background 通知，waitMs 同源透传、summary 截取。
func TestAskOnBackgroundTabEmitsBackgroundNotice(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "tab-active"}
	var gotName string
	var gotPayload any
	app.noteBackgroundAskToast("tab-bg", backgroundAskEvent(15000), func(name string, payload ...any) {
		gotName = name
		if len(payload) == 1 {
			gotPayload = payload[0]
		}
	})
	if gotName != askBackgroundNoticeEvent {
		t.Fatalf("notice channel = %q, want %q", gotName, askBackgroundNoticeEvent)
	}
	notice, ok := gotPayload.(AskBackgroundNotice)
	if !ok {
		t.Fatalf("notice payload type = %T, want AskBackgroundNotice", gotPayload)
	}
	if notice.TabID != "tab-bg" || notice.AskID != "ask-bg" {
		t.Fatalf("notice routing = %+v, want tab-bg/ask-bg", notice)
	}
	if notice.WaitMs != 15000 {
		t.Fatalf("notice waitMs = %d, want 15000 (the same-source window the timer arms with)", notice.WaitMs)
	}
	if notice.Summary != "Ship the fix or hold the release?" {
		t.Fatalf("notice summary = %q, want the first question", notice.Summary)
	}
}

// ① 反面：活跃 tab 不发（用户正看着面板）；无活跃 tab（启动/重放窗口）也不发
// ——后台判定必须是阳性事实，猜测会把每次重开的每个挂起 ask 都弹一遍。
func TestAskOnActiveTabEmitsNoBackgroundNotice(t *testing.T) {
	fire := func(active string) int {
		app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: active}
		count := 0
		app.noteBackgroundAskToast("tab-bg", backgroundAskEvent(15000), func(string, ...any) { count++ })
		return count
	}
	if got := fire("tab-bg"); got != 0 {
		t.Fatalf("active tab fired %d background notice(s), want 0", got)
	}
	if got := fire(""); got != 0 {
		t.Fatalf("no-active-tab surface fired %d background notice(s), want 0 (startup/replay must not re-toast)", got)
	}
}

// ① 空壳防御：无 ask body 的 AskRequest 不发（非 ask 路径误触发的兜底）。
func TestBackgroundNoticeIgnoresAsklessEvent(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "tab-active"}
	count := 0
	app.noteBackgroundAskToast("tab-bg", event.Event{Kind: event.AskRequest}, func(string, ...any) { count++ })
	if count != 0 {
		t.Fatalf("askless AskRequest fired %d background notice(s), want 0", count)
	}
}

// ② 点击跳板：NotifyAskReattachForTab 把重置窗口信号转给该 tab 的控制器。
func TestNotifyAskReattachForTabForwardsToController(t *testing.T) {
	probe := &reattachProbe{got: make(chan string, 1)}
	tab := &WorkspaceTab{ID: "tab-bg", Ctrl: probe, sink: &tabEventSink{tabID: "tab-bg"}}
	app := &App{tabs: map[string]*WorkspaceTab{"tab-bg": tab}, activeTabID: "tab-active"}
	app.NotifyAskReattachForTab("tab-bg", "ask-bg")
	select {
	case id := <-probe.got:
		if id != "ask-bg" {
			t.Fatalf("reattach forwarded id = %q, want ask-bg", id)
		}
	case <-time.After(time.Second):
		t.Fatal("NotifyAskReattachForTab never reached the tab controller")
	}

	// 未知 tab / 空 id：静默无操作（fire-and-forget 姿态），不得 panic。
	app.NotifyAskReattachForTab("tab-missing", "ask-bg")
	app.NotifyAskReattachForTab("tab-bg", "")
}

// The sink-level hook wires the notice into Emit; pin the channel name the
// frontend subscribes to so a rename cannot silently orphan the toast.
func TestAskBackgroundNoticeChannelName(t *testing.T) {
	if askBackgroundNoticeEvent != "ask:background" {
		t.Fatalf("askBackgroundNoticeEvent = %q, want ask:background (frontend bridge contract)", askBackgroundNoticeEvent)
	}
}

// ① 钩子接线：Emit 路径上的 AskRequest 必须触发后台判定——防止那三行接线被
// 静默删除而 toast 链路无声失效（前端测试无法覆盖后端 Emit 内部）。
func TestTabSinkEmitWiresBackgroundAskNotice(t *testing.T) {
	app := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: "tab-active"}
	sink := &tabEventSink{tabID: "tab-bg", app: app}
	sink.setContext(context.Background())
	delivered := captureRuntimeEmits(t, sink)

	sink.Emit(backgroundAskEvent(15000))

	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-delivered:
			if got.name != askBackgroundNoticeEvent {
				continue // the ordinary wire event channel; keep waiting for the notice
			}
			notice, ok := got.payload.(AskBackgroundNotice)
			if !ok {
				t.Fatalf("Emit notice payload type = %T, want AskBackgroundNotice", got.payload)
			}
			if notice.TabID != "tab-bg" || notice.AskID != "ask-bg" || notice.WaitMs != 15000 {
				t.Fatalf("Emit notice = %+v, want tab-bg/ask-bg/waitMs=15000", notice)
			}
			return
		case <-deadline:
			t.Fatal("Emit never fired the ask:background notice for a background tab")
		}
	}
}
