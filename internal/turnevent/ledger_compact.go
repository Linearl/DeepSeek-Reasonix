package turnevent

import (
	"reasonix/internal/event"
)

// 任务 1172（剩余部分）：启用既有 checkpoint 截断。
//
// 判定链逐跳结论（2026-10-30 全库实测，为什么既有能力从未治理存量）：
// checkpoint 截断本体（compactLocked）与读侧契约（FloorSequence/ResetRequired/
// PendingProjections）都是上游原生的，但 cutoff 的推进只认 projection ack 记录
// ——ack 只在 desktop 把投影写进 display sidecar 后发出（tabs.go
// recoverPendingTurnProjections），且只在该会话再次被 desktop 打开时才补发。
// 从未被 desktop 打开过、或打开一次后再没人恢复的会话：ack 永远缺失 ⇒ cutoff
// 停死 ⇒ 每次 AcknowledgeProjection 尾随的 maybeCompactLocked 都在
// `cutoff <= compactedThrough` 处空转（ledger.go compactLocked 首个 return）。
// 实测：20261029 活跃会话 ledger 9.07MB / 14528 event / 3 ack / 0 checkpoint；
// 20260907 弃用会话 3.2MB / 5370 event / 2 ack / 1 checkpoint（截了一次之后
// ack 再也没来）。机制健在，触发链断在「ack 供给」，不是Compact 没接线。
//
// 本文件是 fork 新增的触发面：调用方以盘上证据（display sidecar 已含某 seq
// 之前的全部 pending turn 投影）显式确认消费义务解除，ledger 把
// projectionCommittedThrough 推进到该处并跑一次既有 idle checkpoint。
// 上游既有契约的调用与 fork 新增逻辑的分界（回板要求逐项列清）：
//   - 上游既有、零改动：compactLocked / checkpointRecord 格式 /
//     FloorSequence / ResetRequired / PendingProjections 语义；
//   - fork 新增：CompactIdleThrough（显式确认 + 非 terminal 自保）。
// 不新增任何盘上格式，不新增任何配置项；截断沿用 checkpoint 语义（checkpoint
// 行 + TerminalSummaries 全量保留，正文事件按投影消费证据放行）。

// CompactIdleThrough explicitly confirms that every projection obligation up
// to `through` has been discharged (the caller proves this against durable
// evidence — the desktop sweep checks the display sidecar carries a recorded
// projection for every unacknowledged terminal turn up to that sequence),
// then runs one idle checkpoint compaction. The ledger cannot verify the
// caller's evidence; the contract is that only a caller holding such proof
// may call this, and every other path keeps the strict ack-only discipline.
//
// Safety properties (pinned by tests):
//   - Only advances: through below the current committed-through is a no-op,
//     making repeat calls idempotent.
//   - Never crosses a non-terminal record at or below `through`: a turn still
//     waiting on the user (or otherwise not terminal) keeps its records, and
//     `through` is shrunk to just below that record's sequence before the
//     compaction runs, so the cutoff can only land on a terminal record whose
//     whole turn fits under the confirmed range.
//   - The active-turn gate inside compactLocked still applies: a ledger with
//     a live non-terminal turn at its head is never rewritten.
//
// Returns the log size before and after; both are zero when nothing was
// eligible (already compacted through `through`, or the gate held it back).
func (l *Ledger) CompactIdleThrough(through uint64) (int64, int64, error) {
	if l == nil {
		return 0, 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path == "" {
		return 0, 0, nil
	}
	through = l.shrinkThroughPastNonTerminal(through)
	if through > l.projectionCommittedThrough {
		l.projectionCommittedThrough = through
	}
	before := l.fileSize
	if err := l.compactLocked(true); err != nil {
		return before, l.fileSize, err
	}
	return before, l.fileSize, nil
}

// shrinkThroughPastNonTerminal pulls `through` back below the first record of
// every turn whose LAST record under the range is not terminal. The cutoff
// loop in compactLocked only advances on terminal records, but the
// retained-records rewrite drops everything at or below the cutoff — so a
// through that lands past a turn still waiting on the user would discard that
// turn's records. The judgment is per turn, not per record: a terminal turn's
// own stream events (text/tool recs) are non-terminal lines but belong to a
// turn that did complete, and must not hold the cutoff back.
func (l *Ledger) shrinkThroughPastNonTerminal(through uint64) uint64 {
	lastStatus := make(map[string]event.TurnStatus)
	firstSeq := make(map[string]uint64)
	for _, rec := range l.records {
		if rec.Sequence > through {
			break
		}
		if rec.TurnID == "" {
			continue
		}
		lastStatus[rec.TurnID] = rec.Status
		if seen, ok := firstSeq[rec.TurnID]; !ok || rec.Sequence < seen {
			firstSeq[rec.TurnID] = rec.Sequence
		}
	}
	shrunk := through
	for turnID, status := range lastStatus {
		if status.Terminal() {
			continue
		}
		if first := firstSeq[turnID]; first > 0 && first <= shrunk {
			shrunk = first - 1
		}
	}
	return shrunk
}
