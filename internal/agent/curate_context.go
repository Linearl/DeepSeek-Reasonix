package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// curateResultStatus values mirror the compress tool's vocabulary so a model
// that knows one tool reads the other without relearning.
const (
	curateStatusOK       = "ok"
	curateStatusRejected = "rejected"
)

const (
	maxCurateDropEntries = 32
	maxCurateNoteBytes   = 240
	// curatedNotePrefix marks a replaced tool result so a later reader (model
	// or human replay) can tell a curated note from a live result.
	curatedNotePrefix = "[curated] "
)

// minCurateInterval is the independent lightweight rate limit for the
// fine-grained curation tier (tl1378 增量1). It deliberately does NOT share
// the fold cooldown (minExplicitFoldInterval): curate's whole value is filling
// the gap that cooldown leaves — mid-window cleanup while a fold is
// rate-limited away — and each pass touches a handful of entries instead of
// rewriting the prefix, so it needs a tighter, cheaper guard. The CTXAgent
// paper's counter-example (unlimited curating costing ~20% MORE than doing
// nothing) is exactly the failure this interval bounds: every pass still
// invalidates the prompt cache from its earliest replaced entry onward.
// var, not const: tests collapse the window.
var minCurateInterval = 2 * time.Minute

// CurateContext implements the context-bound curate_context tool: replace
// individually named, already-processed tool results with one-line notes.
// Like CompressContext this installs a projection only; the canonical
// transcript stays untouched. Guardrails (tl1378 增量1):
//   - independent rate limit (minCurateInterval) — the paper's unbounded
//     curate is a measured net loss;
//   - completed turns only — the active turn's results stay live;
//   - every entry must resolve to exactly one visible tool result;
//   - the view must actually shrink ("would not be smaller", like compress).
func (a *Agent) CurateContext(ctx context.Context, req tool.CurateRequest) (tool.CurateResult, error) {
	drops := req.Drop
	if len(drops) == 0 {
		return tool.CurateResult{}, fmt.Errorf("curate_context: drop must name at least one tool result")
	}
	if len(drops) > maxCurateDropEntries {
		return tool.CurateResult{}, fmt.Errorf("curate_context: drop exceeds %d entries", maxCurateDropEntries)
	}
	seen := make(map[string]bool, len(drops))
	for i := range drops {
		drops[i].ID = strings.TrimSpace(drops[i].ID)
		drops[i].Note = strings.TrimSpace(drops[i].Note)
		if drops[i].ID == "" {
			return tool.CurateResult{}, fmt.Errorf("curate_context: drop[%d].id must not be empty", i)
		}
		if drops[i].Note == "" {
			return tool.CurateResult{}, fmt.Errorf("curate_context: drop[%d].note must not be empty", i)
		}
		if len(drops[i].Note) > maxCurateNoteBytes {
			return tool.CurateResult{}, fmt.Errorf("curate_context: drop[%d].note exceeds %d bytes", i, maxCurateNoteBytes)
		}
		if seen[drops[i].ID] {
			return tool.CurateResult{}, fmt.Errorf("curate_context: receipt %s named twice; name each result once", drops[i].ID)
		}
		seen[drops[i].ID] = true
	}

	// A curate rewrites part of the prompt prefix, so it costs the cache for
	// every later turn: hold model-driven curations to one per interval.
	if last := a.lastCurateAt.Load(); last != 0 {
		if elapsed := time.Since(time.Unix(0, last)); elapsed < minCurateInterval {
			wait := (minCurateInterval - elapsed).Round(time.Second)
			return tool.CurateResult{
				Status: curateStatusRejected,
				Reason: fmt.Sprintf("a curation already ran %s ago; curating again within %s would keep invalidating the prompt cache, try again in about %s", elapsed.Round(time.Second), minCurateInterval, wait),
			}, nil
		}
	}

	snap := a.snapshotExplicitCompression()
	// Curation only touches completed turns: the active turn's results are
	// exactly what the model is still working on (same boundary as compress's
	// "after" range).
	completedEnd := len(snap.visible)
	if active := a.activeTurnStart(snap.visible); active >= 0 {
		completedEnd = active
	}

	plan, err := planCurateProjection(a.task.ledger, snap.visible, completedEnd, drops)
	if err != nil {
		return tool.CurateResult{}, err
	}
	result, err := a.installCurateProjection(snap, plan)
	if err != nil {
		return tool.CurateResult{}, err
	}
	if result.Status == curateStatusOK {
		a.lastCurateAt.Store(time.Now().UnixNano())
	}
	return result, nil
}

// curatePlan is the resolved replacement set: one visible-view index per drop
// entry, with the claiming receipt id kept so error messages can name both.
type curatePlan struct {
	indexes map[int]string // view index -> curated note
}

// planCurateProjection resolves every drop entry against the model-visible
// view. Pure so the resolution rules are testable without a live session.
// A receipt resolves only when its call's tool result is visible in a
// completed turn; anything else fails the whole pass with retry guidance —
// a partial install would leave the model guessing which notes landed.
func planCurateProjection(ledger *evidence.Ledger, visible []provider.Message, completedEnd int, drops []tool.CurateDrop) (curatePlan, error) {
	byCallID := make(map[string]int, len(visible))
	for i, msg := range visible {
		if msg.Role == provider.RoleTool && msg.ToolCallID != "" && i < completedEnd {
			if _, dup := byCallID[msg.ToolCallID]; !dup {
				byCallID[msg.ToolCallID] = i
			}
		}
	}
	plan := curatePlan{indexes: make(map[int]string, len(drops))}
	claimedBy := make(map[int]string, len(drops))
	for _, drop := range drops {
		rec, ok := ledger.LookupReceipt(drop.ID)
		if !ok {
			return curatePlan{}, fmt.Errorf("curate_context: receipt %s is not one this session issued; cite the id a tool result ends with ([receipt r_…])", drop.ID)
		}
		idx, ok := byCallID[rec.ToolCallID]
		if !ok {
			return curatePlan{}, fmt.Errorf("curate_context: receipt %s has no completed-turn tool result in the current model context; results from the active turn or already-folded history do not qualify", drop.ID)
		}
		if first, dup := claimedBy[idx]; dup {
			return curatePlan{}, fmt.Errorf("curate_context: receipts %s and %s resolve to the same tool result", first, drop.ID)
		}
		claimedBy[idx] = drop.ID
		plan.indexes[idx] = drop.Note
	}
	if len(plan.indexes) == 0 {
		return curatePlan{}, fmt.Errorf("curate_context: no entry resolved to a replaceable tool result")
	}
	return plan, nil
}

// installCurateProjection installs the replaced view under the same CAS
// discipline as a fold. Status "rejected" (with a Reason) is a normal outcome:
// the request was valid but would not have shrunk the view.
func (a *Agent) installCurateProjection(snap explicitCompressionSnapshot, plan curatePlan) (tool.CurateResult, error) {
	a.sess.compactionRunMu.Lock()
	defer a.sess.compactionRunMu.Unlock()
	if !a.explicitCompressionSnapshotCurrent(snap) {
		return tool.CurateResult{}, errCompressStaleContext
	}
	projection := make([]provider.Message, len(snap.visible))
	for i, msg := range snap.visible {
		if note, hit := plan.indexes[i]; hit {
			replaced := msg
			replaced.Content = curatedNotePrefix + note
			replaced.RawContent = ""
			replaced.Images = nil
			projection[i] = replaced
			continue
		}
		projection[i] = msg
	}
	sourceTokens := a.estimatedVisibleRequestTokens(snap.visible)
	projectionTokens := a.estimatedVisibleRequestTokens(projection)
	result := tool.CurateResult{
		Replaced:         len(plan.indexes),
		SourceTokens:     sourceTokens,
		ProjectionTokens: projectionTokens,
		SavedTokens:      max(0, sourceTokens-projectionTokens),
	}
	if projectionTokens >= sourceTokens {
		result.Status = curateStatusRejected
		result.Reason = "curated context would not be smaller; the named results are already too small to be worth a cache break"
		return result, nil
	}

	inputHash := providerVisibleFingerprint(modelInputMessages(snap.visible))
	outputHash := providerVisibleFingerprint(modelInputMessages(projection))
	state, err := a.commitCurateProjection(curateProjectionCommit{
		canonical: snap.canonical, projected: projection,
		transcriptVersion: snap.transcriptVersion, projectionVersion: snap.projectionVersion, generation: snap.generation,
		inputHash: inputHash, outputHash: outputHash,
		sourceTokens: sourceTokens, projectionTokens: projectionTokens,
		covered: len(snap.canonical), summaryHash: snap.summaryHash,
	})
	if err != nil {
		return tool.CurateResult{}, err
	}
	// The visible view rewrote results the model already read, so the turn
	// loop's identical-result detection starts from the new view (the same
	// reasoning as the compress path's clearResultFingerprints).
	a.turn.loop.clearResultFingerprints()
	slog.Info("agent: context curated",
		"replaced", len(plan.indexes), "source_tokens", sourceTokens,
		"projection_tokens", projectionTokens, "saved_tokens", result.SavedTokens,
		"projection_version", state.Projection.ProjectionVersion)
	result.Status = curateStatusOK
	result.Reason = ""
	return result, nil
}

type curateProjectionCommit struct {
	canonical, projected           []provider.Message
	transcriptVersion              uint64
	projectionVersion, generation  uint64
	inputHash, outputHash          string
	sourceTokens, projectionTokens int
	covered                        int
	summaryHash                    string
}

// commitCurateProjection CAS-installs a curation projection under compactionMu
// with the identical stale-check as commitSummaryProjection: transcript
// version/hash, projection version, and generation must all still match. The
// receipt's Action is "curate" and the prior summary digest is preserved, so
// downstream consumers keep reading the last real summary as the last summary.
func (a *Agent) commitCurateProjection(commit curateProjectionCommit) (CompactionState, error) {
	now := time.Now().UTC()
	coveredHash := coveredPrefixHash(commit.canonical, commit.covered)
	receipt := &ContextMaintenanceReceipt{
		OperationID: fmt.Sprintf("curate-%d-%s", commit.projectionVersion+1, commit.outputHash), Status: "applied",
		Action: "curate", Trigger: CompactionTriggerTool, SourceProjection: commit.projectionVersion,
		ProjectionVersion: commit.projectionVersion + 1, CoveredCount: commit.covered, CoveredPrefixHash: coveredHash,
		InputHash: commit.inputHash, OutputHash: commit.outputHash, InputTokens: commit.sourceTokens,
		ResultTokens: commit.projectionTokens, SavedTokens: max(0, commit.sourceTokens-commit.projectionTokens),
		SummaryHash: commit.summaryHash, CacheBreak: true, CreatedAt: now,
	}
	state := CompactionState{
		SchemaVersion: compactionStateSchemaCurrent, TranscriptVersion: commit.transcriptVersion,
		Generation: commit.generation + 1, PromptCacheKey: a.currentPromptCacheKey(),
		Projection: ContextProjection{
			Messages: commit.projected, TranscriptVersion: commit.transcriptVersion,
			ProjectionVersion: commit.projectionVersion + 1, CoveredCount: commit.covered, CoveredPrefixHash: coveredHash,
			PinnedContextHash: pinnedContextCoverageHash(commit.canonical, commit.covered),
			SummaryHash:       commit.summaryHash, SourceTokens: commit.sourceTokens, ProjectionTokens: commit.projectionTokens,
			ViewInputHash: commit.inputHash, ViewOutputHash: commit.outputHash, CreatedAt: now,
		},
		LastReceipt: receipt, UpdatedAt: now,
	}
	a.sess.compactionMu.Lock()
	current, currentVersion := a.sess.conversation.snapshotMessagesVersion()
	if currentVersion != commit.transcriptVersion ||
		len(current) != len(commit.canonical) ||
		coveredPrefixHash(current, len(current)) != coveredPrefixHash(commit.canonical, len(commit.canonical)) ||
		a.sess.compactionState.Projection.ProjectionVersion != commit.projectionVersion ||
		a.sess.compactionState.Generation != commit.generation {
		a.sess.compactionMu.Unlock()
		return CompactionState{}, errCompressStaleContext
	}
	prev := a.sess.compactionState
	a.sess.compactionState = state
	if err := a.persistCompactionStateLocked(); err != nil {
		a.sess.compactionState = prev
		a.sess.compactionMu.Unlock()
		if err == errCompressStaleContext {
			return CompactionState{}, err
		}
		return CompactionState{}, fmt.Errorf("persist projection: %w", err)
	}
	a.sess.checkpointState = "applied"
	curatedReceipt := state.LastReceipt
	a.sess.compactionMu.Unlock()
	a.emitContextMaintenance(curatedReceipt)
	return state, nil
}
