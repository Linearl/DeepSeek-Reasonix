package main

// Task 550 mechanism tests. The 2026-10-06 acceptance revision replaced "the
// blank row must disappear" with four mechanism criteria:
// ① index-write failures are logged and counted, never swallowed;
// ② a three-source inventory reconcile (tabs ↔ topic-state ↔ session files)
//    lists mismatches as queryable state;
// ③ the project-tree visibility rule for unnamed topics is one stable pure
//    predicate — the same data renders the same rows on every re-render;
// ④ blank intermediate states may exist, but they stay visible to the
//    reconcile instead of being auto-erased.
// The symptom-level assertions (no ghost row, honest repair banner) ride on
// the same predicates and are covered alongside.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sessioncatalog"
)

// ── 机制验收① 索引错误不再被吞 ─────────────────────────────────────

// TestNewSessionTopicIndexFailureIsCounted forces the topic-state write to
// fail (nonexistent workspace root) and asserts the failure counter moves.
// Before task 550 the error was discarded with `_ =`, so a tab whose sidebar
// row later disagreed with the index had no retrievable cause.
func TestNewSessionTopicIndexFailureIsCounted(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	missingRoot := filepath.Join(t.TempDir(), "gone")
	tab := &WorkspaceTab{ID: "fresh", Scope: "project", WorkspaceRoot: missingRoot}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}

	before := app.topicIndexWriteFailures.Load()
	app.assignFreshSessionTopic(tab)
	if after := app.topicIndexWriteFailures.Load(); after != before+1 {
		t.Fatalf("topicIndexWriteFailures = %d, want %d after a failed index write", after, before+1)
	}
	if strings.TrimSpace(tab.TopicID) == "" {
		t.Fatal("assignFreshSessionTopic left the tab without a topic ID; the session must stay usable after a failed index write")
	}
	if cached := app.GetTopicInventoryMismatches(); cached.ReconciledAt != 0 || cached.Mismatches == nil {
		t.Fatalf("GetTopicInventoryMismatches before any reconcile = %+v, want the empty zero-time result", cached)
	}
}

// ── 机制验收② 三源对账 ────────────────────────────────────────────

// TestReconcileTopicInventoryReportsMismatches seeds every mismatch kind the
// reconcile defines and asserts each is listed with a handling path:
// a tab whose topic is missing from the index, a tab whose session file is
// gone, and an indexed global topic with no session file at all (the task
// 550 "Global orphan" class).
func TestReconcileTopicInventoryReportsMismatches(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Inventory Project"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	dir := desktopSessionDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	keepPath := writeTopicSessionWithPrompt(t, dir, "keep.jsonl", "topic-keep", "Keep", root, "hello inventory", time.Now())
	if err := ensureTopicIndexedWithCreatedAt("project", root, "topic-keep", "Keep", topicTitleSourceManual, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed keep topic index: %v", err)
	}

	app := NewApp()
	installSessionCatalogForTest(t, app, dir, "project", root)
	catalog := app.sessionCatalog.Load()
	if catalog == nil {
		t.Fatal("session catalog is not installed")
	}
	syncMetadata := func() {
		t.Helper()
		if err := app.syncSessionCatalogMetadata(context.Background(), catalog); err != nil {
			t.Fatalf("sync session catalog metadata: %v", err)
		}
	}
	syncMetadata()

	// The Global orphan: indexed (topic-state + registry) but zero sessions
	// and no tab anywhere — the exact class the task 550 cleanup removed by
	// hand from the live store.
	if err := ensureTopicIndexedWithCreatedAt("global", "", "topic-orphan", defaultTopicTitle, topicTitleSourceAuto, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed orphan topic: %v", err)
	}
	syncMetadata()

	app.mu.Lock()
	app.tabs["keep"] = &WorkspaceTab{ID: "keep", Scope: "project", WorkspaceRoot: root,
		TopicID: "topic-keep", TopicTitle: "Keep", SessionPath: keepPath}
	app.tabs["noindex"] = &WorkspaceTab{ID: "noindex", Scope: "project", WorkspaceRoot: root,
		TopicID: "topic-unindexed", TopicTitle: "Not indexed", SessionPath: ""}
	app.tabs["ghostfile"] = &WorkspaceTab{ID: "ghostfile", Scope: "project", WorkspaceRoot: root,
		TopicID: "topic-keep", TopicTitle: "Keep", SessionPath: filepath.Join(dir, "missing.jsonl")}
	app.mu.Unlock()

	result := app.ReconcileTopicInventory()
	if !result.SessionCatalogOpen {
		t.Fatal("reconcile ran with the session catalog closed; the projection should be open")
	}
	kinds := map[string][]TopicInventoryMismatch{}
	for _, mismatch := range result.Mismatches {
		if strings.TrimSpace(mismatch.Handling) == "" {
			t.Fatalf("mismatch %s(%s) carries no handling path", mismatch.Kind, mismatch.TopicID)
		}
		kinds[mismatch.Kind] = append(kinds[mismatch.Kind], mismatch)
	}
	if len(kinds[topicInventoryTabWithoutIndexEntry]) != 1 {
		t.Fatalf("tab_without_index_entry mismatches = %+v, want exactly the noindex tab", kinds[topicInventoryTabWithoutIndexEntry])
	}
	if len(kinds[topicInventoryTabWithoutSessionFile]) != 1 {
		t.Fatalf("tab_without_session_file mismatches = %+v, want exactly the ghostfile tab", kinds[topicInventoryTabWithoutSessionFile])
	}
	orphanFound := false
	for _, mismatch := range kinds[topicInventoryIndexTopicWithoutSessions] {
		if mismatch.TopicID == "topic-orphan" && mismatch.Scope == "global" {
			orphanFound = true
		}
	}
	if !orphanFound {
		t.Fatalf("index_topic_without_session_or_tab mismatches = %+v, want the seeded global orphan", kinds[topicInventoryIndexTopicWithoutSessions])
	}
	for _, mismatch := range result.Mismatches {
		if mismatch.TopicID == "topic-keep" && mismatch.TabID != "ghostfile" {
			// The ghostfile tab intentionally carries the keep topic with a
			// missing file (its own expected mismatch above); everything else
			// touching the healthy topic must be silent.
			t.Fatalf("healthy topic keep reported as unexpected mismatch: %+v", mismatch)
		}
	}

	cached := app.GetTopicInventoryMismatches()
	if cached.ReconciledAt != result.ReconciledAt || len(cached.Mismatches) != len(result.Mismatches) {
		t.Fatalf("cached result %+v does not match the run %+v", cached, result)
	}
}

// ── 机制验收③ UI 过滤稳定化 ────────────────────────────────────────

// TestBlankTopicRuntimeVisibilityIsDeterministic renders the runtime
// projection repeatedly over unchanged data and requires the blank row to be
// absent on every pass while the content-ful row (still default-titled: the
// auto rename has not landed) is present on every pass. Renaming is the one
// visible transition, never a per-render flash.
func TestBlankTopicRuntimeVisibilityIsDeterministic(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	blankPath := filepath.Join(dir, "blank-stub.jsonl")
	if err := os.WriteFile(blankPath, nil, 0o600); err != nil {
		t.Fatalf("write zero-byte stub: %v", err)
	}
	contentPath := writeTopicSessionWithPrompt(t, dir, "content.jsonl", "topic-content", defaultTopicTitle, "", "first user turn", time.Now())

	app := NewApp()
	app.mu.Lock()
	app.tabs["blank"] = &WorkspaceTab{ID: "blank", Scope: "global", TopicID: "topic-blank",
		TopicTitle: defaultTopicTitle, topicTitleSource: topicTitleSourceAuto, SessionPath: blankPath}
	app.tabs["content"] = &WorkspaceTab{ID: "content", Scope: "global", TopicID: "topic-content",
		TopicTitle: defaultTopicTitle, topicTitleSource: topicTitleSourceAuto, SessionPath: contentPath}
	app.mu.Unlock()

	labels := func() map[string]string {
		out := map[string]string{}
		for _, topic := range app.GetProjectTreeRuntimeSnapshot().Topics {
			out[topic.Node.TopicID] = topic.Node.Label
		}
		return out
	}
	for range 5 {
		got := labels()
		if _, present := got["topic-blank"]; present {
			t.Fatalf("blank default-titled row rendered (topics %v); the unnamed tab must stay in the tab bar on every render", got)
		}
		if got["topic-content"] != defaultTopicTitle {
			t.Fatalf("content-ful default-titled row missing or renamed: %v — a conversation with transcript content stays visible before the auto rename lands", got)
		}
	}

	// Renaming the blank tab flips visibility exactly once; the row then
	// stays stable across renders.
	app.mu.Lock()
	app.tabs["blank"].TopicTitle = "Ghost renamed"
	app.mu.Unlock()
	for range 3 {
		got := labels()
		if got["topic-blank"] != "Ghost renamed" {
			t.Fatalf("renamed blank row = %v, want the manual title on every render", got)
		}
	}
}

// TestOrdinaryTreeHidesBlankShellIsPure pins the catalog-side predicate as a
// total function: identical input, identical verdict, on every call. The
// opposing inputs (renamed, pinned, running, has turns or preview) must all
// keep the row.
func TestOrdinaryTreeHidesBlankShellIsPure(t *testing.T) {
	app := NewApp()
	renamed := blankShellTopic()
	renamed.Title = "恢复你好"
	pinned := blankShellTopic()
	pinned.Pinned = true
	turned := blankShellTopic()
	turned.Turns = 1
	withSessionTurns := blankShellTopic()
	withSessionTurns.Sessions = []sessioncatalog.SessionRecord{{Path: "/s/a.jsonl", Turns: 2}}
	withSessionPreview := blankShellTopic()
	withSessionPreview.Sessions = []sessioncatalog.SessionRecord{{Path: "/s/a.jsonl", Preview: "draft text"}}
	blankWithZeroTurnSession := blankShellTopic()
	blankWithZeroTurnSession.Sessions = []sessioncatalog.SessionRecord{{Path: "/s/blank.jsonl", Turns: 0}}
	blankEmptyTitle := blankShellTopic()
	blankEmptyTitle.Title = ""

	cases := []struct {
		name     string
		topic    sessioncatalog.TopicRecord
		running  bool
		wantHide bool
	}{
		{name: "renamed title keeps the row", topic: renamed},
		{name: "topic turns keep the row", topic: turned},
		{name: "pinned keeps the row", topic: pinned},
		{name: "running keeps the row", topic: blankShellTopic(), running: true},
		{name: "session turns keep the row", topic: withSessionTurns},
		{name: "session preview keeps the row", topic: withSessionPreview},
		{name: "plain blank hides", topic: blankShellTopic(), wantHide: true},
		{name: "empty title hides", topic: blankEmptyTitle, wantHide: true},
		{name: "zero-turn blank session hides", topic: blankWithZeroTurnSession, wantHide: true},
	}
	for _, tc := range cases {
		for range 5 {
			if got := app.ordinaryTreeHidesBlankShell(tc.topic, tc.running); got != tc.wantHide {
				t.Fatalf("%s: ordinaryTreeHidesBlankShell = %v, want %v (verdict must be identical on every call)", tc.name, got, tc.wantHide)
			}
		}
	}
}

func blankShellTopic() sessioncatalog.TopicRecord {
	return sessioncatalog.TopicRecord{Scope: "global", TopicID: "topic-blank", Title: defaultTopicTitle}
}

// ── 症状验收① 修复横幅判据（Go 侧字段总被填充的守护）──────────────

// TestSessionCatalogStatusAlwaysSerializesRepairActive guards the pairing the
// frontend now relies on: the JSON payload always carries repairActive (and
// its siblings), so "missing field" can only mean an old backend, never a
// half-filled struct. The frontend banner reads only these precise fields
// after task 550 removed the legacy repairPending fallback.
func TestSessionCatalogStatusAlwaysSerializesRepairActive(t *testing.T) {
	data, err := json.Marshal(sessionCatalogStatus(sessioncatalog.Status{State: sessioncatalog.StateReady}))
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	for _, key := range []string{"repairPending", "repairActive", "repairDeferred", "repairBlocked"} {
		value, present := fields[key]
		if !present {
			t.Fatalf("clean catalog status JSON is missing %q (omitempty would reopen the fallback flash): %s", key, data)
		}
		if strings.TrimSpace(string(value)) != "0" {
			t.Fatalf("clean catalog status %s = %s, want 0", key, value)
		}
	}
}
