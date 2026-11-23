package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// tl1787 件3/B：改写前备份历史合并回主线的执行核单测。
//
// 场景钉（用户原话 ABCD→折叠→ED→EDFG）：主线是折叠后的 EDFG，备份是折叠前
// 全文 ABCD——合并把备份里主线不认识的轮次接回主线头部，历史恢复可查；
// 全程零 LLM 调用（纯本地 graft）。

// seedFoldedMain 写一个「折叠后」主线：折叠摘要轮 E + 保留轮 D + 新对话 F/G。
func seedFoldedMain(t *testing.T, dir, name string) string {
	t.Helper()
	mainPath := filepath.Join(dir, name+".jsonl")
	s := NewSession("sys")
	s.Add(userTurn("E folded summary"))
	s.Add(assistantTurn("D kept"))
	s.Add(userTurn("F new question"))
	s.Add(assistantTurn("G new answer"))
	if err := s.Save(mainPath); err != nil {
		t.Fatalf("Save main: %v", err)
	}
	return mainPath
}

// seedBackupTranscript 写一份「改写前备份」转录（备份内 <stem>.jsonl 的形态，
// 落在 <stem>.pre-repair-<stamp>/ 子目录——与真实备份树同构，也避免与宿主
// 同路径互踩）：折叠前的完整历史 ABCD。
func seedBackupTranscript(t *testing.T, dir, name string) string {
	t.Helper()
	backupDir := filepath.Join(dir, name+".pre-repair-20261110T010000")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(backupDir, name+".jsonl")
	b := NewSession("sys")
	b.Add(userTurn("A original"))
	b.Add(assistantTurn("B original"))
	b.Add(userTurn("C original"))
	b.Add(assistantTurn("D kept"))
	if err := b.Save(backupPath); err != nil {
		t.Fatalf("Save backup: %v", err)
	}
	return backupPath
}

func TestGraftSessionExternalPrefixRecoversFoldedHistory(t *testing.T) {
	dir := t.TempDir()
	mainPath := seedFoldedMain(t, dir, "topic")
	backupPath := seedBackupTranscript(t, dir, "topic")

	report, err := GraftSessionExternalPrefix(mainPath, backupPath)
	if err != nil {
		t.Fatalf("graft: %v", err)
	}
	if report.Grafted == 0 {
		t.Fatalf("graft = %+v, want the backup's unique prefix grafted (A/B 独有轮次应接回)", report)
	}
	// 主线内容=备份独有前缀 + 折叠态主线：旧史在前、新史在后，可查完整历史
	// （宿主 5 条=sys 前导+4，加接回的 Grafted 条；合并后
	// [sys, A, B, E, D, F, G]）。
	s := loadSnapshot(t, mainPath)
	if len(s) != 5+report.Grafted {
		t.Fatalf("main after graft = %d messages, want %d", len(s), 5+report.Grafted)
	}
	first := UserMessageText(s[1])
	if first != "A original" {
		t.Fatalf("first user message after graft = %q, want the backup's opener (旧史在前)", first)
	}
	if got := UserMessageText(s[len(s)-2]); got != "F new question" {
		t.Fatalf("newest user turn after graft = %q, want the main's newest question (新史在后)", got)
	}
	// listing 元数据同步成真（「合并后历史可查」的清单面）。
	meta, ok, err := LoadBranchMeta(mainPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta after graft: ok=%v err=%v", ok, err)
	}
	if meta.Preview != "A original" {
		t.Fatalf("meta preview = %q, want the grafted opener", meta.Preview)
	}
}

// 幂等：主线已包含备份全部轮次时 merged=false（grafted=0），零写入。
func TestGraftSessionExternalPrefixIsIdempotentWhenCovered(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "full.jsonl")
	s := NewSession("sys")
	s.Add(userTurn("A original"))
	s.Add(assistantTurn("B original"))
	s.Add(userTurn("C original"))
	s.Add(assistantTurn("D kept"))
	if err := s.Save(mainPath); err != nil {
		t.Fatal(err)
	}
	backupPath := seedBackupTranscript(t, dir, "full")

	report, err := GraftSessionExternalPrefix(mainPath, backupPath)
	if err != nil {
		t.Fatalf("graft: %v", err)
	}
	if report.Grafted != 0 {
		t.Fatalf("graft = %+v, want zero (the main already covers the backup)", report)
	}
	if got := sessionMessageCount(t, mainPath); got != 5 {
		t.Fatalf("main message count = %d, want 5 (idempotent, no writes; sys 前导+4)", got)
	}
}

// 无关链拒绝：备份与主线无任何交点时拒绝（把整份无关对话接进主线是破坏性
// 的——promote 的 HasForkPoint 判据同语义）。
func TestGraftSessionExternalPrefixRefusesUnrelatedChain(t *testing.T) {
	dir := t.TempDir()
	mainPath := seedFoldedMain(t, dir, "solo")
	backupPath := filepath.Join(dir, "unrelated.jsonl")
	b := NewSession("other-identity")
	b.Add(userTurn("totally different A"))
	b.Add(assistantTurn("totally different B"))
	if err := b.Save(backupPath); err != nil {
		t.Fatal(err)
	}

	if _, err := GraftSessionExternalPrefix(mainPath, backupPath); err == nil {
		t.Fatal("merging an unrelated chain must be refused")
	}
	if got := sessionMessageCount(t, mainPath); got != 5 {
		t.Fatalf("main message count = %d, want 5 (refusal leaves the main untouched; sys 前导+4)", got)
	}
}

// 快照装载失败=拒绝（fail-closed 继承）：备份转录读不出（文件缺失形态——
// 备份被清空/损坏超出恢复）时不产生合并决策。损坏主线的场景不在此钉：转录
// 破损而事件日志完好时 LoadSession 侧自带自愈，快照仍可装载=合并安全。
func TestGraftSessionExternalPrefixRefusesWhenSnapshotUnavailable(t *testing.T) {
	dir := t.TempDir()
	mainPath := seedFoldedMain(t, dir, "sick")
	backupPath := filepath.Join(dir, "sick.pre-repair-20261110T010000", "sick.jsonl")
	if _, err := GraftSessionExternalPrefix(mainPath, backupPath); err == nil {
		t.Fatal("graft must refuse when the backup transcript cannot be loaded")
	}
}
