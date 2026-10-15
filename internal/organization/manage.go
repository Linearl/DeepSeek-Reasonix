package organization

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Task 911: the manage-groups surface GC drives over the serve gateway. The
// semantics mirror the desktop package's manage_sessions tool family
// (task 892A) one-for-one — proposal-flow deletions (plan first, confirm to
// execute), 升顶 default / cascade opt-in nesting rules, sessions unfiled but
// never deleted, and a JSONL operation log per write so a rollback can restore
// the exact prior roster.
//
// Desktop coupling intentionally dropped here: no Wails event emission (the
// desktop frontend converges by re-reading on refresh) and no heartbeat
// auto-filing (a desktop-runtime concern).

const (
	// OpLogName is the operation log next to the other desktop state files.
	OpLogName = "manage-sessions-operations.jsonl"
	// BatchThreshold flags plans at or above this blast radius as batch ops.
	BatchThreshold = 10
	// OpLogMaxEntries bounds how far back rollback can reach.
	OpLogMaxEntries = 500
)

var opMu sync.Mutex

// TopicView is one grouped session in the list view.
type TopicView struct {
	TopicID string `json:"topicId"`
	Title   string `json:"title"`
	Order   int    `json:"order"`
}

// GroupView is one session group with its nesting.
type GroupView struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	ParentID string      `json:"parentId,omitempty"`
	Depth    int         `json:"depth"`
	Order    int         `json:"order"`
	Topics   []TopicView `json:"topics"`
}

// ProjectGroupView is one project-group catalog entity.
type ProjectGroupView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ParentID  string `json:"parentId,omitempty"`
	Depth     int    `json:"depth"`
	SortOrder int    `json:"sortOrder"`
}

// SessionView reports one session's group membership + order.
type SessionView struct {
	TopicID       string   `json:"topicId"`
	Title         string   `json:"title"`
	Scope         string   `json:"scope"`
	WorkspaceRoot string   `json:"workspaceRoot,omitempty"`
	GroupID       string   `json:"groupId,omitempty"`
	GroupPath     []string `json:"groupPath,omitempty"`
	Order         int      `json:"order"`
}

// ListResult is the list payload: every roster's groups with nesting/order,
// the project-group catalog, and each session's membership.
type ListResult struct {
	Groups        map[string][]GroupView `json:"groups"`
	ProjectGroups []ProjectGroupView     `json:"projectGroups"`
	Sessions      []SessionView          `json:"sessions"`
	Projects      []ProjectSummary       `json:"projects"`
}

// ProjectSummary is one project's identity for the GC picker.
type ProjectSummary struct {
	Root         string `json:"root"`
	Title        string `json:"title,omitempty"`
	Color        string `json:"color,omitempty"`
	ProjectGroup string `json:"projectGroup,omitempty"`
}

// GroupResult reports one group mutation.
type GroupResult struct {
	Kind    string        `json:"kind"`
	Scope   string        `json:"scope,omitempty"`
	Root    string        `json:"workspaceRoot,omitempty"`
	GroupID string        `json:"groupId"`
	Title   string        `json:"title,omitempty"`
	Parent  string        `json:"parentId,omitempty"`
	Applied bool          `json:"applied"`
	Plan    *DeletionPlan `json:"plan,omitempty"`
	OpID    string        `json:"opId,omitempty"`
}

// ── operation log ───────────────────────────────────────────────────────────

// operation is one JSONL log line; Before/After hold the full roster snapshot
// so a rollback can restore the exact prior state.
type operation struct {
	OpID          string          `json:"opId"`
	At            string          `json:"at"`
	Action        string          `json:"action"`
	Kind          string          `json:"kind"`
	Scope         string          `json:"scope,omitempty"`
	WorkspaceRoot string          `json:"workspaceRoot,omitempty"`
	Subject       string          `json:"subject,omitempty"`
	Plan          *DeletionPlan   `json:"plan,omitempty"`
	Before        json.RawMessage `json:"before,omitempty"`
	After         json.RawMessage `json:"after,omitempty"`
	Applied       bool            `json:"applied"`
}

func logWrite(action, kind, scope, workspaceRoot, subject string, plan *DeletionPlan, before, after any, applied bool) (string, error) {
	op := operation{Action: action, Kind: kind, Scope: scope, WorkspaceRoot: workspaceRoot, Subject: subject, Plan: plan, Applied: applied}
	if before != nil {
		if raw, err := json.Marshal(before); err == nil {
			op.Before = raw
		}
	}
	if after != nil {
		if raw, err := json.Marshal(after); err == nil {
			op.After = raw
		}
	}
	return appendOp(op)
}

func appendOp(op operation) (string, error) {
	op.At = time.Now().UTC().Format(time.RFC3339Nano)
	op.OpID = fmt.Sprintf("op-%d", time.Now().UnixNano())
	b, err := json.Marshal(op)
	if err != nil {
		return "", err
	}
	opMu.Lock()
	defer opMu.Unlock()
	path := filepath.Join(dir(), OpLogName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return "", err
	}
	truncateOpLogLocked(path)
	return op.OpID, nil
}

// truncateOpLogLocked keeps the newest OpLogMaxEntries lines.
func truncateOpLogLocked(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) <= OpLogMaxEntries {
		return
	}
	kept := strings.Join(lines[len(lines)-OpLogMaxEntries:], "\n") + "\n"
	_ = os.WriteFile(path, []byte(kept), 0o644)
}

// ── roster read/write primitives ────────────────────────────────────────────

// ReadSnapshot is the versioned roster read (CAS token for writes).
func ReadSnapshot(scope, workspaceRoot string) (Snapshot, error) {
	scope, workspaceRoot, err := NormalizeTarget(scope, workspaceRoot)
	if err != nil {
		return Snapshot{Groups: []Group{}}, err
	}
	f := LoadFile()
	if scope == "global" {
		return Snapshot{Groups: NonNilGroups(f.GlobalGroups), Revision: f.GlobalGroupsRevision, Applied: true}, nil
	}
	i := ProjectIndexByRoot(f.Projects, workspaceRoot)
	if i < 0 {
		return Snapshot{Groups: []Group{}, Applied: true}, nil
	}
	return Snapshot{Groups: NonNilGroups(f.Projects[i].Groups), Revision: f.Projects[i].GroupsRevision, Applied: true}, nil
}

// SaveSessionGroups persists a roster through the revision CAS: the caller
// passes the revision it read; a concurrent write bumps the revision and the
// save reports Applied=false with the current snapshot instead.
func SaveSessionGroups(scope, workspaceRoot string, expectedRevision uint64, groups []Group) (Snapshot, error) {
	scope, workspaceRoot, err := NormalizeTarget(scope, workspaceRoot)
	if err != nil {
		return Snapshot{Groups: []Group{}}, err
	}
	if err := ValidateSessionGroups(groups); err != nil {
		return Snapshot{Groups: []Group{}}, err
	}
	normalized := NormalizeGroups(groups)
	result := Snapshot{Groups: []Group{}}
	err = UpdateFile(func(f *File) (bool, error) {
		deleted := make(map[string]bool, len(f.DeletedTopics))
		for _, topicID := range f.DeletedTopics {
			deleted[topicID] = true
		}
		candidate := groupsWithoutDeletedTopics(normalized, deleted)
		currentGroups, currentRevision := f.GlobalGroups, f.GlobalGroupsRevision
		var project *Project
		if scope == "project" {
			i := ProjectIndexByRoot(f.Projects, workspaceRoot)
			if i < 0 {
				return false, fmt.Errorf("project %q not found", workspaceRoot)
			}
			project = &f.Projects[i]
			currentGroups, currentRevision = project.Groups, project.GroupsRevision
		}
		if currentRevision != expectedRevision {
			result = Snapshot{Groups: NonNilGroups(currentGroups), Revision: currentRevision, Applied: false}
			return false, nil
		}
		if EqualGroups(candidate, currentGroups) {
			result = Snapshot{Groups: NonNilGroups(currentGroups), Revision: currentRevision, Applied: true}
			return false, nil
		}
		currentRevision++
		if project == nil {
			f.GlobalGroups, f.GlobalGroupsRevision = candidate, currentRevision
		} else {
			project.Groups, project.GroupsRevision = candidate, currentRevision
		}
		result = Snapshot{Groups: NonNilGroups(candidate), Revision: currentRevision, Applied: true}
		return true, nil
	})
	if err != nil {
		return Snapshot{Groups: []Group{}}, err
	}
	return result, nil
}

// mutateSessionGroups applies one semantic change through the CAS loop: read
// snapshot, mutate, versioned save, retry on conflict.
func mutateSessionGroups(scope, workspaceRoot, subject string, mutate func(groups []Group) ([]Group, Group, bool, error)) (Group, []Group, []Group, error) {
	scope, workspaceRoot, err := NormalizeTarget(scope, workspaceRoot)
	if err != nil {
		return Group{}, nil, nil, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := ReadSnapshot(scope, workspaceRoot)
		if err != nil {
			return Group{}, nil, nil, err
		}
		before := append([]Group(nil), snapshot.Groups...)
		groups := append([]Group(nil), snapshot.Groups...)
		next, target, changed, err := mutate(groups)
		if err != nil {
			return Group{}, nil, nil, err
		}
		if !changed {
			return target, before, before, nil
		}
		result, err := SaveSessionGroups(scope, workspaceRoot, snapshot.Revision, next)
		if err != nil {
			return Group{}, before, nil, err
		}
		if result.Applied {
			return target, before, result.Groups, nil
		}
	}
	return Group{}, nil, nil, fmt.Errorf("session group %q: config changed concurrently, retry", subject)
}

// ── session-group operations ────────────────────────────────────────────────

func defaultScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "global"
	}
	return scope
}

// CreateSessionGroup creates an empty group, optionally nested under parentID.
func CreateSessionGroup(scope, workspaceRoot, groupID, title, parentID string) (GroupResult, error) {
	scope = defaultScope(scope)
	groupID, title, parentID = strings.TrimSpace(groupID), strings.TrimSpace(title), strings.TrimSpace(parentID)
	if title == "" {
		return GroupResult{}, fmt.Errorf("title is required")
	}
	if groupID == "" {
		groupID = "collab-" + strings.ToLower(title)
	}
	if parentID == groupID {
		return GroupResult{}, fmt.Errorf("group %q cannot be its own parent", groupID)
	}
	result := GroupResult{Kind: KindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID, Title: title, Parent: parentID}
	_, before, after, err := mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []Group) ([]Group, Group, bool, error) {
		for _, group := range groups {
			if strings.EqualFold(group.ID, groupID) {
				return nil, group, false, fmt.Errorf("group %q already exists (title %q)", groupID, group.Title)
			}
		}
		if parentID != "" {
			found := false
			for _, group := range groups {
				if strings.EqualFold(group.ID, parentID) {
					found = true
					break
				}
			}
			if !found {
				return nil, Group{}, false, fmt.Errorf("parent group %q does not exist", parentID)
			}
		}
		created := Group{ID: groupID, Title: title, Parent: parentID, TopicIDs: []string{}}
		return append(groups, created), created, true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logWrite("group_create", KindSession, scope, workspaceRoot, groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	if logErr != nil {
		return result, fmt.Errorf("group created but operation log failed: %w", logErr)
	}
	return result, nil
}

// RenameSessionGroup retitles a group, keeping id, parent and membership.
func RenameSessionGroup(scope, workspaceRoot, groupID, title string) (GroupResult, error) {
	scope = defaultScope(scope)
	groupID, title = strings.TrimSpace(groupID), strings.TrimSpace(title)
	if groupID == "" || title == "" {
		return GroupResult{}, fmt.Errorf("groupId and title are required")
	}
	result := GroupResult{Kind: KindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID, Title: title}
	target, before, after, err := mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []Group) ([]Group, Group, bool, error) {
		for i, group := range groups {
			if strings.EqualFold(group.ID, groupID) {
				if group.Title == title {
					return groups, group, false, nil
				}
				updated := group
				updated.Title = title
				groups[i] = updated
				return groups, updated, true, nil
			}
		}
		return nil, Group{}, false, fmt.Errorf("group %q not found", groupID)
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logWrite("group_rename", KindSession, scope, workspaceRoot, groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	result.Parent = target.Parent
	if logErr != nil {
		return result, fmt.Errorf("group renamed but operation log failed: %w", logErr)
	}
	return result, nil
}

// DeleteSessionGroup implements the proposal flow: without confirm it returns
// the deletion plan and writes nothing; with confirm=true it executes — 升顶
// default, cascade for the whole subtree. Sessions are unfiled, never deleted.
func DeleteSessionGroup(scope, workspaceRoot, groupID string, cascade, confirm bool) (GroupResult, error) {
	scope = defaultScope(scope)
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return GroupResult{}, fmt.Errorf("groupId is required")
	}
	result := GroupResult{Kind: KindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID}
	snapshot, err := ReadSnapshot(scope, workspaceRoot)
	if err != nil {
		return result, err
	}
	plan, err := PlanGroupDeletion(snapshot.Groups, groupID, cascade)
	if err != nil {
		return result, err
	}
	plan.Batch = len(plan.Deleted)+len(plan.UnfiledTopics) >= BatchThreshold
	result.Plan = &plan
	if !confirm {
		return result, nil
	}
	_, before, after, err := mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []Group) ([]Group, Group, bool, error) {
		next, _, applyErr := ApplyGroupDeletion(groups, groupID, cascade)
		if applyErr != nil {
			return nil, Group{}, false, applyErr
		}
		return next, Group{}, true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logWrite("group_delete", KindSession, scope, workspaceRoot, groupID, &plan, before, after, true)
	result.Applied = true
	result.OpID = opID
	if logErr != nil {
		return result, fmt.Errorf("group deleted but operation log failed: %w", logErr)
	}
	return result, nil
}

// ReorderTopicInGroup moves one filed topic inside its group's member order.
// The topic must already be a member; index is clamped into range.
func ReorderTopicInGroup(scope, workspaceRoot, topicID, groupID string, index int) (GroupResult, error) {
	scope = defaultScope(scope)
	topicID, groupID = strings.TrimSpace(topicID), strings.TrimSpace(groupID)
	if topicID == "" || groupID == "" {
		return GroupResult{}, fmt.Errorf("topicId and groupId are required")
	}
	result := GroupResult{Kind: KindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID}
	target, before, after, err := mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []Group) ([]Group, Group, bool, error) {
		for i, group := range groups {
			if !strings.EqualFold(group.ID, groupID) {
				continue
			}
			pos := -1
			for j, member := range group.TopicIDs {
				if member == topicID {
					pos = j
					break
				}
			}
			if pos < 0 {
				return nil, group, false, fmt.Errorf("topic %q is not in group %q", topicID, groupID)
			}
			if index < 0 {
				index = 0
			}
			if index >= len(group.TopicIDs) {
				index = len(group.TopicIDs) - 1
			}
			if pos == index {
				return groups, group, false, nil
			}
			members := append([]string(nil), group.TopicIDs...)
			members = append(members[:pos], members[pos+1:]...)
			updated := group
			updated.TopicIDs = append(members[:index], append([]string{topicID}, members[index:]...)...)
			groups[i] = updated
			return groups, updated, true, nil
		}
		return nil, Group{}, false, fmt.Errorf("group %q not found", groupID)
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logWrite("reorder_session", KindSession, scope, workspaceRoot, topicID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	result.Title = target.Title
	if logErr != nil {
		return result, fmt.Errorf("session reordered but operation log failed: %w", logErr)
	}
	return result, nil
}

// FileTopic moves one session into a group AND removes it from every other
// group in the same roster, so the session ends up in exactly one place (the
// desktop task-170 semantics — an append-only "move" leaves the session behind
// in its old group whenever that group is written first). A missing target
// group is created on demand; an empty groupId+groupTitle is a no-op.
func FileTopic(scope, workspaceRoot, topicID, groupID, groupTitle string) (GroupResult, error) {
	scope, workspaceRoot, err := NormalizeTarget(scope, workspaceRoot)
	if err != nil {
		return GroupResult{}, err
	}
	topicID, groupID, groupTitle = strings.TrimSpace(topicID), strings.TrimSpace(groupID), strings.TrimSpace(groupTitle)
	if topicID == "" {
		return GroupResult{}, fmt.Errorf("topicId is required")
	}
	if groupID == "" && groupTitle == "" {
		return GroupResult{}, nil
	}
	result := GroupResult{Kind: KindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID, Title: groupTitle}
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := ReadSnapshot(scope, workspaceRoot)
		if err != nil {
			return result, err
		}
		groups := append([]Group(nil), snapshot.Groups...)
		target := -1
		for i, g := range groups {
			if groupID != "" {
				if strings.EqualFold(strings.TrimSpace(g.ID), groupID) {
					target = i
					break
				}
				continue
			}
			if strings.EqualFold(strings.TrimSpace(g.Title), groupTitle) {
				target = i
				break
			}
		}
		if target < 0 {
			if groupID == "" {
				groupID = "collab-" + strings.ToLower(groupTitle)
			}
			if groupTitle == "" {
				// An id with no title is not a usable group label; fall back
				// to the id so the row is still readable in the sidebar.
				groupTitle = groupID
			}
			groups = append(groups, Group{ID: groupID, Title: groupTitle, TopicIDs: []string{topicID}})
		} else {
			result.GroupID = groups[target].ID
			result.Title = groups[target].Title
			already := false
			for _, member := range groups[target].TopicIDs {
				if member == topicID {
					already = true
					break
				}
			}
			if !already {
				groups[target].TopicIDs = append(groups[target].TopicIDs, topicID)
			}
		}
		// Remove the topic from every OTHER group so it ends up in one place.
		removed := []string{}
		for i := range groups {
			if i == target {
				continue
			}
			kept := groups[i].TopicIDs[:0]
			for _, member := range groups[i].TopicIDs {
				if member == topicID {
					removed = append(removed, groups[i].ID)
					continue
				}
				kept = append(kept, member)
			}
			groups[i].TopicIDs = kept
		}
		saved, err := SaveSessionGroups(scope, workspaceRoot, snapshot.Revision, groups)
		if err != nil {
			return result, err
		}
		if saved.Applied {
			opID, logErr := logWrite("file_topic", KindSession, scope, workspaceRoot, topicID, nil, snapshot.Groups, saved.Groups, true)
			result.Applied = true
			result.OpID = opID
			_ = removed
			if logErr != nil {
				return result, fmt.Errorf("topic filed but operation log failed: %w", logErr)
			}
			return result, nil
		}
	}
	return result, fmt.Errorf("session group %q: config changed concurrently, retry", firstNonEmpty(groupID, groupTitle))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ── project-group catalog operations ────────────────────────────────────────

// projectGroupsRoster converts the catalog into the shared Group shape.
func projectGroupsRoster(groups []ProjectGroup) []Group {
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		out = append(out, Group{ID: group.ID, Title: group.Title, Parent: group.Parent})
	}
	return out
}

// ApplyProjectGroupDeletion plans and applies a catalog deletion, then clears
// project pointers at the deleted ids (a project whose group vanished is
// unfiled, not deleted).
func ApplyProjectGroupDeletion(f *File, groupID string, cascade bool) (DeletionPlan, bool, error) {
	roster := projectGroupsRoster(f.ProjectGroups)
	plan, err := PlanGroupDeletion(roster, groupID, cascade)
	if err != nil {
		return plan, false, err
	}
	deleted := make(map[string]bool, len(plan.Deleted))
	for _, id := range plan.Deleted {
		deleted[id] = true
	}
	next, _, applyErr := ApplyGroupDeletion(roster, groupID, cascade)
	if applyErr != nil {
		return plan, false, applyErr
	}
	restored := make([]ProjectGroup, 0, len(next))
	originals := make(map[string]ProjectGroup, len(f.ProjectGroups))
	for _, group := range f.ProjectGroups {
		originals[group.ID] = group
	}
	for _, group := range next {
		original := originals[group.ID]
		original.Title = group.Title
		original.Parent = group.Parent
		restored = append(restored, original)
	}
	changed := !EqualProjectGroups(restored, f.ProjectGroups)
	for i := range f.Projects {
		if deleted[strings.TrimSpace(f.Projects[i].ProjectGroup)] {
			f.Projects[i].ProjectGroup = ""
			changed = true
		}
	}
	f.ProjectGroups = restored
	return plan, changed, nil
}

// CreateProjectGroup creates a catalog entity, optionally nested.
func CreateProjectGroup(groupID, title, parentID string) (GroupResult, error) {
	groupID, title, parentID = strings.TrimSpace(groupID), strings.TrimSpace(title), strings.TrimSpace(parentID)
	if title == "" {
		return GroupResult{}, fmt.Errorf("title is required")
	}
	if groupID == "" {
		groupID = "pgroup-" + strings.ToLower(title)
	}
	result := GroupResult{Kind: KindProject, GroupID: groupID, Title: title, Parent: parentID}
	var before, after []ProjectGroup
	err := UpdateFile(func(f *File) (bool, error) {
		before = append([]ProjectGroup(nil), f.ProjectGroups...)
		for _, group := range f.ProjectGroups {
			if strings.EqualFold(group.ID, groupID) {
				return false, fmt.Errorf("project group %q already exists (title %q)", groupID, group.Title)
			}
		}
		if parentID != "" {
			found := false
			for _, group := range f.ProjectGroups {
				if strings.EqualFold(group.ID, parentID) {
					found = true
					break
				}
			}
			if !found {
				return false, fmt.Errorf("parent project group %q does not exist", parentID)
			}
		}
		candidate := append(append([]ProjectGroup(nil), f.ProjectGroups...), ProjectGroup{ID: groupID, Title: title, Parent: parentID})
		if err := ValidateProjectGroups(candidate); err != nil {
			return false, err
		}
		f.ProjectGroups = candidate
		after = append([]ProjectGroup(nil), candidate...)
		return true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logWrite("group_create", KindProject, "", "", groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	if logErr != nil {
		return result, fmt.Errorf("project group created but operation log failed: %w", logErr)
	}
	return result, nil
}

// RenameProjectGroup retitles a catalog entity.
func RenameProjectGroup(groupID, title string) (GroupResult, error) {
	groupID, title = strings.TrimSpace(groupID), strings.TrimSpace(title)
	if groupID == "" || title == "" {
		return GroupResult{}, fmt.Errorf("groupId and title are required")
	}
	result := GroupResult{Kind: KindProject, GroupID: groupID, Title: title}
	var before, after []ProjectGroup
	err := UpdateFile(func(f *File) (bool, error) {
		before = append([]ProjectGroup(nil), f.ProjectGroups...)
		changed := false
		for i, group := range f.ProjectGroups {
			if !strings.EqualFold(group.ID, groupID) {
				continue
			}
			if group.Title == title {
				return false, nil
			}
			f.ProjectGroups[i].Title = title
			changed = true
			break
		}
		if !changed {
			return false, fmt.Errorf("project group %q not found", groupID)
		}
		after = append([]ProjectGroup(nil), f.ProjectGroups...)
		return true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logWrite("group_rename", KindProject, "", "", groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	if logErr != nil {
		return result, fmt.Errorf("project group renamed but operation log failed: %w", logErr)
	}
	return result, nil
}

// DeleteProjectGroup mirrors DeleteSessionGroup's proposal flow on the
// catalog: plan without confirm, execute with it, default 升顶 / opt-in
// cascade, projects of deleted groups unfiled.
func DeleteProjectGroup(groupID string, cascade, confirm bool) (GroupResult, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return GroupResult{}, fmt.Errorf("groupId is required")
	}
	result := GroupResult{Kind: KindProject, GroupID: groupID}
	f := LoadFile()
	plan, err := PlanGroupDeletion(projectGroupsRoster(f.ProjectGroups), groupID, cascade)
	if err != nil {
		return result, err
	}
	plan.Batch = len(plan.Deleted)+len(plan.UnfiledTopics) >= BatchThreshold
	result.Plan = &plan
	if !confirm {
		return result, nil
	}
	var before, after []ProjectGroup
	err = UpdateFile(func(f *File) (bool, error) {
		before = append([]ProjectGroup(nil), f.ProjectGroups...)
		appliedPlan, changed, applyErr := ApplyProjectGroupDeletion(f, groupID, cascade)
		if applyErr != nil {
			return false, applyErr
		}
		if !changed {
			return false, nil
		}
		plan = appliedPlan
		after = append([]ProjectGroup(nil), f.ProjectGroups...)
		return true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logWrite("group_delete", KindProject, "", "", groupID, &plan, before, after, true)
	result.Applied = true
	result.OpID = opID
	if logErr != nil {
		return result, fmt.Errorf("project group deleted but operation log failed: %w", logErr)
	}
	return result, nil
}

// ── list ────────────────────────────────────────────────────────────────────

func groupDepth(parents map[string]string, id string) int {
	depth := 0
	seen := map[string]bool{}
	for {
		parent, ok := parents[strings.TrimSpace(id)]
		if !ok || parent == "" || seen[parent] {
			return depth
		}
		seen[parent] = true
		depth++
		id = parent
	}
}

func groupViews(groups []Group, titles map[string]string) []GroupView {
	parents := make(map[string]string, len(groups))
	for _, group := range groups {
		parents[group.ID] = strings.TrimSpace(group.Parent)
	}
	out := make([]GroupView, 0, len(groups))
	for order, group := range groups {
		view := GroupView{
			ID: group.ID, Title: group.Title,
			ParentID: strings.TrimSpace(group.Parent),
			Depth:    groupDepth(parents, group.ID),
			Order:    order,
			Topics:   make([]TopicView, 0, len(group.TopicIDs)),
		}
		for i, topicID := range group.TopicIDs {
			view.Topics = append(view.Topics, TopicView{TopicID: topicID, Title: titles[topicID], Order: i})
		}
		out = append(out, view)
	}
	return out
}

// List builds the list payload: the global roster, the project-group catalog,
// per-project rosters, and each session's membership.
func List() (ListResult, error) {
	f := LoadFile()
	out := ListResult{
		Groups:   map[string][]GroupView{},
		Sessions: []SessionView{},
		Projects: make([]ProjectSummary, 0, len(f.Projects)),
	}
	globalTitles := map[string]string{}
	for _, topicID := range f.GlobalTopics {
		globalTitles[topicID] = ""
	}
	out.Groups["global"] = groupViews(NonNilGroups(f.GlobalGroups), globalTitles)
	for _, group := range NonNilGroups(f.GlobalGroups) {
		for _, topicID := range group.TopicIDs {
			path := groupPath(f.GlobalGroups, group.ID)
			out.Sessions = append(out.Sessions, SessionView{TopicID: topicID, Scope: "global", GroupID: group.ID, GroupPath: path})
		}
	}
	for i := range f.Projects {
		project := &f.Projects[i]
		out.Projects = append(out.Projects, ProjectSummary{Root: project.Root, Title: project.Title, Color: project.Color, ProjectGroup: project.ProjectGroup})
		key := "project|" + project.Root
		titles := map[string]string{}
		for _, topicID := range project.Topics {
			titles[topicID] = ""
		}
		out.Groups[key] = groupViews(NonNilGroups(project.Groups), titles)
		for _, group := range NonNilGroups(project.Groups) {
			for _, topicID := range group.TopicIDs {
				path := groupPath(project.Groups, group.ID)
				out.Sessions = append(out.Sessions, SessionView{TopicID: topicID, Scope: "project", WorkspaceRoot: project.Root, GroupID: group.ID, GroupPath: path})
			}
		}
	}
	depths := make(map[string]string, len(f.ProjectGroups))
	for _, group := range f.ProjectGroups {
		depths[group.ID] = strings.TrimSpace(group.Parent)
	}
	out.ProjectGroups = make([]ProjectGroupView, 0, len(f.ProjectGroups))
	for _, group := range f.ProjectGroups {
		out.ProjectGroups = append(out.ProjectGroups, ProjectGroupView{
			ID: group.ID, Title: group.Title,
			ParentID:  strings.TrimSpace(group.Parent),
			Depth:     groupDepth(depths, group.ID),
			SortOrder: group.SortOrder,
		})
	}
	return out, nil
}

// groupPath resolves the title chain root→leaf for one group.
func groupPath(groups []Group, id string) []string {
	byID := make(map[string]Group, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	var chain []string
	current := strings.TrimSpace(id)
	seen := map[string]bool{}
	for current != "" {
		group, ok := byID[current]
		if !ok || seen[current] {
			break
		}
		seen[current] = true
		chain = append([]string{group.Title}, chain...)
		current = strings.TrimSpace(group.Parent)
	}
	return chain
}
