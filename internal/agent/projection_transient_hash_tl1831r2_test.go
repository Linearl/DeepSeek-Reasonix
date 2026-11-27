package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1831r2 fixtures: the pre-tl1831r2 builds fingerprinted transient user-turn
// blocks verbatim. English copy stands in for the old rendering, the tl1831
// Chinese short line for the new one — the recipe must not tell them apart.

const tl1831r2OldStateBlock = "<context-state>window occupancy 752k/1000k (75%); compaction cycles at 85% — compaction is routine maintenance</context-state>"
const tl1831r2NewStateBlock = "<context-state>上下文 752k/1000k (75%)；压缩进度 85%</context-state>"

func tl1831r2Turn(stateBlock string) provider.Message {
	return provider.Message{
		Role: provider.RoleUser,
		Content: "<current-time>2026-11-26T08:00:00+08:00 Thu — 2026-11-26 星期四 — host clock when this turn started</current-time>\n\n" +
			stateBlock + "\n\nhelp me refactor the projection",
	}
}

// The covered-prefix fingerprint must ignore transient user-turn decoration
// (copy edits, fresh clocks) while still failing closed on real edits: user
// prose, assistant bytes, tool bodies.
func TestTransientStrippedCoveredPrefixHashIgnoresTransientBlocks(t *testing.T) {
	oldTurns := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		tl1831r2Turn(tl1831r2OldStateBlock),
		{Role: provider.RoleAssistant, Content: "done"},
	}
	newTurns := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		tl1831r2Turn(tl1831r2NewStateBlock),
		{Role: provider.RoleAssistant, Content: "done"},
	}
	if coveredPrefixHash(oldTurns, len(oldTurns)) != coveredPrefixHash(newTurns, len(newTurns)) {
		t.Fatal("transient copy change must not alter coveredPrefixHash")
	}
	if legacyTransientIncludedCoveredPrefixHash(oldTurns, len(oldTurns)) ==
		legacyTransientIncludedCoveredPrefixHash(newTurns, len(newTurns)) {
		t.Fatal("legacy recipe must keep distinguishing the two renderings (migration gate premise)")
	}
	if coveredPrefixHash(oldTurns, len(oldTurns)) ==
		legacyTransientIncludedCoveredPrefixHash(oldTurns, len(oldTurns)) {
		t.Fatal("stripped and legacy recipes must differ while transient blocks exist")
	}

	proseEdited := append([]provider.Message(nil), oldTurns...)
	proseEdited[1] = tl1831r2Turn(tl1831r2NewStateBlock)
	proseEdited[1].Content = strings.Replace(proseEdited[1].Content, "refactor the projection", "refactor the WASM runtime", 1)
	if coveredPrefixHash(proseEdited, len(proseEdited)) == coveredPrefixHash(oldTurns, len(oldTurns)) {
		t.Fatal("user prose edit must alter coveredPrefixHash")
	}

	assistantEdited := append([]provider.Message(nil), oldTurns...)
	assistantEdited[2].Content = "done-EDITED"
	if coveredPrefixHash(assistantEdited, len(assistantEdited)) == coveredPrefixHash(oldTurns, len(oldTurns)) {
		t.Fatal("assistant content edit must alter coveredPrefixHash")
	}
}

// tl1651 per-message corpus rides the same stripped recipe: a transient-only
// re-render localizes to zero diverging slots; a prose edit pinpoints the slot.
func TestCoveredMessageFingerprintsFollowStrippedRecipe(t *testing.T) {
	oldTurns := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		tl1831r2Turn(tl1831r2OldStateBlock),
		{Role: provider.RoleAssistant, Content: "done"},
	}
	newTurns := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		tl1831r2Turn(tl1831r2NewStateBlock),
		{Role: provider.RoleAssistant, Content: "done"},
	}
	oldFP := coveredMessageFingerprints(oldTurns, len(oldTurns))
	newFP := coveredMessageFingerprints(newTurns, len(newTurns))
	if len(oldFP) != len(newFP) {
		t.Fatalf("pipeline length moved: %d vs %d", len(oldFP), len(newFP))
	}
	for i := range oldFP {
		if oldFP[i] != newFP[i] {
			t.Fatalf("slot %d diverged on a transient-only re-render", i)
		}
	}

	proseEdited := append([]provider.Message(nil), newTurns...)
	proseEdited[1].Content = strings.Replace(proseEdited[1].Content, "refactor the projection", "refactor the WASM runtime", 1)
	editFP := coveredMessageFingerprints(proseEdited, len(proseEdited))
	if editFP[1] == newFP[1] {
		t.Fatal("prose edit must diverge at its own slot")
	}
	for i := range editFP {
		if i != 1 && editFP[i] != newFP[i] {
			t.Fatalf("slot %d diverged without an edit", i)
		}
	}
}

// Gate A — the 装机重启 shape: the sidecar was sealed under the legacy recipe
// over bytes that are still on disk. The only difference is the recipe itself,
// so the load must re-anchor once and restore instead of dropping the fold.
func TestMigrateTransientStrippedPrefixHashReanchorsLegacySidecar(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		tl1831r2Turn(tl1831r2OldStateBlock),
		{Role: provider.RoleAssistant, Content: "done"},
	}
	storedHash := legacyTransientIncludedCoveredPrefixHash(msgs, len(msgs))
	st := CompactionState{
		SchemaVersion:     compactionStateSchemaV4,
		TranscriptVersion: 7,
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "sys"},
				{Role: provider.RoleUser, Content: "[compacted context]"},
			},
			TranscriptVersion: 7,
			ProjectionVersion: 3,
			CoveredCount:      len(msgs),
			CoveredPrefixHash: storedHash,
		},
		LastReceipt: &ContextMaintenanceReceipt{
			Status:            "applied",
			CoveredPrefixHash: storedHash,
		},
	}
	if reason := projectionInvalidReason(st, msgs); reason != projectionFailHashMismatch {
		t.Fatalf("pre-migration verdict = %q, want hash_mismatch", reason)
	}
	if !migrateTransientStrippedPrefixHash(&st, msgs, nil) {
		t.Fatal("legacy-recipe sidecar must migrate")
	}
	if reason := projectionInvalidReason(st, msgs); reason != projectionFailNone {
		t.Fatalf("post-migration verdict = %q, want valid", reason)
	}
	if st.Projection.CoveredPrefixHash != coveredPrefixHash(msgs, len(msgs)) {
		t.Fatal("stored hash was not re-anchored to the stripped recipe")
	}
	corpus := coveredMessageFingerprints(msgs, len(msgs))
	if len(st.Projection.CoveredMessageHashes) != len(corpus) {
		t.Fatalf("per-message corpus length = %d, want %d", len(st.Projection.CoveredMessageHashes), len(corpus))
	}
	if st.LastReceipt.CoveredPrefixHash != st.Projection.CoveredPrefixHash {
		t.Fatal("matching last receipt was not repointed")
	}
	// Idempotence: a second pass is a no-op (already on the new recipe).
	if migrateTransientStrippedPrefixHash(&st, msgs, nil) {
		t.Fatal("migration must not fire twice")
	}
}

// Gate B — same seal, but THIS load's normalization rewrote the provider-visible
// bytes before validation (the tl1651 repair family riding the upgrade). The
// legacy recipe must match the pre-repair disk prefix to legalize the re-anchor.
func TestMigrateTransientStrippedPrefixHashReanchorsLoadRepairedSidecar(t *testing.T) {
	preRepair := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		tl1831r2Turn(tl1831r2OldStateBlock),
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1"}}},
		{Role: provider.RoleTool, ToolCallID: "c1", Content: "ok"},
	}
	st := CompactionState{
		SchemaVersion:     compactionStateSchemaV4,
		TranscriptVersion: 9,
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, {Role: provider.RoleUser, Content: "[compacted]"}},
			TranscriptVersion: 9,
			ProjectionVersion: 1,
			CoveredCount:      len(preRepair),
			CoveredPrefixHash: legacyTransientIncludedCoveredPrefixHash(preRepair, len(preRepair)),
		},
	}
	// The load repair rewrote the transcript (backfilled the tool-call name —
	// the #4727 family) while the transient copy re-rendered alongside. The
	// ToolCalls backing array is shared after the shallow append, so the
	// backfill writes into a deep copy — mutating the shared slice would
	// rewrite preRepair too and dissolve the gate's two sides.
	current := append([]provider.Message(nil), preRepair...)
	current[2].ToolCalls = append([]provider.ToolCall(nil), preRepair[2].ToolCalls...)
	current[2].ToolCalls[0].Name = "read_file"
	current[1] = tl1831r2Turn(tl1831r2NewStateBlock)
	if reason := projectionInvalidReason(st, current); reason != projectionFailHashMismatch {
		t.Fatalf("pre-migration verdict = %q, want hash_mismatch", reason)
	}
	if !migrateTransientStrippedPrefixHash(&st, current, preRepair) {
		t.Fatal("load-repaired legacy sidecar must migrate via the pre-repair gate")
	}
	if reason := projectionInvalidReason(st, current); reason != projectionFailNone {
		t.Fatalf("post-migration verdict = %q, want valid", reason)
	}
}

// Fail-closed: a real user-prose edit is never legalized, with or without a
// pre-repair corpus; and an already-migrated (new-recipe) sidecar with drift
// stays on the drop path.
func TestMigrateTransientStrippedPrefixHashFailsClosedOnRealDrift(t *testing.T) {
	base := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		tl1831r2Turn(tl1831r2OldStateBlock),
		{Role: provider.RoleAssistant, Content: "done"},
	}
	drifted := append([]provider.Message(nil), base...)
	drifted[1].Content = strings.Replace(drifted[1].Content, "refactor the projection", "refactor the WASM runtime", 1)

	st := CompactionState{
		SchemaVersion: compactionStateSchemaV4,
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}},
			ProjectionVersion: 1,
			CoveredCount:      len(base),
			CoveredPrefixHash: legacyTransientIncludedCoveredPrefixHash(base, len(base)),
		},
	}
	if migrateTransientStrippedPrefixHash(&st, drifted, nil) {
		t.Fatal("prose drift must not migrate (gate A)")
	}
	if migrateTransientStrippedPrefixHash(&st, drifted, drifted) {
		t.Fatal("prose drift must not migrate (gate B, repaired corpus equals current)")
	}
	if reason := projectionInvalidReason(st, drifted); reason != projectionFailHashMismatch {
		t.Fatalf("verdict after refused migration = %q, want hash_mismatch", reason)
	}

	// New-recipe sidecar (already migrated / freshly sealed) with real drift:
	// stored hash equals neither recipe over the current prefix — stays invalid.
	fresh := CompactionState{
		SchemaVersion: compactionStateSchemaV4,
		Projection: ContextProjection{
			Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}},
			ProjectionVersion: 2,
			CoveredCount:      len(base),
			CoveredPrefixHash: coveredPrefixHash(base, len(base)),
		},
	}
	if migrateTransientStrippedPrefixHash(&fresh, drifted, nil) {
		t.Fatal("new-recipe sidecar must never migrate")
	}
	if reason := projectionInvalidReason(fresh, drifted); reason != projectionFailHashMismatch {
		t.Fatalf("fresh sidecar verdict = %q, want hash_mismatch", reason)
	}
}

// 验收①③ — the 20261127 incident shape end to end: a pre-upgrade sidecar
// sealed with the transient-included recipe restores after the upgrade restart
// (no wholesale drop), and the wire serves the compacted projection instead of
// the full-canonical fallback.
func TestProjectionSidecarSurvivesTransientRecipeUpgradeRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(tl1831r2Turn(tl1831r2OldStateBlock))
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	msgs, version := sess.snapshotMessagesVersion()
	if version == 0 {
		t.Fatal("fixture must compact at a non-zero transcript version")
	}
	// The old build sealed the sidecar with the transient-included recipe.
	state := restartProjectionState(path, msgs, version, len(msgs))
	state.Projection.CoveredPrefixHash = legacyTransientIncludedCoveredPrefixHash(msgs, len(msgs))
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := SaveCompactionState(path, state); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}

	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "restored" {
		t.Fatalf("checkpoint state = %q, want restored (old sidecar must not be dropped)", got)
	}
	if got := len(reopened.modelVisibleMessages()); got != 2 {
		t.Fatalf("visible messages = %d, want the compacted projection (no full-canonical fallback)", got)
	}
	// The one-time lane republishes the sidecar under the stripped recipe, so
	// the NEXT restart loads it as a plain valid sidecar.
	st, ok, err := LoadCompactionState(path)
	if err != nil || !ok {
		t.Fatalf("reload sidecar: ok=%v err=%v", ok, err)
	}
	if st.Projection.CoveredPrefixHash != coveredPrefixHash(msgs, len(msgs)) {
		t.Fatal("republished sidecar hash is not on the stripped recipe")
	}
	if reason := projectionInvalidReason(st, msgs); reason != projectionFailNone {
		t.Fatalf("republished sidecar verdict = %q, want valid", reason)
	}
}

// 验收② — after the fold is installed, re-rendering the covered turns'
// transient decoration (new copy, fresh clock) keeps the projection valid: the
// tail stays spliced, no rebuild, no fallback. A prose edit still invalidates.
func TestProjectionStaysValidWhenTransientBlocksRewritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(tl1831r2Turn(tl1831r2OldStateBlock))
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "second ask"})
	msgs, version := sess.snapshotMessagesVersion()
	state := restartProjectionState(path, msgs, version, len(msgs))
	if !projectionValid(state, msgs) {
		t.Fatal("fixture projection must start valid")
	}

	// The upgrade re-renders every covered user turn's transient decoration.
	reRendered := append([]provider.Message(nil), msgs...)
	reRendered[1] = tl1831r2Turn(tl1831r2NewStateBlock)
	if !projectionValid(state, reRendered) {
		t.Fatal("transient-only re-render must not invalidate the projection")
	}
	// The visible view is unchanged by the rewrite: projection + appended tail.
	before := modelVisibleFromProjection(state.Projection, msgs)
	after := modelVisibleFromProjection(state.Projection, reRendered)
	if len(before) != len(after) {
		t.Fatalf("visible length moved %d -> %d on a transient re-render", len(before), len(after))
	}
	// Same canonical shape with a prose edit: still fails closed.
	proseEdited := append([]provider.Message(nil), reRendered...)
	proseEdited[1].Content = strings.Replace(proseEdited[1].Content, "refactor the projection", "refactor the WASM runtime", 1)
	if projectionValid(state, proseEdited) {
		t.Fatal("prose edit must still invalidate the projection")
	}
}

// Fork inheritance keeps working across the recipe boundary: a parent sidecar
// sealed with the legacy recipe still transplants when the canonical prefix
// matches (the bytes-unchanged gate).
func TestCopyValidContextProjectionMigratesLegacyRecipeSidecar(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent.jsonl")
	target := filepath.Join(t.TempDir(), "child.jsonl")
	sess := NewSession("system")
	sess.Add(tl1831r2Turn(tl1831r2OldStateBlock))
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	msgs, version := sess.snapshotMessagesVersion()
	state := restartProjectionState(parent, msgs, version, len(msgs))
	state.Projection.CoveredPrefixHash = legacyTransientIncludedCoveredPrefixHash(msgs, len(msgs))
	if err := SaveCompactionState(parent, state); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}
	child := NewSession("system")
	// msgs[0] is the shared leading system prompt NewSession already carries;
	// adding it again would shift the covered prefix and fail the fixture.
	child.AddBatch(msgs[1:]...)
	inherited, err := child.CopyValidContextProjection(parent, target)
	if err != nil {
		t.Fatalf("CopyValidContextProjection: %v", err)
	}
	if !inherited {
		t.Fatal("legacy-recipe sidecar must still inherit across the recipe boundary")
	}
	st, ok, err := LoadCompactionState(target)
	if err != nil || !ok {
		t.Fatalf("load inherited sidecar: ok=%v err=%v", ok, err)
	}
	if st.Projection.CoveredPrefixHash != coveredPrefixHash(msgs, len(msgs)) {
		t.Fatal("inherited sidecar was not migrated to the stripped recipe")
	}
}

// The restart restore path keeps the upgrade session small end to end: after
// the migrated fold serves the wire, growing the transcript only appends the
// tail — no canonical replay. Guards the acceptance-3 read at the agent level.
func TestRestartedAgentServesProjectionTailAfterTransientMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(tl1831r2Turn(tl1831r2OldStateBlock))
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	msgs, version := sess.snapshotMessagesVersion()
	state := restartProjectionState(path, msgs, version, len(msgs))
	state.Projection.CoveredPrefixHash = legacyTransientIncludedCoveredPrefixHash(msgs, len(msgs))
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := SaveCompactionState(path, state); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	loaded = loaded.CloneWithMessages(loaded.Snapshot())
	a := New(nil, tool.NewRegistry(), loaded, Options{
		SessionPath: path,
		WorkspaceID: "workspace",
		ModelRef:    "provider/model",
	}, event.Discard)
	if got := a.ContextMaintenanceSnapshot().CheckpointState; got != "restored" {
		t.Fatalf("checkpoint state = %q, want restored", got)
	}
	canonical := len(loaded.Snapshot())
	visible := len(a.modelVisibleMessages())
	if visible >= canonical {
		t.Fatalf("visible view %d messages >= canonical %d — full-canonical fallback leaked through", visible, canonical)
	}
}
