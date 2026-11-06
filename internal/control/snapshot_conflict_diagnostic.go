package control

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

type snapshotConflictDiagnostic struct {
	At               time.Time `json:"at"`
	BranchID         string    `json:"branch_id"`
	Mode             string    `json:"mode"`
	Outcome          string    `json:"outcome"`
	Kind             string    `json:"kind,omitempty"`
	DiskMessages     int       `json:"disk_messages,omitempty"`
	SnapshotMessages int       `json:"snapshot_messages,omitempty"`
	BaseRevision     int64     `json:"base_revision,omitempty"`
	DiskRevision     int64     `json:"disk_revision,omitempty"`
	RecoveryBranchID string    `json:"recovery_branch_id,omitempty"`
	ExistingRecovery bool      `json:"existing_recovery,omitempty"`
	Occurrence       int       `json:"occurrence,omitempty"`
	Repeated         bool      `json:"repeated_in_process,omitempty"`
	// Caller is the file:line of the code path that hit the conflict. Several
	// writers share one session file and the counters alone cannot say which
	// one raced the disk, so field reports could only prove "same process"
	// (task 34 P0-1). This is what makes the next occurrence attributable.
	Caller string `json:"caller,omitempty"`
	// ForeignLease is true when another runtime currently holds this session's
	// write lease (task 67). Combined with Caller it answers whether a conflict
	// came from a multi-instance write rather than an in-process race.
	ForeignLease bool `json:"foreign_lease,omitempty"`
	// tl1443 recovery trace: the decision, the write path that met the
	// conflict, both sides' digests and who recorded the disk revision.
	Decision         string `json:"decision,omitempty"`
	Reason           string `json:"reason,omitempty"`
	Trigger          string `json:"trigger,omitempty"`
	Entry            string `json:"entry,omitempty"`
	Chain            string `json:"chain,omitempty"`
	SnapshotDigest   string `json:"snapshot_digest,omitempty"`
	BaseDigest       string `json:"base_digest,omitempty"`
	DiskDigest       string `json:"disk_digest,omitempty"`
	LedgerDigest     string `json:"ledger_digest,omitempty"`
	DiskWriter       string `json:"disk_writer,omitempty"`
	DiskWriterPID    int    `json:"disk_writer_pid,omitempty"`
	PeerTrigger      string `json:"peer_trigger,omitempty"`
	PeerChain        string `json:"peer_chain,omitempty"`
	PeerAgeMs        int64  `json:"peer_age_ms,omitempty"`
	BrakeActive      bool   `json:"brake_active,omitempty"`
	BrakeSeq         int    `json:"brake_seq,omitempty"`
	BrakeRemainingMs int64  `json:"brake_remaining_ms,omitempty"`
	SinceLastForkMs  int64  `json:"since_last_fork_ms,omitempty"`
	RewritePending   bool   `json:"rewrite_pending,omitempty"`
	Fallthrough      string `json:"fallthrough,omitempty"`
}

// conflictDiagDedup bounds repeated conflict event log lines for the same
// {path, disk revision} key within a process. Physical recovery outcomes keep
// the first repeat so doctor can observe the concurrent-writer signal.
var conflictDiagDedup sync.Map // key -> *atomic.Int64

// conflictDiagOccurrences counts recovery/conflict outcomes by logical topic
// for this process. Only the count is persisted; the topic ID is never written
// to the diagnostic record.
var conflictDiagOccurrences sync.Map // logical topic key -> *atomic.Int64

// RecordRecoveryLifecycle appends one content-free catalog or cleanup outcome
// to the existing per-session recovery ledger. The closed outcome set prevents
// callers from persisting user-controlled text as diagnostic metadata.
func RecordRecoveryLifecycle(path, outcome string) {
	mode := ""
	switch outcome {
	case "classified_covered", "classified_adopted", "classified_preferred", "classified_diverged":
		mode = "catalog"
	case "cleanup_moved", "cleanup_kept", "cleanup_skipped_in_use", "cleanup_revalidation_failed":
		mode = "cleanup"
	default:
		return
	}
	appendSnapshotConflictDiagnostic(path, mode, outcome, nil, "", false)
}

// snapshotConflictCaller reports the immediate caller of the diagnostic writer
// as base(file):line. Skip 2 = the function that decided to write the record.
func snapshotConflictCaller() string {
	if _, file, line, ok := runtime.Caller(2); ok {
		return filepath.Base(file) + ":" + strconv.Itoa(line)
	}
	return ""
}

func appendSnapshotConflictDiagnostic(path, mode, outcome string, saveErr error, recoveryPath string, existing bool) {
	writeSnapshotConflictDiagnostic(snapshotConflictCaller(), nil, path, mode, outcome, saveErr, recoveryPath, existing)
}

// appendTracedConflictDiagnostic is appendSnapshotConflictDiagnostic for a
// recovery decision that carries a fork trace; the record gains its fields.
func appendTracedConflictDiagnostic(trace *forkTrace, path, mode, outcome string, saveErr error, recoveryPath string, existing bool) {
	writeSnapshotConflictDiagnostic(snapshotConflictCaller(), trace, path, mode, outcome, saveErr, recoveryPath, existing)
}

func writeSnapshotConflictDiagnostic(caller string, trace *forkTrace, path, mode, outcome string, saveErr error, recoveryPath string, existing bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	var diskRev int64
	var conflict *agent.SessionSnapshotConflictError
	if errors.As(saveErr, &conflict) && conflict != nil {
		diskRev = conflict.DiskRevision
	}
	rec := snapshotConflictDiagnostic{
		At:           time.Now(),
		BranchID:     agent.BranchID(path),
		Mode:         mode,
		Outcome:      outcome,
		Caller:       caller,
		ForeignLease: agent.SessionLeaseHeldByOtherRuntime(path),
	}
	createsPhysicalRecovery := diagnosticCreatesPhysicalRecovery(outcome)
	if createsPhysicalRecovery {
		logicalKey := rec.BranchID
		if meta, ok, err := agent.LoadBranchMeta(path); err == nil && ok && strings.TrimSpace(meta.TopicID) != "" {
			logicalKey = strings.Join([]string{meta.Scope, meta.WorkspaceRoot, meta.TopicID}, "\x00")
		}
		value, _ := conflictDiagOccurrences.LoadOrStore(logicalKey, &atomic.Int64{})
		occurrence := int(value.(*atomic.Int64).Add(1))
		rec.Occurrence = occurrence
		rec.Repeated = occurrence > 1
	}
	dedupKey := path + "\x00" + outcome + "\x00" + strconv.FormatInt(diskRev, 10)
	dedupValue, _ := conflictDiagDedup.LoadOrStore(dedupKey, &atomic.Int64{})
	dedupOccurrence := dedupValue.(*atomic.Int64).Add(1)
	dedupLimit := int64(1)
	if createsPhysicalRecovery {
		dedupLimit = 2
	}
	if dedupOccurrence > dedupLimit {
		return
	}
	if conflict != nil {
		rec.Kind = string(conflict.Kind)
		rec.DiskMessages = conflict.ExistingMessages
		rec.SnapshotMessages = conflict.SnapshotMessages
		rec.BaseRevision = conflict.BaseRevision
		rec.DiskRevision = conflict.DiskRevision
	}
	if recoveryPath != "" {
		rec.RecoveryBranchID = agent.BranchID(recoveryPath)
		rec.ExistingRecovery = existing
	}
	trace.fillDiagnostic(&rec)
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	logPath := store.SessionConflictLog(path)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func diagnosticCreatesPhysicalRecovery(outcome string) bool {
	switch strings.TrimSpace(outcome) {
	case "moved_to_stable_recovery", "forked_recovery_branch", "forked_file_lock_recovery":
		return true
	default:
		return false
	}
}
