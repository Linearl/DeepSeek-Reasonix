package agent

import (
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// contextUsage memoises the projected prompt size. The estimate walks every
// visible message, and status gauges redraw far more often than the view moves,
// so it is keyed on everything that can change the answer: the transcript,
// projection, calibration, and provider-visible tool schemas.
type contextUsage struct {
	transcriptVersion  uint64
	projectionVersion  uint64
	calibration        *promptTokenCalibration
	tools              *tool.Registry
	toolSchemaRevision uint64
	tokens             int
}

// ContextUsedTokens is the number ContextManager compares against its
// thresholds: the estimated prompt size of the view the next request sends. A
// gauge fed from the last turn's usage instead lags a turn, counts completion
// tokens the trigger ignores, and reads zero on a rebound session — which is
// how a session displays 8% while it is compacting.
func (a *Agent) ContextUsedTokens() int {
	if a == nil {
		return 0
	}
	session := a.Session()
	if session == nil {
		return 0
	}
	transcriptVersion := session.TranscriptVersion()
	projectionVersion := a.currentProjectionVersion()
	calibration := a.sess.output.promptCalibration.Load()
	tools := a.svc.tools
	toolSchemaRevision := tools.SchemaRevision()
	if cached := a.sess.output.contextUsage.Load(); cached != nil &&
		cached.transcriptVersion == transcriptVersion &&
		cached.projectionVersion == projectionVersion &&
		cached.calibration == calibration &&
		cached.tools == tools &&
		cached.toolSchemaRevision == toolSchemaRevision {
		return cached.tokens
	}
	tokens := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	a.sess.output.contextUsage.Store(&contextUsage{
		transcriptVersion:  transcriptVersion,
		projectionVersion:  projectionVersion,
		calibration:        calibration,
		tools:              tools,
		toolSchemaRevision: toolSchemaRevision,
		tokens:             tokens,
	})
	return tokens
}

// observedContextTokens sizes the current view the way the provider itself
// would (任务 1183): the last trusted sampling request's real prompt count —
// cache-hit plus cache-miss, tool definitions, system framing and the real
// image price, none of which the local estimator reproduces exactly — plus
// that request's completion (its reply is part of the context now), plus a
// local-calibrated price for only the characters appended since. The baseline
// covers ~100% of a mature session, so the ~7% undershoot the estimator
// carries on the whole prompt shrinks to a same-session ratio applied to a
// few recent messages.
//
// ok=false means no trustworthy anchor exists and the caller keeps today's
// estimate untouched: no usage yet (first turn, or a provider that never
// reports), reconstructed-only telemetry, a view that has SHRUNK below the
// baseline (a fold landed or a rewind rewrote history — replaying the stale
// count would re-add the folded prefix and compaction could never converge),
// or a hot model switch (the anchor follows the request destination, the same
// reset semantics the 1190 EWMA factor uses — a stale provider's count must
// not price the new one).
func (a *Agent) observedContextTokens(visible []provider.Message) (int, bool) {
	if a == nil {
		return 0, false
	}
	base := a.sess.output.contextBaseline.Load()
	if base == nil || base.promptTokens <= 0 || base.coveredChars <= 0 {
		return 0, false
	}
	if cur := a.requestProviderName(); cur != base.providerName {
		return 0, false
	}
	curChars := int64(charsOfMessages(visible))
	if curChars < base.coveredChars {
		return 0, false
	}
	tokens := base.promptTokens + base.completionTokens
	if delta := curChars - base.coveredChars; delta > 0 {
		tokens += int(float64(delta) * a.tokPerChar())
	}
	// The baseline already carries the real price of the images it measured;
	// images appended since add their local resolution-derived price. Cropped
	// images get no negative correction — erring high only folds a turn early,
	// erring low lets a session run past the window.
	if cur := imageEstimateTokensOf(provider.Request{Messages: visible}); cur > base.imageTokens {
		tokens += cur - base.imageTokens
	}
	return tokens, true
}
