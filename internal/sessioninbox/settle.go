package sessioninbox

import "log/slog"

// SettleAppliedResidue drops Uncertain rows whose body matches an
// already-applied instruction receipt (P15).
//
// Older recovery passes rewrote orphaned in-flight items to Uncertain without
// consulting any application record, so guidance that had long been applied
// kept replaying onto the shelf as "pending" after every restart. The session
// transcript is the durable application receipt for desktop-source rows (they
// carry no collab mailbox cursor): the caller — the Controller, once per store
// open — matches candidate bodies against the steer texts actually injected
// there. The matcher receives the row's meta alongside its body so hosts can
// match on the authoritative item id first (task 543: a merged receipt stamps
// every member id in its segment headers) and fall back to text comparison.
//
// Only Uncertain rows qualify. Queued rows are live user intent (the user may
// deliberately repeat a finished instruction) and Blocked rows carry their own
// review reason, so both stay user-decided. A body that cannot be read is kept
// too: an unreadable row is never auto-dropped.
//
// Uncertain rows the matcher does not recognise are kept and counted: task 543
// logs them (count + sample ids) instead of letting them slip through
// silently, which is how merged-consumed residue hid before the matcher
// understood merged receipts.
//
// The drop mirrors the settled-residue path: idempotency keys and receipts for
// dropped rows are cleared so re-sending the same instruction later enqueues a
// fresh item instead of deduplicating onto a deleted one.
func (s *Store) SettleAppliedResidue(match func(meta InboxItemMeta, env PromptEnvelope) bool) (int, error) {
	if s == nil {
		return 0, ErrClosed
	}
	if match == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return 0, err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return 0, err
	}
	next := s.man.clone()
	kept := next.Items[:0]
	var removed []InboxItemMeta
	var unmatched []string           // uncertain rows kept without an applied-steer match
	var droppedIDs map[string]string // idempotency key -> itemID (settle deletions)
	for _, item := range next.Items {
		if item.State != StateUncertain {
			kept = append(kept, item)
			continue
		}
		env, readErr := s.readBlobLocked(blobNameFor(item), item.Checksum)
		if readErr != nil || !match(item, env) {
			if readErr == nil {
				unmatched = append(unmatched, item.ID)
			}
			kept = append(kept, item)
			continue
		}
		removed = append(removed, item)
		if item.Idempotency != "" {
			if droppedIDs == nil {
				droppedIDs = map[string]string{}
			}
			droppedIDs[item.Idempotency] = item.ID
		}
	}
	if len(unmatched) > 0 {
		slog.Info("sessioninbox: uncertain residue kept without an applied-steer match",
			"count", len(unmatched), "sampleIDs", boundedSamples(unmatched, 3))
	}
	if len(removed) == 0 {
		return 0, nil
	}
	next.Items = kept
	for key, itemID := range droppedIDs {
		if id, ok := next.Idempotency[key]; ok && id == itemID {
			delete(next.Idempotency, key)
			delete(next.IdempotencyHashes, key)
		}
		delete(next.Receipts, key)
	}
	clearPauseIfEmpty(next)
	if err := s.commitManifestLocked(next); err != nil {
		return 0, err
	}
	for _, item := range removed {
		s.removeBlobLocked(blobNameFor(item))
	}
	s.notifyLocked(s.snapshotLocked())
	return len(removed), nil
}

// boundedSamples caps a sample id list for log lines so a large backlog cannot
// bloat one entry.
func boundedSamples(ids []string, n int) []string {
	if len(ids) > n {
		return append([]string(nil), ids[:n]...)
	}
	return ids
}
