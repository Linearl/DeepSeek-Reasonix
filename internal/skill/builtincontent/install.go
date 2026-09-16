package builtincontent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ShippedPlaybookNames are the three MiMo-derived playbooks that ship in the
// binary (task 116). They remain available from the embed even when no user
// copy exists; InstallToUserDir materializes editable copies under
// <reasonix-home>/skills/ so a user can customize them.
var ShippedPlaybookNames = []string{"deep-research", "data-analytics", "memory-search"}

// InstallResult reports one InstallToUserDir pass.
type InstallResult struct {
	Installed []string `json:"installed"`
	Skipped   []string `json:"skipped"`
	Dir       string   `json:"dir"`
}

// InstallToUserDir copies each shipped playbook into destDir/<name>/SKILL.md
// when that file does not already exist. An existing file is left untouched so
// a user's edits survive restarts and re-installs. Missing parent directories
// are created. Returns the names installed and the names skipped.
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
		result.Installed = append(result.Installed, name)
	}
	return result, nil
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
