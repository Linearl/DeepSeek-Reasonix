package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/capability"
	"reasonix/internal/skill"
	"reasonix/internal/tool"
)

// Task 660: end-to-end use_capability(action=search) cost on a synthetic store
// sized like the user environment (216 skills, ~36KB SKILL.md each). Pre-fix,
// scoring re-ran the skill store's full disk scan once per catalog skill entry
// (64-78s measured); the skill contract index collapses the pattern to two
// scans (catalog + index). The 60s ceiling is a quadratic-regression tripwire,
// not a latency SLA — loaded CI can stretch the two remaining scans.
func TestUseCapabilitySearchTimingSynthetic(t *testing.T) {
	slog.SetLogLoggerLevel(slog.LevelDebug) // surface the segment timing below
	const skillCount = 216
	root := t.TempDir()
	body := strings.Repeat("x", 36*1024)
	// REASONIX_660_REAL_SKILL_DIR measures the real user skill root instead of
	// the synthetic store (task 660 field reproduction).
	realDir := strings.TrimSpace(os.Getenv("REASONIX_660_REAL_SKILL_DIR"))
	if realDir != "" {
		if info, err := os.Stat(realDir); err != nil || !info.IsDir() {
			t.Fatalf("REASONIX_660_REAL_SKILL_DIR %q is not a directory", realDir)
		}
		root = realDir
	} else {
		for i := 0; i < skillCount; i++ {
			name := "timing" + strings.Repeat("0", 3-len(itoaSkill(i))) + itoaSkill(i)
			dir := filepath.Join(root, name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			content := "---\nname: " + name + "\ndescription: timing skill " + name + " for the 660 search-cost measurement\n---\n\n" + body + "\n"
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}
	// The planted target mirrors the reported 41s query (ll-iteration-plan).
	planDir := filepath.Join(root, "ll-iteration-plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	planContent := "---\nname: ll-iteration-plan\ndescription: iterate the ll iteration plan for the current milestone\n---\n\nplan body\n"
	if err := os.WriteFile(filepath.Join(planDir, "SKILL.md"), []byte(planContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	store := skill.New(skill.Options{
		HomeDir:         root,
		ReasonixHomeDir: filepath.Join(root, "reasonix-home"),
		ProjectRoot:     filepath.Join(root, "project"),
		CustomPaths:     []string{root},
		DisableBuiltins: true,
	})
	reg := tool.NewRegistry()
	reg.Add(skill.NewRunSkillTool(store, nil))
	catalogFn := func() capability.Catalog {
		return capability.BuildCatalog(capability.CatalogOptions{
			Tools:  reg.AllContractEntries(),
			Skills: store.List(),
		})
	}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, catalogFn)

	started := time.Now()
	out, resultCount, err := proxy.searchCapabilities("ll-iteration-plan", 3)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	t.Logf("search 217-skill catalog: %d results in %s", resultCount, elapsed)
	if resultCount < 1 || resultCount > 3 {
		t.Fatalf("result count %d outside limit", resultCount)
	}
	if !strings.Contains(out, "skill:ll-iteration-plan") {
		t.Fatalf("planted skill missing from results:\n%s", out)
	}
	if elapsed > 60*time.Second {
		t.Fatalf("search regressed to the task-660 quadratic pattern: %s for %d skills", elapsed, skillCount)
	}
}

func itoaSkill(v int) string {
	if v == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
