package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/session"
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

func TestSessionV4BridgeBindFreshContinueOpen(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "sessions")
	v4Root := filepath.Join(root, "sessions-v4")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = bridge.CloseAll(context.Background())
	})
	ctx := context.Background()

	// 1) BindFresh
	freshPath := filepath.Join(legacyDir, "fresh.jsonl")
	freshRef, err := bridge.BindFresh(ctx, "", []provider.Message{
		{ID: "seed-1", Role: provider.RoleUser, Content: "seeded"},
	}, freshPath)
	if err != nil {
		t.Fatalf("BindFresh: %v", err)
	}
	if freshRef.SessionID == "" {
		t.Fatal("empty fresh session id")
	}
	if ref, ok := bridge.RefForAgentPath(freshPath); !ok || ref.SessionID != freshRef.SessionID {
		t.Fatalf("fresh mapping = %+v ok=%v want %s", ref, ok, freshRef.SessionID)
	}
	if msgs, ok := bridge.HistoryMessages(ctx, freshPath); !ok || len(msgs) == 0 {
		t.Fatalf("fresh history miss: ok=%v n=%d", ok, len(msgs))
	}

	// 2) ContinueLegacy
	legacyPath := filepath.Join(legacyDir, "old.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{ID: "legacy-u", Role: provider.RoleUser, Content: "legacy"})
	if err := sess.Save(legacyPath); err != nil {
		t.Fatal(err)
	}
	continueRef, err := bridge.ContinueLegacy(ctx, legacyPath, "")
	if err != nil {
		t.Fatalf("ContinueLegacy: %v", err)
	}
	if continueRef.SessionID == "" {
		t.Fatal("empty continue session id")
	}
	if ref, ok := bridge.RefForAgentPath(legacyPath); !ok || ref.SessionID != continueRef.SessionID {
		t.Fatalf("continue mapping = %+v ok=%v want %s", ref, ok, continueRef.SessionID)
	}

	// 3) OpenExisting (no create)
	opened, err := bridge.OpenExisting(ctx, freshRef.SessionID, freshPath)
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	if opened.SessionID != freshRef.SessionID {
		t.Fatalf("open id = %s want %s", opened.SessionID, freshRef.SessionID)
	}
	if _, err := bridge.OpenExisting(ctx, "missing-session-id", ""); err == nil {
		t.Fatal("OpenExisting must not create a missing session")
	}

	// Release writer leases before TempDir cleanup on Windows.
	if svc := bridge.Service(); svc != nil {
		for _, id := range []string{freshRef.SessionID, continueRef.SessionID} {
			_ = svc.Close(ctx, session.SessionRef{HostID: "desktop-v4-bridge", SessionID: id})
		}
	}
}
