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

	var forcedNote string
	switch pending.kind {
	case "staging":
		note, err := a.restartAndUpdateExempt("", pending.version, callerSession)
		if err != nil {
			return "", err
		}
		forcedNote = note
	case "installed":
		note, err := a.switchToVersionExempt(pending.version, callerSession)
		if err != nil {
			return "", err
		}
		forcedNote = note
	default:
		return "", fmt.Errorf("restart: unknown staged target kind %q", pending.kind)
	}
	// After the swap is committed: mark the calling session for auto-resume, so
	// the fresh process continues the work (task 254). After — not before — so
	// a failed swap never leaves a stale marker that would resume the session
	// on some later unrelated restart. (Sessions the task-450 grace window had
	// to cancel are staged separately, inside clearRestartPath — "whoever we
	// interrupted, we resume".)
	//
	// 1545 anti-silent-loss: when the caller itself was NOT staged (attended
	// session under the goal_autopilot dial, or the dial off), the restart
	// still ends this very turn — the model must carry that fact back instead
	// of assuming an automatic continuation.
	callerStaged := a.stageAutonomousUpdateResume(callerSession)
	msg := "restart scheduled: the version swap is committed and the app will relaunch shortly — do not retry"
	if forcedNote != "" {
		// Task 450 acceptance 4: a forced pass through someone else's work is
		// part of the result the model sees, not a silent side effect.
		msg += "\n" + forcedNote
	}
	if !callerStaged && strings.TrimSpace(callerSession) != "" {
		msg += "\nthis session is interrupted by the restart but NOT staged for auto-resume (" + restartUnstagedMarker + "): it will not continue by itself after the relaunch"
	}
	return msg, nil
}

// restartMarkerReasonToolRestart is the update-restart marker reason for the
// restart_update tool's plain restart (task 520) — the fourth write point next
// to publish/switch/updater. The marker is what makes the restart "planned":
// without it the next launch closes the auto-resume gate and drops the staged
// roster, and the promised continuation could never happen.
const restartMarkerReasonToolRestart = "restart"

// RestartOnly relaunches the running version WITHOUT switching (task 520): no
// staging publish, no pointer move — the same reload core as the settings
// page's RestartDesktop (restartActiveVersionExempt), with the tool-face
// additions that make the restart "restart AND continue":
//   - callerSession is exempt from the grace window's busy scope (task 254:
//     the tool runs inside the very turn the restart ends) and gets the longer
//     transcript-flush grace;
//   - the update-restart marker is written, so the fresh process opens the
//     auto-resume gate (a plain RestartDesktop deliberately writes none);
//   - the caller is staged for auto-resume exactly like execute
//     (stageAutonomousUpdateResume), honoring the task-254 dial.
//
// The install is untouched, so — like RestartDesktop — this does not require
// the restart-and-update experiment; the tool itself only exists behind
// experimental_autonomous_update. Returns once the relaunch is committed; a
// success must never be retried (that would be another restart).
func (c autonomousUpdateController) RestartOnly(_ context.Context, callerSession string) (string, error) {
	a := c.app
	if a == nil {
		return "", fmt.Errorf("restart: no app")
	}
	report, err := a.restartActiveVersionExempt(callerSession, restartMarkerReasonToolRestart)
	if err != nil {
		return "", err
	}
	// After the relaunch is committed: stage the calling session for
	// auto-resume so the fresh process continues the work — the same 1545
	// anti-silent-loss shape as execute (a declined staging must be named on
	// the tool text, never silent).
	callerStaged := a.stageAutonomousUpdateResume(callerSession)
	msg := "restart scheduled: the app relaunches shortly on the CURRENT version — nothing was switched or published — do not retry"
	if note := report.forcedNote(); note != "" {
		// Task 450 acceptance 4: a forced pass through someone else's work is
		// part of the result the model sees, not a silent side effect.
		msg += "\n" + note
	}
	if !callerStaged && strings.TrimSpace(callerSession) != "" {
		msg += "\nthis session is interrupted by the restart but NOT staged for auto-resume (" + restartUnstagedMarker + "): it will not continue by itself after the relaunch"
	}
	return msg, nil
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
