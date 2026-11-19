package main

// tl1710 验收矩阵的 desktop 侧真实夹具回归：
//
//   A1 trash_list 列表与磁盘一致（真实删除通道 deleteSessionFile 进桶 →
//      agentTrashList 断言条目数/hasTurn/deletedAt/restoreUntil/路径）；
//   A3 trash_restore 还原（桶消失、转录字节一致、branch meta 的分组/话题
//      字段保留、ListTrashedSessions 不再含该条）；
//   A4 trash_restore 冲突拒绝（原位非空 stub → 拒绝且桶原样保留）。
//
// 空壳桶（hasTurn=false）与真会话桶（hasTurn=true）在同一夹具内对照——
// 20261119『决策模式』事故的判别字段就是 hasTurn。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// tl1710StageLiveSession creates a live session (transcript + optional branch
// meta sidecar) in dir and returns its path. withTurn=false leaves the
// transcript 0-byte — the empty-shell shape.
func tl1710StageLiveSession(t *testing.T, dir, name string, withTurn, withMeta bool) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := ""
	if withTurn {
		content = `{"role":"user","content":"real conversation turn"}` + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write transcript %s: %v", name, err)
	}
	if withMeta {
		if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
			Scope:       "global",
			TopicID:     "topic_tl1710_" + strings.TrimSuffix(name, store.SessionTranscriptSuffix),
			TopicTitle:  "tl1710 topic " + strings.TrimSuffix(name, store.SessionTranscriptSuffix),
			CustomTitle: "tl1710 titled " + strings.TrimSuffix(name, store.SessionTranscriptSuffix),
		}); err != nil {
			t.Fatalf("save branch meta %s: %v", name, err)
		}
	}
	return path
}

func tl1710TrashPath(t *testing.T, dir, name string) string {
	t.Helper()
	return filepath.Join(dir, sessionTrashDir, name, name)
}

// A1: the agent's trash listing matches the disk — one record per bucket,
// hasTurn from the transcript itself, deletedAt from the trash meta.
func TestAgentTrashListMatchesDisk(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	live := tl1710StageLiveSession(t, dir, "20261120-090000.000000000-tl1710-live.jsonl", true, true)
	shell := tl1710StageLiveSession(t, dir, "20261120-090100.000000000-tl1710-shell.jsonl", false, false)
	if err := deleteSessionFile(dir, live); err != nil {
		t.Fatalf("trash live: %v", err)
	}
	if err := deleteSessionFile(dir, shell); err != nil {
		t.Fatalf("trash shell: %v", err)
	}

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })
	entries, err := app.agentTrashList()
	if err != nil {
		t.Fatalf("agentTrashList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (live + shell): %+v", len(entries), entries)
	}
	byStem := map[string]agent.TrashEntry{}
	for _, e := range entries {
		byStem[strings.TrimSuffix(filepath.Base(e.SessionPath), store.SessionTranscriptSuffix)] = e
	}
	liveEntry, ok := byStem["20261120-090000.000000000-tl1710-live"]
	if !ok {
		t.Fatalf("live bucket missing from listing: %+v", entries)
	}
	shellEntry, ok := byStem["20261120-090100.000000000-tl1710-shell"]
	if !ok {
		t.Fatalf("shell bucket missing from listing: %+v", entries)
	}

	// hasTurn is the incident's discriminator: real conversation vs empty shell.
	if !liveEntry.HasTurn {
		t.Fatal("live bucket reported hasTurn=false — the 20261119 incident blind spot")
	}
	if shellEntry.HasTurn {
		t.Fatal("empty-shell bucket reported hasTurn=true")
	}
	if liveEntry.Title != "tl1710 titled 20261120-090000.000000000-tl1710-live" {
		t.Fatalf("live title = %q, want the branch-meta custom title", liveEntry.Title)
	}
	// The shell has no meta and no title sidecar — the stem fallback keeps a
	// stable name for the caller.
	if shellEntry.Title == "" {
		t.Fatalf("shell title empty, want stem fallback: %+v", shellEntry)
	}
	if liveEntry.RestoreUntil != agent.TrashRestoreUntilManual || shellEntry.RestoreUntil != agent.TrashRestoreUntilManual {
		t.Fatalf("restoreUntil drift: %q / %q, want constant %q", liveEntry.RestoreUntil, shellEntry.RestoreUntil, agent.TrashRestoreUntilManual)
	}
	if liveEntry.DeletedAt <= 0 {
		t.Fatalf("deletedAt = %d, want the trash-meta timestamp", liveEntry.DeletedAt)
	}
	// sessionPath is the bucket transcript, and it really exists on disk.
	for _, e := range []agent.TrashEntry{liveEntry, shellEntry} {
		if !strings.Contains(filepath.Clean(e.SessionPath), sessionTrashDir) {
			t.Fatalf("sessionPath %q is not inside the trash dir", e.SessionPath)
		}
		if _, err := os.Stat(e.SessionPath); err != nil {
			t.Fatalf("sessionPath %q not on disk: %v", e.SessionPath, err)
		}
	}
}

// A3: restore returns the bucket to its pre-delete state — transcript bytes,
// branch meta (grouping/topic fields), no bucket left, no trash listing row.
func TestAgentTrashRestoreRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	live := tl1710StageLiveSession(t, dir, "20261120-090200.000000000-tl1710-rt.jsonl", true, true)
	before, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	metaBefore, ok, err := agent.LoadBranchMeta(live)
	if err != nil || !ok {
		t.Fatalf("load pre-delete meta: ok=%v err=%v", ok, err)
	}
	if err := deleteSessionFile(dir, live); err != nil {
		t.Fatalf("trash: %v", err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("live transcript still present after trash: %v", err)
	}

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })
	trashPath := tl1710TrashPath(t, dir, filepath.Base(live))
	entries, err := app.agentTrashList()
	if err != nil {
		t.Fatalf("agentTrashList: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("listing after trash = %d entries, want 1: %+v", len(entries), entries)
	}
	if err := app.agentTrashRestore(entries[0].SessionPath); err != nil {
		t.Fatalf("agentTrashRestore: %v", err)
	}

	after, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("restored transcript unreadable: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("restored transcript bytes differ from the pre-delete bytes")
	}
	metaAfter, ok, err := agent.LoadBranchMeta(live)
	if err != nil || !ok {
		t.Fatalf("restored meta unreadable: ok=%v err=%v", ok, err)
	}
	// 分组/话题字段与删前一致。TopicTitle 走 restoreSessionTopicIndex 的既有
	// 显示名规则（topicTitleFromText，tabs.go 18-rune 截断 + "…"）——UI Trash
	// 页还原同样如此，这里断言的是「与既有通道行为一致」而非逐字节相等
	// （tl1710 non-goal：不改 UI 还原通道本身）。
	if metaAfter.TopicID != metaBefore.TopicID || metaAfter.CustomTitle != metaBefore.CustomTitle {
		t.Fatalf("restored meta drift:\n  before %+v\n  after  %+v", metaBefore, metaAfter)
	}
	if want := topicTitleFromText(metaBefore.TopicTitle); metaAfter.TopicTitle != want {
		t.Fatalf("restored TopicTitle = %q, want the restore-chain display form %q", metaAfter.TopicTitle, want)
	}
	if _, err := os.Stat(filepath.Dir(trashPath)); !os.IsNotExist(err) {
		t.Fatalf("trash bucket survived restore: %v", err)
	}
	entries, err = app.agentTrashList()
	if err != nil {
		t.Fatalf("agentTrashList after restore: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("trash listing after restore = %d entries, want 0: %+v", len(entries), entries)
	}
}

// A4: a conflict refusal leaves the bucket untouched — nothing is lost.
func TestAgentTrashRestoreConflictKeepsBucket(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	live := tl1710StageLiveSession(t, dir, "20261120-090300.000000000-tl1710-conflict.jsonl", true, false)
	if err := deleteSessionFile(dir, live); err != nil {
		t.Fatalf("trash: %v", err)
	}
	// A live non-empty transcript now occupies the destination (a different
	// session reused the stem) — the restore must refuse it.
	if err := os.WriteFile(live, []byte(`{"role":"user","content":"new live session"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write conflicting live stub: %v", err)
	}

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })
	trashPath := tl1710TrashPath(t, dir, filepath.Base(live))
	err := app.agentTrashRestore(trashPath)
	if err == nil {
		t.Fatal("restore over a live non-empty transcript must fail")
	}
	// The refusal text names the conflict AND the bucket survives byte-for-byte.
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("conflict error %q does not name the collision", err)
	}
	bucketData, err := os.ReadFile(trashPath)
	if err != nil {
		t.Fatalf("trash transcript should remain after rejected restore: %v", err)
	}
	if !strings.Contains(string(bucketData), "real conversation turn") {
		t.Fatalf("bucket transcript clobbered: %q", bucketData)
	}
	liveData, err := os.ReadFile(live)
	if err != nil || !strings.Contains(string(liveData), "new live session") {
		t.Fatalf("live transcript clobbered by restore: %q, %v", liveData, err)
	}
}

// The listing the tool exposes must be the SAME chain the Trash page renders —
// if agentTrashList ever forks a second assembly, the incident's "UI shows it,
// agent can't see it" drift class re-opens. Pinned via the shared装配：both
// faces see the bucket under the same directory walk.
func TestAgentTrashListAgreesWithTrashPageListing(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	live := tl1710StageLiveSession(t, dir, "20261120-090400.000000000-tl1710-agree.jsonl", true, true)
	if err := deleteSessionFile(dir, live); err != nil {
		t.Fatalf("trash: %v", err)
	}

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })
	entries, err := app.agentTrashList()
	if err != nil {
		t.Fatalf("agentTrashList: %v", err)
	}
	page := app.ListTrashedSessions()
	if len(entries) != len(page) {
		t.Fatalf("agent listing %d != trash-page listing %d", len(entries), len(page))
	}
	pagePaths := map[string]bool{}
	for _, m := range page {
		pagePaths[m.Path] = true
	}
	for _, e := range entries {
		if !pagePaths[e.SessionPath] {
			t.Fatalf("agent-only entry %q absent from the Trash page listing", e.SessionPath)
		}
	}
}

// The trash-meta the round trip relies on carries the key + timestamp the
// listing reads; pin its on-disk shape so a silent format change breaks here
// instead of in the field.
func TestAgentTrashMetaOnDiskShape(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	live := tl1710StageLiveSession(t, dir, "20261120-090500.000000000-tl1710-meta.jsonl", true, false)
	if err := deleteSessionFile(dir, live); err != nil {
		t.Fatalf("trash: %v", err)
	}
	metaPath := filepath.Join(dir, sessionTrashDir, filepath.Base(live), sessionTrashMetaFile)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read trash meta: %v", err)
	}
	var meta struct {
		Key       string `json:"key"`
		DeletedAt int64  `json:"deletedAt"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("trash meta not the documented JSON shape: %v (%s)", err, raw)
	}
	if meta.Key != filepath.Base(live) {
		t.Fatalf("trash meta key = %q, want %q", meta.Key, filepath.Base(live))
	}
	if meta.DeletedAt <= 0 {
		t.Fatalf("trash meta deletedAt = %d, want a unix-ms timestamp", meta.DeletedAt)
	}
}
