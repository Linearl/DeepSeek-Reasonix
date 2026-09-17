package builtincontent_test

import (
	"os"
	"path/filepath"
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
	if len(res.Installed) != 5 {
		t.Fatalf("installed = %v, want five playbooks", res.Installed)
	}
	for _, name := range []string{"deep-research", "data-analytics", "memory-search", "feedback_analysis", "collect_issues"} {
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
	if len(res2.Skipped) != 5 {
		t.Fatalf("second pass skipped = %v, want all five", res2.Skipped)
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
