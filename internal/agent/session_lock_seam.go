package agent

import (
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/store"
)

// SetSessionFileLockWaitForTest shortens the bounded cross-process save-lock
// wait so a test can drive the lock-held path without the full window.
// Restore with the returned function. Production must leave the defaults.
func SetSessionFileLockWaitForTest(wait, poll time.Duration) (restore func()) {
	prevWait, prevPoll := sessionFileLockWait, sessionFileLockPollInterval
	sessionFileLockWait, sessionFileLockPollInterval = wait, poll
	return func() {
		sessionFileLockWait, sessionFileLockPollInterval = prevWait, prevPoll
	}
}

// HoldSessionFileLockForTest takes the session's compatibility save lock the
// way another process would, so saves in this process time out on it.
func HoldSessionFileLockForTest(path string) (release func(), err error) {
	lock, err := tryTakeSessionLockFile(store.SessionLockFile(path))
	if err != nil {
		return nil, err
	}
	return lock.Unlock, nil
}

// HoldSessionLeaseLockForTest takes the session's lease lock through the
// production wrapper (tl1429): the handle is BOOKED in the in-process lock
// registry — exactly the self-lock form a lost binding leaves behind. Tests
// use it to stage the in-process orphan handle; pair it with
// DropSessionLeaseLockRegistrationForTest to model a handle this process
// knows nothing about (a pre-registry leftover / foreign process).
func HoldSessionLeaseLockForTest(path string) (release func(), err error) {
	lock, err := tryTakeSessionLeaseLockFile(store.SessionLeaseLock(canonicalSessionSavePath(path)))
	if err != nil {
		return nil, err
	}
	lock.annotateSessionLock("test_self_lock")
	return func() { lock.RemoveAndUnlock() }, nil
}

// HoldForeignSessionLeaseForTest publishes lease metadata naming another
// runtime and keeps the OS lock held so SessionLeaseHeldByOtherRuntime
// reports true for path. Production code never stages a foreign identity;
// tests use this to exercise multi-instance notices without a second process.
func HoldForeignSessionLeaseForTest(path string) (release func(), err error) {
	path = canonicalSessionSavePath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	unlock, err := tryLockSessionLeaseFile(path)
	if err != nil {
		return nil, err
	}
	info := SessionLeaseInfo{
		SessionPath: path,
		WriterID:    "foreign-runtime-for-test",
		PID:         os.Getpid() + 1,
		Hostname:    "foreign-host",
		AcquiredAt:  time.Now().UTC(),
	}
	if err := SaveSessionLeaseInfo(path, info); err != nil {
		unlock()
		return nil, err
	}
	return func() {
		_ = os.Remove(sessionLeaseInfoPath(path))
		unlock()
	}, nil
}

// HoldForeignSessionLeaseInLockForTest takes the lease lock the way another
// process would AND publishes that foreign identity inside .lease.lock
// itself, exactly like a real second instance does through
// publishSessionLeaseOwner. The in-lock payload is what LoadSessionLeaseInfo
// reads on current builds — an empty lock file reads as corrupt and does NOT
// fall back to the .lease.json sidecar — so a test asserting the foreign
// holder's busy wording must use this rather than SaveSessionLeaseInfo plus
// a raw OS lock. tl 1035.
func HoldForeignSessionLeaseInLockForTest(path, writerID, hostname string, pid int) (release func(), err error) {
	path = canonicalSessionSavePath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	lock, err := tryTakeSessionLeaseLock(path)
	if err != nil {
		return nil, err
	}
	info := SessionLeaseInfo{
		SessionPath: path,
		WriterID:    writerID,
		Hostname:    hostname,
		PID:         pid,
		AcquiredAt:  time.Now().UTC(),
	}
	if err := writeSessionLeaseInfo(lock, info); err != nil {
		lock.Unlock()
		return nil, err
	}
	if err := SaveSessionLeaseInfo(path, info); err != nil {
		lock.Unlock()
		return nil, err
	}
	return func() {
		_ = os.Remove(sessionLeaseInfoPath(path))
		lock.Unlock()
	}, nil
}
