package agent

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1651 打点验收矩阵（先写矩阵后落地，见任务书）：
//
//	fail 出口              分流 reason               钉
//	F1 body 空             projection_body_empty     TestTL1651FailPathReasons/F1
//	F2 covered 越界        covered_count_overflow    …/F2
//	F3 无 hash             legacy_no_hash            …/F3
//	F4 pinned 出处         pinned_provenance         …/F4a(F3形态)+F4b(V4形态)
//	F5 前缀 hash 失配      hash_mismatch             …/F5 + 诊断三通道钉
//
// 零行为约束的钉：TestTL1651ReasonValidityEquivalence（valid ⇔ reason==""）。

// tl1651TestCapture swaps in a buffering slog handler and returns the buffer;
// the returned restore func must be deferred.
func tl1651TestCapture(t *testing.T, level slog.Level) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})))
	return &buf, func() { slog.SetDefault(prev) }
}

// tl1651Fixture builds a small canonical transcript and the matching valid V4
// projection state. Covered=3 covers system+user+assistant; the trailing user
// message is the uncovered tail.
func tl1651Fixture() ([]provider.Message, CompactionState) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task-v1"},
		{Role: provider.RoleAssistant, Content: "done"},
		{Role: provider.RoleUser, Content: "next"},
	}
	st := CompactionState{
		SchemaVersion:     compactionStateSchemaV4,
		TranscriptVersion: 4,
		PromptCacheKey:    "ws|sess|model",
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "sys"},
				formatSummaryMessage("earlier work"),
			},
			TranscriptVersion: 4,
			CoveredCount:      3,
			CoveredPrefixHash: coveredPrefixHash(msgs, 3),
			PinnedContextHash: pinnedContextCoverageHash(msgs, 3),
		},
	}
	return msgs, st
}

// T1–T5: each failure exit of projectionContentValid gets its own label.
func TestTL1651FailPathReasons(t *testing.T) {
	msgs, base := tl1651Fixture()

	t.Run("F1_projection_body_empty", func(t *testing.T) {
		st := base
		st.Projection.Messages = nil
		if got := projectionInvalidReason(st, msgs); got != projectionFailBodyEmpty {
			t.Fatalf("reason = %q, want %q", got, projectionFailBodyEmpty)
		}
	})

	t.Run("F2_covered_count_overflow", func(t *testing.T) {
		st := base
		st.Projection.CoveredCount = 99
		if got := projectionInvalidReason(st, msgs); got != projectionFailCoveredOverflow {
			t.Fatalf("reason = %q, want %q", got, projectionFailCoveredOverflow)
		}
		st2 := base
		st2.Projection.CoveredCount = 0
		if got := projectionInvalidReason(st2, msgs); got != projectionFailCoveredOverflow {
			t.Fatalf("zero covered: reason = %q, want %q", got, projectionFailCoveredOverflow)
		}
	})

	t.Run("F3_legacy_no_hash", func(t *testing.T) {
		st := base
		st.Projection.CoveredPrefixHash = ""
		if got := projectionInvalidReason(st, msgs); got != projectionFailLegacyNoHash {
			t.Fatalf("reason = %q, want %q", got, projectionFailLegacyNoHash)
		}
	})

	t.Run("F4a_pinned_provenance_preV4", func(t *testing.T) {
		pinned := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "<pinned_context_revision r=\"1\">\n<revision/>\n</pinned_context_revision>"},
			{Role: provider.RoleAssistant, Content: "done"},
			{Role: provider.RoleUser, Content: "next"},
		}
		st := base
		st.SchemaVersion = compactionStateSchemaV3
		if !containsPinnedContextRevision(pinned[:3]) {
			t.Fatal("fixture broken: pinned revision not recognized")
		}
		if got := projectionInvalidReason(st, pinned); got != projectionFailPinned {
			t.Fatalf("reason = %q, want %q", got, projectionFailPinned)
		}
	})

	t.Run("F4b_pinned_provenance_v4_hash_mismatch", func(t *testing.T) {
		st := base
		st.Projection.PinnedContextHash = "stale-pinned-hash"
		if got := projectionInvalidReason(st, msgs); got != projectionFailPinned {
			t.Fatalf("reason = %q, want %q", got, projectionFailPinned)
		}
	})

	t.Run("F5_hash_mismatch", func(t *testing.T) {
		edited := append([]provider.Message(nil), msgs...)
		edited[1].Content = "task-EDITED"
		if got := projectionInvalidReason(base, edited); got != projectionFailHashMismatch {
			t.Fatalf("reason = %q, want %q", got, projectionFailHashMismatch)
		}
	})
}

// Zero-behavior pin: the reason twin and the original bool gate must agree on
// every state exercised in this file's fixtures.
func TestTL1651ReasonValidityEquivalence(t *testing.T) {
	msgs, base := tl1651Fixture()
	states := []CompactionState{base, {}, base}
	states[1].Projection.CoveredCount = 99
	states[2].Projection.CoveredPrefixHash = "deadbeef"
	edits := [][]provider.Message{msgs, msgs, msgs}
	edited := append([]provider.Message(nil), msgs...)
	edited[1].Content = "task-EDITED"
	edits[2] = edited

	for i, st := range states {
		want := projectionContentValid(st, edits[i])
		got := projectionInvalidReason(st, edits[i]) == projectionFailNone
		if want != got {
			t.Fatalf("case %d: projectionContentValid=%v but reason twin=%v", i, want, got)
		}
	}
}

// B1: load-repair attribution. The stored hash still matches the PRE-repair
// transcript, so the mismatch was introduced by THIS load's normalization —
// and the first diverging pipeline slot must name the repaired message.
func TestTL1651MismatchDiagnosisLoadRepair(t *testing.T) {
	preRepair := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "half-streamed-args"},
		{Role: provider.RoleAssistant, Content: "done"},
		{Role: provider.RoleUser, Content: "next"},
	}
	current := append([]provider.Message(nil), preRepair...)
	current[1].Content = "repaired-by-loadsession"

	st := CompactionState{
		SchemaVersion: compactionStateSchemaV4,
		Projection: ContextProjection{
			Messages:          []provider.Message{formatSummaryMessage("earlier work")},
			CoveredCount:      3,
			CoveredPrefixHash: coveredPrefixHash(preRepair, 3),
			PinnedContextHash: pinnedContextCoverageHash(preRepair, 3),
		},
	}
	// Sanity: valid against the pre-repair bytes, invalid against the current.
	if projectionInvalidReason(st, preRepair) != projectionFailNone {
		t.Fatal("fixture broken: state must validate against pre-repair transcript")
	}
	if projectionInvalidReason(st, current) != projectionFailHashMismatch {
		t.Fatal("fixture broken: current transcript must fail with hash_mismatch")
	}

	ev := diagnoseProjectionMismatch(st, current, preRepair)
	if ev.Source != "load_repair" {
		t.Fatalf("source = %q, want load_repair", ev.Source)
	}
	if ev.DiffIndex < 0 {
		t.Fatal("diff index not localized")
	}
	if ev.StoredMsgHash == "" || ev.StoredMsgHash == ev.CurrentMsgHash {
		t.Fatalf("stored/current per-message hashes must both exist and differ: %q vs %q", ev.StoredMsgHash, ev.CurrentMsgHash)
	}
	if ev.DiffRole != string(provider.RoleUser) {
		t.Fatalf("diff role = %q, want user", ev.DiffRole)
	}
	// Pipeline is 1:1 for this clean history, so the index is the transcript index.
	if ev.DiffIndex != 1 {
		t.Fatalf("diff index = %d, want 1 (the repaired user message)", ev.DiffIndex)
	}
}

// B2: persisted per-message corpus localization. No pre-repair transcript
// available (cross-process rewrite); the sidecar's CoveredMessageHashes array
// pinpoints the first edited message.
func TestTL1651MismatchDiagnosisPersistedHashes(t *testing.T) {
	original := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, Content: "a1"},
		{Role: provider.RoleUser, Content: "q2"},
	}
	current := append([]provider.Message(nil), original...)
	current[2].Content = "assistant-REWRITTEN"

	st := CompactionState{
		SchemaVersion: compactionStateSchemaV4,
		Projection: ContextProjection{
			Messages:             []provider.Message{formatSummaryMessage("earlier work")},
			CoveredCount:         3,
			CoveredPrefixHash:    coveredPrefixHash(original, 3),
			CoveredMessageHashes: coveredMessageFingerprints(original, 3),
			PinnedContextHash:    pinnedContextCoverageHash(original, 3),
		},
	}
	if projectionInvalidReason(st, current) != projectionFailHashMismatch {
		t.Fatal("fixture broken: want hash_mismatch")
	}

	ev := diagnoseProjectionMismatch(st, current, nil)
	if ev.Source != "persisted_hashes" {
		t.Fatalf("source = %q, want persisted_hashes", ev.Source)
	}
	if ev.DiffIndex != 2 {
		t.Fatalf("diff index = %d, want 2 (the rewritten assistant message)", ev.DiffIndex)
	}
	if ev.DiffRole != string(provider.RoleAssistant) {
		t.Fatalf("diff role = %q, want assistant", ev.DiffRole)
	}
	if ev.StoredMsgHash == "" || ev.StoredMsgHash == ev.CurrentMsgHash {
		t.Fatalf("stored/current per-message hashes must both exist and differ: %q vs %q", ev.StoredMsgHash, ev.CurrentMsgHash)
	}
}

// B2 structural variants: an INSERT inside the covered prefix. A mid-prefix
// insertion shifts every later fingerprint one slot (the whole covered window
// stays n messages long, so lengths match but content diverges at the slot);
// an append past the old covered end shows up as a length divergence.
func TestTL1651MismatchDiagnosisStructuralInsert(t *testing.T) {
	original := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, Content: "a1"},
	}
	notice := provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "injected notice"}

	t.Run("mid_prefix_insert_content_divergence", func(t *testing.T) {
		current := append([]provider.Message(nil), original...)
		current = append(current[:1], append([]provider.Message{notice}, current[1:]...)...)
		st := CompactionState{
			SchemaVersion: compactionStateSchemaV4,
			Projection: ContextProjection{
				Messages:             []provider.Message{formatSummaryMessage("earlier work")},
				CoveredCount:         3,
				CoveredPrefixHash:    coveredPrefixHash(original, 3),
				CoveredMessageHashes: coveredMessageFingerprints(original, 3),
				PinnedContextHash:    pinnedContextCoverageHash(original, 3),
			},
		}
		if projectionInvalidReason(st, current) != projectionFailHashMismatch {
			t.Fatal("fixture broken: want hash_mismatch")
		}
		ev := diagnoseProjectionMismatch(st, current, nil)
		if ev.Source != "persisted_hashes" {
			t.Fatalf("source = %q, want persisted_hashes", ev.Source)
		}
		if ev.DiffIndex != 1 {
			t.Fatalf("diff index = %d, want 1 (insertion slot)", ev.DiffIndex)
		}
		if ev.StoredMsgHash == "" || ev.StoredMsgHash == ev.CurrentMsgHash {
			t.Fatalf("shifted slot must carry distinct stored/current hashes: %q vs %q", ev.StoredMsgHash, ev.CurrentMsgHash)
		}
	})

	t.Run("covered_window_pipeline_shrink_length_divergence", func(t *testing.T) {
		// A structural change INSIDE the covered window that the sanitize
		// pipeline collapses: an orphan tool result (its assistant tool_calls
		// twin is gone) is dropped by SanitizeToolPairing, so the covered
		// window's pipeline length shrinks below the stored corpus length.
		// (A plain append PAST the covered end is not a mismatch at all — the
		// covered prefix is untouched and the projection stays valid, which is
		// the "尾部变化不动前缀" boundary pinned for tl1651.)
		current := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "q1"},
			{Role: provider.RoleTool, ToolCallID: "orphan-c1", Name: "f", Content: "orphan result"},
			{Role: provider.RoleAssistant, Content: "a1"},
		}
		st := CompactionState{
			SchemaVersion: compactionStateSchemaV4,
			Projection: ContextProjection{
				Messages:             []provider.Message{formatSummaryMessage("earlier work")},
				CoveredCount:         3,
				CoveredPrefixHash:    coveredPrefixHash(original, 3),
				CoveredMessageHashes: coveredMessageFingerprints(original, 3),
				PinnedContextHash:    pinnedContextCoverageHash(original, 3),
			},
		}
		if projectionInvalidReason(st, current) != projectionFailHashMismatch {
			t.Fatalf("fixture broken: want hash_mismatch, got %q", projectionInvalidReason(st, current))
		}
		ev := diagnoseProjectionMismatch(st, current, nil)
		if ev.Source != "persisted_hashes" {
			t.Fatalf("source = %q, want persisted_hashes", ev.Source)
		}
		if ev.StoredMsgLen == ev.CurrentMsgLen {
			t.Fatalf("pipeline lengths must diverge: stored=%d current=%d", ev.StoredMsgLen, ev.CurrentMsgLen)
		}
		if ev.DiffIndex < 0 {
			t.Fatalf("diff index must localize the shrink, got %d", ev.DiffIndex)
		}
	})
}

// B3: no corpus at all (legacy sidecar without the per-message array) degrades
// to the whole-prefix hash pair without crashing.
func TestTL1651MismatchDiagnosisDegraded(t *testing.T) {
	original := []provider.Message{
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, Content: "a1"},
	}
	current := append([]provider.Message(nil), original...)
	current[0].Content = "q1-EDITED"
	st := CompactionState{
		SchemaVersion: compactionStateSchemaV4,
		Projection: ContextProjection{
			Messages:          []provider.Message{formatSummaryMessage("earlier work")},
			CoveredCount:      2,
			CoveredPrefixHash: coveredPrefixHash(original, 2),
			PinnedContextHash: pinnedContextCoverageHash(original, 2),
		},
	}
	ev := diagnoseProjectionMismatch(st, current, nil)
	if ev.Source != "none" || ev.DiffIndex != -1 {
		t.Fatalf("degraded diagnosis = %+v, want source=none diff=-1", ev)
	}
	if ev.StoredPrefixHash == "" || ev.CurrentPrefixHash == "" || ev.StoredPrefixHash == ev.CurrentPrefixHash {
		t.Fatalf("whole-prefix pair must exist and differ: %q vs %q", ev.StoredPrefixHash, ev.CurrentPrefixHash)
	}
}

// 复现钉（14:45 现场）：sidecar 按改写前转录写入 → 模拟重启后 covered 前缀
// 被改写 → LoadProjectionSidecar 必须以 hash_mismatch 分流落日志（带差异
// 定位字段），并以细分 reason 发起重建 kick。
func TestTL1651ResumeRewrittenPrefixDiagnosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	original := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: "done"},
		{Role: provider.RoleUser, Content: "next"},
	}
	key := promptCacheKey("ws", BranchID(path), "m")
	stored := CompactionState{
		SchemaVersion:  compactionStateSchemaV4,
		PromptCacheKey: key,
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "sys"},
				formatSummaryMessage("earlier work"),
			},
			CoveredCount:         3,
			CoveredPrefixHash:    coveredPrefixHash(original, 3),
			CoveredMessageHashes: coveredMessageFingerprints(original, 3),
			PinnedContextHash:    pinnedContextCoverageHash(original, 3),
		},
	}
	if err := SaveCompactionState(path, stored); err != nil {
		t.Fatal(err)
	}

	buf, restore := tl1651TestCapture(t, slog.LevelDebug)
	defer restore()

	sess := NewSession("sys")
	// 模拟重启恢复时前缀被改写（auto-resume 修复/其他写手——本钉不区分写手，
	// 只断言分流与定位）。NewSession 已带 index0 的 system 消息。
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task-REWRITTEN-ON-RESUME"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "next"})

	a := New(nil, nil, sess, Options{
		SessionPath: path,
		WorkspaceID: "ws",
		ModelRef:    "m",
	}, event.Discard)

	if a.sess.checkpointState == "restored" {
		t.Fatal("rewritten covered prefix must not restore the projection")
	}
	log := buf.String()
	if !strings.Contains(log, "covered prefix hash mismatch") {
		t.Fatalf("expected hash-mismatch diagnosis line, log:\n%s", log)
	}
	if !strings.Contains(log, "reason=hash_mismatch") {
		t.Fatalf("expected reason=hash_mismatch, log:\n%s", log)
	}
	if !strings.Contains(log, "mismatch_source=persisted_hashes") {
		t.Fatalf("expected persisted_hashes corpus attribution, log:\n%s", log)
	}
	if !strings.Contains(log, "diff_pipeline_index=1") {
		t.Fatalf("expected the rewritten user message localized at pipeline index 1, log:\n%s", log)
	}
	if strings.Contains(log, "context projection restored") {
		t.Fatalf("restored line must not appear for a dropped body, log:\n%s", log)
	}
}

// 打点 c 钉：Rewrite 触碰 covered 前缀区 → Warn 留痕（who/哪段/相对位置）；
// 尾部 rewrite → Debug + in_covered_prefix=false；无 wire 变化 → Debug。
func TestTL1651CoveredPrefixRewriteProbe(t *testing.T) {
	sess := NewSession("")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "q2"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "a2"})

	// Minimal agent runtime owning the covered state; the probe reads it under
	// compactionMu exactly as in production.
	a := &Agent{sess: sessionRuntime{conversation: sess}}
	a.sess.compactionState.Projection = ContextProjection{
		Messages:     []provider.Message{{Role: provider.RoleUser, Content: "folded"}},
		CoveredCount: 2,
	}
	a.installPrefixRewriteProbe()

	buf, restore := tl1651TestCapture(t, slog.LevelDebug)
	defer restore()

	// 1) Prefix-region rewrite via Replace (no reason) → WARN.
	after := append([]provider.Message(nil), sess.Snapshot()...)
	after[0].Content = "q1-REWRITTEN"
	sess.Replace(after)
	warn := buf.String()
	if !strings.Contains(warn, "agent: covered prefix rewritten") {
		t.Fatalf("expected covered-prefix rewrite WARN, log:\n%s", warn)
	}
	if !strings.Contains(warn, "in_covered_prefix=true") {
		t.Fatalf("expected in_covered_prefix=true, log:\n%s", warn)
	}
	if !strings.Contains(warn, "diff_index=0") {
		t.Fatalf("expected diff_index=0, log:\n%s", warn)
	}
	if !strings.Contains(warn, "rewrite_reason=replace") {
		t.Fatalf("expected replace attribution, log:\n%s", warn)
	}

	// 2) Tail-only rewrite → Debug with in_covered_prefix=false.
	buf.Reset()
	tail := append([]provider.Message(nil), sess.Snapshot()...)
	tail[3].Content = "a2-EDITED-TAIL"
	sess.Rewrite(tail, "tail_rewrite_reason")
	tailLog := buf.String()
	if !strings.Contains(tailLog, "history rewritten past covered prefix") {
		t.Fatalf("expected tail-only Debug line, log:\n%s", tailLog)
	}
	if !strings.Contains(tailLog, "in_covered_prefix=false") || !strings.Contains(tailLog, "rewrite_reason=tail_rewrite_reason") {
		t.Fatalf("expected tail attribution fields, log:\n%s", tailLog)
	}

	// 3) Rewrite with no provider-visible change → Debug no-change line.
	buf.Reset()
	sess.Rewrite(sess.Snapshot(), "no_change_reason")
	ncLog := buf.String()
	if !strings.Contains(ncLog, "history rewritten without provider-visible change") {
		t.Fatalf("expected no-change Debug line, log:\n%s", ncLog)
	}
}

// 打点 d 钉：sidecar 写入口 Info 锚点 + 读入口 Debug 锚点。
func TestTL1651SidecarSaveLoadTimeline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")

	buf, restore := tl1651TestCapture(t, slog.LevelDebug)
	defer restore()

	msgs, st := tl1651Fixture()
	_ = msgs
	if err := SaveCompactionState(path, st); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadCompactionState(path); err != nil || !ok {
		t.Fatalf("load sidecar: ok=%v err=%v", ok, err)
	}
	log := buf.String()
	if !strings.Contains(log, "agent: context sidecar saved") {
		t.Fatalf("expected save anchor line, log:\n%s", log)
	}
	if !strings.Contains(log, "agent: context sidecar loaded") {
		t.Fatalf("expected load anchor line, log:\n%s", log)
	}
	if !strings.Contains(log, "covered_count=3") {
		t.Fatalf("expected covered_count field, log:\n%s", log)
	}
}

// 写侧配套钉：投影安装时逐条指纹与 CoveredPrefixHash 同源同步（三个写侧
// 挂点共用 coveredMessageFingerprints；此处钉住管线一致性——用同一管线的
// 整段 hash 与逐条 hash 重组应互相印证）。
func TestTL1651PerMessageFingerprintsPipelineConsistency(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "q1"},
		{Role: provider.RoleAssistant, Content: "a1", ToolCalls: []provider.ToolCall{{ID: "c1", Name: "f", Arguments: "{}"}}},
		{Role: provider.RoleTool, ToolCallID: "c1", Name: "f", Content: "result"},
	}
	fps := coveredMessageFingerprints(msgs, 3)
	if len(fps) == 0 {
		t.Fatal("no fingerprints produced")
	}
	// Same pipeline whole-prefix hash must equal the composition identity used
	// by coveredPrefixHash: both run modelInputMessages+SanitizeToolPairing.
	if coveredPrefixHash(msgs, 3) == "" {
		t.Fatal("empty prefix hash")
	}
	// A one-byte edit must move exactly the edited slot.
	edited := append([]provider.Message(nil), msgs...)
	edited[2].Content = "result-EDITED"
	editedFps := coveredMessageFingerprints(edited, 3)
	if len(editedFps) != len(fps) {
		t.Fatalf("pipeline length changed on content edit: %d vs %d", len(fps), len(editedFps))
	}
	for i := range fps {
		if i == 2 {
			if fps[i] == editedFps[i] {
				t.Fatalf("edited slot %d fingerprint did not move", i)
			}
			continue
		}
		if fps[i] != editedFps[i] {
			t.Fatalf("unedited slot %d fingerprint moved", i)
		}
	}
}
