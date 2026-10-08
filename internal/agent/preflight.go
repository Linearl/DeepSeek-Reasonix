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
// projection plus any post-projection appends, otherwise the full canonical
// transcript. LocalOnly stripping still happens in prepareSamplingRequest.
func (a *Agent) modelVisibleMessages() []provider.Message {
	if a == nil || a.sess.conversation == nil {
		return nil
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	if projectionValid(st, msgs, a.currentPromptCacheKey()) {
		if visible := modelVisibleFromProjection(st.Projection, msgs); len(visible) > 0 {
			return visible
		}
	}
	return msgs
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
// lineage-changing operations (rewind, branch, fork, system/model change).
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

// projectionRebuildTimeout bounds the background rebuild so a wedged summary
// stream cannot pin a goroutine (and the singleflight slot) indefinitely.
const projectionRebuildTimeout = 5 * time.Minute

// kickProjectionRebuild restores a projection in the background right after an
// invalidation or sidecar drop left the session without one (task 549). The
// old behavior waited for the next sampling round, so any request or readout
// in the meantime measured the FULL canonical transcript — on a large session
// that is the canonical-level spike the context panel showed. The rebuild is
// gated to sessions already at or above the compaction trigger: below it the
// canonical view is what the next request carries anyway, and folding would be
// unforced work. Prepare single-flights on compactionRunMu, and
// rebuildPending deduplicates the kicks themselves so a burst of rewrites
// (multi-writer rewind storms) cannot queue one summary per invalidation.
func (a *Agent) kickProjectionRebuild(reason string) {
	if a == nil || a.svc.prov == nil {
		return
	}
	fold := a.compactTrigger()
	if fold <= 0 || a.ContextUsedTokens() < fold {
		return
	}
	if !a.sess.rebuildPending.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.sess.rebuildPending.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), projectionRebuildTimeout)
		defer cancel()
		slog.Info("agent: rebuilding context projection after invalidation",
			"writer", SessionWriterID(), "reason", reason)
		if _, err := a.contextManager().Prepare(ctx, ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
			slog.Warn("agent: post-invalidation projection rebuild failed", "reason", reason, "err", err)
			return
		}
		slog.Info("agent: post-invalidation projection rebuilt", "reason", reason)
	}()
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
		if projectionValid(st, msgs, a.currentPromptCacheKeyLocked()) {
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
	valid := len(st.Projection.Messages) > 0 && projectionValid(st, msgs, key)
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
