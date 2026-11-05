// Package agentimport scans locally installed external AI coding tools
// (Claude Code, Codex, OpenCode) for commands, skills, and MCP server
// declarations and converts them into Reasonix's native shapes. Scanning is
// strictly read-only: the external tools' files are never touched. Landing the
// import reuses the existing mechanisms — custom commands live as .md files in
// the Reasonix home commands dir, skills land as directory-layout skills under
// the Reasonix home skills dir, and MCP entries are handed to a caller-supplied
// sink (the desktop wires it to the same persistence the cc-switch import uses).
package agentimport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/config"
	fileencoding "reasonix/internal/fileutil/encoding"
)

// Tool identifies the external tool a scanned item came from.
type Tool string

const (
	ToolClaude   Tool = "claude"
	ToolCodex    Tool = "codex"
	ToolOpenCode Tool = "opencode"
)

// Kind is the import surface of a scanned item.
type Kind string

const (
	KindCommand Kind = "command"
	KindSkill   Kind = "skill"
	KindMCP     Kind = "mcp"
)

// SourceReport is one scanned tool: whether its configuration was detected and
// where each surface lives. An undetected tool still reports so the UI can say
// "not detected" instead of silently hiding it.
type SourceReport struct {
	Tool     Tool            `json:"tool"`
	Detected bool            `json:"detected"`
	Paths    map[Kind]string `json:"paths,omitempty"` // surface → discovered root (display only)
	Note     string          `json:"note,omitempty"`  // why not detected / scan error
	Items    int             `json:"items"`           // items found for this tool
}

// Item is one importable unit with its converted Reasonix shape.
type Item struct {
	ID   string `json:"id"` // stable: "<tool>:<kind>:<name>"
	Tool Tool   `json:"tool"`
	Kind Kind   `json:"kind"`
	// Name is the Reasonix-side name: slash command path (":" namespaces),
	// skill directory name, or MCP server name.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SourcePath  string `json:"sourcePath,omitempty"`
	// Preview is the human-readable import content: full command/skill file
	// content, or the converted MCP JSON for server entries.
	Preview string `json:"preview,omitempty"`
	// Converted is the exact file content the import would write for
	// command items (skill items copy a whole directory instead) — empty for
	// MCP items, which carry Entry below.
	Converted string `json:"converted,omitempty"`
	// Entry is the converted MCP server for KindMCP items; nil otherwise.
	Entry *config.PluginEntry `json:"-"`
	// NativelyActive marks items whose source directory Reasonix already
	// discovers on its own (the ~/.claude, ~/.agents, ~/.agent convention
	// dirs). Importing them duplicates an already-live asset into the
	// Reasonix home dirs; the UI surfaces this so the user can decide.
	NativelyActive bool `json:"nativelyActive"`
	// Recommended is false for items that will most likely need attention:
	// disabled in the source tool or carrying credentials.
	Recommended bool `json:"recommended"`
	// Notes carries short, non-fatal conversion remarks (e.g. "disabled in
	// source", "carries environment credentials").
	Notes []string `json:"notes,omitempty"`
}

// Candidate pairs an item with the conflict state of Reasonix's target
// storage at scan time. Import re-checks conflicts so a stale scan cannot
// clobber a newer file.
type Candidate struct {
	Item     `json:"item"`
	Conflict bool `json:"conflict"` // same name already exists in the target storage
}

// ScanResult is everything the import panel renders.
type ScanResult struct {
	Sources    []SourceReport `json:"sources"`
	Candidates []Candidate    `json:"candidates"`
}

// Scanner reads external tool configurations. Roots are injectable so tests
// can build fixture trees; the zero-value paths fall back to the real user
// home. Target storage dirs (Reasonix side) drive conflict detection.
type Scanner struct {
	// Home is the external tools' base directory (os.UserHomeDir in
	// production). Empty means resolve at scan time.
	Home string
	// ReasonixHome is the import target's home (config.ReasonixHomeDir in
	// production). Empty means the targets below are used verbatim.
	ReasonixHome string
	// CommandsDir / SkillsDir override the Reasonix target directories for
	// tests; when empty they derive from ReasonixHome.
	CommandsDir string
	SkillsDir   string
	// ExistingMCP holds the currently configured MCP server names for
	// conflict detection; the caller loads them from the user config.
	ExistingMCPNames []string
}

func (s *Scanner) home() (string, error) {
	if home := strings.TrimSpace(s.Home); home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}

// CommandsTarget is the Reasonix directory imported commands are written to.
func (s *Scanner) CommandsTarget() string {
	if dir := strings.TrimSpace(s.CommandsDir); dir != "" {
		return dir
	}
	if home := strings.TrimSpace(s.ReasonixHome); home != "" {
		return filepath.Join(home, "commands")
	}
	return filepath.Join(config.ReasonixHomeDir(), "commands")
}

// SkillsTarget is the Reasonix directory imported skills are written to.
func (s *Scanner) SkillsTarget() string {
	if dir := strings.TrimSpace(s.SkillsDir); dir != "" {
		return dir
	}
	if home := strings.TrimSpace(s.ReasonixHome); home != "" {
		return filepath.Join(home, "skills")
	}
	return filepath.Join(config.ReasonixHomeDir(), "skills")
}

func (s *Scanner) existingMCPSet() map[string]bool {
	out := make(map[string]bool, len(s.ExistingMCPNames))
	for _, name := range s.ExistingMCPNames {
		out[name] = true
	}
	return out
}

// conventionHomeDirs are the home-level directories Reasonix already scans
// natively for skills and commands (config.ConventionDirs). Items scanned
// from under them are marked NativelyActive.
func nativelyActiveHomeDirs(home string) []string {
	out := make([]string, 0, len(config.ConventionDirs))
	for _, c := range config.ConventionDirs {
		out = append(out, filepath.Join(home, c))
	}
	return out
}

func underAny(path string, dirs []string) bool {
	for _, dir := range dirs {
		if rel, err := filepath.Rel(dir, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Scan reads every supported external tool. Missing tools are reported, not
// skipped, so the UI can mark them "not detected". Individual surface failures
// degrade to a skipped file instead of failing the whole scan.
func (s *Scanner) Scan() ScanResult {
	result := ScanResult{}
	home, err := s.home()
	if err != nil {
		result.Sources = []SourceReport{
			{Tool: ToolClaude, Note: fmt.Sprintf("resolve home: %v", err)},
			{Tool: ToolCodex, Note: fmt.Sprintf("resolve home: %v", err)},
			{Tool: ToolOpenCode, Note: fmt.Sprintf("resolve home: %v", err)},
		}
		return result
	}
	native := nativelyActiveHomeDirs(home)
	mcpNames := s.existingMCPSet()

	for _, src := range []struct {
		tool Tool
		run  func() ([]Item, map[Kind]string)
	}{
		{ToolClaude, func() ([]Item, map[Kind]string) { return s.scanClaude(home, native) }},
		{ToolCodex, func() ([]Item, map[Kind]string) { return s.scanCodex(home, native) }},
		{ToolOpenCode, func() ([]Item, map[Kind]string) { return s.scanOpenCode(home, native) }},
	} {
		items, paths := src.run()
		report := SourceReport{Tool: src.tool, Paths: paths, Items: len(items), Detected: len(paths) > 0}
		if !report.Detected {
			report.Note = "not detected"
		}
		result.Sources = append(result.Sources, report)
		for _, item := range items {
			result.Candidates = append(result.Candidates, Candidate{
				Item:     item,
				Conflict: s.hasConflict(item, mcpNames),
			})
		}
	}
	sort.SliceStable(result.Candidates, func(i, j int) bool {
		a, b := result.Candidates[i], result.Candidates[j]
		if a.Tool != b.Tool {
			return toolOrder(a.Tool) < toolOrder(b.Tool)
		}
		if a.Kind != b.Kind {
			return kindOrder(a.Kind) < kindOrder(b.Kind)
		}
		return a.Name < b.Name
	})
	return result
}

func toolOrder(t Tool) int {
	switch t {
	case ToolClaude:
		return 0
	case ToolCodex:
		return 1
	case ToolOpenCode:
		return 2
	}
	return 3
}

func kindOrder(k Kind) int {
	switch k {
	case KindCommand:
		return 0
	case KindSkill:
		return 1
	case KindMCP:
		return 2
	}
	return 3
}

func (s *Scanner) hasConflict(item Item, mcpNames map[string]bool) bool {
	switch item.Kind {
	case KindCommand:
		_, err := os.Stat(filepath.Join(s.CommandsTarget(), commandRelPath(item.Name)))
		return err == nil
	case KindSkill:
		dir := filepath.Join(s.SkillsTarget(), item.Name)
		if _, err := os.Stat(dir); err == nil {
			return true
		}
		_, err := os.Stat(filepath.Join(s.SkillsTarget(), item.Name+".md"))
		return err == nil
	case KindMCP:
		if item.Entry == nil {
			return false
		}
		return mcpNames[item.Entry.Name]
	}
	return false
}

// itemID builds the stable candidate id.
func itemID(tool Tool, kind Kind, name string) string {
	return string(tool) + ":" + string(kind) + ":" + name
}

// --- shared directory walkers ---

// walkMarkdown visits every regular *.md file under dir (sorted, symlink-free)
// calling fn with the full path. A missing dir reports os.ErrNotExist so the
// caller can mark the tool undetected.
func walkMarkdown(dir string, fn func(path string) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.Type()&os.ModeSymlink != 0 {
			continue // scan must never follow links out of the tool's tree
		}
		if e.IsDir() {
			if err := walkMarkdown(full, fn); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if e.Type().IsRegular() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			if err := fn(full); err != nil {
				return err
			}
		}
	}
	return nil
}

// commandNameFromPath mirrors internal/command's naming: the .md-suffixed path
// relative to root with separators turned into ":" namespaces.
func commandNameFromPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	return strings.ReplaceAll(strings.TrimSuffix(filepath.ToSlash(rel), ".md"), "/", ":")
}

// commandItem converts one external command file. Content is preserved
// verbatim (frontmatter included — Reasonix's permissive key:value parser
// accepts Claude/OpenCode frontmatter unchanged); only an entirely
// frontmatter-less file gains a minimal description header so the slash menu
// has something to show. $ARGUMENTS/$1../$$ placeholders are Reasonix's
// native substitution syntax already.
func commandItem(tool Tool, root, path string, native bool) (Item, error) {
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return Item{}, err
	}
	content := strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), string(rune(0xFEFF)))
	fm, _ := frontmatterSplit(content)
	name := commandNameFromPath(root, path)
	item := Item{
		ID:             itemID(tool, KindCommand, name),
		Tool:           tool,
		Kind:           KindCommand,
		Name:           name,
		Description:    strings.TrimSpace(fm["description"]),
		SourcePath:     path,
		NativelyActive: native,
		Recommended:    true,
	}
	if item.Description == "" {
		item.Description = name
	}
	item.Preview = content
	item.Converted = strings.TrimRight(content, "\n") + "\n"
	if len(fm) == 0 {
		// Frontmatter-less prompt file (Codex style): add a display-only
		// header. The body bytes stay untouched.
		item.Converted = fmt.Sprintf("---\ndescription: %s\n---\n\n%s", frontmatterEscapeValue(name), strings.TrimRight(content, "\n"))
		if !strings.HasSuffix(item.Converted, "\n") {
			item.Converted += "\n"
		}
	}
	return item, nil
}

// --- Claude Code ---

func (s *Scanner) scanClaude(home string, native []string) ([]Item, map[Kind]string) {
	var items []Item
	paths := map[Kind]string{}
	claudeDir := filepath.Join(home, ".claude")
	commandsDir := filepath.Join(claudeDir, "commands")
	skillsDir := filepath.Join(claudeDir, "skills")

	if dirExists(commandsDir) {
		paths[KindCommand] = commandsDir
		isNative := underAny(commandsDir, native)
		_ = walkMarkdown(commandsDir, func(path string) error {
			item, err := commandItem(ToolClaude, commandsDir, path, isNative)
			if err == nil {
				items = append(items, item)
			}
			return nil // one bad file must not hide the rest
		})
	}
	if dirExists(skillsDir) {
		paths[KindSkill] = skillsDir
		isNative := underAny(skillsDir, native)
		items = append(items, scanSkillDirItems(ToolClaude, skillsDir, isNative)...)
	}
	entryItems, mcpPath := claudeMCPItems(home)
	if mcpPath != "" {
		paths[KindMCP] = mcpPath
		items = append(items, entryItems...)
	}
	return items, paths
}

// --- Codex ---

func (s *Scanner) scanCodex(home string, native []string) ([]Item, map[Kind]string) {
	var items []Item
	paths := map[Kind]string{}
	codexDir := filepath.Join(home, ".codex")
	commandsDir := filepath.Join(codexDir, "prompts")
	skillsDir := filepath.Join(codexDir, "skills")

	if dirExists(commandsDir) {
		paths[KindCommand] = commandsDir
		_ = walkMarkdown(commandsDir, func(path string) error {
			item, err := commandItem(ToolCodex, commandsDir, path, false)
			if err == nil {
				items = append(items, item)
			}
			return nil
		})
	}
	if dirExists(skillsDir) {
		paths[KindSkill] = skillsDir
		items = append(items, scanSkillDirItems(ToolCodex, skillsDir, false)...)
	}
	entryItems, mcpPath := codexMCPItems(codexDir)
	if mcpPath != "" {
		paths[KindMCP] = mcpPath
		items = append(items, entryItems...)
	}
	return items, paths
}

// --- OpenCode ---

// openCodeBaseDirs lists the candidate base directories in probe order: the
// XDG-style path every platform accepts, then the OS app-data locations.
func openCodeBaseDirs(home string) []string {
	dirs := []string{filepath.Join(home, ".config", "opencode")}
	if appdata := strings.TrimSpace(os.Getenv("APPDATA")); appdata != "" && runtimeGOOS() == "windows" {
		dirs = append(dirs, filepath.Join(appdata, "opencode"))
	}
	return dirs
}

func (s *Scanner) scanOpenCode(home string, native []string) ([]Item, map[Kind]string) {
	var items []Item
	paths := map[Kind]string{}
	var base string
	for _, dir := range openCodeBaseDirs(home) {
		if dirExists(dir) {
			base = dir
			break
		}
	}
	if base == "" {
		return items, paths
	}
	commandsDir := filepath.Join(base, "command")
	skillsDir := filepath.Join(base, "skill")

	if dirExists(commandsDir) {
		paths[KindCommand] = commandsDir
		_ = walkMarkdown(commandsDir, func(path string) error {
			item, err := commandItem(ToolOpenCode, commandsDir, path, false)
			if err == nil {
				items = append(items, item)
			}
			return nil
		})
	}
	if dirExists(skillsDir) {
		paths[KindSkill] = skillsDir
		items = append(items, scanSkillDirItems(ToolOpenCode, skillsDir, false)...)
	}
	entryItems, mcpPath := openCodeMCPItems(base)
	if mcpPath != "" {
		paths[KindMCP] = mcpPath
		items = append(items, entryItems...)
	}
	return items, paths
}

func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}
