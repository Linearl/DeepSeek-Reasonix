package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	if version == "" {
		return fmt.Errorf("restart: version is required")
	}
	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		return fmt.Errorf("restart: source directory is required")
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

	if err := startDetachedLauncher(filepath.Join(installRoot, launcherName)); err != nil {
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
