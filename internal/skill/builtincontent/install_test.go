package builtincontent_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"reasonix/internal/skill/builtincontent"
)

func TestInstallToUserDirWritesShippedPlaybooks(t *testing.T) {
	dir := t.TempDir()
	res, err := builtincontent.InstallToUserDir(dir)
	if err != nil {
		t.Fatalf("InstallToUserDir: %v", err)
	}
	if len(res.Installed) != 9 {
		t.Fatalf("installed = %v, want nine playbooks", res.Installed)
	}
	for _, name := range []string{"deep-research", "data-analytics", "memory-search", "collect_issues", "ll-iteration-intake", "ll-iteration-plan", "ll-iteration-parallel-dev", "ll-fork-guide", "ll-update"} {
		path := filepath.Join(dir, name, "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		if !strings.Contains(text, "name: "+name) && !strings.Contains(text, "name: \""+name+"\"") {
			// name may be absent if the embed fell back to the directory name;
			// the file must at least exist with a frontmatter block and body.
			if !strings.HasPrefix(text, "---") {
				t.Fatalf("%s missing frontmatter:\n%s", path, text[:min(80, len(text))])
			}
		}
		if !strings.Contains(text, "description:") {
			t.Fatalf("%s missing description", path)
		}
	}

	// Second pass skips existing files (user edits survive).
	if err := os.WriteFile(filepath.Join(dir, "deep-research", "SKILL.md"), []byte("---\nname: deep-research\ndescription: edited\n---\n\nCustomized.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2, err := builtincontent.InstallToUserDir(dir)
	if err != nil {
		t.Fatalf("second InstallToUserDir: %v", err)
	}
	if len(res2.Installed) != 0 {
		t.Fatalf("second pass should install nothing, got %v", res2.Installed)
	}
	if len(res2.Skipped) != 9 {
		t.Fatalf("second pass skipped = %v, want all nine", res2.Skipped)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "deep-research", "SKILL.md"))
	if !strings.Contains(string(raw), "Customized.") {
		t.Fatal("user edit was overwritten")
	}
}

func TestInstallToUserDirRequiresDest(t *testing.T) {
	if _, err := builtincontent.InstallToUserDir("  "); err == nil {
		t.Fatal("empty dest must error")
	}
}

// Task 430: ll-update ships its scripts/ subdirectory alongside SKILL.md.
func TestInstallReleasesSkillScripts(t *testing.T) {
	dir := t.TempDir()
	res, err := builtincontent.InstallToUserDir(dir)
	if err != nil {
		t.Fatalf("InstallToUserDir: %v", err)
	}
	script := filepath.Join(dir, "ll-update", "scripts", "switch-version.sh")
	if !slices.Contains(res.Installed, "ll-update") {
		t.Fatalf("installed = %v, ll-update missing", res.Installed)
	}
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("released script missing: %v", err)
	}
	if !strings.Contains(string(raw), "switch-version.sh") {
		t.Fatal("released script has unexpected content")
	}
	// A second pass must not touch the released copy either.
	if _, err := builtincontent.InstallToUserDir(dir); err != nil {
		t.Fatalf("second InstallToUserDir: %v", err)
	}
	raw2, err := os.ReadFile(script)
	if err != nil || string(raw) != string(raw2) {
		t.Fatal("second pass changed the released script")
	}
}

// Task 430: a user dir carrying a retired old-name skill is migrated on
// install — the old directory is removed and the shipped replacement lands in
// its place.
func TestInstallMigratesRenamedSkills(t *testing.T) {
	dir := t.TempDir()
	for _, old := range []string{"reasonix-fork-guide", "iteration-intake", "iteration-planning", "parallel-worktree-dev"} {
		oldDir := filepath.Join(dir, old)
		if err := os.MkdirAll(oldDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + old + "\ndescription: retired\n---\n\nOld copy.\n"
		if err := os.WriteFile(filepath.Join(oldDir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An unrelated user skill must survive the migration untouched.
	keepDir := filepath.Join(dir, "beautiful-article")
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keepDir, "SKILL.md"), []byte("---\nname: beautiful-article\ndescription: mine\n---\n\nKeep.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := builtincontent.InstallToUserDir(dir)
	if err != nil {
		t.Fatalf("InstallToUserDir: %v", err)
	}
	for _, old := range []string{"reasonix-fork-guide", "iteration-intake", "iteration-planning", "parallel-worktree-dev"} {
		if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
			t.Fatalf("old skill dir %s still present", old)
		}
	}
	for _, want := range []string{"ll-fork-guide", "ll-iteration-intake", "ll-iteration-plan", "ll-iteration-parallel-dev"} {
		if _, err := os.Stat(filepath.Join(dir, want, "SKILL.md")); err != nil {
			t.Fatalf("replacement %s not installed: %v", want, err)
		}
	}
	for _, old := range []string{"reasonix-fork-guide", "iteration-intake", "iteration-planning", "parallel-worktree-dev"} {
		if !slices.Contains(res.Retired, old) {
			t.Fatalf("retired = %v, missing %q", res.Retired, old)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(keepDir, "SKILL.md")); err != nil || !strings.Contains(string(raw), "Keep.") {
		t.Fatal("unrelated user skill was touched by migration")
	}
}

// Task 430: when the replacement is already installed, migration must be
// conservative — the old directory stays and nothing is reinstalled.
func TestInstallKeepsOldDirWhenReplacementExists(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, "ll-fork-guide")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	edited := "---\nname: ll-fork-guide\ndescription: edited\n---\n\nCustomized.\n"
	if err := os.WriteFile(filepath.Join(newDir, "SKILL.md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(dir, "reasonix-fork-guide")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "SKILL.md"), []byte("---\nname: reasonix-fork-guide\ndescription: retired\n---\n\nOld.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := builtincontent.InstallToUserDir(dir)
	if err != nil {
		t.Fatalf("InstallToUserDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldDir, "SKILL.md")); err != nil {
		t.Fatal("old dir was removed although the replacement already existed")
	}
	if len(res.Retired) != 0 {
		t.Fatalf("retired = %v, want empty", res.Retired)
	}
	raw, _ := os.ReadFile(filepath.Join(newDir, "SKILL.md"))
	if !strings.Contains(string(raw), "Customized.") {
		t.Fatal("user-edited replacement was overwritten")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
