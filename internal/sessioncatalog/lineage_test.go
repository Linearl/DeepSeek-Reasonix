package sessioncatalog

import "testing"

func TestClassifyRecoveryLineageCoveredAndNormal(t *testing.T) {
	normal := classifyRecoveryLineage(SessionRecord{Path: "/s/a.jsonl"})
	if normal.RecoveryRole != RecoveryRoleNormal {
		t.Fatalf("normal role = %q", normal.RecoveryRole)
	}
	covered := classifyRecoveryLineage(SessionRecord{
		Path: "/s/a-recovery-1.jsonl", Recovered: true, RecoveryCopy: true, ParentID: "a",
	})
	if covered.RecoveryRole != RecoveryRoleCoveredCopy || covered.RecoveryGroupID != "a" {
		t.Fatalf("covered = %+v", covered)
	}
}

func TestPromoteCanonicalLeaves(t *testing.T) {
	recs := []SessionRecord{
		{Path: "/s/leaf.jsonl", RecoveryGroupID: "g1", RecoveryRole: RecoveryRoleDiverged},
	}
	out := promoteCanonicalLeaves(recs)
	if !out[0].RecoveryCanonical || out[0].RecoveryRole != RecoveryRoleAdopted {
		t.Fatalf("unique leaf = %+v", out[0])
	}
	multi := []SessionRecord{
		{Path: "/s/a.jsonl", RecoveryGroupID: "g2", RecoveryRole: RecoveryRoleDiverged},
		{Path: "/s/b.jsonl", RecoveryGroupID: "g2", RecoveryRole: RecoveryRoleDiverged},
	}
	out = promoteCanonicalLeaves(multi)
	for _, r := range out {
		if r.RecoveryCanonical || r.RecoveryRole != RecoveryRoleDiverged {
			t.Fatalf("multi leaf should stay diverged: %+v", r)
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
