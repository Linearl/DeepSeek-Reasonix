package main

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// Task 485 P0 验收①②：detached/idle 释放必须连 tab 持有的 session lease 一起
// 释放——泄漏的锁句柄会让本进程自锁（LockFileEx err=33），会话永久 busy。
// 释放后锁必须立即可探测为 FREE，且 collab/打开路径能立即重新拿到租约。
func TestReleaseDetachedSessionReleasesLease(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	ctrl := &idleReleaseCtrl{}
	tab := &WorkspaceTab{ID: "lease_tab", Ctrl: ctrl}
	tab.adoptSessionLease(lease)
	key := sessionRuntimeKey(sessionPath)
	a.detachedSessions[key] = tab

	if !agent.SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("precondition: lease must be held by this process before the release")
	}

	a.releaseDetachedSession(key, tab, 45*time.Minute)

	if got := a.detachedSessions[key]; got != nil {
		t.Fatal("entry still resident after release")
	}
	if ctrl.closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", ctrl.closeCalls.Load())
	}
	if tab.sessionLeaseRuntimeKey() != "" {
		t.Fatalf("tab still reports lease key %q after release", tab.sessionLeaseRuntimeKey())
	}
	if agent.SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("lease registry still lists the session as held after the release")
	}
	if _, locked, err := agent.InspectSessionLease(key); locked {
		t.Fatal("lease lock still HELD after the detached release (task 485 leak shape)")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("inspect lease: %v", err)
	}

	// 验收②：释放后立即重开（collab 投递走的同一条 acquire 路径）必须成功。
	again, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("re-acquire after detached release: %v", err)
	}
	again.Release()
}

// 释放失败轮次（snapshot 失败跳过）不得提前释放租约：lease 与 runtime 同进退，
// 只有真正完成 teardown 的那一轮才放锁（报告验收③）。
func TestReleaseDetachedSessionKeepsLeaseWhenSnapshotFails(t *testing.T) {
	a := newIdleReleaseTestApp(t)
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	ctrl := &idleReleaseCtrl{unsaved: true, snapshotErr: context.DeadlineExceeded}
	tab := &WorkspaceTab{ID: "lease_tab_snapshot", Ctrl: ctrl}
	tab.adoptSessionLease(lease)
	key := sessionRuntimeKey(sessionPath)
	a.detachedSessions[key] = tab

	a.releaseDetachedSession(key, tab, 45*time.Minute)

	if a.detachedSessions[key] == nil {
		t.Fatal("entry released despite a failed snapshot")
	}
	if !agent.SessionLeaseHeldByCurrentRuntime(key) {
		t.Fatal("lease released although the runtime teardown was skipped")
	}
	lease.Release()
}
