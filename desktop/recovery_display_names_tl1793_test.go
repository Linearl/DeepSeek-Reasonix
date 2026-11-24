package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1793 验收1：版本管理面板的行名（会话行 mainLabel 与副本行 label）必须
// 与侧栏一致地显示中文会话标题；内部名（session 文件 stem）只在任何标题来
// 源都缺时兜底。20261124 真机现场：四个对话名显示系统内部字符串而回收站
// （走 custom_title 优先的 listing 链）渲染出正确中文名——根因是
// recoveryDisplayName 只读 meta 的 topic_title，漏了 custom_title 与
// session-titles sidecar 两级既有标题源。

// writeMetaTitle writes a branch meta sidecar carrying exactly the title
// fields given (empty string omits the key, mirroring omitempty writers).
func writeMetaTitle(t *testing.T, transcriptPath, customTitle, topicTitle string) {
	t.Helper()
	b := `{"id":"` + strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl") + `"`
	if customTitle != "" {
		b += `,"custom_title":"` + customTitle + `"`
	}
	if topicTitle != "" {
		b += `,"topic_title":"` + topicTitle + `"`
	}
	b += "}"
	if err := os.WriteFile(store.SessionMeta(transcriptPath), []byte(b), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

// 场景①主回归：用户改过名（custom_title）而自动标题为空——真机四行显示
// 内部字符串的形态。custom_title 必须胜出（与侧栏同序）。
func TestRecoveryDisplayPrefersCustomTitleLikeSidebar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20261124-080000.000000000-collab.jsonl")
	writeMetaTitle(t, path, "qwen部署", "")

	if got := recoveryDisplayName(path); got != "qwen部署" {
		t.Fatalf("custom_title rename must win like the sidebar: got %q", got)
	}
}

// 场景②优先级：custom_title 与 topic_title 并存时 custom_title 优先——
// 与回收站 listing 链（title=CustomTitle 优先）完全同序，不造第二套口径。
func TestRecoveryDisplayCustomTitleOutranksTopicTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20261124-080001.000000000-collab.jsonl")
	writeMetaTitle(t, path, "赤途对齐修复-第一梯队", "旧自动标题")

	if got := recoveryDisplayName(path); got != "赤途对齐修复-第一梯队" {
		t.Fatalf("custom_title must outrank topic_title: got %q", got)
	}
}

// 场景③原行为保留：只有自动标题的会话继续显示 topic_title（零回退）。
func TestRecoveryDisplayKeepsTopicTitleFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20261124-080002.000000000-collab.jsonl")
	writeMetaTitle(t, path, "", "Heartbeat:玫瑰窃贼完整版")

	if got := recoveryDisplayName(path); got != "Heartbeat:玫瑰窃贼完整版" {
		t.Fatalf("topic_title fallback must keep working: got %q", got)
	}
}

// 场景④titles sidecar：meta 缺失/无标题而目录 overrides 里有名字——第三级
// 既有标题源（侧栏 listing 的同一回退），此前整级缺失。
func TestRecoveryDisplayFallsToSessionTitlesSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20261124-080003.000000000-collab.jsonl")
	if err := os.WriteFile(sessionTitlesPath(dir), []byte(`{"`+filepath.Base(path)+`":"人类专用调研员1"}`), 0o644); err != nil {
		t.Fatalf("write titles sidecar: %v", err)
	}

	if got := recoveryDisplayName(path); got != "人类专用调研员1" {
		t.Fatalf("session-titles override must be consulted: got %q", got)
	}
}

// 场景⑤兜底不变：全部标题源都缺时回落文件 stem（原回退，副本管理等依赖
// 该身份的读数不因本件漂移）。
func TestRecoveryDisplayStemFallbackUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20261124-080004.000000000-collab.jsonl")

	if got := recoveryDisplayName(path); got != "20261124-080004.000000000-collab" {
		t.Fatalf("stem fallback must stay the last resort: got %q", got)
	}
}

// 场景⑥副本行集成：副本的 meta 继承主会话标题（真机桶内实证），副本行
// label 走同一条链显示中文而不是 recovery-xxx stem；组扫描路径的单目录
// titles 读数（recoveryDisplayNameFor 显式传 map）同样生效。
func TestRecoveryCopiesByMainLabelsCarryTitleChain(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "20261120-010000.000000000-plan.jsonl")
	if err := os.WriteFile(mainPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	writeMetaTitle(t, mainPath, "人类专用调研员1", "")
	copyName := "20261120-010000.000000000-plan-recovery-aabbccdd11223344.jsonl"
	copyPath := filepath.Join(dir, copyName)
	if err := os.WriteFile(copyPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	writeMetaTitle(t, copyPath, "人类专用调研员1", "")

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
		t.Fatalf("copy label must walk the same title chain: got %q", got)
	}

	// 显式 titles map 的路径（组扫描的每目录一次读）同样按链解析。
	titles := map[string]string{filepath.Base(copyName): "人类专用调研员1-titles"}
	if got := recoveryDisplayNameFor(copyPath, titles); got != "人类专用调研员1" {
		t.Fatalf("sidecar title must still outrank the titles map: got %q", got)
	}
}
