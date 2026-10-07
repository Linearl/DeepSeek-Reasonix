package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/skill"
)

type skillRootsCache struct {
	key   string
	at    time.Time
	roots []SkillRootView
}

// CapabilitiesView is the MCP & Skills drawer's data: connected/failed MCP
// servers and the discoverable skills, the GUI counterpart to `/mcp` + `/skill`.
type CapabilitiesView struct {
	Servers    []ServerView    `json:"servers"`
	Skills     []SkillView     `json:"skills"`
	SkillRoots []SkillRootView `json:"skillRoots"`
	Plugins    []PluginView    `json:"plugins"`
}

// SkillsSettingsView is the skills management page's data, split from MCP
// status so opening MCP settings does not scan skill roots.
type SkillsSettingsView struct {
	Skills                  []SkillView     `json:"skills"`
	SkillRoots              []SkillRootView `json:"skillRoots"`
	AllowImplicitInvocation bool            `json:"allowImplicitInvocation"`
}

// ServerView is one MCP server for the drawer. Status is "connected" (with
// tool/prompt/resource counts), "deferred" (enabled but idle), "failed" (with

// SkillView is one discoverable skill for the drawer. Also backs the
// Subagents settings surface: the frontend filters this same list to
// RunAs=="subagent" rather than calling a second, redundant endpoint.
type SkillView struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        string   `json:"scope"`
	SourceDir    string   `json:"sourceDir,omitempty"`
	RunAs        string   `json:"runAs"`
	Enabled      bool     `json:"enabled"`
	Plugin       string   `json:"plugin,omitempty"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	// ReadOnly mirrors frontmatter read-only; omitted/false keeps the legacy
	// writable default for older profiles.
	ReadOnly bool   `json:"readOnly,omitempty"`
	Color    string `json:"color,omitempty"`
	// Invocation is the user-facing slash name; InvocationMode preserves the
	// frontmatter policy used by the subagent profile editor.
	Invocation     string `json:"invocation,omitempty"`
	InvocationMode string `json:"invocationMode,omitempty"`
	// Body is the skill's full markdown body (post-frontmatter) — the
	// subagent profile editor pre-fills its system-prompt field from this.
	Body string `json:"body,omitempty"`
	// ConfiguredModel/ConfiguredEffort are the per-name overrides from
	// cfg.Agent.SubagentModels/SubagentEfforts (internal/boot's
	// subagentModelRef/subagentEffortRef read the same map at dispatch time).
	// This is the only lever for a built-in subagent's model/effort, since
	// built-ins have no editable frontmatter file to carry Model/Effort.
	ConfiguredModel  string `json:"configuredModel,omitempty"`
	ConfiguredEffort string `json:"configuredEffort,omitempty"`
}

type SkillRootSkillView struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        string   `json:"scope"`
	RunAs        string   `json:"runAs"`
	Plugin       string   `json:"plugin,omitempty"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	Color        string   `json:"color,omitempty"`
	Invocation   string   `json:"invocation,omitempty"`
}

// SkillRootView is one skill discovery root for the drawer's Sources section.
type SkillRootView struct {
	Dir        string               `json:"dir"`
	Scope      string               `json:"scope"`
	Priority   int                  `json:"priority"`
	Status     string               `json:"status"`
	Enabled    bool                 `json:"enabled"`
	Configured bool                 `json:"configured"`
	Removable  bool                 `json:"removable"`
	Skills     int                  `json:"skills"`
	SkillItems []SkillRootSkillView `json:"skillItems,omitempty"`
	Warning    string               `json:"warning,omitempty"`
}

// Capabilities projects the session's MCP servers (connected + failed) and skills
// for the MCP & Skills drawer. Non-nil slices so the frontend can map over them.
func (a *App) Capabilities() CapabilitiesView {
	skills := a.SkillsSettings()
	return CapabilitiesView{
		Servers:    a.MCPServers(),
		Skills:     skills.Skills,
		SkillRoots: skills.SkillRoots,
		Plugins:    a.Plugins(),
	}
}

// SkillsSettings returns the skills management snapshot without MCP status.
func (a *App) SkillsSettings() SkillsSettingsView {
	out := SkillsSettingsView{Skills: []SkillView{}, SkillRoots: []SkillRootView{}, AllowImplicitInvocation: true}
	a.mu.RLock()
	tab := a.activeTabLocked()
	var ctrl control.SessionAPI
	workspaceRoot := "."
	if tab != nil {
		ctrl = tab.Ctrl
		if strings.TrimSpace(tab.WorkspaceRoot) != "" {
			workspaceRoot = tab.WorkspaceRoot
		}
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return out
	}

	disabled := map[string]bool{}
	var configuredModels, configuredEfforts map[string]string
	if cfg, err := config.LoadForRootReadOnly(workspaceRoot); err == nil {
		out.AllowImplicitInvocation = cfg.ImplicitSkillInvocationEnabled()
		for _, name := range cfg.Skills.DisabledSkills {
			if key := config.SkillNameKey(name); key != "" {
				disabled[key] = true
			}
		}
		configuredModels = cfg.Agent.SubagentModels
		configuredEfforts = cfg.Agent.SubagentEfforts
	}
	out.SkillRoots = a.cachedSkillRootsView(workspaceRoot)
	for _, s := range ctrl.AllSkills() {
		view := SkillView{
			Name: s.Name, Description: s.Description,
			Scope: string(s.Scope), SourceDir: skillSourceDir(s, out.SkillRoots), RunAs: string(s.RunAs),
			Enabled:          !disabled[config.SkillNameKey(s.Name)],
			Plugin:           s.Plugin,
			Model:            s.Model,
			Effort:           s.Effort,
			AllowedTools:     append([]string{}, s.AllowedTools...),
			ReadOnly:         s.ReadOnly,
			Color:            s.Color,
			Invocation:       "/" + s.SlashName(),
			InvocationMode:   s.Invocation,
			ConfiguredModel:  subagentOverrideFor(configuredModels, s.Name),
			ConfiguredEffort: subagentOverrideFor(configuredEfforts, s.Name),
		}
		// Body feeds only the Subagents editor's prompt prefill. Inline skills
		// fold references/ into Body at load time (hundreds of KB for a rich
		// skill library), and every Capabilities/Settings fetch would ship all
		// of it across the JSON bridge for nothing.
		if s.RunAs == skill.RunSubagent {
			view.Body = s.Body
		}
		out.Skills = append(out.Skills, view)
	}
	return out
}

// SetSkillImplicitInvocation persists whether the model may discover and
// invoke skills automatically, then rebuilds the active runtime. Explicit
// /skill invocation and skill management remain available in either mode.
func (a *App) SetSkillImplicitInvocation(enabled bool) error {
	err := a.applySkillConfigChange("disable_implicit_invocation", "skills policy", func(c *config.Config) error {
		c.SetSkillImplicitInvocation(enabled)
		return nil
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

const skillRootsCacheTTL = 10 * time.Second

func (a *App) cachedSkillRootsView(workspaceRoots ...string) []SkillRootView {
	workspaceRoot := "."
	if len(workspaceRoots) > 0 {
		workspaceRoot = workspaceRoots[0]
	}
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	cfg, _ := config.LoadForRootReadOnly(workspaceRoot)
	userCfg := config.LoadForEdit(config.UserConfigPath())
	key := skillRootsCacheKey(workspaceRoot, cfg, userCfg)

	now := time.Now()
	a.skillRootsMu.Lock()
	if a.skillRootsCache.key == key && now.Sub(a.skillRootsCache.at) < skillRootsCacheTTL {
		roots := cloneSkillRootViews(a.skillRootsCache.roots)
		a.skillRootsMu.Unlock()
		return roots
	}
	a.skillRootsMu.Unlock()

	roots := skillRootsViewFrom(workspaceRoot, cfg, userCfg)

	a.skillRootsMu.Lock()
	a.skillRootsCache = skillRootsCache{
		key:   key,
		at:    now,
		roots: cloneSkillRootViews(roots),
	}
	a.skillRootsMu.Unlock()
	return roots
}

func (a *App) invalidateSkillRootsCache() {
	a.skillRootsMu.Lock()
	a.skillRootsCache = skillRootsCache{}
	a.skillRootsMu.Unlock()
}

func skillRootsView() []SkillRootView {
	cwd, _ := os.Getwd()
	cfg, _ := config.Load()
	userCfg := config.LoadForEdit(config.UserConfigPath())
	return skillRootsViewFrom(cwd, cfg, userCfg)
}

func skillRootsViewFrom(workspaceRoot string, cfg, userCfg *config.Config) []SkillRootView {
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	var custom []string
	var excluded []string
	maxDepth := 3
	if cfg != nil {
		custom = cfg.SkillCustomPaths()
		excluded = cfg.SkillExcludedPaths()
		maxDepth = cfg.SkillMaxDepth()
	}
	var pluginPaths map[string][]string
	var pluginAgentPaths map[string][]string
	if cfg != nil {
		pluginPaths = cfg.PluginPackageSkillOwners()
		pluginAgentPaths = cfg.PluginPackageAgentOwners()
	}
	st := skill.New(skill.Options{ProjectRoot: workspaceRoot, CustomPaths: custom, PluginPaths: pluginPaths, PluginAgentPaths: pluginAgentPaths, ExcludedPaths: excluded, MaxDepth: maxDepth, DisableBuiltins: true, Stderr: io.Discard})
	counts := map[string]int{}
	skillItems := map[string][]SkillRootSkillView{}
	roots := st.Roots()
	for _, sk := range st.SlashList() {
		root := skillDisplayRoot(sk, roots)
		counts[root]++
		skillItems[root] = append(skillItems[root], SkillRootSkillView{
			Name:         sk.Name,
			Description:  sk.Description,
			Scope:        string(sk.Scope),
			RunAs:        string(sk.RunAs),
			Plugin:       sk.Plugin,
			Model:        sk.Model,
			Effort:       sk.Effort,
			AllowedTools: append([]string{}, sk.AllowedTools...),
			Color:        sk.Color,
			Invocation:   "/" + sk.SlashName(),
		})
	}
	for root := range skillItems {
		sort.Slice(skillItems[root], func(i, j int) bool {
			return skillItems[root][i].Invocation < skillItems[root][j].Invocation
		})
	}
	userConfigured := map[string]bool{}
	if userCfg != nil {
		for _, p := range userCfg.Skills.Paths {
			userConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
	}
	effectiveConfigured := map[string]bool{}
	effectiveExcluded := map[string]bool{}
	if cfg != nil {
		for _, p := range cfg.Skills.Paths {
			effectiveConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
		for _, p := range cfg.Skills.ExcludedPaths {
			effectiveExcluded[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
	}
	out := []SkillRootView{}
	seenRoots := map[string]int{}
	for _, r := range roots {
		dir := canonicalSkillPathForRoot(r.Dir, workspaceRoot)
		view := SkillRootView{
			Dir:        r.Dir,
			Scope:      string(r.Scope),
			Priority:   r.Priority + 1,
			Status:     string(r.Status),
			Enabled:    true,
			Configured: r.Scope == skill.ScopeCustom && (userConfigured[dir] || effectiveConfigured[dir]),
			Removable:  true,
			Skills:     counts[dir],
			SkillItems: skillItems[dir],
		}
		if idx, ok := seenRoots[dir]; ok {
			out[idx] = mergeDuplicateSkillRootView(out[idx], view)
			continue
		}
		seenRoots[dir] = len(out)
		out = append(out, view)
	}
	if cfg != nil {
		for _, p := range cfg.Skills.Paths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			dir := canonicalSkillPathForRoot(p, workspaceRoot)
			enabled := !effectiveExcluded[dir]
			status := "inactive"
			warning := "configured in project/user config but not active in this workspace"
			if !enabled {
				status = "disabled"
				warning = ""
			}
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: dir, Scope: string(skill.ScopeCustom), Status: status, Enabled: enabled,
				Configured: true, Removable: true, Warning: warning,
			}, workspaceRoot)
		}
		for _, p := range cfg.Skills.ExcludedPaths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			dir := canonicalSkillPathForRoot(p, workspaceRoot)
			scope := skillRootScopeForPath(p, workspaceRoot)
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: dir, Scope: string(scope), Status: "disabled", Enabled: false,
				Configured: scope == skill.ScopeCustom || effectiveConfigured[dir], Removable: true,
			}, workspaceRoot)
		}
	}
	if userCfg != nil {
		userExcluded := map[string]bool{}
		for _, p := range userCfg.Skills.ExcludedPaths {
			userExcluded[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
		for _, p := range userCfg.Skills.Paths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			enabled := !userExcluded[canonicalSkillPathForRoot(p, workspaceRoot)]
			status := "inactive"
			warning := "configured in user config but not active in this workspace; project [skills].paths may override it"
			if !enabled {
				status = "disabled"
				warning = ""
			}
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir:        canonicalSkillPathForRoot(p, workspaceRoot),
				Scope:      string(skill.ScopeCustom),
				Status:     status,
				Enabled:    enabled,
				Configured: true,
				Removable:  true,
				Warning:    warning,
			}, workspaceRoot)
		}
		for _, p := range userCfg.Skills.ExcludedPaths {
			if rootActive(out, p, workspaceRoot) || userConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] {
				continue
			}
			scope := skillRootScopeForPath(p, workspaceRoot)
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: canonicalSkillPathForRoot(p, workspaceRoot), Scope: string(scope), Status: "disabled", Enabled: false,
				Configured: scope == skill.ScopeCustom, Removable: true,
			}, workspaceRoot)
		}
	}
	return out
}

func appendSkillRootView(out *[]SkillRootView, seen *map[string]int, view SkillRootView, workspaceRoot string) {
	dir := canonicalSkillPathForRoot(view.Dir, workspaceRoot)
	if idx, ok := (*seen)[dir]; ok {
		(*out)[idx] = mergeDuplicateSkillRootView((*out)[idx], view)
		return
	}
	(*seen)[dir] = len(*out)
	*out = append(*out, view)
}

func mergeDuplicateSkillRootView(existing, duplicate SkillRootView) SkillRootView {
	existing.Configured = existing.Configured || duplicate.Configured
	existing.Removable = existing.Removable || duplicate.Removable
	if existing.Status != "ok" && duplicate.Status == "ok" {
		existing.Status = duplicate.Status
		existing.Enabled = duplicate.Enabled
	}
	if existing.Skills == 0 && duplicate.Skills > 0 {
		existing.Skills = duplicate.Skills
		existing.SkillItems = duplicate.SkillItems
	}
	if existing.Warning == "" {
		existing.Warning = duplicate.Warning
	}
	return existing
}

func skillRootsCacheKey(workspaceRoot string, cfg, userCfg *config.Config) string {
	type cacheKey struct {
		CWD       string   `json:"cwd"`
		Custom    []string `json:"custom"`
		Plugins   []string `json:"plugins"`
		Excluded  []string `json:"excluded"`
		MaxDepth  int      `json:"maxDepth"`
		UserPaths []string `json:"userPaths"`
	}
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	key := cacheKey{CWD: canonicalSkillPathForRoot(workspaceRoot, workspaceRoot), MaxDepth: 3}
	if cfg != nil {
		key.Custom = canonicalSkillPathsForRoot(cfg.SkillCustomPaths(), workspaceRoot)
		for path, owners := range cfg.PluginPackageSkillOwners() {
			for _, owner := range owners {
				key.Plugins = append(key.Plugins, canonicalSkillPathForRoot(path, workspaceRoot)+"\x00"+owner)
			}
		}
		sort.Strings(key.Plugins)
		key.Excluded = canonicalSkillPathsForRoot(cfg.SkillExcludedPaths(), workspaceRoot)
		key.MaxDepth = cfg.SkillMaxDepth()
	}
	if userCfg != nil {
		key.UserPaths = canonicalSkillPathsForRoot(userCfg.Skills.Paths, workspaceRoot)
	}
	b, err := json.Marshal(key)
	if err != nil {
		return fmt.Sprintf("%s|%v|%v|%v|%d|%v", key.CWD, key.Custom, key.Plugins, key.Excluded, key.MaxDepth, key.UserPaths)
	}
	return string(b)
}

func canonicalSkillPathsForRoot(paths []string, workspaceRoot string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, canonicalSkillPathForRoot(p, workspaceRoot))
	}
	sort.Strings(out)
	return out
}

func normalizeWorkspaceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(root)
}

// canonicalSkillPathForRoot mirrors skill.Store's path resolution while keeping
// comparisons independent of the desktop process CWD. Config may intentionally
// contain relative paths; those are relative to the active workspace.
func canonicalSkillPathForRoot(path, workspaceRoot string) string {
	path = config.ExpandVars(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(normalizeWorkspaceRoot(workspaceRoot), path)
	}
	return config.CanonicalSkillPath(path)
}

func cloneSkillRootViews(in []SkillRootView) []SkillRootView {
	out := make([]SkillRootView, len(in))
	for i, r := range in {
		out[i] = r
		out[i].SkillItems = append([]SkillRootSkillView(nil), r.SkillItems...)
	}
	return out
}

func rootActive(roots []SkillRootView, path string, workspaceRoots ...string) bool {
	workspaceRoot := "."
	if len(workspaceRoots) > 0 {
		workspaceRoot = workspaceRoots[0]
	}
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	for _, r := range roots {
		if canonicalSkillPathForRoot(r.Dir, workspaceRoot) == want {
			return true
		}
	}
	return false
}

// PickSkillFolder opens a directory picker for adding custom skill roots. It only
// returns a path; AddSkillPath performs normalization and writes config.
func (a *App) PickSkillFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur, _ := os.Getwd()
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose skills folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return normalizeSkillPath(dir), nil
}

// PickPluginFolder opens a directory picker for choosing a local plugin package
// source. It returns the selected directory path; plugin install/plan performs
// manifest validation and decides whether to copy or link the package.
func (a *App) PickPluginFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur := a.activeWorkspaceRoot()
	if strings.TrimSpace(cur) == "" {
		cur, _ = os.Getwd()
	}
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Choose plugin folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return filepath.Clean(dir), nil
}

// AddSkillPath adds a custom skill root to the user config and rebuilds the
// controller so the skills index and slash menu reflect it immediately.
func (a *App) AddSkillPath(path string) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		if isConventionSkillRoot(path, workspaceRoot) {
			return c.RestoreSkillPath(path)
		}
		return c.AddSkillPath(path)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// RemoveSkillPath removes a skill source from the user config and rebuilds. For
// convention roots, it records a pseudo-delete in excluded_paths.
func (a *App) RemoveSkillPath(path string) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		removed, err := c.RemoveSkillPath(path)
		if err != nil || removed {
			return err
		}
		return c.ExcludeSkillPath(path)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// SetSkillPathEnabled persists a reversible source toggle and rebuilds the
// controller so the source is immediately included or excluded from discovery.
func (a *App) SetSkillPathEnabled(path string, enabled bool) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		return c.SetSkillPathEnabled(path, enabled)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// RefreshSkills rebuilds the controller without changing config, reloading skill
// discovery, the system prompt index, and slash completions.
func (a *App) RefreshSkills() error {
	a.invalidateSkillRootsCache()
	if err := a.rebuild(); err != nil {
		// The skill cache is already invalidated; refresh the runtime once the
		// other window releases the session lease.
		if _, ok := a.deferredRebuildWarning("skills", err); ok {
			return nil
		}
		return err
	}
	return nil
}

// ReloadCommands rescans command directories and hot-swaps without restarting
// the controller — no MCP disconnect, no hook rerun.
func (a *App) ReloadCommands() error {
	if a.ctx == nil {
		return nil
	}
	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	if ctrl.Running() {
		return fmt.Errorf("wait for the current turn to finish, then retry")
	}
	return ctrl.ReloadCommands(a.ctx)
}

// SetSkillEnabled persists a skill toggle and rebuilds the controller so the
// prompt index, slash menu, and skill tools reflect it immediately.
func (a *App) SetSkillEnabled(name string, enabled bool) error {
	err := a.applySkillConfigChange("disabled_skills", "skill", func(c *config.Config) error {
		return c.SetSkillEnabled(name, enabled)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

func normalizeSkillPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	info, err := os.Stat(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if info.Mode().IsRegular() {
		if filepath.Base(path) == skill.SkillFile {
			return filepath.Clean(filepath.Dir(filepath.Dir(path)))
		}
		return filepath.Clean(filepath.Dir(path))
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(path, skill.SkillFile)); err == nil {
			return filepath.Clean(filepath.Dir(path))
		}
	}
	return filepath.Clean(path)
}

func isConventionSkillRoot(path, workspaceRoot string) bool {
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	if want == "" {
		return false
	}
	bases := []string{normalizeWorkspaceRoot(workspaceRoot)}
	if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, home)
	}
	for _, base := range bases {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		for _, dir := range config.ConventionDirs {
			if want == canonicalSkillPathForRoot(filepath.Join(base, dir, skill.SkillsDirname), workspaceRoot) {
				return true
			}
		}
	}
	return false
}

func skillRootScopeForPath(path, workspaceRoot string) skill.Scope {
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range config.ConventionDirs {
			if want == canonicalSkillPathForRoot(filepath.Join(home, dir, skill.SkillsDirname), workspaceRoot) {
				return skill.ScopeGlobal
			}
		}
	}
	if isConventionSkillRoot(path, workspaceRoot) {
		return skill.ScopeProject
	}
	return skill.ScopeCustom
}

func skillRootPath(path string) string {
	if filepath.Base(path) == skill.SkillFile {
		return filepath.Dir(path)
	}
	return path
}

func skillDisplayRoot(sk skill.Skill, roots []skill.Root) string {
	cleanPath := filepath.Clean(sk.Path)
	for _, r := range roots {
		if r.Scope != sk.Scope {
			continue
		}
		cleanRoot := filepath.Clean(r.Dir)
		prefix := cleanRoot + string(filepath.Separator)
		if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, prefix) {
			return config.CanonicalSkillPath(r.Dir)
		}
	}
	return config.CanonicalSkillPath(filepath.Dir(skillRootPath(sk.Path)))
}

func skillSourceDir(sk skill.Skill, roots []SkillRootView) string {
	path := strings.TrimSpace(sk.Path)
	if path == "" || strings.HasPrefix(path, "(builtin") {
		return ""
	}
	cleanPath := config.CanonicalSkillPath(path)
	bestDir := ""
	bestLen := -1
	for _, root := range roots {
		if root.Scope != "" && root.Scope != string(sk.Scope) {
			continue
		}
		cleanRoot := config.CanonicalSkillPath(root.Dir)
		if cleanRoot == "" {
			continue
		}
		prefix := cleanRoot + string(filepath.Separator)
		if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, prefix) {
			continue
		}
		if len(cleanRoot) > bestLen {
			bestDir = root.Dir
			bestLen = len(cleanRoot)
		}
	}
	if bestDir != "" {
		return bestDir
	}
	return config.CanonicalSkillPath(filepath.Dir(skillRootPath(path)))
}
