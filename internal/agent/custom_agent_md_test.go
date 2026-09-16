package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCustomAgentsParsesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	md := "---\nname: researcher\ndescription: multi-source research\nmode: subagent\nmodel: deepseek-flash\ntools: web_fetch,grep\n---\nYou research carefully.\n"
	if err := os.WriteFile(filepath.Join(dir, "researcher.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	// No frontmatter: name falls back to file stem.
	if err := os.WriteFile(filepath.Join(dir, "helper.md"), []byte("Just help.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "ignored.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defs, err := LoadCustomAgents(dir)
	if err != nil {
		t.Fatalf("LoadCustomAgents: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("defs = %d, want 2: %+v", len(defs), defs)
	}
	if defs[0].Name != "helper" || defs[0].Mode != "primary" || !strings.Contains(defs[0].Body, "Just help") {
		t.Fatalf("helper = %+v", defs[0])
	}
	if defs[1].Name != "researcher" || defs[1].Mode != "subagent" || defs[1].Model != "deepseek-flash" || defs[1].Tools != "web_fetch,grep" {
		t.Fatalf("researcher = %+v", defs[1])
	}
}

func TestLoadCustomAgentsMissingDir(t *testing.T) {
	defs, err := LoadCustomAgents(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(defs) != 0 {
		t.Fatalf("missing dir should be empty, defs=%v err=%v", defs, err)
	}
}

// A typo in mode must not become a third, unroutable dispatch mode.
func TestLoadCustomAgentsFallsBackToPrimaryMode(t *testing.T) {
	dir := t.TempDir()
	md := "---\nname: typo\nmode: Sub-Agent\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "typo.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	defs, err := LoadCustomAgents(dir)
	if err != nil || len(defs) != 1 {
		t.Fatalf("defs=%v err=%v", defs, err)
	}
	if defs[0].Mode != "primary" {
		t.Fatalf("mode = %q, want primary for an unrecognized value", defs[0].Mode)
	}
}

func TestProfileFromCustomAgentOnlyAcceptsSubagent(t *testing.T) {
	sub := CustomAgentDefinition{Name: "researcher", Mode: "subagent", Body: "Research carefully.", Tools: " grep , read_file ", Model: "deepseek-flash"}
	def, ok := ProfileFromCustomAgent(sub)
	if !ok {
		t.Fatal("subagent definition must convert")
	}
	if def.Name != "researcher" || def.Body != "Research carefully." || def.Model != "deepseek-flash" {
		t.Fatalf("def = %+v", def)
	}
	if len(def.AllowedTools) != 2 || def.AllowedTools[0] != "grep" || def.AllowedTools[1] != "read_file" {
		t.Fatalf("AllowedTools = %v", def.AllowedTools)
	}
	if def.Invocation != "manual" {
		t.Fatalf("Invocation = %q, want manual", def.Invocation)
	}

	if _, ok := ProfileFromCustomAgent(CustomAgentDefinition{Name: "primary", Mode: "primary", Body: "x"}); ok {
		t.Fatal("primary-mode agents must not become spawnable profiles")
	}
	if _, ok := ProfileFromCustomAgent(CustomAgentDefinition{Name: "", Mode: "subagent", Body: "x"}); ok {
		t.Fatal("nameless agents must not convert")
	}
	if _, ok := ProfileFromCustomAgent(CustomAgentDefinition{Name: "n", Mode: "subagent", Body: "  "}); ok {
		t.Fatal("empty body must not convert")
	}
}

func TestCustomAgentProfileLookupSkillsWinOnCollision(t *testing.T) {
	base := func(name string) (ProfileDefinition, bool) {
		if name == "shared" {
			return ProfileDefinition{Name: "shared", Body: "from-skill"}, true
		}
		return ProfileDefinition{}, false
	}
	lookup := CustomAgentProfileLookup(base, []CustomAgentDefinition{
		{Name: "shared", Mode: "subagent", Body: "from-md"},
		{Name: "only-md", Mode: "subagent", Body: "md-only"},
		{Name: "primary-role", Mode: "primary", Body: "not-spawnable"},
	})
	if def, ok := lookup("shared"); !ok || def.Body != "from-skill" {
		t.Fatalf("skill must win on collision: %+v ok=%v", def, ok)
	}
	if def, ok := lookup("only-md"); !ok || def.Body != "md-only" {
		t.Fatalf("custom agent must resolve when no skill claims the name: %+v ok=%v", def, ok)
	}
	if _, ok := lookup("primary-role"); ok {
		t.Fatal("primary-mode custom agents must not appear in profile lookup")
	}
	if _, ok := lookup("missing"); ok {
		t.Fatal("unknown name must miss")
	}
}
