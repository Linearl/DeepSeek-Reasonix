package organization

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func errUnsupportedScope(scope string) error { return fmt.Errorf("unsupported scope %q", scope) }
func errWorkspaceRootRequired() error {
	return fmt.Errorf("workspaceRoot is required for project scope")
}

// CleanPath mirrors the desktop helper: trimmed, absolute, filepath.Cleaned.
func CleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

// SamePath compares two paths the way the desktop sidebar does: cleaned and
// case-folded on Windows, so equivalent spellings never split lookups.
func SamePath(a, b string) bool {
	a = CleanPath(a)
	b = CleanPath(b)
	if a == "" || b == "" {
		return false
	}
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// NormalizeProjectRoot returns the canonical spelling of a workspace root.
func NormalizeProjectRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}

// SameProjectRoot reports whether two roots resolve to the same workspace.
func SameProjectRoot(a, b string) bool {
	return SamePath(NormalizeProjectRoot(a), NormalizeProjectRoot(b))
}

// NormalizeProjectColor narrows a color to the sidebar palette.
func NormalizeProjectColor(color string) string {
	switch strings.TrimSpace(strings.ToLower(color)) {
	case "red", "orange", "amber", "green", "teal", "blue", "purple", "pink":
		return strings.TrimSpace(strings.ToLower(color))
	default:
		return ""
	}
}

// UniqueStrings trims, drops empties and de-duplicates preserving order.
func UniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// NormalizeGroups trims and de-duplicates a session-group roster: dup ids drop
// (first wins), a self parent is meaningless and clears, topics dedupe across
// the whole roster, and absent data stays an empty slice.
func NormalizeGroups(groups []Group) []Group {
	out := make([]Group, 0, len(groups))
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	for _, group := range groups {
		group.ID = strings.TrimSpace(group.ID)
		group.Title = strings.TrimSpace(group.Title)
		// Task 350: a self parent is meaningless; drop it rather than reject
		// the whole roster so stale editor state cannot wedge a save.
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

// MergeGroups merges two rosters: first side wins for title/parent blanks,
// topic lists concatenate, and the result is re-normalized.
func MergeGroups(left, right []Group) []Group {
	merged := append(append([]Group(nil), left...), right...)
	byID := make(map[string]int, len(merged))
	out := make([]Group, 0, len(merged))
	for _, group := range merged {
		group.ID = strings.TrimSpace(group.ID)
		if group.ID == "" {
			continue
		}
		if index, ok := byID[group.ID]; ok {
			if out[index].Title == "" {
				out[index].Title = strings.TrimSpace(group.Title)
			}
			// Task 350: keep the parent pointer across merges the same way
			// the title is kept — first side wins, the other fills a blank.
			if out[index].Parent == "" {
				out[index].Parent = strings.TrimSpace(group.Parent)
			}
			out[index].TopicIDs = append(out[index].TopicIDs, group.TopicIDs...)
			continue
		}
		byID[group.ID] = len(out)
		out = append(out, group)
	}
	return NormalizeGroups(out)
}

// NormalizeProjectGroups trims and de-duplicates the project-group catalog.
func NormalizeProjectGroups(groups []ProjectGroup) []ProjectGroup {
	out := make([]ProjectGroup, 0, len(groups))
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

// ValidateSessionGroups bounds and validates a session-group roster.
func ValidateSessionGroups(groups []Group) error {
	if len(groups) > MaxSessionGroups {
		return fmt.Errorf("too many session groups: %d", len(groups))
	}
	seenGroups := make(map[string]bool, len(groups))
	seenTopics := make(map[string]bool)
	for _, group := range groups {
		id, title := strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		if id == "" || title == "" {
			return fmt.Errorf("session group id and title are required")
		}
		if utf8.RuneCountInString(id) > MaxSessionGroupIDRunes || utf8.RuneCountInString(title) > MaxSessionGroupRunes {
			return fmt.Errorf("session group id or title is too long")
		}
		if seenGroups[id] {
			return fmt.Errorf("duplicate session group %q", id)
		}
		seenGroups[id] = true
		if len(group.TopicIDs) > MaxSessionGroupTopics {
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
	return ValidateSessionGroupHierarchy(groups)
}

// ValidateSessionGroupHierarchy checks parent pointers: a parent must exist,
// the ancestor chain must not loop, and it must stay within MaxSessionGroupDepth.
func ValidateSessionGroupHierarchy(groups []Group) error {
	parents := make(map[string]string, len(groups))
	for _, group := range groups {
		parents[group.ID] = strings.TrimSpace(group.Parent)
	}
	for _, group := range groups {
		if err := ValidateGroupParentChain(group.ID, parents, MaxSessionGroupDepth); err != nil {
			return fmt.Errorf("session group %q: %w", group.ID, err)
		}
	}
	return nil
}

// ValidateGroupParentChain walks one ancestor chain: every parent must exist,
// the walk may never revisit a node (cycle) nor exceed maxDepth links.
func ValidateGroupParentChain(id string, parents map[string]string, maxDepth int) error {
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

// ValidateProjectGroups bounds and validates the catalog roster, reusing the
// shared ancestor walker exactly like session groups.
func ValidateProjectGroups(groups []ProjectGroup) error {
	if len(groups) > MaxProjectGroups {
		return fmt.Errorf("too many project groups: %d", len(groups))
	}
	parents := make(map[string]string, len(groups))
	seen := make(map[string]bool, len(groups))
	for _, group := range groups {
		id, title := strings.TrimSpace(group.ID), strings.TrimSpace(group.Title)
		if id == "" || title == "" {
			return fmt.Errorf("project group id and title are required")
		}
		if utf8.RuneCountInString(id) > MaxSessionGroupIDRunes || utf8.RuneCountInString(title) > MaxSessionGroupRunes {
			return fmt.Errorf("project group id or title is too long")
		}
		if seen[id] {
			return fmt.Errorf("duplicate project group %q", id)
		}
		seen[id] = true
		parents[id] = strings.TrimSpace(group.Parent)
	}
	for id := range parents {
		if err := ValidateGroupParentChain(id, parents, MaxSessionGroupDepth); err != nil {
			return fmt.Errorf("project group %q: %w", id, err)
		}
	}
	return nil
}

// GroupChildIDs returns the direct children of parentID, in roster order.
func GroupChildIDs(groups []Group, parentID string) []string {
	parentID = strings.TrimSpace(parentID)
	var out []string
	for _, group := range groups {
		if strings.TrimSpace(group.Parent) == parentID && group.ID != parentID {
			out = append(out, group.ID)
		}
	}
	return out
}

// GroupSubtreeIDs returns groupID plus every descendant, parents before
// children. The entry itself is always first.
func GroupSubtreeIDs(groups []Group, groupID string) []string {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil
	}
	out := []string{groupID}
	for i := 0; i < len(out); i++ {
		out = append(out, GroupChildIDs(groups, out[i])...)
	}
	return out
}

// PlanGroupDeletion computes the effect of deleting groupID: with cascade the
// whole subtree goes; without it the direct children are promoted to the
// deleted group's parent (top level when it was a root group) and only the
// group itself is removed. 升顶 default, cascade opt-in — deletion never
// silently discards a child group's organization.
func PlanGroupDeletion(groups []Group, groupID string, cascade bool) (DeletionPlan, error) {
	groupID = strings.TrimSpace(groupID)
	byID := make(map[string]Group, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	if _, ok := byID[groupID]; !ok {
		return DeletionPlan{}, fmt.Errorf("group %q not found", groupID)
	}
	plan := DeletionPlan{Deleted: []string{groupID}}
	if cascade {
		plan.Deleted = GroupSubtreeIDs(groups, groupID)
	} else if children := GroupChildIDs(groups, groupID); len(children) > 0 {
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

// ApplyGroupDeletion removes the planned groups and promotes the direct
// children of each non-cascade deleted group onto its parent ("" = top level).
func ApplyGroupDeletion(groups []Group, groupID string, cascade bool) ([]Group, DeletionPlan, error) {
	groupID = strings.TrimSpace(groupID)
	plan, err := PlanGroupDeletion(groups, groupID, cascade)
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
	out := make([]Group, 0, len(groups))
	for _, group := range groups {
		if deleted[group.ID] {
			continue
		}
		if promoted[group.ID] {
			// Re-home onto the deleted group's own parent.
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

// CompleteTopicOrder validates a client-supplied full order against the
// previous roster, appending any omitted topics in their prior positions.
func CompleteTopicOrder(orderedTopicIDs, previous []string) ([]string, error) {
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

// NonNilGroups normalizes and guarantees a non-nil slice.
func NonNilGroups(groups []Group) []Group {
	groups = NormalizeGroups(groups)
	if groups == nil {
		return []Group{}
	}
	return groups
}

// EqualGroups reports deep roster equality (order matters).
func EqualGroups(left, right []Group) bool {
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

// EqualProjectGroups reports deep catalog equality.
func EqualProjectGroups(left, right []ProjectGroup) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameStringList(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
