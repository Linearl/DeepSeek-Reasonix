package agent

import (
	"crypto/sha256"
	"runtime"
	"sync"
	"time"
)

// Ledger commit trace (tl1443): a bounded in-process record of who advanced
// each session's CAS ledger, so a later snapshot conflict can name the code
// path that wrote the disk revision it lost to. Recording costs one
// runtime.Callers and a slot copy; symbolization happens only on lookup.
const (
	ledgerCommitTraceCap   = 512
	ledgerCommitTraceDepth = 40
)

type ledgerCommitRecord struct {
	path     string // raw save path; canonicalized only at lookup
	revision int64
	digest   string
	at       time.Time
	depth    int
	pcs      [ledgerCommitTraceDepth]uintptr
}

var ledgerCommits struct {
	mu   sync.Mutex
	ring [ledgerCommitTraceCap]ledgerCommitRecord
	next int
	size int
}

// LedgerCommitTrace is one in-process ledger commit found by
// RecentLedgerCommit. PCs are raw return addresses for runtime.CallersFrames,
// innermost first.
type LedgerCommitTrace struct {
	Revision int64
	Digest   string
	At       time.Time
	PCs      []uintptr
}

func noteLedgerCommit(path string, revision int64, digest [sha256.Size]byte) {
	rec := ledgerCommitRecord{path: path, revision: revision, digest: digestString(digest), at: time.Now()}
	rec.depth = runtime.Callers(2, rec.pcs[:])
	ledgerCommits.mu.Lock()
	ledgerCommits.ring[ledgerCommits.next] = rec
	ledgerCommits.next = (ledgerCommits.next + 1) % ledgerCommitTraceCap
	if ledgerCommits.size < ledgerCommitTraceCap {
		ledgerCommits.size++
	}
	ledgerCommits.mu.Unlock()
}

// RecentLedgerCommit returns this process's newest recorded commit of
// revision on path. false means the revision was not committed by a save in
// this process within the trace window (another runtime, a non-save writer,
// or an evicted slot).
func RecentLedgerCommit(path string, revision int64) (LedgerCommitTrace, bool) {
	if revision <= 0 || path == "" {
		return LedgerCommitTrace{}, false
	}
	var candidates []ledgerCommitRecord
	ledgerCommits.mu.Lock()
	for i := range ledgerCommits.size {
		idx := (ledgerCommits.next - 1 - i + ledgerCommitTraceCap) % ledgerCommitTraceCap
		if rec := ledgerCommits.ring[idx]; rec.revision == revision {
			candidates = append(candidates, rec)
		}
	}
	ledgerCommits.mu.Unlock()
	if len(candidates) == 0 {
		return LedgerCommitTrace{}, false
	}
	key := canonicalSessionSavePath(path)
	for _, rec := range candidates {
		if rec.path == path || canonicalSessionSavePath(rec.path) == key {
			return LedgerCommitTrace{
				Revision: rec.revision,
				Digest:   rec.digest,
				At:       rec.at,
				PCs:      append([]uintptr(nil), rec.pcs[:rec.depth]...),
			}, true
		}
	}
	return LedgerCommitTrace{}, false
}
