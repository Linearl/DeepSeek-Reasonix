package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

const (
	maxCompressAnchorBytes = 512
	maxCompressFocusBytes  = 2000
)

var errCompressStaleContext = errors.New("compress: conversation changed while compression was running; retry with the current context")

// CompressContext implements the context-bound compress tool. It resolves the
// anchor against the current model-visible view and installs a projection only;
// the canonical transcript and checkpoint lineage remain untouched.
// explicitCompressFloorRatio is how full the context must be before a
// model-driven fold is allowed (task 60, point 2). A fold only pays for itself
// once re-reading the history costs more than the detail it discards, so the
// floor tracks the hard threshold instead of being a fixed size: at the default
// ratio this is half the compaction threshold.
const explicitCompressFloorRatio = 0.5

// minExplicitFoldInterval is the minimum gap between two model-driven folds.
// The hard-threshold path is deliberately not rate-limited: it exists to keep a
// session inside its window, so it must always be able to fire.
// var, not const: tests that exercise consecutive folds collapse the window.
var minExplicitFoldInterval = 10 * time.Minute

// foldCooldownInterval resolves the effective fold cooldown (task 318.2): the
// configured minutes when experimental_proactive_compact is on (live read),
// otherwise the hard-coded default — the off path is today's behavior.
func foldCooldownInterval() time.Duration {
	if minutes := config.ProactiveCompactCooldownLive(); minutes > 0 {
		return time.Duration(minutes) * time.Minute
	}
	return minExplicitFoldInterval
}

// visibleContextTokens estimates what the visible transcript costs in prompt
// tokens, using the same calibration the compaction path uses.
func (a *Agent) visibleContextTokens(snap explicitCompressionSnapshot) int {
	chars := charsOfMessages(snap.visible)
	if chars <= 0 {
		return 0
	}
	if perChar := a.tokPerChar(); perChar > 0 {
		return int(float64(chars) / perChar)
	}
	return chars
}

func (a *Agent) CompressContext(ctx context.Context, req tool.CompressRequest) (tool.CompressResult, error) {
	direction := strings.TrimSpace(req.Direction)
	anchor := strings.TrimSpace(req.Anchor)
	focus := strings.TrimSpace(req.Focus)
	if direction != "before" && direction != "after" {
		return tool.CompressResult{}, fmt.Errorf("compress: direction must be before or after")
	}
	if anchor == "" {
		return tool.CompressResult{}, fmt.Errorf("compress: anchor must not be empty")
	}
	if len(anchor) > maxCompressAnchorBytes {
		return tool.CompressResult{}, fmt.Errorf("compress: anchor exceeds %d bytes", maxCompressAnchorBytes)
	}
	if len(focus) > maxCompressFocusBytes {
		return tool.CompressResult{}, fmt.Errorf("compress: focus exceeds %d bytes", maxCompressFocusBytes)
	}

	// A fold rewrites the prompt prefix, so it costs the cache for every later
	// turn: hold model-driven folds to one per interval (task 60, point 2).
	// Task 318.2: with the experiment on, the configured cooldown minutes
	// apply (live read, no restart); with it off — the default — the
	// hard-coded interval below governs exactly as before.
	if last := a.lastExplicitFoldAt.Load(); a.traceAsState && last != 0 {
		interval := foldCooldownInterval()
		if elapsed := time.Since(time.Unix(0, last)); elapsed < interval {
			wait := (interval - elapsed).Round(time.Second)
			return tool.CompressResult{
				Status: "rejected",
				Reason: fmt.Sprintf("a fold already ran %s ago; folding again within %s would keep invalidating the prompt cache, try again in about %s", elapsed.Round(time.Second), interval, wait),
			}, nil
		}
	}

	snap := a.snapshotExplicitCompression()

	// Guard: refuse a fold on a short context. Re-reading a short history is
	// cheaper than losing its detail, and this is the failure mode a model
	// reaching for the tool on its own will hit (task 60, point 2).
	if threshold := a.compactTrigger(); a.traceAsState && threshold > 0 {
		if used := a.visibleContextTokens(snap); used > 0 && used < int(float64(threshold)*explicitCompressFloorRatio) {
			return tool.CompressResult{
				Status: "rejected",
				Reason: fmt.Sprintf("context is about %d tokens, below the %d-token floor for folding; read the material directly instead of compressing", used, int(float64(threshold)*explicitCompressFloorRatio)),
			}, nil
		}
	}

	matches := make([]int, 0, 2)
	for i, msg := range snap.visible {
		if !compressAnchorCandidate(msg) {
			continue
		}
		if strings.Contains(UserMessageText(msg), anchor) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 0 {
		return tool.CompressResult{}, fmt.Errorf("compress: anchor did not match any current user message; retry with an exact excerpt from a visible user turn")
	}
	if len(matches) > 1 {
		return tool.CompressResult{}, fmt.Errorf("compress: anchor matched %d user messages; retry with a longer unique excerpt", len(matches))
	}

	result, err := a.compressVisibleRange(ctx, snap, CompactionTriggerTool, direction, matches[0], anchorPreview(UserMessageText(snap.visible[matches[0]])), focus)
	if err == nil && result.Status != "rejected" {
		a.lastExplicitFoldAt.Store(time.Now().UnixNano())
	}
	return result, err
}

type explicitCompressionSnapshot struct {
	canonical         []provider.Message
	visible           []provider.Message
	transcriptVersion uint64
	coveredHash       string
	projectionVersion uint64
	generation        uint64
	promptCacheKey    string
}

func (a *Agent) snapshotExplicitCompression() explicitCompressionSnapshot {
	canonical, version := a.sess.conversation.snapshotMessagesVersion()
	cacheKey := a.currentPromptCacheKey()
	a.sess.compactionMu.Lock()
	state := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	visible := canonical
	if projectionValid(state, canonical) {
		if projected := modelVisibleFromProjection(state.Projection, canonical); len(projected) > 0 {
			visible = projected
		}
	}
	return explicitCompressionSnapshot{
		canonical:         canonical,
		visible:           compressionVisibleMessages(visible),
		transcriptVersion: version,
		coveredHash:       coveredPrefixHash(canonical, len(canonical)),
		projectionVersion: state.Projection.ProjectionVersion,
		generation:        state.Generation,
		promptCacheKey:    cacheKey,
	}
}

func compressionVisibleMessages(msgs []provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(msgs)+1)
	for _, msg := range msgs {
		if !msg.LocalOnly {
			summary, user, split := splitLegacyCoalescedSummary(msg)
			if split {
				out = append(out, summary, user)
			} else {
				out = append(out, msg)
			}
		}
	}
	return out
}

// Older schema-v1 sidecars may have persisted a strict-role merge of the
// summary and its following user turn. Split that legacy shape for range
// planning; new sidecars keep the logical messages separate and coalesce only
// on the provider request copy.
func splitLegacyCoalescedSummary(msg provider.Message) (provider.Message, provider.Message, bool) {
	if !isCompactionSummary(msg) {
		return provider.Message{}, provider.Message{}, false
	}
	separator := summaryTagClose + "\n\n"
	i := strings.Index(msg.Content, separator)
	if i < 0 || i+len(separator) >= len(msg.Content) {
		return provider.Message{}, provider.Message{}, false
	}
	summary := msg
	summary.Origin = provider.MessageOriginHost
	summary.Content = msg.Content[:i+len(summaryTagClose)]
	summary.RawContent = ""
	summary.Images = nil
	summary.ToolCalls = nil
	summary.ResponsesItems = nil
	summary.ServerSearch = nil
	summary.CreatedAt = 0
	user := msg
	// The legacy coalesced record did not retain the following turn's
	// provenance. Empty keeps old-session fallback available instead of
	// asserting that an old host continuation was user-authored.
	user.Origin = ""
	user.Content = msg.Content[i+len(separator):]
	user.RawContent = ""
	return summary, user, true
}

func compressAnchorCandidate(msg provider.Message) bool {
	if msg.Role != provider.RoleUser || msg.LocalOnly || isCompactionSummary(msg) {
		return false
	}
	return IsUserAuthoredTurnMessage(msg)
}

func anchorPreview(text string) string {
	return truncatePreview(previewProse(text))
}

type visibleCompressionPlan struct {
	result    tool.CompressResult
	foldMask  []bool
	dropMask  []bool
	fold      []provider.Message
	firstFold int
}

type preparedVisibleCompression struct {
	fold         []provider.Message
	instructions string
	inputMode    string
}

func (a *Agent) compressVisibleRange(
	ctx context.Context,
	snap explicitCompressionSnapshot,
	trigger string,
	direction string,
	anchorIndex int,
	preview string,
	instructions string,
) (tool.CompressResult, error) {
	a.sess.compactionRunMu.Lock()
	defer a.sess.compactionRunMu.Unlock()
	if !a.explicitCompressionSnapshotCurrent(snap) {
		return tool.CompressResult{}, errCompressStaleContext
	}
	plan, ok := a.planVisibleCompression(snap, direction, anchorIndex, preview)
	if !ok {
		return plan.result, nil
	}
	result := plan.result
	inputMode := SummaryInputNonPrefix
	if direction == "before" && foldMatchesVisiblePrefix(snap.visible, plan.fold) {
		inputMode = SummaryInputCachePrefix
	}

	a.svc.sink.Emit(event.Event{Kind: event.CompactionStarted, Compaction: event.Compaction{Trigger: trigger}})
	// 任务 556: meter the pass from Started to every exit (defer covers Done,
	// aborts, panics) so live readout events never outlive the pending card.
	a.compactionLiveBegin(trigger)
	defer a.compactionLiveStop()
	prepared, reason, err := a.prepareVisibleCompression(ctx, trigger, plan.fold, instructions, inputMode)
	if err != nil {
		a.emitCompactionAborted(trigger)
		return tool.CompressResult{}, err
	}
	if reason != "" {
		a.emitCompactionAborted(trigger)
		result.Reason = reason
		return result, nil
	}

	res, err := a.foldToSummaryMode(ctx, prepared.fold, prepared.instructions, prepared.inputMode)
	summary := res.Text
	tele := compactionTelemetryFromSummary(trigger, a.CacheState(), result.SourceTokens, res)
	if err != nil {
		tele.Error = err.Error()
		a.emitCompactionTelemetry(tele)
		a.emitCompactionAborted(trigger)
		return tool.CompressResult{}, err
	}
	summary, err = a.interceptCompactionComplete(ctx, summary)
	if err != nil {
		tele.Error = err.Error()
		a.emitCompactionTelemetry(tele)
		a.emitCompactionAborted(trigger)
		return tool.CompressResult{}, err
	}

	projection := buildVisibleCompressionProjection(snap.visible, plan, summary)
	projection, pinnedCheckpoint, err := rebasePinnedContextProjection(projection, snap.canonical, len(snap.canonical))
	if err != nil {
		a.emitCompactionAborted(trigger)
		return tool.CompressResult{}, err
	}
	projectionTokens := a.estimatedVisibleRequestTokens(projection)
	tele.ProjectionTokens = projectionTokens
	result.Messages = len(plan.fold)
	result.ProjectionTokens = projectionTokens
	result.Mode = res.Mode
	if projectionTokens >= result.SourceTokens {
		if pinnedCheckpoint {
			result.Reason = "pinned-context-too-large: checkpoint prevents compaction from reducing context"
			a.emitCompactionTelemetry(tele)
			a.emitCompactionAborted(trigger)
			return result, nil
		}
		result.Reason = "compressed context would not be smaller"
		a.emitCompactionTelemetry(tele)
		a.emitCompactionAborted(trigger)
		return result, nil
	}

	inputHash := providerVisibleFingerprint(modelInputMessages(snap.visible))
	outputHash := providerVisibleFingerprint(projection)
	state, err := a.commitSummaryProjection(summaryProjectionCommit{
		canonical: snap.canonical, fold: prepared.fold, projected: projection, result: res,
		transcriptVersion: snap.transcriptVersion, projectionVersion: snap.projectionVersion, generation: snap.generation,
		activeTurn: a.activeTurnCreatedAt.Load(), trigger: trigger, summary: summary,
		inputHash: inputHash, outputHash: outputHash, sourceTokens: result.SourceTokens, projectionTokens: projectionTokens,
		covered: len(snap.canonical),
	})
	if err != nil {
		if errors.Is(err, errCompressStaleContext) {
			tele.Error = err.Error()
			a.emitCompactionTelemetry(tele)
		}
		a.emitCompactionAborted(trigger)
		return tool.CompressResult{}, err
	}
	a.emitCompactionTelemetry(tele)
	// The transcript was rewritten, so repeating a pre-compaction tool result is
	// no longer a duplicate (#39 / upstream #10023).
	a.turn.loop.clearResultFingerprints()
	a.svc.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{
		Trigger: trigger, Messages: len(plan.fold), Summary: summary, Archive: state.LastReceipt.Archive,
	}})
	// Task 317: the explicit compress tool is the manual "second compaction"
	// a user reaches for after a network error — its zero-hit round must
	// attribute its miss exactly like the automatic fold does.
	a.logCompactionMissAttribution(trigger, tele, result.SourceTokens)
	result.Status = "ok"
	result.Reason = ""
	return result, nil
}

func foldMatchesVisiblePrefix(visible, fold []provider.Message) bool {
	head := 0
	if len(visible) > 0 && visible[0].Role == provider.RoleSystem {
		head = 1
	}
	if len(fold) == 0 || head+len(fold) > len(visible) {
		return false
	}
	return providerVisibleFingerprint(modelInputMessages(fold)) ==
		providerVisibleFingerprint(modelInputMessages(visible[head:head+len(fold)]))
}

func (a *Agent) explicitCompressionSnapshotCurrent(snap explicitCompressionSnapshot) bool {
	current, version := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	projectionVersion := a.sess.compactionState.Projection.ProjectionVersion
	generation := a.sess.compactionState.Generation
	a.sess.compactionMu.Unlock()
	return version == snap.transcriptVersion && len(current) == len(snap.canonical) &&
		coveredPrefixHash(current, len(current)) == snap.coveredHash &&
		projectionVersion == snap.projectionVersion && generation == snap.generation &&
		a.currentPromptCacheKey() == snap.promptCacheKey
}

func (a *Agent) planVisibleCompression(snap explicitCompressionSnapshot, direction string, anchorIndex int, preview string) (visibleCompressionPlan, bool) {
	sourceTokens := a.estimatedVisibleRequestTokens(snap.visible)
	plan := visibleCompressionPlan{result: tool.CompressResult{
		Status:           "noop",
		Direction:        direction,
		Anchor:           preview,
		SourceTokens:     sourceTokens,
		ProjectionTokens: sourceTokens,
	}}
	if anchorIndex < 0 || anchorIndex >= len(snap.visible) {
		plan.result.Reason = "anchor is no longer present in the model context"
		return plan, false
	}
	head := 0
	if len(snap.visible) > 0 && snap.visible[0].Role == provider.RoleSystem {
		head = 1
	}
	completedEnd := len(snap.visible)
	if active := a.activeTurnStart(snap.visible); active >= 0 {
		completedEnd = active
	}
	start, end := head, anchorIndex
	if direction == "after" {
		start, end = anchorIndex, completedEnd
	}
	if start < head {
		start = head
	}
	if end > completedEnd {
		end = completedEnd
	}
	if start >= end {
		plan.result.Reason = "selected range is empty"
		return plan, false
	}

	plan.foldMask = make([]bool, len(snap.visible))
	plan.dropMask = make([]bool, len(snap.visible))
	plan.firstFold = len(snap.visible)
	latestContext := latestSessionContextIndex(snap.visible)
	for i, msg := range snap.visible {
		selected := i >= start && i < end
		mergeSummary := i < completedEnd && isCompactionSummary(msg)
		if isSessionContextMessage(msg) {
			// Context never enters the summarizer. Once an older snapshot falls
			// inside the explicitly compressed range, remove it from the
			// projection; the latest valid snapshot remains byte-identical.
			plan.dropMask[i] = selected && i != latestContext
			continue
		}
		if msg.Role == provider.RoleSystem || i < head || (!selected && !mergeSummary) {
			continue
		}
		plan.foldMask[i] = true
		plan.fold = append(plan.fold, msg)
		if i < plan.firstFold {
			plan.firstFold = i
		}
	}
	if len(plan.fold) == 0 {
		plan.result.Reason = "selected range has no model-visible messages"
		return plan, false
	}
	return plan, true
}

func (a *Agent) prepareVisibleCompression(ctx context.Context, trigger string, fold []provider.Message, instructions, inputMode string) (preparedVisibleCompression, string, error) {
	if a.svc.hooks != nil {
		if hookInstructions := a.svc.hooks.PreCompact(ctx, trigger); hookInstructions != "" {
			if instructions != "" {
				instructions += "\n"
			}
			instructions += hookInstructions
		}
	}
	filteredFold, removedPinned := withoutPinnedContextRevisions(fold)
	if len(filteredFold) == 0 {
		return preparedVisibleCompression{}, "selected range contains no summarizable messages", nil
	}
	if removedPinned {
		inputMode = SummaryInputNonPrefix
	}
	originalHash := providerVisibleFingerprint(modelInputMessages(filteredFold))
	preparedFold, preparedInstructions, err := a.interceptCompactionPrepare(ctx, filteredFold, instructions)
	if err != nil {
		return preparedVisibleCompression{}, "", err
	}
	preparedFold = modelInputMessages(preparedFold)
	if len(preparedFold) == 0 {
		return preparedVisibleCompression{}, "compaction hook removed the selected range", nil
	}
	if !removedPinned && providerVisibleFingerprint(modelInputMessages(preparedFold)) != originalHash {
		inputMode = SummaryInputExtensionRewritten
	}
	return preparedVisibleCompression{fold: preparedFold, instructions: preparedInstructions, inputMode: inputMode}, "", nil
}

func buildVisibleCompressionProjection(visible []provider.Message, plan visibleCompressionPlan, summary string) []provider.Message {
	projection := make([]provider.Message, 0, len(visible)-len(plan.fold)+1)
	for i, msg := range visible {
		if i == plan.firstFold {
			projection = append(projection, formatSummaryMessage(summary))
		}
		if !plan.foldMask[i] && (len(plan.dropMask) <= i || !plan.dropMask[i]) {
			projection = append(projection, msg)
		}
	}
	return projectionMessagesPreservingPinnedContext(projection)
}

func compactionTelemetryFromSummary(trigger, cacheState string, sourceTokens int, res foldSummary) CompactionTelemetry {
	tele := CompactionTelemetry{
		Trigger: trigger, CacheState: cacheState, Mode: res.Mode,
		SourceTokens:      sourceTokens,
		ProviderRequestID: res.RequestID,
		FoldTokens:        res.FoldTokens,
		Spans:             res.Spans,
		SummaryInputMode:  res.InputMode,
	}
	if tele.Spans <= 0 {
		tele.Spans = 1
	}
	usage := res.Usage
	if usage == nil {
		return tele
	}
	tele.InputTokens = usage.PromptTokens
	tele.OutputTokens = usage.CompletionTokens
	tele.CacheHitTokens = usage.CacheHitTokens
	tele.CacheMissTokens = usage.CacheMissTokens
	tele.CacheWriteTokens = usage.CacheWriteTokens
	tele.RequestCount = usage.RequestCount
	if tele.RequestCount <= 0 {
		tele.RequestCount = 1
	}
	return tele
}

// foldSummaryWithChunkedFallback retries summary size failures through the
// resilient fragment/tree-reduce path used for over-length sessions.
func (a *Agent) foldSummaryWithChunkedFallback(ctx context.Context, trigger string, fold []provider.Message, instructions string, sourceTokens int, inputMode string) (foldSummary, CompactionTelemetry, error) {
	res, tele, err := a.foldSummaryWithTelemetry(ctx, trigger, fold, instructions, sourceTokens, inputMode)
	if err == nil || !chunkedFallbackApplies(err, inputMode) {
		return res, tele, err
	}
	// 任务 556: stream the fragment progress (and the live token readout) to
	// the frontend. The #9082 wiring was lost when the chunked path moved to
	// session_extract.go — the callback went back to nil and the live card
	// regressed to a bare "compacting…". Feed the pass meter instead of
	// emitting directly: fold transitions emit on every change (bounded by
	// maxChunkedSummaryCalls), chunk deltas go through the 1s throttle gate.
	chunked, chunkedErr := a.chunkedFoldSummary(ctx, fold, instructions, func(done, total int) {
		a.compactionLiveFoldProgress(done, total)
	})
	chunked.Usage = mergeSamplingUsage(res.Usage, chunked.Usage)
	chunked.Spans += res.Spans
	if chunked.FoldTokens <= 0 {
		chunked.FoldTokens = res.FoldTokens
	}
	if chunked.RequestID == "" {
		chunked.RequestID = res.RequestID
	}
	if chunkedErr != nil {
		tele = compactionTelemetryFromSummary(trigger, a.CacheState(), sourceTokens, chunked)
		tele.Error = fmt.Sprintf("%v (chunked fallback: %v)", err, chunkedErr)
		return chunked, tele, chunkedErr
	}
	return chunked, compactionTelemetryFromSummary(trigger, a.CacheState(), sourceTokens, chunked), nil
}

// chunkedFallbackApplies reports a size failure the fragment path can fix. A
// provider overflow qualifies only once the transcript form has failed too;
// before that a re-planned replay is one request instead of many.
func chunkedFallbackApplies(err error, inputMode string) bool {
	if provider.AsContextLimitError(err) != nil {
		return inputMode == SummaryInputSlim
	}
	return summarySizeFailure(err)
}

// summaryTransientMaxAttempts bounds Task 303's automatic retry for transient
// provider stream failures (the mimo-api INTERNAL_ERROR mid-stream sample).
// Size/limit failures are excluded — those belong to the chunked fallback.
const summaryTransientMaxAttempts = 2

// Task 330: a 429 is not a stream hiccup - the 10M TPM window needs roughly
// a minute, so the generic short backoff (303) would just re-trip the limit.
// Rate-limited summaries get their own lane: wait out the provider's
// Retry-After when it states one, otherwise wait a default minute, then
// resume the same request once. The wait is not a blind retry: the request
// is identical, only the window has to roll over.
var summaryRateLimitWaitDefault = 60 * time.Second

// summaryRateLimitWaitDefaultCap bounds a provider-supplied Retry-After so a
// hostile or buggy value cannot park the compaction goroutine for hours.
const summaryRateLimitWaitCap = 300 * time.Second

// summaryRateLimited reports the rate-limit shape: a stated 429 status or a
// rate-limit message. It is deliberately separate from
// summaryTransientRetryable (task 330): 429 waits, it does not fast-retry.
func summaryRateLimited(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "rate limit") || strings.Contains(low, "429") {
		return true
	}
	return httpStatusOf(err.Error()) == 429
}

// summaryRateLimitWait returns how long to wait before resuming: the
// provider's Retry-After when the error text carries one (numeric seconds -
// the form our providers emit), otherwise the default minute, both capped.
func summaryRateLimitWait(err error) time.Duration {
	low := strings.ToLower(err.Error())
	for _, marker := range []string{"retry-after:", "retry after:", "retry-after ", "retry after "} {
		if i := strings.Index(low, marker); i >= 0 {
			rest := strings.TrimSpace(low[i+len(marker):])
			n := 0
			for _, c := range rest {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			if n > 0 {
				return min(time.Duration(n)*time.Second, summaryRateLimitWaitCap)
			}
		}
	}
	return summaryRateLimitWaitDefault
}

// summaryModelRejected reports the status-400 shape: the upstream answer when
// the request's model (or another request-side fact) is refused — the 切模型
// incident's "status 400: {\"model\":…}" echo carried no reason at all. The
// typed APIError is preferred; free-text status extraction keeps the task-243
// A4 shape discipline for providers that flatten the error. Distinct from
// summaryTransientRetryable: a 400 never heals by waiting, only by changing
// what the request is — the 任务635 lane retries it on the current model.
func summaryModelRejected(err error) bool {
	if err == nil {
		return false
	}
	var api *provider.APIError
	if errors.As(err, &api) {
		return api.Status == 400
	}
	return httpStatusOf(err.Error()) == 400
}

// summaryTransientRetryable reports whether a summary request failed in a way
// that a short backoff can fix: the provider broke the stream or the transport
// mid-response, not a semantic rejection of the input.
func summaryTransientRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"stream error", "internal_error", "read stream", "connection reset",
		"broken pipe", "unexpected eof", "server disconnected",
		"429", "502", "503", "504", "deadline exceeded",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// promptCacheTTLEstimate is the observation-only TTL assumption for cache-miss
// attribution (task 317): providers do not publish their prompt-cache window;
// the industry range is 5–10 minutes, so the lower bound is used. Nothing
// gates on this value — it only labels a log line.
const promptCacheTTLEstimate = 5 * time.Minute

// compactionMissAttribution names why a compaction round measured zero cache
// hit (task 317, cache-查证 §④ candidates). Pure for testing:
//   - zero_usage_no_request: the summary request never carried usage — the
//     server never reported (or never wrote) a cache surface;
//   - error_gap_over_ttl: the last provider error is older than the TTL
//     estimate, so the server-side prefix has likely been evicted (candidate B);
//   - error_gap_within_ttl: a recent error sits inside the window — the miss
//     more likely comes from a never-written prefix (zero-usage error round,
//     candidate A) than from TTL;
//   - prefix_or_model_shape: no recent error — the known prefix/model-shape
//     family (modelRef switch, #9572).
func compactionMissAttribution(tele CompactionTelemetry, errorGap time.Duration, hadError bool) string {
	if tele.InputTokens == 0 && tele.RequestCount == 0 {
		return "zero_usage_no_request"
	}
	if !hadError {
		return "prefix_or_model_shape"
	}
	if errorGap >= promptCacheTTLEstimate {
		return "error_gap_over_ttl"
	}
	return "error_gap_within_ttl"
}

// logCompactionMissAttribution emits the miss-attribution line for a
// successful compaction round that measured zero cache hit (task 317, cache
// 保全 面 b). Both compaction entry points call it — the explicit compress
// tool and the automatic projection fold — so a manual "second compaction"
// after an outage ships the same evidence as an auto fold. Observation only:
// no behavior decision reads this line.
func (a *Agent) logCompactionMissAttribution(trigger string, tele CompactionTelemetry, sourceTokens int) {
	if tele.CacheHitTokens != 0 || sourceTokens <= 0 {
		return
	}
	lastErr := a.sess.lastProviderErrorAt.Load()
	hadError := lastErr > 0
	var gap time.Duration
	gapMs := int64(-1)
	if hadError {
		gap = time.Since(time.UnixMilli(lastErr))
		gapMs = gap.Milliseconds()
	}
	slog.Warn("agent: compaction cache miss attribution",
		"trigger", trigger,
		"attribution", compactionMissAttribution(tele, gap, hadError),
		"error_gap_ms", gapMs,
		"model_ref", a.destinationModelRef(),
		"input_tokens", tele.InputTokens,
		"cache_miss_tokens", tele.CacheMissTokens,
		"source_tokens", sourceTokens)
}

// summarizeFold runs the summary request, retrying transient stream failures
// with backoff (Task 303). Every attempt and the final outcome carry their
// reason in slog, so a "summary failed" report always ships its retry trail
// plus the cache readout that explains a slow full-prefill round.
func (a *Agent) summarizeFold(ctx context.Context, trigger string, fold []provider.Message, instructions string, sourceTokens int, inputMode string, req foldRequest) (foldSummary, CompactionTelemetry, error) {
	var res foldSummary
	var tele CompactionTelemetry
	var err error
	rateWaits := 0
	// 任务635 P0: the summary request destination resolves live per attempt
	// (runSummaryRequest → providerForRequest), so a model switch landing while
	// a summary is in flight changes what the next attempt reaches. The 切模型
	// 上下文窗口异常 incident (根因诊断 20261008) deadlocked because a
	// status-400 aimed at the projection-era model left the input hash parked
	// with no alternative exit. modelRefRetried bounds that escape hatch to one
	// retry, mirroring the 429 lane's wait-once discipline.
	// 任务707: the compared ref is the SUMMARY destination (summaryDestinationRef)
	// — with an armed compact model the serving ref is stable across attempts,
	// so a conversation-model switch no longer reads as "destination changed"
	// and cannot trigger a blind resend of a deterministic 400.
	modelRefRetried := false
	for attempt := 0; ; attempt++ {
		attemptRef := a.summaryDestinationRef()
		if req.allowChunked {
			res, tele, err = a.foldSummaryWithChunkedFallback(ctx, trigger, fold, instructions, sourceTokens, inputMode)
		} else {
			res, tele, err = a.foldSummaryWithTelemetry(ctx, trigger, fold, instructions, sourceTokens, inputMode)
		}
		if err == nil {
			if rateWaits > 0 {
				slog.Info("agent: summary request resumed after rate-limit wait",
					"waits", rateWaits, "attempts", attempt+1, "trigger", trigger,
					"source_tokens", sourceTokens)
			}
			if attempt > 0 && rateWaits == 0 {
				if modelRefRetried {
					slog.Info("agent: summary request succeeded after model-ref retry",
						"attempts", attempt+1, "trigger", trigger,
						"model_ref", a.summaryDestinationRef(),
						"source_tokens", sourceTokens,
						"cache_hit_tokens", tele.CacheHitTokens, "cache_miss_tokens", tele.CacheMissTokens)
				} else {
					slog.Info("agent: summary request succeeded after transient retry",
						"attempts", attempt+1, "trigger", trigger,
						"source_tokens", sourceTokens,
						"cache_hit_tokens", tele.CacheHitTokens, "cache_miss_tokens", tele.CacheMissTokens)
				}
			}
			return res, tele, nil
		}
		// 任务635 P0: a status-400 rejection is the shape the upstream answers
		// with when the request's model is refused (the incident's
		// "status 400: {\"model\":…}" echo). When the destination ref changed
		// since the failed attempt, one retry on the CURRENT model is the
		// alternative exit — the rejection is not deterministic input shape.
		// When the ref is unchanged the same 400 would fire again, so no blind
		// duplicate spend; the failure then parks as before.
		if !modelRefRetried && summaryModelRejected(err) {
			if ref := a.summaryDestinationRef(); ref != attemptRef {
				modelRefRetried = true
				slog.Warn("agent: summary request rejected — retrying once on the current model",
					"trigger", trigger, "failed_ref", attemptRef, "current_ref", ref, "err", err)
				continue
			}
		}
		// Task 330: 429 gets its own lane ahead of the generic transient
		// backoff - wait the window out (Retry-After when the provider states
		// it, default a minute), then resume this identical request once.
		// The wait is logged with its duration for the acceptance readout, and
		// a successful resume logs separately from the generic transient one.
		if summaryRateLimited(err) && rateWaits < 1 {
			wait := summaryRateLimitWait(err)
			slog.Info("agent: summary rate limited — waiting to resume",
				"trigger", trigger, "wait_s", int(wait/time.Second),
				"attempt", attempt+1, "err", err)
			select {
			case <-ctx.Done():
				return res, tele, ctx.Err()
			case <-time.After(wait):
			}
			rateWaits++
			continue
		}
		if attempt >= summaryTransientMaxAttempts || !summaryTransientRetryable(err) {
			// Task 317: a failed summary is a provider error too — stamp it so
			// the NEXT compaction round can attribute its miss against the
			// error→compact gap (same stamp the sampling trail writes).
			a.sess.lastProviderErrorAt.Store(time.Now().UnixMilli())
			slog.Warn("agent: summary request failed",
				"trigger", trigger, "attempts", attempt+1, "err", err,
				"model_ref", attemptRef,
				"transient", summaryTransientRetryable(err),
				"rate_limited", summaryRateLimited(err),
				"source_tokens", sourceTokens,
				"cache_hit_tokens", tele.CacheHitTokens, "cache_miss_tokens", tele.CacheMissTokens)
			return res, tele, err
		}
		slog.Warn("agent: summary request transient failure — retrying",
			"attempt", attempt+1, "max_attempts", summaryTransientMaxAttempts+1,
			"trigger", trigger, "err", err)
		select {
		case <-ctx.Done():
			return res, tele, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
}

func (a *Agent) compactToProjectionLocked(ctx context.Context, trigger, instructions string, req foldRequest) (CompactionOutcome, error) {
	activeTurn := a.activeTurnCreatedAt.Load()
	canonical, transcriptVersion := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	stateSnapshot := a.sess.compactionState
	startProjectionVersion := a.sess.compactionState.Projection.ProjectionVersion
	startGeneration := a.sess.compactionState.Generation
	a.sess.compactionMu.Unlock()
	msgs, onProjection := a.visibleInputForFold(stateSnapshot, canonical, transcriptVersion)
	viewInputHash := providerVisibleFingerprint(modelInputMessages(msgs))
	head, start, ok := a.planFoldRegion(msgs, req.force, req.mustFree)
	if !ok {
		return CompactionNoop, nil
	}
	latestContext := latestSessionContextIndex(msgs)
	_, preliminaryFold, _ := a.partitionFoldForProjectionAt(msgs[head:start], head, latestContext)
	if len(preliminaryFold) == 0 || (!req.force && !foldEconomics(preliminaryFold)) {
		return CompactionNoop, nil
	}
	fixedPrefixTokens := a.estimatedVisibleRequestTokens(msgs[:head])
	if a.destinationContextWindow() > 0 && fixedPrefixTokens >= a.compactTrigger() {
		return CompactionNoop, fmt.Errorf("%w: fixed prefix (%d tokens) already exceeds trigger (%d)", errCheckpointRejected, fixedPrefixTokens, a.compactTrigger())
	}

	a.svc.sink.Emit(event.Event{Kind: event.CompactionStarted, Compaction: event.Compaction{Trigger: trigger}})
	// 任务 556: meter the pass from Started to every exit (defer covers Done,
	// aborts, panics) so live readout events never outlive the pending card.
	a.compactionLiveBegin(trigger)
	defer a.compactionLiveStop()
	// Task 303: phase 1/3 of the compaction slog trail (start → fold/summary →
	// done). Token fields live in the done log where the counters exist.
	slog.Info("agent: compaction started", "trigger", trigger, "stage", "start")
	if a.svc.hooks != nil {
		if hookInstr := a.svc.hooks.PreCompact(ctx, trigger); hookInstr != "" {
			if instructions != "" {
				instructions += "\n"
			}
			instructions += hookInstr
		}
	}
	// Cap every automatic summary input (#9572), including pressure folds after
	// projection invalidation. mustFree also covers the over-ceiling manual rescue
	// merged in #9474; ordinary manual compaction keeps its requested range.
	if req.mustFree || trigger != CompactionTriggerManual {
		start = a.maximumSafeSummaryPrefixEnd(msgs, head, start, instructions)
		if start <= head {
			a.emitCompactionAborted(trigger)
			return CompactionNoop, fmt.Errorf("%w: no balanced prefix leaves enough room for a summary response", errCheckpointRejected)
		}
	}

	covered, bodySuffix := projectionCoverageForFold(stateSnapshot, msgs, start, onProjection)
	regionHadPinnedRevision := containsPinnedContextRevision(msgs[head:start])
	kept, fold, retention := a.partitionFoldForProjectionAt(msgs[head:start], head, latestContext)
	if len(fold) == 0 {
		a.emitCompactionAborted(trigger)
		return CompactionNoop, nil
	}
	originalFoldHash := providerVisibleFingerprint(modelInputMessages(fold))
	var err error
	fold, instructions, err = a.interceptCompactionPrepare(ctx, fold, instructions)
	if err != nil {
		a.emitCompactionAborted(trigger)
		return CompactionNoop, err
	}
	if len(fold) == 0 {
		a.emitCompactionAborted(trigger)
		return CompactionNoop, nil
	}
	if req.mustFree || trigger != CompactionTriggerManual {
		if err := a.validateSafeSummaryRequest(fold, instructions, req.slim); err != nil {
			a.emitCompactionAborted(trigger)
			return CompactionNoop, err
		}
	}

	sourceTokens := a.estimatedVisibleRequestTokens(msgs)
	inputMode := summaryInputModeFor(req, regionHadPinnedRevision,
		providerVisibleFingerprint(modelInputMessages(fold)) != originalFoldHash)
	res, tele, err := a.summarizeFold(ctx, trigger, fold, instructions, sourceTokens, inputMode, req)
	if err != nil {
		a.emitCompactionTelemetry(tele)
		a.emitCompactionAborted(trigger)
		return CompactionNoop, err
	}
	summary, err := a.interceptCompactionComplete(ctx, res.Text)
	if err != nil {
		tele.Error = err.Error()
		a.emitCompactionTelemetry(tele)
		a.emitCompactionAborted(trigger)
		return CompactionNoop, err
	}

	// The projection body freezes only prefix + digest + kept messages; the
	// verbatim tail splices live from canonical[start:] so tail-side rewrites
	// (rewind truncation, snips) stay visible without rebuilding the fold.
	projMsgs := checkpointProjectionMessages(msgs, head, kept, summary)
	if len(bodySuffix) > 0 {
		projMsgs = append(projMsgs, projectionMessagesPreservingPinnedContext(bodySuffix)...)
	}
	tele.UserTurnsKept, tele.UserTurnsDropped = retention.Kept, retention.Dropped
	projMsgs, spliced, projTokens, err := a.preparePinnedCheckpointCandidate(trigger, projMsgs, canonical, covered, sourceTokens, &tele)
	if err != nil {
		a.emitCompactionAborted(trigger)
		return CompactionNoop, err
	}
	viewOutputHash := providerVisibleFingerprint(modelInputMessages(spliced))
	_, err = a.commitSummaryProjection(summaryProjectionCommit{
		canonical: canonical, fold: fold, projected: projMsgs, result: res,
		transcriptVersion: transcriptVersion, projectionVersion: startProjectionVersion,
		generation: startGeneration, activeTurn: activeTurn, trigger: trigger,
		summary: summary, inputHash: viewInputHash, outputHash: viewOutputHash,
		sourceTokens: sourceTokens, projectionTokens: projTokens, covered: covered,
	})
	if err != nil {
		a.emitCompactionAborted(trigger)
		return CompactionNoop, err
	}
	a.svc.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{
		Trigger: trigger, Messages: len(fold), Summary: summary,
	}})
	// Task 303: phase 3/3 — done with the cache readout. The 688K/1.5% sample
	// (full prefill after a model switch, promptCacheKey carries modelRef) is
	// quantified here: input vs hit/miss tokens pin a slow round to cache miss.
	slog.Info("agent: compaction done", "trigger", trigger, "stage", "done",
		"folded_messages", len(fold), "source_tokens", sourceTokens,
		"projection_tokens", projTokens,
		"input_tokens", tele.InputTokens, "output_tokens", tele.OutputTokens,
		"cache_hit_tokens", tele.CacheHitTokens, "cache_miss_tokens", tele.CacheMissTokens,
		"request_count", tele.RequestCount)
	// Task 317 (cache 保全 面 b): the automatic fold attributes its miss the
	// same way the explicit tool does (shared helper, both entry points).
	a.logCompactionMissAttribution(trigger, tele, sourceTokens)
	return CompactionInstalled, nil
}

func (a *Agent) preparePinnedCheckpointCandidate(
	trigger string,
	projection, canonical []provider.Message,
	covered, sourceTokens int,
	tele *CompactionTelemetry,
) ([]provider.Message, []provider.Message, int, error) {
	projection, pinnedCheckpoint, err := rebasePinnedContextProjection(projection, canonical, covered)
	if err != nil {
		return nil, nil, 0, err
	}
	spliced := append(append([]provider.Message(nil), projection...), canonical[covered:]...)
	projectionTokens := a.estimatedVisibleRequestTokens(spliced)
	tele.ProjectionTokens = projectionTokens
	a.emitCompactionTelemetry(*tele)
	if err := a.acceptCheckpointCandidate(trigger, sourceTokens, projectionTokens); err != nil {
		if pinnedCheckpoint {
			return nil, nil, 0, fmt.Errorf("pinned-context-too-large: checkpoint prevents compaction acceptance: %w", err)
		}
		return nil, nil, 0, err
	}
	return projection, spliced, projectionTokens, nil
}

// projectionCoverageForFold maps a working-view boundary to canonical
// coverage. A suffix inside an existing frozen body remains in the new body
// because it has no corresponding canonical tail to splice from.
func projectionCoverageForFold(state CompactionState, msgs []provider.Message, start int, onProjection bool) (int, []provider.Message) {
	if !onProjection {
		return start, nil
	}
	body := len(state.Projection.Messages)
	prior := state.Projection.CoveredCount
	if start < body {
		return prior, msgs[start:body]
	}
	return prior + (start - body), nil
}

// visibleInputForFold prefers the prior projection + new history over full
// canonical. The second return reports whether the projection was used, so
// fold boundaries can be translated back to canonical indices.
func (a *Agent) visibleInputForFold(state CompactionState, canonical []provider.Message, transcriptVersion uint64) ([]provider.Message, bool) {
	if projectionValid(state, canonical) {
		if projected := modelVisibleFromProjection(state.Projection, canonical); len(projected) > 0 {
			return projected, true
		}
	}
	return canonical, false
}

func checkpointProjectionMessages(msgs []provider.Message, head int, kept []provider.Message, summary string) []provider.Message {
	projMsgs := make([]provider.Message, 0, head+1+len(kept))
	projMsgs = append(projMsgs, msgs[:head]...)
	projMsgs = append(projMsgs, kept...)
	projMsgs = append(projMsgs, formatSummaryMessage(summary))
	return provider.ProjectionMessages(projMsgs)
}

// acceptCheckpointCandidate requires real savings: a candidate that would not
// shrink the view is rejected because installing it cannot help. Task 516: a
// candidate below source but still at or above the physical input ceiling is
// real progress and is installed — the summary cost is already spent, the
// smaller view is what the next fold starts from, and the maintenance ladder
// keeps folding while the truncation rescue stays the below-ceiling guarantee.
// The old whole-candidate rejection here was a dead end: the #9572 summary
// input cap bounds one fold well below the window, so a view several times the
// window could never reach the ceiling in one fold, and the same oversized
// candidate was re-summarized and re-rejected every round (the 00:55 event:
// candidate 2.09M vs ceiling 999,744, fold_installed=false, loop).
func (a *Agent) acceptCheckpointCandidate(trigger string, sourceTokens, candidateTokens int) error {
	if candidateTokens >= sourceTokens {
		return fmt.Errorf("%w: candidate would not reduce tokens (%d >= %d)", errCheckpointRejected, candidateTokens, sourceTokens)
	}
	hard := a.hardInputCeiling()
	if trigger != CompactionTriggerManual && hard > 0 && candidateTokens >= hard {
		slog.Warn("agent: checkpoint candidate still above physical ceiling — installing partial fold progress",
			"trigger", trigger, "source_tokens", sourceTokens,
			"candidate_tokens", candidateTokens, "ceiling", hard)
	}
	return nil
}

// planFoldRegion returns [head:start] to fold; force shrinks the recent tail.
// splitActive lets an overflow rescue fold the active turn's older completed
// rounds as well; otherwise the active turn stays verbatim.
func (a *Agent) planFoldRegion(msgs []provider.Message, force, splitActive bool) (head, start int, ok bool) {
	head, start, ok = a.planCompaction(msgs, minCompactMessages, force)
	if !ok {
		head, start, ok = a.planCompaction(msgs, 1, force)
	}
	if !ok {
		return head, start, false
	}
	if active := a.activeTurnStart(msgs); active >= head && active < start {
		if splitActive {
			start = activeTurnFoldBoundary(msgs, active, start)
		} else {
			start = active
		}
	}
	return head, start, start > head
}

type userTurnRetention struct {
	Kept    int
	Dropped int
}

func (a *Agent) partitionFoldForProjection(region []provider.Message) (kept, fold []provider.Message, retention userTurnRetention) {
	return a.partitionFoldForProjectionAt(region, 0, latestSessionContextIndex(region))
}

func (a *Agent) partitionFoldForProjectionAt(region []provider.Message, offset, latestContext int) (kept, fold []provider.Message, retention userTurnRetention) {
	for i, m := range region {
		if m.LocalOnly || IsPinnedContextRevision(m) {
			continue
		}
		if isSessionContextMessage(m) {
			if offset+i == latestContext {
				kept = append(kept, m)
			}
			continue
		}
		fold = append(fold, m)
		if IsUserAuthoredTurnMessage(m) {
			retention.Dropped++
		}
	}
	return kept, fold, retention
}

func latestSessionContextIndex(messages []provider.Message) int {
	for i := range slices.Backward(messages) {
		if isSessionContextMessage(messages[i]) {
			return i
		}
	}
	return -1
}

// runCompactionSummary uses the single local summarizer path for every provider.
func (a *Agent) runCompactionSummary(ctx context.Context, fold []provider.Message, instructions string) (summary, mode string, usage *provider.Usage, providerReqID string, err error) {
	summary, usage, err = a.summarizeOnce(ctx, fold, instructions)
	if err != nil {
		return "", CompactionModeSummarized, usage, "", err
	}
	return summary, CompactionModeSummarized, usage, "", nil
}
