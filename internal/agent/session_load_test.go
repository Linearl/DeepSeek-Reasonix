package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

func TestLoadSessionMigratesLegacyInjectedUserContentWithoutChangingProviderBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	const raw = "fix the bug"
	const legacy = "<reasoning-language>\nVisible reasoning/thinking text preference: use Simplified Chinese.\n</reasoning-language>\n\nfix the bug"
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: legacy})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot legacy fixture: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	stored := loaded.Snapshot()
	if got := stored[1].Content; got != legacy {
		t.Fatalf("migrated provider content = %q, want legacy bytes", got)
	}
	if got := stored[1].RawContent; got != raw {
		t.Fatalf("migrated raw content = %q, want %q", got, raw)
	}
	if stored[1].ProviderContent != "" {
		t.Fatalf("migrated transitional provider content = %q, want empty", stored[1].ProviderContent)
	}
	model := provider.ModelMessages(stored)
	if got := model[1].Content; got != legacy {
		t.Fatalf("provider content after migration = %q, want %q", got, legacy)
	}
	if !loaded.normalizedDirty {
		t.Fatal("legacy migration must schedule a rewrite on the next save")
	}
}


func TestLoadSessionMigratesTransitionalProviderContentToLegacySafeShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	const raw = "fix the bug"
	const rendered = "<reasoning-language>zh</reasoning-language>\n\nfix the bug"
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: raw, ProviderContent: rendered})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot transitional fixture: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	stored := loaded.Snapshot()
	if stored[1].Content != rendered || stored[1].RawContent != raw || stored[1].ProviderContent != "" {
		t.Fatalf("transitional user turn not canonicalized: %+v", stored[1])
	}
	model := provider.ModelMessages(stored)
	if model[1].Content != rendered || model[1].RawContent != "" || model[1].ProviderContent != "" {
		t.Fatalf("provider model turn not canonical: %+v", model[1])
	}
	if !loaded.normalizedDirty {
		t.Fatal("transitional migration must schedule a rewrite on the next save")
	}
}

