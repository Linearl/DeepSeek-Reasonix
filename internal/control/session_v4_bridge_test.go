package control

import (
	"bytes"
	"context"
	"errors"
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

func TestSessionV4BridgeSyncSurvivesTranscriptChangeAndRebuild(t *testing.T) {
	root := t.TempDir()
	v4Root := filepath.Join(root, "sessions-v4")
	ctx := context.Background()
	agentPath := filepath.Join(root, "sessions", "chat.jsonl")

	bridge, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.CloseAll(context.Background()) })

	first := []provider.Message{{ID: "u1", Role: provider.RoleUser, Content: "hello"}}
	if err := bridge.SyncAgentTranscript(ctx, agentPath, first); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	grown := append(append([]provider.Message{}, first...),
		provider.Message{ID: "a1", Role: provider.RoleAssistant, Content: "hi"})
	if err := bridge.SyncAgentTranscript(ctx, agentPath, grown); err != nil {
		t.Fatalf("sync after transcript change: %v", err)
	}
	// Replaying the identical transcript stays a no-op through the digest guard.
	if err := bridge.SyncAgentTranscript(ctx, agentPath, grown); err != nil {
		t.Fatalf("replay sync: %v", err)
	}

	// Runtime rebuild: the desktop rebuilds the controller (model/effort switch,
	// session clear) and with it the v4 bridge. CloseAll must hand every writer
	// back, otherwise the replacement bridge can never mirror that session again.
	if err := bridge.CloseAll(ctx); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	rebuilt, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rebuilt.CloseAll(context.Background()) })
	if err := rebuilt.SyncAgentTranscript(ctx, agentPath, grown); err != nil {
		t.Fatalf("sync after runtime rebuild: %v", err)
	}
	if msgs, ok := rebuilt.HistoryMessages(ctx, agentPath); !ok || len(msgs) != len(grown) {
		t.Fatalf("history after rebuild: ok=%v n=%d want %d", ok, len(msgs), len(grown))
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

// TestSessionV4BridgeReclaimsWriterFromReplacedBridge covers the desktop.log
// health debt: a controller rebuild replaces its v4 bridge, but the replaced
// bridge still held the writer lease in the process-wide lock registry. The
// replacement must reclaim that writer instead of failing every later sync with
// "session writer is owned by another runtime".
func TestSessionV4BridgeReclaimsWriterFromReplacedBridge(t *testing.T) {
	root := t.TempDir()
	v4Root := filepath.Join(root, "sessions-v4")
	ctx := context.Background()
	agentPath := filepath.Join(root, "sessions", "chat.jsonl")
	msgs := []provider.Message{{ID: "u1", Role: provider.RoleUser, Content: "hello"}}

	// The bridge of a controller that is about to be replaced: it mirrors the
	// session and stays open, exactly like a rebuild that runs before the old
	// controller is torn down.
	replaced, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replaced.CloseAll(context.Background()) })
	if err := replaced.SyncAgentTranscript(ctx, agentPath, msgs); err != nil {
		t.Fatalf("replaced bridge sync: %v", err)
	}

	rebuilt, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rebuilt.CloseAll(context.Background()) })
	if err := rebuilt.SyncAgentTranscript(ctx, agentPath, msgs); err != nil {
		t.Fatalf("sync after replaced bridge: %v", err)
	}
	if got, ok := rebuilt.HistoryMessages(ctx, agentPath); !ok || len(got) != len(msgs) {
		t.Fatalf("history after reclaim: ok=%v n=%d want %d", ok, len(got), len(msgs))
	}
}

// TestSessionV4BridgeModeGates covers the read/write decoupling of the four
// store modes (task 155): the mirror keeps running while the read side moves,
// and mode 4 freezes the legacy transcript as a read-only fallback.
func TestSessionV4BridgeModeGates(t *testing.T) {
	root := t.TempDir()
	v4Root := filepath.Join(root, "sessions-v4")
	ctx := context.Background()
	agentPath := filepath.Join(root, "sessions", "chat.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{ID: "u1", Role: provider.RoleUser, Content: "legacy"})
	if err := sess.Save(agentPath); err != nil {
		t.Fatal(err)
	}
	legacyBefore, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}

	bridge, err := NewSessionV4Bridge(v4Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.CloseAll(context.Background()) })
	msgs := sess.Snapshot()
	if err := bridge.SyncAgentTranscript(ctx, agentPath, msgs); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// Default mirrors the previous behaviour: v4 is the preferred read copy.
	if got, ok := bridge.HistoryMessages(ctx, agentPath); !ok || len(got) != len(msgs) {
		t.Fatalf("default read: ok=%v n=%d", ok, len(got))
	}

	// Stage 2 (dual_write_read_v3): the mirror still accepts writes, but reads
	// come from the legacy transcript so the UI falls back on its own.
	bridge.SetReadsV4(false)
	if _, ok := bridge.HistoryMessages(ctx, agentPath); ok {
		t.Fatal("stage 2 must not serve reads from v4")
	}
	if err := bridge.SyncAgentTranscript(ctx, agentPath, append(msgs,
		provider.Message{ID: "a1", Role: provider.RoleAssistant, Content: "hi"})); err != nil {
		t.Fatalf("stage 2 mirror write: %v", err)
	}
	bridge.SetReadsV4(true)
	if got, ok := bridge.HistoryMessages(ctx, agentPath); !ok || len(got) != len(msgs)+1 {
		t.Fatalf("stage 3 read after mirror write: ok=%v n=%d want %d", ok, len(got), len(msgs)+1)
	}

	// Stage 4 (v4_only): the maintained copy is v4, the legacy transcript is a
	// frozen read-only fallback - new imports are refused and the file is left
	// exactly as it was.
	bridge.SetV3Frozen(true)
	if _, err := bridge.ImportLegacy(ctx, agentPath); !errors.Is(err, ErrLegacyReadOnly) {
		t.Fatalf("frozen import = %v, want ErrLegacyReadOnly", err)
	}
	if _, err := bridge.ContinueLegacy(ctx, agentPath, ""); !errors.Is(err, ErrLegacyReadOnly) {
		t.Fatalf("frozen continue = %v, want ErrLegacyReadOnly", err)
	}
	legacyAfter, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyBefore, legacyAfter) {
		t.Fatal("frozen mode must not rewrite the legacy transcript")
	}
	if _, ok := bridge.HistoryMessages(ctx, agentPath); !ok {
		t.Fatal("frozen mode still serves reads from the v4 copy")
	}
}
