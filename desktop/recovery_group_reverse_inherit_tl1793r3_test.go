package main

import (
	"os"
	"path/filepath"
	"testing"
)

// tl1793r3 组行反向继承钉：正本缺失的组（20261125 现场 qwen27b 072821 存量
// 孤立组——组 key 指向的正本 jsonl 已不在盘上），组头 MainLabel 的四级链各站
// 全空（sidecar/titles 都按正本文件名取键，文件没了就读空）→ 兜底成裸 stem，
// 而组内副本行各自解析出真名（副本 meta 有 topic_title）。本件把组头显示名
// 从任一副本的既有解析链反向继承——只限正本缺失，在场正本自己的名字永远
// 胜出；副本 stem 永不上组头。r2 读面六钉（副本行继承）与本组级五钉互补。

const tl1793r3OrphanStem = "20261104-072821.000000000-qwen27b"

func tl1793r3OrphanMainPath(dir string) string {
	return filepath.Join(dir, tl1793r3OrphanStem+".jsonl")
}

func tl1793r3WriteCopy(t *testing.T, dir, recoveryHex string) string {
	t.Helper()
	copyPath := filepath.Join(dir, tl1793r3OrphanStem+"-recovery-"+recoveryHex+".jsonl")
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	return copyPath
}

// 组钉①列表路径（用户现场形态）：正本缺失、副本 meta 带 topic_title——组头
// MainLabel 显示「qwen部署」而不是裸 stem（20261125 装机实拍组头形态）。
func TestRecoveryGroupListInheritsLabelFromCopyWhenMainMissing(t *testing.T) {
	dir := t.TempDir()
	copyPath := tl1793r3WriteCopy(t, dir, "aabbccdd11223344")
	writeMetaTitle(t, copyPath, "", "qwen部署")

	groups, err := recoveryCopyGroupsIn(dir)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	group := groups[0]
	if group.MainExists {
		t.Fatalf("fixture shape: main must be missing (orphan group)")
	}
	if group.MainLabel != "qwen部署" {
		t.Fatalf("mainless group header must inherit the copy's resolved title: got %q", group.MainLabel)
	}
	if group.MainLabel == tl1793r3OrphanStem {
		t.Fatalf("mainless group header must not bottom out at the raw stem")
	}
}

// 组钉②扫描路径：ScanRecoveryCopyGroup 的组视图会整体替换面板里的组——反向
// 继承必须同样成立，否则点「扫描」的瞬间组头翻回裸 stem（刷新无效二轮返修
// 的正场）。
func TestRecoveryGroupScanInheritsLabelFromCopyWhenMainMissing(t *testing.T) {
	dir := t.TempDir()
	copyPath := tl1793r3WriteCopy(t, dir, "aabbccdd11223344")
	writeMetaTitle(t, copyPath, "", "qwen部署")

	group, ok := recoveryCopyGroupFor(dir, tl1793r3OrphanMainPath(dir))
	if !ok {
		t.Fatalf("scan must still describe the orphan group")
	}
	if group.MainExists {
		t.Fatalf("fixture shape: main must be missing (orphan group)")
	}
	if group.MainLabel != "qwen部署" {
		t.Fatalf("scanned orphan group header must keep the inherited title: got %q", group.MainLabel)
	}
	if len(group.Copies) != 1 || !group.Copies[0].Orphan {
		t.Fatalf("fixture shape: the copy must stay orphan-marked in the scan view")
	}
}

// 组钉③在场正本不越位：正本在场且有自己的标题时组头仍用正本自己的名字——
// 反向继承只补正本缺失的空位，从不覆盖正本的既有显示名。
func TestRecoveryGroupKeepsOwnTitleWhenMainExists(t *testing.T) {
	dir := t.TempDir()
	mainPath := tl1793r3OrphanMainPath(dir)
	if err := os.WriteFile(mainPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	writeMetaTitle(t, mainPath, "", "正本自己的标题")
	copyPath := tl1793r3WriteCopy(t, dir, "aabbccdd11223344")
	writeMetaTitle(t, copyPath, "", "qwen部署")

	groups, err := recoveryCopyGroupsIn(dir)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if !groups[0].MainExists {
		t.Fatalf("fixture shape: main must exist")
	}
	if groups[0].MainLabel != "正本自己的标题" {
		t.Fatalf("titled main must keep its own name (no reverse inheritance overreach): got %q", groups[0].MainLabel)
	}
}

// 组钉④全空回落不变：正本缺失且副本也无可解析标题——组头仍回落正本 stem
// （原兜底零回退；此时没有任何真名可借）。
func TestRecoveryGroupStemFallbackUnchangedWhenNothingResolvable(t *testing.T) {
	dir := t.TempDir()
	tl1793r3WriteCopy(t, dir, "aabbccdd11223344")

	groups, err := recoveryCopyGroupsIn(dir)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if got := groups[0].MainLabel; got != tl1793r3OrphanStem {
		t.Fatalf("stem fallback must stay the group's last resort: got %q want %q", got, tl1793r3OrphanStem)
	}
}

// 组钉⑤任一副本语义：首个副本无可解析标题、第二个副本有——继承走完整个
// 副本序列取第一个真名，不因首个副本落空而放弃（目录序确定性：ReadDir 按
// 名排，0001 在 0002 前）。
func TestRecoveryGroupInheritsFromFirstResolvableCopy(t *testing.T) {
	dir := t.TempDir()
	tl1793r3WriteCopy(t, dir, "0000000000000001") // 无 meta——不可解析
	copy2 := tl1793r3WriteCopy(t, dir, "0000000000000002")
	writeMetaTitle(t, copy2, "", "qwen部署")

	groups, err := recoveryCopyGroupsIn(dir)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if got := groups[0].MainLabel; got != "qwen部署" {
		t.Fatalf("the walk must pass over an unresolvable copy and take the next real title: got %q", got)
	}
}

// 组钉⑥副本 stem 永不上组头：组级借用只走「无兜底链」——副本自身链耗尽时
// 借用返回空（而不是副本 stem），组头才得以保持组钉④的正本 stem 兜底；两级
// 兜底语义分开，组头永远不会出现 recovery-xxx 串。
func TestRecoveryGroupHelperNeverReturnsCopyStem(t *testing.T) {
	dir := t.TempDir()
	copyPath := tl1793r3WriteCopy(t, dir, "aabbccdd11223344")

	got := recoveryGroupLabelFromCopies([]RecoveryCopyView{{Path: copyPath}}, nil)
	if got != "" {
		t.Fatalf("group helper must not lend the copy's stem: got %q", got)
	}
}
