package agent

// tl1429: 进程内锁句柄登记面。此前 tryTakeSessionLockFileAt/tryTakeSessionLockFile
// 拿到的每把锁句柄都不留任何进程内痕迹:一个丢失绑定的句柄(lease 对象被撕了
// registry 条目、guard 被 take 后没走到释放、组件间引用丢失)持有的 OS 锁在
// 本进程内既不可指认也不可释放——SessionLeaseHeldByOtherRuntime 的 lock probe
// 对「本进程第二句柄」与「另一进程」同样失败,tl1404 终局兜底因此把自锁形态
// 误判成「held outside this process」提前烧断重试(2026-11-04 17:55 装机现场,
// holder 恒 pid=0 零身份)。
//
// 登记面给每把 wrapper 句柄记录:持有者 kind(哪个 runtime/op 在持)与持有时刻。
// 三类消费方:
//  1. 拒绝日志 —— 获取失败时 dump 持有条目(替代恒 pid=0 的零身份);
//  2. 终局强放 —— ForceReleaseInProcessSessionLease 对登记在册的句柄直接撤
//     (本进程名下一切退场的终局语义),不再因 tryTake 失败整体放弃;
//  3. 终局判定 —— SessionLeaseHeldOutsideProcess 用「登记表是否有该锁条目」
//     区分本进程句柄与真外部占用;登记表外的 probe 失败仍按外部处理(保守侧
//     不变:真实外部占用永不抢)。
//
// 登记面是诊断/强放证据,不是互斥原语:条目不阻止任何 take,同路径异常双持时
// 新句柄覆盖旧条目(旧句柄释放时 noteReleased 认指针,不误删新条目)。绕过
// wrapper 的句柄(测试用裸 OS 原语构造的「模型化外部」、probe 用即取即放的
// tryLockSessionLeaseFile)天然不在册——这正是「登记表外=本进程不知情=按外部
// 处理」语义的实现方式。

import (
	"strings"
	"sync"
	"time"

	"reasonix/internal/store"
)

// sessionLockKindUnattributed is the initial kind of every registered handle.
// The acquire paths re-annotate with the runtime/op that owns the hold; an
// entry still carrying this value at dump time names a take whose owner never
// claimed it (short-lived arbitration takes, or a take still inside the
// acquire call itself).
const sessionLockKindUnattributed = "unattributed"

// SessionLockKindInFlightRemovalGuard reports whether kind names a removal
// guard this process currently operates (tl1429b-2). Those handles are NOT
// lost bindings: a live goroutine (the archive trash phase, the delayed
// trash, the cleanup-pending reconcile) holds the guard struct and will
// release it when its step completes. The orphan sweep and the terminal
// force-release must leave them alone — evicting one tears the mutual
// exclusion between two concurrent destructive flows on the same session
// (the 2026-11-06 08:26 heal-vs-reconcile loop: each side's heal swept the
// other side's in-flight guard as an "orphan" and both trashed concurrently).
func SessionLockKindInFlightRemovalGuard(kind string) bool {
	switch kind {
	case "removal_guard", "removal_guard_converted":
		return true
	}
	return false
}

// sessionLeaseLockHeldByInFlightRemovalGuard reports whether the registered
// holder(s) of path's lease lock include an in-flight removal guard.
func sessionLeaseLockHeldByInFlightRemovalGuard(path string) bool {
	lockPath := store.SessionLeaseLock(canonicalSessionSavePath(path))
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	reg, ok := sessionLockRegistry.byPath[lockPath]
	if !ok || reg == nil {
		return false
	}
	return SessionLockKindInFlightRemovalGuard(reg.kind)
}

// sessionLockRegistration is one live in-process lock handle. The registry
// holds the *sessionLockFile itself so the terminal force-release can evict
// exactly the handle that blocks a session — an orphaned handle would
// otherwise be nameless and unreleasable.
type sessionLockRegistration struct {
	lock       *sessionLockFile
	kind       string
	acquiredAt time.Time
}

var sessionLockRegistry = struct {
	sync.Mutex
	byPath map[string]*sessionLockRegistration
}{byPath: map[string]*sessionLockRegistration{}}

// registerSessionLock books a freshly taken handle under its lock path.
// Called by the platform take primitives right after the OS lock is held.
func (l *sessionLockFile) registerSessionLock() {
	if l == nil || l.path == "" {
		return
	}
	reg := &sessionLockRegistration{lock: l, kind: sessionLockKindUnattributed, acquiredAt: time.Now().UTC()}
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	sessionLockRegistry.byPath[l.path] = reg
	l.reg = reg
}

// annotateSessionLock records which runtime/op now owns the hold. Acquire
// paths call it right after the take returns; without it every entry would
// dump as "unattributed" and the refusal log could not answer "who".
func (l *sessionLockFile) annotateSessionLock(kind string) {
	if l == nil || kind == "" {
		return
	}
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	if l.reg != nil && sessionLockRegistry.byPath[l.path] == l.reg {
		l.reg.kind = kind
	}
}

// noteReleased retires the registration when the handle unlocks. Unlock and
// RemoveAndUnlock both call it; idempotence comes from l.reg being cleared,
// so a double release (or RemoveAndUnlock's internal Unlock) never touches a
// successor entry booked under the same path.
func (l *sessionLockFile) noteReleased() {
	if l == nil || l.reg == nil {
		return
	}
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	if sessionLockRegistry.byPath[l.path] == l.reg {
		delete(sessionLockRegistry.byPath, l.path)
	}
	l.reg = nil
}

// SessionLockHolderEvidence is the redacted identity of one in-process lock
// holder: the runtime/op kind and the hold's start time. It deliberately
// carries no path — refusal logs name the holder, never the session.
type SessionLockHolderEvidence struct {
	Kind       string    `json:"kind"`
	AcquiredAt time.Time `json:"acquired_at"`
}

func (e SessionLockHolderEvidence) String() string {
	since := "unknown"
	if !e.AcquiredAt.IsZero() {
		since = e.AcquiredAt.UTC().Format(time.RFC3339)
	}
	kind := e.Kind
	if kind == "" {
		kind = sessionLockKindUnattributed
	}
	return "kind=" + kind + " since=" + since
}

// sessionLockEvidenceFor snapshots the in-process holders of one lock path,
// sorted by acquire time. Used by refusal paths to dump WHO holds the lock
// when the holder identity record on disk is missing or pid-less.
func sessionLockEvidenceFor(lockPath string) []SessionLockHolderEvidence {
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	var out []SessionLockHolderEvidence
	for _, reg := range sessionLockRegistry.byPath {
		if reg == nil || reg.lock == nil || reg.lock.path != lockPath {
			continue
		}
		kind := reg.kind
		if kind == "" {
			kind = sessionLockKindUnattributed
		}
		out = append(out, SessionLockHolderEvidence{Kind: kind, AcquiredAt: reg.acquiredAt})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].AcquiredAt.Before(out[j-1].AcquiredAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// sessionLockHeldInProcess reports whether any wrapper handle in this process
// currently holds lockPath. The registry is the only source: a lock held by
// another process (or a handle this process booked no entry for) reports
// false, keeping every conservative consumer on its current side.
func sessionLockHeldInProcess(lockPath string) bool {
	if lockPath == "" {
		return false
	}
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	_, ok := sessionLockRegistry.byPath[lockPath]
	return ok
}

// takeRegisteredSessionLock evicts the registered handle for lockPath and
// returns it for the caller to release. The entry leaves the registry here;
// the handle's own release then finds nothing to retire. Terminal-archive
// only — this is the strong arm that tears down a handle this process lost
// the binding for.
func takeRegisteredSessionLock(lockPath string) *sessionLockFile {
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	reg := sessionLockRegistry.byPath[lockPath]
	if reg == nil || reg.lock == nil {
		return nil
	}
	delete(sessionLockRegistry.byPath, lockPath)
	reg.lock.reg = nil
	return reg.lock
}

// SessionLeaseLockHeldInProcess reports whether this process holds the
// session's lease lock through any registered wrapper handle (tl1429: the
// self-lock form the refusal logs and the terminal fallback must be able to
// name).
func SessionLeaseLockHeldInProcess(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	return sessionLockHeldInProcess(store.SessionLeaseLock(canonicalSessionSavePath(path)))
}

// SessionLeaseLockInProcessHolders snapshots the in-process holders of the
// session's lease lock, for refusal logs and diagnostics.
func SessionLeaseLockInProcessHolders(path string) []SessionLockHolderEvidence {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return sessionLockEvidenceFor(store.SessionLeaseLock(canonicalSessionSavePath(path)))
}

// SessionSaveLockHeldInProcess / SessionSaveLockInProcessHolders are the save
// lock counterparts (the "holder unknown + save lock long-held" form dumps
// through the same evidence channel).
func SessionSaveLockHeldInProcess(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	return sessionLockHeldInProcess(store.SessionLockFile(canonicalSessionSavePath(path)))
}

func SessionSaveLockInProcessHolders(path string) []SessionLockHolderEvidence {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return sessionLockEvidenceFor(store.SessionLockFile(canonicalSessionSavePath(path)))
}

// DropSessionLeaseLockRegistrationForTest removes the registration entry for
// path's lease lock while the handle itself stays held — the test seam that
// models a handle this process knows nothing about (a pre-registry leftover,
// or a foreign process holding the lock through a path this process never
// booked). Production never calls it.
func DropSessionLeaseLockRegistrationForTest(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	lockPath := store.SessionLeaseLock(canonicalSessionSavePath(path))
	sessionLockRegistry.Lock()
	defer sessionLockRegistry.Unlock()
	reg := sessionLockRegistry.byPath[lockPath]
	if reg == nil {
		return false
	}
	delete(sessionLockRegistry.byPath, lockPath)
	reg.lock.reg = nil
	return true
}
