package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxSessionGroups       = 100
	maxSessionGroupIDRunes = 160
	maxSessionGroupRunes   = 120
	maxSessionGroupTopics  = 10_000
	// maxSessionGroupDepth (task 892) caps nesting at a generous 64 levels:
	// arbitrary depth is the point (simulating a company org chart), but an
	// unbounded chain from corrupt data would make every ancestor walk — cycle
	// check, tree render, cascade planning — quadratic, so a sanity cap stays.
	// Cycles are prevented by the explicit ancestor walk in
	// validateGroupHierarchy, not by the depth cap.
	maxSessionGroupDepth = 64
	// maxProjectGroups bounds the project-group catalog (task 892): the same
	// scale as session groups, one global list per workspace file.
	maxProjectGroups = 100
)

type desktopProject struct {
	Root             string         `json:"root"`
	Title            string         `json:"title,omitempty"`
	Color            string         `json:"color,omitempty"`
	ProjectGroup     string         `json:"projectGroup,omitempty"`
	Topics           []string       `json:"topics"`
	PinnedTopics     []string       `json:"pinnedTopics,omitempty"`
	ManualTopicOrder bool           `json:"manualTopicOrder,omitempty"`
	Groups           []desktopGroup `json:"groups,omitempty"`
	GroupsRevision   uint64         `json:"-"`
}

type desktopGroup struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	TopicIDs []string `json:"topicIds,omitempty"`
	// Parent (task 350) nests this group under another group of the same
	// workspace. Purely additive and optional: empty for flat data, and old
	// binaries ignore the unknown JSON key, so flat and hierarchical saves stay
	// interchangeable. HIERARCHY IS DISPLAY ONLY — it must never imply
	// permission or command semantics (层级≠指挥权); addressing keeps using the
	// flat group id.
	Parent string `json:"parent,omitempty"`
}

type ProjectGroupsSnapshot struct {
	Groups   []desktopGroup `json:"groups"`
	Revision uint64         `json:"revision"`
	Applied  bool           `json:"applied"`
}

type desktopProjectFile struct {
	GlobalTitle            string         `json:"globalTitle,omitempty"`
	GlobalColor            string         `json:"globalColor,omitempty"`
	GlobalTopics           []string       `json:"globalTopics,omitempty"`
	GlobalPinnedTopics     []string       `json:"globalPinnedTopics,omitempty"`
	GlobalManualTopicOrder bool           `json:"globalManualTopicOrder,omitempty"`
	GlobalGroups           []desktopGroup `json:"globalGroups,omitempty"`
	GlobalGroupsRevision   uint64         `json:"-"`
	DeletedTopics          []string       `json:"deletedTopics,omitempty"`
	PinnedProjects         []string       `json:"pinnedProjects,omitempty"`
	SidebarOrder           []string       `json:"sidebarOrder,omitempty"`
	// ProjectGroups (task 892) is the project-group catalog: the entities a
	// project's projectGroup field points at. Before 892 the frontend kept the
	// entity list in localStorage only; the backend just carried the pointer
	// string. Moving the roster into the catalog file makes nesting (parent)
	// and tool-managed CRUD durable and cross-window consistent. Flat/absent
	// data reads as an empty list — old saves never carried the key.
	ProjectGroups []desktopProjectGroup `json:"projectGroups,omitempty"`
	Projects      []desktopProject      `json:"projects"`
}

// desktopProjectGroup is one project-group entity in the catalog, mirroring
// desktopGroup's nesting contract: parent references another project group's
// id, empty = top level. Hierarchy is display organization only (层级≠指挥权);
// a project references its group through the desktopProject.ProjectGroup id.
type desktopProjectGroup struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Parent    string `json:"parent,omitempty"`
	SortOrder int    `json:"sortOrder,omitempty"`
}

func normalizeGroups(groups []desktopGroup) []desktopGroup {
	out := make([]desktopGroup, 0, len(groups))
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	for _, group := range groups {
		group.ID = strings.TrimSpace(group.ID)
		group.Title = strings.TrimSpace(group.Title)
		// Task 350: a self parent is meaningless; drop it rather than reject the
		// whole roster so stale editor state cannot wedge a save.
		group.Parent = strings.TrimSpace(group.Parent)
		if group.Parent == group.ID {
			group.Parent = ""
		}
		if group.ID == "" || seenGroups[group.ID] {
			continue
		}
		seenGroups[group.ID] = true
		topics := make([]string, 0, len(group.TopicIDs))
		for _, topicID := range group.TopicIDs {
			topicID = strings.TrimSpace(topicID)
			if topicID == "" || seenTopics[topicID] {
				continue
			}
			seenTopics[topicID] = true
			topics = append(topics, topicID)
		}
		group.TopicIDs = topics
		out = append(out, group)
	}
	return out
}

func mergeDesktopGroups(left, right []desktopGroup) []desktopGroup {
	merged := append(append([]desktopGroup(nil), left...), right...)
	byID := make(map[string]int, len(merged))
	out := make([]desktopGroup, 0, len(merged))
	for _, group := range merged {
		group.ID = strings.TrimSpace(group.ID)
		if group.ID == "" {
			continue
		}
		if index, ok := byID[group.ID]; ok {
			if out[index].Title == "" {
				out[index].Title = strings.TrimSpace(group.Title)
			}
			// Task 350: keep the parent pointer across merges the same way the
			// title is kept — first side wins, the other fills a blank.
			if out[index].Parent == "" {
				out[index].Parent = strings.TrimSpace(group.Parent)
			}
			out[index].TopicIDs = append(out[index].TopicIDs, group.TopicIDs...)
			continue
		}
		byID[group.ID] = len(out)
		out = append(out, group)
	}
	return normalizeGroups(out)
}

func validateSessionGroups(groups []desktopGroup) error {
	if len(groups) > maxSessionGroups {
		return fmt.Errorf("too many session groups: %d", len(groups))
	}
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	for _, group := range groups {
		id, title := strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		if id == "" || title == "" {
			return fmt.Errorf("session group id and title are required")
		}
		if utf8.RuneCountInString(id) > maxSessionGroupIDRunes || utf8.RuneCountInString(title) > maxSessionGroupRunes {
			return fmt.Errorf("session group id or title is too long")
		}
		if seenGroups[id] {
			return fmt.Errorf("duplicate session group %q", id)
		}
		seenGroups[id] = true
		if len(group.TopicIDs) > maxSessionGroupTopics {
			return fmt.Errorf("session group %q has too many topics", id)
		}
		for _, topicID := range group.TopicIDs {
			topicID = strings.TrimSpace(topicID)
			if topicID == "" || seenTopics[topicID] {
				return fmt.Errorf("invalid or duplicate grouped topic %q", topicID)
			}
			seenTopics[topicID] = true
		}
	}
	return validateSessionGroupHierarchy(groups)
}

// validateSessionGroupHierarchy checks the task-350 parent pointers under the
// task-892 recursive contract: a parent must exist, the ancestor chain must not
// loop (a cycle would make the tree unrenderable and every walk unbounded), and
// the chain must stay within maxSessionGroupDepth. The hierarchy is display
// organization only, so these checks guard data integrity, not permissions.
func validateSessionGroupHierarchy(groups []desktopGroup) error {
	parents := make(map[string]string, len(groups))
	for _, group := range groups {
		parents[group.ID] = strings.TrimSpace(group.Parent)
	}
	for _, group := range groups {
		if err := validateGroupParentChain(group.ID, parents, maxSessionGroupDepth); err != nil {
			return fmt.Errorf("session group %q: %w", group.ID, err)
		}
	}
	return nil
}

// validateGroupParentChain walks one group's ancestor chain: every parent must
// exist, the walk may never revisit a node (cycle), and it may never exceed
// maxDepth links. Shared by the session-group and project-group validators —
// both trees are flat id→parent maps, so one walker covers both.
func validateGroupParentChain(id string, parents map[string]string, maxDepth int) error {
	visited := make(map[string]bool)
	current := id
	for depth := 0; ; depth++ {
		parent := parents[current]
		if parent == "" {
			return nil
		}
		if _, ok := parents[parent]; !ok {
			return fmt.Errorf("unknown parent %q", parent)
		}
		if visited[parent] {
			return fmt.Errorf("parent chain forms a cycle at %q", parent)
		}
		if depth >= maxDepth {
			return fmt.Errorf("parent chain exceeds max depth %d", maxDepth)
		}
		visited[current] = true
		current = parent
	}
}

// ── task 892: subtree helpers (arbitrary nesting) ────────────────────────────

// groupChildIDs returns the direct children of parentID, in roster order.
func groupChildIDs(groups []desktopGroup, parentID string) []string {
	parentID = strings.TrimSpace(parentID)
	var out []string
	for _, group := range groups {
		if strings.TrimSpace(group.Parent) == parentID && group.ID != parentID {
			out = append(out, group.ID)
		}
	}
	return out
}

// groupSubtreeIDs returns groupID plus every descendant, parents before
// children (stable roster order within a level). The entry itself is always
// first, so a caller can peel it off to get "the children's subtree".
func groupSubtreeIDs(groups []desktopGroup, groupID string) []string {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil
	}
	out := []string{groupID}
	for i := 0; i < len(out); i++ {
		out = append(out, groupChildIDs(groups, out[i])...)
	}
	return out
}

// deleteGroupsPlan describes what a group deletion will do before it does it —
// the proposal half of the task-892 safety contract (批量删组提案制).
type deleteGroupsPlan struct {
	// Deleted lists the group ids the deletion removes.
	Deleted []string `json:"deleted"`
	// Promoted lists child groups re-parented onto the deleted group's parent
	// (or the top level) — the default, non-destructive semantics.
	Promoted []string `json:"promoted,omitempty"`
	// UnfiledTopics lists the topics that lose their group because the group
	// holding them is deleted. Topics are never deleted, only unfiled.
	UnfiledTopics []string `json:"unfiledTopics,omitempty"`
	// Batch marks a plan at or above the tool layer's blast-radius threshold;
	// the proposal response carries it so the caller shows the full list
	// before confirming (task 892 安全分级).
	Batch bool `json:"batch,omitempty"`
}

// planGroupDeletion computes the effect of deleting groupID: with cascade the
// whole subtree goes; without it the direct children are promoted to the
// deleted group's parent (top level when it was a root group) and only the
// group itself is removed. The chosen semantics (task 892): 升顶 default,
// cascade opt-in — deletion never silently discards a child group's
// organization, mirroring 用户决策判据 3 (删保护性结构前先有替代路径).
func planGroupDeletion(groups []desktopGroup, groupID string, cascade bool) (deleteGroupsPlan, error) {
	groupID = strings.TrimSpace(groupID)
	byID := make(map[string]desktopGroup, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	if _, ok := byID[groupID]; !ok {
		return deleteGroupsPlan{}, fmt.Errorf("group %q not found", groupID)
	}
	plan := deleteGroupsPlan{Deleted: []string{groupID}}
	if cascade {
		plan.Deleted = groupSubtreeIDs(groups, groupID)
	} else if children := groupChildIDs(groups, groupID); len(children) > 0 {
		plan.Promoted = children
	}
	deletedSet := make(map[string]bool, len(plan.Deleted))
	for _, id := range plan.Deleted {
		deletedSet[id] = true
	}
	for _, group := range groups {
		if !deletedSet[group.ID] {
			continue
		}
		plan.UnfiledTopics = append(plan.UnfiledTopics, group.TopicIDs...)
	}
	return plan, nil
}

// applyGroupDeletion removes the planned groups and promotes the direct
// children of each non-cascade deleted group onto its parent ("" = top level).
// Callers persist the result through the versioned save, which re-validates
// the whole roster — a plan computed against stale data cannot corrupt it.
func applyGroupDeletion(groups []desktopGroup, groupID string, cascade bool) ([]desktopGroup, deleteGroupsPlan, error) {
	groupID = strings.TrimSpace(groupID)
	plan, err := planGroupDeletion(groups, groupID, cascade)
	if err != nil {
		return nil, plan, err
	}
	deleted := make(map[string]bool, len(plan.Deleted))
	for _, id := range plan.Deleted {
		deleted[id] = true
	}
	promoted := make(map[string]bool, len(plan.Promoted))
	for _, id := range plan.Promoted {
		promoted[id] = true
	}
	out := make([]desktopGroup, 0, len(groups))
	for _, group := range groups {
		if deleted[group.ID] {
			continue
		}
		if promoted[group.ID] {
			// Re-home onto the deleted group's own parent. The plan only
			// promotes direct children, so one lookup is the whole story.
			var parent string
			for _, candidate := range groups {
				if candidate.ID == groupID {
					parent = strings.TrimSpace(candidate.Parent)
					break
				}
			}
			group.Parent = parent
		}
		out = append(out, group)
	}
	return out, plan, nil
}

// ── task 892: project-group catalog (the second of the two grouping systems) ─

// normalizeProjectGroups trims and de-duplicates the catalog roster the same
// way normalizeGroups does for session groups: dup ids drop (first wins), a
// self parent is meaningless and clears, and absent data stays an empty slice
// rather than nil so old flat saves round-trip losslessly.
func normalizeProjectGroups(groups []desktopProjectGroup) []desktopProjectGroup {
	out := make([]desktopProjectGroup, 0, len(groups))
	seen := make(map[string]bool, len(groups))
	for _, group := range groups {
		group.ID = strings.TrimSpace(group.ID)
		group.Title = strings.TrimSpace(group.Title)
		group.Parent = strings.TrimSpace(group.Parent)
		if group.Parent == group.ID {
			group.Parent = ""
		}
		if group.ID == "" || seen[group.ID] {
			continue
		}
		seen[group.ID] = true
		out = append(out, group)
	}
	return out
}

// validateProjectGroups bounds and validates the catalog roster, reusing the
// shared ancestor walker: unknown parent, cycles and over-deep chains are all
// rejected exactly like session groups.
func validateProjectGroups(groups []desktopProjectGroup) error {
	if len(groups) > maxProjectGroups {
		return fmt.Errorf("too many project groups: %d", len(groups))
	}
	parents := make(map[string]string, len(groups))
	seen := make(map[string]bool, len(groups))
	for _, group := range groups {
		id, title := strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		if id == "" || title == "" {
			return fmt.Errorf("project group id and title are required")
		}
		if utf8.RuneCountInString(id) > maxSessionGroupIDRunes || utf8.RuneCountInString(title) > maxSessionGroupRunes {
			return fmt.Errorf("project group id or title is too long")
		}
		if seen[id] {
			return fmt.Errorf("duplicate project group %q", id)
		}
		seen[id] = true
		parents[id] = strings.TrimSpace(group.Parent)
	}
	for id := range parents {
		if err := validateGroupParentChain(id, parents, maxSessionGroupDepth); err != nil {
			return fmt.Errorf("project group %q: %w", id, err)
		}
	}
	return nil
}

func completeTopicOrder(orderedTopicIDs, previous []string) ([]string, error) {
	available := make(map[string]bool, len(previous))
	for _, id := range previous {
		available[id] = true
	}
	seen := make(map[string]bool, len(orderedTopicIDs))
	next := make([]string, 0, len(previous))
	for _, id := range orderedTopicIDs {
		id = strings.TrimSpace(id)
		if id == "" || !available[id] {
			return nil, fmt.Errorf("unknown topic %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate topic %q", id)
		}
		seen[id] = true
		next = append(next, id)
	}
	for _, id := range previous {
		if !seen[id] {
			next = append(next, id)
		}
	}
	return next, nil
}

func normalizeOrganizationTarget(scope, workspaceRoot string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "global" {
		return scope, "", nil
	}
	if scope != "project" {
		return "", "", fmt.Errorf("unsupported scope %q", scope)
	}
	workspaceRoot = normalizeProjectRoot(workspaceRoot)
	if workspaceRoot == "" {
		return "", "", fmt.Errorf("workspaceRoot is required for project scope")
	}
	return scope, workspaceRoot, nil
}

// ReorderTopics persists a manual order without dropping topics omitted by a
// partially loaded client. The manual flag preserves activity sorting for
// existing users until they explicitly drag a topic.
// tl1208: 列表变更体收口到 reorderTopicOrder（保序重排，不改归属成员）。
func (a *App) ReorderTopics(scope, workspaceRoot string, orderedTopicIDs []string) error {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return err
	}
	if len(orderedTopicIDs) == 0 {
		return fmt.Errorf("orderedTopicIDs is required")
	}
	if err := reorderTopicOrder(scope, workspaceRoot, orderedTopicIDs); err != nil {
		return err
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

func (a *App) ListProjectGroups(scope, workspaceRoot string) ([]desktopGroup, error) {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return nil, err
	}
	f := loadProjectsFile()
	if scope == "global" {
		return a.filterKnownGroupMembers(nonNilGroups(f.GlobalGroups)), nil
	}
	i := projectIndexByRoot(f.Projects, workspaceRoot)
	if i < 0 {
		return []desktopGroup{}, nil
	}
	return a.filterKnownGroupMembers(nonNilGroups(f.Projects[i].Groups)), nil
}

// filterKnownGroupMembers drops group member ids that no longer resolve to a
// live catalog topic (#9518). The project tree counts persisted topicIds, so
// stale ids (deleted sessions, tombstoned topics) inflate collapsed-group
// counts. The catalog is authoritative and cross-page; when it is not ready —
// or the membership lookup fails — groups are returned untouched, matching the
// pre-filter behavior instead of hiding valid members.
func (a *App) filterKnownGroupMembers(groups []desktopGroup) []desktopGroup {
	var ids []string
	for _, group := range groups {
		ids = append(ids, group.TopicIDs...)
	}
	if len(ids) == 0 {
		return groups
	}
	catalog := a.sessionCatalog.Load()
	if catalog == nil {
		return groups
	}
	ctx, cancel := a.catalogReadContext()
	defer cancel()
	known, err := catalog.KnownTopicIDs(ctx, ids)
	if err != nil {
		return groups
	}
	changed := false
	out := make([]desktopGroup, len(groups))
	for i, group := range groups {
		if len(group.TopicIDs) == 0 {
			out[i] = group
			continue
		}
		filtered := make([]string, 0, len(group.TopicIDs))
		groupChanged := false
		for _, id := range group.TopicIDs {
			if known[id] {
				filtered = append(filtered, id)
			} else {
				groupChanged = true
			}
		}
		if !groupChanged {
			out[i] = group
			continue
		}
		changed = true
		group.TopicIDs = filtered
		out[i] = group
	}
	if !changed {
		return groups
	}
	return out
}

func nonNilGroups(groups []desktopGroup) []desktopGroup {
	groups = normalizeGroups(groups)
	if groups == nil {
		return []desktopGroup{}
	}
	return groups
}

// GetProjectGroups is the versioned organization read used by current
// frontends. ListProjectGroups remains for old binaries during the transition.
func (a *App) GetProjectGroups(scope, workspaceRoot string) (ProjectGroupsSnapshot, error) {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return ProjectGroupsSnapshot{Groups: []desktopGroup{}}, err
	}
	f := loadProjectsFile()
	if scope == "global" {
		groups := a.fileHeartbeatTopics(a.filterKnownGroupMembers(nonNilGroups(f.GlobalGroups)))
		return ProjectGroupsSnapshot{Groups: groups, Revision: f.GlobalGroupsRevision, Applied: true}, nil
	}
	i := projectIndexByRoot(f.Projects, workspaceRoot)
	if i < 0 {
		return ProjectGroupsSnapshot{Groups: []desktopGroup{}, Applied: true}, nil
	}
	return ProjectGroupsSnapshot{Groups: a.filterKnownGroupMembers(nonNilGroups(f.Projects[i].Groups)), Revision: f.Projects[i].GroupsRevision, Applied: true}, nil
}

// The managed group that automatic filing targets. The id is fixed so the group
// survives restarts and upgrades; the title is only its first-created label and
// the user may rename it without breaking the association.
const (
	heartbeatGroupID    = "group-heartbeat"
	heartbeatGroupTitle = "定时任务"
)

// fileHeartbeatTopics files automation sessions under the managed group without
// touching groups the user maintains (task 17 S2). A session already filed
// anywhere is left alone, so a manual move is never undone, and the group is
// created on demand only when there is something to put in it.
func (a *App) fileHeartbeatTopics(groups []desktopGroup) []desktopGroup {
	if a == nil || a.topicState == nil {
		return groups
	}
	snapshot, err := a.topicState.snapshot("")
	if err != nil || len(snapshot.Records) == 0 {
		return groups
	}
	member := map[string]struct{}{}
	for _, group := range groups {
		for _, id := range group.TopicIDs {
			member[id] = struct{}{}
		}
	}
	var add []string
	for id, record := range snapshot.Records {
		if _, filed := member[id]; filed {
			continue
		}
		if topicOriginIsHeartbeat(record.AutoMeta) {
			add = append(add, id)
		}
	}
	if len(add) == 0 {
		return groups
	}
	sort.Strings(add)
	out := append([]desktopGroup(nil), groups...)
	for i := range out {
		if out[i].ID == heartbeatGroupID {
			out[i].TopicIDs = append(append([]string(nil), out[i].TopicIDs...), add...)
			return out
		}
	}
	return append(out, desktopGroup{ID: heartbeatGroupID, Title: heartbeatGroupTitle, TopicIDs: add})
}

// topicOriginIsHeartbeat reports whether a topic was created by the automation
// scheduler. Titles are user-editable, so grouping keys off this stamp instead.
func topicOriginIsHeartbeat(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var meta struct {
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return false
	}
	return meta.Origin == heartbeatTopicOrigin
}

// AddTopicToGroup files a topic into a collaboration group, creating the group
// when it does not exist yet (task 144). Grouping is organisational only: it
// uses the topic id, so renaming a session never moves it out of its group, and
// addressing still goes through the session's contact_id.
//
// groupID, when given, is authoritative: an existing group is matched by id, not
// by title. Matching on the title alone made two teams with the same name merge,
// and made a group renamed by the user unreachable.
func (a *App) AddTopicToGroup(scope, workspaceRoot, topicID, groupID, groupTitle string) error {
	topicID = strings.TrimSpace(topicID)
	groupID = strings.TrimSpace(groupID)
	groupTitle = strings.TrimSpace(groupTitle)
	if topicID == "" {
		return fmt.Errorf("topicId is required")
	}
	if groupID == "" && groupTitle == "" {
		return nil
	}
	// CAS retry: another writer (sidebar drag, heartbeat filing) may hold the
	// snapshot we just read, and a lost update would silently drop the new
	// session out of its team.
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := a.GetProjectGroups(scope, workspaceRoot)
		if err != nil {
			return err
		}
		groups := append([]desktopGroup(nil), snapshot.Groups...)
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
				// An id with no title is not a usable group label; fall back to
				// the id so the row is still readable in the sidebar.
				groupTitle = groupID
			}
			groups = append(groups, desktopGroup{ID: groupID, Title: groupTitle, TopicIDs: []string{topicID}})
		} else {
			for _, member := range groups[target].TopicIDs {
				if member == topicID {
					return nil
				}
			}
			groups[target].TopicIDs = append(groups[target].TopicIDs, topicID)
		}
		result, err := a.SaveSessionGroupsVersioned(scope, workspaceRoot, snapshot.Revision, groups)
		if err != nil {
			return err
		}
		if result.Applied {
			return nil
		}
	}
	return fmt.Errorf("session group %q: config changed concurrently, retry", firstNonEmptyString(groupID, groupTitle))
}

// MoveTopicToGroup files a topic into a group AND removes it from every other
// group, so the session ends up in exactly one place (task 170).
//
// AddTopicToGroup only appends, and normalizeGroups keeps a topic in the FIRST
// group that lists it, so an append-only "move" leaves the session behind in its
// old group whenever that group is written first - the visible symptom is a
// session that appears in both groups, or that never leaves the old one.
//
// It returns the target group, the titles the session was removed from, and
// whether it was already filed there (in which case nothing is written).
func (a *App) MoveTopicToGroup(scope, workspaceRoot, topicID, groupID, groupTitle string) (desktopGroup, []string, bool, error) {
	topicID = strings.TrimSpace(topicID)
	groupID = strings.TrimSpace(groupID)
	groupTitle = strings.TrimSpace(groupTitle)
	if topicID == "" {
		return desktopGroup{}, nil, false, fmt.Errorf("topicId is required")
	}
	if groupID == "" && groupTitle == "" {
		return desktopGroup{}, nil, false, fmt.Errorf("group or groupId is required")
	}
	// CAS retry, same contract as AddTopicToGroup: the sidebar may hold the
	// snapshot we just read, and a lost update would silently drop the move.
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := a.GetProjectGroups(scope, workspaceRoot)
		if err != nil {
			return desktopGroup{}, nil, false, err
		}
		groups := append([]desktopGroup(nil), snapshot.Groups...)
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
		alreadyFiled := target >= 0 && groupContainsTopic(groups[target], topicID)
		removed := make([]string, 0, 1)
		for i := range groups {
			if i == target || !groupContainsTopic(groups[i], topicID) {
				continue
			}
			kept := make([]string, 0, len(groups[i].TopicIDs))
			for _, member := range groups[i].TopicIDs {
				if member != topicID {
					kept = append(kept, member)
				}
			}
			groups[i].TopicIDs = kept
			removed = append(removed, strings.TrimSpace(groups[i].Title))
		}
		if target < 0 {
			if groupID == "" {
				groupID = "collab-" + strings.ToLower(groupTitle)
			}
			if groupTitle == "" {
				// An id with no title is not a usable label; the sidebar shows
				// the id rather than an empty row.
				groupTitle = groupID
			}
			groups = append(groups, desktopGroup{ID: groupID, Title: groupTitle, TopicIDs: []string{topicID}})
			target = len(groups) - 1
		} else if !alreadyFiled {
			groups[target].TopicIDs = append(append([]string(nil), groups[target].TopicIDs...), topicID)
		}
		moved := groups[target]
		if alreadyFiled && len(removed) == 0 {
			// Nothing to write: the session is already only in the target group.
			return moved, nil, true, nil
		}
		result, err := a.SaveSessionGroupsVersioned(scope, workspaceRoot, snapshot.Revision, groups)
		if err != nil {
			return desktopGroup{}, removed, alreadyFiled, err
		}
		if result.Applied {
			return moved, removed, alreadyFiled, nil
		}
	}
	return desktopGroup{}, nil, false, fmt.Errorf("session group %q: config changed concurrently, retry", firstNonEmptyString(groupID, groupTitle))
}

// groupContainsTopic reports whether a group lists the topic.
func groupContainsTopic(group desktopGroup, topicID string) bool {
	for _, member := range group.TopicIDs {
		if member == topicID {
			return true
		}
	}
	return false
}

func (a *App) SaveSessionGroups(scope, workspaceRoot string, groups []desktopGroup) error {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return err
	}
	if err := validateSessionGroups(groups); err != nil {
		return err
	}
	normalized := normalizeGroups(groups)
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		deleted := make(map[string]bool, len(f.DeletedTopics))
		for _, topicID := range f.DeletedTopics {
			deleted[topicID] = true
		}
		candidate := groupsWithoutDeletedTopics(normalized, deleted)
		if scope == "global" {
			if equalGroups(candidate, f.GlobalGroups) {
				return false, nil
			}
			f.GlobalGroups = candidate
			f.GlobalGroupsRevision++
			return true, nil
		}
		i := projectIndexByRoot(f.Projects, workspaceRoot)
		if i < 0 {
			return false, fmt.Errorf("project %q not found", workspaceRoot)
		}
		if equalGroups(candidate, f.Projects[i].Groups) {
			return false, nil
		}
		f.Projects[i].Groups = candidate
		f.Projects[i].GroupsRevision++
		return true, nil
	}); err != nil {
		return err
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

// SaveSessionGroupsVersioned is a compare-and-swap over one workspace. It
// prevents two windows (and archive cleanup) from overwriting each other's
// full group snapshots. On conflict the current state is returned so the
// frontend can reapply its semantic mutation and retry.
func (a *App) SaveSessionGroupsVersioned(scope, workspaceRoot string, expectedRevision uint64, groups []desktopGroup) (ProjectGroupsSnapshot, error) {
	scope, workspaceRoot, err := normalizeOrganizationTarget(scope, workspaceRoot)
	if err != nil {
		return ProjectGroupsSnapshot{Groups: []desktopGroup{}}, err
	}
	if err := validateSessionGroups(groups); err != nil {
		return ProjectGroupsSnapshot{Groups: []desktopGroup{}}, err
	}
	normalized := normalizeGroups(groups)
	result := ProjectGroupsSnapshot{Groups: []desktopGroup{}}
	changed := false
	err = updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		deleted := make(map[string]bool, len(f.DeletedTopics))
		for _, topicID := range f.DeletedTopics {
			deleted[topicID] = true
		}
		candidate := groupsWithoutDeletedTopics(normalized, deleted)
		currentGroups, currentRevision := f.GlobalGroups, f.GlobalGroupsRevision
		var project *desktopProject
		if scope == "project" {
			i := projectIndexByRoot(f.Projects, workspaceRoot)
			if i < 0 {
				return false, fmt.Errorf("project %q not found", workspaceRoot)
			}
			project = &f.Projects[i]
			currentGroups, currentRevision = project.Groups, project.GroupsRevision
		}
		if currentRevision != expectedRevision {
			result = ProjectGroupsSnapshot{Groups: nonNilGroups(currentGroups), Revision: currentRevision, Applied: false}
			return false, nil
		}
		if equalGroups(candidate, currentGroups) {
			result = ProjectGroupsSnapshot{Groups: nonNilGroups(currentGroups), Revision: currentRevision, Applied: true}
			return false, nil
		}
		currentRevision++
		if project == nil {
			f.GlobalGroups, f.GlobalGroupsRevision = candidate, currentRevision
		} else {
			project.Groups, project.GroupsRevision = candidate, currentRevision
		}
		result = ProjectGroupsSnapshot{Groups: nonNilGroups(candidate), Revision: currentRevision, Applied: true}
		changed = true
		return true, nil
	})
	if err != nil {
		return ProjectGroupsSnapshot{Groups: []desktopGroup{}}, err
	}
	if changed {
		a.emitProjectTreeMetadataChanged()
	}
	return result, nil
}

func equalGroups(left, right []desktopGroup) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Title != right[i].Title || left[i].Parent != right[i].Parent || !sameStringList(left[i].TopicIDs, right[i].TopicIDs) {
			return false
		}
	}
	return true
}

func groupsWithoutTopic(groups []desktopGroup, topicID string) ([]desktopGroup, bool) {
	next := make([]desktopGroup, len(groups))
	changed := false
	for i, group := range groups {
		next[i] = group
		next[i].TopicIDs = removeString(group.TopicIDs, topicID)
		changed = changed || !sameStringList(next[i].TopicIDs, group.TopicIDs)
	}
	return next, changed
}
