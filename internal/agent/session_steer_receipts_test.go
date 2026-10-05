package agent

import (
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

// P15: the transcript's injected steers are the durable application receipt
// for desktop guidance rows. Only steer-shaped messages count; plain user
// messages must never enter the receipt set (a repeated instruction is the
// user's live intent, not residue).
func TestAppliedSteerReceiptTexts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "plain user message"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: midTurnSteerMessage("use smaller diffs")})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "ok"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: MidTurnSteerPrefix})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	set := AppliedSteerReceiptTexts(path)
	if len(set) != 1 {
		t.Fatalf("receipt set = %v, want exactly the injected steer text", set)
	}
	if _, ok := set["use smaller diffs"]; !ok {
		t.Fatalf("receipt set = %v, want the unwrapped steer text", set)
	}
	if _, ok := set["plain user message"]; ok {
		t.Fatal("plain user message must not become an application receipt")
	}
}

// Missing or empty inputs report no receipts: no receipt, no auto-settlement.
func TestAppliedSteerReceiptTextsEmptyInputs(t *testing.T) {
	if set := AppliedSteerReceiptTexts(""); set != nil {
		t.Fatalf("empty path = %v, want nil", set)
	}
	if set := AppliedSteerReceiptTexts(filepath.Join(t.TempDir(), "missing.jsonl")); set != nil {
		t.Fatalf("missing transcript = %v, want nil", set)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "plain only"})
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if set := AppliedSteerReceiptTexts(path); set != nil {
		t.Fatalf("steer-free transcript = %v, want nil", set)
	}
}
