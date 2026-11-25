package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

// tl1793r2 读面钉：一期回退链（custom_title→topic_title→titles sidecar→stem）
// 之下、stem 兜底之上补两级继承源——①副本 meta.topic_id 查 topic registry
// （侧栏/回收站族渲染同源），②stem 前缀匹配原对话递归同链。20261125 现场：
// 回收站恢复的副本在版本管理显示文件字符串而回收站族渲染出中文名——副本
// 创建早于标题继承，一期回退链各源全空。写面（tl1793r2 agent 件）管新副本，
// 读面救存量副本，两者覆盖面不相交。

const tl1793r2CopySuffix = "-recovery-aabbccdd11223344.jsonl"

// 读面①父链：原对话在场（meta 带中文 topic_title）而副本全空——副本行沿
// stem 前缀找到原对话并继承其显示名（回收站族「副本并入主线行」的读面同源）。
func TestRecoveryCopyInheritsTitleFromLiveParent(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "20261120-010000.000000000-plan.jsonl")
	if err := os.WriteFile(mainPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	writeMetaTitle(t, mainPath, "", "人类专用调研员1")
	copyPath := filepath.Join(dir, "20261120-010000.000000000-plan"+tl1793r2CopySuffix)
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}

	if got := recoveryDisplayName(copyPath); got != "人类专用调研员1" {
		t.Fatalf("titleless copy must inherit the live parent's title: got %q", got)
	}
}

// 读面②registry：原对话不在场（仍在回收站——用户现场形态），副本 meta 带
// 原对话 topic_id，registry 里有中文名——副本行显示 registry 标题而不是 stem。
func TestRecoveryCopyInheritsTitleFromTopicRegistry(t *testing.T) {
	dir := config.SessionDir()
	if dir == "" {
		t.Skip("session dir unavailable")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("ensure session dir: %v", err)
	}
	topicID := "topic_20261104-072818"
	if err := setTopicTitle("", topicID, "人类专用调研员1"); err != nil {
		t.Fatalf("register topic title: %v", err)
	}
	copyPath := filepath.Join(dir, "20261104-072818.000000000-plan"+tl1793r2CopySuffix)
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	b := `{"id":"20261104-072818.000000000-plan-recovery-aabbccdd11223344","topic_id":"` + topicID + `"}`
	if err := os.WriteFile(store.SessionMeta(copyPath), []byte(b), 0o644); err != nil {
		t.Fatalf("write copy meta: %v", err)
	}
	if got := recoveryDisplayName(copyPath); got != "人类专用调研员1" {
		t.Fatalf("titleless copy with the parent's topic id must render the registry title: got %q", got)
	}
}

// 读面③stem 前缀扫描：原对话 stem 超长（副本铸造时截断+散列，剥离结果不再
// 等于原 stem），精确文件名落空后按前缀在目录内找回原对话并继承标题。
func TestRecoveryCopyInheritsTitleViaStemPrefixScan(t *testing.T) {
	dir := t.TempDir()
	longStem := "20261120-010000.000000000-" + strings.Repeat("a", 70) + "tail"
	mainPath := filepath.Join(dir, longStem+".jsonl")
	if err := os.WriteFile(mainPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	writeMetaTitle(t, mainPath, "", "Heartbeat:玫瑰窃贼完整版")

	// 复刻铸造形态：>80 字节 stem → 截 80 字节 + "-" + 12 hex，再接 recovery 尾号。
	// 剥离结果带散列段，与原 stem 不是直接前缀——读面需先剥散列再前缀匹配。
	truncated := strings.Trim(longStem[:80], "-_. ")
	copyStem := truncated + "-001122334455" + tl1793r2CopySuffix
	copyPath := filepath.Join(dir, copyStem)
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	parent, ok := agent.RecoveryFilenameParentID(copyPath)
	if !ok || !strings.HasPrefix(parent, truncated) {
		t.Fatalf("fixture shape: parent=%q ok=%v truncated=%q", parent, ok, truncated)
	}

	if got := recoveryDisplayName(copyPath); got != "Heartbeat:玫瑰窃贼完整版" {
		t.Fatalf("truncated-stem copy must find the original by prefix: got %q", got)
	}
}

// 读面④兜底不变：副本全空且无可解析对象（目录内只有它自己）——仍回落
// 副本 stem（原兜底零回退；一期场景⑤的非副本钉继续由一期套件把守）。
func TestRecoveryCopyStemFallbackUnchangedWhenNothingResolvable(t *testing.T) {
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "20261124-080004.000000000-collab"+tl1793r2CopySuffix)
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}

	want := strings.TrimSuffix(filepath.Base(copyPath), ".jsonl")
	if got := recoveryDisplayName(copyPath); got != want {
		t.Fatalf("stem fallback must stay the last resort for unresolvable copies: got %q want %q", got, want)
	}
}

// 读面⑤组扫描集成：recoveryCopiesByMain 的副本行 label 同样继承（真机面板
// 实际走的就是这条每目录一次 titles 读数的扫描路径）。
func TestRecoveryCopiesByMainLabelsInheritFromParent(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "20261120-010000.000000000-plan.jsonl")
	if err := os.WriteFile(mainPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	writeMetaTitle(t, mainPath, "", "人类专用调研员1")
	copyPath := filepath.Join(dir, "20261120-010000.000000000-plan"+tl1793r2CopySuffix)
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}

	byMain, order, err := recoveryCopiesByMain(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(order) != 1 || order[0] != mainPath {
		t.Fatalf("group order = %v, want [%s]", order, mainPath)
	}
	copies := byMain[mainPath]
	if len(copies) != 1 {
		t.Fatalf("copies = %d, want 1", len(copies))
	}
	if got := copies[0].Label; got != "人类专用调研员1" {
		t.Fatalf("scan label must inherit the parent title: got %q", got)
	}
}

// 读面⑥优先级不越位：副本自带 topic_title 时一级 sidecar 仍旧胜出——新增
// 两级继承源只排在既有链之后，一期优先级零漂移。
func TestRecoveryCopyOwnSidecarStillOutranksInheritedSources(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "20261120-010000.000000000-plan.jsonl")
	if err := os.WriteFile(mainPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	writeMetaTitle(t, mainPath, "", "父对话自动标题")
	copyPath := filepath.Join(dir, "20261120-010000.000000000-plan"+tl1793r2CopySuffix)
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	writeMetaTitle(t, copyPath, "", "副本自己的标题")

	if got := recoveryDisplayName(copyPath); got != "副本自己的标题" {
		t.Fatalf("own sidecar title must outrank inherited sources: got %q", got)
	}
}
