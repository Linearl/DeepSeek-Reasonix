package agent

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/provider"
)

// tl1435: PrepareFresh persists the running sidecar meta at dispatch. The
// right-dock subagent panel and the capsule read the persisted directory
// (ListSubagentsByParent) as their ended source; before tl1435 a child whose
// dispatch never produces a session-transcript card — workflow node execution
// is the live case (internal/workflow runner.go calls TaskTool.Execute outside
// any turn) — existed on disk only after its terminal save, so the panel's
// running section could never see it. The running meta landing at dispatch is
// the registration the panel's realtime half reads; the terminal save is what
// moves the record to the ended half.
func TestSubagentStorePrepareFreshPersistsRunningMeta(t *testing.T) {
	// The store lives at <sessionDir>/subagents — ListSubagentsByParent takes
	// the session dir (the desktop passes the same pair).
	sessionDir := t.TempDir()
	dir := filepath.Join(sessionDir, "subagents")
	store := NewSubagentStore(dir)
	spec := testSubagentSpec(t, "review")
	spec.Description = "独立复核证据链"

	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	defer run.Release()

	// The dispatch registration is visible to the same query the ended
	// directory uses — no terminal save required.
	artifacts, err := ListSubagentsByParent(sessionDir, spec.ParentSession)
	if err != nil {
		t.Fatalf("ListSubagentsByParent (running): %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("running artifacts = %d, want 1 (the dispatch must land the running meta)", len(artifacts))
	}
	if artifacts[0].Meta.Status != SubagentRunning {
		t.Fatalf("persisted status = %q, want %q", artifacts[0].Meta.Status, SubagentRunning)
	}
	if artifacts[0].Meta.Description != spec.Description {
		t.Fatalf("persisted description = %q, want %q (the panel row label reads it)", artifacts[0].Meta.Description, spec.Description)
	}
	if artifacts[0].Meta.ParentSession != spec.ParentSession {
		t.Fatalf("persisted parent = %q, want %q", artifacts[0].Meta.ParentSession, spec.ParentSession)
	}

	// The terminal save flips the same record — this is the running→ended
	// move the panel renders.
	run.Session.Add(provider.Message{Role: provider.RoleUser, Content: "finding"})
	if err := store.SaveCompleted(run); err != nil {
		t.Fatalf("SaveCompleted: %v", err)
	}
	terminal, err := ListSubagentsByParent(sessionDir, spec.ParentSession)
	if err != nil {
		t.Fatalf("ListSubagentsByParent (terminal): %v", err)
	}
	if len(terminal) != 1 {
		t.Fatalf("terminal artifacts = %d, want 1 (same record, status flipped)", len(terminal))
	}
	if terminal[0].Meta.Status != SubagentCompleted {
		t.Fatalf("terminal status = %q, want %q", terminal[0].Meta.Status, SubagentCompleted)
	}
	if terminal[0].Ref != run.Ref {
		t.Fatalf("terminal ref = %q, want %q", terminal[0].Ref, run.Ref)
	}
}

// The destroyed-parent contract is unchanged: a parent the checker already
// reports gone keeps the disk-silent dispatch (its terminal saves were no-ops
// before tl1435 and its running meta must not land either).
func TestSubagentStorePrepareFreshSkipsPersistForDestroyedParent(t *testing.T) {
	store := NewSubagentStore(t.TempDir()).WithDestroyedChecker(func(parentSession string) bool {
		return parentSession == "parent-session"
	})
	spec := testSubagentSpec(t, "review")
	run, err := store.PrepareFresh(spec)
	if err != nil {
		t.Fatalf("PrepareFresh: %v", err)
	}
	defer run.Release()
	if _, err := os.Stat(store.metaPath(run.Ref)); !os.IsNotExist(err) {
		t.Fatalf("destroyed parent dispatch should not persist the running meta, stat err = %v", err)
	}
}
