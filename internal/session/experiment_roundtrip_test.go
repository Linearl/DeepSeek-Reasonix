package session_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/session"
)

// TestExperimentCreateAndMigrateRoundTrip is the storage-level acceptance for
// the session-v4 experiment: new sessions live under sessions-v4, and a legacy
// JSONL migrates without deleting the source.
func TestExperimentCreateAndMigrateRoundTrip(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "sessions")
	v4Root := filepath.Join(root, "sessions-v4")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}

	svc, err := session.NewService("exp", session.NewFilesystemPersistence(v4Root))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.CloseAll(context.Background()) })

	created, err := svc.Create(context.Background(), session.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"message": provider.Message{
		ID: "m1", Role: provider.RoleUser, Content: "hello v4",
	}})
	if _, err := created.Session().Append(context.Background(), session.Batch{
		OperationID: "exp-seed",
		Events:      []session.Event{{Kind: "message/complete", Payload: payload}},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := created.Session().Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := svc.Close(context.Background(), created.Ref()); err != nil {
		t.Fatalf("Service.Close: %v", err)
	}

	legacyPath := filepath.Join(legacyDir, "old.jsonl")
	sess := agent.NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "legacy question", ID: "legacy-user"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "legacy answer", ID: "legacy-assistant"})
	if err := sess.Save(legacyPath); err != nil {
		t.Fatalf("Save legacy: %v", err)
	}
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	migrated, err := session.MigrateLegacy(context.Background(), legacyPath, v4Root)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if migrated.TargetID == "" {
		t.Fatal("empty migration target id")
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("legacy source was modified by migration")
	}
	if _, err := os.Stat(filepath.Join(migrated.TargetDir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	again, err := session.MigrateLegacy(context.Background(), legacyPath, v4Root)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if !again.Reused || again.TargetID != migrated.TargetID {
		t.Fatalf("expected reuse of same target, first=%q second=%q reused=%v", migrated.TargetID, again.TargetID, again.Reused)
	}
}
