package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/filelock"
	"reasonix/internal/pathidentity"
)

// 会话文件锁域（tl1633 自 save.go 纯移动拆出）：跨进程文件锁、
// 进程内 save-path 互斥、meta 锁与 save-path 锁 key 规范化漏斗。

var (
	sessionSaveLocks sync.Map

	// sessionFileLockWait bounds cross-process save-lock acquisition. Session
	// leases normally prevent competing writers, but CLI/legacy writers and a
	// stalled process can still hold the compatibility .lock file. Navigation
	// and desktop shutdown snapshot synchronously; waiting forever here wedges
	// the UI and keeps the session lease (and WebView) alive indefinitely.
	// Package vars let focused tests shorten the wait without slowing the suite.
	sessionFileLockWait = 5 * time.Second

	sessionFileLockPollInterval = 25 * time.Millisecond

	sessionMetaLockWait = 5 * time.Second

	// 任务 885/946: sessionSavePathLockWait bounds the IN-PROCESS save-path
	// mutex acquisition for EVERY caller — the blocking save path (885) and
	// the loads/maintenance/recovery paths that go through
	// lockSessionSavePath (946). The cross-process file lock above has been
	// bounded since task 357, but this mutex waited forever — a wedged holder
	// (full-replay load, stuck projection) turned into an unbounded queue
	// (observed 791ms → 11973ms monotonic growth, 2026-10-14 incident) and,
	// past the 20s process watchdog, a whole-process kill that dropped the
	// user's in-flight message (2026-10-16 closing_tab incidents,
	// "发信无反应"). The budget is deliberately above every healthy-but-slow
	// critical section seen in the field (~14s for the 238MB-events session),
	// so brief contention waits instead of failing: only a genuinely wedged
	// holder trips it, and the timeout surfaces as ErrSessionFileLockHeld so
	// the existing shutdown-recovery, degraded-read and friendly-error
	// machinery all engage instead of a silent hang.
	sessionSavePathLockWait = 30 * time.Second

	// sessionSavePathLockPoll is the try-lock retry cadence while waiting for
	// the in-process save-path mutex; same rhythm as the file-lock poll.
	sessionSavePathLockPoll = 25 * time.Millisecond

	// ErrSessionFileLockHeld reports that the save lock stayed held for the
	// full bounded acquisition window: the cross-process compatibility .lock
	// file kept by another process, or — task 885 — the in-process save-path
	// mutex kept by another goroutine past sessionSavePathLockWait. Callers
	// that are about to terminate can use this sentinel to persist a recovery
	// branch without waiting on the same stalled file again.
	ErrSessionFileLockHeld = errors.New("session file lock held")
)

// lockSessionSavePath acquires the in-process save-path mutex for path with a
// bounded wait (任务 946): TryLock every sessionSavePathLockPoll and give up
// after sessionSavePathLockWait with ErrSessionFileLockHeld instead of
// queueing forever behind a wedged holder. The blocking save path was bounded
// in 885, but every load/maintenance/recovery caller kept an unbounded
// mu.Lock — one wedged save still parked hydration, tab close and shutdown
// behind the 20s process watchdog, whose hard kill dropped the user's
// in-flight message (2026-10-16 "发信无反应" incidents). The budget is shared
// with the save path on purpose: it sits above every healthy-but-slow
// critical section observed in the field, so brief contention waits and only
// a genuinely wedged holder fails.
func lockSessionSavePath(path string) (func(), error) {
	return lockSessionSavePathWithin(path, sessionSavePathLockWait)
}

// sessionSaveLockWarnWait is the queueing time above which the save-path lock is worth a
// line in the log. Below it, a lock acquired immediately stays silent.
const sessionSaveLockWarnWait = 250 * time.Millisecond

// tryLockSessionSavePath lets low-priority maintenance yield immediately to a
// foreground save. The returned unlock is non-nil only when acquired.
func tryLockSessionSavePath(path string) (func(), bool) {
	key := canonicalSessionSavePath(path)
	v, _ := sessionSaveLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// savePathLockFree is an advisory, never-blocking probe of the in-process
// save-path mutex: true means a save starting right now would not queue
// behind anyone. The lock can change hands before a later try-lock, so this
// must only be used to skip speculative work, never as authorization to
// write.
func savePathLockFree(path string) bool {
	key := canonicalSessionSavePath(path)
	v, ok := sessionSaveLocks.Load(key)
	if !ok {
		return true
	}
	mu := v.(*sync.Mutex)
	if mu.TryLock() {
		mu.Unlock()
		return true
	}
	return false
}

// HoldSessionSavePathForTest acquires the in-process save-path mutex for path
// and returns the release func, so other packages' tests can reproduce
// save-path contention (the mutex itself is deliberately unexported). It keeps
// a plain blocking Lock — a test deliberately holding the lock past the
// production budget is exactly the wedged-holder scenario 946 bounds on the
// real code paths. Tests only — production code must use lockSessionSavePath /
// tryLockSessionSavePath.
func HoldSessionSavePathForTest(path string) func() {
	key := canonicalSessionSavePath(path)
	v, _ := sessionSaveLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// lockSessionFile waits briefly for the cross-process compatibility save lock.
// A short overlap with a legitimate writer is allowed to settle, but an
// stalled or indefinitely held lock fails the save instead of freezing tab
// switching or application shutdown. The caller keeps its in-memory transcript
// and can retry through the existing autosave/recovery paths.
func lockSessionFile(path string) (func(), error) {
	wait := sessionFileLockWait
	poll := sessionFileLockPollInterval
	if poll <= 0 {
		poll = time.Millisecond
	}
	deadline := time.Now().Add(wait)
	for {
		unlock, err := tryLockSessionFile(path)
		if err == nil {
			return unlock, nil
		}
		if !errors.Is(err, ErrSessionFileLockHeld) {
			return nil, err
		}
		remaining := time.Until(deadline)
		if wait <= 0 || remaining <= 0 {
			return nil, ErrSessionFileLockHeld
		}
		if poll > remaining {
			poll = remaining
		}
		time.Sleep(poll)
	}
}

// LockSessionMetaPath serializes a complete branch-meta read-modify-write
// cycle with both goroutines in this process and other Reasonix processes.
// Callers must hold it from the first read through the final replace.
func LockSessionMetaPath(path string) (func(), error) {
	lockPath, err := sessionMetaLockTarget(path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionMetaLockWait)
	releaseFile, err := filelock.Acquire(ctx, lockPath)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("lock session metadata: %w", err)
	}
	return func() {
		releaseFile()
	}, nil
}

func tryLockSessionMetaPath(path string) (func(), bool, error) {
	lockPath, err := sessionMetaLockTarget(path)
	if err != nil {
		return nil, false, err
	}
	releaseFile, err := filelock.TryAcquire(lockPath)
	if errors.Is(err, filelock.ErrHeld) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("try lock session metadata: %w", err)
	}
	return releaseFile, true, nil
}

func sessionMetaLockTarget(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty session path")
	}
	canonical := canonicalSessionSavePath(path)
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		return "", fmt.Errorf("create session metadata dir: %w", err)
	}
	return sessionMetaLockPath(canonical), nil
}

func sessionMetaLockPath(path string) string {
	canonical := canonicalSessionSavePath(path)
	digest := sha256.Sum256([]byte(canonical))
	return filepath.Join(filepath.Dir(canonical), "."+hex.EncodeToString(digest[:8])+".meta.lock")
}

// UpdateBranchMeta is the owner-level metadata API. The callback runs while
// the cross-process metadata lock is held, so callers cannot accidentally
// load a stale sidecar and overwrite fields written by another runtime.
func UpdateBranchMeta(path string, touchUpdated bool, update func(*BranchMeta) error) error {
	unlock, err := LockSessionMetaPath(path)
	if err != nil {
		return err
	}
	defer unlock()
	m, err := ensureBranchMetaUnlocked(path)
	if err != nil {
		return err
	}
	if update != nil {
		if err := update(&m); err != nil {
			return err
		}
	}
	return saveBranchMeta(path, m, touchUpdated)
}

func canonicalSessionSavePath(path string) string {
	// Task 883 probe: time the canonicalization only; the returned key is
	// exactly what it was before.
	started := time.Now()
	key := pathidentity.Canonical(path)
	canonicalProbeSaveLockKey(path, key, started)
	return key
}

// CanonicalSessionPath is the identity key of a session path: cleaned,
// absolute, and case-folded on Windows, matching the key form used by the
// lease registry and the save-path locks. Any runtime bookkeeping that
// compares or maps session paths (desktop tabs, detached runtimes) must use
// this exact form, or the same file splits into distinct keys — e.g.
// `C:\Users\...` vs the lease's lowercased `c:\users\...`. Empty input stays
// empty instead of resolving to the working directory.
func CanonicalSessionPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return canonicalSessionSavePath(path)
}
