// Package organization is the shared home for the project-tree organization
// store (desktop-projects.json + its sidecar) and the session/project group
// rosters it carries. Task 911 lifted the store out of the desktop package so
// the serve gateway can manage the SAME files the desktop app does: GC's group
// management writes through here, the desktop frontend reads the same sidecar
// on its next refresh, and both sides see one roster.
//
// SCHEMA SYNC CONTRACT: the JSON shapes here must stay byte-compatible with
// the desktop package's types (desktop/project_tree_organization.go and
// desktop/project_tree_organization_store.go). Until the desktop side is
// migrated onto this package (task 911 follow-up), the two implementations
// coexist; any schema change must land in BOTH files in the same commit.
package organization

import "strings"

const (
	// GlobalOrderToken marks the global roster inside SidebarOrder.
	GlobalOrderToken = "__global__"

	// ProjectsFile / OrganizationFile are the two store files, resolved
	// against the reasonix home dir (config.ReasonixHomeDir).
	ProjectsFile        = "desktop-projects.json"
	OrganizationFile    = "desktop-project-tree-organization.json"
	organizationVersion = 1

	// Limits mirror the desktop package's roster bounds.
	MaxSessionGroups       = 100
	MaxSessionGroupIDRunes = 160
	MaxSessionGroupRunes   = 120
	MaxSessionGroupTopics  = 10_000
	// MaxSessionGroupDepth (task 892) caps nesting at a generous 64 levels:
	// arbitrary depth is the point, but an unbounded chain from corrupt data
	// would make every ancestor walk quadratic. Cycles are prevented by the
	// explicit ancestor walk in ValidateGroupHierarchy, not by the depth cap.
	MaxSessionGroupDepth = 64
	MaxProjectGroups     = 100

	// GroupKinds select which grouping system an operation targets.
	KindSession = "session"
	KindProject = "project"
)

// Project is one workspace entry in the projects file.
type Project struct {
	Root             string   `json:"root"`
	Title            string   `json:"title,omitempty"`
	Color            string   `json:"color,omitempty"`
	ProjectGroup     string   `json:"projectGroup,omitempty"`
	Topics           []string `json:"topics"`
	PinnedTopics     []string `json:"pinnedTopics,omitempty"`
	ManualTopicOrder bool     `json:"manualTopicOrder,omitempty"`
	Groups           []Group  `json:"groups,omitempty"`
	GroupsRevision   uint64   `json:"-"`
}

// Group is one session-group roster entity. Parent (task 350) nests this group
// under another group of the same workspace — purely additive and optional.
// HIERARCHY IS DISPLAY ONLY — it must never imply permission or command
// semantics (层级≠指挥权); addressing keeps using the flat group id.
type Group struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	TopicIDs []string `json:"topicIds,omitempty"`
	Parent   string   `json:"parent,omitempty"`
}

// ProjectGroup is one project-group catalog entity (task 892), mirroring
// Group's nesting contract. A project references its group through
// Project.ProjectGroup.
type ProjectGroup struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Parent    string `json:"parent,omitempty"`
	SortOrder int    `json:"sortOrder,omitempty"`
}

// Snapshot is a versioned read of one roster (the CAS token for writes).
type Snapshot struct {
	Groups   []Group `json:"groups"`
	Revision uint64  `json:"revision"`
	Applied  bool    `json:"applied"`
}

// File is the desktop-projects.json document.
type File struct {
	GlobalTitle            string   `json:"globalTitle,omitempty"`
	GlobalColor            string   `json:"globalColor,omitempty"`
	GlobalTopics           []string `json:"globalTopics,omitempty"`
	GlobalPinnedTopics     []string `json:"globalPinnedTopics,omitempty"`
	GlobalManualTopicOrder bool     `json:"globalManualTopicOrder,omitempty"`
	GlobalGroups           []Group  `json:"globalGroups,omitempty"`
	GlobalGroupsRevision   uint64   `json:"-"`
	DeletedTopics          []string `json:"deletedTopics,omitempty"`
	PinnedProjects         []string `json:"pinnedProjects,omitempty"`
	SidebarOrder           []string `json:"sidebarOrder,omitempty"`
	// ProjectGroups (task 892) is the project-group catalog: the entities a
	// project's projectGroup field points at. Flat/absent data reads as an
	// empty list — old saves never carried the key.
	ProjectGroups []ProjectGroup `json:"projectGroups,omitempty"`
	Projects      []Project      `json:"projects"`
}

// DeletionPlan describes what a group deletion will do before it does it —
// the proposal half of the task-892 safety contract (批量删组提案制).
type DeletionPlan struct {
	// Deleted lists the group ids the deletion removes.
	Deleted []string `json:"deleted"`
	// Promoted lists child groups re-parented onto the deleted group's parent
	// (or the top level) — the default, non-destructive semantics.
	Promoted []string `json:"promoted,omitempty"`
	// UnfiledTopics lists the topics that lose their group because the group
	// holding them is deleted. Topics are never deleted, only unfiled.
	UnfiledTopics []string `json:"unfiledTopics,omitempty"`
	// Batch marks a plan at or above the tool layer's blast-radius threshold.
	Batch bool `json:"batch,omitempty"`
}

// NormalizeTarget validates the scope+workspaceRoot pair the way the desktop
// manage_sessions helpers do: "global" clears the root, "project" requires one.
func NormalizeTarget(scope, workspaceRoot string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "global" {
		return scope, "", nil
	}
	if scope != "project" {
		return "", "", errUnsupportedScope(scope)
	}
	workspaceRoot = NormalizeProjectRoot(workspaceRoot)
	if workspaceRoot == "" {
		return "", "", errWorkspaceRootRequired()
	}
	return scope, workspaceRoot, nil
}
