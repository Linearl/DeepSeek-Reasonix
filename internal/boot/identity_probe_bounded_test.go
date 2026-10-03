package boot

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 任务 460：boot 配置路径上残余的无界文件系统探测加界。additional directory
// 归一（解析+目录校验）、allow/forbid root 去重键（pathComparisonKey）、
// 工作目录向上 .git 走查（nearestGitRoot）原本在死网络路径上各吃满一次
// SMB 重连预算（~21s，与任务 455 同签名）。本文件逐条锁定预算行为与本地回归。

func shortenProbeBudgets(t *testing.T) (restore func()) {
	t.Helper()
	origStatTimeout, origStat := additionalDirStatTimeout, additionalDirStat
	origWalkBudget, origWalk := gitRootWalkBudget, gitRootWalk
	additionalDirStatTimeout = 50 * time.Millisecond
	gitRootWalkBudget = 50 * time.Millisecond
	return func() {
		additionalDirStatTimeout, additionalDirStat = origStatTimeout, origStat
		gitRootWalkBudget, gitRootWalk = origWalkBudget, origWalk
	}
}

// TestNormalizeAdditionalDirsSlowStatSkipsGrant：目录校验超时时必须跳过该目录
// 并保持 boot 不报错（授权面 fail closed：未验证的根不授予；boot 可用性 fail
// open），而不是挂满 SMB 重连预算后让整个 Build 失败。
func TestNormalizeAdditionalDirsSlowStatSkipsGrant(t *testing.T) {
	restore := shortenProbeBudgets(t)
	defer restore()

	slow := errors.New("slow stat sentinel")
	originalStat := additionalDirStat
	additionalDirStat = func(string) (os.FileInfo, error) {
		time.Sleep(2 * time.Second) // 远超预算（模拟死网络盘上的目录校验）
		return nil, slow
	}
	t.Cleanup(func() { additionalDirStat = originalStat })

	root := t.TempDir()
	dead := filepath.Join(root, "dead-extra")
	start := time.Now()
	out, err := normalizeAdditionalDirs(root, []string{dead})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("timed-out additional dir probe must skip, not fail Build: %v", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("timed-out probe cost %v (bound leaked through; budget was 50ms)", elapsed)
	}
	if len(out) != 0 {
		t.Fatalf("skipped dir must not be granted, got %v", out)
	}
}

// TestNormalizeAdditionalDirsLocalBehaviorRegression：本地行为回归——存在的
// 目录照常授予；普通文件与不存在目录仍报错（拼写保护不放宽）。
func TestNormalizeAdditionalDirsLocalBehaviorRegression(t *testing.T) {
	restore := shortenProbeBudgets(t)
	defer restore()

	root := t.TempDir()
	extra := filepath.Join(root, "extra")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := normalizeAdditionalDirs(root, []string{"extra"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !strings.EqualFold(out[0], filepath.Clean(extra)) {
		t.Fatalf("existing dir grant = %v, want [%q]", out, filepath.Clean(extra))
	}

	file := filepath.Join(root, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeAdditionalDirs(root, []string{"plain.txt"}); err == nil {
		t.Fatal("plain file must keep failing the not-a-directory check")
	}
	missing := filepath.Join(root, "missing-dir")
	if _, err := normalizeAdditionalDirs(root, []string{missing}); err == nil {
		t.Fatal("missing dir must keep failing the inspect check (typo protection)")
	}
}

// TestBoundedNearestGitRootTimesOut：.git 走查超时时必须返回「无 root」并快速
// 结束（回退语义与找不到 .git 一致），而不是挂起。
func TestBoundedNearestGitRootTimesOut(t *testing.T) {
	restore := shortenProbeBudgets(t)
	defer restore()

	slowRoot := t.TempDir()
	originalWalk := gitRootWalk
	gitRootWalk = func(string) (string, bool) {
		time.Sleep(2 * time.Second) // 远超预算（模拟死网络盘上的逐级探测）
		return slowRoot, true
	}
	t.Cleanup(func() { gitRootWalk = originalWalk })

	start := time.Now()
	root, ok := boundedNearestGitRoot(filepath.Join(t.TempDir(), "deep"))
	elapsed := time.Since(start)
	if ok {
		t.Fatalf("timed-out walk must report no root, got %q", root)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("timed-out walk cost %v (bound leaked through; budget was 50ms)", elapsed)
	}
}

// TestBoundedNearestGitRootFindsLocalRepository：预算内本地走查行为不变——
// 从子目录向上找到含 .git 的仓库根。
func TestBoundedNearestGitRootFindsLocalRepository(t *testing.T) {
	restore := shortenProbeBudgets(t)
	defer restore()

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(repo, "pkg", "sub")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	root, ok := boundedNearestGitRoot(deep)
	if !ok {
		t.Fatal("local repository root not found within budget")
	}
	if !strings.EqualFold(root, filepath.Clean(repo)) {
		t.Fatalf("git root = %q, want %q", root, filepath.Clean(repo))
	}
}

// TestPathComparisonKeyBlackHoleUNCBounded：任务 460 端到端守卫（Windows）。
// 去重键计算碰到不可路由 UNC 时必须在解析预算内返回，而不是吃满 ~21s SMB
// 重连；键值与原实现超时回退（保留原路径）一致。
func TestPathComparisonKeyBlackHoleUNCBounded(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC share reconnect budget is Windows-specific behavior")
	}
	target := `\\10.255.255.1\task460blackhole\allow-root`
	want := strings.ToLower(filepath.Clean(target))

	start := time.Now()
	if got := pathComparisonKey(target); got != want {
		t.Fatalf("black-hole key = %q, want cleaned lowered %q", got, want)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("black-hole key cost %v (unbounded; SMB reconnect budget leaked through)", elapsed)
	}
}

// TestPathComparisonKeyLocalRegression：本地键回归——Windows 大小写别名同键。
func TestPathComparisonKeyLocalRegression(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive-letter case aliasing is Windows-specific")
	}
	dir := t.TempDir()
	if pathComparisonKey(dir) != pathComparisonKey(strings.ToLower(dir)) {
		t.Fatalf("case aliases diverged: %q vs %q", pathComparisonKey(dir), pathComparisonKey(strings.ToLower(dir)))
	}
}
