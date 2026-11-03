package agent

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// tl1315: the catalog asks LoadSessionContentSnapshot about every recovery
// branch on every directory reconcile. A transcript whose strict load fails
// closed (dirty normalization / damaged event log) used to be re-parsed in
// full on every ask — the 2-3s-per-file cost that pushed real scans past the
// 2-minute budget. These tests pin the remembered verdict and its boundaries.

func countContentSnapshotLoads(t *testing.T) *atomic.Int64 {
	t.Helper()
	var loads atomic.Int64
	previous := contentSnapshotLoadHook
	contentSnapshotLoadHook = func(string) { loads.Add(1) }
	t.Cleanup(func() { contentSnapshotLoadHook = previous })
	return &loads
}

func danglingToolCallTranscript() []provider.Message {
	return []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "run it"},
		{Role: provider.RoleAssistant, Content: "", ToolCalls: []provider.ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		}},
	}
}

func TestLoadSessionContentSnapshotRemembersFailClosedVerdictPerFileVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tl1315-dirty.jsonl")
	writeLegacyJSONLSession(t, path, danglingToolCallTranscript())
	loads := countContentSnapshotLoads(t)

	if _, ok := LoadSessionContentSnapshot(path); ok {
		t.Fatal("dirty-normalization transcript must fail closed")
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("first ask performed %d strict loads, want 1", got)
	}
	for i := 0; i < 3; i++ {
		if _, ok := LoadSessionContentSnapshot(path); ok {
			t.Fatal("remembered verdict must stay fail-closed")
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("unchanged file was re-parsed: %d strict loads, want 1", got)
	}
}

func TestLoadSessionContentSnapshotReevaluatesAfterTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tl1315-healed.jsonl")
	writeLegacyJSONLSession(t, path, danglingToolCallTranscript())
	loads := countContentSnapshotLoads(t)

	if _, ok := LoadSessionContentSnapshot(path); ok {
		t.Fatal("dirty-normalization transcript must fail closed")
	}
	// The transcript is healed on disk (the tool call gets its result): the
	// remembered verdict belongs to the old bytes and must not outlive them.
	healed := append(danglingToolCallTranscript(),
		provider.Message{Role: provider.RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "package main"},
		provider.Message{Role: provider.RoleAssistant, Content: "done"},
	)
	writeLegacyJSONLSession(t, path, healed)
	future := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	snap, ok := LoadSessionContentSnapshot(path)
	if !ok {
		t.Fatal("healed transcript must load once its bytes changed")
	}
	if snap.Len() != len(healed) {
		t.Fatalf("healed snapshot has %d messages, want %d", snap.Len(), len(healed))
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("strict loads = %d, want 2 (one per file version)", got)
	}
}

func TestLoadSessionContentSnapshotTouchReevaluatesRefusal(t *testing.T) {
	// The refusal key is deliberately stricter than the positive cache key:
	// a bare mtime touch costs one more parse but can never pin a verdict onto
	// bytes it was not reached on.
	path := filepath.Join(t.TempDir(), "tl1315-touched.jsonl")
	writeLegacyJSONLSession(t, path, danglingToolCallTranscript())
	loads := countContentSnapshotLoads(t)

	_, _ = LoadSessionContentSnapshot(path)
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	_, _ = LoadSessionContentSnapshot(path)
	_, _ = LoadSessionContentSnapshot(path)
	if got := loads.Load(); got != 2 {
		t.Fatalf("strict loads = %d, want 2 (touch re-evaluates once, then remembered)", got)
	}
}

func TestLoadSessionContentSnapshotNeverRemembersTransientFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tl1315-late.jsonl")
	loads := countContentSnapshotLoads(t)

	// A missing file (mid-rename, not yet written) is an error, not a verdict.
	for i := 0; i < 2; i++ {
		if _, ok := LoadSessionContentSnapshot(path); ok {
			t.Fatal("missing transcript cannot produce a snapshot")
		}
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("transient failure was remembered: %d strict loads, want 2", got)
	}

	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "arrived later"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadSessionContentSnapshot(path); !ok {
		t.Fatal("a transcript that appears after a transient failure must load")
	}
}

func TestContentSnapshotRefusalsStayBounded(t *testing.T) {
	contentSnapshotRefusals.Lock()
	savedOrder := append([]string(nil), contentSnapshotRefusals.order...)
	savedEntries := make(map[string]struct{}, len(contentSnapshotRefusals.entries))
	for k := range contentSnapshotRefusals.entries {
		savedEntries[k] = struct{}{}
	}
	contentSnapshotRefusals.order = nil
	contentSnapshotRefusals.entries = map[string]struct{}{}
	contentSnapshotRefusals.Unlock()
	t.Cleanup(func() {
		contentSnapshotRefusals.Lock()
		contentSnapshotRefusals.order = savedOrder
		contentSnapshotRefusals.entries = savedEntries
		contentSnapshotRefusals.Unlock()
	})

	for i := 0; i < contentSnapshotRefusalLimit+40; i++ {
		rememberContentSnapshotRefusal(filepath.Join("v", time.Duration(i).String()))
	}
	contentSnapshotRefusals.Lock()
	n, m := len(contentSnapshotRefusals.order), len(contentSnapshotRefusals.entries)
	contentSnapshotRefusals.Unlock()
	if n != contentSnapshotRefusalLimit || m != contentSnapshotRefusalLimit {
		t.Fatalf("refusal memo grew to order=%d entries=%d, want both %d", n, m, contentSnapshotRefusalLimit)
	}
	if contentSnapshotRefused(filepath.Join("v", time.Duration(0).String())) {
		t.Fatal("oldest refusal should have been evicted first")
	}
}
