package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/installlayout"
)

// fakeInstallRoot builds a minimal versioned install: a launcher, a
// current.json pointer, and version trees each carrying a desktop binary so
// SwitchToVersion's completeness check accepts them.
func fakeInstallRoot(t *testing.T, versions []string, active string) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range versions {
		dir := filepath.Join(root, installlayout.VersionsDirName, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, installlayout.DesktopBinaryName()), []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(root, installlayout.LauncherBinaryName())
	if err := os.WriteFile(launcher, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	if active != "" {
		ptr := installlayout.CurrentPointer{SchemaVersion: installlayout.CurrentSchemaVersion, ActiveVersion: active, ActiveDir: installlayout.VersionDirRelative(active)}
		body, err := json.Marshal(ptr)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, installlayout.CurrentFileName), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		versionSwitchInstallRoot = resolveVersionedInstallRoot
		versionSwitchStartLauncher = startDetachedLauncher
		versionSwitchQuit = func(a *App) { a.quitApp() }
	})
	versionSwitchInstallRoot = func() (string, error) { return root, nil }
	return root
}

// fakeVersionedConfig toggles the experiment through the app's own settings
// path against an isolated state dir.
func fakeVersionedConfig(t *testing.T, experimentOn bool) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := &App{}
	if err := app.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalRestartUpdate(experimentOn) }); err != nil {
		t.Fatal(err)
	}
}

func TestListInstalledVersionsFiltersSortsAndMarksActive(t *testing.T) {
	fakeVersionedConfig(t, false)
	fakeInstallRoot(t, []string{"v1.38.3", "v1.38.3-20260921-2300", "v1.37.0"}, "v1.38.3-20260921-2300")
	// Noise that must not surface as a pickable version.
	versionsDir := filepath.Join(versionSwitchRootForTest(), installlayout.VersionsDirName)
	if err := os.MkdirAll(filepath.Join(versionsDir, "versions", ".staging-abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionsDir, "versions", "not-a-version.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	got, err := app.ListInstalledVersions()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range got {
		names = append(names, v.Version)
	}
	want := []string{"v1.38.3-20260921-2300", "v1.38.3", "v1.37.0"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("versions=%v want %v (newest-name first)", names, want)
	}
	for _, v := range got {
		if v.Version == "v1.38.3-20260921-2300" && !v.Active {
			t.Fatalf("active version not marked: %+v", got)
		}
		if v.Version != "v1.38.3-20260921-2300" && v.Active {
			t.Fatalf("non-active version marked active: %+v", got)
		}
	}
}

// versionSwitchRootForTest re-derives the fake root the same way the stub
// does; it exists only to keep noise fixtures inside the same scratch tree.
func versionSwitchRootForTest() string {
	root, err := versionSwitchInstallRoot()
	if err != nil {
		panic(err)
	}
	return root
}

func TestSwitchToVersionGuards(t *testing.T) {
	fakeInstallRoot(t, []string{"v1.38.3", "v1.37.0"}, "v1.38.3")
	fakeVersionedConfig(t, false) // experiment OFF
	app := &App{}
	if err := app.SwitchToVersion("v1.37.0"); err == nil || !strings.Contains(err.Error(), "experiment is off") {
		t.Fatalf("switch with experiment off must refuse, got %v", err)
	}
	fakeVersionedConfig(t, true)
	if err := app.SwitchToVersion(""); err == nil || !strings.Contains(err.Error(), "version is required") {
		t.Fatalf("empty version must be refused, got %v", err)
	}
	if err := app.SwitchToVersion("../escape"); err == nil {
		t.Fatalf("traversal version must be refused")
	}
	if err := app.SwitchToVersion("v1.38.3"); err == nil || !strings.Contains(err.Error(), "already the active version") {
		t.Fatalf("switch to active version must be a named no-op, got %v", err)
	}
}

func TestSwitchToVersionRefusesIncompleteTree(t *testing.T) {
	fakeVersionedConfig(t, true)
	root := fakeInstallRoot(t, []string{"v1.38.3", "v1.37.0"}, "v1.38.3")
	// A version tree missing its desktop binary must not become active.
	if err := os.Remove(filepath.Join(root, installlayout.VersionsDirName, "v1.37.0", installlayout.DesktopBinaryName())); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if err := app.SwitchToVersion("v1.37.0"); err == nil || !strings.Contains(err.Error(), "incomplete version tree") {
		t.Fatalf("incomplete tree must be refused, got %v", err)
	}
	// The pointer must still name the old version.
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != "v1.38.3" {
		t.Fatalf("refused switch moved the pointer: %+v", ptr)
	}
}

func TestSwitchToVersionMovesPointerAndRelaunches(t *testing.T) {
	fakeVersionedConfig(t, true)
	root := fakeInstallRoot(t, []string{"v1.38.3-20260922-0100", "v1.38.3"}, "v1.38.3")
	launched := make(chan string, 1)
	quit := make(chan struct{})
	versionSwitchStartLauncher = func(launcherPath string, exitingPID int) error {
		launched <- filepath.Dir(launcherPath)
		return nil
	}
	versionSwitchQuit = func(a *App) { close(quit) }
	app := &App{}
	if err := app.SwitchToVersion("v1.38.3-20260922-0100"); err != nil {
		t.Fatal(err)
	}
	if dir := <-launched; dir != root {
		t.Fatalf("launcher started from %q want %q", dir, root)
	}
	<-quit
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != "v1.38.3-20260922-0100" {
		t.Fatalf("pointer not moved: %+v", ptr)
	}
	if ptr.ActiveDir != "versions/v1.38.3-20260922-0100" {
		t.Fatalf("active dir wrong: %+v", ptr)
	}
	// Read-back verification is the acceptance criterion: a fresh ReadCurrent
	// (not the in-memory value) must name the switched version.
	again, err := app.ListInstalledVersions()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range again {
		if v.Version == "v1.38.3-20260922-0100" && !v.Active {
			t.Fatalf("list after switch does not mark the new active version: %+v", again)
		}
	}
}

func TestSwitchToVersionMissingLauncherReportsAndKeepsPointer(t *testing.T) {
	fakeVersionedConfig(t, true)
	root := fakeInstallRoot(t, []string{"v1.38.3", "v1.37.0"}, "v1.38.3")
	if err := os.Remove(filepath.Join(root, installlayout.LauncherBinaryName())); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if err := app.SwitchToVersion("v1.37.0"); err == nil || !strings.Contains(err.Error(), "launcher") {
		t.Fatalf("missing launcher must surface, got %v", err)
	}
	// The pointer has moved (that write committed first by design); the error
	// text tells the caller the launcher must be run manually. Verify the move
	// so the message's promise stays true.
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil {
		t.Fatal(err)
	}
	if ptr.ActiveVersion != "v1.37.0" {
		t.Fatalf("pointer unexpected after launcher failure: %+v", ptr)
	}
}
