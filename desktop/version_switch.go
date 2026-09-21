package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/installlayout"
)

// Version switching between already-published versions (task 210).
//
// Task 81's RestartAndUpdate publishes a staged build as a NEW version; this
// file covers the other half of the versions/ mechanism: listing what is
// already installed and moving current.json onto an existing version tree.
// Nothing is copied — the launcher already starts whatever current.json points
// at, and each versions/<ver>/ tree carries its own bundled payload, so a
// switch touches exactly one file (the pointer). User-level state, including
// the user's own skills under the state directory, is version-independent and
// is never overwritten here (install-overwrite rules in FORK.md #6 stay
// intact: bundled skills ship inside version trees, user-iterated skills live
// outside them).

// InstalledVersion is one entry of the version picker. Version is the
// directory name and doubles as the activeVersion string (see
// installlayout.ValidateVersionName). ModTimeUnix is the version tree's last
// modification time, surfaced as a human hint for "when this was published".
type InstalledVersion struct {
	Version     string `json:"version"`
	Active      bool   `json:"active"`
	ModTimeUnix int64  `json:"modTimeUnix"`
}

// relaunchHooks let tests intercept the process-lifecycle side effects
// (spawning the launcher, quitting this app, deriving the install root)
// without faking processes or install layouts.
var (
	versionSwitchStartLauncher = startDetachedLauncher
	versionSwitchQuit          = func(a *App) { a.quitApp() }
	// versionSwitchInstallRoot lets tests point the switcher at a scratch
	// install root instead of deriving one from the test binary's path.
	versionSwitchInstallRoot = resolveVersionedInstallRoot
)

// resolveVersionedInstallRoot returns the install root for the running binary
// with task-81 wording for the not-a-versioned-install case, so both entry
// points fail the same way.
func resolveVersionedInstallRoot() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("restart: locate executable: %w", err)
	}
	installRoot, err := installlayout.ResolveInstallRoot(executable)
	if err != nil || installRoot == "" {
		return "", fmt.Errorf("restart: this build is not a versioned install, so there are no versions to list: %w", err)
	}
	return installRoot, nil
}

// ListInstalledVersions returns the published version trees under
// versions/, newest-name first. Directory names sort chronologically here
// because fork builds carry a -YYYYMMDD-HHMM suffix (and plain vX.Y.Z names
// sort before their suffixed refreshes), which is exactly the order a
// version picker wants. Staging directories and anything that is not a valid
// version name are skipped: the picker only offers versions the launcher can
// actually boot.
func (a *App) ListInstalledVersions() ([]InstalledVersion, error) {
	installRoot, err := versionSwitchInstallRoot()
	if err != nil {
		return nil, err
	}
	active := ""
	if ptr, ptrErr := installlayout.ReadCurrent(installRoot); ptrErr == nil {
		active = ptr.ActiveVersion
	}
	entries, err := os.ReadDir(filepath.Join(installRoot, installlayout.VersionsDirName))
	if err != nil {
		return nil, fmt.Errorf("restart: read versions directory: %w", err)
	}
	out := make([]InstalledVersion, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := installlayout.ValidateVersionName(entry.Name()); err != nil {
			continue
		}
		info, statErr := entry.Info()
		var mod int64
		if statErr == nil {
			mod = info.ModTime().Unix()
		}
		out = append(out, InstalledVersion{Version: entry.Name(), Active: entry.Name() == active, ModTimeUnix: mod})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

// SwitchToVersion moves current.json onto an existing version tree and
// relaunches through the launcher. Guard order mirrors RestartAndUpdate:
// opt-in experiment first, then busy state, then argument validation, so a
// misdirected call can never become a version swap while the feature is off
// or a turn is running (task 81 guard semantics preserved).
func (a *App) SwitchToVersion(version string) error {
	if a == nil {
		return fmt.Errorf("restart: no app")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("restart: version is required; call ListInstalledVersions for the available names")
	}
	// Opt-in only, same as task 81: switching the active version is the same
	// destructive class as publishing one.
	if cfg, cfgErr := config.Load(); cfgErr != nil || !cfg.Desktop.ExperimentalRestartUpdate {
		return fmt.Errorf("restart: the restart-and-update experiment is off; enable experimental_restart_update in the desktop settings")
	}
	// Same busy guard as RestartDesktop: a running turn owns a session write
	// and may hold an approval or ask card; restarting through it strands both.
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

	installRoot, err := versionSwitchInstallRoot()
	if err != nil {
		return err
	}
	if err := installlayout.ValidateVersionName(version); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	current, err := installlayout.ReadCurrent(installRoot)
	if err != nil {
		return fmt.Errorf("restart: read current pointer: %w", err)
	}
	if current.ActiveVersion == version {
		return fmt.Errorf("restart: %s is already the active version; nothing to switch", version)
	}
	versionDir := filepath.Join(installRoot, installlayout.VersionDirRelative(version))
	if info, statErr := os.Stat(filepath.Join(versionDir, installlayout.DesktopBinaryName())); statErr != nil || info.IsDir() {
		return fmt.Errorf("restart: %s has no %s; refusing to point the install at an incomplete version tree", version, installlayout.DesktopBinaryName())
	}

	// The one write: move the pointer. WriteCurrent re-validates the version
	// name and active dir, and its atomic replace is the layout commit point.
	if err := installlayout.WriteCurrent(installRoot, installlayout.CurrentPointer{
		ActiveVersion: version,
		ActiveDir:     installlayout.VersionDirRelative(version),
	}); err != nil {
		return fmt.Errorf("restart: switch pointer: %w", err)
	}
	slog.Info("restart: switching active version", "from", current.ActiveVersion, "to", version, "installRoot", installRoot)

	launcherPath := filepath.Join(installRoot, installlayout.LauncherBinaryName())
	if info, statErr := os.Lstat(launcherPath); statErr != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("restart: the launcher %s is missing from %s (pointer already moved; run the launcher manually to boot %s)", installlayout.LauncherBinaryName(), installRoot, version)
	}
	if err := versionSwitchStartLauncher(launcherPath, os.Getpid()); err != nil {
		slog.Error("restart: launcher start failed after version switch", "version", version, "err", err)
		return fmt.Errorf("restart: start launcher: %w", err)
	}
	// Answer first, exit after — same contract as the other restart paths.
	go func() {
		time.Sleep(750 * time.Millisecond)
		versionSwitchQuit(a)
	}()
	return nil
}
