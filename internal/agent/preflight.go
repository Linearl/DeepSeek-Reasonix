package agent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/provider"
)

// ErrCompactionRequired is returned when the prompt exceeds the provider limit
// and compaction could not produce a usable projection. Callers may retry.
var ErrCompactionRequired = errors.New("context exceeds provider limit and compaction failed")

// ErrNoFoldableRegion is the terminal compaction outcome (task 424): the
// context is over the maintenance threshold but nothing foldable remains, so
// retrying over the same session state can never succeed. The desktop
// cold-cache loop parks such sessions until new activity re-arms them instead
// of retrying every tick. The text is the historical reason string so wrapped
// messages stay byte-identical.
var ErrNoFoldableRegion = errors.New("context is above the maintenance threshold but no foldable region remains")

// modelVisibleMessages returns the provider-bound message list: a valid
// projection plus any post-projection appends, otherwise a view over the full
// canonical transcript. LocalOnly stripping still happens in
// prepareSamplingRequest.
//
// 任务719: an unusable projection no longer serves the raw canonical transcript
// unconditionally — once it outgrows the hard input ceiling the view degrades
// to a bounded recent-tail window (boundedTailView), so the request cannot
// overflow the window while compaction rebuilds the projection.
func (a *Agent) modelVisibleMessages() []provider.Message {
	if a == nil || a.sess.conversation == nil {
		return nil
	}
	msgs, version := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	if projectionValid(st, msgs) {
		// 任务638: a lineage-key change (model hot switch) must not invalidate
		// the fold — finalize the namespace rebind on the wire path so the view
		// stays projected and the destination's cache namespace takes over.
		a.rebindProjectionLineage(st, msgs)
		if visible := modelVisibleFromProjection(st.Projection, msgs); len(visible) > 0 {
			// 任务719: the projection is authoritative again — forget the
			// degraded-window evaluation so a later episode starts fresh.
			a.sess.tailView.clear()
			return visible
		}
	}
	return a.boundedTailView(msgs, version)
}

// rebindProjectionLineage follows a lineage-key change (任务638 model hot
// switch): when the projection body still matches the canonical covered
// prefix, the state's cache namespace is rewritten to the current destination
// key and the sidecar is republished — the runtime counterpart of
// LoadProjectionSidecar's key-change fallback, so both entry points share one
// policy. The strong reasoning-replay overlay is cleared with the namespace:
// its repair cutoff is indexed against the old model's thinking wire shape,
// while the projection body itself (model-agnostic) stays.
func (a *Agent) rebindProjectionLineage(st CompactionState, msgs []provider.Message) {
	if len(st.Projection.Messages) == 0 {
		return
	}
	key := a.currentPromptCacheKey()
	if key == "" {
		return
	}
	// Cheap gate first: the common (unchanged lineage) case is a string
	// compare; the covered-prefix hash only runs when the key actually moved.
	if normalized, ok := lineageKeyCompatible(st.PromptCacheKey, key); ok && normalized == st.PromptCacheKey {
		return
	}
	if !projectionContentValid(st, msgs) {
		// Real content drift: the invalidation flow owns the drop — a rebind
		// must never resurrect a stale fold.
		return
	}
	a.sess.compactionMu.Lock()
	// Re-check under the lock: a concurrent writer may have replaced the state
	// (new projection generation) between the snapshot and now.
	if a.sess.compactionState.Projection.ProjectionVersion != st.Projection.ProjectionVersion {
		a.sess.compactionMu.Unlock()
		return
	}
	a.sess.compactionState.PromptCacheKey = key
	err := a.persistCompactionStateLocked()
	a.sess.compactionMu.Unlock()
	// The overlay is thinking-format specific; the projected history is not.
	a.sess.clearReasoningReplayStrongProjection()
	if err != nil {
		slog.Warn("agent: persist rebound projection lineage", "err", err)
	}
	slog.Info("agent: context projection rebound to current lineage",
		"writer", SessionWriterID(), "projection_version", st.Projection.ProjectionVersion)
}

func (a *Agent) currentProjectionVersion() uint64 {
	if a == nil {
		return 0
	}
	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	return a.sess.compactionState.Projection.ProjectionVersion
}

// currentPromptCacheKey is the lineage key for the bound session + model.
func (a *Agent) currentPromptCacheKey() string {
	if a == nil {
		return ""
	}
	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	return a.currentPromptCacheKeyLocked()
}

func (a *Agent) currentPromptCacheKeyLocked() string {
	// Task 602: the key carries the effective ref so a hot switch lands the
	// destination's cache namespace from its first request.
	return promptCacheKey(a.workspaceID, BranchID(a.sess.path), a.destinationModelRef())
}

// InvalidateProjection drops the in-memory and on-disk projection after
// lineage-changing operations (rewind, branch, fork). 任务638: a model change
// is not on this list — it rebinds the cache namespace (rebindProjectionLineage)
// and keeps a content-valid projection.
func (a *Agent) InvalidateProjection() {
	a.invalidateProjection("explicit")
}

// invalidateProjection is InvalidateProjection with the trigger recorded for
// the drop log (task 549): invalidation is the moment the model-visible view
// falls back to the full canonical transcript, so the drop must leave a trail.
func (a *Agent) invalidateProjection(reason string) {
	if a == nil {
		return
	}
	// A strong reasoning-replay overlay is indexed against the old canonical
	// history. Clear it together with the compaction projection so rewind,
	// branch, and model/system lineage changes cannot reuse a stale anchor.
	a.sess.clearReasoningReplayStrongProjection()
	a.sess.compactionMu.Lock()
	path := a.sess.path
	hadProjection := len(a.sess.compactionState.Projection.Messages) > 0
	a.sess.compactionState = CompactionState{}
	a.sess.compactionMu.Unlock()
	if hadProjection {
		var msgs []provider.Message
		if a.sess.conversation != nil {
			msgs, _ = a.sess.conversation.snapshotMessagesVersion()
		}
		slog.Info("agent: context projection invalidated",
			"writer", SessionWriterID(), "reason", reason,
			"msgs", len(msgs),
			"canonical_tokens", estimateMessagesTokens(modelInputMessages(msgs)))
	} else {
		slog.Debug("agent: context projection already absent on invalidation",
			"writer", SessionWriterID(), "reason", reason)
	}
	a.sess.compaction.stuck = false
	a.sess.compaction.stuckInputHash = ""
	a.sess.compaction.consecutive = 0
	a.sess.compaction.failedTurn.Store(0)
	a.sess.compaction.lastTurn.Store(0)
	// 任务516④: a lineage change starts a new fallback episode — let its
	// first maintenance check warn immediately instead of waiting out the
	// previous episode's throttle.
	a.sess.compaction.fallbackWarnAt = time.Time{}
	if path != "" {
		if err := RemoveCompactionState(path); err != nil {
			slog.Warn("agent: remove context projection", "err", err)
		}
	}
	a.kickProjectionRebuild(reason)
}

// projectionRebuildTimeout bounds ONE Prepare round of the background rebuild
// so a wedged summary stream cannot pin a goroutine (and the singleflight
// slot) indefinitely.
const projectionRebuildTimeout = 5 * time.Minute

// 任务813 rebuild-episode bounds. An oversized session (调研-2 shape: canonical
// several windows past the hard ceiling) rarely returns under the fold trigger
// in one Prepare round, so the kick loops bounded rounds until the projection
// is usable again and the visible view sits back under the fold trigger.
// Every round folds at most the pressure ladder's segments, and each segment
// is capped to a safe summary prefix (#9572) — the rebuild is segmented by
// construction, never one full-transcript summary. The round budget is the
// hard spending cap; past it the installed segments persist and the normal
// track (next request's pressure preflight, cold-cache tick) continues.
const (
	projectionRebuildMaxRounds   = 4 // Prepare transactions per kick episode
	projectionRebuildMaxFailures = 2 // consecutive failed rounds before giving up
)

// kickProjectionRebuild restores a projection in the background right after an
// invalidation or sidecar drop left the session without one (task 549). The
// old behavior waited for the next sampling round, so any request or readout
// in the meantime measured the FULL canonical transcript — on a large session
// that is the canonical-level spike the context panel showed.
//
// The kick is gated to sessions at or above the compaction trigger, with one
// 任务813 exception: the degraded tail view (任务719) caps the visible
// estimate at 0.60x window — below the 0.80x fold trigger — so the bare
// ContextUsedTokens gate silently skipped the kick for exactly the oversized
// invalid-projection sessions the rebuild exists for (the 调研-2 regression:
// the panel pinned at "投影失效 · 全量回退中" with no background recovery,
// because the degraded view hid the pressure the gate looks for). While a
// degraded episode is active — tail view live, meaning the projection is
// unusable and the canonical view is past the hard ceiling — the rebuild is
// always forced. Prepare single-flights on compactionRunMu, and
// rebuildPending deduplicates the kicks themselves so a burst of rewrites
// (multi-writer rewind storms) cannot queue one summary per invalidation.
func (a *Agent) kickProjectionRebuild(reason string) {
	if a == nil || a.svc.prov == nil {
		return
	}
	fold := a.compactTrigger()
	if fold <= 0 {
		return
	}
	// ContextUsedTokens first: evaluating it is what builds (and activates)
	// the degraded tail view on a fresh episode, so the active() read below
	// sees the episode this kick is deciding about.
	used := a.ContextUsedTokens()
	degraded := a.sess.tailView.active()
	if used < fold && !degraded {
		return
	}
	if !a.sess.rebuildPending.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.sess.rebuildPending.Store(false)
		slog.Info("agent: rebuilding context projection after invalidation",
			"writer", SessionWriterID(), "reason", reason)
		failures := 0
		for round := 1; round <= projectionRebuildMaxRounds; round++ {
			if a.projectionRebuildSettled() {
				slog.Info("agent: post-invalidation projection rebuilt",
					"reason", reason, "rounds", round)
				return
			}
			// 任务813: a round that neither errors nor moves the projection
			// version is parked behind the maintenance gates (blocked receipt,
			// stuck view) or has nothing foldable — burning the remaining
			// rounds against it changes nothing, so the episode stops and the
			// normal retry rhythm owns the next attempt.
			startVersion := a.currentProjectionVersion()
			ctx, cancel := context.WithTimeout(context.Background(), projectionRebuildTimeout)
			_, err := a.contextManager().Prepare(ctx, ContextPreparePolicy{Trigger: CompactionTriggerPressure})
			cancel()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					// Shutdown owns the goroutine; neither a failure nor a retry.
					return
				}
				failures++
				// 任务813: every installed segment is durable, so a failed round
				// retries from the committed prefix instead of giving the episode
				// up after one try (the pre-813 single-shot shape).
				if failures >= projectionRebuildMaxFailures {
					slog.Warn("agent: post-invalidation projection rebuild failed",
						"reason", reason, "rounds", round, "consecutive_failures", failures, "err", err)
					return
				}
				slog.Warn("agent: post-invalidation projection rebuild round failed — retrying",
					"reason", reason, "rounds", round, "err", err)
				continue
			}
			failures = 0
			if a.currentProjectionVersion() == startVersion {
				slog.Warn("agent: post-invalidation projection rebuild parked — no progress this round",
					"reason", reason, "rounds", round)
				return
			}
		}
		if a.projectionRebuildSettled() {
			slog.Info("agent: post-invalidation projection rebuilt",
				"reason", reason, "rounds", projectionRebuildMaxRounds)
			return
		}
		// Round budget exhausted with the view still above the fold trigger.
		// The installed segments persist; the normal track (next request's
		// pressure preflight, cold-cache tick) continues from here — the
		// session never returns to the unbounded full-canonical fallback.
		slog.Warn("agent: post-invalidation projection rebuild round budget exhausted — normal track continues",
			"reason", reason, "rounds", projectionRebuildMaxRounds)
	}()
}

// projectionRebuildSettled reports whether the rebuild episode is over: a
// usable projection is installed again AND the visible view sits back under
// the fold trigger, so the normal compaction track owns the session from here.
// A valid projection alone is not enough — a mid-catch-up fold leaves a large
// spliced tail that the next rounds still need to shrink.
func (a *Agent) projectionRebuildSettled() bool {
	if a == nil || a.sess.conversation == nil {
		return true
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	if !projectionValid(st, msgs) {
		return false
	}
	visible := modelVisibleFromProjection(st.Projection, msgs)
	if len(visible) == 0 {
		return false
	}
	return a.estimatedVisibleRequestTokens(visible) < a.compactTrigger()
}

// InvalidateProjectionIfStale keeps the projection when it still matches the
// current transcript and performs the full invalidation otherwise. History
// rewrites that only touch messages past CoveredCount keep their fold.
func (a *Agent) InvalidateProjectionIfStale() {
	if a == nil {
		return
	}
	// This helper is called after a history rewrite even when the existing
	// compaction fold remains valid (for example a tail-only rewind). The
	// reasoning-replay overlay still belongs to the pre-rewrite shape.
	a.sess.clearReasoningReplayStrongProjection()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	if len(st.Projection.Messages) > 0 && a.sess.conversation != nil {
		msgs, _ := a.sess.conversation.snapshotMessagesVersion()
		if projectionValid(st, msgs) {
			a.sess.compactionMu.Unlock()
			return
		}
	}
	a.sess.compactionMu.Unlock()
	a.invalidateProjection("stale_history_rewrite")
}

// LoadProjectionSidecar loads the context sidecar into the agent. Corrupt or
// incompatible state is dropped so the next request rebuilds from canonical.
// Sidecars whose PromptCacheKey does not match the current agent lineage are
// discarded without deleting the file (another model may still own it).
func (a *Agent) LoadProjectionSidecar(sessionPath string) {
	if a == nil {
		return
	}
	a.sess.compactionMu.Lock()
	a.sess.path = sessionPath
	a.sess.compactionState = CompactionState{}
	a.sess.checkpointState = "none"
	a.sess.compactionMu.Unlock()
	if sessionPath == "" {
		a.resetCompactionState()
		return
	}
	st, ok, err := LoadCompactionState(sessionPath)
	if err != nil {
		slog.Warn("agent: load context projection", "err", err)
		_ = RemoveCompactionState(sessionPath)
		a.resetCompactionState()
		a.kickProjectionRebuild("sidecar_corrupt")
		return
	}
	if !ok {
		a.resetCompactionState()
		a.kickProjectionRebuild("sidecar_missing")
		return
	}
	var msgs, preRepair []provider.Message
	if a.sess.conversation != nil {
		msgs, preRepair = a.sess.conversation.projectionValidationMessages()
	}
	needsNormalization := migratePromotedCoveredPrefixHash(&st, msgs)
	a.sess.compactionMu.Lock()
	key := a.currentPromptCacheKeyLocked()
	normalized, keyOK := lineageKeyCompatible(st.PromptCacheKey, key)
	// Keep receipt-only blocked/failed sidecars (no projection body) and legacy
	// top-level BlockedInputHash so generation-scoped suppressions survive restart.
	hasMaintenanceSignal := st.Projection.CoveredPrefixHash != "" ||
		st.BlockedInputHash != "" ||
		(st.LastReceipt != nil && (st.LastReceipt.Status == "blocked" || st.LastReceipt.Status == "failed" ||
			st.LastReceipt.Status == "applied"))
	if key != "" && !keyOK {
		// Lineage key changed (upgrade, model/workspace switch). Rebind when
		// the projection body still matches the canonical covered prefix.
		contentValid := projectionContentValid(st, msgs)
		if !contentValid && migrateLegacyCoveredPrefixHash(&st, msgs, preRepair) {
			contentValid = true
			needsNormalization = true
		}
		if contentValid {
			normalized, keyOK = key, true
		}
	}
	if (key != "" && !keyOK) || !hasMaintenanceSignal {
		a.sess.compactionState = CompactionState{}
		a.sess.checkpointState = "none"
		a.sess.compactionMu.Unlock()
		// Task 549: record every drop that forces the full-canonical fallback —
		// a lineage change (model/workspace switch, upgrade) or a sidecar with
		// no usable projection body. This was invisible before: the panel then
		// showed canonical-level numbers with no trail to explain them.
		reason := "no_maintenance_signal"
		if !keyOK {
			reason = "lineage_key_changed"
		}
		slog.Info("agent: context projection not restored",
			"writer", SessionWriterID(), "reason", reason,
			"stored_key", st.PromptCacheKey != "", "key_matched", keyOK,
			"msgs", len(msgs),
			"canonical_tokens", estimateMessagesTokens(modelInputMessages(msgs)))
		a.kickProjectionRebuild(reason)
		return
	}
	// Only rewrite legacy native-editing lineage keys; exact matches stay pure-read.
	if keyOK && key != "" && normalized != st.PromptCacheKey {
		st.PromptCacheKey = normalized
		needsNormalization = true
	}
	// Only mark restored when the projection still matches the transcript.
	if !projectionContentValid(st, msgs) && migrateLegacyCoveredPrefixHash(&st, msgs, preRepair) {
		needsNormalization = true
	}
	valid := len(st.Projection.Messages) > 0 && projectionValid(st, msgs)
	if !valid && len(st.Projection.Messages) > 0 {
		// Keep blocked receipts / telemetry; drop unusable projection body.
		st.Projection = ContextProjection{}
	}
	a.sess.compactionState = st
	if valid {
		a.sess.checkpointState = "restored"
		if needsNormalization {
			if err := a.persistCompactionStateLocked(); err != nil {
				slog.Warn("agent: persist normalized projection lineage", "err", err)
			}
		}
	} else {
		a.sess.checkpointState = "none"
	}
	a.sess.compactionMu.Unlock()
	if !valid && len(st.Projection.Messages) == 0 {
		// The sidecar carried receipts only (or an unusable body that was just
		// dropped): the next view is full canonical until something folds.
		a.kickProjectionRebuild("projection_body_invalid")
	}
}

// lineageKeyCompatible reports whether a stored PromptCacheKey still belongs to
// the current session/model lineage. Legacy native context-editing keys used a
// "|context-editing-native-..." suffix on an otherwise matching base key.
func lineageKeyCompatible(stored, current string) (normalized string, ok bool) {
	stored, current = strings.TrimSpace(stored), strings.TrimSpace(current)
	if current == "" {
		// Unknown current lineage: accept any stored key as-is.
		return stored, true
	}
	if stored == "" {
		return "", false
	}
	if stored == current {
		return current, true
	}
	const nativeSuffix = "|context-editing-native"
	if strings.HasPrefix(stored, current+nativeSuffix) {
		return current, true
	}
	if i := strings.Index(stored, nativeSuffix); i > 0 && stored[:i] == current {
		return current, true
	}
	return "", false
}

func (a *Agent) resetCompactionState() {
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{}
	a.sess.checkpointState = "none"
	a.sess.compactionMu.Unlock()
}

// BindSessionPath rebinds projection persistence to path. When loadSidecar is
// true the existing sidecar is loaded (resume/switch); otherwise in-memory
// projection is cleared without deleting another session's sidecar file.
func (a *Agent) BindSessionPath(path string, loadSidecar bool) {
	if a == nil {
		return
	}
	if loadSidecar {
		a.LoadProjectionSidecar(path)
		return
	}
	a.sess.compactionMu.Lock()
	a.sess.path = path
	a.sess.compactionState = CompactionState{}
	a.sess.checkpointState = "none"
	a.sess.cacheState = CacheStateUnknown
	a.sess.compactionMu.Unlock()
	a.sess.compaction.stuck = false
	a.sess.compaction.stuckInputHash = ""
	a.sess.compaction.consecutive = 0
	a.sess.compaction.failedTurn.Store(0)
	a.sess.compaction.lastTurn.Store(0)
	a.sess.compaction.fallbackWarnAt = time.Time{}
}

// SetSessionPath binds the transcript path used for projection persistence.
func (a *Agent) SetSessionPath(path string) {
	if a == nil {
		return
	}
	a.sess.compactionMu.Lock()
	a.sess.path = path
	a.sess.compactionMu.Unlock()
}

// SessionPath returns the bound transcript path.
func (a *Agent) SessionPath() string {
	if a == nil {
		return ""
	}
	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	return a.sess.path
}

// SetCacheState records the resume-time cache estimate without rewriting history.
func (a *Agent) SetCacheState(state string) {
	if a == nil {
		return
	}
	switch state {
	case CacheStateWarm, CacheStateCold, CacheStateUnknown:
	default:
		state = CacheStateUnknown
	}
	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	a.sess.cacheState = state
	if a.sess.compactionState.SchemaVersion == 0 && len(a.sess.compactionState.Projection.Messages) == 0 {
		a.sess.compactionState.SchemaVersion = compactionStateSchemaCurrent
	}
	a.sess.compactionState.LastCacheState = state
	a.sess.compactionState.UpdatedAt = time.Now().UTC()
}

// CacheState returns the last estimated cache warm/cold/unknown label.
func (a *Agent) CacheState() string {
	if a == nil {
		return CacheStateUnknown
	}
	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	if a.sess.cacheState == "" {
		return CacheStateUnknown
	}
	return a.sess.cacheState
}

func (a *Agent) persistCompactionStateLocked() error {
	if a.sess.path == "" {
		return nil
	}
	return SaveCompactionState(a.sess.path, a.sess.compactionState)
}

// promptCacheKey builds a stable lineage key for session + model identity.
// It deliberately excludes message counts, timestamps, and projection hashes.
func promptCacheKey(workspaceID, sessionLineage, modelRef string) string {
	parts := []string{
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(sessionLineage),
		strings.TrimSpace(modelRef),
	}
	return strings.Join(parts, "|")
}
