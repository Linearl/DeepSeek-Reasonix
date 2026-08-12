package sessioncatalog

import (
	"context"
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

	if record.RecoveryCopy || (parentPath != "" && agent.RecoveryBranchCoveredByParent(record.Path, filepath.Dir(record.Path))) {
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

func classifyRecoveryLineageFromContent(record SessionRecord) SessionRecord {
	record.RecoveryCopy = false
	return classifyRecoveryLineage(record)
}

// promoteCanonicalLeaves marks unique non-covered leaves that cover every
// ancestor in their group as adopted/canonical. Multiple non-covering leaves
// stay diverged. open/running/pinned/leased decisions are left to callers.
func promoteCanonicalLeaves(records []SessionRecord) []SessionRecord {
	byGroup, groupRoot := recoveryLineageGroups(records)
	for groupID, idxs := range byGroup {
		for _, i := range idxs {
			records[i].RecoveryRole = RecoveryRoleDiverged
			records[i].RecoveryCanonical = false
		}
		rootIndex, ok := groupRoot[groupID]
		if !ok {
			continue
		}
		candidate, ok := uniqueLongestRecovery(records, idxs)
		if ok && recoveryCandidateCovers(records, candidate, rootIndex, idxs) {
			records[candidate].RecoveryRole = RecoveryRoleAdopted
			records[candidate].RecoveryCanonical = true
		}
	}
	return records
}

func recoveryLineageGroups(records []SessionRecord) (map[string][]int, map[string]int) {
	byID := make(map[string]int, len(records))
	for i := range records {
		byID[agent.BranchID(records[i].Path)] = i
	}
	byGroup := map[string][]int{}
	groupRoot := map[string]int{}
	for i := range records {
		rec := records[i]
		if !rec.Recovered {
			continue
		}
		parentID := strings.TrimSpace(rec.ParentID)
		seen := map[string]struct{}{}
		for parentID != "" {
			if _, loop := seen[parentID]; loop {
				parentID = ""
				break
			}
			seen[parentID] = struct{}{}
			parentIndex, ok := byID[parentID]
			if !ok {
				parentID = ""
				break
			}
			parent := records[parentIndex]
			if !parent.Recovered {
				groupRoot[parentID] = parentIndex
				break
			}
			parentID = strings.TrimSpace(parent.ParentID)
		}
		if parentID != "" {
			records[i].RecoveryGroupID = parentID
		}
	}
	for i, rec := range records {
		if rec.RecoveryGroupID == "" || rec.RecoveryRole == RecoveryRoleCoveredCopy || rec.RecoveryRole == RecoveryRoleNormal {
			continue
		}
		byGroup[rec.RecoveryGroupID] = append(byGroup[rec.RecoveryGroupID], i)
	}
	return byGroup, groupRoot
}

func uniqueLongestRecovery(records []SessionRecord, idxs []int) (int, bool) {
	if len(idxs) == 0 {
		return 0, false
	}
	candidate := idxs[0]
	if len(idxs) == 1 {
		return candidate, true
	}
	if records[candidate].TurnsState == TurnsUnknown {
		return 0, false
	}
	for _, i := range idxs[1:] {
		if records[i].TurnsState == TurnsUnknown {
			return 0, false
		}
		if records[i].Turns > records[candidate].Turns {
			candidate = i
		}
	}
	for _, i := range idxs {
		if i != candidate && records[i].Turns >= records[candidate].Turns {
			return 0, false
		}
	}
	return candidate, true
}

func recoveryCandidateCovers(records []SessionRecord, candidate, root int, idxs []int) bool {
	if !agent.SessionContentCovers(records[candidate].Path, records[root].Path) {
		return false
	}
	for _, i := range idxs {
		if i != candidate && !agent.SessionContentCovers(records[candidate].Path, records[i].Path) {
			return false
		}
	}
	return true
}

func (c *Catalog) refreshDirectoryRecoveryLineage(ctx context.Context, target DirectoryTarget, records []SessionRecord) error {
	for i := range records {
		records[i] = classifyRecoveryLineageFromContent(normalizeSessionRecord(records[i]))
	}
	records = promoteCanonicalLeaves(records)
	c.mutationMu.Lock()
	defer c.mutationMu.Unlock()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	affected := map[TopicKey]struct{}{}
	changed := false
	for _, record := range records {
		if !record.Recovered {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE catalog_sessions SET recovery_copy=?,recovery_group_id=?,recovery_role=?,recovery_canonical=? WHERE path=?`,
			boolToInt(record.RecoveryCopy), record.RecoveryGroupID, record.RecoveryRole, boolToInt(record.RecoveryCanonical), record.Path)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if rows, _ := result.RowsAffected(); rows > 0 {
			changed = true
		}
		if record.TopicID != "" {
			affected[TopicKey{Scope: record.Scope, WorkspaceRoot: record.WorkspaceRoot, TopicID: record.TopicID}] = struct{}{}
		}
	}
	if !changed {
		return tx.Rollback()
	}
	for key := range affected {
		if err := recomputeTopic(ctx, tx, key); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	revision, err := bumpRevision(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.publishRevision(revision, []string{target.WorkspaceRoot}, "recovery_lineage")
	return nil
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
