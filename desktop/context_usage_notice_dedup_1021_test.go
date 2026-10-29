package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务 1021 验收（超时提示降噪半）：`context read timed out (>2s); showing
// last known value` 在一次压缩会话内合并计数而非逐条刷屏。
//
//  1. 同一 tab 连续多次超时：只有第一次发用户提示，后续折叠（transcript 里
//     不出现第二条 "timed out"）；warn 日志保留每次发生（882 诊断语义不变）；
//  2. 读恢复成功时补发恰好一条 "recovered; N repeat timeout(s) folded"，
//     N 等于被折叠的次数；之后健康读回到静默（882 正常路径无声）；
//  3. 去重按 tab 隔离：tab A 的 armed 窗口不吞掉 tab B 的首条超时提示。
//
// 实现锚点：context_usage_fetch.go contextUsageNoticeMarkTimeout /
// contextUsageNoticeCleared（a11cdc4ea，任务 1021）。

// newContextUsageTab 依 fixture 的方式再造一个可发 notice 的 tab，供跨 tab
// 隔离断言使用；返回的 channel 收该 tab 的 Notice 事件。
func newContextUsageTab(t *testing.T, f *contextUsageFixture, id string) chan event.Event {
	t.Helper()
	dir := t.TempDir()
	ag := agent.New(usageProvider{usage: &provider.Usage{}}, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	tab := &WorkspaceTab{
		ID:          id,
		Scope:       "global",
		Ready:       true,
		Ctrl:        control.New(control.Options{Executor: ag, Sink: event.Discard, SessionDir: dir, SessionPath: filepath.Join(dir, "session.jsonl")}),
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: f.app}
	installNoopRuntimeEvents(f.app, tab.sink)
	f.app.tabs[tab.ID] = tab
	f.app.tabOrder = append(f.app.tabOrder, tab.ID)
	notices := make(chan event.Event, 8)
	tab.sink.SetBotSink(event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice {
			notices <- e
		}
	}))
	return notices
}

// assertNoNoticeOn 等待一段无 notice 窗口（跨 tab 断言用，独立于 fixture 的
// 主 channel）。
func assertNoNoticeOn(t *testing.T, notices chan event.Event) {
	t.Helper()
	select {
	case e := <-notices:
		t.Fatalf("unexpected notice: %q", e.Text)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestContextUsageTimeoutNoticeDedupedInOneTab 钉验收 1+2：连续三次超时只发
// 一条提示（第二、三次折叠），warn 日志三次全留；恢复后补发恰好一条含折叠
// 计数的恢复提示；此后健康读静默。
func TestContextUsageTimeoutNoticeDedupedInOneTab(t *testing.T) {
	f := newContextUsageFixture(t)
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute { return contextComputeFor(7) }
	if got := f.app.ContextUsageForTab(f.tab.ID); got.Used != 7 {
		t.Fatalf("priming read used = %d, want 7", got.Used)
	}
	f.assertNoNotice(t)

	hang := func(control.SessionAPI) contextUsageCompute {
		time.Sleep(2 * time.Second)
		return contextComputeFor(9)
	}
	f.app.contextUsageReadStub = hang
	// Timeout #1 arms the window and emits the only "timed out" notice.
	_ = f.app.ContextUsageForTab(f.tab.ID)
	f.waitForNotice(t, "timed out", "last known value")

	// Timeouts #2 and #3 fold into the armed notice: no new user-facing line.
	// (Regression = a second "timed out" notice lands here — the transcript
	// flooding that motivated task 1021.)
	_ = f.app.ContextUsageForTab(f.tab.ID)
	_ = f.app.ContextUsageForTab(f.tab.ID)
	f.assertNoNotice(t)
	// The warn log keeps every occurrence (operator channel stays lossy).
	if n := strings.Count(f.logs.String(), "context usage read timed out"); n < 3 {
		t.Fatalf("warn log timeout lines = %d, want >= 3 (folding must not silence the log)", n)
	}

	// Recovery: exactly one follow-up notice carrying the folded count, then
	// the window disarms and healthy reads stay silent again.
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute { return contextComputeFor(8) }
	if got := f.app.ContextUsageForTab(f.tab.ID); got.Used != 8 {
		t.Fatalf("recovery read used = %d, want 8", got.Used)
	}
	f.waitForNotice(t, "recovered", "2 repeat timeout(s) folded")
	_ = f.app.ContextUsageForTab(f.tab.ID)
	f.assertNoNotice(t)
}

// TestContextUsageTimeoutNoticeIndependentPerTab 钉验收 3：tab A 的 armed
// 去重窗口不吞 tab B 的首条超时提示——去重键是 tab，不是全局。
func TestContextUsageTimeoutNoticeIndependentPerTab(t *testing.T) {
	f := newContextUsageFixture(t)
	noticesB := newContextUsageTab(t, f, "tab_context_fetch_b")

	// Prime both tabs so their timeouts fall back to "last known value".
	f.app.contextUsageReadStub = func(control.SessionAPI) contextUsageCompute { return contextComputeFor(11) }
	if got := f.app.ContextUsageForTab(f.tab.ID); got.Used != 11 {
		t.Fatalf("tab A priming read used = %d, want 11", got.Used)
	}
	if got := f.app.ContextUsageForTab("tab_context_fetch_b"); got.Used != 11 {
		t.Fatalf("tab B priming read used = %d, want 11", got.Used)
	}
	f.assertNoNotice(t)
	assertNoNoticeOn(t, noticesB)

	hang := func(control.SessionAPI) contextUsageCompute {
		time.Sleep(2 * time.Second)
		return contextComputeFor(12)
	}
	f.app.contextUsageReadStub = hang
	// Tab A times out and arms its window; tab B's first timeout must still
	// announce itself (per-tab dedup, not global).
	_ = f.app.ContextUsageForTab(f.tab.ID)
	f.waitForNotice(t, "timed out", "last known value")
	_ = f.app.ContextUsageForTab("tab_context_fetch_b")
	f.waitForNoticeOn(t, noticesB, "timed out", "last known value")

	// A second tab A timeout stays folded even while tab B is armed.
	_ = f.app.ContextUsageForTab(f.tab.ID)
	f.assertNoNotice(t)
}

// waitForNoticeOn 是 waitForNotice 的跨 channel 版本：断言恰好一条含全部
// fragments 的 notice 到达（通道属另一个 tab）。
func (f *contextUsageFixture) waitForNoticeOn(t *testing.T, notices chan event.Event, fragments ...string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-notices:
			if e.Kind != event.Notice {
				continue
			}
			match := true
			for _, fragment := range fragments {
				if !strings.Contains(e.Text, fragment) {
					match = false
					break
				}
			}
			if match {
				return
			}
			t.Fatalf("unexpected notice text %q, want fragments %v", e.Text, fragments)
		case <-deadline:
			t.Fatalf("no notice containing %v arrived", fragments)
		}
	}
}
