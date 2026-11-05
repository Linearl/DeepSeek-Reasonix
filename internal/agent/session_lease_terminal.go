package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/store"
)

// tl1404 终局兜底原语。SweepOrphanInProcessSessionLease 的证据纪律(磁盘身份
// 缺失/损坏/无 pid 才动手)治「已知残留形态」;归档终局兜底需要的是最后一级:
// 本进程对某个会话路径持有的全部 lease 状态,无条件退场——活对象(含
// save-drain 等待可能无限卡的形态)直接撤、owner 条目直接清、盘上身份记录与
// 锁文件直接撕。外部进程持有的 OS 锁不是本进程状态,锁拿不下即原样返回——
// 兜底从不越进程抢锁,只是把自己名下的一切放下。
//
// 代价语义:ForceReleaseNow 跳过 save-drain(save-drain 等一个可能永不结束的
// 卡死保存,终局下「必成功」优先),一个仍在飞的受权保存可能被撤销写权——
// 该会话此时正被归档丢弃,丢一个运行中 turn 正是终局文案向用户预告的代价。
// 因此两个原语仅供归档终局兜底路径调用;常规拒绝/清扫路径(证据纪律)不受影响。

// ForceReleaseNow releases the lease without waiting for in-flight
// authority-guarded saves. Terminal-archive-only: see the file comment.
func (l *SessionLease) ForceReleaseNow() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	leaseLock := l.leaseLock
	l.leaseLock = nil
	ownerID := l.ownerID
	l.mu.Unlock()
	unregisterSessionLeaseLive(l)
	slog.Warn("session: terminal archive force-released a live in-process lease (skipping the save drain)",
		"session_base", filepath.Base(l.path), "owner_generation", ownerID)
	// Revoke ownership-sensitive repair before the OS lock becomes available to
	// a successor; CompareAndDelete keeps a stale generation from deauthorizing
	// a newer reclaimed lease.
	sessionLeaseActiveOwners.CompareAndDelete(l.path, ownerID)
	_ = os.Remove(sessionLeaseInfoPath(l.path))
	sessionLeaseOwners.CompareAndDelete(l.path, ownerID)
	if leaseLock != nil {
		_ = leaseLock.RemoveAndUnlock()
	}
	_ = removeStaleSessionLockSidecar(l.path, store.SessionLockFile(l.path))
}

// ForceReleaseInProcessSessionLease tears down every piece of lease state THIS
// process holds for path: the registered live lease object (released without
// the save drain), the owner-map entries, the identity record, and the lock
// files. The OS lease lock is the boundary: a lock still held by another
// process (or an unknown handle this process cannot name) keeps the take
// failing, the leftover entry is left for the caller's refusal path, and
// released reports only what this process actually gave up. Terminal-archive-
// only: the evidence discipline of the ordinary sweep primitives does not
// apply here.
func ForceReleaseInProcessSessionLease(path string) (released bool) {
	path = canonicalSessionSavePath(path)
	if strings.TrimSpace(path) == "" {
		return false
	}
	if lease := liveSessionLeaseFor(path); lease != nil {
		lease.ForceReleaseNow()
		released = true
	}
	leaseLock, lockErr := tryTakeSessionLeaseLock(path)
	if lockErr != nil {
		// The lock is held by something this process cannot release (another
		// process, or an unregistered handle). Nothing more to give up here.
		return released
	}
	if cur, ok := sessionLeaseOwners.Load(path); ok {
		if sessionLeaseOwners.CompareAndDelete(path, cur) {
			released = true
		}
	}
	sessionLeaseActiveOwners.Delete(path)
	_ = os.Remove(sessionLeaseInfoPath(path))
	_ = leaseLock.RemoveAndUnlock()
	if released {
		slog.Warn("session: terminal archive discarded the in-process lease owner entry and identity record",
			"session_base", filepath.Base(path))
	}
	return released
}
