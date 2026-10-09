package proc

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Task 679 (Windows conhost flash, audit wrap-up): every spawn must either go
// through proc.Command / proc.CommandContext (HideWindow + CREATE_NO_WINDOW on
// Windows) or sit in the explicit exemption table below with a written reason.
// The first version of this rule was "just use proc.Command everywhere"; it
// immediately went stale because legitimate exemptions exist — non-Windows
// build tags, console-attached TUI handoffs, intentional GUI relaunches. This
// test is the mechanical form of the rule, in the spirit of task 299's
// orchestrator-binding guard: it scans every non-test .go file in the module
// for raw exec.Command / exec.CommandContext call sites and fails on any site
// that is neither a proc constructor nor allowlisted. A future spawn cannot be
// added without either migrating it or writing down why it is exempt.
//
// Known reading of the rule: the scan is line-based and skips comment lines,
// so a raw call hidden inside a /* */ block or a string literal would slip
// past — that shape is one line of review; the guard covers the macro
// omission (a new file with a bare spawn flashes by default).
func TestEverySpawnSiteIsHiddenOrExempt(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}

	// Exempt raw spawn sites. Keys are slash-separated paths relative to the
	// module root; every entry must still contain at least one raw call site
	// (stale entries fail below), and each carries the reason it is exempt.
	exemptions := map[string]string{
		// Console-context CLI benchmark harness: children inherit the
		// operator's console, no window can appear.
		"cmd/e2ebench/diff.go":         "console-context benchmark harness",
		"cmd/e2ebench/forkmode.go":     "console-context benchmark harness",
		"cmd/e2ebench/grader.go":       "console-context benchmark harness",
		"cmd/e2ebench/main.go":         "console-context benchmark harness",
		"cmd/e2ebench/mutation.go":     "console-context benchmark harness",
		"cmd/e2ebench/segmentrun.go":   "console-context benchmark harness",
		"cmd/e2ebench/swebench_run.go": "console-context benchmark harness",
		// The launcher is -H windowsgui; the probe child is a copy of the
		// launcher itself, so it is a GUI-subsystem child and cannot pop a
		// console.
		"cmd/reasonix-launcher/probe_windows.go": "windowsgui self-copy crash probe (GUI child)",
		// Console migrator launching the windowsgui launcher at the end of a
		// user-run migration; the GUI child cannot pop a console.
		"cmd/reasonix-legacy-migrator/main.go": "console migrator starts windowsgui launcher child",
		// Non-Windows build tags: CREATE_NO_WINDOW does not exist there and
		// proc.HideWindow is a no-op.
		"desktop/cmd/update-helper/main_linux.go":  "linux-only (dpkg/polkit helper)",
		"desktop/devinfo_darwin.go":                "darwin-only",
		"desktop/external_opener_darwin.go":        "darwin-only",
		"desktop/external_opener_linux.go":         "linux-only (xdg)",
		"desktop/open_workspace_darwin.go":         "darwin-only",
		"desktop/open_workspace_unix.go":           "unix-only (xdg-open)",
		"desktop/terminal_process_unix.go":         "unix-only",
		"desktop/updater_deb_linux.go":             "linux-only",
		"desktop/updater_install_linux.go":         "linux-only",
		"desktop/updater_mac.go":                   "darwin-only",
		"desktop/updater_other.go":                 "!windows build tag",
		"internal/notify/sender_darwin.go":         "darwin-only",
		"internal/notify/sender_linux.go":          "linux-only",
		"internal/sandbox/git_preflight_darwin.go": "darwin-only",
		"internal/sandbox/seatbelt_darwin.go":      "darwin-only",
		"internal/sandbox/seatbelt_other.go":       "linux-only (bubblewrap)",
		// Relaunch already goes through configureDetachedRelaunch, which sets
		// SysProcAttr{HideWindow: true, CreationFlags: DETACHED_PROCESS|...}.
		"desktop/restart_update.go": "relaunch uses configureDetachedRelaunch (explicit HideWindow)",
		// Terminal TUI flows: tea.ExecProcess hands the user's console/ConPTY
		// to an interactive editor or OAuth child; the browser openers are
		// intentionally visible (rundll32 is GUI-subsystem anyway).
		"internal/cli/mcp_manager_actions.go": "TUI console-attached editor/auth/browser handoff (intentional visible)",
		"internal/cli/remote_connect.go":      "openInBrowser is intentionally visible",
		// The desktop child is a windowsgui binary — no console can be created
		// for it regardless of flags.
		"internal/desktoplauncher/launcher.go": "windowsgui desktop child (migrator spawn is via proc.Command)",
		"internal/desktoplauncher/watchdog.go": "windowsgui desktop child restart",
		// taskkill in KillTree is hidden at the site: HideWindow(kill) follows.
		"internal/proc/kill_windows.go": "taskkill fallback, HideWindow applied at site",
	}

	constructorFile := "internal/proc/command.go"
	constructorSites := 0
	exemptionHits := map[string]int{}
	violations := []string{}
	scannedFiles := 0

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "tmp":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		scannedFiles++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if !strings.Contains(trimmed, "exec.Command(") && !strings.Contains(trimmed, "exec.CommandContext(") {
				continue
			}
			if rel == constructorFile {
				constructorSites++
				continue
			}
			if _, ok := exemptions[rel]; ok {
				exemptionHits[rel]++
				continue
			}
			violations = append(violations, rel+":"+strconv.Itoa(i+1)+" "+trimmed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module tree: %v", err)
	}

	if scannedFiles < 500 {
		t.Fatalf("guard scanned only %d go files — did the walk root break?", scannedFiles)
	}
	if constructorSites < 4 {
		t.Fatalf("guard saw only %d constructor sites in %s, expected the full surface (Command/CommandContext/VisibleCommand/VisibleCommandContext)", constructorSites, constructorFile)
	}
	for _, v := range violations {
		t.Errorf("raw spawn not migrated and not exempt: %s — use proc.Command/proc.CommandContext, or add an exemption with a reason", v)
	}
	for file := range exemptions {
		if exemptionHits[file] == 0 {
			t.Errorf("stale exemption %q: no raw spawn site remains in the file — drop the entry", file)
		}
	}
	if len(exemptionHits) < 25 {
		t.Fatalf("guard matched only %d exemption files, expected the full audited surface (31 at task-679 time)", len(exemptionHits))
	}
}

// TestHideWindowImplementationIntact pins the implementation the coverage rule
// relies on: both hidden constructors must call HideWindow, HideWindow must
// set both HideWindow and CREATE_NO_WINDOW (0x08000000) on Windows, and the
// KillTree taskkill fallback must stay hidden. Gutting any of these would keep
// this package compiling while every "hidden" spawn flashes again.
func TestHideWindowImplementationIntact(t *testing.T) {
	command, err := os.ReadFile("command.go")
	if err != nil {
		t.Fatalf("read command.go: %v", err)
	}
	src := string(command)
	if got := strings.Count(src, "HideWindow(cmd)"); got < 2 {
		t.Errorf("command.go calls HideWindow(cmd) %d times, want >= 2 (Command and CommandContext)", got)
	}
	hideWindows, err := os.ReadFile("hide_windows.go")
	if err != nil {
		t.Fatalf("read hide_windows.go: %v", err)
	}
	hw := string(hideWindows)
	if !strings.Contains(hw, "HideWindow = true") {
		t.Error("hide_windows.go no longer sets SysProcAttr.HideWindow")
	}
	if !strings.Contains(hw, "0x08000000") {
		t.Error("hide_windows.go no longer sets CREATE_NO_WINDOW (0x08000000)")
	}
	killWindows, err := os.ReadFile("kill_windows.go")
	if err != nil {
		t.Fatalf("read kill_windows.go: %v", err)
	}
	if !strings.Contains(string(killWindows), "HideWindow(kill)") {
		t.Error("kill_windows.go taskkill fallback no longer hidden")
	}
	// Task 679's own fix: the sentinel exit scan runs git per guarded
	// operation inside the agent process — it must stay on proc.Command.
	exitscan, err := os.ReadFile(filepath.Join("..", "sentinel", "exitscan.go"))
	if err != nil {
		t.Fatalf("read sentinel/exitscan.go: %v", err)
	}
	if !strings.Contains(string(exitscan), "proc.Command(") {
		t.Error("sentinel/exitscan.go gitContent no longer spawns via proc.Command")
	}
	// The three surfaces task 679 names explicitly — MCP stdio plugins, hooks,
	// and the bash tool — must stay on the hidden constructors. This chain is
	// what made a codegraph MCP (re)spawn flash a conhost per launch.
	pinnedSurfaces := map[string]string{
		filepath.Join("..", "plugin", "transport_stdio.go"): "proc.CommandContext(",
		filepath.Join("..", "hook", "hook.go"):              "proc.CommandContext(",
		filepath.Join("..", "tool", "builtin", "bash.go"):   "proc.CommandContext(",
	}
	for file, want := range pinnedSurfaces {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(src), want) {
			t.Errorf("%s no longer spawns via %s — the hidden-constructor rule regressed on a task-679 surface", file, want)
		}
	}
}
