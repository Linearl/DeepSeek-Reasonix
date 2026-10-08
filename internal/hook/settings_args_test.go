package hook

import (
	"context"
	
	"testing"
)

// Task 628: a global settings.json hook with an args vector
// ({"command": "…reasonix-computer-use.bat", "args": ["--hook"]}) used to have
// its args dropped on the floor — the .bat then fell through to its MCP-server
// branch on every tool call (heavy imports, multi-second cold starts) instead
// of running the lightweight route_guard, which is how the PreToolUse budget
// kept blowing. These tests pin the repair: settings args reach the spawner's
// argv, and hooks without args keep the legacy shell behavior.

func TestLoadPromotesSettingsArgsToExecArgv(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"hooks":{"PreToolUse":[{"command":"C:\\tools\\hook.bat","args":["--hook"],"description":"guard"}]}}`)
	hooks := Load(LoadOptions{HomeDir: home})
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
	got := hooks[0].Argv
	if len(got) != 1 || got[0] != "--hook" {
		t.Fatalf("argv not promoted from settings args: %q", got)
	}
	if len(hooks[0].Args) != 1 || hooks[0].Args[0] != "--hook" {
		t.Fatalf("raw settings args not retained: %q", hooks[0].Args)
	}
}

func TestRunPassesSettingsArgsToSpawner(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"hooks":{"PreToolUse":[{"command":"C:\\tools\\hook.bat","args":["--hook"]}]}}`)
	hooks := Load(LoadOptions{HomeDir: home})

	var captured SpawnInput
	spawner := func(_ context.Context, in SpawnInput) SpawnResult {
		captured = in
		return SpawnResult{ExitCode: 0}
	}
	report := Run(context.Background(), Payload{Event: PreToolUse, ToolName: "bash", Cwd: t.TempDir()}, hooks, spawner)
	if report.Blocked {
		t.Fatal("passing hook must not block")
	}
	if len(captured.Args) != 1 || captured.Args[0] != "--hook" {
		t.Fatalf("spawner did not receive settings args: %+v", captured)
	}
	if captured.Mode != ExecutionLegacy {
		// The mode stays legacy: spawnLegacyCommand branches to exec form on
		// args != nil, so no manifest-style mode rewrite is needed.
		t.Fatalf("unexpected execution mode %q, want legacy with argv", captured.Mode)
	}
}

func TestSettingsWithoutArgsKeepLegacyShellForm(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"hooks":{"PreToolUse":[{"command":"echo pre"}]}}`)
	hooks := Load(LoadOptions{HomeDir: home})
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
	if hooks[0].Argv != nil || hooks[0].Args != nil {
		t.Fatalf("hook without args must keep nil argv, got argv=%q args=%q", hooks[0].Argv, hooks[0].Args)
	}

	var captured SpawnInput
	spawner := func(_ context.Context, in SpawnInput) SpawnResult {
		captured = in
		return SpawnResult{ExitCode: 0}
	}
	Run(context.Background(), Payload{Event: PreToolUse, ToolName: "bash", Cwd: t.TempDir()}, hooks, spawner)
	if captured.Args != nil {
		t.Fatalf("spawner must see nil args for shell-form hooks, got %q", captured.Args)
	}
}

func TestSettingsArgsSurviveGlobalScopeLoad(t *testing.T) {
	// Mirrors the 628 field report: the hook lives in the legacy global
	// settings.json (report shows "hook [global/PreToolUse]"), not in a
	// project file.
	home := t.TempDir()
	writeSettings(t, home, `{"hooks":{"PreToolUse":[{"command":"C:\\tools\\hook.bat","args":["--hook"],"timeout":15000,"applies_to":"main"}]}}`)
	hooks := Load(LoadOptions{HomeDir: home})
	if len(hooks) != 1 {
		t.Fatalf("expected 1 global hook, got %d", len(hooks))
	}
	h := hooks[0]
	if h.Scope != ScopeGlobal {
		t.Fatalf("scope = %q, want global", h.Scope)
	}
	if len(h.Argv) != 1 || h.Argv[0] != "--hook" {
		t.Fatalf("global hook argv not promoted: %q", h.Argv)
	}
	if h.Timeout != 15000 {
		t.Fatalf("timeout = %d, want 15000", h.Timeout)
	}
}
