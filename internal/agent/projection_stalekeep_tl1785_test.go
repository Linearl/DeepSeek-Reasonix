package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1785SealedState builds a v4 sidecar sealed against msgs[:covered] with the
// full tl1651 per-message corpus, so the repair-drift attribution gates have
// evidence to read. tver rides both transcript_version levels the way the
// commit paths persist it.
func tl1785SealedState(path string, msgs []provider.Message, covered int, tver uint64) CompactionState {
	key := promptCacheKey("workspace", BranchID(path), "provider/model")
	return CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: tver,
		PromptCacheKey:    key,
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "system"},
				{Role: provider.RoleUser, Content: "[compacted context]"},
			},
			TranscriptVersion:    tver,
			ProjectionVersion:    7,
			CoveredCount:         covered,
			CoveredPrefixHash:    coveredPrefixHash(msgs, covered),
			CoveredMessageHashes: coveredMessageFingerprints(msgs, covered),
		},
	}
}

// tl1785RewriteFixture persists a five-message transcript plus a sidecar
// sealed against the PRE-rewrite bytes, simulating the tl1116 recurrence
// family: the covered prefix was rewritten after the fold was sealed (repair
// accumulated across earlier loads, or an installer's normalization rules),
// and the restart's own load finds no repair left to run (well-formed disk, so
// migrateLoadRepairedPrefixHash's pre-repair gate cannot fire).
func tl1785RewriteFixture(t *testing.T, rewrite func(msgs []provider.Message)) (path string, original []provider.Message) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "u1"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "draft v1"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "u2"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	original, _ = sess.snapshotMessagesVersion()
	if len(original) != 5 {
		t.Fatalf("fixture canonical length = %d, want 5", len(original))
	}
	if err := SaveCompactionState(path, tl1785SealedState(path, original, len(original), 453)); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}
	rewritten := append([]provider.Message(nil), original...)
	rewrite(rewritten)
	sess = sess.CloneWithMessages(rewritten)
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	return path, original
}

// TestTL1785RepairDriftKeepsBodyStale: an assistant-slot rewrite inside the
// covered prefix (the repair family's only touch surface) keeps the body
// marked stale instead of dropping it — the wire serves the bounded stale
// splice (fold + uncovered tail), never the full canonical fallback, and the
// on-disk sidecar stays untouched (pure-read load: no hash re-anchor, no
// corpus bless — the tl1770 乙案 migrate channel remains unimplemented).
func TestTL1785RepairDriftKeepsBodyStale(t *testing.T) {
	path, _ := tl1785RewriteFixture(t, func(msgs []provider.Message) {
		msgs[2].Content = "draft v1 (normalized)"
	})
	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "none" {
		t.Fatalf("checkpoint state = %q, want none (kept stale, not restored)", got)
	}
	visible := reopened.modelVisibleMessages()
	if len(visible) != 2 {
		t.Fatalf("visible messages = %d, want the 2-message stale splice (not the 5-message full canonical)", len(visible))
	}
	if got := visible[1].Content; got != "[compacted context]" {
		t.Fatalf("visible[1] = %q, want the stale fold body", got)
	}
	persisted, ok, err := LoadCompactionState(path)
	if err != nil || !ok {
		t.Fatalf("LoadCompactionState after keep: ok=%v err=%v", ok, err)
	}
	if len(persisted.Projection.CoveredMessageHashes) == 0 {
		t.Fatal("kept-stale load must not strip the sidecar corpus (pure-read)")
	}
	// tl1785 伴生修复: the transcript version trail survives the restart.
	if got := reopened.Session().TranscriptVersion(); got != 453 {
		t.Fatalf("transcript version after restart = %d, want floor 453 restored from the sidecar", got)
	}
}

// TestTL1785UserSlotRewriteStillDrops: a user-slot edit is real divergence —
// the repair family never rewrites user-visible bytes — so the body drops and
// the view falls back to the (under-ceiling) full canonical transcript, which
// is the REWRITTEN canonical (the user's actual on-disk history), not the
// pre-rewrite bytes the stale fold was sealed against.
func TestTL1785UserSlotRewriteStillDrops(t *testing.T) {
	path, original := tl1785RewriteFixture(t, func(msgs []provider.Message) {
		msgs[1].Content = "u1 rewritten by hand"
	})
	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "none" {
		t.Fatalf("checkpoint state = %q, want none", got)
	}
	visible := reopened.modelVisibleMessages()
	if len(visible) != len(original) {
		t.Fatalf("visible messages = %d, want the %d-message canonical fallback (body dropped)", len(visible), len(original))
	}
	expected := append([]provider.Message(nil), original...)
	expected[1].Content = "u1 rewritten by hand"
	for i := range visible {
		if visible[i].Content != expected[i].Content {
			t.Fatalf("visible[%d] = %q, want rewritten canonical %q", i, visible[i].Content, expected[i].Content)
		}
	}
}

// TestTL1785RepairDriftGateMatrix exercises the attribution gates directly:
// assistant/tool-slot divergence keeps, everything else (user/system edits,
// structural pipeline change, missing evidence, overflow, no divergence)
// fails closed.
func TestTL1785RepairDriftGateMatrix(t *testing.T) {
	base := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "u1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "package main"},
	}
	// cloneMsgs deep-copies ToolCalls slices so a mutation in one subtest can
	// never alias base's backing arrays (the shallow append copy would).
	cloneMsgs := func(msgs []provider.Message) []provider.Message {
		out := append([]provider.Message(nil), msgs...)
		for i := range out {
			out[i].ToolCalls = append([]provider.ToolCall(nil), out[i].ToolCalls...)
		}
		return out
	}
	seal := func(msgs []provider.Message) CompactionState {
		return CompactionState{Projection: ContextProjection{
			CoveredCount:         len(msgs),
			CoveredPrefixHash:    coveredPrefixHash(msgs, len(msgs)),
			CoveredMessageHashes: coveredMessageFingerprints(msgs, len(msgs)),
		}}
	}

	t.Run("assistant_slot_divergence_keeps", func(t *testing.T) {
		current := cloneMsgs(base)
		current[2].ToolCalls[0].Arguments = `{"path":"main.go","normalized":true}`
		got := projectionRepairDriftKeep(seal(base), current)
		if len(got) != 1 || got[0] != 2 {
			t.Fatalf("diverging slots = %v, want [2]", got)
		}
	})
	t.Run("tool_slot_divergence_keeps", func(t *testing.T) {
		current := cloneMsgs(base)
		current[3] = provider.Message{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "package main (truncated args closed)"}
		got := projectionRepairDriftKeep(seal(base), current)
		if len(got) != 1 || got[0] != 3 {
			t.Fatalf("diverging slots = %v, want [3]", got)
		}
	})
	t.Run("user_slot_divergence_drops", func(t *testing.T) {
		current := cloneMsgs(base)
		current[1].Content = "u1 rewritten"
		if got := projectionRepairDriftKeep(seal(base), current); got != nil {
			t.Fatalf("diverging slots = %v, want nil (user edit is real divergence)", got)
		}
	})
	t.Run("system_slot_divergence_drops", func(t *testing.T) {
		current := cloneMsgs(base)
		current[0].Content = "system v2"
		if got := projectionRepairDriftKeep(seal(base), current); got != nil {
			t.Fatalf("diverging slots = %v, want nil (system edit is real divergence)", got)
		}
	})
	t.Run("structural_pipeline_change_drops", func(t *testing.T) {
		// Two calls with two results on the current side vs a one-call corpus:
		// the sanitized pipeline grew 4 → 5 — a structural rewrite, never
		// repair drift.
		current := []provider.Message{
			base[0], base[1],
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
				{ID: "call-1", Name: "read_file", Arguments: `{"path":"main.go"}`},
				{ID: "call-2", Name: "read_file", Arguments: `{"path":"util.go"}`},
			}},
			base[3],
			{Role: provider.RoleTool, ToolCallID: "call-2", Name: "read_file", Content: "package util"},
		}
		if got := projectionRepairDriftKeep(seal(base), current); got != nil {
			t.Fatalf("diverging slots = %v, want nil (pipeline length changed)", got)
		}
	})
	t.Run("missing_corpus_drops", func(t *testing.T) {
		st := seal(base)
		st.Projection.CoveredMessageHashes = nil
		if got := projectionRepairDriftKeep(st, base); got != nil {
			t.Fatalf("diverging slots = %v, want nil (no evidence corpus)", got)
		}
	})
	t.Run("covered_overflow_drops", func(t *testing.T) {
		st := seal(base)
		st.Projection.CoveredCount = len(base) + 1
		if got := projectionRepairDriftKeep(st, base); got != nil {
			t.Fatalf("diverging slots = %v, want nil (covered overflow)", got)
		}
	})
	t.Run("no_divergence_drops", func(t *testing.T) {
		// Whole-prefix mismatch with an all-equal corpus is a fingerprint
		// discrepancy — fail closed.
		if got := projectionRepairDriftKeep(seal(base), base); got != nil {
			t.Fatalf("diverging slots = %v, want nil (corpus fully matches)", got)
		}
	})
}

// TestTL1785TranscriptVersionFloorRestored pins the counter recovery chain:
// the process-local counter starts at 0 after LoadSession (the pre-existing
// pinned reset semantics), the sidecar load restores the monotonic floor from
// the sidecar trail, and subsequent appends keep incrementing past it.
func TestTL1785TranscriptVersionFloorRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	msgs, version := sess.snapshotMessagesVersion()
	if version == 0 {
		t.Fatal("fixture must seal at a non-zero transcript version")
	}
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := SaveCompactionState(path, tl1785SealedState(path, msgs, len(msgs), version)); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.TranscriptVersion(); got != 0 {
		t.Fatalf("loaded transcript version = %d, want process-local reset to 0 before sidecar wiring", got)
	}
	reopened := New(nil, tool.NewRegistry(), loaded, Options{
		SessionPath: path,
		WorkspaceID: "workspace",
		ModelRef:    "provider/model",
	}, event.Discard)
	if got := reopened.Session().TranscriptVersion(); got != version {
		t.Fatalf("transcript version after sidecar load = %d, want floor %d", got, version)
	}
	reopened.Session().Add(provider.Message{Role: provider.RoleUser, Content: "next"})
	if got := reopened.Session().TranscriptVersion(); got != version+1 {
		t.Fatalf("transcript version after append = %d, want %d (counter keeps incrementing past the floor)", got, version+1)
	}
}

// TestFloorTranscriptVersionUnit: nil-safe, zero is a no-op, a lower value
// never rewinds, a higher value raises.
func TestFloorTranscriptVersionUnit(t *testing.T) {
	var nilSession *Session
	nilSession.FloorTranscriptVersion(9) // must not panic
	s := NewSession("system")
	s.FloorTranscriptVersion(0)
	if got := s.TranscriptVersion(); got != 0 {
		t.Fatalf("floor 0 moved the counter to %d, want no-op", got)
	}
	s.Add(provider.Message{Role: provider.RoleUser, Content: "x"})
	base := s.TranscriptVersion()
	s.FloorTranscriptVersion(base-1)
	if got := s.TranscriptVersion(); got != base {
		t.Fatalf("lower floor rewound the counter to %d, want %d", got, base)
	}
	s.FloorTranscriptVersion(base + 41)
	if got := s.TranscriptVersion(); got != base+41 {
		t.Fatalf("higher floor left the counter at %d, want %d", got, base+41)
	}
}

// TestTL1785HashConsistentRestoresUnchanged: the green path keeps its pre-tl1785
// shape — a sidecar sealed against the exact on-disk prefix restores as valid
// (checkpointState restored, projected view serves) and the floor restore does
// not perturb the verdict.
func TestTL1785HashConsistentRestoresUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "u1"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	msgs, version := sess.snapshotMessagesVersion()
	if version == 0 {
		t.Fatal("fixture must seal at a non-zero transcript version")
	}
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := SaveCompactionState(path, tl1785SealedState(path, msgs, len(msgs), version)); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}
	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "restored" {
		t.Fatalf("checkpoint state = %q, want restored", got)
	}
	visible := reopened.modelVisibleMessages()
	if len(visible) != 2 {
		t.Fatalf("visible messages = %d, want the 2-message projected view", len(visible))
	}
	if got := reopened.Session().TranscriptVersion(); got != version {
		t.Fatalf("transcript version = %d, want floor %d without disturbing the restore", got, version)
	}
}
