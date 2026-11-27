package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1831c — the 20261127 08:58 incident's two stale-keep gate failures,
// fixed: the structural gate demanded pipeline length == covered_count (any
// session whose sanitize pipeline dropped LocalOnly records — 2360 ≠ 2371 —
// was unattributable), and the role gate read canonical msgs[i] at pipeline
// index i (a tool slot read as user once the drops shifted coordinates).
// Together they turned a tolerable repair-drift mismatch into a wholesale
// drop and a 2.9M-token full-canonical fallback.

// tl1831cFixture seals a sidecar against a transcript whose sanitize pipeline
// is SHORTER than its covered count (LocalOnly records inside the covered
// prefix — the 08:58 shape), applies a rewrite to the canonical, and returns
// the sealed state plus the rewritten canonical.
func tl1831cFixture(t *testing.T, rewrite func(msgs []provider.Message)) (CompactionState, []provider.Message, int) {
	t.Helper()
	base := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "u1"},
		// LocalOnly interrupted-turn record inside the covered prefix: the
		// sanitize pipeline drops it, so pipeline length < covered_count.
		{Role: provider.RoleAssistant, Content: "partial stream", LocalOnly: true},
		{Role: provider.RoleUser, Content: "u2"},
		{Role: provider.RoleAssistant, Content: "draft v1"},
		{Role: provider.RoleUser, Content: "u3"},
		{Role: provider.RoleAssistant, Content: "done"},
	}
	covered := len(base)
	st := CompactionState{Projection: ContextProjection{
		CoveredCount:         covered,
		CoveredPrefixHash:    coveredPrefixHash(base, covered),
		CoveredMessageHashes: coveredMessageFingerprints(base, covered),
	}}
	pipeline := coveredMessageFingerprints(base, covered)
	if len(pipeline) >= covered {
		t.Fatalf("fixture pipeline len = %d, want < covered %d (LocalOnly drop must shorten the pipeline)", len(pipeline), covered)
	}
	current := append([]provider.Message(nil), base...)
	rewrite(current)
	return st, current, len(pipeline)
}

// TestTL1831cKeepSurvivesSanitizeShortenedPipeline: the structural gate's old
// second clause (len(pipeline) == covered_count) made every LocalOnly-bearing
// session unattributable; the fixture reproduces the 08:58 arithmetic
// (pipeline 6 < covered 7) and pins that an assistant-slot repair rewrite
// inside the covered prefix still keeps.
func TestTL1831cKeepSurvivesSanitizeShortenedPipeline(t *testing.T) {
	st, current, pipelineLen := tl1831cFixture(t, func(msgs []provider.Message) {
		msgs[4].Content = "draft v1 (normalized)"
	})
	v := projectionRepairDriftKeepVerdict(st, current)
	if len(v.keep) != 1 || v.keep[0] != 3 {
		t.Fatalf("keep slots = %v, want [3] (pipeline slot 3 = canonical assistant slot 4)", v.keep)
	}
	if v.refusal != "" {
		t.Fatalf("refusal = %q, want empty (keep applies)", v.refusal)
	}
	if len(v.keepRoles) != 1 || v.keepRoles[0] != string(provider.RoleAssistant) {
		t.Fatalf("keep roles = %v, want [assistant]", v.keepRoles)
	}
	if len(v.keepCanonIdx) != 1 || v.keepCanonIdx[0] != 4 {
		t.Fatalf("keep canonical indexes = %v, want [4]", v.keepCanonIdx)
	}
	if pipelineLen >= st.Projection.CoveredCount {
		t.Fatalf("pipeline len = %d, must be < covered_count %d for this fixture to exercise the old gate's dead clause", pipelineLen, st.Projection.CoveredCount)
	}
}

// TestTL1831cToolSlotKeepsThroughCoordinateShift: the 08:58 role-gate
// distortion. The diverging slot is an assistant tool-call turn; sanitize
// dropped a LocalOnly record before it, so the pipeline slot index and the
// canonical index differ. The gate must tolerate (pipeline slot role =
// assistant), and the verdict must name the slot's true canonical index.
func TestTL1831cToolSlotKeepsThroughCoordinateShift(t *testing.T) {
	base := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "u1"},
		{Role: provider.RoleAssistant, Content: "partial stream", LocalOnly: true},
		{Role: provider.RoleUser, Content: "u2"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"main.go"}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "package main"},
	}
	covered := len(base)
	st := CompactionState{Projection: ContextProjection{
		CoveredCount:         covered,
		CoveredPrefixHash:    coveredPrefixHash(base, covered),
		CoveredMessageHashes: coveredMessageFingerprints(base, covered),
	}}
	pipeline := coveredMessageFingerprints(base, covered)
	if len(pipeline) >= covered {
		t.Fatalf("pipeline len = %d, want < covered %d", len(pipeline), covered)
	}
	// Rewrite the assistant call's arguments (the repair family's truncated-
	// argument surface) — the assistant slot sits at pipeline index 3,
	// canonical index 4.
	rewritten := append([]provider.Message(nil), base...)
	for i := range rewritten {
		rewritten[i].ToolCalls = append([]provider.ToolCall(nil), rewritten[i].ToolCalls...)
	}
	rewritten[4].ToolCalls[0].Arguments = `{"path":"main.go","normalized":true}`
	v := projectionRepairDriftKeepVerdict(st, rewritten)
	if len(v.keep) != 1 || v.keep[0] != 3 {
		t.Fatalf("keep slots = %v, want [3]", v.keep)
	}
	if v.keepRoles[0] != string(provider.RoleAssistant) || v.keepCanonIdx[0] != 4 {
		t.Fatalf("keep roles/indexes = %v/%v, want [assistant]/[4]", v.keepRoles, v.keepCanonIdx)
	}
}

// TestTL1831cRoleGateReadsPipelineSlotNotCanonical: the decisive negative.
// The diverging slot is a USER message whose pipeline index points at a
// non-user canonical message (LocalOnly drop shifted the coordinates). The
// old canonical-index read saw "assistant" and would have wrongly kept; the
// pipeline-slot read sees user and refuses with the content class.
func TestTL1831cRoleGateReadsPipelineSlotNotCanonical(t *testing.T) {
	sealed := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "u1"},
		{Role: provider.RoleAssistant, Content: "partial stream", LocalOnly: true},
		{Role: provider.RoleUser, Content: "u2"},
	}
	covered := len(sealed)
	seal := CompactionState{Projection: ContextProjection{
		CoveredCount:         covered,
		CoveredPrefixHash:    coveredPrefixHash(sealed, covered),
		CoveredMessageHashes: coveredMessageFingerprints(sealed, covered),
	}}
	// Canonical slot 3 (user "u2") sits at pipeline slot 2; canonical slot 2
	// — the LocalOnly assistant record — has no pipeline slot at all.
	tampered := append([]provider.Message(nil), sealed...)
	tampered[3].Content = "u2 rewritten by hand"
	if got := sealed[3].Role; got != provider.RoleUser {
		t.Fatalf("fixture canonical slot 3 role = %s, want user", got)
	}
	if got := sealed[2].Role; got != provider.RoleAssistant {
		t.Fatalf("fixture canonical slot 2 role = %s, want assistant (the old read's wrong answer)", got)
	}
	v := projectionRepairDriftKeepVerdict(seal, tampered)
	if v.refusal != "content" {
		t.Fatalf("refusal = %q, want content (real user edit must drop)", v.refusal)
	}
	if v.detailIndex != 2 || v.detailRole != string(provider.RoleUser) {
		t.Fatalf("refusal slot %d role %q, want slot 2 role user (pipeline coordinates)", v.detailIndex, v.detailRole)
	}
	if len(v.keep) != 0 {
		t.Fatalf("keep = %v, want empty", v.keep)
	}
}

// TestTL1831cRealTamperingStillDrops: the tl1203 security钉 — a user-slot
// content edit with NO LocalOnly interference still refuses with the content
// class (the gate fix loosens nothing about real divergence).
func TestTL1831cRealTamperingStillDrops(t *testing.T) {
	base := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "u1"},
		{Role: provider.RoleAssistant, Content: "a1"},
	}
	seal := CompactionState{Projection: ContextProjection{
		CoveredCount:         len(base),
		CoveredPrefixHash:    coveredPrefixHash(base, len(base)),
		CoveredMessageHashes: coveredMessageFingerprints(base, len(base)),
	}}
	tampered := append([]provider.Message(nil), base...)
	tampered[1].Content = "u1 rewritten by hand"
	v := projectionRepairDriftKeepVerdict(seal, tampered)
	if v.refusal != "content" || v.detailIndex != 1 {
		t.Fatalf("refusal = %q slot %d, want content @1", v.refusal, v.detailIndex)
	}
	if v.keep != nil {
		t.Fatalf("keep = %v, want nil (tampering is never kept)", v.keep)
	}
}

// TestTL1831cStructuralChangeStillDrops: pipeline shape moved against the
// sealed corpus → structural refusal with both lengths in the verdict.
func TestTL1831cStructuralChangeStillDrops(t *testing.T) {
	base := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "u1"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read_file", Content: "ok"},
	}
	seal := CompactionState{Projection: ContextProjection{
		CoveredCount:         len(base),
		CoveredPrefixHash:    coveredPrefixHash(base, len(base)),
		CoveredMessageHashes: coveredMessageFingerprints(base, len(base)),
	}}
	structural := []provider.Message{
		base[0], base[1],
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "call-1", Name: "read_file", Arguments: `{}`},
			{ID: "call-2", Name: "read_file", Arguments: `{}`},
		}},
		base[3],
		{Role: provider.RoleTool, ToolCallID: "call-2", Name: "read_file", Content: "ok2"},
	}
	v := projectionRepairDriftKeepVerdict(seal, structural)
	if v.refusal != "structural" {
		t.Fatalf("refusal = %q, want structural", v.refusal)
	}
	if v.storedLen != 4 || v.currentLen != 5 {
		t.Fatalf("lengths = %d/%d, want 4/5", v.storedLen, v.currentLen)
	}
}

// TestTL1831cPipelineCanonicalIndexes: the mapping unit — LocalOnly drops
// vanish from the map's domain, fabricated placeholders map to -1, and every
// surviving slot names its canonical source.
func TestTL1831cPipelineCanonicalIndexes(t *testing.T) {
	twoCalls := []provider.ToolCall{
		{ID: "c1", Name: "t", Arguments: `{}`},
		{ID: "c2", Name: "t", Arguments: `{}`},
	}
	canonical := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},                        // canonical 0 → pipeline 0
		{Role: provider.RoleUser, Content: "u1"},                              // canonical 1 → pipeline 1
		{Role: provider.RoleAssistant, Content: "partial", LocalOnly: true},   // dropped
		{Role: provider.RoleUser, Content: "u2"},                              // canonical 3 → pipeline 2
		{Role: provider.RoleAssistant, ToolCalls: twoCalls},                   // canonical 4 → pipeline 3 + fabricated 4
		{Role: provider.RoleTool, ToolCallID: "c1", Name: "t", Content: "r1"}, // canonical 5 → pipeline 4 (pairToolResults reorders by call id)
	}
	got := pipelineCanonicalIndexes(canonical, len(canonical))
	want := []int{0, 1, 3, 4, 5, -1}
	if len(got) != len(want) {
		t.Fatalf("map = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("map[%d] = %d, want %d (full map %v)", i, got[i], want[i], got)
		}
	}
	// The mapping is pure: the canonical slice's IDs are untouched.
	for i := range canonical {
		if strings.HasPrefix(canonical[i].ID, pipelineSourceMarkerPrefix) {
			t.Fatalf("canonical[%d].ID was mutated by the mapping: %q", i, canonical[i].ID)
		}
	}
}

// TestTL1831cStaleKeepReplayZeroDrop is the 08:58 replay at the preflight
// level: a sidecar sealed against a LocalOnly-shortened pipeline plus an
// assistant-slot repair rewrite must keep the body stale (the 2-message stale
// splice serves, NOT the full canonical fallback) and the sidecar stays
// pure-read (no re-anchor, no corpus bless).
func TestTL1831cStaleKeepReplayZeroDrop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "u1"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "partial stream", LocalOnly: true})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "u2"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "draft v1"})
	original, _ := sess.snapshotMessagesVersion()
	// snapshot includes the seeded system message at index 0: system + 4 appends.
	if len(original) != 5 {
		t.Fatalf("fixture canonical length = %d, want 5", len(original))
	}
	pipeline := coveredMessageFingerprints(original, len(original))
	if len(pipeline) >= len(original) {
		t.Fatalf("pipeline len = %d, want < covered %d", len(pipeline), len(original))
	}
	key := promptCacheKey("workspace", BranchID(path), "provider/model")
	if err := SaveCompactionState(path, CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: 453,
		PromptCacheKey:    key,
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "system"},
				{Role: provider.RoleUser, Content: "[compacted context]"},
			},
			TranscriptVersion:    453,
			ProjectionVersion:    10,
			CoveredCount:         len(original),
			CoveredPrefixHash:    coveredPrefixHash(original, len(original)),
			CoveredMessageHashes: coveredMessageFingerprints(original, len(original)),
		},
	}); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}
	// The takeover-shaped rewrite: the live writer re-seals the file with its
	// normalized assistant bytes — repair family, assistant slot only
	// (canonical slot 4 = the assistant "draft v1").
	rewritten := append([]provider.Message(nil), original...)
	rewritten[4].Content = "draft v1 (rewritten by takeover)"
	sess = sess.CloneWithMessages(rewritten)
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "none" {
		t.Fatalf("checkpoint state = %q, want none (kept stale, not restored)", got)
	}
	visible := reopened.modelVisibleMessages()
	if len(visible) != 2 {
		t.Fatalf("visible messages = %d, want the 2-message stale splice (zero-drop replay: NOT the %d-message canonical fallback)", len(visible), len(rewritten))
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
}

// TestTL1831cDiagnoseDiffTruthfulUnderCoordinateShift: the tl1651 diagnosis
// twin reports the pipeline slot's own role (and the canonical index) instead
// of the canonical-at-index approximation.
func TestTL1831cDiagnoseDiffTruthfulUnderCoordinateShift(t *testing.T) {
	sealed := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "u1"},
		{Role: provider.RoleAssistant, Content: "partial stream", LocalOnly: true},
		{Role: provider.RoleUser, Content: "u2"},
		{Role: provider.RoleAssistant, Content: "draft v1"},
	}
	st := CompactionState{Projection: ContextProjection{
		CoveredCount:         len(sealed),
		CoveredPrefixHash:    coveredPrefixHash(sealed, len(sealed)),
		CoveredMessageHashes: coveredMessageFingerprints(sealed, len(sealed)),
	}}
	current := append([]provider.Message(nil), sealed...)
	current[4].Content = "draft v1 (normalized)"
	ev := diagnoseProjectionMismatch(st, current, nil)
	if ev.Source != "persisted_hashes" {
		t.Fatalf("source = %q, want persisted_hashes", ev.Source)
	}
	if ev.DiffIndex != 3 {
		t.Fatalf("diff pipeline index = %d, want 3", ev.DiffIndex)
	}
	if ev.DiffRole != string(provider.RoleAssistant) {
		t.Fatalf("diff role = %q, want assistant (pipeline slot's own role)", ev.DiffRole)
	}
	if ev.DiffCanonicalIndex != 4 {
		t.Fatalf("diff canonical index = %d, want 4", ev.DiffCanonicalIndex)
	}
}
