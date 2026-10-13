package installlayout

import (
	"encoding/json"
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
//   （7 树累积静态形态）+ TestEnforceRetentionAcrossConsecutiveUpdates
//   （两轮「发布+切换+启动确认清理」动态循环，835 对账补钉）
// ②replaced 残壳不留存 —— TestEnforceRetentionRemovesStaleReplacedShells
//   （超龄 24h 清除；新鲜残壳留 24h 宽限是与签名 helper 链同源口径——
//   残壳是切换中途崩溃的遗留，完整 activate 自清，宽限不越日积累）
// ③current.json/staging 不被误清 —— 同上两测试 + 新鲜 staging 保留断言
// ④清理在成功确认后 —— 调用点合同（desktop 启动链），installlayout 层以
//   RetentionReport 记录删除面供调用方日志与测试核对；回滚源（上一版）
//   不丢由 TestEnforceRetentionAcrossConsecutiveUpdates 显式钉死（835 对账
//   补钉），构建侧 keep=5 与本侧 keep=3 共用 PruneVersionTrees 同一引擎，
//   仅 keep 参数按场景取值（构建机余量 vs 运行时上限）。

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

// publishRetentionTree 模拟一次更新的发布面：新版本树落到 versions/（本地
// 构建一次一棵）。
func publishRetentionTree(t *testing.T, root, version string) {
	t.Helper()
	dir := filepath.Join(root, VersionsDirName, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DesktopBinaryName()), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// switchRetentionActive 模拟切换面：current.json 改指新版本（更新链的指针
// 换位）。
func switchRetentionActive(t *testing.T, root, version string) {
	t.Helper()
	body, err := json.Marshal(CurrentPointer{SchemaVersion: CurrentSchemaVersion, ActiveVersion: version, ActiveDir: VersionDirRelative(version)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CurrentFileName), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEnforceRetentionAcrossConsecutiveUpdates 钉验收①④的字面场景（835
// 对账补钉）：连续两次更新——每轮 = 发布新树 + 切 active + 启动确认后清理
// ——之后 versions ≤4；且每一轮的上一版（下一轮的回滚源）必须仍在 keep
// 窗口内。840 版只有 7 树静态累积形态，本钉补动态两轮循环与回滚源显式断言。
func TestEnforceRetentionAcrossConsecutiveUpdates(t *testing.T) {
	// 起点 = 现场累积形态（2026-10-12：7 棵树超上限）。
	root := writePruneFixture(t, []string{
		"v1.38.3-20261012-1100",
		"v1.38.3-20261012-1748",
		"v1.38.3-20261013-0014",
		"v1.38.3-20261013-0911",
		"v1.38.3-20261013-1530",
		"v1.38.3-20261014-0900",
		"v1.38.3-20261014-1200",
	}, "v1.38.3-20261014-0900")

	// 第一次更新：发布 20261014-1800、切换，新版本启动确认后清理。
	first := "v1.38.3-20261014-1800"
	publishRetentionTree(t, root, first)
	switchRetentionActive(t, root, first)
	EnforceVersionRetention(root)

	afterFirst := remainingVersions(t, root)
	if len(afterFirst) > RetentionKeepHistory+1 {
		t.Fatalf("after the first update versions = %d (%v), want ≤ %d", len(afterFirst), afterFirst, RetentionKeepHistory+1)
	}
	if !slicesContains(afterFirst, first) {
		t.Fatalf("the freshly activated version must survive the first pass: %v", afterFirst)
	}

	// 第二次更新：连续第二轮发布+切换+启动确认清理。
	second := "v1.38.3-20261015-0900"
	publishRetentionTree(t, root, second)
	switchRetentionActive(t, root, second)
	EnforceVersionRetention(root)

	afterSecond := remainingVersions(t, root)
	if len(afterSecond) > RetentionKeepHistory+1 {
		t.Fatalf("after two consecutive updates versions = %d (%v), want ≤ %d", len(afterSecond), afterSecond, RetentionKeepHistory+1)
	}
	if !slicesContains(afterSecond, second) {
		t.Fatalf("the running version must survive: %v", afterSecond)
	}
	// 验收④：第一轮的 active = 第二轮更新的回滚源，必须存活——keep=3 的
	// 底线是最新 3 棵历史树恒在窗口内，更新失败永远有源可回。
	if !slicesContains(afterSecond, first) {
		t.Fatalf("the previous version (rollback source) must survive retention: %v", afterSecond)
	}
	// 清理是真删除而非只「不再增长」：跌出 keep 窗口的树必须消失。
	if slicesContains(afterSecond, "v1.38.3-20261013-0911") {
		t.Fatalf("trees beyond the keep window must be pruned, kept = %v", afterSecond)
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
