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

	msg := sess.Snapshot()
	if err := bridge.SyncAgentTranscript(context.Background(), legacyPath, msg); err != nil {
		t.Fatalf("SyncAgentTranscript: %v", err)
	}
	if _, ok := bridge.RefForAgentPath(legacyPath); !ok {
		t.Fatal("expected agent path mapping after sync")
	}
	got, ok := bridge.HistoryMessages(context.Background(), legacyPath)
	if !ok || len(got) == 0 {
		t.Fatalf("HistoryMessages miss: ok=%v n=%d", ok, len(got))
	}
	// Second identical sync is a no-op.
	if err := bridge.SyncAgentTranscript(context.Background(), legacyPath, msg); err != nil {
		t.Fatalf("second sync: %v", err)
	}
}
