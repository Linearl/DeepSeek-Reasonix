package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/store"
)

func TestDeleteSubagentsByParentSweepsEventLogLeftovers(t *testing.T) {
	sessionDir := t.TempDir()
	subDir := filepath.Join(sessionDir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ref := "sa_cleanup_test"
	sessionPath := filepath.Join(subDir, ref+".jsonl")
	meta := SubagentMeta{Ref: ref, ParentSession: "parent-1", Status: SubagentCompleted}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	files := []string{
		sessionPath,
		filepath.Join(subDir, ref+".meta.json"),
		// Leftovers from builds where sub-agent saves bootstrapped event logs.
		store.SessionEventLog(sessionPath),
		store.SessionEventIndex(sessionPath),
	}
	for _, p := range files {
		content := []byte("{}\n")
		if filepath.Base(p) == ref+".meta.json" {
			content = metaBytes
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	if err := DeleteSubagentsByParent(sessionDir, "parent-1"); err != nil {
		t.Fatalf("DeleteSubagentsByParent: %v", err)
	}
	for _, p := range files {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("subagent artifact survived delete: %s (err=%v)", p, err)
		}
	}
}

// seedSubagentRecord writes one artifact's transcript, metadata, and event-log
// sidecars for the delete-primitive tests. Returns every file it created.
func seedSubagentRecord(t *testing.T, sessionDir, ref, parentSession string, status SubagentStatus) []string {
	t.Helper()
	subDir := filepath.Join(sessionDir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(subDir, ref+".jsonl")
	meta := SubagentMeta{Ref: ref, ParentSession: parentSession, Status: status}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	files := []string{
		sessionPath,
		filepath.Join(subDir, ref+".meta.json"),
		store.SessionEventLog(sessionPath),
		store.SessionEventIndex(sessionPath),
	}
	for _, p := range files {
		content := []byte("{}\n")
		if filepath.Base(p) == ref+".meta.json" {
			content = metaBytes
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return files
}

func assertAllGone(t *testing.T, files []string) {
	t.Helper()
	for _, p := range files {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("artifact file survived delete: %s (err=%v)", p, err)
		}
	}
}

func TestDeleteSubagentArtifactRemovesOwnedRecordAndSidecars(t *testing.T) {
	sessionDir := t.TempDir()
	owned := seedSubagentRecord(t, sessionDir, "sa_del_owned", "parent-1", SubagentCompleted)
	foreign := seedSubagentRecord(t, sessionDir, "sa_del_foreign", "parent-2", SubagentCompleted)

	if err := DeleteSubagentArtifact(sessionDir, "parent-1", "sa_del_owned"); err != nil {
		t.Fatalf("DeleteSubagentArtifact: %v", err)
	}
	assertAllGone(t, owned)
	// A record owned by another parent must be untouched — membership is the
	// delete authorization.
	for _, p := range foreign {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("foreign artifact was touched: %s (err=%v)", p, err)
		}
	}
}

func TestDeleteSubagentArtifactRefusesRunningUnknownAndMissing(t *testing.T) {
	sessionDir := t.TempDir()
	running := seedSubagentRecord(t, sessionDir, "sa_del_running", "parent-1", SubagentRunning)

	if err := DeleteSubagentArtifact(sessionDir, "parent-1", "sa_del_running"); err == nil {
		t.Fatal("running record must be refused")
	} else if !strings.Contains(err.Error(), "still running") {
		t.Fatalf("unexpected error for running ref: %v", err)
	}
	for _, p := range running {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("running artifact must stay intact: %s (err=%v)", p, err)
		}
	}
	if err := DeleteSubagentArtifact(sessionDir, "parent-1", "sa_del_unknown"); err == nil {
		t.Fatal("unknown ref must be rejected")
	} else if !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("unexpected error for unknown ref: %v", err)
	}
	if err := DeleteSubagentArtifact(sessionDir, "parent-1", "../../escape"); err == nil {
		t.Fatal("traversal-shaped ref must be rejected")
	}
	// Convergence: a meta-only record (transcript already gone) deletes fine,
	// and the retry after success reports the record as gone rather than
	// deleting something anew.
	metaOnly := seedSubagentRecord(t, sessionDir, "sa_del_metaonly", "parent-1", SubagentFailed)
	if err := os.Remove(metaOnly[0]); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSubagentArtifact(sessionDir, "parent-1", "sa_del_metaonly"); err != nil {
		t.Fatalf("meta-only delete must succeed: %v", err)
	}
	assertAllGone(t, metaOnly)
}

func TestDeleteEndedSubagentsKeepsRunningAndCounts(t *testing.T) {
	sessionDir := t.TempDir()
	done := seedSubagentRecord(t, sessionDir, "sa_end_done", "parent-1", SubagentCompleted)
	failed := seedSubagentRecord(t, sessionDir, "sa_end_failed", "parent-1", SubagentFailed)
	interrupted := seedSubagentRecord(t, sessionDir, "sa_end_interrupted", "parent-1", SubagentInterrupted)
	running := seedSubagentRecord(t, sessionDir, "sa_end_running", "parent-1", SubagentRunning)

	removed, err := DeleteEndedSubagents(sessionDir, "parent-1")
	if err != nil {
		t.Fatalf("DeleteEndedSubagents: %v", err)
	}
	if removed != 3 {
		t.Fatalf("expected 3 removed (completed/failed/interrupted), got %d", removed)
	}
	assertAllGone(t, done)
	assertAllGone(t, failed)
	assertAllGone(t, interrupted)
	for _, p := range running {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("running artifact must survive clear-ended: %s (err=%v)", p, err)
		}
	}
	// Convergence: a rerun finds nothing ended left and reports 0.
	removed, err = DeleteEndedSubagents(sessionDir, "parent-1")
	if err != nil {
		t.Fatalf("second DeleteEndedSubagents: %v", err)
	}
	if removed != 0 {
		t.Fatalf("rerun after clear must remove 0, got %d", removed)
	}
	if _, err := os.Stat(running[0]); err != nil {
		t.Errorf("running artifact must survive rerun: %v", err)
	}
}

func TestSubagentSaveUsesDurableEventLog(t *testing.T) {
	sessionDir := t.TempDir()
	subDir := filepath.Join(sessionDir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(subDir, "sa_force.jsonl")
	s := NewSession("sub")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save(path); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if _, err := os.Stat(store.SessionEventLog(path)); err != nil {
		t.Fatalf("subagent event log missing after save: %v", err)
	}
	if _, err := os.Stat(store.SessionEventIndex(path)); err != nil {
		t.Fatalf("subagent event index missing after save: %v", err)
	}
}
