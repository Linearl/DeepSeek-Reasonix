package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		// The lock take failed: the holder is either another process, or a
		// handle THIS process booked in the lock registry but lost the binding
		// for (the tl1429 self-lock form — a lease object whose registry entry
		// was torn, a guard taken and never released, a dropped reference).
		// The registry separates the two: a booked handle is this process's
		// own state and the terminal semantics (everything under this
		// process's name retires) say it goes; an unbooked lock is outside
		// this process and is never grabbed.
		held, evidence := sessionLeaseLockInProcessHolder(path)
		if !held {
			return released
		}
		// tl1429b-2: an in-flight removal guard is a live owner with a
		// goroutine behind it (a previous attempt's delayed trash, a
		// concurrent reconcile). Force-releasing it would set two destructive
		// flows loose on the same session — the terminal's own trash runs
		// serialized behind the archive mutex, so the in-flight op either
		// already finished (guard gone) or converges the artifacts afterwards
		// (the committed cleanup-pending form). Leave it and let the terminal
		// degrade to its guard-less path.
		if sessionLeaseLockHeldByInFlightRemovalGuard(path) {
			slog.Warn("session: terminal archive left an in-flight removal guard in place",
				"session_base", filepath.Base(path),
				"holder_kind", evidenceHolderKind(evidence),
				"held_since", evidenceHoldSince(evidence))
			return released
		}
		evicted := takeRegisteredSessionLock(store.SessionLeaseLock(path))
		if evicted != nil {
			_ = evicted.RemoveAndUnlock()
		}
		released = true
		// The owner/identity residue the free-lock path clears below is
		// cleared here too: with the handle gone the lock is free, so the
		// same teardown applies.
		if cur, ok := sessionLeaseOwners.Load(path); ok {
			if sessionLeaseOwners.CompareAndDelete(path, cur) {
				// already counted released above
			}
		}
		sessionLeaseActiveOwners.Delete(path)
		_ = os.Remove(sessionLeaseInfoPath(path))
		slog.Warn("session: terminal archive force-released an unbound in-process lease lock handle",
			"session_base", filepath.Base(path),
			"holder_kind", evidenceHolderKind(evidence),
			"held_since", evidenceHoldSince(evidence))
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

// SessionLeaseHeldOutsideProcess answers the tl1404 terminal question with
// the tl1429 evidence: is the lease lock held by something this process
// cannot name? SessionLeaseHeldByOtherRuntime reports true for BOTH a foreign
// process and an in-process handle whose registry entry is the only trace —
// the probe cannot tell the two apart. The lock registry can: a booked entry
// means the hold is this process's own state (the terminal owns it); no entry
// means genuinely outside (never grabbed, tl1367 line 1).
func SessionLeaseHeldOutsideProcess(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	path = canonicalSessionSavePath(path)
	if !SessionLeaseHeldByOtherRuntime(path) {
		return false
	}
	return !SessionLeaseLockHeldInProcess(path)
}

// sessionLeaseLockInProcessHolder reports whether the session's lease lock is
// held by a registered in-process handle, with its evidence for the log.
func sessionLeaseLockInProcessHolder(path string) (bool, []SessionLockHolderEvidence) {
	held := SessionLeaseLockHeldInProcess(path)
	if !held {
		return false, nil
	}
	return true, SessionLeaseLockInProcessHolders(path)
}

// evidenceHolderKind renders the first evidence entry's kind for a log attr.
func evidenceHolderKind(evidence []SessionLockHolderEvidence) string {
	if len(evidence) == 0 {
		return sessionLockKindUnattributed
	}
	kind := evidence[0].Kind
	if kind == "" {
		return sessionLockKindUnattributed
	}
	return kind
}

// evidenceHoldSince renders the earliest evidence hold time for a log attr.
func evidenceHoldSince(evidence []SessionLockHolderEvidence) string {
	if len(evidence) == 0 {
		return "unknown"
	}
	earliest := evidence[0].AcquiredAt
	for _, ev := range evidence[1:] {
		if !ev.AcquiredAt.IsZero() && (earliest.IsZero() || ev.AcquiredAt.Before(earliest)) {
			earliest = ev.AcquiredAt
		}
	}
	if earliest.IsZero() {
		return "unknown"
	}
	return earliest.UTC().Format(time.RFC3339)
}
