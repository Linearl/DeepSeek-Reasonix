package main

// tl1415h 刀①+刀②钉：打开漏斗裁决日志（open_session 裁决行）与冷机窗口的
// 按需有界 reconcile。
//
// 取证报告（docs/issues/reports/问题诊断报告归档/tl1415四轮取证-20261108.md）
// 定案：1415f 已进包且静态组界定逐门全过，真漏洞=冷机时序窗口——topic 注册
// 表让树行先于目录 reconcile 可见可点，用户点击路径没有启动恢复路径的「排
// 在 reconcile 之后」语义，窗口内首开组未建、权威#1/#2/#3 全空、两条启发式
// 双死，停在主线（用户 B8 实测「首开错切 2 次正常」与窗口分支吻合）。同时
// 「打开漏斗正常路径零日志」是首开错切当场实例无法百分百归因的唯一缺口。
//
// 钉面：
//  ① 暖机裁决行：权威#3（meta 旗标）命中时，日志带 authority/authorityKind/
//     groupId/groupMembers/groupReady，且 redirected=true（打开落副本）；
//  ② 冷机窗口行：路径未入册时 windowReady=false + filed=false（窗口观测标记；
//     未知目录不触发窗口 reconcile——known-dirs 保守边界）；
//  ③ 窗口内首开落副本（刀②核心，修前红/修后绿）：catalog 冷机（组未建）+
//     不等待直接点开 → 有界 reconcile 建组 → 权威#3 命中 → 首开落副本；
//  ④ 超时兜底：reconcile 预算耗尽时按既有降级路径走（保持原路径）+ Warn 落
//     日志，不挂起不扩面；
//  ⑤ 日志量控制：resume/retarget 复用的 continuePathForOpen 零日志——裁决行
//     只在打开动作触发，非热路径。
//
// 行为钉同步：打开的返回值语义在暖机与未知目录形态下不变（1415e/f 五钉是同
// 一承诺的暖机面）。

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sessioncatalog"
)

// saveVerdictFixture 在真实全局会话目录落一份 schema-1 会话并盖上 topic/scope
// 元数据（投影的 LogicalTopicID/Scope 首源是 meta）。dir 必须是已知会话目录
// （config.SessionDir()）——刀②的窗口 reconcile 只碰 known targets。
func saveVerdictFixture(t *testing.T, path string, turns int, topicID string) {
	t.Helper()
	activateTestSave(t, path, func() []string {
		msgs := make([]string, 0, turns*2)
		for i := 0; i < turns; i++ {
			msgs = append(msgs, "裁决钉 问题"+string(rune('一'+i)), "裁决钉 回答"+string(rune('一'+i)))
		}
		return msgs
	}()...)
	created := time.UnixMilli(100)
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		ID: agent.BranchID(path), Scope: "global", TopicID: topicID, TopicTitle: "tl1415h 裁决钉",
		CreatedAt: created, UpdatedAt: created,
	}); err != nil {
		t.Fatalf("stamp meta %s: %v", path, err)
	}
}

// stampAdoptedFlag 给副本盖用户采纳旗标（不带 digest——filing 层验证不过、
// 权威#2 空，权威#3 直读旗标命中：取证现场「digest 漂移→权威#2 空」的同落
// 点形态）。
func stampAdoptedFlag(t *testing.T, copyPath, mainPath string) {
	t.Helper()
	if err := agent.SaveBranchMetaPreserveUpdated(copyPath, func() agent.BranchMeta {
		meta := agent.BranchMeta{
			ID: agent.BranchID(copyPath), Scope: "global", TopicID: "topic_verdict_1415h",
			TopicTitle: "tl1415h 裁决钉", Recovered: true, ParentID: agent.BranchID(mainPath),
			RecoveryDepth: 1, RecoveryPreferred: true, RecoveryReason: "rewrite conflict",
			CreatedAt: time.UnixMilli(200), UpdatedAt: time.UnixMilli(200),
		}
		return meta
	}()); err != nil {
		t.Fatalf("stamp preferred flag: %v", err)
	}
}

func openVerdictApp(t *testing.T, catalog *sessioncatalog.Catalog) *App {
	t.Helper()
	return openAuthorityApp(t, catalog)
}

// openVerdictColdCatalog 开一个内存 catalog 但不对任何目录 reconcile——冷机
// 窗口形态（组未建、路径未入册）。
func openVerdictColdCatalog(t *testing.T) *sessioncatalog.Catalog {
	t.Helper()
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatalf("open cold catalog: %v", err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	return catalog
}

func captureVerdictLogs(t *testing.T, run func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	run()
	return buf.String()
}

// ① 暖机裁决行：meta 旗标命中权威#3，打开重定向到副本（与面板 Selected 同
// 一权威），日志逐字段可读。
func TestOpenSessionAuthorityVerdictRedirectsAndLogs(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "verdict-1415h-main.jsonl")
	copyPath := filepath.Join(dir, "verdict-1415h-main-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		for _, p := range []string{mainPath, copyPath, mainPath + ".meta", copyPath + ".meta"} {
			_ = os.Remove(p)
		}
	})
	// 取证现场形态：主线轮次更多（副本轮次回归、线性启发式死），用户显式采
	// 纳副本（旗标）、filing 因无 digest 验证不过（权威#2 空）——权威#3 必须命中。
	saveVerdictFixture(t, mainPath, 3, "topic_verdict_1415h")
	saveVerdictFixture(t, copyPath, 1, "topic_verdict_1415h")
	stampAdoptedFlag(t, copyPath, mainPath)

	app := openVerdictApp(t, openVerdictWarmCatalog(t, dir))

	logs := captureVerdictLogs(t, func() {
		_, got := app.resolveOpenTopicSessionPath("global", "", mainPath)
		if !sameRecoveryLineagePath(got, copyPath) {
			t.Fatalf("warm open must redirect to the adopted copy (authority #3), got %s", got)
		}
	})
	for _, want := range []string{
		"desktop: open session authority verdict",
		"sessionPath=" + mainPath,
		"topicId=topic_verdict_1415h",
		"filed=true",
		"groupReady=true",
		"groupMembers=2",
		"authorityKind=meta_flag",
		"redirected=true",
		"windowReady=true",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("verdict log must name %q; log:\n%s", want, logs)
		}
	}
}

// openVerdictWarmCatalog 开内存 catalog 并把目录 reconcile 进投影（暖机形态：
// 目录组分类已跑——取证报告状态 A）。
func openVerdictWarmCatalog(t *testing.T, dir string) *sessioncatalog.Catalog {
	t.Helper()
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatalf("reconcile fixture directory: %v", err)
	}
	return catalog
}

// ② 冷机窗口行：路径未入册（目录从未 reconcile）时 windowReady=false +
// filed=false，打开保持原路径；目录不在已知会话目录集时不触发窗口 reconcile
// （known-dirs 保守边界——与 sessionDirForPath 同一门）。
func TestOpenSessionAuthorityVerdictMarksColdWindow(t *testing.T) {
	mainPath := filepath.Join(t.TempDir(), "verdict-1415h-cold.jsonl")
	saveVerdictFixture(t, mainPath, 1, "topic_verdict_1415h_cold")

	app := openVerdictApp(t, openVerdictColdCatalog(t))

	logs := captureVerdictLogs(t, func() {
		_, got := app.resolveOpenTopicSessionPath("global", "", mainPath)
		if got != mainPath {
			t.Fatalf("cold-window open keeps the requested path, got %s", got)
		}
	})
	for _, want := range []string{
		"desktop: open session authority verdict",
		"filed=false",
		"windowReady=false",
		"redirected=false",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("cold-window verdict log must name %q; log:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "windowReconciled=true") {
		t.Fatalf("a directory outside the known session dirs must not be reconciled on open; log:\n%s", logs)
	}
}

// ③ 刀②核心行为钉（修前红/修后绿）：catalog 冷机（该目录从未 reconcile、
// 组未建），用户不等待直接点击树行首开。修前：权威全空+启发式双死，停在主
// 线（错误副本）；修后：点击路径先对该目录做一次有界 reconcile（与启动恢复
// 路径 retargetOpenTabsToContinuations 排在 reconcile 之后的排序语义对齐），
// 组建成、权威#3 命中，首开落副本。
func TestOpenSessionWindowReconcileLandsAdoptedCopyOnFirstOpen(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "verdict-1415h-window-main.jsonl")
	copyPath := filepath.Join(dir, "verdict-1415h-window-main-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		for _, p := range []string{mainPath, copyPath, mainPath + ".meta", copyPath + ".meta"} {
			_ = os.Remove(p)
		}
	})
	// 与取证现场同形：主线轮次更多（副本轮次回归），用户显式采纳副本（旗
	// 标），filing 验证不过——唯一权威=meta 旗标（权威#3）。
	saveVerdictFixture(t, mainPath, 3, "topic_verdict_1415h_window")
	saveVerdictFixture(t, copyPath, 1, "topic_verdict_1415h_window")
	stampAdoptedFlag(t, copyPath, mainPath)

	app := openVerdictApp(t, openVerdictColdCatalog(t))

	logs := captureVerdictLogs(t, func() {
		_, got := app.resolveOpenTopicSessionPath("global", "", mainPath)
		if !sameRecoveryLineagePath(got, copyPath) {
			t.Fatalf("first open inside the cold window must land on the adopted copy after the bounded window reconcile, got %s", got)
		}
	})
	for _, want := range []string{
		"desktop: open session authority verdict",
		"windowReady=true",
		"windowReconciled=true",
		"redirected=true",
		"authorityKind=meta_flag",
		"groupMembers=2",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("window-reconciled open verdict must name %q; log:\n%s", want, logs)
		}
	}
}

// ④ 超时兜底：reconcile 预算耗尽时，点击打开按既有降级路径走（保持原路径），
// Warn 落日志（兜底可见），不挂起不扩面。
func TestOpenSessionWindowReconcileTimeoutFallsBack(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "verdict-1415h-timeout-main.jsonl")
	copyPath := filepath.Join(dir, "verdict-1415h-timeout-main-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		for _, p := range []string{mainPath, copyPath, mainPath + ".meta", copyPath + ".meta"} {
			_ = os.Remove(p)
		}
	})
	saveVerdictFixture(t, mainPath, 2, "topic_verdict_1415h_timeout")
	saveVerdictFixture(t, copyPath, 1, "topic_verdict_1415h_timeout")
	stampAdoptedFlag(t, copyPath, mainPath)

	prev := openSessionWindowReconcileBudget
	openSessionWindowReconcileBudget = time.Nanosecond
	t.Cleanup(func() { openSessionWindowReconcileBudget = prev })

	app := openVerdictApp(t, openVerdictColdCatalog(t))

	logs := captureVerdictLogs(t, func() {
		_, got := app.resolveOpenTopicSessionPath("global", "", mainPath)
		// 超时兜底=既有降级路径：保持原路径（旧行为），绝不因收口失败而挂起。
		if got != mainPath {
			t.Fatalf("a timed-out window reconcile must fall back to the pre-existing path, got %s", got)
		}
	})
	if !strings.Contains(logs, "desktop: open session window reconcile") {
		t.Fatalf("a timed-out reconcile must leave a Warn trace; log:\n%s", logs)
	}
	if !strings.Contains(logs, "windowReconciled=true") {
		t.Fatalf("the attempted (timed-out) reconcile must be visible in the verdict; log:\n%s", logs)
	}
}

// ⑤ 日志量控制：resume/retarget 复用的 continuePathForOpen 零日志——裁决行
// 只在打开动作（resolveOpenTopicSessionPath）触发，非热路径。
func TestOpenSessionAuthorityVerdictOnlyOnOpen(t *testing.T) {
	app := openVerdictApp(t, openVerdictColdCatalog(t))
	logs := captureVerdictLogs(t, func() {
		if continued := app.continuePathForOpen(filepath.Join(t.TempDir(), "whatever.jsonl")); continued != "" {
			t.Fatalf("no filing means no continuation, got %q", continued)
		}
	})
	if strings.Contains(logs, "open session authority verdict") {
		t.Fatalf("continuePathForOpen (resume/retarget path) must stay silent; log:\n%s", logs)
	}
}
