package main

// Task 558 capsule: delete wiring for the ended sub-agents directory —
// DeleteSubagentRecord (single record) and ClearEndedSubagents (all ended,
// running kept). The agent-level file sweeping is covered in
// internal/agent/subagent_cleanup_test.go; these tests cover the desktop
// surface: path validation, ownership through sessionDirForPath, and the
// end-to-end "record leaves the directory AND its files leave the disk"
// contract the panel's delete buttons rely on.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

func writeDeletableSubagentRecord(t *testing.T, dir, ref, parentSession string, status agent.SubagentStatus) []string {
	t.Helper()
	subagentDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(subagentDir, ref+".jsonl")
	meta := agent.SubagentMeta{
		Ref:           ref,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		Status:        status,
		Kind:          "task",
		Name:          "del-" + ref,
		WorkspaceRoot: dir,
		ParentSession: parentSession,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	files := []string{
		sessionPath,
		filepath.Join(subagentDir, ref+".meta.json"),
		store.SessionEventLog(sessionPath),
		store.SessionEventIndex(sessionPath),
	}
	for _, p := range files {
		content := []byte("{}\n")
		if filepath.Base(p) == ref+".meta.json" {
			content = data
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return files
}

func assertFilesGone(t *testing.T, files []string) {
	t.Helper()
	for _, p := range files {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("artifact file survived delete: %s (err=%v)", p, err)
		}
	}
}

func TestDeleteSubagentRecordRemovesRecordAndFiles(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	ref := "sa_20261006_100000_000000000_000000000055"
	files := writeDeletableSubagentRecord(t, dir, ref, agent.BranchID(sessionPath), agent.SubagentCompleted)

	// The record is visible before, gone from the directory after, and its
	// files are gone from disk — the two things the panel's delete promises.
	views, err := app.ListSubagentsByParent(sessionPath)
	if err != nil {
		t.Fatalf("ListSubagentsByParent: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 artifact before delete, got %d", len(views))
	}
	if err := app.DeleteSubagentRecord(sessionPath, ref); err != nil {
		t.Fatalf("DeleteSubagentRecord: %v", err)
	}
	views, err = app.ListSubagentsByParent(sessionPath)
	if err != nil {
		t.Fatalf("ListSubagentsByParent after delete: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("expected empty directory after delete, got %d entries", len(views))
	}
	assertFilesGone(t, files)
}

func TestDeleteSubagentRecordRejectsForeignRunningAndBadInput(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	foreign := writeDeletableSubagentRecord(t, dir, "sa_20261006_100000_000000000_000000000056", "other-parent", agent.SubagentCompleted)
	running := writeDeletableSubagentRecord(t, dir, "sa_20261006_100000_000000000_000000000057", agent.BranchID(sessionPath), agent.SubagentRunning)

	if err := app.DeleteSubagentRecord(sessionPath, "sa_20261006_100000_000000000_000000000056"); err == nil {
		t.Fatal("ref owned by another parent must be rejected")
	} else if !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("unexpected error for foreign ref: %v", err)
	}
	if err := app.DeleteSubagentRecord(sessionPath, "sa_20261006_100000_000000000_000000000057"); err == nil {
		t.Fatal("running record must be refused")
	} else if !strings.Contains(err.Error(), "still running") {
		t.Fatalf("unexpected error for running ref: %v", err)
	}
	if err := app.DeleteSubagentRecord(sessionPath, ""); err == nil {
		t.Fatal("empty ref must be rejected")
	}
	if err := app.DeleteSubagentRecord("", "sa_x"); err == nil {
		t.Fatal("empty session path must be rejected")
	}
	if err := app.DeleteSubagentRecord(filepath.Join(dir, "not-a-session.jsonl"), "sa_x"); err == nil {
		t.Fatal("session path outside known dirs must be rejected")
	}
	// Rejections are non-destructive: nothing was removed by any failed call.
	if _, err := os.Stat(foreign[0]); err != nil {
		t.Errorf("foreign transcript touched by rejected delete: %v", err)
	}
	if _, err := os.Stat(running[0]); err != nil {
		t.Errorf("running transcript touched by rejected delete: %v", err)
	}
}

func TestClearEndedSubagentsKeepsRunningAndReturnsCount(t *testing.T) {
	app, dir, sessionPath := newCapsuleTestSession(t)
	parent := agent.BranchID(sessionPath)
	completed := writeDeletableSubagentRecord(t, dir, "sa_20261006_100000_000000000_000000000058", parent, agent.SubagentCompleted)
	failed := writeDeletableSubagentRecord(t, dir, "sa_20261006_100000_000000000_000000000059", parent, agent.SubagentFailed)
	interrupted := writeDeletableSubagentRecord(t, dir, "sa_20261006_100000_000000000_000000000060", parent, agent.SubagentInterrupted)
	running := writeDeletableSubagentRecord(t, dir, "sa_20261006_100000_000000000_000000000061", parent, agent.SubagentRunning)
	otherParent := writeDeletableSubagentRecord(t, dir, "sa_20261006_100000_000000000_000000000062", "other-parent", agent.SubagentCompleted)

	removed, err := app.ClearEndedSubagents(sessionPath)
	if err != nil {
		t.Fatalf("ClearEndedSubagents: %v", err)
	}
	if removed != 3 {
		t.Fatalf("expected 3 ended records removed, got %d", removed)
	}
	assertFilesGone(t, completed)
	assertFilesGone(t, failed)
	assertFilesGone(t, interrupted)
	if _, err := os.Stat(running[0]); err != nil {
		t.Errorf("running transcript must survive clear: %v", err)
	}
	if _, err := os.Stat(otherParent[0]); err != nil {
		t.Errorf("other parent's transcript must survive clear: %v", err)
	}
	// Convergence: clearing again is a clean no-op with 0.
	removed, err = app.ClearEndedSubagents(sessionPath)
	if err != nil {
		t.Fatalf("second ClearEndedSubagents: %v", err)
	}
	if removed != 0 {
		t.Fatalf("second clear must remove 0, got %d", removed)
	}
	if _, err := app.ClearEndedSubagents(""); err == nil {
		t.Fatal("empty session path must be rejected")
	}
}
