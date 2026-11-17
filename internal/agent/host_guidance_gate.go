package agent

import (
	"log/slog"
	"time"

	"reasonix/internal/provider"
)

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
var (
	// hostGuidanceCompactionWaitBound bounds the queue wait. Production
	// compactions run minutes-long (the 09:4x window was ~100s; the 包68 boot
	// compaction 2m50s), so five minutes covers them with headroom; past the
	// bound we write through rather than drop. Vars so tests can shrink them.
	hostGuidanceCompactionWaitBound = 5 * time.Minute
	// hostGuidanceCompactionPoll is the in-flight check cadence while queued.
	hostGuidanceCompactionPoll = 200 * time.Millisecond
)

// addHostGuidance is the single write funnel for channel-F host guidance
// (Origin=host user-role messages the run loop injects into the canonical
// transcript). All callers must land guidance through it: while a compaction
// is in flight the append would invalidate the pass's CAS, so it queues until
// settle (bounded, then writes through — see above).
func (a *Agent) addHostGuidance(msg provider.Message) {
	a.waitHostGuidanceCompactionSettle()
	a.sess.conversation.Add(msg)
}

// waitHostGuidanceCompactionSettle blocks while a real maintenance transaction
// is in flight, bounded by hostGuidanceCompactionWaitBound. Zero-cost when no
// compaction is running (one atomic load — the idle face pays nothing).
func (a *Agent) waitHostGuidanceCompactionSettle() {
	if a.maintaining.Load() == 0 {
		return
	}
	slog.Debug("agent: host guidance queued behind in-flight compaction",
		"writer", SessionWriterID(), "bound", hostGuidanceCompactionWaitBound.String())
	deadline := time.Now().Add(hostGuidanceCompactionWaitBound)
	for a.maintaining.Load() > 0 {
		if !time.Now().Before(deadline) {
			// Bounded wait exhausted: write through and let the commit CAS be
			// the backstop (first-round precedent: the CAS backstop + failure
			// card visibility stay; a burned pass is reported, never silent).
			slog.Warn("agent: host guidance writing through past unsettled compaction",
				"writer", SessionWriterID(), "waited", hostGuidanceCompactionWaitBound.String())
			return
		}
		time.Sleep(hostGuidanceCompactionPoll)
	}
}
