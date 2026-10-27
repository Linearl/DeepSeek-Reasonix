package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

// writeTailBridgeFixture writes a .jsonl transcript the way the agent's
// display-checkpoint writer does: one marshaled provider.Message per line.
func writeTailBridgeFixture(t *testing.T, msgs []provider.Message) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "20261025-000000-model.jsonl")
	data := make([]byte, 0, 4096)
	for _, m := range msgs {
		line, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// TestAppSessionTailExcerptsBridge is the tl-1177b 方案 B bridge contract: the
// wall's hover overlay can pull the newest recognizable user messages for any
// saved session — including ones written before any excerpts sidecar existed
// — while non-transcript paths answer empty instead of reading random files.
func TestAppSessionTailExcerptsBridge(t *testing.T) {
	app := &App{}
	path := writeTailBridgeFixture(t, []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "first question"},
		{Role: provider.RoleAssistant, Content: "answer"},
		{Role: provider.RoleUser, Content: "@refs.only"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "Host progress check"},
		{Role: provider.RoleUser, Content: "newest question"},
	})
	got := app.SessionTailExcerpts(path)
	if len(got) != 2 || got[0] != "newest question" || got[1] != "first question" {
		t.Fatalf("SessionTailExcerpts = %v, want [newest question first question]", got)
	}

	// Event logs and other sidecars share the .jsonl tail; only transcripts
	// may be read.
	if got := app.SessionTailExcerpts(path + ""); len(got) != 2 {
		t.Fatalf("transcript path rejected: %v", got)
	}
	eventsPath := filepath.Join(t.TempDir(), "20261025-000000-model.events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(`{"role":"user","content":"leak"}"`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := app.SessionTailExcerpts(eventsPath); got != nil {
		t.Fatalf("event log excerpts = %v, want nil", got)
	}
	if got := app.SessionTailExcerpts("   "); got != nil {
		t.Fatalf("empty path excerpts = %v, want nil", got)
	}
	if got := app.SessionTailExcerpts(filepath.Join(t.TempDir(), "missing.jsonl")); got != nil {
		t.Fatalf("missing file excerpts = %v, want nil", got)
	}
}
