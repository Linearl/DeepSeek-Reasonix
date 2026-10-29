package sandbox

import (
	"fmt"
	"testing"
	"time"
)

// tl1257 — shell inventory stale-while-revalidate 契约（CI 安全，白盒注入
// build，无真实子进程探测）。设置/实验室展示面的读数走 ForView 三件套，
// 契约是：TTL 过期条目立即返回旧快照（绝不阻塞在发现重建上），后台单飞
// 重建最终发布；键变化与显式失效仍走同步重建；执行面 ResolveShell 语义
// 完全不变。这便是「实验室打开 ≤1s」验收在机制层的达标断言：设置页打开
// = Settings() → sandboxViewFor → ForView 读，读必须毫秒级返回。
func TestShellInventoryStaleWhileRevalidateContract1257(t *testing.T) {
	inv := newShellInventory()
	calls := 0
	inv.build = func(goos, prefer, configPath string) *shellSnapshot {
		calls++
		return &shellSnapshot{
			key:     shellInventoryKey(goos, prefer, configPath),
			builtAt: time.Now(),
			goos:    goos,
			caps:    []ShellCapability{{ID: "probe-marker", Path: fmt.Sprintf("snapshot-%d", calls)}},
		}
	}

	// ① 首读（无缓存可服务）：同步构建。
	first := inv.snapshotOpts("windows", "", "", true)
	if calls != 1 {
		t.Fatalf("first read must build synchronously: calls=%d", calls)
	}
	if first.caps[0].Path != "snapshot-1" {
		t.Fatalf("first read returned wrong snapshot: %s", first.caps[0].Path)
	}

	// ② TTL 内 stale-ok 读：直接命中，零构建。
	inv.snapshotOpts("windows", "", "", true)
	if calls != 1 {
		t.Fatalf("fresh entry must not rebuild: calls=%d", calls)
	}

	// ③ 条目过期 + stale-ok：立即返回旧快照，不发同步构建；后台单飞重建
	// 随后发布。50ms 上限 = 展示面读数的达标断言（生产链其余块均在数百 ms 级）。
	ageCurrentEntry(t, inv)
	start := time.Now()
	stale := inv.snapshotOpts("windows", "", "", true)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("stale-ok read on expired entry took %s (must be <50ms: never block on rebuild)", elapsed)
	}
	if stale.caps[0].Path != "snapshot-1" {
		t.Fatalf("stale-ok read must serve the stale snapshot, got %q", stale.caps[0].Path)
	}
	if calls != 1 {
		t.Fatalf("stale-ok read must not build synchronously: calls=%d", calls)
	}

	// ④ 后台重建在窗口内发布新快照。
	waitForPublishedBuild(t, inv, 2*time.Second)
	if calls != 2 {
		t.Fatalf("background rebuild must eventually run: calls=%d", calls)
	}
	inv.mu.Lock()
	freshPath := inv.current.caps[0].Path
	inv.mu.Unlock()
	if freshPath != "snapshot-2" {
		t.Fatalf("background rebuild must publish: current=%q", freshPath)
	}

	// ⑤ 条目过期 + 非 stale-ok（执行面 ResolveShell 路径）：仍同步重建，
	// 语义与修复前完全一致。
	ageCurrentEntry(t, inv)
	before := calls
	synced := inv.snapshotOpts("windows", "", "", false)
	if calls != before+1 {
		t.Fatalf("expired sync-path read must rebuild synchronously: calls=%d (want %d)", calls, before+1)
	}
	if synced.caps[0].Path != fmt.Sprintf("snapshot-%d", before+1) {
		t.Fatalf("sync-path read must return the fresh snapshot, got %q", synced.caps[0].Path)
	}

	// ⑥ 键变化（配置偏好/路径被编辑）：stale-ok 也不许把旧键快照当新键结果
	// 返回——同步重建（缓存契约：不得张冠李戴）。
	before = calls
	renamed := inv.snapshotOpts("windows", "bash", "", true)
	if calls != before+1 {
		t.Fatalf("key change must rebuild synchronously even with staleOk: calls=%d (want %d)", calls, before+1)
	}
	if renamed.key != shellInventoryKey("windows", "bash", "") {
		t.Fatalf("published snapshot key mismatch: %q", renamed.key)
	}
	if renamed.caps[0].Path != fmt.Sprintf("snapshot-%d", before+1) {
		t.Fatalf("key-change read must return the new-key snapshot, got %q", renamed.caps[0].Path)
	}
}

// ⑦ 显式失效（重新检测按钮）：清条目 + 代际防回灌——失效时在飞的后台重建
// 结果不得覆盖失效决定，失效后的下一次读走同步重建。
func TestShellInventoryInvalidateDuringBackgroundRefresh1257(t *testing.T) {
	inv := newShellInventory()
	calls := 0
	buildEntered := make(chan struct{})
	release := make(chan struct{})
	inv.build = func(goos, prefer, configPath string) *shellSnapshot {
		calls++
		// 仅第 2 次（失效时在飞的后台重建）挂起，供失效竞态注入；
		// 其余构建直接通过。
		if calls == 2 {
			close(buildEntered)
			<-release
		}
		return &shellSnapshot{
			key:     shellInventoryKey(goos, prefer, configPath),
			builtAt: time.Now(),
			goos:    goos,
			caps:    []ShellCapability{{ID: "probe-marker", Path: fmt.Sprintf("snapshot-%d", calls)}},
		}
	}
	inv.snapshotOpts("windows", "", "", false) // build #1（同步）
	ageCurrentEntry(t, inv)

	// stale-ok 读启动后台重建（build #2 卡在 release 前）。
	done := make(chan *shellSnapshot, 1)
	go func() { done <- inv.snapshotOpts("windows", "", "", true) }()
	<-buildEntered

	// 重建在飞时显式失效：代际 +1。
	inv.invalidate()

	// 在飞读者拿到的是失效前快照（读契约不因失效被打破）。
	got := <-done
	if got == nil || len(got.caps) == 0 {
		t.Fatalf("in-flight stale-ok reader must still get a snapshot")
	}
	close(release)

	// 等后台重建收尾：代际不符 → 不得回灌发布。
	waitForIdle(t, inv, 2*time.Second)
	inv.mu.Lock()
	published := inv.current
	inv.mu.Unlock()
	if published != nil {
		t.Fatalf("generation-mismatched background rebuild must not republish after invalidate")
	}

	// 失效后无旧条目可服务 → 下一次读（stale 与否）走同步重建（build #3）。
	fresh := inv.snapshotOpts("windows", "", "", true)
	if fresh == nil || len(fresh.caps) == 0 {
		t.Fatalf("read after invalidate must rebuild synchronously")
	}
	if fresh.caps[0].Path != "snapshot-3" {
		t.Fatalf("post-invalidate read must serve the fresh build, got %q", fresh.caps[0].Path)
	}
}

// --- helpers ---

func ageCurrentEntry(t *testing.T, inv *shellInventory) {
	t.Helper()
	inv.mu.Lock()
	if inv.current == nil {
		inv.mu.Unlock()
		t.Fatalf("test bug: expected a cached entry")
	}
	inv.current.builtAt = time.Now().Add(-shellInventoryTTL - time.Minute)
	inv.mu.Unlock()
}

func waitForPublishedBuild(t *testing.T, inv *shellInventory, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inv.mu.Lock()
		fresh := inv.current != nil && time.Since(inv.current.builtAt) < shellInventoryTTL
		inv.mu.Unlock()
		if fresh {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("background rebuild did not publish within %s", timeout)
}

func waitForIdle(t *testing.T, inv *shellInventory, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		inv.mu.Lock()
		idle := inv.refreshing == nil
		inv.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("background rebuild did not settle within %s", timeout)
}
