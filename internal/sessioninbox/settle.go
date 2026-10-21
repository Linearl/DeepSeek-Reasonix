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

// SettlePlannedRestartResidue drops the admission-proven review parks whose
// source mail carries a settled delivery receipt (任务840: the planned-restart
// read confirmation).
//
// Face it fixes: an unattended update restart kills the consuming process
// mid-turn, recovery rewrites the orphaned rows to Uncertain (RestartOrphan /
// SteerUnapplied), and neither settle pass can clear them — the transcript
// matcher has no steer receipt for them, and RecoverOrphanedInFlightOwnedBy's
// settled drop skips both parks by design (585/641/589b: no injection
// evidence). The rows then replay onto the shelf with a retry button even
// though their content was consumed before the restart (实测 2026-10-13: three
// consumed guidance messages back as pending after the 24.1 unattended update).
//
// The missing evidence is supplied by the CALLER, not the row: this method is
// only invoked from the task-254 roster consumption point, where the roster
// entry proves THIS restart was planned by the update chain (the same
// planned-vs-crash distinction task 435 drew for the fence), and the
// delivered callback is the task-570 delivery receipt / recipient cursor —
// the collab mail's durable read confirmation, written at delivery time and
// immune to the restart window. Admission is proven by the parked reasons
// themselves: both are only ever written for rows that were already admitted
// into a turn of the PREVIOUS process.
//
// Safety net against a wrong drop: a truly unconsumed collab row is re-delivered
// by the pump, which dedupes onto a fresh row via the idempotency key — an
// at-least-once contract, never silent loss.
//
// BlockReasonTranscriptNotDurable stays untouched (its content did not reach
// the transcript, so the review park is the honest face), and so does every
// Queued/Blocked row and every row the callback cannot vouch for. The drop
// mirrors SettleAppliedResidue: idempotency keys and receipts travel with the
// row.
//
// 任务1004（1008 复发新形态）：停摆期积压的 steer 在恢复后其 durable 副本可能
// 已越过 MarkSteerConsumed 交付边界（StateSteerConsumed），但既没有 park 原因
// 也不是 Uncertain —— 既躲过 840 settle（只匹配 Uncertain+两 park 原因），又
// 在下一次进程重启的 loadOrInit 里被有意跳过（P15 注释：留给
// RecoverOrphanedInFlightOwnedBy 当 applied residue 丢弃，但那个 drop 只在
// store open/rebind 时跑，1008 现场恢复路径不经过 store 重开，副本就留在
// shelf 上重入显示）。本方法补上第三种形态：StateSteerConsumed 且交付已结算
// 的行，在计划内恢复/清扫点直接丢弃——交付回执本身即应用证据（MarkSteerConsumed
// 在指令交给 agent 之前落盘，与 589b 的"accept ≠ injection"论证无关：consume
// 已经是 injection 之后的边界）。StateSteerConsumed 不检查 BlockReason（该
// 状态本身即 admission-proven 的已消费形态，无 park 原因可查）。
func (s *Store) SettlePlannedRestartResidue(delivered func(InboxItemMeta) bool) (int, error) {
	if s == nil {
		return 0, ErrClosed
	}
	if delivered == nil {
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
	var droppedIDs map[string]string // idempotency key -> itemID (settle deletions)
	for _, item := range next.Items {
		// 任务1004: StateSteerConsumed 已越过 durable 交付边界（应用证据由
		// 消费本身提供），交付回执确认来源消息已送达即可丢弃——无需 park
		// 原因。其余状态维持 840 原判据（Uncertain + 两 park 原因）。
		if item.State != StateSteerConsumed &&
			(item.State != StateUncertain ||
				(item.BlockReason != BlockReasonRestartOrphan && item.BlockReason != BlockReasonSteerUnapplied)) {
			kept = append(kept, item)
			continue
		}
		if !delivered(item) {
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
