package agent

// 任务 923 切片 B 正确性测试：增量 transcript digest 必须在所有路径上与
// digestAndSizeSessionMessages 全量重算 bit 级一致，且任何绕过 append 漏斗的
// 变更（原地改、Rewrite、Replace、系统提示替换、协议恢复过期）都必须让增量
// 状态失效回退——本文件对每类变更点逐一证伪。

import (
	"fmt"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

func t923AssertDigestMatchesFull(t *testing.T, s *Session, path string, label string) {
	t.Helper()
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	got, size, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion)
	want, wantSize, err := digestAndSizeSessionMessages(msgs)
	if err != nil {
		t.Fatalf("%s: full digest: %v", label, err)
	}
	if !ok {
		t.Fatalf("%s: incremental digest state unavailable (should be seeded by a save)", label)
	}
	if got != want {
		t.Fatalf("%s: incremental digest != full digest", label)
	}
	if size != wantSize {
		t.Fatalf("%s: incremental size %d != full size %d", label, size, wantSize)
	}
}

// The core shape: seed via a save, then appends keep the stream current, and
// every snapshot's incremental digest equals the full recompute.
func TestT923IncrementalDigestAppendFlow(t *testing.T) {
	s := NewSession("sys")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s.AddBatch(t923SyntheticMessages(50)...)
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	t923AssertDigestMatchesFull(t, s, path, "after seed")
	for i := 0; i < 5; i++ {
		s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("turn %d", i)})
		s.AddBatch(provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("reply %d", i)})
		// Save every other round so the stream must stay correct across
		// unsaved appends too.
		if i%2 == 0 {
			if err := s.SaveSnapshot(path); err != nil {
				t.Fatal(err)
			}
		}
		t923AssertDigestMatchesFull(t, s, path, fmt.Sprintf("round %d", i))
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	t923AssertDigestMatchesFull(t, s, path, "final save")
}

// Every rewrite-shaped mutation must desync the stream (fail open) and the
// next full pass must restore it. Enumerated per mutation site audited in 923.
func TestT923IncrementalDigestInvalidatedByRewrites(t *testing.T) {
	newSeeded := func(t *testing.T) (*Session, string) {
		t.Helper()
		s := NewSession("sys")
		path := filepath.Join(t.TempDir(), "session.jsonl")
		s.AddBatch(t923SyntheticMessages(20)...)
		if err := s.Save(path); err != nil {
			t.Fatal(err)
		}
		return s, path
	}
	mutations := map[string]func(s *Session){
		"Rewrite": func(s *Session) {
			msgs := s.Snapshot()
			msgs[1].Content = "rewritten"
			s.Rewrite(msgs, "test")
		},
		"Replace": func(s *Session) {
			msgs := s.Snapshot()
			msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "replaced"})
			s.Replace(msgs)
		},
		"ReplaceLocalMetadata": func(s *Session) {
			msgs := s.Snapshot()
			msgs[2].LocalOnly = true
			s.ReplaceLocalMetadata(msgs)
		},
		"SetLeadingSystemPromptWithReason": func(s *Session) {
			s.SetLeadingSystemPromptWithReason("new sys", "test")
		},
		"ReplaceSystemMessage": func(s *Session) {
			s.ReplaceSystemMessage("new sys content")
		},
		"UpdateToolCallPreview": func(s *Session) {
			s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "bash"}}})
			s.UpdateToolCallPreview(provider.ToolCall{ID: "call-1", Name: "bash", Diff: "d", Added: 1})
		},
		"AddDecisionReceipt": func(s *Session) {
			s.AddDecisionReceipt(&provider.DecisionReceipt{ID: "r-1", Kind: "tool", Tool: "bash", Outcome: "approved"})
		},
		"ConsumeFinalReadinessRecovery": func(s *Session) {
			s.Add(provider.Message{Role: provider.RoleTool, LocalOnly: true, FinalReadinessRecovery: &provider.FinalReadinessRecovery{Pending: true}})
			s.ConsumeFinalReadinessRecovery()
		},
		"IncrementRewrite": func(s *Session) {
			s.IncrementRewrite()
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s, path := newSeeded(t)
			mutate(s)
			// The mutation must not produce a stale incremental hit.
			msgs, version, rewriteVersion := s.snapshotWithVersion()
			_, _, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion)
			if ok {
				// A mutation that only appends (AddDecisionReceipt fallback)
				// may legitimately keep the stream if the funnel fed it; then
				// the digest must still equal the full pass.
				t923AssertDigestMatchesFull(t, s, path, name)
				return
			}
			// Fail-open: full pass must succeed and restore the stream.
			if err := s.SaveSnapshot(path); err != nil {
				t.Fatalf("%s: save after mutation: %v", name, err)
			}
			t923AssertDigestMatchesFull(t, s, path, name+" after reseed")
		})
	}
}

// In-place byte mutations that bump only version (tool result checkpoint,
// protocol record store) must also desync — the version guard catches them.
func TestT923IncrementalDigestInvalidatedByInPlaceEdits(t *testing.T) {
	s := NewSession("sys")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "c1", Name: "bash"}}})
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "c1", Name: "bash", Content: "old"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	s.updateBatchGuardResults([]provider.ToolCall{{ID: "c1", Name: "bash"}}, []string{"new"})
	if s.Snapshot()[2].Content != "new" {
		t.Fatal("test premise: tool result must change")
	}
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	if _, _, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion); ok {
		t.Fatal("in-place tool result edit must desync the incremental digest")
	}
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	t923AssertDigestMatchesFull(t, s, path, "after in-place edit reseed")
}

// Two savers on the same session (turn-end + periodic) must never read a
// stream that disagrees with their own snapshot: the version pinning must
// reject a snapshot taken before an concurrent append.
func TestT923IncrementalDigestRejectsStaleSnapshot(t *testing.T) {
	s := NewSession("sys")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s.AddBatch(t923SyntheticMessages(20)...)
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	s.Add(provider.Message{Role: provider.RoleUser, Content: "late append"})
	// The stale snapshot (older version/shorter count) must miss.
	if _, _, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion); ok {
		t.Fatal("stale snapshot must not hit the incremental digest")
	}
	// The fresh snapshot must hit and match the full pass.
	t923AssertDigestMatchesFull(t, s, path, "fresh snapshot")
}

// NewSession with only a system prompt has never been saved: the stream is
// nil and the first save must seed it.
func TestT923IncrementalDigestSeedsOnFirstSave(t *testing.T) {
	s := NewSession("sys")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	msgs, version, rewriteVersion := s.snapshotWithVersion()
	if _, _, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion); ok {
		t.Fatal("unsaved session must not report an incremental digest")
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	t923AssertDigestMatchesFull(t, s, path, "first save seeded")
}

// End-to-end invariant on a long mixed workload: after every operation the
// incremental digest (when available) equals the full recompute, and the
// durable file round-trips to a transcript with the same digest.
func TestT923IncrementalDigestLongMixedWorkload(t *testing.T) {
	s := NewSession("sys")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	for i := 0; i < 30; i++ {
		switch i % 4 {
		case 0:
			s.Add(provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("u%d", i)})
		case 1:
			s.AddBatch(
				provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("a%d", i)},
				provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("u%d-next", i)},
			)
		case 2:
			msgs := s.Snapshot()
			msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("r%d", i)})
			s.Rewrite(msgs, "test_compact")
		case 3:
			if err := s.SaveSnapshot(path); err != nil {
				t.Fatal(err)
			}
		}
		msgs, version, rewriteVersion := s.snapshotWithVersion()
		if _, _, ok := s.incrementalDigestAndSize(msgs, version, rewriteVersion); ok {
			t923AssertDigestMatchesFull(t, s, path, fmt.Sprintf("op %d", i))
		}
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	// Round-trip: the reloaded session's transcript digest must equal the
	// live one (老会话数据兼容 acceptance for the digest surface).
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	liveDigest, _, err := digestAndSizeSessionMessages(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	loadDigest, _, err := digestAndSizeSessionMessages(loaded.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if liveDigest != loadDigest {
		t.Fatal("round-trip digest mismatch")
	}
}
