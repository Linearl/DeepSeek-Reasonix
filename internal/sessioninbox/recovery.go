package sessioninbox

import "time"

// RecoverOrphanedInFlight converts admitted items that no live Controller owns
// into reviewable pending work. The transition is atomic so a crash cannot
// leave only part of a multi-item active set recoverable.
func (s *Store) RecoverOrphanedInFlight(ownedIDs []string) (int, error) {
	owned := make(map[string]struct{}, len(ownedIDs))
	for _, id := range ownedIDs {
		if id != "" {
			owned[id] = struct{}{}
		}
	}
	return s.RecoverOrphanedInFlightOwnedBy(func(id string) bool {
		_, ok := owned[id]
		return ok
	}, nil)
}

// RecoverOrphanedInFlightOwnedBy resolves live ownership only after the Store
// transaction is current. The callback must be lock-free and must not call
// Store methods; Controller uses sync.Map-backed ownership so a newly admitted
// item cannot be recovered from a stale pre-transaction snapshot.
//
// Task 263: settledBy reports whether an in-flight item's source message was
// already consumed elsewhere (the collab mailbox cursor). Such an item finished
// its job before the restart — it is dropped along the normal completion path
// instead of being resurrected as uncertain work, so consumed messages never
// replay onto the guidance shelf after an update. nil keeps the old behaviour.
func (s *Store) RecoverOrphanedInFlightOwnedBy(ownedBy func(string) bool, settledBy func(InboxItemMeta) bool) (int, error) {
	if s == nil {
		return 0, ErrClosed
	}
	isOwned := func(id string) bool {
		return ownedBy != nil && ownedBy(id)
	}
	inFlight := func(m InboxItemMeta) bool {
		switch m.State {
		case StateRunning, StateSteerAccepted, StateSteerConsumed:
			return true
		default:
			return false
		}
	}
	isSettled := func(m InboxItemMeta) bool {
		return settledBy != nil && inFlight(m) && settledBy(m)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	needsRecovery := false
	for i := range s.man.Items {
		if isOwned(s.man.Items[i].ID) {
			continue
		}
		if inFlight(s.man.Items[i]) {
			needsRecovery = true
		}
	}
	if !needsRecovery {
		return 0, nil
	}
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return 0, err
	}
	defer release()
	if err := s.mutableLocked(); err != nil {
		return 0, err
	}

	next := s.man.clone()
	now := time.Now().UTC()
	recovered := 0
	kept := next.Items[:0]
	var droppedIDs map[string]string // idempotency key -> itemID (settle deletions)
	for i := range next.Items {
		item := next.Items[i]
		if !isOwned(item.ID) && inFlight(item) {
			if isSettled(item) {
				// Task 263: already consumed before the restart — drop it the
				// way a completed item is dropped; it is not recovered work.
				if item.Idempotency != "" {
					if droppedIDs == nil {
						droppedIDs = map[string]string{}
					}
					droppedIDs[item.Idempotency] = item.ID
				}
				continue
			}
			item.State = StateUncertain
			item.BlockReason = "in-flight owner is no longer active"
			item.UpdatedAt = now
			recovered++
		}
		kept = append(kept, item)
	}
	dropped := len(next.Items) - len(kept)
	next.Items = kept
	// A deletion must take its idempotency bookkeeping with it, or the manifest
	// verifier rejects the commit ("idempotency key references missing item").
	for key, itemID := range droppedIDs {
		if id, ok := next.Idempotency[key]; ok && id == itemID {
			delete(next.Idempotency, key)
			delete(next.IdempotencyHashes, key)
		}
		delete(next.Receipts, key)
	}
	if recovered == 0 && dropped == 0 {
		return 0, nil
	}
	if recovered > 0 {
		next.Paused = true
		next.Recovered = true
		next.RecoveredN = min(len(next.Items), next.RecoveredN+recovered)
	}
	if err := s.commitManifestLocked(next); err != nil {
		return 0, err
	}
	s.notifyLocked(s.snapshotLocked())
	return recovered, nil
}
