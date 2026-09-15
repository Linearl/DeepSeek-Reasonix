package control

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

func TestSessionV4BridgeImportAndSync(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "sessions")
	v4Root := filepath.Join(root, "sessions-v4")
	t.Setenv("REASONIX_SESSION_STORAGE", "")

	bridge, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.CloseAll(context.Background()) })

	legacyPath := filepath.Join(legacyDir, "chat.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{ID: "u1", Role: provider.RoleUser, Content: "hello"})
	if err := sess.Save(legacyPath); err != nil {
		t.Fatal(err)
	}
	imported, err := bridge.ImportLegacy(context.Background(), legacyPath)
	if err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}
	if imported.TargetID == "" {
		t.Fatal("empty target")
	}

	msgs := sess.Snapshot()
	if err := bridge.SyncAgentTranscript(context.Background(), legacyPath, msgs); err != nil {
		t.Fatalf("SyncAgentTranscript: %v", err)
	}
	// Second identical sync is a no-op.
	if err := bridge.SyncAgentTranscript(context.Background(), legacyPath, msgs); err != nil {
		t.Fatalf("second sync: %v", err)
	}
}
