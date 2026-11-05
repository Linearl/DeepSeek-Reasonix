package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// agentImportFixtureHome builds a fake external-tools home (same shapes the
// real installs use) and redirects the Reasonix target home + OS home env so
// the App bridge methods resolve both sides into temp space.
func agentImportFixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("REASONIX_HOME", target)

	write := func(rel, content string) {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(".claude", "commands", "review.md"), "---\ndescription: Review code\nargument-hint: <file>\n---\n\nReview $ARGUMENTS. $$\n")
	write(filepath.Join(".claude", "skills", "pdf", "SKILL.md"), "---\nname: pdf\ndescription: Handle PDFs\n---\n\nRead it.\n")
	write(filepath.Join(".claude.json"), `{"mcpServers":{"exa":{"type":"stdio","command":"cmd","args":["/c","npx","-y","exa-mcp-server"],"env":{"EXA_API_KEY":"secret"}}}}`)
	write(filepath.Join(".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n")
	return home
}

func TestAgentImportScanBridgeDetectsSources(t *testing.T) {
	agentImportFixtureHome(t)
	app := &App{}
	view := app.AgentImportScan()
	if len(view.Sources) != 3 {
		t.Fatalf("sources = %+v", view.Sources)
	}
	detected := map[string]bool{}
	for _, src := range view.Sources {
		detected[src.Tool] = src.Detected
	}
	for _, tool := range []string{"claude", "codex"} {
		if !detected[tool] {
			t.Fatalf("tool %s should be detected: %+v", tool, view.Sources)
		}
	}
	if len(view.Items) < 4 {
		t.Fatalf("expected at least 4 items, got %+v", view.Items)
	}
	ids := map[string]bool{}
	for _, item := range view.Items {
		ids[item.ID] = true
	}
	for _, want := range []string{"claude:command:review", "claude:skill:pdf", "claude:mcp:exa", "codex:mcp:codegraph"} {
		if !ids[want] {
			t.Fatalf("missing item %q; have %v", want, ids)
		}
	}
}

func TestAgentImportImportBridgeLandsAllThreeKinds(t *testing.T) {
	agentImportFixtureHome(t)
	app := &App{}
	scan := app.AgentImportScan()
	requests := make([]AgentImportRequestView, 0, len(scan.Items))
	for _, item := range scan.Items {
		requests = append(requests, AgentImportRequestView{ID: item.ID, Policy: "skip"})
	}
	receipt, err := app.AgentImportImport(requests)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != len(requests) || receipt.Failed != 0 || receipt.Skipped != 0 {
		t.Fatalf("receipt = %+v", receipt)
	}

	home := os.Getenv("REASONIX_HOME")
	// Command landed in the Reasonix home commands dir.
	raw, err := os.ReadFile(filepath.Join(home, "commands", "review.md"))
	if err != nil {
		t.Fatalf("command not landed: %v", err)
	}
	if !strings.Contains(string(raw), "Review $ARGUMENTS. $$") {
		t.Fatalf("command content lost: %s", raw)
	}
	// Skill landed as a directory-layout skill.
	if _, err := os.Stat(filepath.Join(home, "skills", "pdf", "SKILL.md")); err != nil {
		t.Fatalf("skill not landed: %v", err)
	}
	// MCP entries persisted into the user config with conversion fidelity.
	cfg := config.LoadForEdit(config.UserConfigPath())
	byName := map[string]config.PluginEntry{}
	for _, p := range cfg.Plugins {
		byName[p.Name] = p
	}
	exa, ok := byName["exa"]
	if !ok {
		t.Fatalf("exa not persisted: %+v", cfg.Plugins)
	}
	if exa.Command != "cmd" || strings.Join(exa.Args, "|") != "/c|npx|-y|exa-mcp-server" || exa.Env["EXA_API_KEY"] != "secret" {
		t.Fatalf("exa fidelity lost: %+v", exa)
	}
	cg, ok := byName["codegraph"]
	if !ok || cg.Command != "codegraph" || strings.Join(cg.Args, "|") != "serve|--mcp" {
		t.Fatalf("codegraph fidelity lost: %+v", byName["codegraph"])
	}
	// A second import with skip must not duplicate anything.
	receipt2, err := app.AgentImportImport(requests)
	if err != nil {
		t.Fatal(err)
	}
	if receipt2.Skipped != len(requests) {
		t.Fatalf("second import receipt = %+v", receipt2)
	}
}

func TestAgentImportImportBridgeRejectsEmptySelection(t *testing.T) {
	agentImportFixtureHome(t)
	app := &App{}
	if _, err := app.AgentImportImport(nil); err == nil {
		t.Fatal("empty selection must be rejected")
	}
}

func TestAgentImportImportBridgePerItemFailureCarriesDetail(t *testing.T) {
	agentImportFixtureHome(t)
	app := &App{}
	scan := app.AgentImportScan()
	// Import only MCP items but break the user config path so the sink fails
	// (REASONIX_HOME points at a file, not a directory).
	broken := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(broken, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REASONIX_HOME", broken)
	requests := []AgentImportRequestView{}
	for _, item := range scan.Items {
		if item.Kind == "mcp" {
			requests = append(requests, AgentImportRequestView{ID: item.ID})
		}
	}
	if len(requests) == 0 {
		t.Fatal("expected MCP items in scan")
	}
	receipt, err := app.AgentImportImport(requests)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Failed != len(requests) {
		t.Fatalf("receipt = %+v", receipt)
	}
	for _, item := range receipt.Items {
		if item.Status != "failed" || item.Detail == "" {
			t.Fatalf("failed item must carry a reason: %+v", item)
		}
	}
}
