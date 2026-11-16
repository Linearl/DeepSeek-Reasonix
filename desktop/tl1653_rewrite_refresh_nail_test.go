package main

// tl1653 断言扩展（1606 族形态）：「后端状态改写 → 前端必刷新」三颗钉。
//
// 现场（20261116 21:24 问题咨询-接续）：切 effort→max 走 fallback 重建
// （19.7s），swap 落地 1 秒后 open-session 自愈 Diet 把转录 179→35 条 CAS
// 改写（folded=145，与用户截图「折叠145条」吻合）——改写只有 slog 与备份，
// 没有任何 wire 信号：前端驻留页与 revision/digest 指纹静默失效，下次切回
// 的 freshness 门虽判 resident-stale 实拉了一页，但落地的页面在改写后的
// 状态上收敛为空（Transcript 首帧门 empty=true，seq=2/seq=3 两次切回都无
// first-frame 行），空页又匹配新指纹被驻留——空白自持，直到关 tab 重开。
//
// 三颗钉（前红后绿）：
//   钉①（Fix2）：effort fallback swap 在 runtime:rebuilt 围栏后必须同车道
//     推送 tab:meta（tl1389 模型切换 parity）——前端指纹不再等无关拉取。
//   钉②（Fix3）：fast path 的静默早退分支必须留痕——事故全卷 grep
//     "effort override declined" 零命中的观测缺口：runtime 缺位的 tab
//     （open-topic 冷读、无活控制器）连 per-request 臂都进不去，declined
//     日志根本没机会发。现在该分支发 fast-skipped-no-controller。
//   钉③（Fix1+1606 扩形态）：Diet 改写落盘后必须发 session_changed+
//     SessionReset（head 切换分支 SwitchBranch 已有的同一契约，前端
//     session-changed 面据此整页重载）——且改写后 HistorySliceForTab 立即
//     服务折叠后转录（非空、含折叠占位、revision 前进）＝「改写后必渲染」。

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1653RecordSink captures every event a controller emits, for the
// session_changed assertion. The effort nails reuse the runtime-event name
// recorder from async_effort_rebuild_test.go instead (they assert emitRuntimeEvent names).
func tl1653RecordSink() (event.FuncSink, func() []event.Event) {
	var mu sync.Mutex
	var seen []event.Event
	sink := event.FuncSink(func(e event.Event) {
		mu.Lock()
		seen = append(seen, e)
		mu.Unlock()
	})
	return sink, func() []event.Event {
		mu.Lock()
		defer mu.Unlock()
		out := make([]event.Event, len(seen))
		copy(out, seen)
		return out
	}
}

// tl1653BigTurns mirrors the tl1570 fixture: big assistant turns so the
// pricing estimate crosses the diet line against a small test window.
func tl1653BigTurns(turns int) []provider.Message {
	big := strings.Repeat("word ", 500)
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "SYS"},
		{Role: provider.RoleUser, Content: "standing constraint: never change the public API"},
	}
	for range turns {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: big},
			provider.Message{Role: provider.RoleUser, Content: "continue"},
		)
	}
	return msgs
}

// tl1653SeededController seeds the over-window session file and reopens it
// the way the effort rebuild does (LoadSession + Resume), with the self-heal
// gate held OFF through Resume so the background heal cannot race the
// synchronous assertions. Returns the reopened controller.
func tl1653SeededController(t *testing.T, dir, name string, msgs []provider.Message, sink event.Sink) *control.Controller {
	t.Helper()
	// The gate is package-global: hold it OFF for seeding/Resume (the real
	// hook fires the heal in the background; this nail drives it
	// synchronously) and leave it off on cleanup — the test-binary default.
	control.SetSessionSelfHealGate(nil)
	t.Cleanup(func() { control.SetSessionSelfHealGate(nil) })

	path := filepath.Join(dir, name)
	exec := agent.New(nil, nil, agent.NewSession("SYS"), agent.Options{}, event.Discard)
	seed := control.New(control.Options{Executor: exec, SessionDir: dir, SessionPath: path, Label: "seed", Sink: event.Discard})
	t.Cleanup(seed.Close)
	exec.Session().Messages = msgs
	if err := seed.Snapshot(); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	exec2 := agent.New(nil, nil, agent.NewSession("SYS"), agent.Options{}, event.Discard)
	c := control.New(control.Options{Executor: exec2, SessionDir: dir, SessionPath: path, Label: "reopen", Sink: sink})
	t.Cleanup(c.Close)
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	c.Resume(loaded, path)
	return c
}

// 钉③：Diet 改写落盘 → session_changed+SessionReset 上线 + 改写后转录在册
// 可渲染（非空、含折叠占位、revision 前进）。1606 族的「改写后必渲染」
// 在 Diet 形态上的扩展。
func TestTL1653DietRewriteAnnouncesResetAndServesFoldedTranscript(t *testing.T) {
	dir := reloadRuntimeFixture(t)
	msgs := tl1653BigTurns(8)
	sink, snapshotEvents := tl1653RecordSink()
	c := tl1653SeededController(t, dir, "tl1653-diet.jsonl", msgs, sink)
	path := c.SessionPath()

	preMeta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("pre-rewrite branch meta: ok=%v err=%v", ok, err)
	}

	// 无信号反例先证：改写前的 sink 里没有 session_changed。
	for _, e := range snapshotEvents() {
		if e.Kind == event.SessionChanged {
			t.Fatalf("session_changed emitted before any rewrite: %+v", e)
		}
	}

	control.SetSessionSelfHealGate(func() bool { return true })
	out := c.SelfHealOpenSession(5000)
	if !out.Dieted {
		t.Fatalf("fixture stopped dieting — the nail is stale: detail=%q", out.Detail)
	}

	// 修后行为：改写完成必须发 session_changed + SessionReset（head 切换分支
	// 的同一契约），路径=被改写的会话文件。
	found := false
	for _, e := range snapshotEvents() {
		if e.Kind == event.SessionChanged && e.SessionReset {
			if filepath.Clean(e.SessionPath) != filepath.Clean(path) {
				t.Fatalf("session_changed carried path %q, want %q", e.SessionPath, path)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("diet rewrite emitted no session_changed+SessionReset; the frontend would keep the stale resident page: %+v", snapshotEvents())
	}

	// 必渲染：改写后 HistorySliceForTab 立即可服务折叠后转录。
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{ID: "tab_tl1653", Scope: "global", Ready: true, Ctrl: c, model: "old/old-model",
		sink: &tabEventSink{tabID: "tab_tl1653", app: app, ctx: context.Background()}, disabledMCP: map[string]ServerView{}}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		tab.releaseSessionLease()
	})

	slice := app.HistorySliceForTab(tab.ID, HistorySliceRequest{Cursor: "", Turns: 60})
	if slice.Error != "" {
		t.Fatalf("post-rewrite history slice failed: %s", slice.Error)
	}
	if len(slice.Entries) == 0 {
		t.Fatal("post-rewrite history slice is empty; the rewritten transcript must be renderable immediately")
	}
	foldPlaceholder := false
	for _, entry := range slice.Entries {
		if strings.Contains(entry.Message.Content, "本地折叠") {
			foldPlaceholder = true
		}
	}
	if !foldPlaceholder {
		t.Fatalf("fold placeholder missing from the served page; rows=%d", len(slice.Entries))
	}
	postMeta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("post-rewrite branch meta: ok=%v err=%v", ok, err)
	}
	if postMeta.Revision <= preMeta.Revision {
		t.Fatalf("rewrite did not advance the revision: pre=%d post=%d", preMeta.Revision, postMeta.Revision)
	}
}

// 钉①：effort fallback swap 的围栏后必须同车道推 tab:meta。重用
// async_effort_rebuild_test.go 的 harness（stub provider 无 per-request
// 词表 → 两快路径都 decline → 后台重建）。
func TestTL1653EffortFallbackSwapPushesMetaRefresh(t *testing.T) {
	asyncEffortTestConfig(t)
	var mu sync.Mutex
	var eventNames []string
	record := func(name string) {
		mu.Lock()
		eventNames = append(eventNames, name)
		mu.Unlock()
	}
	app := NewApp()
	app.ctx = context.Background()
	app.runtimeEvents.emit = func(_ context.Context, name string, _ ...any) { record(name) }
	dir := config.SessionDir()
	tab, oldCtrl := asyncEffortTab(t, app, dir, record)

	if err := app.SetEffortForTab(tab.ID, "low"); err != nil {
		t.Fatalf("declined effort switch returned an error: %v", err)
	}
	waitForEffortSettled(t, app, tab.ID)
	if tab.Ctrl == oldCtrl {
		t.Fatal("effort fallback did not swap the controller")
	}

	mu.Lock()
	defer mu.Unlock()
	hasFence, hasMeta := false, false
	for _, name := range eventNames {
		switch name {
		case "runtime:rebuilt":
			hasFence = true
		case tabMetaRefreshEventChannel:
			hasMeta = true
		}
	}
	if !hasFence {
		t.Fatalf("runtime:rebuilt fence missing: %v", eventNames)
	}
	if !hasMeta {
		t.Fatalf("tab:meta push missing after the effort swap (tl1653 fix ②); the frontend fingerprint waits for an unrelated pull: %v", eventNames)
	}
}

// 钉②：runtime 缺位的 tab（事故形态：open-topic 冷读、无活控制器）切
// effort 时，per-request 臂静默跳过——现在必须留痕
// fast-skipped-no-controller，declined 观测缺口闭合。
func TestTL1653EffortFastPathLogsNoControllerSkip(t *testing.T) {
	asyncEffortTestConfig(t)
	buf := captureSlog(t)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{ID: "tab_noc", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app, ctx: context.Background()}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		tab.releaseSessionLease()
		app.mu.RLock()
		ctrl := tab.Ctrl
		app.mu.RUnlock()
		if ctrl != nil {
			ctrl.Close()
		}
	})

	if app.controllerForTab(tab) != nil {
		t.Fatal("harness tab must have no live controller for this nail")
	}
	// The call returns at once (tl1388 async fallback); the fast-path skip
	// line is synchronous with the call. The background rebuild is drained
	// below so the nail never leaks a goroutine into a later test.
	if err := app.SetEffortForTab(tab.ID, "max"); err != nil {
		t.Fatalf("effort switch on a controller-less tab failed: %v", err)
	}
	log := buf.String()
	if !strings.Contains(log, "fast-skipped-no-controller") {
		t.Fatalf("controller-less fast path exited silently again (the tl1653 observation gap is back):\n%s", log)
	}
	waitForEffortSettled(t, app, tab.ID)
}
