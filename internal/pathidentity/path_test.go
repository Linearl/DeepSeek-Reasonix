package pathidentity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sandbox"
)

// 任务 460：Canonical 的逐级 EvalSymlinks 走查原为无界实现，死网络路径上每次
// 保存路径键计算 / 单实例身份检查都会吃满一次 SMB 重连预算（~21s，与任务 455
// 同签名）。现委托 sandbox 共享有界引擎（任务 455fix），本文件锁定两件事：
// 本地路径键形不变；死路径在预算内返回而非挂起。

// TestCanonicalLocalBehaviorRegression：键形回归——存在路径解析到物理路径；
// 不存在的尾部解析到最深存在祖先并回拼（与共享引擎同构）；Windows 大小写归一。
func TestCanonicalLocalBehaviorRegression(t *testing.T) {
	dir := t.TempDir()

	if got, want := Canonical(dir), strings.ToLower(filepath.Clean(dir)); got != want {
		t.Fatalf("existing dir key = %q, want %q", got, want)
	}

	missing := filepath.Join(dir, "not-yet-created", "session.json")
	resolved, err := sandbox.ResolveAbsPath(filepath.Clean(missing))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Canonical(missing), strings.ToLower(filepath.Clean(resolved)); got != want {
		t.Fatalf("missing-tail key = %q, want engine-resolved %q", got, want)
	}

	// 空白输入不炸、仍有确定性键。
	if got := Canonical("   "); got == "" {
		t.Fatal("blank input produced empty key")
	}
}

// TestCanonicalMatchesSharedEngine：Canonical 与共享引擎的解析结果逐点一致
// （引擎缓存语义之内的合法路径），委托接线不被静默绕开。
func TestCanonicalMatchesSharedEngine(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	inputs := []string{dir, nested, filepath.Join(dir, "a", "missing-tail")}
	for _, in := range inputs {
		resolved, err := sandbox.ResolveAbsPath(filepath.Clean(in))
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ToLower(filepath.Clean(resolved))
		if got := Canonical(in); got != want {
			t.Fatalf("Canonical(%q) = %q, want shared-engine key %q", in, got, want)
		}
	}
}

// TestCanonicalBlackHoleUNCBounded：任务 460 端到端守卫（Windows）。不可路由
// UNC 上的路径键计算必须在解析预算内返回 cleaned 键，而不是吃满 ~21s SMB
// 重连；二次调用走引擎 TTL 缓存瞬时返回。
func TestCanonicalBlackHoleUNCBounded(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC share reconnect budget is Windows-specific behavior")
	}
	target := `\\10.255.255.1\task460blackhole\sessions\conv.json`
	want := strings.ToLower(filepath.Clean(target))

	start := time.Now()
	got := Canonical(target)
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("black-hole key cost %v (unbounded; SMB reconnect budget leaked through)", elapsed)
	}
	if got != want {
		t.Fatalf("black-hole key = %q, want cleaned lowered %q", got, want)
	}

	start = time.Now()
	if got2 := Canonical(target); got2 != want {
		t.Fatalf("second call = %q, want %q", got2, want)
	}
	if cached := time.Since(start); cached > 50*time.Millisecond {
		t.Fatalf("second call took %v, want a TTL cache hit", cached)
	}
}
