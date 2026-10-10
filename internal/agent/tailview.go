package agent

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"reasonix/internal/provider"
)

// 任务719: the projection-to-view fallback used to be binary — a valid
// projection served the spliced view, and everything else served the FULL
// canonical transcript. On a large session that lost its projection (restart
// content drift, lineage key change, sidecar drop) the very next request
// carried e.g. 2.17M tokens into a 1M window: the context-limit loop observed
// on 2026-10-10. Compaction eventually restores a projection, but until it
// lands every request overflows.
//
// The bounded recent-tail view is the missing middle rung: when the projection
// is unusable AND the canonical transcript outgrows the hard input ceiling,
// the model-visible view degrades to a recent-tail window (pinned head + a
// truncation marker + carried digests + the newest replay-safe units). The
// view stays wire-legal, the context share stays far below the window, and
// compaction still rebuilds a real projection — the degraded window is a
// rolling view, never an installed state.

// tailViewTargetShare bounds the degraded tail view at this fraction of the
// model window: well below the 0.80 compaction trigger so the view keeps
// headroom for appends, and far below the hard input ceiling so no request
// overflows while the projection rebuilds.
const tailViewTargetShare = 0.60

// tailViewState caches the evaluated view per transcript version. The
// canonical snapshot is immutable per version, so the view is a pure function
// of it; ContextUsedTokens recomputes far more often than the transcript
// moves, and on a multi-megabyte session the estimate walk is not free.
// view == nil with known == true means the full view is legal (below the hard
// ceiling) — the cached answer is "no degradation needed".
type tailViewState struct {
	mu       sync.Mutex
	version  uint64
	known    bool
	view     []provider.Message
	warnedAt time.Time
}

func (s *tailViewState) load(version uint64) ([]provider.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.known || s.version != version {
		return nil, false
	}
	return s.view, true
}

func (s *tailViewState) store(version uint64, view []provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version, s.known, s.view = version, true, view
}

// clear forgets the evaluation: the projection is authoritative again (or the
// lineage changed), so the next invalid-projection pass starts fresh.
func (s *tailViewState) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.known, s.view, s.version = false, nil, 0
}

// active reports whether the currently cached evaluation is a degraded tail
// view — the signal prepareOnce uses to force the compaction pass even though
// the bounded view itself sits below the fold trigger.
func (s *tailViewState) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.known && s.view != nil
}

// tailViewTarget is the content budget of the degraded view.
func (a *Agent) tailViewTarget() int {
	window := a.effectiveContextWindow()
	if a == nil || window <= 0 {
		return 0
	}
	return max(1, int(float64(window)*tailViewTargetShare))
}

// boundedTailView returns the model-visible view for a session whose
// projection is unusable: the full canonical transcript while it fits under
// the hard input ceiling, a bounded recent-tail window once it does not. The
// evaluation is cached per transcript version.
//
// Sizing uses the calibrated request estimate (estimatedVisibleRequestTokens),
// the same estimator hardInputCeiling and the maintenance checks speak: the
// plain estimateMessagesTokens walk over-counts ASCII text ~4x (one token per
// rune), which would degrade a perfectly-fitting English session.
func (a *Agent) boundedTailView(msgs []provider.Message, version uint64) []provider.Message {
	if a == nil || len(msgs) == 0 {
		return msgs
	}
	if view, ok := a.sess.tailView.load(version); ok {
		if view != nil {
			return view
		}
		return msgs
	}
	hard := a.hardInputCeiling()
	total := a.estimatedVisibleRequestTokens(msgs)
	if hard <= 0 || total < hard {
		// The full view is legal — exactly the pre-719 shape, cached so the
		// estimate is not re-walked on every gauge refresh.
		a.sess.tailView.store(version, nil)
		return msgs
	}
	view, dropped := a.tailWindowView(msgs, a.tailViewTarget())
	if len(view) == len(msgs) {
		// Nothing could be bounded (the newest unit alone fills the window):
		// leave the view unchanged and let the overflow chain own it.
		a.sess.tailView.store(version, nil)
		return msgs
	}
	a.sess.tailView.store(version, view)
	a.warnTailViewDegrade(total, a.estimatedVisibleRequestTokens(view), len(msgs), dropped)
	return view
}

// tailWindowView builds the bounded view: the pinned head, one truncation
// marker, the carried digests from the hidden region, then the newest
// replay-safe units that fit the target budget. Unit boundaries keep every
// tool call with its results, so the cut can never orphan a pairing. Returns
// the view unchanged when the whole transcript already fits. All sizing runs
// through the calibrated request estimator — the same voice as the target and
// the hard ceiling.
func (a *Agent) tailWindowView(msgs []provider.Message, target int) ([]provider.Message, int) {
	if target <= 0 || len(msgs) == 0 {
		return msgs, 0
	}
	head := a.pinnedPrefixLen(msgs)
	spanTokens := func(lo, hi int) int {
		return a.estimatedPromptTokens(msgs[lo:hi])
	}
	headTokens := spanTokens(0, head)
	markerMsg := HostGeneratedUserMessage(fmt.Sprintf(truncatedHistoryMarker, 0))
	markerTokens := a.estimatedPromptTokens([]provider.Message{markerMsg})
	budget := target - headTokens - markerTokens
	if budget < 1 {
		budget = 1
	}
	// Newest-first unit accumulation. The newest unit is always kept even when
	// it alone busts the budget — a single giant message cannot be split at
	// view level, and the overflow chain already owns that shape.
	units := extractMessageUnits(msgs)
	keepFrom := len(msgs)
	acc := 0
	for i := len(units) - 1; i >= 0; i-- {
		u := units[i]
		if u.lo < head {
			break
		}
		t := spanTokens(u.lo, u.hi)
		if acc > 0 && acc+t > budget {
			break
		}
		acc += t
		keepFrom = u.lo
	}
	if keepFrom <= head {
		return msgs, 0
	}
	// Carry the rolling summary, pinned revisions, and the latest session-context
	// snapshot across the cut, matching dropOldestUnits' retention policy: state
	// built before the boundary must survive it. Orphan tool results are never
	// carried — unit boundaries already pair them with their calls.
	latest := latestSessionContextIndex(msgs)
	var kept []provider.Message
	for i := head; i < keepFrom; i++ {
		if msgs[i].Role == provider.RoleTool {
			continue
		}
		if i == latest || isCompactionSummary(msgs[i]) || IsPinnedContextRevision(msgs[i]) {
			kept = append(kept, msgs[i])
		}
	}
	dropped := keepFrom - head - len(kept)
	out := make([]provider.Message, 0, head+1+len(kept)+len(msgs)-keepFrom)
	out = append(out, msgs[:head]...)
	out = append(out, HostGeneratedUserMessage(fmt.Sprintf(truncatedHistoryMarker, dropped)))
	out = append(out, kept...)
	out = append(out, msgs[keepFrom:]...)
	return out, dropped
}

// warnTailViewDegrade names the degradation once per cooldown so the episode
// is diagnosable without spamming every gauge refresh: canonical size, the
// bounded view it became, and how much was hidden.
func (a *Agent) warnTailViewDegrade(canonicalTokens, viewTokens, messages, dropped int) {
	s := &a.sess.tailView
	s.mu.Lock()
	now := time.Now()
	if !s.warnedAt.IsZero() && now.Sub(s.warnedAt) < projectionFallbackWarnCooldown {
		s.mu.Unlock()
		return
	}
	s.warnedAt = now
	s.mu.Unlock()
	slog.Warn("agent: context view degraded to a bounded recent-tail window",
		"canonical_tokens", canonicalTokens, "view_tokens", viewTokens,
		"messages", messages, "dropped", dropped, "window", a.effectiveContextWindow())
}
