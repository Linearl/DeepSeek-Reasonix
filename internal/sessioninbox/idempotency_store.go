package sessioninbox

import (
	"log/slog"
	"time"
)

// LookupEnvelopeReceipt checks semantic identity before sources are read again.
//
// Honest blank note (task 234 minor①, audit): this helper is currently a
// ZERO-CALLER slice — its upstream caller lives in the #10545 core submission
// face, which the scope-c ruling excluded from the pick batch. Kept, not
// deleted: the semantic-identity contract (same key + same envelope hash →
// idempotent hit, differing hash → ErrIdempotencyConflict) is what the
// core-side admission must call when it lands; wire it up or remove it
// together with that landing. Verified zero callers with a full-tree grep at
// 018798436 (only this defining file matched).
func (s *Store) LookupEnvelopeReceipt(key string, env PromptEnvelope) (InboxReceipt, bool, error) {
	if key == "" {
		return InboxReceipt{}, false, nil
	}
	hash, err := idempotencyRequestHash(env)
	if err != nil {
		return InboxReceipt{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := s.beginDiskTransactionLocked()
	if err != nil {
		return InboxReceipt{}, false, err
	}
	defer release()
	return s.idempotentReceiptLocked(key, hash)
}

// LookupReceipt reads the existing bounded idempotency records without
// creating or replaying a write. Used after an uncertain transport outcome.
func (s *Store) LookupReceipt(key string) (InboxReceipt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return InboxReceipt{}, false
	}
	if id, ok := s.man.Idempotency[key]; ok {
		if _, found := s.man.item(id); found {
			return InboxReceipt{ItemID: id, Disposition: DispositionIdempotentHit, Position: s.man.indexOf(id) + 1, Paused: s.man.Paused, Idempotent: true}, true
		}
	}
	if receipt, ok := s.man.Receipts[key]; ok && time.Since(receipt.CompletedAt) <= idempotencyReceiptTTL {
		return InboxReceipt{ItemID: receipt.ItemID, Disposition: DispositionIdempotentHit, Paused: s.man.Paused, Idempotent: true}, true
	}
	return InboxReceipt{}, false
}

func (s *Store) idempotentReceiptLocked(key, requestHash string) (InboxReceipt, bool, error) {
	if key == "" {
		return InboxReceipt{}, false, nil
	}
	if id, ok := s.man.Idempotency[key]; ok {
		if item, found := s.man.item(id); found {
			if previous := s.man.IdempotencyHashes[key]; previous != "" && previous != requestHash {
				return InboxReceipt{}, false, ErrIdempotencyConflict
			}
			// Task 309: the dedup proof line — a repeated delivery of the same
			// content on the same thread lands here instead of the queue.
			slog.Info("sessioninbox: idempotent hit, duplicate delivery deduped",
				"key", key, "item_id", item.ID, "position", s.man.indexOf(item.ID)+1)
			return InboxReceipt{
				ItemID: item.ID, Disposition: DispositionIdempotentHit,
				Position: s.man.indexOf(item.ID) + 1, Paused: s.man.Paused,
				Capacity: s.snapshotLocked().Capacity, Idempotent: true,
			}, true, nil
		}
	}
	receipt, ok := s.man.Receipts[key]
	if !ok || time.Since(receipt.CompletedAt) > idempotencyReceiptTTL {
		return InboxReceipt{}, false, nil
	}
	if receipt.RequestHash != requestHash {
		return InboxReceipt{}, false, ErrIdempotencyConflict
	}
	slog.Info("sessioninbox: idempotent hit, already-consumed receipt replayed",
		"key", key, "item_id", receipt.ItemID)
	return InboxReceipt{
		ItemID: receipt.ItemID, Disposition: DispositionIdempotentHit,
		Paused: s.man.Paused, Capacity: s.snapshotLocked().Capacity, Idempotent: true,
	}, true, nil
}

func (s *Store) idempotentAliasReplayLocked(key, requestHash, itemID string) (bool, error) {
	if key == "" {
		return false, nil
	}
	if existingID, ok := s.man.Idempotency[key]; ok {
		if existingHash := s.man.IdempotencyHashes[key]; existingHash != "" && existingHash != requestHash {
			return false, ErrIdempotencyConflict
		}
		// Platform redelivery while the bound item is still queued: same key,
		// same item, same content — replay the current row untouched, exactly
		// like the consumed-receipt branch below. Proceeding would merge the
		// body a second time and duplicate the user's text
		// (TestCollectAppendDeduplicatesPlatformRedelivery).
		if existingID == itemID {
			return true, nil
		}
		// Task 309 × 221: same content under a key that points at another
		// item is the merge rebind (B's key aliased onto the surviving row)
		// — allowed to proceed, not a conflict. Only a content change under
		// the same key conflicts.
		_ = existingID
		return false, nil
	}
	receipt, ok := s.man.Receipts[key]
	if !ok || time.Since(receipt.CompletedAt) > idempotencyReceiptTTL {
		return false, nil
	}
	if receipt.RequestHash != requestHash || receipt.ItemID != itemID {
		return false, ErrIdempotencyConflict
	}
	return true, nil
}

func bindIdempotency(m *manifest, key, itemID, requestHash string) {
	if key == "" {
		return
	}
	if m.Idempotency == nil {
		m.Idempotency = map[string]string{}
	}
	if m.IdempotencyHashes == nil {
		m.IdempotencyHashes = map[string]string{}
	}
	m.Idempotency[key] = itemID
	m.IdempotencyHashes[key] = requestHash
}
