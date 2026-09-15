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
