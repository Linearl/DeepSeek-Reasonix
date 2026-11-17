package agent

// tl1648x: host guidance vs. compaction mutex (delayed-injection variant).
//
// The 2026-11-17 09:4x incident shape: an auto-resume submits its continue
// prompt through the normal admission gate (compaction not yet in flight →
// admitted), the resumed turn kicks a background pressure compaction
// (armRebuildEpisode / guardian / manual), and the turn's own run-loop
// guidance (retry nudges, advisories, continuation prompts — tl1650 channel
// F) lands via bare conversation.Add while the pass is in flight. Any append
// inside the compaction window invalidates commitSummaryProjection's CAS
// (transcriptVersion + covered-prefix hash, compact_commit.go), the whole
// pass is sacrificed (errCompressStaleContext → summaryFailed → blocked) and
// the receipt reports the dropped users (user_dropped=18 on scene). The
// first-round tl1648 gate covers steer consumption only; channel F had no
// gate.
//
// Queue semantics (same precedent as the tl1648 steer gate): while a real
// maintenance transaction is in flight (任务920 maintaining counter) the
// guidance waits for the pass to settle, then lands — the user expectation is
// "compaction finishes, then the conversation continues", not "the compaction
// burns". The wait is bounded: past the bound the guidance writes through and
// the CAS backstop stays as the last resort (guidance is never silently
// dropped — retry chains depend on it landing eventually).
//
// Deadlock safety: no compaction path (Prepare/compactRanges/guardian) ever
// constructs channel-F guidance, so a waiter can never be the pass holder
// itself; endMaintenance's defer fires on every exit path (任务920 S3), so a
// settled pass always releases the waiter. The controller-side 303 self-heal
// cannot misfire on the blocked run loop either: the in-flight pass keeps
// emitting context-maintenance events, which feed the controller-wide
// liveness tracker (the same shape as a synchronous in-turn compaction).
//
// tl1650 note: channel-F guidance is deliberately NOT folded into the
// unified injection entry (run-loop protocol, 任务6 contract); this helper is
// the channel-F write funnel and the natural seam for the stage-2
// normalization to absorb the gate from.
//
// tl1669 (20261117 21:33 用户拍板：压缩执行模型改同步 await): the wait
// implementation is COLLAPSED onto the single await primitive
// (compaction_await.go awaitCompactionSettle) and the write itself goes
// through the shared canonical write funnel (appendTurnMessage). One await
// primitive, one write funnel — the 1648x-specific wait/bound/poll triple no
// longer exists as a second mechanism. Behavior is unchanged: bounded wait →
// timeout writes through → CAS backstop stays.
//
// tl1650/tl1669 note: the guidance write is a CANONICAL write, so it is
// queued exactly like the turn's own messages (US3: 压缩等待期间我发的消息/
// 系统注入排队等压缩完成再进入，不丢失).
import "reasonix/internal/provider"

func (a *Agent) addHostGuidance(msg provider.Message) {
	a.appendTurnMessage(nil, msg)
}
