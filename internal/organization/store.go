package organization

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

var fileMu sync.Mutex

// sidecarMu guards the organization sidecar file (separate from fileMu: the
// desktop store uses two locks here too, and SaveFile is called both under
// fileMu — via UpdateFile — and standalone).
var sidecarMu sync.Mutex

// HomeDir is the store root, injected once at server/app start so the package
// stays independent of config boot order in tests.
var homeDir func() string

// SetHomeDir wires the reasonix home directory resolver. Serve and desktop
// callers both point it at config.ReasonixHomeDir.
func SetHomeDir(resolver func() string) { homeDir = resolver }

func dir() string {
	if homeDir != nil {
		return homeDir()
	}
	return config.ReasonixHomeDir()
}

// ── builtin roots (task 186 semantics, lifted verbatim) ─────────────────────

// globalWorkspaceRoot is <home>/global-workspace.
func globalWorkspaceRoot() string {
	return filepath.Join(dir(), "global-workspace")
}

// BuiltinWorkspaceRoots lists the host's own directories: the global workspace
// root, the session directory, and the per-project session container. They are
// not user projects — never rendered, and evicted on load so a stale entry
// cannot survive a restart. Their sessions stay reachable through the built-in
// Global folder.
func BuiltinWorkspaceRoots() []string {
	roots := make([]string, 0, 3)
	if root := strings.TrimSpace(globalWorkspaceRoot()); root != "" {
		roots = append(roots, root)
	}
	if sd := strings.TrimSpace(config.SessionDir()); sd != "" {
		roots = append(roots, sd)
	}
	if base := strings.TrimSpace(config.MemoryUserDir()); base != "" {
		roots = append(roots, filepath.Join(base, "projects"))
	}
	return roots
}

// IsBuiltinWorkspaceRoot reports whether root is one of the host's own
// directories (path-normalised, case-insensitive on Windows).
func IsBuiltinWorkspaceRoot(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	for _, candidate := range BuiltinWorkspaceRoots() {
		if SamePath(root, candidate) {
			return true
		}
	}
	return false
}

// StripBuiltinProjects removes every project entry that points at a builtin
// root, plus the sidebar-order and pinned entries that named it. Session data
// is deliberately untouched: only the project node disappears.
func StripBuiltinProjects(f File) File {
	if len(f.Projects) > 0 {
		kept := make([]Project, 0, len(f.Projects))
		for _, project := range f.Projects {
			if IsBuiltinWorkspaceRoot(project.Root) {
				continue
			}
			kept = append(kept, project)
		}
		f.Projects = kept
	}
	f.SidebarOrder = stripBuiltinRootStrings(f.SidebarOrder)
	f.PinnedProjects = stripBuiltinRootStrings(f.PinnedProjects)
	return f
}

func stripBuiltinRootStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	kept := make([]string, 0, len(values))
	for _, value := range values {
		// "__global__" and friends are order tokens, not paths; the builtin
		// check only matches real directories, so tokens pass through.
		if IsBuiltinWorkspaceRoot(value) {
			continue
		}
		kept = append(kept, value)
	}
	return kept
}

// ── normalization ────────────────────────────────────────────────────────────

// ProjectIndexByRoot finds a project by normalized root; -1 when absent.
func ProjectIndexByRoot(projects []Project, root string) int {
	root = NormalizeProjectRoot(root)
	if root == "" {
		return -1
	}
	for i, project := range projects {
		if SameProjectRoot(project.Root, root) {
			return i
		}
	}
	return -1
}

func projectRootInList(roots []string, root string) bool {
	root = NormalizeProjectRoot(root)
	if root == "" {
		return false
	}
	for _, candidate := range roots {
		if SameProjectRoot(candidate, root) {
			return true
		}
	}
	return false
}

// NormalizeFile applies the load/save-time normalization the desktop store
// applies: trims, dedupes, merges duplicate project roots (first wins for
// title/color, lists concatenate), and repairs the sidebar order.
func NormalizeFile(f File) File {
	out := File{
		GlobalTitle:            strings.TrimSpace(f.GlobalTitle),
		GlobalColor:            NormalizeProjectColor(f.GlobalColor),
		GlobalTopics:           UniqueStrings(f.GlobalTopics),
		GlobalPinnedTopics:     UniqueStrings(f.GlobalPinnedTopics),
		GlobalManualTopicOrder: f.GlobalManualTopicOrder,
		GlobalGroups:           NormalizeGroups(f.GlobalGroups),
		GlobalGroupsRevision:   f.GlobalGroupsRevision,
		DeletedTopics:          UniqueStrings(f.DeletedTopics),
		ProjectGroups:          NormalizeProjectGroups(f.ProjectGroups),
	}
	for _, p := range f.Projects {
		root := NormalizeProjectRoot(p.Root)
		if root == "" {
			continue
		}
		p.Root = root
		p.Title = strings.TrimSpace(p.Title)
		p.Color = NormalizeProjectColor(p.Color)
		p.Topics = UniqueStrings(p.Topics)
		p.PinnedTopics = UniqueStrings(p.PinnedTopics)
		p.Groups = NormalizeGroups(p.Groups)
		if i := ProjectIndexByRoot(out.Projects, root); i >= 0 {
			if out.Projects[i].Title == "" && p.Title != "" {
				out.Projects[i].Title = p.Title
			}
			if out.Projects[i].Color == "" && p.Color != "" {
				out.Projects[i].Color = p.Color
			}
			out.Projects[i].Topics = UniqueStrings(append(out.Projects[i].Topics, p.Topics...))
			out.Projects[i].PinnedTopics = UniqueStrings(append(out.Projects[i].PinnedTopics, p.PinnedTopics...))
			out.Projects[i].ManualTopicOrder = out.Projects[i].ManualTopicOrder || p.ManualTopicOrder
			out.Projects[i].Groups = MergeGroups(out.Projects[i].Groups, p.Groups)
			out.Projects[i].GroupsRevision = max(out.Projects[i].GroupsRevision, p.GroupsRevision)
			continue
		}
		out.Projects = append(out.Projects, p)
	}
	for _, root := range UniqueStrings(f.PinnedProjects) {
		root = NormalizeProjectRoot(root)
		if i := ProjectIndexByRoot(out.Projects, root); i >= 0 && !projectRootInList(out.PinnedProjects, out.Projects[i].Root) {
			out.PinnedProjects = append(out.PinnedProjects, out.Projects[i].Root)
		}
	}
	out.SidebarOrder = normalizeSidebarOrder(f.SidebarOrder, out.Projects)
	return out
}

func normalizeSidebarOrder(order []string, projects []Project) []string {
	seenGlobal := false
	var seenRoots []string
	out := make([]string, 0, len(order))
	for _, value := range order {
		value = strings.TrimSpace(value)
		if value == GlobalOrderToken {
			if !seenGlobal {
				seenGlobal = true
				out = append(out, value)
			}
			continue
		}
		root := NormalizeProjectRoot(value)
		i := ProjectIndexByRoot(projects, root)
		if i < 0 {
			continue
		}
		root = projects[i].Root
		if projectRootInList(seenRoots, root) {
			continue
		}
		seenRoots = append(seenRoots, root)
		out = append(out, root)
	}
	return out
}

// ── organization sidecar (the durable copy old builds cannot rewrite) ───────

type sidecarEntry struct {
	Root             string   `json:"root,omitempty"`
	ManualTopicOrder bool     `json:"manualTopicOrder,omitempty"`
	TopicOrder       []string `json:"topicOrder,omitempty"`
	Groups           []Group  `json:"groups"`
	GroupsRevision   uint64   `json:"groupsRevision,omitempty"`
}

type sidecarFile struct {
	Version  int            `json:"version"`
	Global   sidecarEntry   `json:"global"`
	Projects []sidecarEntry `json:"projects"`
}

func projectsFileHasOrganization(f File) bool {
	if f.GlobalManualTopicOrder || len(f.GlobalGroups) > 0 || f.GlobalGroupsRevision > 0 {
		return true
	}
	for _, project := range f.Projects {
		if project.ManualTopicOrder || len(project.Groups) > 0 || project.GroupsRevision > 0 {
			return true
		}
	}
	return false
}

func organizationFromProjectsFile(f File) sidecarFile {
	out := sidecarFile{
		Version: organizationVersion,
		Global: sidecarEntry{
			ManualTopicOrder: f.GlobalManualTopicOrder,
			TopicOrder:       append([]string(nil), f.GlobalTopics...),
			Groups:           NormalizeGroups(f.GlobalGroups),
			GroupsRevision:   f.GlobalGroupsRevision,
		},
		Projects: make([]sidecarEntry, 0, len(f.Projects)),
	}
	for _, project := range f.Projects {
		out.Projects = append(out.Projects, sidecarEntry{
			Root:             project.Root,
			ManualTopicOrder: project.ManualTopicOrder,
			TopicOrder:       append([]string(nil), project.Topics...),
			Groups:           NormalizeGroups(project.Groups),
			GroupsRevision:   project.GroupsRevision,
		})
	}
	return out
}

func applyPersistedTopicOrder(current, persisted []string) []string {
	available := make(map[string]bool, len(current))
	for _, topicID := range current {
		available[topicID] = true
	}
	seen := make(map[string]bool, len(current))
	out := make([]string, 0, len(current))
	for _, topicID := range persisted {
		topicID = strings.TrimSpace(topicID)
		if topicID == "" || !available[topicID] || seen[topicID] {
			continue
		}
		seen[topicID] = true
		out = append(out, topicID)
	}
	for _, topicID := range current {
		if !seen[topicID] {
			seen[topicID] = true
			out = append(out, topicID)
		}
	}
	return out
}

func groupsWithoutDeletedTopics(groups []Group, deleted map[string]bool) []Group {
	out := NormalizeGroups(groups)
	for index := range out {
		kept := out[index].TopicIDs[:0]
		for _, topicID := range out[index].TopicIDs {
			if !deleted[topicID] {
				kept = append(kept, topicID)
			}
		}
		out[index].TopicIDs = kept
	}
	return out
}

func applyProjectOrganization(f File, sidecar sidecarFile) File {
	deleted := make(map[string]bool, len(f.DeletedTopics))
	for _, topicID := range f.DeletedTopics {
		deleted[topicID] = true
	}
	f.GlobalManualTopicOrder = sidecar.Global.ManualTopicOrder
	if f.GlobalManualTopicOrder {
		f.GlobalTopics = applyPersistedTopicOrder(f.GlobalTopics, sidecar.Global.TopicOrder)
	}
	f.GlobalGroups = groupsWithoutDeletedTopics(sidecar.Global.Groups, deleted)
	f.GlobalGroupsRevision = sidecar.Global.GroupsRevision
	for _, persisted := range sidecar.Projects {
		index := ProjectIndexByRoot(f.Projects, persisted.Root)
		if index < 0 {
			continue
		}
		project := &f.Projects[index]
		project.ManualTopicOrder = persisted.ManualTopicOrder
		if project.ManualTopicOrder {
			project.Topics = applyPersistedTopicOrder(project.Topics, persisted.TopicOrder)
		}
		project.Groups = groupsWithoutDeletedTopics(persisted.Groups, deleted)
		project.GroupsRevision = persisted.GroupsRevision
	}
	return NormalizeFile(f)
}

func loadSidecar() (sidecarFile, bool) {
	path := filepath.Join(dir(), OrganizationFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return sidecarFile{}, false
	}
	var sidecar sidecarFile
	if json.Unmarshal(b, &sidecar) != nil || sidecar.Version != organizationVersion {
		return sidecarFile{}, false
	}
	if sidecar.Global.Groups == nil {
		sidecar.Global.Groups = []Group{}
	}
	return sidecar, true
}

func saveSidecar(f File) error {
	sidecarMu.Lock()
	defer sidecarMu.Unlock()

	d := dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	path := filepath.Join(d, OrganizationFile)
	if existing, err := os.ReadFile(path); err == nil {
		var header struct {
			Version int `json:"version"`
		}
		if json.Unmarshal(existing, &header) == nil && header.Version > organizationVersion {
			return fmt.Errorf("project organization version %d is newer than supported version %d", header.Version, organizationVersion)
		}
	}
	b, err := json.MarshalIndent(organizationFromProjectsFile(f), "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return fileutil.ReplaceFile(tmp, path)
}

// ── the load/save/update store loop ─────────────────────────────────────────

// LoadFile reads the projects file with the sidecar organization applied and
// builtin roots stripped — the same view the desktop sidebar renders.
func LoadFile() File {
	path := filepath.Join(dir(), ProjectsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}
	}
	var f File
	_ = json.Unmarshal(b, &f)
	f = NormalizeFile(f)
	if sidecar, ok := loadSidecar(); ok {
		return StripBuiltinProjects(applyProjectOrganization(f, sidecar))
	}
	// Upgrade existing inline organization state immediately. The sidecar is
	// what makes a later old-version save non-destructive.
	if projectsFileHasOrganization(f) {
		_ = saveSidecar(f)
	}
	return StripBuiltinProjects(f)
}

// SaveFile normalizes and persists both store files atomically.
func SaveFile(f File) error {
	d := dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	f = NormalizeFile(f)
	if err := saveSidecar(f); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(d, ProjectsFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return fileutil.ReplaceFile(tmp, path)
}

// UpdateFile runs one read-modify-write cycle under the package lock. The
// mutator reports whether anything changed; an unchanged pass writes nothing.
// A mutator may add a builtin root — it is stripped before the write so the
// node cannot come back on the next boot (task 186).
func UpdateFile(mutator func(*File) (bool, error)) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	f := LoadFile()
	changed, err := mutator(&f)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return SaveFile(StripBuiltinProjects(f))
}
