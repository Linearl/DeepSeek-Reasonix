package collabinbox

// tl1768 — filter-based batch dispose: the shared底层 both the agent tool
// (mailbox action=batch_dispose) and the desktop panel build on.
//
// Task 689 gave the mailbox read/consume verbs (peek/drain/query); task 856 ②
// gave the PANEL id-listed delete/clear-all. tl1768 closes the remaining gap:
// dispose BY FILTER (to/from/bucket/thread_id/since/until/unread_only/state —
// the exact Query the read side already speaks), so a pile of already-handled
// mail can be cleaned in one shot without clicking rows one by one.
//
// Semantics (tl1722 回收/硬删双语义 + tl1721 软删除墓碑先例):
//
//   - mode=archive — soft disposal: entries are marked dismissed (the same
//     tombstone Dismiss writes), hidden from the default view, recoverable
//     via Undismiss. The transport layer is untouched.
//   - mode=delete  — physical removal of the archive copies (the Delete
//     semantics: archive-only; mail already injected into conversations is
//     never touched), duplicate clusters expand, dead dismissed/decided keys
//     are garbage-collected. Not recoverable.
//
// Two-phase safety: PlanDispose is the dry-run — it lists EXACTLY what a
// DisposeByFilter with the same filters would touch, and it refuses to answer
// on a degraded (lock-busy, possibly partial) read: a confirmation list built
// from a partial view would authorize the wrong rows. DisposeByFilter re-runs
// the same filter under the exclusive lock at execute time; the caller owns
// the human confirmation between the two calls (the agent tool pins this with
// dry_run defaulting to true).

import (
	"context"
	"fmt"
	"sort"
)

// Dispose modes (tl1722 双语义先例).
const (
	// DisposeArchive dismisses the matched entries (recoverable via Undismiss).
	DisposeArchive = "archive"
	// DisposeDelete physically removes the matched archive copies (irreversible).
	DisposeDelete = "delete"
)

// ValidDisposeMode reports whether name is a known dispose mode.
func ValidDisposeMode(mode string) bool {
	switch mode {
	case DisposeArchive, DisposeDelete:
		return true
	}
	return false
}

// DisposePlan is the dry-run answer: what a dispose with these filters would
// touch. Total is the FULL match count; Entries lists at most maxLimit (newest
// first) with Truncated=true when the match exceeded the cap — a plan that
// silently hid matches would understate the blast radius.
type DisposePlan struct {
	Mode      string  `json:"mode"`
	Total     int     `json:"total"`
	Truncated bool    `json:"truncated"`
	Entries   []Entry `json:"entries"`
}

// DisposeResult is the execute answer. Disposed counts ENTRIES (what the plan
// listed); Removed additionally counts the physical mail lines delete mode
// took (duplicate clusters remove more copies than entries) — 0 for archive,
// which moves nothing physical.
type DisposeResult struct {
	Mode        string   `json:"mode"`
	Disposed    int      `json:"disposed"`
	Removed     int      `json:"removed,omitempty"`
	IDs         []string `json:"ids"`
	Recoverable bool     `json:"recoverable"`
	Snapshot    Snapshot `json:"snapshot"`
}

// planEntries is the shared dry-run core: build the decorated table and apply
// the query filters, newest first. Caller must hold a HEALTHY read lock (a
// degraded index is refused by the caller — a partial view must never produce
// a confirmation list).
func (s *Store) planEntries(ctx context.Context, q Query) ([]Entry, error) {
	idx, err := s.buildIndex(ctx)
	if err != nil {
		return nil, err
	}
	if idx.degraded {
		return nil, fmt.Errorf("collabinbox: dispose plan skipped — the mail transport lock is busy (%s), the view may be partial; retry once it frees up", s.LockHolderInfo())
	}
	entries := idx.entries
	decorate(entries, q.Viewer)
	filtered := filterEntries(entries, q)
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].At > filtered[j].At })
	return filtered, nil
}

// PlanDispose answers the dry-run: which entries would a DisposeByFilter with
// this query touch. Read-only (shared lock), applyRetention stays off — a plan
// never mutates anything.
func (s *Store) PlanDispose(ctx context.Context, q Query, mode string) (DisposePlan, error) {
	if !ValidDisposeMode(mode) {
		return DisposePlan{}, fmt.Errorf("collabinbox: unknown dispose mode %q (want archive|delete)", mode)
	}
	unlock, degraded, err := s.lockRead(ctx)
	if err != nil {
		return DisposePlan{}, err
	}
	if unlock != nil {
		defer unlock()
	}
	if degraded {
		// A confirmation list built from an unlocked (possibly partial) read
		// could authorize the wrong rows — refuse instead of guessing.
		return DisposePlan{}, fmt.Errorf("collabinbox: dispose plan skipped — the inbox lock is busy (%s); retry", s.LockHolderInfo())
	}
	filtered, err := s.planEntries(ctx, q)
	if err != nil {
		return DisposePlan{}, err
	}
	listed := filtered
	if len(listed) > maxLimit {
		listed = listed[:maxLimit]
	}
	return DisposePlan{
		Mode:      mode,
		Total:     len(filtered),
		Truncated: len(filtered) > maxLimit,
		Entries:   listed,
	}, nil
}

// DisposeByFilter executes one batch dispose: every entry matching q is
// archived (dismiss tombstone, recoverable) or physically deleted (archive-
// only semantics — identical to Delete). The filter is re-run under the
// exclusive lock at execute time, so the call is atomic against concurrent
// panel writes; the match is hard-capped at maxLimit and REFUSED above it —
// a runaway filter must not mass-delete in one call (narrow with
// until/since/bucket, or clear the whole archive deliberately from the panel).
func (s *Store) DisposeByFilter(ctx context.Context, q Query, mode string) (DisposeResult, error) {
	if !ValidDisposeMode(mode) {
		return DisposeResult{}, fmt.Errorf("collabinbox: unknown dispose mode %q (want archive|delete)", mode)
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return DisposeResult{}, err
	}
	idx, err := s.buildIndex(ctx)
	if err != nil {
		unlock()
		return DisposeResult{}, err
	}
	if idx.degraded {
		// Same honesty as the plan: a partial view must not decide what dies.
		unlock()
		return DisposeResult{}, fmt.Errorf("collabinbox: dispose skipped — the mail transport lock is busy (%s), the view may be partial; retry", s.LockHolderInfo())
	}
	entries := idx.entries
	decorate(entries, q.Viewer)
	matched := filterEntries(entries, q)
	if len(matched) > maxLimit {
		unlock()
		return DisposeResult{}, fmt.Errorf("collabinbox: filter matches %d entries, over the %d-per-call cap — narrow the filter (until/since/bucket/from) and dispose in batches, or use the panel's clear-all for the whole archive", len(matched), maxLimit)
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].At > matched[j].At })
	ids := make([]string, 0, len(matched))
	for _, e := range matched {
		ids = append(ids, e.ID)
	}
	res := DisposeResult{
		Mode:        mode,
		Disposed:    len(ids),
		IDs:         ids,
		Recoverable: mode == DisposeArchive,
	}
	switch mode {
	case DisposeArchive:
		// The write half of Dismiss, under the already-held lock (shared core).
		now := s.now()
		snap, werr := s.writeStateLocked(ctx, func(st *stateFile) {
			for _, id := range ids {
				st.Dismissed[id] = now
			}
		})
		if werr != nil {
			unlock()
			return DisposeResult{}, werr
		}
		res.Snapshot = snap
		unlock()
	case DisposeDelete:
		// The write half of Delete (fold expansion + PruneBy + state GC).
		removed, derr := s.deleteIDsLocked(ctx, idx, ids)
		if derr != nil {
			unlock()
			return DisposeResult{}, derr
		}
		res.Removed = removed
		snap, lerr := s.List(ctx, Query{Limit: 1}, false)
		if lerr != nil {
			unlock()
			return DisposeResult{}, lerr
		}
		res.Snapshot = snap
		unlock()
	}
	return res, nil
}
