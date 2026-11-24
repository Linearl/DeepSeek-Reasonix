package agent

import (
	"context"
	"fmt"
	"log/slog"

	"reasonix/internal/provider"
)

const minSummaryOutputTokens = 512

// summaryOutputBudget scales only shared/unknown-window summaries. Providers
// with an independent completion window keep the full digest cap; smaller
// shared windows reserve one quarter for a useful briefing without crowding
// every fold out of the prompt budget.
func (a *Agent) summaryOutputBudget() int {
	if contextBudgetPolicyOf(a.providerForRequest()).WindowMode == provider.ContextWindowIndependent {
		return summaryOutputMaxTokens
	}
	window := a.effectiveContextWindow()
	if window <= 0 {
		return summaryOutputMaxTokens
	}
	return min(summaryOutputMaxTokens, max(window/4, minSummaryOutputTokens))
}

// foldSummary is what compaction reports about turning a fold into a digest.
// It is populated even when the call fails, so telemetry still records how
// large the attempt was and that exactly one call was used.
type foldSummary struct {
	Text       string
	Mode       string
	RequestID  string
	Usage      *provider.Usage
	FoldTokens int
	Spans      int
	InputMode  string
	// Pointer (tl1691) is the zero-summary lane's content-hash name of the
	// folded region (fold-<sha6>); empty on the summary lane.
	Pointer string
}

// guardedSummaryInputTokens sizes the summarizer input on the same calibrated
// ruler as the request-shape estimate (任务 988 对账切片, 随 1025 落地): telemetry
// src/fold/proj must be three positions of ONE ruler or CJK sessions cannot
// reconcile the compaction line.
func (a *Agent) guardedSummaryInputTokens(msgs []provider.Message) int {
	return a.estimatedVisibleRequestTokens(msgs)
}

func (a *Agent) summaryInputBudget(instructions string) int {
	window := a.effectiveContextWindow()
	if window <= 0 {
		window = a.contextWindow
	}
	if window <= 0 {
		return 0
	}
	return max(0, window-a.summaryOutputBudget()-estimateTextTokens(compactionInstruction)-estimateTextTokens(instructions)-protocolReserveTokens)
}

// 任务 1003: preFoldSummaryInput is the mandatory pre-send check-and-fold the
// task book settles on ("发前必检必折"): before ANY summarizer request leaves,
// its input must be verified — and folded down if needed — to the destination
// context ceiling, on the SAME calibrated ruler the wire prices with
// (estimatedRequestTokens), not the coarse internal planning ruler. The leak
// it closes: the fold prefix is CHOSEN on estimateMessagesTokens (cold
// ~4-chars-per-token fallback, tool schemas and images invisible to it) yet
// the request is BILLED and REJECTED on calibrated/image/schema-aware tokens.
// Any downward drift — dense CJK content, accumulated screenshots, tool
// schemas — makes that choice under-estimate, the request crosses the window,
// the provider answers 400, and the compaction fails with in=0/out=0 (the
// 44572-requested-incident, REASONIX.md:305). Validating without folding would
// merely turn the 400 into a clean error — compaction would still fail and the
// visible view would still fall back to the full history. Folding instead
// keeps the shrink chain alive: every request is admitted, so the chain always
// converges instead of breaking mid-ladder. Image-only pressure is deliberately
// NOT elided (task 1040: eliding text cannot shed an image, so it would lower
// the ledger without lowering the payload) — the caller routes that case to the
// transcript form whose slim rendering drops images for real.
func (a *Agent) preFoldSummaryInput(fold []provider.Message, instructions string, inputMode *string) []provider.Message {
	budget := a.summaryInputBudget(instructions)
	if budget <= 0 {
		return fold
	}
	if a.estimatedRequestTokens(a.summaryRequest(fold, instructions)) <= budget {
		return fold
	}
	// An image-only over-budget fold (elideOversizedContents skips image
	// messages per task 1040, so eliding its text cannot shed the payload) is
	// routed to the transcript form: the slim rendering drops images and its
	// tool-free bounded shape actually sheds them.
	imageTokens := imageEstimateTokensOf(provider.Request{Messages: modelInputMessages(fold)})
	if imageTokens > 0 && a.estimatedRequestTokens(a.slimSummaryRequest(fold, instructions)) <= budget {
		*inputMode = SummaryInputSlim
		return fold
	}
	fold, affected := a.elideOversizedContentsForSummary(fold, budget)
	if affected > 0 {
		slog.Info("agent: summary input pre-folded to the context ceiling before send",
			"budget", budget, "elided_messages", affected,
			"model_ref", a.destinationModelRef())
	}
	return fold
}

// preFoldFullSendInput is the tl1770 拍板②③ admission for the regular lane's
// full-send summary input: the whole visible view (system + fold region +
// verbatim D tail) rides the request — the conversation request's own shape,
// so the provider's prefix-based cache covers it end to end (codex
// compact.rs: history + appended instruction). When that request exceeds the
// safe prompt budget, codex's over-window retry applies (compact.rs L311
// "Trim from the beginning to preserve cache (prefix-based) and keep recent
// messages intact"): whole replay units drop from the OLDEST end until the
// request fits — never a mid-body elision, so the digest input stays
// whole-message shaped and the newest content survives intact. An image-only
// over-budget view routes to the transcript form instead (任务 1040
// discipline: eliding text cannot shed an image; the slim form drops them for
// real). inputMode may be flipped to SummaryInputSlim; the view itself is
// never reshaped beyond the oldest-unit trim.
func (a *Agent) preFoldFullSendInput(fullView []provider.Message, instructions string, inputMode *string) []provider.Message {
	budget, enforced := a.safeSummaryPromptTokenLimit()
	if !enforced {
		budget = a.summaryInputBudget(instructions)
	}
	if budget <= 0 {
		return fullView
	}
	if a.estimatedRequestTokens(a.summaryRequest(fullView, instructions)) <= budget {
		return fullView
	}
	imageTokens := imageEstimateTokensOf(provider.Request{Messages: modelInputMessages(fullView)})
	if imageTokens > 0 && a.estimatedRequestTokens(a.slimSummaryRequest(fullView, instructions)) <= budget {
		*inputMode = SummaryInputSlim
		return fullView
	}
	trimmed, dropped := a.trimSummaryInputFromHead(fullView, instructions, budget)
	if dropped > 0 {
		slog.Info("agent: full-send summary input trimmed from the head to the safe budget",
			"dropped_messages", dropped, "kept_messages", len(trimmed),
			"budget", budget, "model_ref", a.destinationModelRef())
	}
	return trimmed
}

// trimSummaryInputFromHead drops whole oldest replay units from a full-send
// summary input until its request prices within budget (tl1770 拍板③, codex
// compact.rs remove_first_item loop). Protected from the trim: the system
// prefix, every compaction summary (the new digest's merge source — dropping
// one would orphan the facts it accumulated), and at least the newest
// trimmable unit (codex keeps recent messages intact; turn_input_len > 1
// floor). The drop count is binary-searched against the calibrated
// whole-request pricer — per-message pricing is NOT additive across the CJK
// excess calibration, so every probe verifies with the real ruler. When even
// the maximally trimmed form exceeds the budget it goes out unchanged: the
// provider's own overflow lane owns it from there, exactly like codex's
// single-item floor.
func (a *Agent) trimSummaryInputFromHead(msgs []provider.Message, instructions string, budget int) ([]provider.Message, int) {
	head := a.pinnedPrefixLen(msgs)
	units := extractMessageUnits(msgs)
	type dropSpan struct{ lo, hi int }
	cands := make([]dropSpan, 0, len(units))
	for _, u := range units {
		if u.lo < head {
			continue
		}
		protected := false
		for i := u.lo; i < u.hi; i++ {
			if isCompactionSummary(msgs[i]) {
				protected = true
				break
			}
		}
		if !protected {
			cands = append(cands, dropSpan{lo: u.lo, hi: u.hi})
		}
	}
	// At most len(cands)-1 units may drop: the newest trimmable unit always
	// stays (codex: keep recent messages intact).
	price := func(drop int) int {
		if drop == 0 {
			return a.estimatedRequestTokens(a.summaryRequest(msgs, instructions))
		}
		cut := make([]bool, len(msgs))
		for _, c := range cands[:drop] {
			for i := c.lo; i < c.hi; i++ {
				cut[i] = true
			}
		}
		kept := make([]provider.Message, 0, len(msgs))
		for i, m := range msgs {
			if cut[i] {
				continue
			}
			kept = append(kept, m)
		}
		return a.estimatedRequestTokens(a.summaryRequest(kept, instructions))
	}
	if len(cands) <= 1 || price(0) <= budget {
		return msgs, 0
	}
	lo, hi := 0, len(cands)-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		if price(mid) <= budget {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	drop := lo
	if price(drop) > budget {
		// Even the floor form (system + protected summaries + newest unit)
		// exceeds the budget: send the maximally trimmed form anyway — codex
		// sends its single-item floor and lets the provider's verdict land.
		drop = len(cands) - 1
	}
	cut := make([]bool, len(msgs))
	for _, c := range cands[:drop] {
		for i := c.lo; i < c.hi; i++ {
			cut[i] = true
		}
	}
	kept := make([]provider.Message, 0, len(msgs))
	dropped := 0
	for i, m := range msgs {
		if cut[i] {
			dropped++
			continue
		}
		kept = append(kept, m)
	}
	return kept, dropped
}

// foldToSummary turns a fold region into one digest with exactly one provider
// request. Pressure-time tool pruning is durable and happens before this call;
// the summary request never performs a private second transformation.
func (a *Agent) foldToSummary(ctx context.Context, fold []provider.Message, instructions string) (foldSummary, error) {
	return a.foldToSummaryMode(ctx, fold, instructions, SummaryInputCachePrefix, nil)
}

// foldToSummaryMode turns a fold region into one digest with exactly one
// provider request. fullSend (tl1770 拍板②) overrides the region the request
// carries: the regular lane sends the whole view instead of the fold region —
// it arrives already admitted by preFoldFullSendInput at the planning site.
// FoldTokens keeps reporting the FOLD region on the calibrated ruler (the
// planning number the gate, the done line and the ladder read); the billed
// full-send input shows up in the request's own cache readout (tl1770 观测行)
// and the usage telemetry, never as a third ruler here.
func (a *Agent) foldToSummaryMode(ctx context.Context, fold []provider.Message, instructions, inputMode string, fullSend []provider.Message) (foldSummary, error) {
	// 任务 988 对账切片（随 1025 落地）：FoldTokens 与 src/proj 位同用校准尺
	// （estimatedVisibleRequestTokens），静态 estimateMessagesTokens 只留内部
	// 规划预算——否则 telemetry 一行里 fold/src/proj 三把尺，中文会话对账必歪。
	res := foldSummary{Mode: CompactionModeSummarized, Spans: 1, FoldTokens: a.guardedSummaryInputTokens(fold), InputMode: inputMode}
	if len(fullSend) > 0 {
		if inputMode == SummaryInputSlim {
			summary, usage, err := a.summarizeTranscript(ctx, fullSend, instructions)
			res.Text, res.Usage = summary, usage
			return res, err
		}
		return a.singleCallSummary(ctx, res, fullSend, instructions)
	}
	// 任务 1003: the mandatory pre-send check-and-fold. Only the replay form is
	// folded here — the transcript form is a distinct input shape (slimmed
	// bodies, no schemas) whose own rendering already bounds it, and its
	// request must stay exactly the shape this mode names.
	if inputMode != SummaryInputSlim {
		fold = a.preFoldSummaryInput(fold, instructions, &inputMode)
		res.FoldTokens = a.guardedSummaryInputTokens(fold)
	}
	if inputMode == SummaryInputSlim {
		summary, usage, err := a.summarizeTranscript(ctx, fold, instructions)
		res.Text, res.Usage = summary, usage
		return res, err
	}
	return a.singleCallSummary(ctx, res, fold, instructions)
}

// elideOversizedContentsForSummary folds the fold region down to the summary
// input budget using the SAME calibrated ruler the request is priced with. The
// threshold is the average fold size (budget / message count), so a fold of
// many mid-size messages — the incident's shape, where each individual
// message sits under the visible-view rung's target/16 cut but the region
// together crosses the window — has every over-average message priced down
// instead of being sent whole. Compaction summaries and pinned revisions stay
// intact (same exclusion discipline as the visible-view rung); when elision
// alone cannot land the request, whole oldest-first replay units are dropped
// next, mirroring truncateView's middle rung. The canonical transcript is
// never touched — this works on a copy.
func (a *Agent) elideOversizedContentsForSummary(fold []provider.Message, budget int) ([]provider.Message, int) {
	if budget <= 0 || len(fold) == 0 {
		return fold, 0
	}
	work := append([]provider.Message(nil), fold...)
	if a.estimatedRequestTokens(a.summaryRequest(work, "")) <= budget {
		return fold, 0
	}
	head := a.pinnedPrefixLen(work)
	// Threshold = average fold-message size on the budget: anything over the
	// average is cut to head+tail until the region fits as a whole. The cut
	// length is a small fraction of that average (~1/8) — enough to shed most
	// of a mid-size message while keeping a useful head, and small enough that
	// a message around the average (the incident's 2000-char ones) is not
	// skipped whole by the head+tail guard.
	threshold := max(1, budget/len(work))
	cutChars := max(1, threshold/contentElideOversizedDiv)
	work, affected := a.elideOversizedContentsAt(work, head, budget, threshold, cutChars)
	if a.estimatedRequestTokens(a.summaryRequest(work, "")) <= budget {
		return work, affected
	}
	// Rung 2: drop whole replay units oldest-first, exactly like truncateView's
	// middle rung, until the fold fits. Compaction summaries and pinned
	// revisions survive; only ordinary history is dropped.
	dropped, out := a.dropOldestFoldUnits(work, budget)
	affected += dropped
	if a.estimatedRequestTokens(a.summaryRequest(out, "")) <= budget {
		return out, affected
	}
	// Rung 3: the drop may have exposed the next tier of oversized carriers —
	// elide once more with a halved threshold before giving up.
	out, more := a.elideOversizedContentsAt(out, a.pinnedPrefixLen(out), budget, max(1, threshold/2), cutChars)
	affected += more
	return out, affected
}

// dropOldestFoldUnits removes whole replay units from the oldest end of a fold
// until the fold's summary request fits the budget. It mirrors truncateView's
// middle rung, with the fold region as the unit container (no pinned/system
// prefix — summaryRequest re-attaches it) and compaction digests / pinned
// revisions surviving one marker, exactly like the visible-view drop.
func (a *Agent) dropOldestFoldUnits(fold []provider.Message, budget int) (int, []provider.Message) {
	if len(fold) == 0 {
		return 0, fold
	}
	units := extractMessageUnits(fold)
	if len(units) == 0 {
		return 0, fold
	}
	carriers := sessionContextCarrierSet(fold)
	// Decide how many oldest units to drop by walking the fold and measuring
	// the request on the pruned candidate each round (the units are few and
	// the ruler is the same one the send path uses).
	keep := len(units)
	dropped := 0
	for keep > 0 {
		candidate, n := pruneFoldToUnits(fold, units, carriers, keep)
		if n == 0 {
			break
		}
		if a.estimatedRequestTokens(a.summaryRequest(candidate, "")) <= budget {
			dropped = n
			break
		}
		keep--
	}
	if dropped == 0 {
		return 0, fold
	}
	out, _ := pruneFoldToUnits(fold, units, carriers, keep)
	// Keep the ledger marker honest: name how many messages were folded away.
	marker := HostGeneratedUserMessage(fmt.Sprintf(truncatedHistoryMarker, dropped))
	return dropped, append([]provider.Message{marker}, out...)
}

// pruneFoldToUnits keeps the LAST keep replay units (plus every protected
// message) and drops the rest. It returns the pruned fold and how many ordinary
// messages were dropped.
func pruneFoldToUnits(fold []provider.Message, units []extractMessageSpan, carriers map[int]bool, keep int) ([]provider.Message, int) {
	if keep >= len(units) {
		return fold, 0
	}
	drop := make(map[int]bool)
	// units[:keep] is the OLDEST end — those are what gets dropped.
	for _, u := range units[:keep] {
		for i := u.lo; i < u.hi; i++ {
			drop[i] = true
		}
	}
	out := make([]provider.Message, 0, len(fold))
	dropped := 0
	for i, m := range fold {
		if drop[i] && !carriers[i] && !isCompactionSummary(m) && !IsPinnedContextRevision(m) {
			dropped++
			continue
		}
		out = append(out, m)
	}
	return out, dropped
}

func (a *Agent) singleCallSummary(ctx context.Context, res foldSummary, fold []provider.Message, instructions string) (foldSummary, error) {
	summary, mode, usage, reqID, err := a.runCompactionSummary(ctx, fold, instructions)
	res.Text, res.Mode, res.Usage, res.RequestID = summary, mode, usage, reqID
	return res, err
}

func (a *Agent) foldSummaryWithTelemetry(ctx context.Context, trigger string, fold []provider.Message, instructions string, sourceTokens int, inputMode string, fullSend []provider.Message) (foldSummary, CompactionTelemetry, error) {
	res, err := a.foldToSummaryMode(ctx, fold, instructions, inputMode, fullSend)
	tele := compactionTelemetryFromSummary(trigger, a.CacheState(), sourceTokens, res)
	if err != nil {
		tele.Error = err.Error()
	}
	return res, tele, err
}
