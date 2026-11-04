package agent

import (
	"fmt"
	"log/slog"
)

// The tl1378 增量2 review-hint layer: every reviewHintBatchInterval tool
// batches or reviewHintTokenDelta estimated tokens of growth, the host
// appends ONE advisory line to the latest tool result suggesting a
// compression review. The CTXAgent paper's read: strong models start
// reviewing around 50-80k tokens on their own; weaker models keep filling the
// window until the hard threshold — the hint gives them the same cue without
// the host ever compressing on its behalf. Nothing reads the hint back: no
// threshold feeds a maintenance decision, it is pure prompt surface.
const (
	reviewHintBatchInterval = 25
	reviewHintTokenDelta    = 20_000
)

// reviewHintDue reports whether the advisory line should fire. Pure for
// testing. watermark is the estimate captured at the last hint (0 = never
// hinted, so growth is measured from session start — a resumed long session
// gets one hint on its first eligible batch, which is the point).
func reviewHintDue(batchesSince, watermark, est int64) bool {
	if batchesSince >= reviewHintBatchInterval {
		return true
	}
	return est-watermark >= reviewHintTokenDelta
}

// reviewHintLine formats the advisory line. One line by contract — the host
// appends it at the tail of the latest tool result.
func reviewHintLine(est int64) string {
	return fmt.Sprintf("[context review hint] the visible context is about %d tokens; consider reviewing compression (the compress tool, or curate_context when offered) at the next natural boundary. This is advisory only — the host will not compress on its own.", est)
}

// maybeApplyReviewHint is the executeBatch finalize hook: it may append the
// hint line to the batch's last outcome before that result is stored, so the
// line rides the fresh tail and costs zero cache invalidation (it extends new
// content, it never rewrites stored history). Gating:
//   - the switch (a.reviewHint) decided by the caller's presence on this path;
//   - only the batch's LAST index — the "latest tool result" the model reads;
//   - only a real, clean outcome — never piggybacked on blocked/error/
//     cancelled stubs (advisory lines do not ride error spam).
func (a *Agent) maybeApplyReviewHint(o *toolOutcome, last bool) {
	if !a.reviewHint || !last || o == nil || o.blocked || o.errMsg != "" || o.output == "" {
		return
	}
	batches := a.reviewHintBatches.Load()
	watermark := a.reviewHintTokenWatermark.Load()
	est := int64(a.estimatedVisibleRequestTokens(a.modelVisibleMessages()))
	if !reviewHintDue(batches, watermark, est) {
		return
	}
	o.output += "\n" + reviewHintLine(est)
	a.reviewHintBatches.Store(0)
	a.reviewHintTokenWatermark.Store(est)
	slog.Info("agent: review hint appended",
		"est_tokens", est, "batches_since", batches, "token_delta", est-watermark)
}
