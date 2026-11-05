package agentimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildHome creates a fake user home with external tool fixtures shaped like
// the real on-disk conventions (verified against the local installs).
func buildHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Claude Code: commands (flat + namespaced), skills (dir + references), MCP in ~/.claude.json.
	write(filepath.Join(".claude", "commands", "review.md"), "---\ndescription: Review code\nargument-hint: <file>\n---\n\nReview $ARGUMENTS with care. Cost: $$5.\n")
	write(filepath.Join(".claude", "commands", "git", "commit.md"), "---\ndescription: Commit staged changes\n---\n\n$ARGUMENTS\n")
	write(filepath.Join(".claude", "skills", "pdf", "SKILL.md"), "---\nname: pdf\ndescription: Handle PDF files\n---\n\nRead the PDF with the pdf tool.\n")
	write(filepath.Join(".claude", "skills", "pdf", "references", "format.md"), "# Format details\n")
	write(filepath.Join(".claude.json"), `{"numStartups":3,"mcpServers":{"exa":{"type":"stdio","command":"cmd","args":["/c","npx","-y","exa-mcp-server"],"env":{"EXA_API_KEY":"secret"}},"docs":{"type":"http","url":"https://docs.example/mcp","headers":{"X-Key":"k"}}}}`)

	// Codex: frontmatter-less prompts, skills, TOML mcp_servers with env subtable.
	write(filepath.Join(".codex", "prompts", "ship.md"), "Ship it: $ARGUMENTS\n")
	write(filepath.Join(".codex", "skills", "buffett", "SKILL.md"), "---\nname: buffett\ndescription: Invest like Buffett\n---\n\nValue invest.\n")
	write(filepath.Join(".codex", "config.toml"), `[mcp_servers.codegraph]
command = "codegraph"
args = ["serve", "--mcp"]

[mcp_servers.node_repl]
command = 'C:\bin\node_repl.exe'
startup_timeout_sec = 120

[mcp_servers.node_repl.env]
NODE_REPL_NODE_PATH = 'C:\bin\node.exe'

[mcp_servers.disabled_one]
command = "gone"
enabled = false
`)

	// OpenCode: namespaced command, skill, opencode.json local+remote+disabled MCP.
	write(filepath.Join(".config", "opencode", "command", "deploy.md"), "---\ndescription: Deploy the app\nagent: build\n---\n\nDeploy $1 to $2.\n")
	write(filepath.Join(".config", "opencode", "skill", "writer", "SKILL.md"), "---\nname: writer\ndescription: Write prose\n---\n\nWrite well.\n")
	write(filepath.Join(".config", "opencode", "opencode.json"), `{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "local-tool": {"type": "local", "command": ["npx", "-y", "local-tool"]},
    "remote-tool": {"type": "remote", "url": "https://mcp.example/sse", "headers": {"Authorization": "Bearer t"}},
    "off-tool": {"type": "local", "command": ["gone"], "enabled": false}
  }
}`)
	return home
}

func scanFor(t *testing.T, home, commandsDir, skillsDir string, mcpNames ...string) ScanResult {
	t.Helper()
	s := &Scanner{Home: home, CommandsDir: commandsDir, SkillsDir: skillsDir, ExistingMCPNames: mcpNames}
	return s.Scan()
}

func findCandidate(t *testing.T, result ScanResult, id string) Candidate {
	t.Helper()
	for _, c := range result.Candidates {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("candidate %q not found; have %d candidates", id, len(result.Candidates))
	return Candidate{}
}

func TestScanDetectsAllThreeTools(t *testing.T) {
	home := buildHome(t)
	result := scanFor(t, home, filepath.Join(t.TempDir(), "commands"), filepath.Join(t.TempDir(), "skills"))
	if len(result.Sources) != 3 {
		t.Fatalf("expected 3 source reports, got %d", len(result.Sources))
	}
	for _, src := range result.Sources {
		if !src.Detected && src.Note != "not detected" {
			t.Fatalf("source %s: expected detected (reports carry Detected=false only for undetected)", src.Tool)
		}
		if src.Items == 0 {
			t.Fatalf("source %s: expected items, got 0", src.Tool)
		}
		if len(src.Paths) < 3 {
			t.Fatalf("source %s: expected all three surfaces discovered, got %v", src.Tool, src.Paths)
		}
	}
	// 3 claude (2 commands + 1 skill) + 3 MCP claude? no: claude = 2 commands + 1 skill + 2 mcp = 5
	// codex = 1 prompt + 1 skill + 3 mcp = 5; opencode = 1 command + 1 skill + 3 mcp = 5 → 15
	if len(result.Candidates) != 15 {
		t.Fatalf("expected 15 candidates, got %d", len(result.Candidates))
	}
}

func TestScanClaudeCommandConversion(t *testing.T) {
	home := buildHome(t)
	result := scanFor(t, home, filepath.Join(t.TempDir(), "commands"), filepath.Join(t.TempDir(), "skills"))

	c := findCandidate(t, result, "claude:command:review")
	if c.Description != "Review code" {
		t.Fatalf("description = %q", c.Description)
	}
	if !c.NativelyActive {
		t.Fatal("claude items under ~/.claude must be marked natively active")
	}
	// Format fidelity: frontmatter preserved verbatim, $ARGUMENTS/$$ untouched.
	if !strings.Contains(c.Converted, "argument-hint: <file>") {
		t.Fatalf("argument-hint lost:\n%s", c.Converted)
	}
	if !strings.Contains(c.Converted, "Review $ARGUMENTS with care. Cost: $$5.") {
		t.Fatalf("substitution placeholders not preserved:\n%s", c.Converted)
	}
	// Namespaced command name follows Reasonix convention.
	if ns := findCandidate(t, result, "claude:command:git:commit"); ns.Name != "git:commit" {
		t.Fatalf("namespaced command name = %q", ns.Name)
	}
}

func TestScanCodexFrontmatterLessPromptGainsHeader(t *testing.T) {
	home := buildHome(t)
	result := scanFor(t, home, filepath.Join(t.TempDir(), "commands"), filepath.Join(t.TempDir(), "skills"))
	c := findCandidate(t, result, "codex:command:ship")
	if c.NativelyActive {
		t.Fatal("codex items must not be marked natively active")
	}
	if !strings.HasPrefix(c.Converted, "---\ndescription: ship\n---\n") {
		t.Fatalf("frontmatter-less prompt missing display header:\n%s", c.Converted)
	}
	if !strings.Contains(c.Converted, "Ship it: $ARGUMENTS") {
		t.Fatalf("body not preserved:\n%s", c.Converted)
	}
}

func TestScanClaudeMCPConversion(t *testing.T) {
	home := buildHome(t)
	result := scanFor(t, home, filepath.Join(t.TempDir(), "commands"), filepath.Join(t.TempDir(), "skills"))

	c := findCandidate(t, result, "claude:mcp:exa")
	e := c.Entry
	if e == nil || e.Command != "cmd" || strings.Join(e.Args, "|") != "/c|npx|-y|exa-mcp-server" {
		t.Fatalf("stdio conversion wrong: %+v", e)
	}
	if e.Env["EXA_API_KEY"] != "secret" || e.Type != "stdio" {
		t.Fatalf("env/type not preserved: %+v", e)
	}
	if e.Name != "exa" {
		t.Fatalf("name not carried: %+v", e)
	}

	h := findCandidate(t, result, "claude:mcp:docs")
	if h.Entry.Type != "http" || h.Entry.URL != "https://docs.example/mcp" || h.Entry.Headers["X-Key"] != "k" {
		t.Fatalf("http conversion wrong: %+v", h.Entry)
	}
}

func TestScanCodexMCPConversion(t *testing.T) {
	home := buildHome(t)
	result := scanFor(t, home, filepath.Join(t.TempDir(), "commands"), filepath.Join(t.TempDir(), "skills"))

	c := findCandidate(t, result, "codex:mcp:node_repl")
	e := c.Entry
	if e.Command != `C:\bin\node_repl.exe` || e.Type != "stdio" {
		t.Fatalf("command not preserved: %+v", e)
	}
	if e.Env["NODE_REPL_NODE_PATH"] != `C:\bin\node.exe` {
		t.Fatalf("env subtable not mapped: %+v", e)
	}
	if e.StartupTimeoutSeconds != 120 {
		t.Fatalf("startup_timeout_sec not mapped: %+v", e)
	}

	g := findCandidate(t, result, "codex:mcp:codegraph")
	if g.Entry.Command != "codegraph" || strings.Join(g.Entry.Args, "|") != "serve|--mcp" {
		t.Fatalf("args not preserved: %+v", g.Entry)
	}

	d := findCandidate(t, result, "codex:mcp:disabled_one")
	if d.Recommended {
		t.Fatal("disabled source entry must not be recommended")
	}
	hasNote := false
	for _, n := range d.Notes {
		if n == "disabled in source" {
			hasNote = true
		}
	}
	if !hasNote {
		t.Fatalf("missing disabled note: %v", d.Notes)
	}
}

func TestScanOpenCodeMCPConversion(t *testing.T) {
	home := buildHome(t)
	result := scanFor(t, home, filepath.Join(t.TempDir(), "commands"), filepath.Join(t.TempDir(), "skills"))

	c := findCandidate(t, result, "opencode:mcp:local-tool")
	if c.Entry.Command != "npx" || strings.Join(c.Entry.Args, "|") != "-y|local-tool" {
		t.Fatalf("opencode local command array not split: %+v", c.Entry)
	}

	r := findCandidate(t, result, "opencode:mcp:remote-tool")
	if r.Entry.Type != "http" || r.Entry.URL != "https://mcp.example/sse" || r.Entry.Headers["Authorization"] != "Bearer t" {
		t.Fatalf("opencode remote conversion wrong: %+v", r.Entry)
	}

	off := findCandidate(t, result, "opencode:mcp:off-tool")
	if off.Recommended {
		t.Fatal("disabled opencode entry must not be recommended")
	}
}

func TestScanOpenCodeJSONCFallback(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only a .jsonc exists, carrying comments and a trailing comma-free shape.
	jsonc := "{\n// main config\n\"mcp\": {\"tool\": {\"type\": \"local\", \"command\": [\"run\", \"tool\"] /* inline */}}\n}\n"
	if err := os.WriteFile(filepath.Join(path, "opencode.jsonc"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{Home: home, CommandsDir: filepath.Join(t.TempDir(), "commands"), SkillsDir: filepath.Join(t.TempDir(), "skills")}
	result := s.Scan()
	c := findCandidate(t, result, "opencode:mcp:tool")
	if c.Entry.Command != "run" || strings.Join(c.Entry.Args, "|") != "tool" {
		t.Fatalf("jsonc fallback conversion wrong: %+v", c.Entry)
	}
}

func TestScanMarksSourceNotDetected(t *testing.T) {
	home := t.TempDir() // no tool dirs at all
	result := scanFor(t, home, filepath.Join(t.TempDir(), "commands"), filepath.Join(t.TempDir(), "skills"))
	if len(result.Candidates) != 0 {
		t.Fatalf("expected zero candidates, got %d", len(result.Candidates))
	}
	for _, src := range result.Sources {
		if src.Detected || src.Note != "not detected" {
			t.Fatalf("source %s should be not-detected: %+v", src.Tool, src)
		}
	}
}

func TestScanDetectsConflictAgainstTargets(t *testing.T) {
	home := buildHome(t)
	targets := t.TempDir()
	commandsDir := filepath.Join(targets, "commands")
	skillsDir := filepath.Join(targets, "skills")
	if err := os.MkdirAll(filepath.Join(commandsDir, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "review.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "git", "commit.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "pdf"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := scanFor(t, home, commandsDir, skillsDir, "exa", "codegraph")
	for _, id := range []string{"claude:command:review", "claude:command:git:commit", "claude:skill:pdf", "claude:mcp:exa", "codex:mcp:codegraph"} {
		if c := findCandidate(t, result, id); !c.Conflict {
			t.Fatalf("%s should conflict with target storage", id)
		}
	}
	for _, id := range []string{"codex:command:ship", "codex:skill:buffett", "claude:mcp:docs", "opencode:mcp:local-tool"} {
		if c := findCandidate(t, result, id); c.Conflict {
			t.Fatalf("%s must not conflict", id)
		}
	}
}

func TestScanDoesNotFollowSymlinks(t *testing.T) {
	if os.Getenv("AGENTIMPORT_SYMLINK_TEST") == "" {
		t.Skip("symlink creation needs privileges on Windows; set AGENTIMPORT_SYMLINK_TEST to run")
	}
	home := t.TempDir()
	commandsDir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "linked.md"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "linked.md"), filepath.Join(commandsDir, "linked.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	s := &Scanner{Home: home, CommandsDir: filepath.Join(t.TempDir(), "commands"), SkillsDir: filepath.Join(t.TempDir(), "skills")}
	result := s.Scan()
	if len(result.Candidates) != 0 {
		t.Fatalf("symlinked files must not be scanned, got %d candidates", len(result.Candidates))
	}
}
