package agent

import (
	"crypto/sha256"
	"os"
	"strconv"
	"strings"
)

// SnapshotConflictIdentity is the content-free evidence a snapshot conflict
// carries for recovery tracing (tl1443): the digest of each side and the
// ledger writer that recorded the disk revision. Digests are full hex; an
// empty field means the classifier had no value for that side.
type SnapshotConflictIdentity struct {
	SnapshotDigest string // transcript this save tried to write
	BaseDigest     string // this session's persisted baseline for the path
	DiskDigest     string // transcript currently on disk (normalized view)
	LedgerDigest   string // content digest the disk ledger records
	LedgerWriter   string // writer id that recorded the disk revision
}

// sessionLedger is the CAS ledger slice of the branch-meta sidecar.
type sessionLedger struct {
	revision int64
	digest   string
	writer   string
}

// sessionContentLedger reads the CAS ledger in one sidecar read. A missing
// sidecar is the zero ledger; an unreadable one is an error (see
// sessionContentRevision).
func sessionContentLedger(path string) (sessionLedger, error) {
	meta, ok, err := loadBranchMetaRetry(path)
	if err != nil {
		return sessionLedger{}, err
	}
	if !ok {
		return sessionLedger{}, nil
	}
	return sessionLedger{
		revision: meta.Revision,
		digest:   strings.TrimSpace(meta.ContentDigest),
		writer:   strings.TrimSpace(meta.WriterID),
	}, nil
}

func newSnapshotConflictIdentity(base sessionPersistState, next, disk [sha256.Size]byte, ledger sessionLedger) SnapshotConflictIdentity {
	identity := SnapshotConflictIdentity{
		SnapshotDigest: digestString(next),
		DiskDigest:     digestString(disk),
		LedgerDigest:   ledger.digest,
		LedgerWriter:   ledger.writer,
	}
	if base.ok {
		identity.BaseDigest = digestString(base.digest)
	}
	return identity
}

// Ledger writer origins: where the runtime that recorded a disk revision ran,
// relative to this process. One closed set for logs and diagnostics.
const (
	LedgerWriterSelf         = "self_process"
	LedgerWriterOtherProcess = "other_process"
	LedgerWriterOtherHost    = "other_host"
	LedgerWriterUnknown      = "unknown"
)

// LedgerWriterOrigin classifies a ledger writer id (host-pid-nonce, see
// newSessionWriterID) against this process, returning the writer pid when the
// id carries one. The host is compared, never returned.
func LedgerWriterOrigin(writerID string) (string, int) {
	writerID = strings.TrimSpace(writerID)
	if writerID == "" {
		return LedgerWriterUnknown, 0
	}
	if writerID == SessionWriterID() {
		return LedgerWriterSelf, os.Getpid()
	}
	host, pid, ok := splitSessionWriterID(writerID)
	if !ok {
		return LedgerWriterUnknown, 0
	}
	if selfHost, _, selfOK := splitSessionWriterID(SessionWriterID()); selfOK && host != selfHost {
		return LedgerWriterOtherHost, pid
	}
	return LedgerWriterOtherProcess, pid
}

// splitSessionWriterID parses host-pid-nonce from the right: the sanitized
// host may itself contain dashes, the pid and nonce never do.
func splitSessionWriterID(id string) (string, int, bool) {
	parts := strings.Split(id, "-")
	if len(parts) < 3 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(parts[len(parts)-2])
	if err != nil || pid <= 0 {
		return "", 0, false
	}
	return strings.Join(parts[:len(parts)-2], "-"), pid, true
}

// ShortDigest is the 12-hex-digit prefix recovery traces log for a digest.
func ShortDigest(digest string) string {
	digest = strings.TrimSpace(digest)
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
