package agentimport

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
)

// ConflictPolicy decides what happens when the target storage already holds an
// item with the same name.
type ConflictPolicy string

const (
	// PolicySkip leaves the existing item untouched (default for conflicts).
	PolicySkip ConflictPolicy = "skip"
	// PolicyOverwrite replaces the existing item with the imported one.
	PolicyOverwrite ConflictPolicy = "overwrite"
	// PolicyRename imports under "<name>-from-<tool>".
	PolicyRename ConflictPolicy = "rename"
)

// ImportRequest selects one scanned candidate and its conflict handling.
type ImportRequest struct {
	ID     string         `json:"id"`     // Candidate.Item.ID from the scan
	Policy ConflictPolicy `json:"policy"` // only consulted when the target name is taken
}

// MCPSink persists one MCP server entry into the user config (and may connect
// it). The desktop wires this to the same persistence path the cc-switch
// import uses; tests inject an in-memory sink.
type MCPSink func(entry config.PluginEntry) error

// Importer lands scanned candidates into Reasonix storage.
type Importer struct {
	Scanner Scanner
	// MCPSink is required for KindMCP requests; without it MCP items fail
	// with an explanatory error rather than being silently dropped.
	MCPSink MCPSink
}

// ItemOutcome is the per-item line of the receipt.
type ItemOutcome struct {
	ID     string `json:"id"`
	Tool   Tool   `json:"tool"`
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"` // "imported" | "renamed" | "skipped" | "failed"
	// Detail explains a skip/failure, or carries the new name for renames.
	Detail string `json:"detail,omitempty"`
}

// Receipt is the summary the UI shows after an import run.
type Receipt struct {
	Imported int           `json:"imported"`
	Skipped  int           `json:"skipped"`
	Failed   int           `json:"failed"`
	Items    []ItemOutcome `json:"items"`
}

// Import lands the requested candidates. The scanner state is re-read for
// conflicts at import time (targets may have changed since the panel scan),
// so an explicit Policy is honoured only when a conflict still exists.
func (im *Importer) Import(requests []ImportRequest) (Receipt, error) {
	receipt := Receipt{}
	if len(requests) == 0 {
		return receipt, nil
	}
	scan := im.Scanner.Scan()
	byID := make(map[string]Candidate, len(scan.Candidates))
	for _, c := range scan.Candidates {
		byID[c.ID] = c
	}
	for _, req := range requests {
		cand, ok := byID[req.ID]
		if !ok {
			receipt.Failed++
			receipt.Items = append(receipt.Items, ItemOutcome{
				ID: req.ID, Status: "failed",
				Detail: "no longer detected; rescan and retry",
			})
			continue
		}
		outcome := im.importOne(cand, req.Policy)
		switch outcome.Status {
		case "imported", "renamed":
			receipt.Imported++
		case "skipped":
			receipt.Skipped++
		default:
			receipt.Failed++
		}
		receipt.Items = append(receipt.Items, outcome)
	}
	sort.SliceStable(receipt.Items, func(i, j int) bool {
		return receipt.Items[i].ID < receipt.Items[j].ID
	})
	return receipt, nil
}

func (im *Importer) importOne(cand Candidate, policy ConflictPolicy) ItemOutcome {
	item := cand.Item
	outcome := ItemOutcome{ID: item.ID, Tool: item.Tool, Kind: item.Kind, Name: item.Name}
	switch item.Kind {
	case KindCommand:
		return im.importCommand(item, cand.Conflict, policy)
	case KindSkill:
		return im.importSkill(item, cand.Conflict, policy)
	case KindMCP:
		return im.importMCP(item, cand.Conflict, policy)
	}
	outcome.Status = "failed"
	outcome.Detail = fmt.Sprintf("unknown import kind %q", item.Kind)
	return outcome
}

// importCommand writes the converted command file into the Reasonix commands
// dir, preserving ":" namespaces as subdirectories.
func (im *Importer) importCommand(item Item, conflict bool, policy ConflictPolicy) ItemOutcome {
	outcome := ItemOutcome{ID: item.ID, Tool: item.Tool, Kind: item.Kind, Name: item.Name}
	targetDir := im.Scanner.CommandsTarget()
	target := filepath.Join(targetDir, commandRelPath(item.Name))
	name := item.Name
	if conflict {
		switch normalizePolicy(policy) {
		case PolicySkip:
			outcome.Status = "skipped"
			outcome.Detail = "same name already exists"
			return outcome
		case PolicyRename:
			name = renamedCommand(item.Name, string(item.Tool))
			target = filepath.Join(targetDir, commandRelPath(name))
			outcome.Name = name
		case PolicyOverwrite:
			// fall through to the write below
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		outcome.Status = "failed"
		outcome.Detail = err.Error()
		return outcome
	}
	if err := fileutil.AtomicWriteFile(target, []byte(item.Converted), 0o644); err != nil {
		outcome.Status = "failed"
		outcome.Detail = err.Error()
		return outcome
	}
	outcome.Status = "imported"
	if name != item.Name {
		outcome.Status = "renamed"
		outcome.Detail = name
	}
	return outcome
}

// importSkill copies the source skill (SKILL.md plus siblings like
// references/) into the Reasonix skills dir as a directory-layout skill.
// Flat source skills are normalised to the canonical layout.
func (im *Importer) importSkill(item Item, conflict bool, policy ConflictPolicy) ItemOutcome {
	outcome := ItemOutcome{ID: item.ID, Tool: item.Tool, Kind: item.Kind, Name: item.Name}
	targetRoot := im.Scanner.SkillsTarget()
	name := sanitizeSkillName(item.Name)
	if name == "" {
		outcome.Status = "failed"
		outcome.Detail = "name cannot be converted to a Reasonix skill name"
		return outcome
	}
	renamed := name != item.Name
	if renamed {
		outcome.Name = name
	}
	source := skillSourceRoot(item)
	target := filepath.Join(targetRoot, name)
	if conflict {
		switch normalizePolicy(policy) {
		case PolicySkip:
			outcome.Status = "skipped"
			outcome.Detail = "same name already exists"
			return outcome
		case PolicyRename:
			name = sanitizeSkillName(renamedCommand(item.Name, string(item.Tool)))
			if name == "" || name == "." {
				outcome.Status = "failed"
				outcome.Detail = "renamed skill name is empty"
				return outcome
			}
			renamed = true
			target = filepath.Join(targetRoot, name)
			outcome.Name = name
		case PolicyOverwrite:
			if err := removeExistingSkill(targetRoot, name); err != nil {
				outcome.Status = "failed"
				outcome.Detail = err.Error()
				return outcome
			}
		}
	} else if renamed {
		// A sanitized name can still collide with an existing skill even
		// though the original name did not.
		if _, err := os.Stat(target); err == nil {
			outcome.Status = "skipped"
			outcome.Detail = "sanitized name already exists"
			return outcome
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		outcome.Status = "failed"
		outcome.Detail = err.Error()
		return outcome
	}
	if source == "" {
		// Flat source skill: write the scanned content as SKILL.md under the
		// canonical directory layout.
		if err := fileutil.AtomicWriteFile(filepath.Join(target, "SKILL.md"), []byte(item.Preview), 0o644); err != nil {
			outcome.Status = "failed"
			outcome.Detail = err.Error()
			return outcome
		}
	} else if err := copyDirTree(source, target); err != nil {
		outcome.Status = "failed"
		outcome.Detail = err.Error()
		return outcome
	}
	outcome.Status = "imported"
	if renamed {
		outcome.Status = "renamed"
		outcome.Detail = outcome.Name
	}
	return outcome
}

// importMCP hands the converted entry to the injected sink.
func (im *Importer) importMCP(item Item, conflict bool, policy ConflictPolicy) ItemOutcome {
	outcome := ItemOutcome{ID: item.ID, Tool: item.Tool, Kind: item.Kind, Name: item.Name}
	if item.Entry == nil {
		outcome.Status = "failed"
		outcome.Detail = "item carries no MCP server definition"
		return outcome
	}
	if im.MCPSink == nil {
		outcome.Status = "failed"
		outcome.Detail = "MCP import sink unavailable in this context"
		return outcome
	}
	entry := *item.Entry
	if conflict {
		switch normalizePolicy(policy) {
		case PolicySkip:
			outcome.Status = "skipped"
			outcome.Detail = "same name already exists"
			return outcome
		case PolicyRename:
			entry.Name = renamedCommand(entry.Name, string(item.Tool))
			outcome.Name = entry.Name
		case PolicyOverwrite:
			// Upsert is the sink's existing contract (cc-switch import).
		}
	}
	if err := im.MCPSink(entry); err != nil {
		outcome.Status = "failed"
		outcome.Detail = err.Error()
		return outcome
	}
	outcome.Status = "imported"
	if entry.Name != item.Name {
		outcome.Status = "renamed"
		outcome.Detail = entry.Name
	}
	return outcome
}

func normalizePolicy(policy ConflictPolicy) ConflictPolicy {
	switch policy {
	case PolicyOverwrite, PolicyRename:
		return policy
	default:
		return PolicySkip
	}
}

// renamedCommand derives the conflict-free name "<name>-from-<tool>".
func renamedCommand(name, tool string) string {
	return name + "-from-" + tool
}

// sanitizeSkillName maps an external skill name onto Reasonix's skill name
// charset (letters, digits, '_', '-', '.'). Empty results mean unfixable.
func sanitizeSkillName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 64 {
		out = strings.TrimRight(out[:64], "-.")
	}
	return out
}

// skillSourceRoot resolves the directory a skill item copies from: the skill
// directory for directory-layout skills, or "" for flat skills (their content
// is written from the scanned preview instead).
func skillSourceRoot(item Item) string {
	if filepath.Base(item.SourcePath) == "SKILL.md" {
		return filepath.Dir(item.SourcePath)
	}
	return ""
}

// copyDirTree recursively copies regular files from source to target,
// preserving relative paths and skipping symlinked entries entirely.
func copyDirTree(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		from := filepath.Join(source, e.Name())
		to := filepath.Join(target, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(to, 0o755); err != nil {
				return err
			}
			if err := copyDirTree(from, to); err != nil {
				return err
			}
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		if err := copyFile(from, to); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// removeExistingSkill deletes a target-root skill in either layout (flat
// <name>.md or directory <name>/) before an overwrite. Mirrors
// skill.Store.Delete's layout handling without touching skills it didn't scan.
func removeExistingSkill(targetRoot, name string) error {
	dir := filepath.Join(targetRoot, name)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return os.RemoveAll(dir)
	}
	flat := filepath.Join(targetRoot, name+".md")
	if _, err := os.Stat(flat); err == nil {
		return os.Remove(flat)
	}
	return nil // nothing to remove; overwrite proceeds as a fresh create
}

// LoadExistingMCPNames reads the user config's plugin names for conflict
// detection. Errors degrade to an empty list: a failed read must not block
// scanning, it only loses conflict markers.
func LoadExistingMCPNames() []string {
	path := config.UserConfigPath()
	if strings.TrimSpace(path) == "" {
		return nil
	}
	cfg := config.LoadForEdit(path)
	names := make([]string, 0, len(cfg.Plugins))
	for _, p := range cfg.Plugins {
		names = append(names, p.Name)
	}
	return names
}
