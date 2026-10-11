package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/outputstyle"
)

// writeOutputStyleFile drops one style .md under the isolated user home (the
// first outputstyle.Dirs() entry once HOME/USERPROFILE point at the temp dir).
func writeOutputStyleFile(t *testing.T, home, name, content string) {
	t.Helper()
	dir := filepath.Join(home, ".reasonix", "output-styles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Compile-time pin, same discipline as labIntakeSwitches (task 262): the
// frontend calls all four, so deleting any wrapper must fail the build
// instead of resurfacing as a dead selector behind a clicking switch.
type outputStyleBindings interface {
	SetExperimentalOutputStyleUI(bool) error
	SetOutputStyle(string) (string, error)
	ListOutputStyles() (OutputStyleListView, error)
	GetOutputStylesDir() (string, error)
}

var _ outputStyleBindings = (*App)(nil)

// TestOutputStyleLabRoundTrip (task 385a) pins the acceptance list at the App
// layer: default-off zero regression, selector/settings round trip, custom md
// with and without keep-coding-instructions, a bad frontmatter surfaced as a
// visible issue, and an unknown style rejected instead of silently written.
func TestOutputStyleLabRoundTrip(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	app := &App{}

	// 1. Default off: no gate, no style — zero regression before any click.
	if got := app.Settings(); got.ExperimentalOutputStyleUI || got.OutputStyle != "" {
		t.Fatalf("defaults must be off/empty: switch=%v style=%q", got.ExperimentalOutputStyleUI, got.OutputStyle)
	}
	if got := app.DesktopStartupSettings(); got.ExperimentalOutputStyleUI || got.OutputStyle != "" {
		t.Fatalf("startup defaults must be off/empty: switch=%v style=%q", got.ExperimentalOutputStyleUI, got.OutputStyle)
	}
	view, err := app.ListOutputStyles()
	if err != nil {
		t.Fatalf("ListOutputStyles: %v", err)
	}
	if view.Active != "" {
		t.Fatalf("active must start empty, got %q", view.Active)
	}
	// Built-in 3 are always listed, even with the gate off (the list is data,
	// not UI — the gate only controls whether the panel renders).
	seenBuiltin := map[string]bool{}
	for _, opt := range view.Options {
		if opt.Builtin {
			seenBuiltin[opt.Name] = true
		}
	}
	for _, name := range []string{"explanatory", "learning", "concise"} {
		if !seenBuiltin[name] {
			t.Errorf("built-in %q missing from options: %+v", name, view.Options)
		}
	}

	// 2. The gate flips and reads back through both views.
	if err := app.SetExperimentalOutputStyleUI(true); err != nil {
		t.Fatalf("SetExperimentalOutputStyleUI: %v", err)
	}
	if got := app.Settings(); !got.ExperimentalOutputStyleUI {
		t.Fatal("Settings() must read the gate back on")
	}
	if got := app.DesktopStartupSettings(); !got.ExperimentalOutputStyleUI {
		t.Fatal("DesktopStartupSettings() must read the gate back on")
	}

	// 3. Selection round trip: setter → Settings readback → selector active.
	if _, err := app.SetOutputStyle("concise"); err != nil {
		t.Fatalf("SetOutputStyle(concise): %v", err)
	}
	if got := app.Settings(); got.OutputStyle != "concise" {
		t.Fatalf("Settings().OutputStyle = %q, want concise", got.OutputStyle)
	}
	data, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read user config: %v", err)
	}
	if !strings.Contains(string(data), `output_style = "concise"`) ||
		!strings.Contains(string(data), "experimental_output_style_ui = true") {
		t.Fatalf("both keys must be on disk:\n%s", data)
	}
	view, err = app.ListOutputStyles()
	if err != nil {
		t.Fatalf("ListOutputStyles after select: %v", err)
	}
	if view.Active != "concise" {
		t.Fatalf("view.Active = %q, want concise", view.Active)
	}
	activeCount := 0
	for _, opt := range view.Options {
		if opt.Active {
			activeCount++
			if opt.Name != "concise" {
				t.Errorf("active option is %q, want concise", opt.Name)
			}
		}
	}
	if activeCount != 1 {
		t.Errorf("exactly one option must be active, got %d", activeCount)
	}

	// 4. An unknown style is a visible error, not a silent write.
	if _, err := app.SetOutputStyle("no-such-style-385a"); err == nil {
		t.Fatal("unknown style must be rejected with an error")
	}
	if got := app.Settings(); got.OutputStyle != "concise" {
		t.Fatalf("a rejected set must not change the persisted value, got %q", got.OutputStyle)
	}

	// 5. Custom md: both keep-coding states load; a broken frontmatter is
	// surfaced as an issue instead of vanishing.
	writeOutputStyleFile(t, home, "plain.md",
		"---\ndescription: append style\n---\nBe plain.\n")
	writeOutputStyleFile(t, home, "persona.md",
		"---\ndescription: replace style\nkeep-coding-instructions: false\n---\nPure persona.\n")
	writeOutputStyleFile(t, home, "broken.md",
		"---\ndescription: \"unclosed quote\n---\nBody.\n")

	view, err = app.ListOutputStyles()
	if err != nil {
		t.Fatalf("ListOutputStyles with custom files: %v", err)
	}
	byName := map[string]OutputStyleOption{}
	for _, opt := range view.Options {
		byName[opt.Name] = opt
	}
	plain, ok := byName["plain"]
	if !ok || !plain.KeepCoding || plain.Builtin {
		t.Errorf("plain.md (no flag) must load keep=true custom: %+v ok=%v", plain, ok)
	}
	persona, ok := byName["persona"]
	if !ok || persona.KeepCoding || persona.Builtin {
		t.Errorf("persona.md (keep=false) must load keep=false custom: %+v ok=%v", persona, ok)
	}
	if _, ok := byName["broken"]; ok {
		t.Error("broken.md must not appear as a selectable style")
	}
	foundIssue := false
	for _, issue := range view.Issues {
		if issue.Name == "broken" && issue.Reason != "" && issue.Path != "" {
			foundIssue = true
		}
	}
	if !foundIssue {
		t.Errorf("broken.md must surface as a visible issue, got %+v", view.Issues)
	}

	// A custom style is selectable — the write goes through the same setter.
	if _, err := app.SetOutputStyle("plain"); err != nil {
		t.Fatalf("SetOutputStyle(plain): %v", err)
	}
	if got := app.Settings(); got.OutputStyle != "plain" {
		t.Fatalf("custom style readback = %q, want plain", got.OutputStyle)
	}

	// 6. "default" is the no-style sentinel: it normalizes to the empty value.
	if _, err := app.SetOutputStyle("default"); err != nil {
		t.Fatalf("SetOutputStyle(default): %v", err)
	}
	if got := app.Settings(); got.OutputStyle != "" {
		t.Fatalf("default must clear the style, got %q", got.OutputStyle)
	}
}

// TestOutputStyleOpenDir (task 770) pins the open-directory binding: with
// nothing on disk the .reasonix/output-styles fallback is created so the
// reveal lands on a real folder; an existing home convention dir wins over
// the fallback; and among several existing dirs the highest-priority one
// (.reasonix — the loader's later-wins order) wins.
func TestOutputStyleOpenDir(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	app := &App{}

	// 1. Empty home: the fallback is created and returned.
	dir, err := app.GetOutputStylesDir()
	if err != nil {
		t.Fatalf("GetOutputStylesDir: %v", err)
	}
	wantFallback := filepath.Join(home, ".reasonix", "output-styles")
	if dir != wantFallback {
		t.Fatalf("dir = %q, want created fallback %q", dir, wantFallback)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("fallback must exist as a dir after the call: %v", err)
	}

	// 2. Only the low-priority .claude dir exists: it must win over the
	// fallback, because that is where the user's custom styles actually live.
	// The fallback from step 1 is dropped first so the priority walk faces a
	// real choice instead of the dir it just created.
	if err := os.RemoveAll(wantFallback); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(home, ".claude", "output-styles")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err = app.GetOutputStylesDir()
	if err != nil {
		t.Fatalf("GetOutputStylesDir after .claude: %v", err)
	}
	if dir != claude {
		t.Fatalf("dir = %q, want existing .claude dir %q", dir, claude)
	}

	// 3. Both .claude and .reasonix exist: .reasonix wins (later-wins order).
	if err := os.MkdirAll(wantFallback, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err = app.GetOutputStylesDir()
	if err != nil {
		t.Fatalf("GetOutputStylesDir with both dirs: %v", err)
	}
	if dir != wantFallback {
		t.Fatalf("dir = %q, want highest-priority %q", dir, wantFallback)
	}

	// 4. The dual-track builtin (task 770) must resolve and apply as an
	// augmentation of the coding prompt.
	st, ok := outputstyle.Resolve("dual-track", outputstyle.Dirs())
	if !ok {
		t.Fatal("builtin dual-track must resolve")
	}
	if !st.Builtin || !st.KeepCoding {
		t.Fatalf("dual-track must be a keep-coding builtin: %+v", st)
	}
	if !strings.Contains(st.Description, "通俗") || !strings.Contains(st.Body, "通俗版") {
		t.Fatalf("dual-track text must carry the 双轨 contract: desc=%q body=%q", st.Description, st.Body)
	}
	got := outputstyle.Apply("base prompt", st)
	if !strings.HasPrefix(got, "base prompt\n\n") {
		t.Fatalf("dual-track must append to the base prompt, got %q", got)
	}
}
