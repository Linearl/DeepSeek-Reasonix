package agent

import (
	"fmt"

	"reasonix/internal/provider"
)

// summaryPlanMarginRatio is the planning headroom left under the window for
// estimator error. The fixed protocol reserve alone is under 1% of a 1M
// window, thinner than the tokenizer drift real sessions show (#9818).
const summaryPlanMarginRatio = 0.05

func summaryPlanReserve(window int) int {
	return max(protocolReserveTokens, int(float64(window)*summaryPlanMarginRatio))
}

// tl1587: the segment bound for one automatic summary request. The #9572 safe
// prefix caps the summary input at (window - output - 5%) — at a 1M window
// that is ~94% of the window in ONE request, and the 20261114 包92 incident
// (31-line failure chain: output truncation → empty provider response →
// transport failure, then fail-streak → Diet → projection fallback → oldest-
// history trim) showed a near-window prefill fails in every shape at once.
// A summary request is auxiliary work: it must ride in a SEGMENT, sized as a
// quarter of the window — the same one-quarter share the summary output budget
// reserves — with a floor that keeps every window at or below 4× the floor on
// today's exact paths (the segment regime engages only past a 64K window, so
// ordinary sessions and the small-window test contract are untouched).
const (
	summarySegmentInputRatio       = 0.25
	summarySegmentInputFloorTokens = 16 * 1024
)

// summarySegmentInputBudget reports the segment input ceiling and whether the
// segment regime is engaged at all. Unknown gateways conservatively honor the
// configured or learned window exactly like safeSummaryPromptTokenLimit;
// explicitly independent providers keep the full fold. enforced=false below
// the floor: a small window's quarter stays under today's safe cap anyway.
func (a *Agent) summarySegmentInputBudget() (budget int, enforced bool) {
	window := a.effectiveContextWindow()
	if window <= 0 || contextBudgetPolicyOf(a.providerForRequest()).WindowMode == provider.ContextWindowIndependent {
		return 0, false
	}
	budget = max(summarySegmentInputFloorTokens, int(float64(window)*summarySegmentInputRatio))
	return budget, budget > summarySegmentInputFloorTokens
}

// maximumSafeSummaryPrefixEnd returns the largest balanced contiguous prefix
// whose exact summary request leaves the collector's minimum output budget.
// The remaining middle and tail stay verbatim in the projection.
func (a *Agent) maximumSafeSummaryPrefixEnd(msgs []provider.Message, head, end int, instructions string) int {
	if head < 0 || end <= head || end > len(msgs) {
		return end
	}
	maxPromptTokens, enforce := a.safeSummaryPromptTokenLimit()
	if !enforce {
		return end
	}
	return a.summaryPrefixEndWithinBudget(msgs, head, end, instructions, maxPromptTokens)
}

// boundedSummaryPrefixEnd is the tl1587 planning cap: the fold prefix is bounded
// by the SEGMENT budget first, so a full-window session's summary request rides
// in a quarter-window segment instead of a near-window single prefill. When the
// segment cannot admit any balanced prefix (a giant first message), the #9572
// safe budget stands — the segment cap tightens the oversized regime, it never
// blocks a fold that today's cap admits.
func (a *Agent) boundedSummaryPrefixEnd(msgs []provider.Message, head, end int, instructions string) int {
	if head < 0 || end <= head || end > len(msgs) {
		return end
	}
	safeMax, enforce := a.safeSummaryPromptTokenLimit()
	if !enforce {
		return end
	}
	if segMax, segEnforced := a.summarySegmentInputBudget(); segEnforced && segMax < safeMax {
		if segEnd := a.summaryPrefixEndWithinBudget(msgs, head, end, instructions, segMax); segEnd > head {
			return segEnd
		}
	}
	return a.summaryPrefixEndWithinBudget(msgs, head, end, instructions, safeMax)
}

// summaryPrefixEndWithinBudget returns the largest balanced contiguous prefix
// in [head:end] whose exact summary request prices within maxPromptTokens.
func (a *Agent) summaryPrefixEndWithinBudget(msgs []provider.Message, head, end int, instructions string, maxPromptTokens int) int {
	if maxPromptTokens <= 0 {
		return head
	}
	fits := func(candidate int) bool {
		fold, _ := withoutPinnedContextRevisions(msgs[head:candidate])
		request := a.summaryRequest(fold, instructions)
		return a.estimatedRequestTokens(request) <= maxPromptTokens
	}
	if fits(end) {
		return end
	}

	low, high, best := head+1, end-1, head
	for low <= high {
		mid := low + (high-low)/2
		if fits(mid) {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	// A tail beginning with a tool result would split it from the assistant
	// tool-call message. Move the fold boundary back across the whole result
	// group; the assistant call and all of its results then remain together.
	for best > head && best < len(msgs) && msgs[best].Role == provider.RoleTool {
		best--
	}
	return best
}

// safeSummaryPromptTokenLimit is shared by prefix planning and the final
// post-extension guard. Unknown gateways conservatively honor the configured
// or learned window; explicitly independent providers retain the full fold.
func (a *Agent) safeSummaryPromptTokenLimit() (int, bool) {
	window := a.effectiveContextWindow()
	if window <= 0 || contextBudgetPolicyOf(a.providerForRequest()).WindowMode == provider.ContextWindowIndependent {
		return 0, false
	}
	return window - a.summaryOutputBudget() - summaryPlanReserve(window), true
}

// validateSafeSummaryRequest guards the final fold in the request form that
// will actually be sent.
func (a *Agent) validateSafeSummaryRequest(fold []provider.Message, instructions string, slim bool) error {
	maxPromptTokens, enforce := a.safeSummaryPromptTokenLimit()
	if !enforce {
		return nil
	}
	request := a.summaryRequest(fold, instructions)
	if slim {
		request = a.slimSummaryRequest(fold, instructions)
	}
	requestTokens := a.estimatedRequestTokens(request)
	if maxPromptTokens <= 0 || requestTokens > maxPromptTokens {
		return fmt.Errorf("%w: prepared summary request (%d tokens) exceeds safe prompt budget (%d)",
			errCheckpointRejected, requestTokens, maxPromptTokens)
	}
	return nil
}
