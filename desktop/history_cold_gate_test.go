package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/store"
	"reasonix/internal/tool"
)

// tl1281 冷读解码门与切换驱逐的 desktop 层验收：
// ① oversize（≥32MiB，与首绘尾窗同判据）转录的冷读不再走全量解码兜底——
//    事件日志权威且锚页身份失配时，页从锚扫描服务（Source=scan-anchor）；
//    扫描也失败时延迟报错；字段查值回落为未命中；
// ② idle 切换驱逐把 oversize 后台 tab 打回任务 619② 骨架态（Ctrl=nil、
//    点击可重建、冷读可用）；守卫逐条拒绝小会话/当前 tab/未落盘 tab。

// tl1281OverwriteCheckpointGrows replaces the checkpoint with ~mb MiB of valid
// message lines whose content differs from the ledger-anchored transcript, so
// the anchor digest provably diverges from the ledger identity (the sealed
// schema-2 / anchor-behind shape the acceptance samples carry). Bodies repeat
// a realistic mixed paragraph: single-character megabyte bodies are a
// pathological worst case for the transcript classification regexes
// (StripTransientUserBlocks 家族) and would measure fixture, not production.
func tl1281OverwriteCheckpointGrows(t *testing.T, sessionPath string, mb int) {
	t.Helper()
	const bodyBytes = 1 << 16
	paragraph := "Read the config file and report the failing section. " +
		"Context: the loader retried 3 times before surfacing ErrSessionFileLockHeld; " +
		"see internal/agent/save_lock_wait_946_test.go lines 60-75 for the contract.\n"
	body := strings.Repeat(paragraph, bodyBytes/len(paragraph)+1)[:bodyBytes]
	perLine := bodyBytes + 256
	n := mb << 20 / perLine
	if n < 4 {
		n = 4
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		m := provider.Message{Role: provider.RoleUser, Content: body, ID: "G" + string(rune('a'+i%26)) + string(rune('0'+i/26))}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(sessionPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("overwrite checkpoint: %v", err)
	}
}

// tl1281SavedSession persists a small real session (ledger meta, event log,
// display index all present), the shape every save produces.
func tl1281SavedSession(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi"})
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("fixture save: %v", err)
	}
	return path
}

func TestTL1281HistoryColdGateClassification(t *testing.T) {
	path := tl1281SavedSession(t)
	if historyColdDecodeOversize(path) {
		t.Fatal("a small session must not classify as oversize")
	}
	tl1281OverwriteCheckpointGrows(t, path, 33)
	if !historyColdDecodeOversize(path) {
		t.Fatal("a ≥32MiB transcript must classify as oversize (the acceptance samples are 32-173MB)")
	}
}

func TestTL1281ColdHistorySliceOversizeServesScanAnchor(t *testing.T) {
	path := tl1281SavedSession(t)
	tl1281OverwriteCheckpointGrows(t, path, 33)
	// The saved display index describes the original transcript; it is stale
	// against the rewritten checkpoint and must not serve this read.
	_ = os.Remove(store.SessionDisplayIndex(path))

	a := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: ""}
	tr := newHistorySliceTrace("tab_tl1281")
	sessionDir := filepath.Dir(path)
	slice, err := a.coldHistorySlice(sessionDir, path, HistorySliceRequest{Turns: 60}, tr)
	if err != nil {
		t.Fatalf("cold read: %v", err)
	}
	if slice.Source != "scan-anchor" {
		t.Fatalf("oversize cold read source = %q, want scan-anchor (the full-decode fallback must not run)", slice.Source)
	}
	if len(slice.Entries) == 0 {
		t.Fatal("anchor page came back empty")
	}
}

func TestTL1281ColdHistorySliceOversizeDeferredWhenScanFails(t *testing.T) {
	path := tl1281SavedSession(t)
	// Garbage bytes: the bounded scanner refuses, and the gate must defer
	// instead of paying the full authoritative decode.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 33<<20)), 0o600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	_ = os.Remove(store.SessionDisplayIndex(path))

	a := &App{tabs: map[string]*WorkspaceTab{}, activeTabID: ""}
	tr := newHistorySliceTrace("tab_tl1281b")
	_, err := a.coldHistorySlice(filepath.Dir(path), path, HistorySliceRequest{Turns: 60}, tr)
	if err == nil {
		t.Fatal("an oversize transcript whose scan fails must defer with an error, not fall back to a full decode")
	}
	if !strings.Contains(err.Error(), "deferred") {
		t.Fatalf("deferred error = %v, want the cold decode gate message", err)
	}
}

// tl1281EvictableTab builds an App with one built tab over the given session,
// mirroring the appWithIdleTabAt fixture shape.
func tl1281EvictableTab(t *testing.T, path string) (*App, *WorkspaceTab) {
	t.Helper()
	sess, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession fixture: %v", err)
	}
	ag := agent.New(stubProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, SessionDir: filepath.Dir(path), SessionPath: path, Sink: event.Discard})
	tab := &WorkspaceTab{
		ID:          "tl1281_tab",
		Ctrl:        ctrl,
		SessionPath: path,
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: nil}
	a := &App{
		tabs:        map[string]*WorkspaceTab{tab.ID: tab},
		activeTabID: tab.ID,
	}
	tab.sink.app = a
	return a, tab
}

func TestTL1281EvictIdleTabRuntimeReleasesSkeleton(t *testing.T) {
	path := tl1281SavedSession(t)
	tl1281OverwriteCheckpointGrows(t, path, 33)
	a, tab := tl1281EvictableTab(t, path)
	// 后台态：fixture 默认把 tab 设为 active，驱逐要求它已让位。
	a.activeTabID = "other_tab"

	a.evictIdleTabRuntime(tab.ID)

	if tab.Ctrl != nil {
		t.Fatal("an evicted oversize tab must drop its controller (the decoded transcript is the acceptance metric)")
	}
	if tab.Ready {
		t.Fatal("an evicted tab must leave the ready state so the click rebuild re-runs")
	}
	if !lazyTabNeedsBuildLocked(tab) {
		t.Fatal("an evicted tab must be rebuildable through the 任务 619② lazy-build path")
	}
	// The tab stays listed and its session stays cold-readable.
	if _, ok := a.tabs[tab.ID]; !ok {
		t.Fatal("eviction must not remove the tab")
	}
	tr := newHistorySliceTrace(tab.ID)
	if _, err := a.coldHistorySlice(filepath.Dir(path), path, HistorySliceRequest{Turns: 60}, tr); err != nil {
		t.Fatalf("cold read after eviction: %v", err)
	}
}

func TestTL1281EvictSkipsSmallSessions(t *testing.T) {
	path := tl1281SavedSession(t)
	a, tab := tl1281EvictableTab(t, path)

	a.evictIdleTabRuntime(tab.ID)

	if tab.Ctrl == nil {
		t.Fatal("a small session's runtime must not be evicted: its decoded state is negligible and the rebuild cost is not")
	}
}

func TestTL1281EvictSkipsActiveTab(t *testing.T) {
	path := tl1281SavedSession(t)
	tl1281OverwriteCheckpointGrows(t, path, 33)
	a, tab := tl1281EvictableTab(t, path)
	// tab is the active tab in this fixture; eviction must decline.
	a.evictIdleTabRuntime(tab.ID)
	if tab.Ctrl == nil {
		t.Fatal("the active tab must never be evicted")
	}
}

func TestTL1281EvictSkipsUnsavedChanges(t *testing.T) {
	path := tl1281SavedSession(t)
	tl1281OverwriteCheckpointGrows(t, path, 33)
	a, tab := tl1281EvictableTab(t, path)
	// An append without a snapshot: unsaved changes must veto eviction.
	ctrl, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatal("fixture premise: the tab controller is a *control.Controller")
	}
	ctrl.Executor().Session().Add(provider.Message{Role: provider.RoleUser, Content: "draft turn"})
	if !tab.Ctrl.SessionHasUnsavedChanges() {
		t.Fatal("fixture premise: the tab must report unsaved changes")
	}
	a.evictIdleTabRuntime(tab.ID)
	if tab.Ctrl == nil {
		t.Fatal("a tab with unsaved changes must not be evicted")
	}
}
