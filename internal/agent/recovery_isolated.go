package agent

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/provider"
)

// ownsWritableBaseline reports whether this session may rewrite path at the
// current disk revision. Ownership requires either a digest match against the
// session's persisted baseline, or a live generation-bound write authority for
// the path at the same revision. Process-level "I hold a lease" alone is not
// enough: a stale controller after rebind must not rewrite under a successor.
func (s *Session) ownsWritableBaseline(path string, existingDigest, rawDigest [sha256.Size]byte, rawDiffers bool, existingRevision int64, existingLedgerDigest string, nextVersion uint64) bool {
	if s.ownsPersistedState(path, existingDigest, existingRevision, existingLedgerDigest, nextVersion) {
		return true
	}
	if rawDiffers && s.ownsPersistedState(path, rawDigest, existingRevision, existingLedgerDigest, nextVersion) {
		return true
	}
	state := s.persistState(path)
	if !state.ok || !state.revisionKnown || state.version > nextVersion {
		return false
	}
	if existingRevision != 0 && state.revision != existingRevision {
		return false
	}
	// Authority-bound same-revision reshape (tool preview, load normalize).
	return s.hasValidWriteAuthority(path)
}

// fixedWriterRecoverySessionPath is one fixed isolated path per writer identity
// so depth-cap / shutdown isolation rewrites in place instead of forking a new
// digest-keyed file on every conflict tick.
func fixedWriterRecoverySessionPath(originalPath string) string {
	writerDigest := sha256.Sum256([]byte(SessionWriterID()))
	suffix := fmt.Sprintf("-recovery-%x", writerDigest[:6])
	id := BranchID(originalPath)
	if strings.HasSuffix(id, suffix) {
		return originalPath
	}
	return filepath.Join(filepath.Dir(originalPath),
		fmt.Sprintf("%s%s.jsonl", recoveryParentStem(id), suffix))
}

// writeRecoveryEventLog writes a recovery event log. Isolated writer lanes
// compact so repeated in-place rewrites stay bounded.
func writeRecoveryEventLog(path string, msgs []provider.Message, digest [sha256.Size]byte, isolated bool) error {
	if isolated {
		return compactSessionEventLog(path, msgs, digest, 0, "recovery")
	}
	return appendSessionReplaceEvent(path, msgs, digest, 0, "recovery")
}
