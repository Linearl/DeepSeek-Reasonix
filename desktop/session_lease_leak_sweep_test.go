package main

import (
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

func shrinkLeakAgeForTest(t *testing.T, d time.Duration) {
	t.Helper()
	old := sessionLeaseLeakMinAge
	sessionLeaseLeakMinAge = d
	t.Cleanup(func() { sessionLeaseLeakMinAge = old })
}

// Task 485 P1 验收（正例）：租约登记为活跃、holder 是本进程、但 tabs /
// detachedSessions 里已无任何活 runtime——这正是 308-O4 释放链泄漏出的孤儿形态。
// 扫描必须通过 tracker 持有的原始 lease 对象走正式 Release：锁回到 FREE、
// 登记清空、collab/打开路径立即可重新拿锁。
func TestSweepOrphanedSessionLeasesReleasesOrphan(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	shrinkLeakAgeForTest(t, 0)
	sessionPath := filepath.Join(t.TempDir(), "orphan.jsonl")
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	tab := &WorkspaceTab{ID: "orphan_tab"}
	tab.adoptSessionLease(lease)
	key := sessionRuntimeKey(sessionPath)
	if !agent.SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("precondition: lease must be registered active")
	}
	if _, ok := trackedSessionLeaseEntry(key); !ok {
		t.Fatal("precondition: the tab-bound lease must be tracked")
	}

	// 泄漏形态：tab 对象已从 App 注册表消失（本测试未登记它），lease 无任何
	// 释放触点，只剩 tracker 里的对象引用。
	a.sweepOrphanedSessionLeases(time.Now())

	if !lease.Released() {
		t.Fatal("orphan lease was not released by the sweep")
	}
	if _, ok := trackedSessionLeaseEntry(key); ok {
		t.Fatal("tracker entry not removed after the orphan release")
	}
	if agent.SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("lease registry still lists the orphan as held")
	}
	again, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("re-acquire after orphan sweep: %v", err)
	}
	again.Release()
}

// 反例一：tab 活着（可见 tab 注册在案并持有该 key）——acquire 中间窗与正常
// 长持有的保护面，扫描绝不能释放。
func TestSweepOrphanedSessionLeasesSparesLiveTab(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	shrinkLeakAgeForTest(t, 0)
	sessionPath := filepath.Join(t.TempDir(), "live.jsonl")
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	tab := &WorkspaceTab{ID: "live_tab"}
	tab.adoptSessionLease(lease)
	key := sessionRuntimeKey(sessionPath)
	a.tabs[tab.ID] = tab
	defer tab.releaseSessionLease()

	a.sweepOrphanedSessionLeases(time.Now())

	if lease.Released() {
		t.Fatal("sweep released a live tab's lease")
	}
	if !agent.SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("live tab's lease registration vanished")
	}
}

// 反例二：刚 acquire 的孤儿（未过 2-tick 阈值）——防 acquire 中间窗误杀，
// 默认阈值下扫描必须按兵不动。
func TestSweepOrphanedSessionLeasesRespectsAgeThreshold(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	sessionPath := filepath.Join(t.TempDir(), "young.jsonl")
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	tab := &WorkspaceTab{ID: "young_tab"}
	tab.adoptSessionLease(lease)

	a.sweepOrphanedSessionLeases(time.Now())

	if lease.Released() {
		t.Fatal("sweep fired inside the acquire middle window")
	}
	lease.Release()
}

// 已被正常路径释放的陈旧 tracker 条目必须被清理，且绝不打出幽灵释放日志。
func TestSweepOrphanedSessionLeasesPrunesStaleTrackerEntry(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	shrinkLeakAgeForTest(t, 0)
	sessionPath := filepath.Join(t.TempDir(), "stale.jsonl")
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	tab := &WorkspaceTab{ID: "stale_tab"}
	tab.adoptSessionLease(lease)
	key := sessionRuntimeKey(sessionPath)
	tab.releaseSessionLease()
	if !lease.Released() {
		t.Fatal("precondition: the lease must be released before the sweep")
	}
	if _, ok := trackedSessionLeaseEntry(key); ok {
		t.Fatal("precondition: release must have dropped the tracker entry")
	}

	// 正常路径已经清掉条目；这里只验证扫描对缺失条目无副作用。
	a.sweepOrphanedSessionLeases(time.Now())
	if _, ok := trackedSessionLeaseEntry(key); ok {
		t.Fatal("stale tracker entry resurrected")
	}
}

// 纯判据边界：nil info 否决；turn 登记否决；未到阈值否决；到阈值放行。
func TestSessionLeaseLeakDecision(t *testing.T) {
	now := time.Now()
	if sessionLeaseLeakDecision(nil, now, 0) {
		t.Fatal("nil info must never fire")
	}
	turned := &agent.SessionLeaseInfo{
		AcquiredAt: now.Add(-time.Hour),
		Turn:       &agent.SessionLeaseTurn{TurnID: "turn"},
	}
	if sessionLeaseLeakDecision(turned, now, time.Minute) {
		t.Fatal("a registered turn must veto the sweep")
	}
	young := &agent.SessionLeaseInfo{AcquiredAt: now.Add(-time.Second)}
	if sessionLeaseLeakDecision(young, now, time.Minute) {
		t.Fatal("a lease inside the age window must not fire")
	}
	if !sessionLeaseLeakDecision(young, now, 0) {
		t.Fatal("minAge 0 must fire immediately for testing")
	}
	old := &agent.SessionLeaseInfo{AcquiredAt: now.Add(-2 * time.Hour)}
	if !sessionLeaseLeakDecision(old, now, time.Minute) {
		t.Fatal("an orphan past the age threshold must fire")
	}
}
