package main

import (
	"fmt"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// tl1705 复现钉：外部写入者（协作/CLI 进程）推进同一会话文件后，挂着
// desktop 侧 controller 的旁观 tab 走 liveHistorySlice 读到的是 controller
// 内存旧视图——新落盘的行不在页里，切回零感知（20261119 现场：协调线
// tab_0766f36b resident-stale fetch 671ms 后页面依旧，关闭重开冷读才可见）。
// 修复=live 读前对账 branch-meta ledger（CAS revision/digest），内存基线
// 落后即改走冷读（磁盘权威）。
func TestHistorySliceLiveTabBehindExternalWriter(t *testing.T) {
	app := historySliceTestApp(t)
	dir := t.TempDir()
	var msgs []provider.Message
	for i := range 5 {
		msgs = append(msgs, historySliceUser(i, fmt.Sprintf("q%d", i)), historySliceAssistant(i, fmt.Sprintf("a%d", i)))
	}
	sess, path := saveHistorySliceSession(t, dir, "external.jsonl", msgs)
	newLiveHistoryTab(t, app, dir, path, sess)

	// 外部写入者：独立进程视图——加载同一文件、追加一轮、保存。tab 的
	// controller 内存不知道这次推进（无 wire 事件、无内存增长）。
	ext, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("external writer load: %v", err)
	}
	ext.Add(historySliceUser(5, "external-question"))
	ext.Add(historySliceAssistant(5, "external-answer"))
	if err := ext.Save(path); err != nil {
		t.Fatalf("external writer save: %v", err)
	}

	page := app.HistorySliceForTab("test", HistorySliceRequest{Turns: 12})
	if page.Stale {
		t.Fatal("unexpected stale page")
	}
	if len(page.Entries) == 0 {
		t.Fatal("empty page")
	}
	last := page.Entries[len(page.Entries)-1]
	if last.Message.Content != "external-answer" {
		t.Fatalf("latest entry = %q, want external-answer: a resident tab must surface rows another writer persisted without a tab reopen", last.Message.Content)
	}
}

// 防误伤钉：ledger 与内存基线一致（前台活会话的常态）时 live 快路径必须
// 原样保持——本修复只把「可证明落后」的读降级到冷读，活会话零回退。
func TestHistorySliceLiveFreshKeepsFastPath(t *testing.T) {
	app := historySliceTestApp(t)
	dir := t.TempDir()
	var msgs []provider.Message
	for i := range 4 {
		msgs = append(msgs, historySliceUser(i, fmt.Sprintf("q%d", i)), historySliceAssistant(i, fmt.Sprintf("a%d", i)))
	}
	sess, path := saveHistorySliceSession(t, dir, "fresh-live.jsonl", msgs)
	newLiveHistoryTab(t, app, dir, path, sess)

	if page := app.HistorySliceForTab("test", HistorySliceRequest{}); page.Source != "live-index" {
		t.Fatalf("Source = %q, want live-index: the live fast path must not degrade when the ledger matches the in-memory baseline", page.Source)
	}
}

// 保存前内存基线缺失（controller 从未在此进程保存、ledger 已有指纹）时，
// 内存视图无法证明覆盖了已落盘内容——必须冷读。
func TestHistorySliceLiveNoBaselineFallsCold(t *testing.T) {
	app := historySliceTestApp(t)
	dir := t.TempDir()
	var msgs []provider.Message
	for i := range 4 {
		msgs = append(msgs, historySliceUser(i, fmt.Sprintf("q%d", i)), historySliceAssistant(i, fmt.Sprintf("a%d", i)))
	}
	_, path := saveHistorySliceSession(t, dir, "no-baseline.jsonl", msgs)

	// 挂一个从未保存过的 live controller：内存含同样的 4 轮（可见内容一致），
	// 但 persisted baseline 缺失（SaveSnapshot 语义不落 CAS revision）。
	empty := agent.NewSession("")
	empty.Add(historySliceUser(0, "stray-in-memory"))
	newLiveHistoryTab(t, app, dir, path, empty)

	page := app.HistorySliceForTab("test", HistorySliceRequest{Turns: 12})
	if page.Stale {
		t.Fatal("unexpected stale page")
	}
	if len(page.Entries) == 0 {
		t.Fatal("empty page")
	}
	last := page.Entries[len(page.Entries)-1]
	if last.Message.Content != "a3" {
		t.Fatalf("latest entry = %q, want a3 from disk: a ledger-fingerprinted session with no in-memory baseline must cold-read, not serve a stray memory view", last.Message.Content)
	}
}
