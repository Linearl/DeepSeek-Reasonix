package agent

import (
	"fmt"
	"os"

	"reasonix/internal/store"
)

// CompactSessionFile is the manual "repair / slim this session" entry (task
// 275). It performs the same maintenance rewrite the automatic oversized gate
// would have done — DAG rotation plus the schema-1 compact — but as an
// explicit command, and reports the byte sizes it saw so callers can show the
// before/after to the user.
//
// Idleness is the lease: acquiring it fails when another runtime currently
// holds the session (desktop window, serve process), which is exactly the
// "single writer proof" the rotation path needs — and holding it for the
// duration satisfies sessionDAGSingleWriterProof's own lease check. The
// rewrite itself goes through Session.SaveRewriteCompact, so it reuses the
// production save locks, CAS and AtomicWriteFile rollback: a failed compact
// leaves the previous log in place rather than a half-rewritten one.
func CompactSessionFile(sessionPath string) (before, after int64, err error) {
	if sessionPath == "" {
		return 0, 0, fmt.Errorf("empty session path")
	}
	lease, leaseErr := TryAcquireSessionLease(sessionPath)
	if leaseErr != nil {
		return 0, 0, fmt.Errorf("session is not idle (lease busy): %w", leaseErr)
	}
	defer lease.Release()
	before = sessionCompactFileSize(sessionPath)
	s, err := LoadSession(sessionPath)
	if err != nil {
		return before, 0, err
	}
	if err := s.SaveRewriteCompact(sessionPath); err != nil {
		return before, 0, err
	}
	after = sessionCompactFileSize(sessionPath)
	return before, after, nil
}

// sessionCompactFileSize is the size of the DAG event log — the file the
// oversized gate watches and the one the user-facing before/after numbers
// refer to (task 275: 300MB+ .events.jsonl files). The compat jsonl is
// deliberately excluded: it tracks transcript content, not log bloat.
func sessionCompactFileSize(sessionPath string) int64 {
	if info, err := os.Stat(store.SessionEventLog(sessionPath)); err == nil {
		return info.Size()
	}
	return 0
}
