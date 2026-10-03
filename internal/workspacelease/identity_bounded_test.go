package workspacelease

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"reasonix/internal/sandbox"
)

// 任务 460：workspace 根的物理身份解析（symlink 解析 + 向上 .git 走查）曾有界化前
// 会在死网络路径上各吃满一次 SMB 重连预算（~21s，与任务 455 同签名），而该解析
// 在每个 tab boot（boot.go workspacelease.New）与桌面租约重叠判定都会执行。
// 本文件锁定两条界：解析走 sandbox 共享有界引擎；.git 走查套 250ms 预算。

// TestWorkspaceIdentitiesSlowGitWalkBounded：.git 走查超过预算时必须在预算内返回，
// 结果回退为未折叠 root 的身份（不挂起、不超时失败）。构造失败必须 Fatal，禁止 SKIP。
func TestWorkspaceIdentitiesSlowGitWalkBounded(t *testing.T) {
	originalWalk := gitWorktreeRootWalk
	originalBudget := identityResolveBudget
	gitWorktreeRootWalk = func(string) string {
		time.Sleep(2 * time.Second) // 远超预算的慢走查（模拟死网络盘）
		return t.TempDir()
	}
	identityResolveBudget = 50 * time.Millisecond
	t.Cleanup(func() {
		gitWorktreeRootWalk = originalWalk
		identityResolveBudget = originalBudget
	})

	root := filepath.Join(t.TempDir(), "deep", "nonexistent")
	start := time.Now()
	canonical, _, err := workspaceIdentities(root)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("workspaceIdentities returned error: %v", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("slow git walk cost %v (bound leaked through; budget was 50ms)", elapsed)
	}
	if canonical == "" {
		t.Fatal("workspaceIdentities returned empty canonical identity on timeout fallback")
	}
	// 回退契约：走查超时时跳过仓库根折叠，身份 = 引擎解析后 root 的身份。
	resolved, resolveErr := sandbox.ResolveAbsPath(filepath.Clean(root))
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	if want := identityKeyFor(t, resolved); canonical != want {
		t.Fatalf("timeout fallback identity = %q, want resolved-root identity %q (git fold skipped)", canonical, want)
	}
}

// TestWorkspaceIdentitiesBlackHoleUNCBounded：任务 460 端到端守卫（Windows）。
// 不可路由 UNC 上的 workspace 根必须在解析预算+走查预算内返回 cleaned 路径身份，
// 而不是吃满 ~21s SMB 重连；二次调用共享引擎 TTL 缓存。
func TestWorkspaceIdentitiesBlackHoleUNCBounded(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC share reconnect budget is Windows-specific behavior")
	}
	target := `\\10.255.255.1\task460blackhole\workspace`
	start := time.Now()
	canonical, compatibility, err := workspaceIdentities(target)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("black-hole root returned error: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("black-hole root cost %v (unbounded; SMB reconnect budget leaked through)", elapsed)
	}
	if canonical == "" || compatibility == "" {
		t.Fatalf("black-hole root produced empty identity: canonical=%q compatibility=%q", canonical, compatibility)
	}
	if want := identityKeyFor(t, filepath.Clean(target)); canonical != want {
		t.Fatalf("black-hole identity = %q, want unresolved cleaned identity %q", canonical, want)
	}

	start = time.Now()
	if _, _, err := workspaceIdentities(target); err != nil {
		t.Fatal(err)
	}
	if cached := time.Since(start); cached > 2*time.Second {
		t.Fatalf("second call took %v, want the engine TTL cache / SMB negative cache to absorb it", cached)
	}
}

// TestWorkspaceIdentitiesLocalBehaviorRegression：本地路径行为回归——存在目录解析到
// 物理路径；仓库子目录折叠到仓库根（既有契约，见 lease_test.go 同款断言）。
func TestWorkspaceIdentitiesLocalBehaviorRegression(t *testing.T) {
	root := t.TempDir()
	canonical, _, err := workspaceIdentities(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := identityKeyFor(t, root); canonical != want {
		t.Fatalf("existing dir identity = %q, want %q", canonical, want)
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(repo, "packages", "app")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoIdentity, _, err := workspaceIdentities(repo)
	if err != nil {
		t.Fatal(err)
	}
	subIdentity, _, err := workspaceIdentities(subdir)
	if err != nil {
		t.Fatal(err)
	}
	if subIdentity != repoIdentity {
		t.Fatalf("repository subdirectory identity = %q, want root identity %q", subIdentity, repoIdentity)
	}
}

// identityKeyFor 计算未做任何解析时 root 的期望身份（与 workspaceIdentities 的
// 回退路径同构）：cleaned abs → compatibility 身份键。
func identityKeyFor(t *testing.T, root string) string {
	t.Helper()
	compatibility := compatibilityIdentityPath(root)
	return normalizeIdentityPath(compatibility)
}
