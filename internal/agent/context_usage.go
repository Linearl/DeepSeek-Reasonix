package agent

import (
	"time"

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
	baseline           *contextBaselineState
	tokens             int
}

// ContextUsedTokens is the number ContextManager compares against its
// thresholds: the estimated prompt size of the view the next request sends. A
// gauge fed from the last turn's usage instead lags a turn, counts completion
// tokens the trigger ignores, and reads zero on a rebound session — which is
// how a session displays 8% while it is compacting.
//
// 任务1183/tl1600: the trigger's Prepare overrides this estimate with the
// provider-measured anchor (observedContextTokens, context_manager.go) — the
// gauge must ride the same override or the display contradicts the compaction
// the user is watching (the 20261115 新13 incident: the gauge sat in the 50s
// while the trigger judged the anchor above the line). The degraded-state
// occupancy override (tailView/stale-serving) stays trigger-only: those states
// announce themselves through the panel's ProjectionValid label, and the gauge
// keeps answering "what the next request sends".
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
	baseline := a.sess.output.contextBaseline.Load()
	if cached := a.sess.output.contextUsage.Load(); cached != nil &&
		cached.transcriptVersion == transcriptVersion &&
		cached.projectionVersion == projectionVersion &&
		cached.calibration == calibration &&
		cached.tools == tools &&
		cached.toolSchemaRevision == toolSchemaRevision &&
		cached.baseline == baseline {
		return cached.tokens
	}
	visible := a.modelVisibleMessages()
	tokens := a.estimatedVisibleRequestTokens(visible)
	if obs, ok := a.observedContextTokens(visible); ok {
		tokens = obs
	}
	a.sess.output.contextUsage.Store(&contextUsage{
		transcriptVersion:  transcriptVersion,
		projectionVersion:  projectionVersion,
		calibration:        calibration,
		tools:              tools,
		toolSchemaRevision: toolSchemaRevision,
		baseline:           baseline,
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
//
// tl1570 cold-start anchor: when the in-memory anchor is absent (fresh
// process before the first sampled turn) the anchor persisted in the
// session's .jsonl.meta sidecar takes over after the same destination gates
// plus its own 24h trust window (loadPersistedContextBaseline). This is the
// restart leg of the tl1506 caliber: the open-session self-heal judges by the
// provider's own accounting across relaunches instead of misjudging a mature
// session as over-window from the estimate. A validated read-back re-arms the
// in-memory anchor so later reads skip the sidecar I/O.
func (a *Agent) observedContextTokens(visible []provider.Message) (int, bool) {
	if a == nil {
		return 0, false
	}
	base := a.sess.output.contextBaseline.Load()
	if base == nil {
		if persisted, ok := a.loadPersistedContextBaseline(time.Now()); ok {
			base = persisted
			a.sess.output.contextBaseline.Store(persisted)
			a.sess.output.lastPersistedBaseline.Store(persisted)
		}
	}
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

// TrustedObservedContextTokens is the host-facing face of the 任务 1183
// real-usage anchor (tl1506): the provider-measured context size of the
// agent's current provider-visible view, or ok=false when no trusted anchor
// exists — no usage yet (fresh process before the first turn), reconstructed
// (Estimated/Unknown) telemetry only, a view that shrank below the measured
// request (a fold or rewind landed), or a hot model switch (the anchor keys to
// the request destination). The open-session self-heal injects this reading as
// HealthInspectOptions.ActualContextTokens so the over-window criterion judges
// by the provider's own accounting before falling back to the local estimate —
// 用户终裁 20261109（tl1506）：超窗判据有实账按实账判，无实账才退回估算。
func (a *Agent) TrustedObservedContextTokens() (int, bool) {
	if a == nil {
		return 0, false
	}
	if a.Session() == nil {
		return 0, false
	}
	return a.observedContextTokens(a.modelVisibleMessages())
}

// trustedContextBaselineBase is the anchor's OWN measurement (tl1644): the
// provider-measured size of the last sampled request, WITHOUT repricing the
// current view. observedContextTokens adds (currentChars − coveredChars) ×
// tokPerChar on top of the base — correct for compaction triggers, which ask
// "how big is the view I am about to send", but wrong for the overflow-diet
// defer gate, which asks "was the last actually-served view sendable". After a
// projection invalidation the fallback view IS the full canonical transcript,
// so the delta term alone can cross the ceiling even when the anchor's base
// (the healthy pre-invalidation view, the tl1644 现场: 669K served) sits far
// below it. Gates mirror loadPersistedContextBaseline: destination match plus
// structural sanity; the 24h TTL and covered-chars guard are already inside
// the loader / observedContextTokens respectively.
//
// 读到内存锚缺失时复用 tl1570 的盘上回读并回填内存锚（与 observedContextTokens
// 同一行为），保证重启后首轮检查即可用。
func (a *Agent) trustedContextBaselineBase() (int, bool) {
	if a == nil || a.Session() == nil {
		return 0, false
	}
	base := a.sess.output.contextBaseline.Load()
	if base == nil {
		if persisted, ok := a.loadPersistedContextBaseline(time.Now()); ok {
			base = persisted
			a.sess.output.contextBaseline.Store(persisted)
			a.sess.output.lastPersistedBaseline.Store(persisted)
		}
	}
	if base == nil || base.promptTokens <= 0 || base.coveredChars <= 0 {
		return 0, false
	}
	if cur := a.requestProviderName(); cur != base.providerName {
		return 0, false
	}
	n := base.promptTokens + base.completionTokens + base.imageTokens
	if n <= 0 {
		return 0, false
	}
	return n, true
}
