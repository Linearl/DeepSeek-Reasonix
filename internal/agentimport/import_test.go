package agentimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// newFixtureImporter wires a scanner + importer over the shared fixture home,
// with isolated target dirs and an in-memory MCP sink.
func newFixtureImporter(t *testing.T) (*Importer, *[]config.PluginEntry, string, string) {
	t.Helper()
	home := buildHome(t)
	targets := t.TempDir()
	commandsDir := filepath.Join(targets, "commands")
	skillsDir := filepath.Join(targets, "skills")
	saved := &[]config.PluginEntry{}
	im := &Importer{
		Scanner: Scanner{Home: home, CommandsDir: commandsDir, SkillsDir: skillsDir},
		MCPSink: func(entry config.PluginEntry) error {
			*saved = append(*saved, entry)
			return nil
		},
	}
	return im, saved, commandsDir, skillsDir
}

func mustScan(t *testing.T, im *Importer) ScanResult {
	t.Helper()
	return im.Scanner.Scan()
}

func TestImportLandsCommandsVerbatim(t *testing.T) {
	im, _, commandsDir, _ := newFixtureImporter(t)
	result := mustScan(t, im)
	_ = result
	receipt, err := im.Import([]ImportRequest{
		{ID: "claude:command:review"},
		{ID: "claude:command:git:commit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 2 || receipt.Skipped != 0 || receipt.Failed != 0 {
		t.Fatalf("receipt = %+v", receipt)
	}
	raw, err := os.ReadFile(filepath.Join(commandsDir, "review.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Review $ARGUMENTS with care. Cost: $$5.") || !strings.Contains(string(raw), "argument-hint: <file>") {
		t.Fatalf("landed command lost content:\n%s", raw)
	}
	nsRaw, err := os.ReadFile(filepath.Join(commandsDir, "git", "commit.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nsRaw), "Commit staged changes") {
		t.Fatalf("namespaced command wrong:\n%s", nsRaw)
	}
}

func TestImportLandsSkillWithSiblings(t *testing.T) {
	im, _, _, skillsDir := newFixtureImporter(t)
	receipt, err := im.Import([]ImportRequest{{ID: "claude:skill:pdf"}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	skill, err := os.ReadFile(filepath.Join(skillsDir, "pdf", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(skill), "Handle PDF files") {
		t.Fatalf("SKILL.md wrong:\n%s", skill)
	}
	ref, err := os.ReadFile(filepath.Join(skillsDir, "pdf", "references", "format.md"))
	if err != nil {
		t.Fatalf("references sibling not copied: %v", err)
	}
	if !strings.Contains(string(ref), "Format details") {
		t.Fatalf("reference content wrong:\n%s", ref)
	}
}

func TestImportFlatSkillNormalisedToDirectoryLayout(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, "legacy.md"), []byte("---\ndescription: legacy\n---\n\nLegacy body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := t.TempDir()
	skillsDir := filepath.Join(targets, "skills")
	im := &Importer{
		Scanner: Scanner{Home: home, CommandsDir: filepath.Join(targets, "commands"), SkillsDir: skillsDir},
		MCPSink: func(config.PluginEntry) error { return nil },
	}
	receipt, err := im.Import([]ImportRequest{{ID: "claude:skill:legacy"}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "legacy", "SKILL.md")); err != nil {
		t.Fatalf("flat skill not normalised to <name>/SKILL.md: %v", err)
	}
}

func TestImportMCPTroughSink(t *testing.T) {
	im, saved, _, _ := newFixtureImporter(t)
	receipt, err := im.Import([]ImportRequest{{ID: "codex:mcp:node_repl"}, {ID: "opencode:mcp:local-tool"}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 2 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(*saved) != 2 {
		t.Fatalf("sink received %+v", *saved)
	}
	byName := map[string]config.PluginEntry{}
	for _, e := range *saved {
		byName[e.Name] = e
	}
	e := byName["node_repl"]
	if e.Command != `C:\bin\node_repl.exe` || e.StartupTimeoutSeconds != 120 || e.Env["NODE_REPL_NODE_PATH"] != `C:\bin\node.exe` {
		t.Fatalf("MCP fidelity lost through import: %+v", e)
	}
	lt := byName["local-tool"]
	if lt.Command != "npx" || strings.Join(lt.Args, "|") != "-y|local-tool" {
		t.Fatalf("opencode conversion lost: %+v", lt)
	}
}

func TestImportConflictPolicies(t *testing.T) {
	// Seed the target with pre-existing command/skill/MCP sharing names with
	// the fixtures.
	im, saved, commandsDir, skillsDir := newFixtureImporter(t)
	if err := os.MkdirAll(commandsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commandsDir, "review.md"), []byte("EXISTING"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skillsDir, "pdf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "pdf", "SKILL.md"), []byte("EXISTING"), 0o644); err != nil {
		t.Fatal(err)
	}
	*saved = append(*saved, config.PluginEntry{Name: "exa", Command: "old"})
	// Conflict detection reads the scanner's existing-name list, mirroring the
	// production wiring (config plugin names).
	im.Scanner.ExistingMCPNames = []string{"exa"}

	// Skip (default for conflicts).
	receipt, err := im.Import([]ImportRequest{
		{ID: "claude:command:review", Policy: PolicySkip},
		{ID: "claude:skill:pdf", Policy: PolicySkip},
		{ID: "claude:mcp:exa", Policy: PolicySkip},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Skipped != 3 || receipt.Imported != 0 {
		t.Fatalf("skip receipt = %+v", receipt)
	}
	raw, _ := os.ReadFile(filepath.Join(commandsDir, "review.md"))
	if string(raw) != "EXISTING" {
		t.Fatalf("skip must leave the target untouched, got %q", raw)
	}
	if len(*saved) != 1 || (*saved)[0].Command != "old" {
		t.Fatalf("skip must not call the MCP sink: %+v", *saved)
	}

	// Overwrite replaces the target content.
	receipt, err = im.Import([]ImportRequest{
		{ID: "claude:command:review", Policy: PolicyOverwrite},
		{ID: "claude:skill:pdf", Policy: PolicyOverwrite},
		{ID: "claude:mcp:exa", Policy: PolicyOverwrite},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 3 || receipt.Skipped != 0 {
		t.Fatalf("overwrite receipt = %+v", receipt)
	}
	raw, _ = os.ReadFile(filepath.Join(commandsDir, "review.md"))
	if strings.Contains(string(raw), "EXISTING") {
		t.Fatal("overwrite did not replace the command file")
	}
	skillRaw, _ := os.ReadFile(filepath.Join(skillsDir, "pdf", "SKILL.md"))
	if strings.Contains(string(skillRaw), "EXISTING") {
		t.Fatal("overwrite did not replace the skill")
	}
	if len(*saved) != 2 || (*saved)[1].Command != "cmd" {
		t.Fatalf("overwrite did not upsert the MCP entry: %+v", *saved)
	}

	// Rename writes under "<name>-from-<tool>" and leaves the original alone.
	receipt, err = im.Import([]ImportRequest{
		{ID: "claude:command:review", Policy: PolicyRename},
		{ID: "claude:skill:pdf", Policy: PolicyRename},
		{ID: "claude:mcp:exa", Policy: PolicyRename},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 3 {
		t.Fatalf("rename receipt = %+v", receipt)
	}
	for _, item := range receipt.Items {
		if item.Status != "renamed" {
			t.Fatalf("expected renamed status: %+v", item)
		}
	}
	if _, err := os.Stat(filepath.Join(commandsDir, "review-from-claude.md")); err != nil {
		t.Fatalf("renamed command missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "pdf-from-claude", "SKILL.md")); err != nil {
		t.Fatalf("renamed skill missing: %v", err)
	}
	names := []string{}
	for _, e := range (*saved)[2:] {
		names = append(names, e.Name)
	}
	if len(names) != 1 || names[0] != "exa-from-claude" {
		t.Fatalf("renamed MCP entry wrong: %v", names)
	}
}

func TestImportReceiptCountsFailuresWithReason(t *testing.T) {
	im, _, _, _ := newFixtureImporter(t)
	im.MCPSink = func(config.PluginEntry) error {
		return os.ErrPermission
	}
	receipt, err := im.Import([]ImportRequest{
		{ID: "claude:command:review"},
		{ID: "claude:mcp:exa"},
		{ID: "claude:mcp:docs"},
		{ID: "claude:command:vanished"}, // not in the latest scan
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 1 || receipt.Failed != 3 || receipt.Skipped != 0 {
		t.Fatalf("receipt = %+v", receipt)
	}
	byID := map[string]ItemOutcome{}
	for _, item := range receipt.Items {
		byID[item.ID] = item
	}
	mcpFail := byID["claude:mcp:exa"]
	if mcpFail.Status != "failed" || mcpFail.Detail == "" {
		t.Fatalf("failed item must carry a reason: %+v", mcpFail)
	}
	vanished := byID["claude:command:vanished"]
	if vanished.Status != "failed" || !strings.Contains(vanished.Detail, "no longer detected") {
		t.Fatalf("vanished item wrong: %+v", vanished)
	}
}

func TestImportSanitizesForeignSkillNames(t *testing.T) {
	home := t.TempDir()
	skillsRoot := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(filepath.Join(skillsRoot, "wei_rd skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, "wei_rd skill", "SKILL.md"), []byte("---\ndescription: d\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := t.TempDir()
	skillsDir := filepath.Join(targets, "skills")
	im := &Importer{
		Scanner: Scanner{Home: home, CommandsDir: filepath.Join(targets, "commands"), SkillsDir: skillsDir},
		MCPSink: func(config.PluginEntry) error { return nil },
	}
	receipt, err := im.Import([]ImportRequest{{ID: "codex:skill:wei_rd skill"}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "wei_rd-skill", "SKILL.md")); err != nil {
		t.Fatalf("sanitized skill dir missing: %v", err)
	}
	if receipt.Items[0].Status != "renamed" || receipt.Items[0].Name != "wei_rd-skill" {
		t.Fatalf("receipt must surface the sanitized name: %+v", receipt.Items[0])
	}
}

func TestImportScanStaysPureRead(t *testing.T) {
	home := buildHome(t)
	// Hash every fixture file before, scan + import, hash again: the external
	// tools' tree must be byte-identical (pure-read contract).
	hashes := map[string]string{}
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		hashes[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	targets := t.TempDir()
	im := &Importer{
		Scanner: Scanner{Home: home, CommandsDir: filepath.Join(targets, "commands"), SkillsDir: filepath.Join(targets, "skills")},
		MCPSink: func(config.PluginEntry) error { return nil },
	}
	_ = im.Scanner.Scan()
	result := im.Scanner.Scan()
	requests := make([]ImportRequest, 0, len(result.Candidates))
	for _, c := range result.Candidates {
		requests = append(requests, ImportRequest{ID: c.ID, Policy: PolicyOverwrite})
	}
	if _, err := im.Import(requests); err != nil {
		t.Fatal(err)
	}
	after := map[string]string{}
	err = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		after[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != len(after) {
		t.Fatalf("external tree changed: %d files before, %d after", len(hashes), len(after))
	}
	for path, content := range hashes {
		if after[path] != content {
			t.Fatalf("external file %q was modified by scan/import", path)
		}
	}
}

func TestImportSkillsOverwriteRemovesStaleSiblings(t *testing.T) {
	im, _, _, skillsDir := newFixtureImporter(t)
	// Seed an existing skill with a stale sibling the source does not have.
	if err := os.MkdirAll(filepath.Join(skillsDir, "pdf", "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "pdf", "references", "stale.md"), []byte("STALE"), 0o644); err != nil {
		t.Fatal(err)
	}
	receipt, err := im.Import([]ImportRequest{{ID: "claude:skill:pdf", Policy: PolicyOverwrite}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Imported != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "pdf", "references", "stale.md")); !os.IsNotExist(err) {
		t.Fatalf("overwrite must remove stale siblings, stat err = %v", err)
	}
	ref, err := os.ReadFile(filepath.Join(skillsDir, "pdf", "references", "format.md"))
	if err != nil {
		t.Fatalf("fresh sibling missing after overwrite: %v", err)
	}
	if !strings.Contains(string(ref), "Format details") {
		t.Fatalf("fresh sibling content wrong: %s", ref)
	}
}
