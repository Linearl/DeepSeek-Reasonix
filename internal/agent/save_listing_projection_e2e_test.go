package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

// TestTurnEndSaveLandsRecentExcerptsMeta reproduces the tl-1177b rework
// question: after a real turn (mid-turn tool checkpoints deferring the
// projection, turn-end snapshot publishing it), the branch-meta sidecar must
// carry recent_excerpts. tl-1177 shipped the field into
// persistSessionListingProjection only; the hover overlay still read an empty
// field on every packaged session (0 occurrences store-wide).
func TestTurnEndSaveLandsRecentExcerptsMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "20261025-000000-test-model.jsonl")
	s := NewSession("system prompt")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first question about widgets"})
	bindSessionWriter(t, s, path)
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}

	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "intermediate answer"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: "second question about gears"})
	s.AddBatch(
		provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "t1", Name: "write_file", Arguments: `{}`}}},
		provider.Message{Role: provider.RoleTool, ToolCallID: "t1", Name: "write_file", Content: "done", ToolRunState: provider.ToolRunCompleted},
	)
	if err := s.SaveToolCheckpoint(path, false); err != nil {
		t.Fatal(err)
	}
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "final answer"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}

	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta: ok=%v err=%v", ok, err)
	}
	if len(meta.RecentExcerpts) == 0 {
		t.Fatalf("recent_excerpts missing after turn-end snapshot: preview=%q turns=%d schema=%d", meta.Preview, meta.Turns, meta.SchemaVersion)
	}
	found := false
	for _, e := range meta.RecentExcerpts {
		if e == "second question about gears" {
			found = true
		}
	}
	if !found {
		t.Fatalf("recent_excerpts missing the newest user message: %q", meta.RecentExcerpts)
	}
}
