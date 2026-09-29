package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/installlayout"
	"reasonix/internal/tool"
)

// Task 254: the host side of the restart_update tool. Listing and target
// staging are read-only or pointer-free; execution reuses the task-81/210
// swap paths (RestartAndUpdate / SwitchToVersion) with one addition — the
// tool call runs inside a turn, so the caller's own session is exempt from
// the busy guard. That turn is the one the restart is supposed to end; every
// OTHER tab's active work still refuses the swap.
//
// The shutdown grace is longer on this path (3s vs 750ms): the calling turn
// is still streaming when execute returns, and the transcript tail needs
// those seconds to reach disk before the process exits.

// autonomousUpdateQuitGrace is how long the exempt path waits before quitting,
// giving the calling turn time to finish its reply and flush the transcript.
const autonomousUpdateQuitGrace = 3 * time.Second

// autonomousUpdateController implements tool.AutonomousUpdateController on top
// of the App. Stateless beyond the pending target, which lives on the App
// behind its own mutex (never the tab lock — set_target and execute are
// separate tool calls).
type autonomousUpdateController struct {
	app *App
}

// newAutonomousUpdateController is the boot-facing constructor.
func newAutonomousUpdateController(a *App) tool.AutonomousUpdateController {
	return autonomousUpdateController{app: a}
}

// pendingUpdateTarget is what set_target staged for execute.
type pendingUpdateTarget struct {
	kind    string // "staging" | "installed"
	version string // normalized version label (staging: from version.txt)
}

// ListVersions reports installed version trees plus the staged build, each
// with a health bit. A tree without the CLI binary is marked unhealthy:
// switching to it bricks servepool with 503s (task 248).
func (c autonomousUpdateController) ListVersions(_ context.Context) ([]tool.VersionHealth, string, string, error) {
	a := c.app
	if a == nil {
		return nil, "", "", fmt.Errorf("restart: no app")
	}
	installed, err := a.ListInstalledVersions()
	if err != nil {
		return nil, "", "", err
	}
	installRoot, err := versionSwitchInstallRoot()
	if err != nil {
		return nil, "", "", err
	}
	active := ""
	if ptr, ptrErr := installlayout.ReadCurrent(installRoot); ptrErr == nil {
		active = ptr.ActiveVersion
	}
	out := make([]tool.VersionHealth, 0, len(installed)+1)
	for _, v := range installed {
		out = append(out, tool.VersionHealth{
			Version:     v.Version,
			Active:      v.Active,
			Healthy:     versionTreeHealthy(installRoot, v.Version),
			ModTimeUnix: v.ModTimeUnix,
		})
	}
	stagingVersion := ""
	if label, ok := readStagingVersion(installRoot); ok {
		stagingVersion = label
		out = append(out, tool.VersionHealth{
			Version: label,
			Staging: true,
			Healthy: stagingHealthy(installRoot),
		})
	}
	return out, active, stagingVersion, nil
}

// SetTarget validates and stages an update target. "staging" publishes the
// staged build; an installed version name rolls back to that tree. The active
// version is refused (a no-op swap).
func (c autonomousUpdateController) SetTarget(_ context.Context, target string) (string, error) {
	a := c.app
	if a == nil {
		return "", fmt.Errorf("restart: no app")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("restart: target is required; call list_versions first")
	}
	installRoot, err := versionSwitchInstallRoot()
	if err != nil {
		return "", err
	}
	current := ""
	if ptr, ptrErr := installlayout.ReadCurrent(installRoot); ptrErr == nil {
		current = ptr.ActiveVersion
	}

	var pending pendingUpdateTarget
	var description string
	if strings.EqualFold(target, "staging") {
		// Task 381: honor the configured staging directory (empty = default).
		stagingDir, stagingErr := stagingRoot()
		if stagingErr != nil {
			return "", stagingErr
		}
		label, ok := readStagingVersionAt(stagingDir)
		if !ok {
			return "", fmt.Errorf("restart: no staged build: %s is missing or unreadable (rebuild to re-stage)", filepath.Join(stagingDir, "version.txt"))
		}
		if !stagingHealthyAt(stagingDir) {
			return "", fmt.Errorf("restart: the staged build in %s is missing the desktop or CLI binary; rebuild before publishing", stagingDir)
		}
		pending = pendingUpdateTarget{kind: "staging", version: label}
		description = fmt.Sprintf("target staged: publish the staged build %s as a new version and restart", label)
	} else {
		version := target
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		if err := installlayout.ValidateVersionName(version); err != nil {
			return "", fmt.Errorf("restart: %w", err)
		}
		if current != "" && version == current {
			return "", fmt.Errorf("restart: %s is already the active version; nothing to switch", version)
		}
		if info, statErr := os.Stat(filepath.Join(installRoot, installlayout.VersionDirRelative(version), installlayout.DesktopBinaryName())); statErr != nil || info.IsDir() {
			return "", fmt.Errorf("restart: %s is not an installed version; pick one from list_versions", version)
		}
		if !versionTreeHealthy(installRoot, version) {
			return "", fmt.Errorf("restart: %s is missing the desktop or CLI binary (unhealthy in list_versions); refusing to stage it", version)
		}
		pending = pendingUpdateTarget{kind: "installed", version: version}
		description = fmt.Sprintf("target staged: roll back to installed version %s and restart", version)
	}

	a.autonomousMu.Lock()
	a.autonomousPending = pending
	a.autonomousMu.Unlock()
	slog.Info("restart: autonomous update target staged", "kind", pending.kind, "version", pending.version)
	return description, nil
}

// ExecuteTarget performs the staged target. callerSession is the session whose
// turn issued the tool call; its tab is exempt from the busy guard because the
// restart is meant to end that very turn (auto-resume marks it beforehand).
// Returns once the swap is committed — the restart follows and a success must
// never be retried.
func (c autonomousUpdateController) ExecuteTarget(_ context.Context, callerSession string) (string, error) {
	a := c.app
	if a == nil {
		return "", fmt.Errorf("restart: no app")
	}
	a.autonomousMu.Lock()
	pending := a.autonomousPending
	a.autonomousPending = pendingUpdateTarget{}
	a.autonomousMu.Unlock()
	if pending.kind == "" {
		return "", fmt.Errorf("restart: no update target staged; call set_target first")
	}

	switch pending.kind {
	case "staging":
		if err := a.restartAndUpdateExempt("", pending.version, callerSession); err != nil {
			return "", err
		}
	case "installed":
		if err := a.switchToVersionExempt(pending.version, callerSession); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("restart: unknown staged target kind %q", pending.kind)
	}
	// After the swap is committed: mark the calling session for auto-resume, so
	// the fresh process continues the work (task 254). After — not before — so
	// a failed swap never leaves a stale marker that would resume the session
	// on some later unrelated restart.
	a.stageAutonomousUpdateResume(callerSession)
	return "restart scheduled: the version swap is committed and the app will relaunch shortly — do not retry", nil
}

// versionTreeHealthy reports whether versions/<version> carries the desktop and
// CLI binaries. The CLI is the servepool-critical member (task 248).
func versionTreeHealthy(installRoot, version string) bool {
	dir := filepath.Join(installRoot, installlayout.VersionDirRelative(version))
	return regularFileExists(filepath.Join(dir, installlayout.DesktopBinaryName())) &&
		regularFileExists(filepath.Join(dir, installlayout.CLIBinaryName()))
}

// stagingHealthy reports whether staging/ carries the desktop and CLI binaries.
// Task 381: the dir-level variants take the resolved staging root directly so
// the configured override flows through; the no-arg forms keep the historical
// default-root callers working.
func stagingHealthy(installRoot string) bool {
	return stagingHealthyAt(filepath.Join(installRoot, "staging"))
}

func stagingHealthyAt(dir string) bool {
	return regularFileExists(filepath.Join(dir, installlayout.DesktopBinaryName())) &&
		regularFileExists(filepath.Join(dir, installlayout.CLIBinaryName()))
}

// readStagingVersion returns the normalized staging/version.txt label.
func readStagingVersion(installRoot string) (string, bool) {
	return readStagingVersionAt(filepath.Join(installRoot, "staging"))
}

func readStagingVersionAt(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "version.txt"))
	if err != nil {
		return "", false
	}
	label := strings.TrimSpace(string(raw))
	if label == "" {
		return "", false
	}
	if !strings.HasPrefix(label, "v") {
		label = "v" + label
	}
	if err := installlayout.ValidateVersionName(label); err != nil {
		return "", false
	}
	return label, true
}

func regularFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}
