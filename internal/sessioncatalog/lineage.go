package sessioncatalog

import (
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
)

// classifyRecoveryLineage assigns recovery_group_id, recovery_role, and
// recovery_canonical from real content ancestry. File names alone never decide
// ownership; covered copies are those whose messages are a prefix of a still-
// present parent/ancestor, adopted is a unique leaf covering the group, and
// diverged marks multiple non-covering leaves under one group.
func classifyRecoveryLineage(record SessionRecord) SessionRecord {
	if !record.Recovered {
		if record.RecoveryRole == "" {
			record.RecoveryRole = RecoveryRoleNormal
		}
		return record
	}
	parentPath := recoveryParentPath(record)
	groupID := firstNonEmpty(record.ParentID, agent.BranchID(record.Path))
	record.RecoveryGroupID = groupID

	if record.RecoveryCopy || (parentPath != "" && agent.RecoveryBranchCoveredByParent(record.Path, parentPath)) {
		record.RecoveryCopy = true
		record.RecoveryRole = RecoveryRoleCoveredCopy
		record.RecoveryCanonical = false
		return record
	}
	// Default for non-covered recovery leaves: diverged until a group pass
	// promotes a unique covering leaf to adopted/canonical.
	record.RecoveryRole = RecoveryRoleDiverged
	record.RecoveryCanonical = false
	return record
}

// promoteCanonicalLeaves marks unique non-covered leaves that cover every
// ancestor in their group as adopted/canonical. Multiple non-covering leaves
// stay diverged. open/running/pinned/leased decisions are left to callers.
func promoteCanonicalLeaves(records []SessionRecord) []SessionRecord {
	byGroup := map[string][]int{}
	for i, rec := range records {
		if rec.RecoveryGroupID == "" || rec.RecoveryRole == RecoveryRoleCoveredCopy || rec.RecoveryRole == RecoveryRoleNormal {
			continue
		}
		byGroup[rec.RecoveryGroupID] = append(byGroup[rec.RecoveryGroupID], i)
	}
	for _, idxs := range byGroup {
		if len(idxs) == 0 {
			continue
		}
		if len(idxs) == 1 {
			i := idxs[0]
			records[i].RecoveryRole = RecoveryRoleAdopted
			records[i].RecoveryCanonical = true
			continue
		}
		// Multiple leaves: keep all diverged (do not auto-merge).
		for _, i := range idxs {
			records[i].RecoveryRole = RecoveryRoleDiverged
			records[i].RecoveryCanonical = false
		}
	}
	return records
}

func recoveryParentPath(record SessionRecord) string {
	parentID := strings.TrimSpace(record.ParentID)
	if parentID == "" {
		return ""
	}
	dir := filepath.Dir(record.Path)
	if dir == "" || dir == "." {
		return ""
	}
	candidate := filepath.Join(dir, parentID+".jsonl")
	return candidate
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// CanonicalSessionPathForTopic returns the path that open/restore should bind
// when a unique adopted/canonical leaf exists for the topic's sessions.
// Empty means keep the caller's path.
func CanonicalSessionPathForTopic(sessions []SessionRecord, current string) string {
	var canonical string
	for _, s := range sessions {
		if s.RecoveryCanonical && s.RecoveryRole == RecoveryRoleAdopted {
			if canonical != "" && canonical != s.Path {
				// Ambiguous: do not retarget.
				return ""
			}
			canonical = s.Path
		}
	}
	if canonical == "" || canonical == current {
		return ""
	}
	return canonical
}
