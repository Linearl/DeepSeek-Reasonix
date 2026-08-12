package sessioncatalog

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

func saveLineageSession(t *testing.T, path string, messages ...string) {
	t.Helper()
	s := agent.NewSession("sys")
	for i, message := range messages {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		s.Add(provider.Message{Role: role, Content: message})
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyRecoveryLineageUsesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.jsonl")
	saveLineageSession(t, root, "q", "a", "continued", "done")
	branchSession := agent.NewSession("sys")
	branchSession.Add(provider.Message{Role: provider.RoleUser, Content: "q"})
	branchSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "a"})
	info, err := branchSession.SaveConflictRecoveryBranch(agent.RecoveryBranchOptions{OriginalPath: root})
	if err != nil {
		t.Fatal(err)
	}
	covered := classifyRecoveryLineage(SessionRecord{
		Path: info.Path, Recovered: true, ParentID: agent.BranchID(root),
	})
	if covered.RecoveryRole != RecoveryRoleCoveredCopy || !covered.RecoveryCopy {
		t.Fatalf("covered = %+v", covered)
	}
	normal := classifyRecoveryLineage(SessionRecord{Path: root})
	if normal.RecoveryRole != RecoveryRoleNormal {
		t.Fatalf("normal role = %q", normal.RecoveryRole)
	}
}

func TestPromoteCanonicalLeavesRequiresContentCoverage(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.jsonl")
	leaf := filepath.Join(dir, "leaf.jsonl")
	saveLineageSession(t, root, "q", "a")
	saveLineageSession(t, leaf, "q", "a", "next", "answer")
	recs := []SessionRecord{
		{Path: root, RecoveryRole: RecoveryRoleNormal, Turns: 1, TurnsState: TurnsValid},
		{Path: leaf, Recovered: true, ParentID: "root", RecoveryGroupID: "root", RecoveryRole: RecoveryRoleDiverged, Turns: 2, TurnsState: TurnsValid},
	}
	out := promoteCanonicalLeaves(recs)
	if !out[1].RecoveryCanonical || out[1].RecoveryRole != RecoveryRoleAdopted {
		t.Fatalf("unique covering leaf = %+v", out[1])
	}

	peer := filepath.Join(dir, "peer.jsonl")
	saveLineageSession(t, peer, "q", "a", "other", "branch")
	recs = append(recs, SessionRecord{
		Path: peer, Recovered: true, ParentID: "root", RecoveryGroupID: "root", RecoveryRole: RecoveryRoleDiverged, Turns: 2, TurnsState: TurnsValid,
	})
	out = promoteCanonicalLeaves(recs)
	for _, record := range out[1:] {
		if record.RecoveryCanonical {
			t.Fatalf("ambiguous equal-length leaves must stay diverged: %+v", out)
		}
	}
}

func TestCanonicalSessionPathForTopic(t *testing.T) {
	sessions := []SessionRecord{
		{Path: "/s/old.jsonl", RecoveryRole: RecoveryRoleNormal},
		{Path: "/s/leaf.jsonl", RecoveryRole: RecoveryRoleAdopted, RecoveryCanonical: true},
	}
	if got := CanonicalSessionPathForTopic(sessions, "/s/old.jsonl"); got != "/s/leaf.jsonl" {
		t.Fatalf("retarget = %q", got)
	}
	if got := CanonicalSessionPathForTopic(sessions, "/s/leaf.jsonl"); got != "" {
		t.Fatalf("already canonical should not retarget: %q", got)
	}
}

func TestReconcilePersistsContentProvenCanonicalLeaf(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "root.jsonl")
	saveLineageSession(t, root, "q", "a")
	continued := agent.NewSession("sys")
	continued.Add(provider.Message{Role: provider.RoleUser, Content: "q"})
	continued.Add(provider.Message{Role: provider.RoleAssistant, Content: "a"})
	continued.Add(provider.Message{Role: provider.RoleUser, Content: "next"})
	continued.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer"})
	info, err := continued.SaveConflictRecoveryBranch(agent.RecoveryBranchOptions{OriginalPath: root})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(ctx, Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close(ctx)
	if err := catalog.ReconcileDirectory(ctx, DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	record, ok, err := catalog.GetSession(ctx, info.Path)
	if err != nil || !ok {
		t.Fatalf("GetSession ok=%v err=%v", ok, err)
	}
	if record.RecoveryRole != RecoveryRoleAdopted || !record.RecoveryCanonical {
		t.Fatalf("reconciled recovery = %+v, want adopted canonical", record)
	}
}
