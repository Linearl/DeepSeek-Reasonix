package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务516 验收：candidate 超 ceiling 时接受部分进度，打破「同尺寸反复被拒」死锁。

// TestCheckpointCandidateAboveCeilingInstallsPartialProgress pins the task 516
// acceptance contract: a candidate below source but at or above the physical
// input ceiling is real progress and is accepted for automatic maintenance;
// only a candidate without real savings stays rejected.
func TestCheckpointCandidateAboveCeilingInstallsPartialProgress(t *testing.T) {
	a := New(&denseTokenizerProvider{window: 20_000}, tool.NewRegistry(), longASCIISession(2),
		Options{ContextWindow: 20_000, CompactRatio: 0.8}, event.Discard)
	hard := a.hardInputCeiling()
	if hard <= 0 {
		t.Fatalf("hardInputCeiling = %d, fixture cannot exercise the ceiling path", hard)
	}
	source := hard + 1000

	// candidate ∈ [ceiling, source)：真实降低视图，必须接受（516 主修契约）。
	if err := a.acceptCheckpointCandidate(CompactionTriggerPressure, source, hard+100); err != nil {
		t.Fatalf("candidate in [ceiling, source) was rejected: %v — the 307 dead end is back", err)
	}
	// candidate ≥ source：无真实收益，仍拒绝。
	if err := a.acceptCheckpointCandidate(CompactionTriggerPressure, source, source); !errors.Is(err, errCheckpointRejected) {
		t.Fatalf("candidate >= source: err = %v, want errCheckpointRejected", err)
	}
	// 手动压缩从不做 ceiling 检查（既有语义保持）。
	if err := a.acceptCheckpointCandidate(CompactionTriggerManual, source, hard+100); err != nil {
		t.Fatalf("manual trigger must skip the ceiling check: %v", err)
	}
	// ceiling 以下照常接受。
	if err := a.acceptCheckpointCandidate(CompactionTriggerPressure, source, hard-100); err != nil {
		t.Fatalf("candidate below ceiling: %v", err)
	}
}

// TestPartialFoldProgressConvergesOverCeiling is the task 516 acceptance
// scenario: a view several times the window whose single fold is cut down by
// the #9572 summary-input cap, so every candidate lands between the ceiling and
// the source. The old path rejected such candidates whole (fold_installed=false,
// same size re-rejected next round); the fixed path installs the partial fold,
// keeps folding from the smaller base, and converges inside the window without
// a single physical-ceiling rejection and without the lossy truncation rescue.
func TestPartialFoldProgressConvergesOverCeiling(t *testing.T) {
	prov := &denseTokenizerProvider{window: 20_000}
	sess := longASCIISession(24)
	a := New(prov, tool.NewRegistry(), sess, Options{ContextWindow: 20_000, CompactRatio: 0.8}, event.Discard)
	source, hard := a.ContextUsedTokens(), a.hardInputCeiling()
	if source < hard {
		t.Fatalf("fixture estimates %d tokens, below the ceiling %d; add turns", source, hard)
	}

	var ceilingRejects, folds int
	a.svc.sink = event.FuncSink(func(e event.Event) {
		switch e.Kind {
		case event.ContextMaintenanceEvent:
			if e.Maintenance != nil && strings.Contains(e.Maintenance.Reason, "physical ceiling") {
				ceilingRejects++
			}
		case event.CompactionDone:
			if e.Compaction.Summary != "" {
				folds++
			}
		}
	})

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("prepare = %v, want partial-progress folds to converge", err)
	}
	if final := a.ContextUsedTokens(); final >= hard {
		t.Fatalf("view stayed at %d tokens, at/above the ceiling %d — no convergence", final, hard)
	}
	if ceilingRejects != 0 {
		t.Fatalf("%d physical-ceiling rejections, want 0 (the repeated-rejection deadlock shape)", ceilingRejects)
	}
	if folds < 2 {
		t.Fatalf("%d summary folds installed, want >=2 (partial progress across rounds, not one-shot)", folds)
	}
	if r := a.sess.compactionState.LastReceipt; r == nil || r.Status != "applied" || r.Action != "summary" {
		t.Fatalf("receipt = %+v, want the final fold applied (not a truncation rescue)", r)
	}
	if latestDigest(a.sess.compactionState.Projection.Messages) == "" {
		t.Fatal("converged without installing a digest")
	}
}

// TestProjectionFallbackObservation covers the 任务516④ fallback watch: the
// full-canonical fallback gets one throttled warning naming the reason, quiet
// on normal fresh sessions, re-armed once a projection is valid again.
func TestProjectionFallbackObservation(t *testing.T) {
	a := New(&denseTokenizerProvider{window: 20_000}, tool.NewRegistry(), longASCIISession(2),
		Options{ContextWindow: 20_000, CompactRatio: 0.8}, event.Discard)

	// 空投影 + 视图低于物理上限：新会话常态，不告警。
	a.observeProjectionFallback(a.ContextUsedTokens())
	if !a.sess.compaction.fallbackWarnAt.IsZero() {
		t.Fatal("a fresh small session warned about the canonical fallback")
	}

	// 空投影 + 视图超过物理上限（2.7M/1M 形状）：告警一次并进入冷却。
	a.observeProjectionFallback(a.hardInputCeiling() + 1000)
	first := a.sess.compaction.fallbackWarnAt
	if first.IsZero() {
		t.Fatal("an over-ceiling no-projection view did not warn")
	}
	a.observeProjectionFallback(a.hardInputCeiling() + 2000)
	if !a.sess.compaction.fallbackWarnAt.Equal(first) {
		t.Fatal("the cooldown did not hold within the same episode")
	}

	// 投影体存在但血统键失配：不受 est 门槛，解除冷却后立即告警。
	a.sess.compaction.fallbackWarnAt = time.Time{}
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{
		PromptCacheKey: "other|lineage|key",
		Projection: ContextProjection{
			Messages: []provider.Message{{Role: provider.RoleUser, Content: "folded"}}, CoveredCount: 1, CoveredPrefixHash: "x",
		},
	}
	a.sess.compactionMu.Unlock()
	a.observeProjectionFallback(10)
	if a.sess.compaction.fallbackWarnAt.IsZero() {
		t.Fatal("a lineage-key mismatch did not warn on its first check")
	}

	// 投影重新有效：解除冷却，下一事件可立即告警。
	a.sess.compactionMu.Lock()
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionState = CompactionState{
		SchemaVersion:  compactionStateSchemaCurrent,
		PromptCacheKey: a.currentPromptCacheKeyLocked(),
		Projection: ContextProjection{
			Messages:          []provider.Message{msgs[0]},
			CoveredCount:      1,
			CoveredPrefixHash: coveredPrefixHash(msgs, 1),
		},
	}
	a.sess.compactionMu.Unlock()
	a.observeProjectionFallback(a.ContextUsedTokens())
	if !a.sess.compaction.fallbackWarnAt.IsZero() {
		t.Fatal("a valid projection did not re-arm the fallback throttle")
	}
}
