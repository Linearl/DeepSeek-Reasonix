package main

// 任务 892A：manage_sessions 工具族的宿主半。单工具多 action（而非一堆小工具），
// 覆盖会话与分组的整理操作：
//
//	list / rename_session / move_session / reorder_session /
//	group_create / group_rename / group_delete（以上两套分组通用，kind 区分）/
//	rollback / op_log
//
// 设计约束（任务书）：
//   - 每个写 action ①写数据源（经 updateProjectsFile/SaveSessionGroupsVersioned，
//     sidecar 走 host API 链路，不直改文件）②发出 reason="metadata" 的
//     project-tree-changed-v2 事件——侧栏刷新只认它（20261009 清理战役实证）；
//   - 分级安全：轻操作（单实体 rename/reorder/create/move）自动执行；
//     删除（含批量影响面）强制提案制——先 dry-run 返回计划，confirm=true 才落地；
//   - 每次写留操作日志（JSONL，含 before/after 快照），rollback 可据此回退。
//
// 层级语义（892 拍板）：分组任意嵌套（ parentId ），默认删除 = 子组升顶
// （promote 到被删组的父层），cascade=true 才连子树一起删；会话只被摘组，
// 永不随组删除。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// manageSessionsOpLogName is the operation log next to the other desktop
	// state files: one JSON line per manage write, newest last. The before
	// snapshot in each entry is the rollback source (task 892 安全：操作留
	// 日志可回退, 对齐 858 教训).
	manageSessionsOpLogName = "manage-sessions-operations.jsonl"
	// manageSessionsBatchThreshold flags plans at or above this blast radius
	// (deleted groups + unfiled topics) as batch operations in the proposal.
	// Deletion always needs confirm regardless; the flag tells the caller to
	// show the full list before confirming.
	manageSessionsBatchThreshold = 10
	// manageSessionsOpLogCap bounds how far back rollback can reach: the log
	// file is truncated to the newest entries past this size.
	manageSessionsOpLogMaxEntries = 500
)

// groupKind selects which of the two grouping systems a group_* action
// operates on. "session" = sidebar session groups (topics), "project" =
// the project-group catalog.
const (
	groupKindSession = "session"
	groupKindProject = "project"
)

var manageSessionsOpMu sync.Mutex

// manageSessionsScope defaults an omitted scope to the global roster — the
// tool's docs say "omit to let the host resolve", and for group_* actions
// with no session context that resolution is global.
func manageSessionsScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "global"
	}
	return scope
}

// ── view types (the B-slice frontend contract) ──────────────────────────────

// ManageSessionsTopicView is one grouped session in the list view. Order is
// the index inside its group's topicIds array (the persisted manual order).
type ManageSessionsTopicView struct {
	TopicID string `json:"topicId"`
	Title   string `json:"title"`
	Order   int    `json:"order"`
}

// ManageSessionsGroupView is one session group with its nesting. ParentID is
// the tool/frontend-facing spelling of the stored `parent` field (task-350
// additive key); Depth starts at 0 for top-level groups.
type ManageSessionsGroupView struct {
	ID       string                    `json:"id"`
	Title    string                    `json:"title"`
	ParentID string                    `json:"parentId,omitempty"`
	Depth    int                       `json:"depth"`
	Order    int                       `json:"order"`
	Topics   []ManageSessionsTopicView `json:"topics"`
}

// ManageSessionsProjectGroupView is one project-group catalog entity.
type ManageSessionsProjectGroupView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ParentID  string `json:"parentId,omitempty"`
	Depth     int    `json:"depth"`
	SortOrder int    `json:"sortOrder"`
}

// ManageSessionsSessionView reports one session's group membership + order.
// GroupPath is the title chain root→leaf, so a caller can render the nesting
// without walking parents itself.
type ManageSessionsSessionView struct {
	TopicID       string   `json:"topicId"`
	Title         string   `json:"title"`
	Scope         string   `json:"scope"`
	WorkspaceRoot string   `json:"workspaceRoot,omitempty"`
	GroupID       string   `json:"groupId,omitempty"`
	GroupPath     []string `json:"groupPath,omitempty"`
	Order         int      `json:"order"`
}

// ManageSessionsListResult is the `list` action payload. Groups is keyed by
// roster: "global" for the global sidebar, "project|<root>" for a project.
type ManageSessionsListResult struct {
	Groups        map[string][]ManageSessionsGroupView `json:"groups"`
	ProjectGroups []ManageSessionsProjectGroupView     `json:"projectGroups"`
	Sessions      []ManageSessionsSessionView          `json:"sessions"`
}

// ManageSessionsGroupResult reports one group mutation.
type ManageSessionsGroupResult struct {
	Kind    string            `json:"kind"`
	Scope   string            `json:"scope,omitempty"`
	Root    string            `json:"workspaceRoot,omitempty"`
	GroupID string            `json:"groupId"`
	Title   string            `json:"title,omitempty"`
	Parent  string            `json:"parentId,omitempty"`
	Applied bool              `json:"applied"`
	Plan    *deleteGroupsPlan `json:"plan,omitempty"`
	OpID    string            `json:"opId,omitempty"`
}

// ── write-change signal ─────────────────────────────────────────────────────

// emitManageSessionsWriteChanged is the task-892 write contract: every write
// action publishes the reason="metadata" v2 tree event. It is emitted
// synchronously here (the catalog's own publish rides an async sync and may
// lag a manual refresh), and the catalog metadata sync is requested on top so
// the catalog rows converge too.
func (a *App) emitManageSessionsWriteChanged() {
	revision := uint64(0)
	if catalog := a.sessionCatalog.Load(); catalog != nil {
		revision = catalog.Status().Revision
	}
	a.emitProjectTreeChangedV2(revision, nil, "metadata")
	a.requestSessionCatalogMetadataSync()
}

// ── operation log ───────────────────────────────────────────────────────────

// manageSessionsOperation is one JSONL log line. Before/After hold the full
// roster snapshot (session groups or project groups, per Kind) so a rollback
// can restore the exact prior state through the versioned save.
type manageSessionsOperation struct {
	OpID          string            `json:"opId"`
	At            string            `json:"at"`
	Action        string            `json:"action"`
	Kind          string            `json:"kind"`
	Scope         string            `json:"scope,omitempty"`
	WorkspaceRoot string            `json:"workspaceRoot,omitempty"`
	Subject       string            `json:"subject,omitempty"`
	Plan          *deleteGroupsPlan `json:"plan,omitempty"`
	Before        json.RawMessage   `json:"before,omitempty"`
	After         json.RawMessage   `json:"after,omitempty"`
	Applied       bool              `json:"applied"`
	Error         string            `json:"error,omitempty"`
}

func manageSessionsOpLogPath() string {
	return filepath.Join(desktopConfigDir(), manageSessionsOpLogName)
}

func newManageSessionsOpID() string {
	return fmt.Sprintf("op-%d", time.Now().UnixNano())
}

// appendManageSessionsOp writes one log line and returns its op id. A log
// failure is returned, never swallowed — 892 安全约束要求写操作必留痕。
func appendManageSessionsOp(op manageSessionsOperation) (string, error) {
	manageSessionsOpMu.Lock()
	defer manageSessionsOpMu.Unlock()

	if op.OpID == "" {
		op.OpID = newManageSessionsOpID()
	}
	if op.At == "" {
		op.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(op)
	if err != nil {
		return op.OpID, err
	}
	path := manageSessionsOpLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return op.OpID, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return op.OpID, err
	}
	_, writeErr := f.Write(append(line, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return op.OpID, writeErr
	}
	if closeErr != nil {
		return op.OpID, closeErr
	}
	return op.OpID, nil
}

// readManageSessionsOps returns the log, oldest first, bounded to the newest
// manageSessionsOpLogMaxEntries lines.
func readManageSessionsOps() ([]manageSessionsOperation, error) {
	b, err := readFileUTF8(manageSessionsOpLogPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	out := make([]manageSessionsOperation, 0, 16)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var op manageSessionsOperation
		if json.Unmarshal([]byte(line), &op) != nil {
			continue
		}
		out = append(out, op)
	}
	if len(out) > manageSessionsOpLogMaxEntries {
		out = out[len(out)-manageSessionsOpLogMaxEntries:]
	}
	return out, nil
}

// ── session-group write operations ──────────────────────────────────────────

// mutateSessionGroups applies one semantic change to a workspace's group
// roster through the CAS loop AddTopicToGroup uses: read snapshot, mutate,
// versioned save, retry on conflict. The mutate callback returns the group the
// change is about (for the result envelope) plus whether anything changed.
func (a *App) mutateSessionGroups(scope, workspaceRoot, subject string, mutate func(groups []desktopGroup) ([]desktopGroup, desktopGroup, bool, error)) (desktopGroup, []desktopGroup, []desktopGroup, error) {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return desktopGroup{}, nil, nil, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := a.GetProjectGroups(scope, workspaceRoot)
		if err != nil {
			return desktopGroup{}, nil, nil, err
		}
		before := append([]desktopGroup(nil), snapshot.Groups...)
		groups := append([]desktopGroup(nil), snapshot.Groups...)
		next, target, changed, err := mutate(groups)
		if err != nil {
			return desktopGroup{}, nil, nil, err
		}
		if !changed {
			return target, before, before, nil
		}
		result, err := a.SaveSessionGroupsVersioned(scope, workspaceRoot, snapshot.Revision, next)
		if err != nil {
			return desktopGroup{}, before, nil, err
		}
		if result.Applied {
			return target, before, result.Groups, nil
		}
	}
	return desktopGroup{}, nil, nil, fmt.Errorf("session group %q: config changed concurrently, retry", subject)
}

// CreateSessionGroup creates an empty group, optionally nested under parentID.
// An unknown parent is rejected up front — creating an orphan would already
// fail the save-side validation, but failing here names the culprit.
func (a *App) CreateSessionGroup(scope, workspaceRoot, groupID, title, parentID string) (ManageSessionsGroupResult, error) {
	scope = manageSessionsScope(scope)
	groupID, title, parentID = strings.TrimSpace(groupID), strings.TrimSpace(title), strings.TrimSpace(parentID)
	if title == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("title is required")
	}
	if groupID == "" {
		groupID = "collab-" + strings.ToLower(title)
	}
	if parentID == groupID {
		return ManageSessionsGroupResult{}, fmt.Errorf("group %q cannot be its own parent", groupID)
	}
	result := ManageSessionsGroupResult{Kind: groupKindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID, Title: title, Parent: parentID}
	_, before, after, err := a.mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []desktopGroup) ([]desktopGroup, desktopGroup, bool, error) {
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
				return nil, desktopGroup{}, false, fmt.Errorf("parent group %q does not exist", parentID)
			}
		}
		created := desktopGroup{ID: groupID, Title: title, Parent: parentID, TopicIDs: []string{}}
		return append(groups, created), created, true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logManageSessionsWrite("group_create", groupKindSession, scope, workspaceRoot, groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	a.emitManageSessionsWriteChanged()
	if logErr != nil {
		return result, fmt.Errorf("group created but operation log failed: %w", logErr)
	}
	return result, nil
}

func logManageSessionsWrite(action, kind, scope, workspaceRoot, subject string, plan *deleteGroupsPlan, before, after any, applied bool) (string, error) {
	op := manageSessionsOperation{Action: action, Kind: kind, Scope: scope, WorkspaceRoot: workspaceRoot, Subject: subject, Plan: plan, Applied: applied}
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
	return appendManageSessionsOp(op)
}

// RenameSessionGroup retitles a group, keeping id, parent and membership.
func (a *App) RenameSessionGroup(scope, workspaceRoot, groupID, title string) (ManageSessionsGroupResult, error) {
	scope = manageSessionsScope(scope)
	groupID, title = strings.TrimSpace(groupID), strings.TrimSpace(title)
	if groupID == "" || title == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("groupId and title are required")
	}
	result := ManageSessionsGroupResult{Kind: groupKindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID, Title: title}
	target, before, after, err := a.mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []desktopGroup) ([]desktopGroup, desktopGroup, bool, error) {
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
		return nil, desktopGroup{}, false, fmt.Errorf("group %q not found", groupID)
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logManageSessionsWrite("group_rename", groupKindSession, scope, workspaceRoot, groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	result.Parent = target.Parent
	a.emitManageSessionsWriteChanged()
	if logErr != nil {
		return result, fmt.Errorf("group renamed but operation log failed: %w", logErr)
	}
	return result, nil
}

// DeleteSessionGroup implements the proposal flow (提案制): without confirm
// it returns the deletion plan and writes nothing; with confirm=true it
// executes the planned semantics — 升顶 default, cascade for the whole
// subtree. Sessions are unfiled, never deleted.
func (a *App) DeleteSessionGroup(scope, workspaceRoot, groupID string, cascade, confirm bool) (ManageSessionsGroupResult, error) {
	scope = manageSessionsScope(scope)
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("groupId is required")
	}
	result := ManageSessionsGroupResult{Kind: groupKindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID}
	snapshot, err := a.GetProjectGroups(scope, workspaceRoot)
	if err != nil {
		return result, err
	}
	plan, err := planGroupDeletion(snapshot.Groups, groupID, cascade)
	if err != nil {
		return result, err
	}
	plan.Batch = len(plan.Deleted)+len(plan.UnfiledTopics) >= manageSessionsBatchThreshold
	result.Plan = &plan
	if !confirm {
		// Proposal only: nothing is written, nothing is logged as a mutation.
		return result, nil
	}
	_, before, after, err := a.mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []desktopGroup) ([]desktopGroup, desktopGroup, bool, error) {
		next, _, applyErr := applyGroupDeletion(groups, groupID, cascade)
		if applyErr != nil {
			return nil, desktopGroup{}, false, applyErr
		}
		return next, desktopGroup{}, true, nil
	})
	if err != nil {
		// The plan was computed against a stale roster; re-report so the
		// caller can re-propose against the fresh state.
		return result, err
	}
	opID, logErr := logManageSessionsWrite("group_delete", groupKindSession, scope, workspaceRoot, groupID, &plan, before, after, true)
	result.Applied = true
	result.OpID = opID
	a.emitManageSessionsWriteChanged()
	if logErr != nil {
		return result, fmt.Errorf("group deleted but operation log failed: %w", logErr)
	}
	return result, nil
}

// ReorderTopicInGroup moves one filed topic inside its group's member order —
// the manual order the sidebar drag produces. The topic must already be a
// member; index is clamped into range so a stale drag target cannot fail the
// write after the frontend already moved the row.
func (a *App) ReorderTopicInGroup(scope, workspaceRoot, topicID, groupID string, index int) (ManageSessionsGroupResult, error) {
	scope = manageSessionsScope(scope)
	topicID, groupID = strings.TrimSpace(topicID), strings.TrimSpace(groupID)
	if topicID == "" || groupID == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("topicId and groupId are required")
	}
	result := ManageSessionsGroupResult{Kind: groupKindSession, Scope: scope, Root: workspaceRoot, GroupID: groupID}
	target, before, after, err := a.mutateSessionGroups(scope, workspaceRoot, groupID, func(groups []desktopGroup) ([]desktopGroup, desktopGroup, bool, error) {
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
		return nil, desktopGroup{}, false, fmt.Errorf("group %q not found", groupID)
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logManageSessionsWrite("reorder_session", groupKindSession, scope, workspaceRoot, topicID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	result.Title = target.Title
	a.emitManageSessionsWriteChanged()
	if logErr != nil {
		return result, fmt.Errorf("session reordered but operation log failed: %w", logErr)
	}
	return result, nil
}

// ── project-group catalog operations ────────────────────────────────────────

// projectGroupsRoster converts the project-group catalog into the shared
// desktopGroup shape so the subtree/deletion helpers serve both systems.
func projectGroupsRoster(groups []desktopProjectGroup) []desktopGroup {
	out := make([]desktopGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, desktopGroup{ID: group.ID, Title: group.Title, Parent: group.Parent})
	}
	return out
}

// applyProjectGroupDeletion plans and applies a catalog deletion through the
// shared helpers, then clears project pointers at the deleted ids (a project
// whose group vanished is unfiled, not deleted).
func applyProjectGroupDeletion(f *desktopProjectFile, groupID string, cascade bool) (deleteGroupsPlan, bool, error) {
	roster := projectGroupsRoster(f.ProjectGroups)
	plan, err := planGroupDeletion(roster, groupID, cascade)
	if err != nil {
		return plan, false, err
	}
	deleted := make(map[string]bool, len(plan.Deleted))
	for _, id := range plan.Deleted {
		deleted[id] = true
	}
	next, _, applyErr := applyGroupDeletion(roster, groupID, cascade)
	if applyErr != nil {
		return plan, false, applyErr
	}
	restored := make([]desktopProjectGroup, 0, len(next))
	originals := make(map[string]desktopProjectGroup, len(f.ProjectGroups))
	for _, group := range f.ProjectGroups {
		originals[group.ID] = group
	}
	for _, group := range next {
		original := originals[group.ID]
		original.Title = group.Title
		original.Parent = group.Parent
		restored = append(restored, original)
	}
	changed := !equalProjectGroups(restored, f.ProjectGroups)
	for i := range f.Projects {
		if deleted[strings.TrimSpace(f.Projects[i].ProjectGroup)] {
			f.Projects[i].ProjectGroup = ""
			changed = true
		}
	}
	f.ProjectGroups = restored
	return plan, changed, nil
}

func (a *App) CreateProjectGroup(groupID, title, parentID string) (ManageSessionsGroupResult, error) {
	groupID, title, parentID = strings.TrimSpace(groupID), strings.TrimSpace(title), strings.TrimSpace(parentID)
	if title == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("title is required")
	}
	if groupID == "" {
		groupID = "pgroup-" + strings.ToLower(title)
	}
	result := ManageSessionsGroupResult{Kind: groupKindProject, GroupID: groupID, Title: title, Parent: parentID}
	var before, after []desktopProjectGroup
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		before = append([]desktopProjectGroup(nil), f.ProjectGroups...)
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
		candidate := append(append([]desktopProjectGroup(nil), f.ProjectGroups...), desktopProjectGroup{ID: groupID, Title: title, Parent: parentID})
		if err := validateProjectGroups(candidate); err != nil {
			return false, err
		}
		f.ProjectGroups = candidate
		after = append([]desktopProjectGroup(nil), candidate...)
		return true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logManageSessionsWrite("group_create", groupKindProject, "", "", groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	a.emitManageSessionsWriteChanged()
	if logErr != nil {
		return result, fmt.Errorf("project group created but operation log failed: %w", logErr)
	}
	return result, nil
}

func (a *App) RenameProjectGroup(groupID, title string) (ManageSessionsGroupResult, error) {
	groupID, title = strings.TrimSpace(groupID), strings.TrimSpace(title)
	if groupID == "" || title == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("groupId and title are required")
	}
	result := ManageSessionsGroupResult{Kind: groupKindProject, GroupID: groupID, Title: title}
	var before, after []desktopProjectGroup
	err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		before = append([]desktopProjectGroup(nil), f.ProjectGroups...)
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
		after = append([]desktopProjectGroup(nil), f.ProjectGroups...)
		return true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logManageSessionsWrite("group_rename", groupKindProject, "", "", groupID, nil, before, after, true)
	result.Applied = true
	result.OpID = opID
	a.emitManageSessionsWriteChanged()
	if logErr != nil {
		return result, fmt.Errorf("project group renamed but operation log failed: %w", logErr)
	}
	return result, nil
}

// DeleteProjectGroup mirrors DeleteSessionGroup's proposal flow on the
// catalog: plan without confirm, execute with it, default 升顶 / opt-in
// cascade, projects of deleted groups unfiled.
func (a *App) DeleteProjectGroup(groupID string, cascade, confirm bool) (ManageSessionsGroupResult, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("groupId is required")
	}
	result := ManageSessionsGroupResult{Kind: groupKindProject, GroupID: groupID}
	f := loadProjectsFile()
	plan, err := planGroupDeletion(projectGroupsRoster(f.ProjectGroups), groupID, cascade)
	if err != nil {
		return result, err
	}
	plan.Batch = len(plan.Deleted)+len(plan.UnfiledTopics) >= manageSessionsBatchThreshold
	result.Plan = &plan
	if !confirm {
		return result, nil
	}
	var before, after []desktopProjectGroup
	err = updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		before = append([]desktopProjectGroup(nil), f.ProjectGroups...)
		appliedPlan, changed, applyErr := applyProjectGroupDeletion(f, groupID, cascade)
		if applyErr != nil {
			return false, applyErr
		}
		if !changed {
			return false, nil
		}
		plan = appliedPlan
		after = append([]desktopProjectGroup(nil), f.ProjectGroups...)
		return true, nil
	})
	if err != nil {
		return result, err
	}
	opID, logErr := logManageSessionsWrite("group_delete", groupKindProject, "", "", groupID, &plan, before, after, true)
	result.Applied = true
	result.OpID = opID
	a.emitManageSessionsWriteChanged()
	if logErr != nil {
		return result, fmt.Errorf("project group deleted but operation log failed: %w", logErr)
	}
	return result, nil
}

// ── list ────────────────────────────────────────────────────────────────────

func manageSessionsGroupDepth(parents map[string]string, id string) int {
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

func manageSessionsGroupViews(groups []desktopGroup, titles map[string]string) []ManageSessionsGroupView {
	parents := make(map[string]string, len(groups))
	for _, group := range groups {
		parents[group.ID] = strings.TrimSpace(group.Parent)
	}
	out := make([]ManageSessionsGroupView, 0, len(groups))
	for order, group := range groups {
		view := ManageSessionsGroupView{
			ID: group.ID, Title: group.Title,
			ParentID: strings.TrimSpace(group.Parent),
			Depth:    manageSessionsGroupDepth(parents, group.ID),
			Order:    order,
			Topics:   make([]ManageSessionsTopicView, 0, len(group.TopicIDs)),
		}
		for i, topicID := range group.TopicIDs {
			view.Topics = append(view.Topics, ManageSessionsTopicView{TopicID: topicID, Title: titles[topicID], Order: i})
		}
		out = append(out, view)
	}
	return out
}

// ManageSessionsList builds the `list` payload: every roster's groups with
// nesting/order, the project-group catalog, and each session's membership.
func (a *App) ManageSessionsList() (ManageSessionsListResult, error) {
	f := loadProjectsFile()
	result := ManageSessionsListResult{
		Groups:        map[string][]ManageSessionsGroupView{},
		ProjectGroups: []ManageSessionsProjectGroupView{},
		Sessions:      []ManageSessionsSessionView{},
	}
	appendRoster := func(key, workspaceRoot string, roster []desktopGroup) {
		titles := loadTopicTitles(workspaceRoot)
		groups := a.filterKnownGroupMembers(nonNilGroups(roster))
		result.Groups[key] = manageSessionsGroupViews(groups, titles)
		parents := make(map[string]string, len(groups))
		titlesByID := make(map[string]string, len(groups))
		for _, group := range groups {
			parents[group.ID] = strings.TrimSpace(group.Parent)
			titlesByID[group.ID] = group.Title
		}
		scope, wsRoot := "global", ""
		if key != "global" {
			scope, wsRoot = "project", workspaceRoot
		}
		for _, group := range groups {
			for order, topicID := range group.TopicIDs {
				view := ManageSessionsSessionView{TopicID: topicID, Title: titles[topicID], Scope: scope, WorkspaceRoot: wsRoot, GroupID: group.ID, Order: order}
				// GroupPath: root→leaf title chain.
				for id, guard := group.ID, 0; id != "" && guard < maxSessionGroupDepth; guard++ {
					view.GroupPath = append([]string{titlesByID[id]}, view.GroupPath...)
					id = parents[id]
				}
				result.Sessions = append(result.Sessions, view)
			}
		}
	}
	appendRoster("global", "", f.GlobalGroups)
	for _, project := range f.Projects {
		appendRoster("project|"+project.Root, project.Root, project.Groups)
	}
	parents := make(map[string]string, len(f.ProjectGroups))
	for _, group := range f.ProjectGroups {
		parents[group.ID] = strings.TrimSpace(group.Parent)
	}
	for _, group := range f.ProjectGroups {
		result.ProjectGroups = append(result.ProjectGroups, ManageSessionsProjectGroupView{
			ID: group.ID, Title: group.Title,
			ParentID:  strings.TrimSpace(group.Parent),
			Depth:     manageSessionsGroupDepth(parents, group.ID),
			SortOrder: group.SortOrder,
		})
	}
	return result, nil
}

// ── rollback ────────────────────────────────────────────────────────────────

// RollbackManageOperation restores the `before` snapshot of one logged write.
// There is no interlock against newer writes beyond the versioned save's own
// validation — rolling back an old op after later edits replays history, so
// callers should roll back the newest matching op promptly.
func (a *App) RollbackManageOperation(opID string) (ManageSessionsGroupResult, error) {
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return ManageSessionsGroupResult{}, fmt.Errorf("opId is required")
	}
	ops, err := readManageSessionsOps()
	if err != nil {
		return ManageSessionsGroupResult{}, err
	}
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		if op.OpID != opID || !op.Applied || len(op.Before) == 0 {
			continue
		}
		result := ManageSessionsGroupResult{Kind: op.Kind, Scope: op.Scope, Root: op.WorkspaceRoot, GroupID: op.Subject}
		switch op.Kind {
		case groupKindSession:
			var before []desktopGroup
			if err := json.Unmarshal(op.Before, &before); err != nil {
				return result, fmt.Errorf("rollback %q: corrupt before snapshot: %w", opID, err)
			}
			if err := a.SaveSessionGroups(op.Scope, op.WorkspaceRoot, before); err != nil {
				return result, fmt.Errorf("rollback %q: %w", opID, err)
			}
		case groupKindProject:
			var before []desktopProjectGroup
			if err := json.Unmarshal(op.Before, &before); err != nil {
				return result, fmt.Errorf("rollback %q: corrupt before snapshot: %w", opID, err)
			}
			if err := validateProjectGroups(before); err != nil {
				return result, fmt.Errorf("rollback %q: %w", opID, err)
			}
			if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
				if equalProjectGroups(before, f.ProjectGroups) {
					return false, nil
				}
				f.ProjectGroups = append([]desktopProjectGroup(nil), before...)
				return true, nil
			}); err != nil {
				return result, fmt.Errorf("rollback %q: %w", opID, err)
			}
		default:
			return result, fmt.Errorf("rollback %q: unsupported kind %q", opID, op.Kind)
		}
		a.emitManageSessionsWriteChanged()
		result.Applied = true
		if _, logErr := logManageSessionsWrite("rollback", op.Kind, op.Scope, op.WorkspaceRoot, opID, nil, nil, nil, true); logErr != nil {
			return result, fmt.Errorf("rollback applied but operation log failed: %w", logErr)
		}
		return result, nil
	}
	return ManageSessionsGroupResult{}, fmt.Errorf("operation %q not found in the manage-sessions log", opID)
}

func equalProjectGroups(left, right []desktopProjectGroup) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Title != right[i].Title || left[i].Parent != right[i].Parent || left[i].SortOrder != right[i].SortOrder {
			return false
		}
	}
	return true
}

// ── dispatch (the manage_sessions tool / Wails face) ────────────────────────

// ManageSessions is the single dispatch face behind the agent's
// manage_sessions tool and the frontend's grouping actions. Action names match
// the tool schema; the args are the same JSON object, so the tool forwards
// them verbatim after target resolution.
func (a *App) ManageSessions(action string, args json.RawMessage) (json.RawMessage, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	encode := func(value any) (json.RawMessage, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	switch strings.TrimSpace(action) {
	case "list":
		result, err := a.ManageSessionsList()
		if err != nil {
			return nil, err
		}
		return encode(result)
	case "rename_session":
		var p struct {
			TopicID     string `json:"topic_id"`
			SessionPath string `json:"session_path"`
			Title       string `json:"title"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		result, err := a.renameCollabSession(p.TopicID, p.SessionPath, p.Title)
		if err != nil {
			return nil, err
		}
		a.emitManageSessionsWriteChanged()
		return encode(map[string]any{"status": "renamed", "result": result})
	case "move_session":
		var p struct {
			TopicID       string `json:"topic_id"`
			SessionPath   string `json:"session_path"`
			Scope         string `json:"scope"`
			WorkspaceRoot string `json:"workspace_root"`
			GroupID       string `json:"group_id"`
			Group         string `json:"group"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		// The "先摘后 append" is MoveTopicToGroup's single versioned save: the
		// removal from the old group and the append to the new one land in one
		// CAS write, so a failure leaves the session exactly where it was.
		result, err := a.moveCollabTopicToGroup(p.TopicID, p.SessionPath, p.Scope, p.WorkspaceRoot, p.GroupID, p.Group)
		if err != nil {
			return nil, err
		}
		a.emitManageSessionsWriteChanged()
		return encode(map[string]any{"status": "moved", "result": result})
	case "reorder_session":
		var p struct {
			TopicID       string `json:"topic_id"`
			GroupID       string `json:"group_id"`
			Index         int    `json:"index"`
			Scope         string `json:"scope"`
			WorkspaceRoot string `json:"workspace_root"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		result, err := a.ReorderTopicInGroup(p.Scope, p.WorkspaceRoot, p.TopicID, p.GroupID, p.Index)
		if err != nil {
			return nil, err
		}
		return encode(result)
	case "group_create", "group_rename", "group_delete":
		var p struct {
			Kind          string `json:"kind"`
			GroupID       string `json:"group_id"`
			Title         string `json:"title"`
			ParentID      string `json:"parent_id"`
			Cascade       bool   `json:"cascade"`
			Confirm       bool   `json:"confirm"`
			Scope         string `json:"scope"`
			WorkspaceRoot string `json:"workspace_root"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		kind := strings.TrimSpace(p.Kind)
		if kind == "" {
			kind = groupKindSession
		}
		if kind != groupKindSession && kind != groupKindProject {
			return nil, fmt.Errorf("kind %q is not supported (use %q or %q)", p.Kind, groupKindSession, groupKindProject)
		}
		if kind == groupKindProject {
			switch strings.TrimSpace(action) {
			case "group_create":
				result, err := a.CreateProjectGroup(p.GroupID, p.Title, p.ParentID)
				if err != nil {
					return nil, err
				}
				return encode(result)
			case "group_rename":
				result, err := a.RenameProjectGroup(p.GroupID, p.Title)
				if err != nil {
					return nil, err
				}
				return encode(result)
			default:
				result, err := a.DeleteProjectGroup(p.GroupID, p.Cascade, p.Confirm)
				if err != nil {
					return nil, err
				}
				return encode(result)
			}
		}
		switch strings.TrimSpace(action) {
		case "group_create":
			result, err := a.CreateSessionGroup(p.Scope, p.WorkspaceRoot, p.GroupID, p.Title, p.ParentID)
			if err != nil {
				return nil, err
			}
			return encode(result)
		case "group_rename":
			result, err := a.RenameSessionGroup(p.Scope, p.WorkspaceRoot, p.GroupID, p.Title)
			if err != nil {
				return nil, err
			}
			return encode(result)
		default:
			result, err := a.DeleteSessionGroup(p.Scope, p.WorkspaceRoot, p.GroupID, p.Cascade, p.Confirm)
			if err != nil {
				return nil, err
			}
			return encode(result)
		}
	case "rollback":
		var p struct {
			OpID string `json:"op_id"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		result, err := a.RollbackManageOperation(p.OpID)
		if err != nil {
			return nil, err
		}
		return encode(result)
	case "op_log":
		ops, err := readManageSessionsOps()
		if err != nil {
			return nil, err
		}
		if ops == nil {
			ops = []manageSessionsOperation{}
		}
		return encode(map[string]any{"operations": ops})
	default:
		return nil, fmt.Errorf("unknown action %q (list, rename_session, move_session, reorder_session, group_create, group_rename, group_delete, rollback, op_log)", action)
	}
}

// AgentManageSessions adapts ManageSessions to the agent capability
// signature (agent.ManageSessionsFunc). Kept tiny so boot wiring stays a
// one-liner.
func (a *App) AgentManageSessions(action string, args json.RawMessage) (json.RawMessage, error) {
	return a.ManageSessions(action, args)
}
