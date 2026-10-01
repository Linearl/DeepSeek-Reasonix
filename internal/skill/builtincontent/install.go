package builtincontent

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ShippedPlaybookNames are the playbooks that ship in the binary (task 116)
// and get materialized as editable user-dir copies. The ll-iteration trio
// (task 206-era release prep, 2026-09-21) joins them: intake/plan are local,
// parallel-dev requires the cross-session collaboration switch to be on.
// ll-fork-guide replaces the builtin-only reasonix-fork-guide (task 430);
// ll-update ships with scripts/switch-version.sh (task 430).
var ShippedPlaybookNames = []string{"deep-research", "data-analytics", "memory-search", "collect_issues", "ll-iteration-intake", "ll-iteration-plan", "ll-iteration-parallel-dev", "ll-fork-guide", "ll-update"}

// renamedSkills maps retired user-dir skill names to their shipped replacement
// (task 430). On install, an old-name directory whose replacement is missing is
// removed so the replacement gets materialized on the same pass. When both are
// present the old directory is left alone — the replacement already works and
// the residue is the user's to keep or delete.
var renamedSkills = map[string]string{
	"reasonix-fork-guide":   "ll-fork-guide",
	"iteration-intake":      "ll-iteration-intake",
	"iteration-planning":    "ll-iteration-plan",
	"parallel-worktree-dev": "ll-iteration-parallel-dev",
}

// extraFiles lists skill-relative files (besides SKILL.md) that ship inside a
// skill and must be materialized alongside it. They are written only when the
// skill itself is installed, so an existing user copy is never touched.
var extraFiles = map[string][]string{
	"ll-update": {"scripts/switch-version.sh"},
}

// InstallResult reports one InstallToUserDir pass.
type InstallResult struct {
	Installed []string `json:"installed"`
	Skipped   []string `json:"skipped"`
	Retired   []string `json:"retired,omitempty"`
	Dir       string   `json:"dir"`
}

// InstallToUserDir copies each shipped playbook into destDir/<name>/SKILL.md
// when that file does not already exist. An existing file is left untouched so
// a user's edits survive restarts and re-installs. Missing parent directories
// are created. Returns the names installed and the names skipped. Before the
// copy pass, retired old-name directories (see renamedSkills) whose
// replacement is missing are removed so the replacement installs in their
// place; their names are reported under Retired.
func InstallToUserDir(destDir string) (InstallResult, error) {
	destDir = strings.TrimSpace(destDir)
	if destDir == "" {
		return InstallResult{}, fmt.Errorf("destination skills dir is required")
	}
	items, err := All()
	if err != nil {
		return InstallResult{}, err
	}
	byName := map[string]SkillMarkdown{}
	for _, sk := range items {
		byName[sk.Name] = sk
	}
	result := InstallResult{Dir: destDir}
	if err := migrateRenamedSkills(destDir, &result); err != nil {
		return result, err
	}
	names := append([]string(nil), ShippedPlaybookNames...)
	sort.Strings(names)
	for _, name := range names {
		sk, ok := byName[name]
		if !ok {
			continue
		}
		target := filepath.Join(destDir, name, "SKILL.md")
		if _, err := os.Stat(target); err == nil {
			result.Skipped = append(result.Skipped, name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return result, err
		}
		content := renderSkillFile(sk)
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return result, err
		}
		for _, rel := range extraFiles[name] {
			// embed.FS uses slash-separated paths regardless of host OS.
			raw, err := files.ReadFile(path.Join(name, rel))
			if err != nil {
				return result, fmt.Errorf("embedded extra file %s/%s: %w", name, rel, err)
			}
			extraPath := filepath.Join(destDir, name, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(extraPath), 0o755); err != nil {
				return result, err
			}
			if err := os.WriteFile(extraPath, raw, 0o644); err != nil {
				return result, err
			}
		}
		result.Installed = append(result.Installed, name)
	}
	return result, nil
}

// migrateRenamedSkills removes a retired old-name skill directory when its
// replacement is not installed yet, so the copy pass below materializes the
// replacement (task 430). Old directories left behind when the replacement
// already exists are reported but never deleted.
func migrateRenamedSkills(destDir string, result *InstallResult) error {
	oldNames := make([]string, 0, len(renamedSkills))
	for oldName := range renamedSkills {
		oldNames = append(oldNames, oldName)
	}
	sort.Strings(oldNames)
	for _, oldName := range oldNames {
		newName := renamedSkills[oldName]
		oldDir := filepath.Join(destDir, oldName)
		if _, err := os.Stat(oldDir); err != nil {
			continue // old name absent: nothing to migrate
		}
		newSkill := filepath.Join(destDir, newName, "SKILL.md")
		if _, err := os.Stat(newSkill); err == nil {
			continue // replacement already installed: leave the residue alone
		}
		if err := os.RemoveAll(oldDir); err != nil {
			return fmt.Errorf("retire old skill dir %s: %w", oldDir, err)
		}
		result.Retired = append(result.Retired, oldName)
	}
	return nil
}

// renderSkillFile rebuilds a SKILL.md from an embedded SkillMarkdown so the
// user copy round-trips through the same frontmatter parser.
func renderSkillFile(sk SkillMarkdown) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeFM := func(k, v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteByte('\n')
	}
	// Prefer the original frontmatter keys so aliases (runAs vs runas) survive.
	if len(sk.Frontmatter) > 0 {
		keys := make([]string, 0, len(sk.Frontmatter))
		for k := range sk.Frontmatter {
			if k == "name" || k == "description" || k == "body" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeFM("name", sk.Name)
		writeFM("description", sk.Description)
		for _, k := range keys {
			writeFM(k, sk.Frontmatter[k])
		}
	} else {
		writeFM("name", sk.Name)
		writeFM("description", sk.Description)
		writeFM("runAs", sk.RunAs)
	}
	b.WriteString("---\n\n")
	b.WriteString(sk.Body)
	if !strings.HasSuffix(sk.Body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}
