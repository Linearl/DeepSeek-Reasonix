package repair

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRebuildDerivedStateCommitsWhenAuditLogFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	path := filepath.Join(home, "desktop-tabs.json")
	original := []byte("bad")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repairLogPath(), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := RebuildDerivedState("tabs"); err != nil {
		t.Fatalf("rebuild must commit despite a failing audit log: %v", err)
	}
	if _, err := UndoLastRepair(); err != nil {
		t.Fatalf("undo after audit-log failure: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("undone derived state = %q (%v), want %q", got, err, original)
	}
}
