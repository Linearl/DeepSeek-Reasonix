package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRule(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadScopesAndFrontmatterPaths(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	writeRule(t, project, ".reasonix/rules/web.md", "---\npaths: [\"webapp/**/*.ts\"]\n---\nUse the team's web conventions.")
	writeRule(t, project, ".reasonix/rules/always.md", "Prefer small commits.")
	writeRule(t, user, "shared.md", "---\npaths: [\"*.py\"]\n---\nPython house style.")

	loaded, warnings, err := Load(Options{ProjectRoot: project, UserRoot: user})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(loaded) != 3 {
		t.Fatalf("got %d rules, want 3: %+v", len(loaded), loaded)
	}

	// A frontend workspace pulls the scoped web rule; a Go-only one does not.
	web := AssembleSection(t, loaded, []string{"webapp/src/app.ts"})
	if !strings.Contains(web, "web conventions") || !strings.Contains(web, "Prefer small commits") {
		t.Fatalf("frontend workspace must include scoped + unscoped rules:\n%s", web)
	}
	if strings.Contains(web, "Python house style") {
		t.Fatalf("python rule must not apply to a frontend workspace:\n%s", web)
	}
	goOnly := AssembleSection(t, loaded, []string{"internal/agent/agent.go"})
	if strings.Contains(goOnly, "web conventions") {
		t.Fatalf("web rule must not apply to a Go-only workspace:\n%s", goOnly)
	}
}

func TestProjectRuleShadowsUserRuleOfSameName(t *testing.T) {
	project := t.TempDir()
	user := t.TempDir()
	writeRule(t, user, "style.md", "user version")
	writeRule(t, project, ".reasonix/rules/style.md", "project version")

	loaded, _, err := Load(Options{ProjectRoot: project, UserRoot: user})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("shadowing must collapse to one rule, got %d", len(loaded))
	}
	if !strings.Contains(loaded[0].Body, "project version") {
		t.Fatalf("project rule must win: %+v", loaded[0])
	}
}

func TestMissingRootsAreNotAnError(t *testing.T) {
	loaded, warnings, err := Load(Options{ProjectRoot: t.TempDir(), UserRoot: filepath.Join(t.TempDir(), "nope")})
	if err != nil || len(loaded) != 0 {
		t.Fatalf("absent rules must be a clean no-op: %v %+v", err, loaded)
	}
	if len(warnings) != 0 {
		t.Fatalf("absent rules must not warn: %v", warnings)
	}
}

func TestAssembleRespectsByteCap(t *testing.T) {
	big := strings.Repeat("x", MaxSectionBytes)
	loaded := []Rule{{Name: "big.md", Body: big}, {Name: "second.md", Body: "second"}}
	section, used, truncated := Assemble(loaded, nil)
	if !truncated {
		t.Fatal("oversized rule set must report truncation")
	}
	if len(used) != 0 || section != "" {
		t.Fatalf("nothing fits, so nothing is emitted: %q used=%d", section, len(used))
	}
}

func TestMatchGlobDivergencesAreDocumented(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"*.go", "agent.go", true},
		{"*.go", "internal/agent/agent.go", false}, // * stays within one segment
		{"**/*.go", "internal/agent/agent.go", true},
		{"internal/**", "internal/agent/agent.go", true},
		{"webapp/**/*.ts", "webapp/src/app.ts", true},
		{"webapp/**/*.ts", "server/main.go", false},
		{"**", "anything/at/all.go", true},
		{"", "x.go", false},
	}
	for _, tc := range cases {
		if got := MatchGlob(tc.pattern, tc.path); got != tc.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func AssembleSection(t *testing.T, loaded []Rule, files []string) string {
	t.Helper()
	section, _, _ := Assemble(loaded, files)
	return section
}
