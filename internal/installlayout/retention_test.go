package installlayout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 任务835：更新器侧 versions 自动清理。规则（用户 2026-10-12 定）：最多保留
// 3 个历史版本+当前运行版（共 4 个版本目录）+staging；超额旧版与
// *.replaced-* 残壳在更新成功后自动清除；quarantine 同规则超期自动清。
//
// 验收对照：
// ①连续两次更新后 versions ≤4 —— TestEnforceRetentionKeepsThreeHistoriesPlusActive
// ②replaced 残壳不留存 —— TestEnforceRetentionRemovesStaleReplacedShells
// ③current.json/staging 不被误清 —— 同上两测试 + 新鲜 staging 保留断言
// ④清理在成功确认后 —— 调用点合同（desktop 启动链），installlayout 层以
//   RetentionReport 记录删除面供调用方日志与测试核对。

// TestEnforceRetentionKeepsThreeHistoriesPlusActive 钉数量上限：7 棵树
// （两次更新叠加的累积形态）收敛到 active + newest 3。
func TestEnforceRetentionKeepsThreeHistoriesPlusActive(t *testing.T) {
	versions := []string{
		"v1.38.3-20261010-0900",
		"v1.38.3-20261011-1000",
		"v1.38.3-20261012-1100",
		"v1.38.3-20261012-1748",
		"v1.38.3-20261013-0014",
		"v1.38.3-20261013-0911",
		"v1.38.3-20261014-1200",
	}
	root := writePruneFixture(t, versions, "v1.38.3-20261013-0911")

	report := EnforceVersionRetention(root)

	got := remainingVersions(t, root)
	if len(got) > RetentionKeepHistory+1 {
		t.Fatalf("versions after retention = %d (%v), want ≤ %d", len(got), got, RetentionKeepHistory+1)
	}
	if !slicesContains(got, "v1.38.3-20261013-0911") {
		t.Fatalf("the active version must survive retention: %v", got)
	}
	// active 是第 2 新（在 keep 窗口内），保留面 = newest 3（active 在内，不双计）。
	want := []string{"v1.38.3-20261014-1200", "v1.38.3-20261013-0911", "v1.38.3-20261013-0014"}
	if len(got) != len(want) {
		t.Fatalf("kept = %v, want %v", got, want)
	}
	for _, name := range want {
		if !slicesContains(got, name) {
			t.Fatalf("expected %s to survive, kept = %v", name, got)
		}
	}
	if len(report.PrunedTrees) != 4 {
		t.Fatalf("pruned = %v, want the 4 oldest trees", report.PrunedTrees)
	}
}

// TestEnforceRetentionActiveAmongNewestKeepsThreeTotal 钉不双计：active 就是
// 最新版时总保留 3 棵（3 历史+当前的重叠形态）。
func TestEnforceRetentionActiveAmongNewestKeepsThreeTotal(t *testing.T) {
	versions := []string{"v1.0", "v2.0", "v3.0", "v4.0"}
	root := writePruneFixture(t, versions, "v4.0")

	EnforceVersionRetention(root)

	if got := remainingVersions(t, root); len(got) != 3 {
		t.Fatalf("kept = %v, want 3 (active inside the keep window is not double-counted)", got)
	}
}

// TestEnforceRetentionRemovesStaleReplacedShells 钉验收②③：超龄 .replaced-
// 残壳清除；current.json、staging 本体与新鲜 .staging- 草稿全部保留。
func TestEnforceRetentionRemovesStaleReplacedShells(t *testing.T) {
	root := writePruneFixture(t, []string{"v1.0", "v2.0"}, "v2.0")
	versionsDir := filepath.Join(root, VersionsDirName)

	// 超龄残壳（26h 前）必须清除；新鲜残壳（同次切换产物）保留。
	staleShell := filepath.Join(versionsDir, "v1.0.replaced-nonce1")
	if err := os.MkdirAll(staleShell, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-26 * time.Hour)
	if err := os.Chtimes(staleShell, old, old); err != nil {
		t.Fatal(err)
	}
	freshShell := filepath.Join(versionsDir, "v2.0.replaced-nonce2")
	if err := os.MkdirAll(freshShell, 0o755); err != nil {
		t.Fatal(err)
	}
	// staging 本体（restart_update 的发布源）与新鲜 .staging- 草稿都保留。
	stagingBody := filepath.Join(root, "staging")
	if err := os.MkdirAll(stagingBody, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingBody, "version.txt"), []byte("v3.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	freshStaging := filepath.Join(versionsDir, StagingDirName("v3.0", "nonce9"))
	if err := os.MkdirAll(freshStaging, 0o755); err != nil {
		t.Fatal(err)
	}

	report := EnforceVersionRetention(root)

	if _, err := os.Stat(staleShell); !os.IsNotExist(err) {
		t.Fatalf("the stale replaced shell must be removed, stat err = %v", err)
	}
	if !slicesContains(report.RemovedReplaced, "v1.0.replaced-nonce1") {
		t.Fatalf("the removed shell must be reported: %+v", report)
	}
	for _, keep := range []string{freshShell, freshStaging, stagingBody} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("%s must survive retention: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, CurrentFileName)); err != nil {
		t.Fatalf("current.json must survive retention: %v", err)
	}
}

// TestEnforceRetentionQuarantineSweep 钉 quarantine 同规则：安装根与 versions/
// 下的超龄 orphaned/quarantine 节点清除，新鲜节点与 symlink 保留。
func TestEnforceRetentionQuarantineSweep(t *testing.T) {
	root := writePruneFixture(t, []string{"v1.0"}, "v1.0")
	versionsDir := filepath.Join(root, VersionsDirName)
	old := time.Now().Add(-26 * time.Hour)

	staleOrphan := filepath.Join(root, "Reasonix.app.reasonix-orphaned-123")
	staleQuarantine := filepath.Join(versionsDir, "config.toml.reasonix-quarantine-456")
	freshQuarantine := filepath.Join(root, "Reasonix.app.reasonix-quarantine-789")
	for _, dir := range []string{staleOrphan, staleQuarantine, freshQuarantine} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{staleOrphan, staleQuarantine} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	report := EnforceVersionRetention(root)

	if _, err := os.Stat(staleOrphan); !os.IsNotExist(err) {
		t.Fatalf("stale orphaned node must be removed, stat err = %v", err)
	}
	if _, err := os.Stat(staleQuarantine); !os.IsNotExist(err) {
		t.Fatalf("stale quarantined node must be removed, stat err = %v", err)
	}
	if _, err := os.Stat(freshQuarantine); err != nil {
		t.Fatalf("fresh quarantined node must survive the grace window: %v", err)
	}
	if len(report.RemovedQuarantined) != 2 {
		t.Fatalf("removed quarantined = %v, want 2", report.RemovedQuarantined)
	}
}

// TestEnforceRetentionOnEmptyRoot 钉无害面：空安装根（无 versions/）不是错误，
// 报告为空。
func TestEnforceRetentionOnEmptyRoot(t *testing.T) {
	root := t.TempDir()
	report := EnforceVersionRetention(root)
	if !report.Empty() {
		t.Fatalf("empty install root must produce an empty report, got %+v", report)
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.EqualFold(s, needle) {
			return true
		}
	}
	return false
}
