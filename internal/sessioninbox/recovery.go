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
// Task 263: settledBy reports whether an item's source message was already
// delivered elsewhere (the collab mailbox cursor / task-570 delivery
// receipt). Such an item finished its job before the restart — it is dropped
// along the normal completion path instead of being resurrected as uncertain
// work, so consumed messages never replay onto the guidance shelf after an
// update. nil keeps the old behaviour.
//
// Task 641: "delivered" is not "consumed into a turn". The pump acks the mail
// cursor (and records the delivery receipt) the moment the message lands in
// this inbox — long before any turn admits it. Only states that prove
// admission may therefore settle as residue: in-flight, plus the Uncertain
// face loadOrInit gives them after a cross-process restart. A Queued/Blocked
// row was never admitted — its content is real pending work that must survive
// the restart and resume (settling it silently deleted the message: with a
// working probe every delivered-but-unprocessed collab row would have
// vanished on every restart).
//
// P15: an orphaned StateSteerConsumed row is dropped unconditionally, probe or
// not. The consume transition is the durable "instruction handed to the agent"
// boundary, so the row is applied residue by definition — rewriting it to
// Uncertain is what resurrected already-applied composer guidance as pending
// work after every restart.
//
// Task 589b: acceptance is not injection. StateSteerAccepted never settles:
// between the durable accept (TrySteerInboxItem's SetState) and the durable
// consume (the loader's MarkSteerConsumed, at the next tool-round gap) sits a
// tool-call-length window, and a kill inside it leaves a row whose delivery
// receipt was written long before the accept — the probe's evidence cannot
// distinguish this face. The row is parked as unapplied-steer review work
// instead (same BlockReason the graceful turn-exit path uses), so it survives
// every later settled pass until the user dispositions it.
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
		// Task 300 + 641: the settled drop covers admission-proven states only.
		// 300 gated on in-flight and missed the Uncertain face loadOrInit gives
		// them after a cross-process restart; extending to every pending state
		// also matched Queued/Blocked rows, whose mail cursor is settled at
		// DELIVERY time — with a live probe that deleted real pending work.
		if settledBy == nil || !(inFlight(m) || m.State == StateUncertain) {
			return false
		}
		// Task 589b: accepted-but-uninjected has no injection evidence — the
		// delivery receipt predates the accept, so settledBy(m) is true with
		// near-certainty for collab rows and cannot stand in for "reached a
		// turn". Never settle the accepted state itself.
		if m.State == StateSteerAccepted {
			return false
		}
		// Task 585/641: review parks keep the row even when the mail was
		// delivered — their content did not durably reach the transcript, so
		// delivery is not application evidence. Dropping them would silently
		// delete exactly the work the parks exist to surface for inspection.
		if m.BlockReason == BlockReasonSteerUnapplied || m.BlockReason == BlockReasonTranscriptNotDurable {
			return false
		}
		return settledBy(m)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	needsRecovery := false
	for i := range s.man.Items {
		item := s.man.Items[i]
		if isOwned(item.ID) {
			continue
		}
		// Task 300: an all-pending leftover set must not return early (the 263
		// drop ran only on in-flight, and loadOrInit had already rewritten those
		// to Uncertain), but a plain live queue needs no disk pass — only
		// settled Uncertain residue is worth the transaction (task 641: Queued
		// and Blocked never settle). Probe reads are lock-free file reads,
		// safe under s.mu.
		if inFlight(item) {
			needsRecovery = true
			continue
		}
		// Task 641: only Uncertain survives to this probe — Queued/Blocked were
		// never admitted, so their settled delivery is not residue.
		if item.State == StateUncertain && settledBy != nil && isSettled(item) {
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
		if !isOwned(item.ID) && (inFlight(item) || isPendingState(item.State)) {
			// P15: a consumed steer crossed the durable delivery boundary
			// (MarkSteerConsumed commits before the instruction is handed to
			// the agent), so an unowned consumed row was already applied when
			// the previous run ended — rewriting it to Uncertain is what
			// replayed finished guidance onto the shelf after every update
			// restart. Task 641: isSettled gates on admission-proven states,
			// so a settled Queued/Blocked row (delivered, never admitted) is
			// real pending work and survives. Runs ahead of the settled probe
			// so the consumed-steer drop holds for hosts that inject no probe
			// at all.
			if item.State == StateSteerConsumed || isSettled(item) {
				if item.Idempotency != "" {
					if droppedIDs == nil {
						droppedIDs = map[string]string{}
					}
					droppedIDs[item.Idempotency] = item.ID
				}
				continue
			}
			// Returned "recovered" stays in-flight-only so repeat calls keep
			// idempotence (0 after the first pass); pending residue is counted
			// into RecoveredN below instead.
			if inFlight(item) {
				// Task 589b: an accepted steer orphaned mid-window parks with
				// the same reason the graceful unapplied-steer path uses
				// (Controller.onInboxUnappliedSteer), so later recovery passes
				// keep it via the park exemption instead of settling it as
				// delivered residue on their very next run.
				wasAccepted := item.State == StateSteerAccepted
				item.State = StateUncertain
				if wasAccepted {
					item.BlockReason = BlockReasonSteerUnapplied
				} else {
					item.BlockReason = "in-flight owner is no longer active"
				}
				item.UpdatedAt = now
				recovered++
			}
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
	// Task 300: RecoveredN = every surviving unowned pending item — in-flight
	// orphans land here too once rewritten to Uncertain (counted once, not
	// double-counted with `recovered`) — recounted, never accumulated, so
	// items this pass just dropped cannot keep their "Recovered N" slot. That
	// is exactly how a processed guidance message still replayed onto the
	// shelf after the restart.
	survivingPending := 0
	for i := range next.Items {
		item := next.Items[i]
		if !isOwned(item.ID) && isPendingState(item.State) {
			survivingPending++
		}
	}
	next.RecoveredN = min(len(next.Items), survivingPending)
	// The pause holds whenever live pending work remains — including a
	// drop-only pass (recovered==0) that cleared settled residue but left
	// real work for /queue review: task 300's "tell, don't replay" half.
	if recovered > 0 || survivingPending > 0 {
		next.Paused = true
	}
	if recovered > 0 {
		next.Recovered = true
	}
	if err := s.commitManifestLocked(next); err != nil {
		return 0, err
	}
	s.notifyLocked(s.snapshotLocked())
	return recovered, nil
}
