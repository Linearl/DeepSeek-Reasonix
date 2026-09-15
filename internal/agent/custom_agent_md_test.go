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
