package control

// 任务 710: redundant snapshot-save coalescing.
//
// The fork开发-新5 incident (2026-10-10): a 444-minute turn on a 96MB event
// log took 4431 saves, each one a schema-1 full-rewrite path paying a whole
// log replay plus a content-sized replace event (0.7-1.3s on the user's
// disk), saturating IO and freezing tab switches. The save-frequency side of
// that storm is a fan of redundant savers: the desktop TurnDone autosave, the
// action-time snapshotTab calls, and the mid-turn ticker all persist the same
// session on top of the durability boundaries that already ran (turn end,
// inbox completion, shutdown).
//
// The gate gives the redundant savers one cheap shared rule: if a durable
// snapshot for this exact session path landed less than snapshotSaveMinGap
// ago, a snapshot-mode save may skip — the transcript it would persist is at
// most one gap stale, the in-flight-turn marker plus the next boundary save
// (turn end / shutdown) still cover the crash window (#3772's contract), and
// a forced-rewrite session is never skipped.
//
// Deliberately NOT gated: SnapshotForShutdown, SnapshotRewrite, the turn-end
// snapshotActivityIfChanged, the inbox completion snapshot, and every
// non-blocking form. Those are durability boundaries or already self-skip
// when busy; the gate exists to thin the redundant fan-out, never to delay a
// boundary.

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// snapshotSaveMinGap is atomic (nanoseconds) so a test can shrink it without
// racing a controller parked in a previous test.
var snapshotSaveMinGap atomic.Int64

func init() { snapshotSaveMinGap.Store(int64(30 * time.Second)) }

// saveCoalesceState remembers where and when a durable snapshot last landed.
// It lives on the Controller, so a session/path swap (new/fork/switch) starts
// with a cold gate — a fresh path always saves.
type saveCoalesceState struct {
	mu              sync.Mutex
	lastDurableAt   time.Time
	lastDurablePath string
}

// recordDurableSnapshot notes a completed durable snapshot so the redundant
// savers can coalesce behind it.
func (c *Controller) recordDurableSnapshot(path string, at time.Time) {
	if c == nil || path == "" {
		return
	}
	c.saveCoalesce.mu.Lock()
	c.saveCoalesce.lastDurableAt = at
	c.saveCoalesce.lastDurablePath = path
	c.saveCoalesce.mu.Unlock()
}

// snapshotSaveRecentlyDurable reports whether a snapshot save to path may
// skip: a durable one landed within the gap and no rewrite is pending. The
// rewrite check runs against the live session — a pending compaction/rewrite
// must land regardless of recency, because the transcript the disk holds is
// not the shape the memory holds.
//
// 任务 748: two facts defeat the "at most one gap stale" argument outright,
// so they disable the skip:
//   - the session file vanished externally — the skipped save is then not
//     one gap stale but infinitely stale, and the save is also the only
//     place that detects the removal and forks the stable recovery branch;
//   - turn markers are queued but not yet persisted — they are the schema-2
//     crash contract (#3772) the skip itself leans on, and leaving them
//     memory-only removes the very coverage the gap window claims.
func (c *Controller) snapshotSaveRecentlyDurable(path string) bool {
	if c == nil || path == "" {
		return false
	}
	gap := time.Duration(snapshotSaveMinGap.Load())
	if gap <= 0 {
		return false
	}
	c.saveCoalesce.mu.Lock()
	at, recorded := c.saveCoalesce.lastDurableAt, c.saveCoalesce.lastDurablePath == path
	c.saveCoalesce.mu.Unlock()
	if !recorded || at.IsZero() || time.Since(at) >= gap {
		return false
	}
	if c.executor != nil && c.executor.Session() != nil {
		if c.executor.Session().NeedsRewriteSave() {
			return false
		}
		if c.executor.Session().HasPendingTurnMarkers() {
			return false
		}
	}
	if _, err := os.Stat(path); err != nil {
		// Missing or unreadable: let the real save run so external removal is
		// detected and recovered instead of coalesced away.
		return false
	}
	return true
}
