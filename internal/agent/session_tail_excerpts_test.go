package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// writeTailFixture persists msgs through the real .jsonl writer so the tail
// reader sees the exact bytes a live session produces.
func writeTailFixture(t *testing.T, msgs []provider.Message) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "20261025-000000-model.jsonl")
	if err := writeSessionMessages(path, msgs); err != nil {
		t.Fatalf("writeSessionMessages: %v", err)
	}
	return path
}

// TestSessionTailUserExcerptsTailWindow is the tl-1177b 方案 B contract for a
// session whose newest exchanges sit inside the first tail window: every
// recognizable user message comes back, newest first, with the same noise
// filter as the sidecar path (host turns, steers, @file dumps, paste chrome).
// tl 1188 raised the width to 8, so all four recognizable fixture messages
// now come back (the fixture holds fewer than the width).
func TestSessionTailUserExcerptsTailWindow(t *testing.T) {
	path := writeTailFixture(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first task"},
		{Role: provider.RoleAssistant, Content: "ok"},
		{Role: provider.RoleUser, Content: "@noise.files"},
		{Role: provider.RoleUser, Content: "second task"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "Host progress check: still working"},
		{Role: provider.RoleUser, Content: "third task"},
		{Role: provider.RoleAssistant, Content: strings.Repeat("working… ", 40)},
		{Role: provider.RoleUser, Content: "[Pasted text #1 · 20 lines]\nnewest task"},
	})
	got := SessionTailUserExcerpts(path, RecentExcerptCount)
	want := []string{"newest task", "third task", "second task", "first task"}
	if len(got) != len(want) {
		t.Fatalf("excerpts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("excerpts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSessionTailUserExcerptsGrowsWindow covers a tail whose first window is
// all assistant/tool traffic: the reader retries with a wider window instead
// of returning fewer excerpts than the transcript holds. User rows over the
// per-line byte cap (embedded payloads) are skipped as excerpt candidates, so
// older recognizable turns fill their slots.
func TestSessionTailUserExcerptsGrowsWindow(t *testing.T) {
	filler := strings.Repeat("x", 48*1024)
	giant := strings.Repeat("y", 80*1024) // over maxTailExcerptBytes: never decodable
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "oldest task"},
	}
	for i := 0; i < 2; i++ {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: filler},
			provider.Message{Role: provider.RoleUser, Content: giant}, // embedded-payload row, skipped
		)
	}
	msgs = append(msgs,
		provider.Message{Role: provider.RoleUser, Content: "middle task"},
		provider.Message{Role: provider.RoleAssistant, Content: filler},
		provider.Message{Role: provider.RoleUser, Content: "newest task"},
		provider.Message{Role: provider.RoleAssistant, Content: filler},
		provider.Message{Role: provider.RoleAssistant, Content: filler},
		provider.Message{Role: provider.RoleAssistant, Content: filler},
	)
	path := writeTailFixture(t, msgs)
	got := SessionTailUserExcerpts(path, RecentExcerptCount)
	want := []string{"newest task", "middle task", "oldest task"}
	if len(got) != len(want) {
		t.Fatalf("excerpts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("excerpts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, excerpt := range got {
		if strings.Contains(excerpt, "yyyy") {
			t.Fatalf("giant payload row leaked into excerpts: %q", excerpt[:40])
		}
	}
}

// TestSessionTailUserExcerptsToleratesTornTail: a save appending the display
// checkpoint can leave a partial final line; it must be dropped, not parsed.
func TestSessionTailUserExcerptsToleratesTornTail(t *testing.T) {
	path := writeTailFixture(t, []provider.Message{
		{Role: provider.RoleUser, Content: "task one"},
		{Role: provider.RoleUser, Content: "task two"},
		{Role: provider.RoleUser, Content: "task three"},
	})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"role":"user","content":"torn line no newli`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	got := SessionTailUserExcerpts(path, RecentExcerptCount)
	want := []string{"task three", "task two", "task one"}
	if len(got) != len(want) {
		t.Fatalf("excerpts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("excerpts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSessionTailUserExcerptsDegradesGracefully covers the empty-answer paths:
// missing file, empty file, no user turns at all.
func TestSessionTailUserExcerptsDegradesGracefully(t *testing.T) {
	if got := SessionTailUserExcerpts(filepath.Join(t.TempDir(), "missing.jsonl"), 3); got != nil {
		t.Fatalf("missing file excerpts = %v, want nil", got)
	}
	empty := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := SessionTailUserExcerpts(empty, 3); got != nil {
		t.Fatalf("empty file excerpts = %v, want nil", got)
	}
	path := writeTailFixture(t, []provider.Message{
		{Role: provider.RoleUser, Content: "@only.files"},
		{Role: provider.RoleAssistant, Content: "ok"},
	})
	if got := SessionTailUserExcerpts(path, 3); got != nil {
		t.Fatalf("all-noise excerpts = %v, want nil", got)
	}
}
