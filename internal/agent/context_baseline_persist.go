package agent

import (
	"log/slog"
	"time"
)

// tl1570：超窗判定实账落盘（20261113 包90 装机现场用户报障的根治）。
//
// 现场事故链：重启 → SelfHealOpenSession 首轮前必然无实账（任务 1183 的
// contextBaseline 是纯内存 atomic，重启即丢）→ 超窗判据退回计价口径估算
// （tl1506 三段口径的第二段）→ 成熟大会话估算易 ≥95% 窗口 → 误判超窗 →
// Diet（剥图+折叠）→ fold 改写日志前缀 → provider 缓存全 miss → 压缩慢/失败
// → compact_fail_streak 又触发 Diet → 投影失效回退全量更超窗 → 裁剪最早历史。
//
// 修复：contextBaseline 随 usage 采样同步持久化到会话侧车（.jsonl.meta 的
// context_baseline 字段——选 meta 而非新侧车文件的依据见 BranchMeta.ContextBaseline
// 注释），重启后 observedContextTokens 在内存锚缺失时回读落盘锚作为冷启动
// 实账锚；仅真正无锚（全新会话/换 provider/换模型/视图增长超界/超 24h）才
// 退回估算。侧车跟随 trash/换位链（SessionSidecarFiles 内含 SessionMeta），
// 删除会话零残留。

// contextBaselinePersistTTL bounds how long a disk-read anchor stays trusted
// (tl1570 用户拍板：落盘实账有效期 ≤24h). A stale anchor prices a view that
// may have grown or been rewritten in ways the covered-chars guard cannot see;
// past the window the criterion falls back to the estimate. In-memory anchors
// are not TTL'd — they are never older than their process.
const contextBaselinePersistTTL = 24 * time.Hour

// persistContextBaseline writes the anchor into the session's .jsonl.meta
// sidecar. Called from the sampling path (storeContextBaseline) after the
// in-memory anchor lands; a failed write never fails the sampling turn — the
// in-memory anchor already serves the live process, and the next trusted
// sample retries the sidecar.
//
// Write throttle: an anchor identical (sameMeasurement) to the one most
// recently written — or re-armed from disk — is skipped, so streaming usage
// chunks repeating the same measurement do not rewrite the meta per chunk.
//
// Locking: tryLock (TryAcquire), never the blocking 5s meta-lock wait — the
// caller is the hot streaming loop. Losing the race (autosave's listing
// refresh holds the lock) skips this write; the next differing sample retries.
func (a *Agent) persistContextBaseline(s *contextBaselineState) {
	if a == nil || s == nil {
		return
	}
	path := a.SessionPath()
	if path == "" {
		// Memory-only session (no transcript bound): nothing to persist beside.
		return
	}
	if cur := a.sess.output.lastPersistedBaseline.Load(); cur != nil && cur.sameMeasurement(s) {
		return
	}
	unlock, ok, err := tryLockSessionMetaPath(path)
	if err != nil {
		slog.Warn("tl1570: context baseline persist lock failed", "path", path, "err", err)
		return
	}
	if !ok {
		// Autosave/listing refresh owns the lock right now; skip — the next
		// trusted sample persists the (then-current) anchor.
		return
	}
	defer unlock()
	m, err := ensureBranchMetaUnlocked(path)
	if err != nil {
		slog.Warn("tl1570: context baseline persist read-back failed", "path", path, "err", err)
		return
	}
	if m.ContextBaseline != nil {
		if cur := metaToContextBaselineState(m.ContextBaseline); cur.sameMeasurement(s) {
			// Another writer already flushed this exact measurement (or we
			// re-armed from it): adopt its disk state without rewriting.
			a.sess.output.lastPersistedBaseline.Store(s)
			return
		}
	}
	stamp := s.sampledAt
	if stamp.IsZero() {
		stamp = time.Now()
	}
	m.ContextBaseline = &ContextBaselineMeta{
		ProviderName:     s.providerName,
		ModelRef:         s.modelRef,
		PromptTokens:     s.promptTokens,
		CompletionTokens: s.completionTokens,
		CoveredChars:     s.coveredChars,
		ImageTokens:      s.imageTokens,
		SampledAt:        stamp.UTC(),
	}
	if err := saveBranchMeta(path, m, false); err != nil {
		slog.Warn("tl1570: context baseline persist write failed", "path", path, "err", err)
		return
	}
	a.sess.output.lastPersistedBaseline.Store(s)
}

// clearContextBaseline drops the real-usage anchor after a view-changing
// projection install (fold/summary commit, prune/truncate rescue — every
// applied receipt with CacheBreak). The anchor describes one measured request
// shape; after the install that shape no longer exists, and its own shrink
// guard (curChars < coveredChars) only holds until the turn's appends re-cross
// the stale coveredChars — at which point the anchor wakes and re-prices the
// new view at the OLD request's real count, overshooting by the folded (or
// pruned) region's real cost. That overshoot is the tl1600 measured trigger
// contradiction: the deciding est jumped above the line while the estimator
// (the number the gauge displayed) sat below it. Clearing at install makes the
// anchor dead until the next trusted sample re-anchors the NEW view — the
// estimator serves the gap exactly as the pre-1183 behavior did.
//
// Best-effort by the same contract as persistContextBaseline: the in-memory
// clear is unconditional, the sidecar rewrite retries on the next trusted
// sample if the meta lock is busy.
func (a *Agent) clearContextBaseline() {
	if a == nil {
		return
	}
	a.sess.output.contextBaseline.Store(nil)
	a.sess.output.lastPersistedBaseline.Store(nil)
	a.clearPersistedContextBaseline()
}

// clearPersistedContextBaseline rewrites the .jsonl.meta sidecar without its
// context_baseline field so a restart cannot re-arm an anchor whose measured
// view no longer exists (loadPersistedContextBaseline would otherwise revive
// the wake-up overshoot across the restart, the exact leg tl1570 added).
func (a *Agent) clearPersistedContextBaseline() {
	if a == nil {
		return
	}
	path := a.SessionPath()
	if path == "" {
		return
	}
	unlock, ok, err := tryLockSessionMetaPath(path)
	if err != nil || !ok {
		// Lock busy: the next trusted sample's persistContextBaseline overwrites
		// the stale anchor anyway; until then the shrink guard plus the load-time
		// gates bound the damage the same way they did pre-clear.
		return
	}
	defer unlock()
	m, err := ensureBranchMetaUnlocked(path)
	if err != nil {
		slog.Warn("tl1600: context baseline clear read-back failed", "path", path, "err", err)
		return
	}
	if m.ContextBaseline == nil {
		return
	}
	m.ContextBaseline = nil
	if err := saveBranchMeta(path, m, false); err != nil {
		slog.Warn("tl1600: context baseline clear write failed", "path", path, "err", err)
	}
}

// loadPersistedContextBaseline reads the persisted anchor back from the
// session's .jsonl.meta sidecar and applies the tl1570 validity gates:
//
//   - 24h trust window (contextBaselinePersistTTL) on the sampling stamp;
//   - provider match: the anchor prices the request destination it measured
//     (requestProviderName), same re-key semantics as the in-memory anchor;
//   - model match: a hot model switch inside one provider instance changes
//     destinationModelRef while providerName may stay equal — a mismatched
//     ref invalidates the anchor (双非空才比：旧锚缺 ref、目标无 ref 时放行
//     给 providerName 段兜底);
//   - structural sanity: positive promptTokens and coveredChars.
//
// The covered-chars view guard (coveredChars ≤ current view) lives in
// observedContextTokens where the live view is at hand — the anchor alone
// cannot check it.
func (a *Agent) loadPersistedContextBaseline(now time.Time) (*contextBaselineState, bool) {
	if a == nil {
		return nil, false
	}
	path := a.SessionPath()
	if path == "" {
		return nil, false
	}
	m, ok, err := LoadBranchMeta(path)
	if err != nil || !ok || m.ContextBaseline == nil {
		// Missing meta / unreadable sidecar / no anchor yet: a legitimate
		// no-anchor state (fresh session, or pre-tl1570 build wrote this
		// meta) — the caller keeps today's estimate fallback.
		return nil, false
	}
	rec := m.ContextBaseline
	if rec.PromptTokens <= 0 || rec.CoveredChars <= 0 {
		return nil, false
	}
	sampled := rec.SampledAt
	if sampled.IsZero() || sampled.Before(now.Add(-contextBaselinePersistTTL)) {
		return nil, false
	}
	s := metaToContextBaselineState(rec)
	if cur := a.requestProviderName(); cur != s.providerName {
		return nil, false
	}
	if curRef := a.destinationModelRef(); curRef != "" && s.modelRef != "" && curRef != s.modelRef {
		return nil, false
	}
	return s, true
}

func metaToContextBaselineState(rec *ContextBaselineMeta) *contextBaselineState {
	return &contextBaselineState{
		providerName:     rec.ProviderName,
		modelRef:         rec.ModelRef,
		promptTokens:     rec.PromptTokens,
		completionTokens: rec.CompletionTokens,
		coveredChars:     rec.CoveredChars,
		imageTokens:      rec.ImageTokens,
		sampledAt:        rec.SampledAt,
	}
}
