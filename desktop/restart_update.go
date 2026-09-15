package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/installlayout"
)

// RestartAndUpdate publishes a locally built desktop release as a new version and
// relaunches through the launcher (task 81, experiment).
//
// The update helper is deliberately not involved. Its path expects a signed payload
// manifest and exists to verify a downloaded artifact; a local build has no manifest
// and nothing to verify. What this needs is only the installation half -- copy the
// binaries into versions/<version>/ and then move the current.json pointer -- and
// installlayout already provides exactly that, with the atomicity that matters here:
// every failure before the pointer swap leaves the running version untouched.
//
// This process has to exit for the new version to take over, and only the
// application can make that happen: the helper's instance handoff opens the target
// with PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE, never PROCESS_TERMINATE.
func (a *App) RestartAndUpdate(sourceDir, version string) error {
	if a == nil {
		return fmt.Errorf("restart: no app")
	}
	version = strings.TrimSpace(version)
	sourceDir = strings.TrimSpace(sourceDir)

	// Opt-in only (task 81): the action swaps the active install version, so neither a
	// stale UI nor a tool call may reach it while the experiment is off.
	if cfg, cfgErr := config.Load(); cfgErr != nil || !cfg.Desktop.ExperimentalRestartUpdate {
		return fmt.Errorf("restart: the restart-and-update experiment is off; enable experimental_restart_update in the desktop settings")
	}

	// A running turn owns a session write and may have an approval or ask card
	// outstanding; restarting through it would strand both. Refuse instead.
	a.mu.Lock()
	busy := false
	for _, tab := range a.tabs {
		if tab.hasActiveRuntimeWork() {
			busy = true
			break
		}
	}
	a.mu.Unlock()
	if busy {
		return fmt.Errorf("restart: a turn is running or background jobs are active; stop them first")
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restart: locate executable: %w", err)
	}
	installRoot, err := installlayout.ResolveInstallRoot(executable)
	if err != nil || installRoot == "" {
		return fmt.Errorf("restart: this build is not a versioned install, so there is no pointer to move: %w", err)
	}

	// Default staging location: a local build dropped into InstallRoot/staging/. Kept
	// as a convention rather than a setting so the button needs no configuration.
	if sourceDir == "" {
		sourceDir = filepath.Join(installRoot, "staging")
	}

	// The status-bar button sends an empty version on purpose: a local build has no
	// signed manifest to read one from, so the version travels with the payload the
	// build script staged (staging/version.txt). Demanding it from the UI left the
	// button failing every time it was pressed.
	if version == "" {
		if raw, readErr := os.ReadFile(filepath.Join(sourceDir, "version.txt")); readErr == nil {
			version = strings.TrimSpace(string(raw))
		}
	}
	if version == "" {
		return fmt.Errorf("restart: no version to publish: %s/version.txt is missing (rebuild to re-stage) and the caller sent none", sourceDir)
	}
	// version.txt carries the product version (1.38.3); the install layout names its
	// directories with a "v" prefix (v1.38.3 - see installlayout.ValidateVersionName),
	// and the directory name IS the activeVersion string. Normalise here so the two
	// cannot drift, and report a bad label before anything is copied.
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if validateErr := installlayout.ValidateVersionName(version); validateErr != nil {
		return fmt.Errorf("restart: %w", validateErr)
	}
	// A silent restart reads as a dead button: the click closes the app and the
	// only trace of what happened lives here. Log every milestone.
	slog.Info("restart: publishing staged build", "version", version, "sourceDir", sourceDir, "installRoot", installRoot)

	members := []installlayout.Member{
		{Name: installlayout.DesktopBinaryName(), Path: filepath.Join(sourceDir, installlayout.DesktopBinaryName())},
		{Name: installlayout.CLIBinaryName(), Path: filepath.Join(sourceDir, installlayout.CLIBinaryName())},
		{Name: installlayout.UpdateHelperBinaryName(), Path: filepath.Join(sourceDir, installlayout.UpdateHelperBinaryName())},
	}
	for _, member := range members {
		if info, statErr := os.Lstat(member.Path); statErr != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("restart: staged %s is missing from %s", member.Name, sourceDir)
		}
	}
	launcherName := installlayout.LauncherBinaryName()
	launcherPath := filepath.Join(sourceDir, launcherName)
	if info, statErr := os.Lstat(launcherPath); statErr != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("restart: staged %s is missing from %s", launcherName, sourceDir)
	}

	if err := installlayout.ActivateVersion(installlayout.ActivationRequest{
		InstallRoot:       installRoot,
		Version:           version,
		RequestID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Members:           members,
		RequiredNames:     installlayout.AllowedVersionMembers(),
		RootMembers:       []installlayout.Member{{Name: launcherName, Path: launcherPath}},
		RequiredRootNames: []string{launcherName},
	}); err != nil {
		return fmt.Errorf("restart: publish version: %w", err)
	}

	slog.Info("restart: publish committed; relaunching", "version", version)
	if err := startDetachedLauncher(filepath.Join(installRoot, launcherName)); err != nil {
		slog.Error("restart: launcher start failed after commit", "version", version, "err", err)
		return fmt.Errorf("restart: start launcher: %w", err)
	}

	// Answer first, exit after. The caller is a UI action or a tool call that has to
	// get a result: a restart that never returns reads as a failure, and retrying a
	// failure like this is another restart.
	go func() {
		time.Sleep(750 * time.Millisecond)
		a.quitApp()
	}()
	return nil
}

// startDetachedLauncher starts the launcher so that it outlives this process.
func startDetachedLauncher(launcherPath string) error {
	cmd := exec.Command(launcherPath)
	cmd.Dir = filepath.Dir(launcherPath)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// restartUpdaterAdapter exposes App.RestartAndUpdate to the tool layer. It exists
// because the tool interface returns a message for the model while the app method
// returns only an error: the message is what tells the model the swap is committed
// and must not be retried (task 81).
type restartUpdaterAdapter struct{ app *App }

func (r restartUpdaterAdapter) RestartAndUpdate(_ context.Context, sourceDir, version string) (string, error) {
	if err := r.app.RestartAndUpdate(sourceDir, version); err != nil {
		return "", err
	}
	return "restart scheduled: the staged build was published and the app will relaunch shortly", nil
}
