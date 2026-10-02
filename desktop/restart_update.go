package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// The forced-through note is log-only on the UI face: the button's result
	// is success/failure, and every forced step already warned with the
	// restartForcedMarker.
	_, err := a.restartAndUpdateExempt(sourceDir, version, "")
	return err
}

// restartAndUpdateExempt is RestartAndUpdate with a busy-guard exemption for
// callerSession (task 254). The restart_update tool executes inside a turn —
// the very turn the restart is meant to end — so without the exemption the
// tool would always refuse itself. Task 450 (plan A) replaced the old
// any-tab-busy refusal with the bounded grace window (clearRestartPath): the
// caller's own turn stays exempt, everyone else gets a chance to finish and
// is cancelled with a resume marker if they don't. The returned string is the
// forced-through note for the tool face ("" when the window cleared
// naturally); UI callers log it instead.
func (a *App) restartAndUpdateExempt(sourceDir, version, callerSession string) (string, error) {
	if a == nil {
		return "", fmt.Errorf("restart: no app")
	}
	version = strings.TrimSpace(version)
	sourceDir = strings.TrimSpace(sourceDir)

	// Argument completion answers first: the status-bar button sends an empty
	// version on purpose (a local build has no signed manifest), so an empty
	// version is completed from the staging payload before any guard runs.
	// Only a version that is still missing after completion is a refusal, and
	// that refusal names the staging path so the caller can tell a malformed
	// request from a disabled feature (task 81 guard order preserved).
	if sourceDir == "" {
		if dir, dirErr := stagingRoot(); dirErr == nil && dir != "" {
			sourceDir = dir
		} else if executable, execErr := os.Executable(); execErr == nil {
			if installRoot, rootErr := installlayout.ResolveInstallRoot(executable); rootErr == nil && installRoot != "" {
				sourceDir = filepath.Join(installRoot, "staging")
			}
		}
	}
	if version == "" && sourceDir != "" {
		if raw, readErr := os.ReadFile(filepath.Join(sourceDir, "version.txt")); readErr == nil {
			version = strings.TrimSpace(string(raw))
		}
	}
	if version == "" {
		return "", fmt.Errorf("restart: no version to publish: %s/version.txt is missing (rebuild to re-stage) and the caller sent none", sourceDir)
	}

	// Opt-in only (task 81): the action swaps the active install version, so neither a
	// stale UI nor a tool call may reach it while the experiment is off.
	if cfg, cfgErr := config.Load(); cfgErr != nil || !cfg.Desktop.ExperimentalRestartUpdate {
		return "", fmt.Errorf("restart: the restart-and-update experiment is off; enable experimental_restart_update in the desktop settings")
	}

	// Task 450 (plan A): the busy refusal is gone — but the grace window is
	// SIDE-EFFECTFUL (audit-2 major fix): it stops the heartbeat (no restart
	// path exists until the app relaunches) and cancels other tabs (their
	// resume markers only get consumed after a restart). Running it before the
	// validation gates meant a misdirected call — switching to the active
	// version, a plain restart on a portable build — paid those costs and then
	// failed, silently. So the window runs only AFTER every fallible check,
	// immediately before the first irreversible step (ActivateVersion): past
	// that point the swap is committed and the restart is happening, which is
	// exactly what the window's side effects presume.
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("restart: locate executable: %w", err)
	}
	installRoot, err := installlayout.ResolveInstallRoot(executable)
	if err != nil || installRoot == "" {
		return "", fmt.Errorf("restart: this build is not a versioned install, so there is no pointer to move: %w", err)
	}

	// Default staging location (task 381: the configured override wins when
	// set; the historical InstallRoot/staging convention stays the fallback).
	if sourceDir == "" {
		if dir, dirErr := stagingRoot(); dirErr == nil && dir != "" {
			sourceDir = dir
		} else {
			sourceDir = filepath.Join(installRoot, "staging")
		}
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
		return "", fmt.Errorf("restart: no version to publish: %s/version.txt is missing (rebuild to re-stage) and the caller sent none", sourceDir)
	}
	// version.txt carries the product version (1.38.3); the install layout names its
	// directories with a "v" prefix (v1.38.3 - see installlayout.ValidateVersionName),
	// and the directory name IS the activeVersion string. Normalise here so the two
	// cannot drift, and report a bad label before anything is copied.
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if validateErr := installlayout.ValidateVersionName(version); validateErr != nil {
		return "", fmt.Errorf("restart: %w", validateErr)
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
			return "", fmt.Errorf("restart: staged %s is missing from %s", member.Name, sourceDir)
		}
	}
	launcherName := installlayout.LauncherBinaryName()
	launcherPath := filepath.Join(sourceDir, launcherName)
	if info, statErr := os.Lstat(launcherPath); statErr != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("restart: staged %s is missing from %s", launcherName, sourceDir)
	}

	forced := a.clearRestartPath(callerSession)
	if err := installlayout.ActivateVersion(installlayout.ActivationRequest{
		InstallRoot:       installRoot,
		Version:           version,
		RequestID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Members:           members,
		RequiredNames:     installlayout.AllowedVersionMembers(),
		RootMembers:       []installlayout.Member{{Name: launcherName, Path: launcherPath}},
		RequiredRootNames: []string{launcherName},
	}); err != nil {
		return "", fmt.Errorf("restart: publish version: %w", err)
	}

	slog.Info("restart: publish committed; relaunching", "version", version)
	if err := restartStartLauncher(filepath.Join(installRoot, launcherName), os.Getpid()); err != nil {
		slog.Error("restart: launcher start failed after commit", "version", version, "err", err)
		return "", fmt.Errorf("restart: start launcher: %w", err)
	}

	// Answer first, exit after. The caller is a UI action or a tool call that has to
	// get a result: a restart that never returns reads as a failure, and retrying a
	// failure like this is another restart. The tool path (task 254) grants a longer
	// grace: the calling turn is still streaming and its transcript tail needs the
	// seconds to reach disk before the process exits.
	grace := 750 * time.Millisecond
	if callerSession != "" {
		grace = autonomousUpdateQuitGrace
	}
	go func() {
		time.Sleep(grace)
		restartQuit(a)
	}()
	return forced.forcedNote(), nil
}

// Restart-family knobs (task 450, plan A). Vars, not consts, so tests can
// shrink the windows; production runs on the defaults below.
var (
	// restartGraceWait is how long a restart entry waits for other tabs'
	// active work to finish on its own before cancelling it. 10s covers a
	// heartbeat's short turn with headroom (user ruling 2026-10-02: N=10,
	// a starting point — it may move to config later).
	restartGraceWait = 10 * time.Second
	// restartGracePoll is the busy-check cadence inside the grace window.
	restartGracePoll = 200 * time.Millisecond
	// restartCancelSettle bounds the post-cancel unwind wait before the swap
	// proceeds regardless — the same bound as the takeover handoff
	// (session_lease_handoff.go).
	restartCancelSettle = 3 * time.Second
	// restartCancelSettlePoll is the settle-check cadence.
	restartCancelSettlePoll = 100 * time.Millisecond
)

// restartStopHeartbeat stops the heartbeat engine, indirected for tests the
// same way as versionSwitchStartLauncher (version_switch.go). Stopping it
// first is what keeps new heartbeat turns from igniting during the grace
// window or between the window and the pointer swap — the same call the
// shutdown body makes (shutdown.go). The default body lives in
// stopHeartbeatEngine so tests can exercise the real hook while the var is
// stubbed.
var restartStopHeartbeat = stopHeartbeatEngine

func stopHeartbeatEngine(a *App) {
	if a != nil && a.heartbeat != nil {
		a.heartbeat.Stop()
	}
}

// Launcher/quit seams for the publish path, mirroring versionSwitchStartLauncher
// and versionSwitchQuit so tests can intercept the process side effects the same
// way on both restart faces. restartResolveInstallRoot exists for the same
// reason on the plain-restart face: the audit-2 fix gates the grace window on
// launchability, so tests need to point it at a scratch install root.
var (
	restartStartLauncher      = startDetachedLauncher
	restartQuit               = func(a *App) { a.quitApp() }
	restartResolveInstallRoot = installlayout.ResolveInstallRoot
)

// restartForcedMarker is the greppable token every forced-path log line and
// tool-facing note carries (acceptance 4: the forced-through fact must be
// visible on both faces, not buried in behavior).
const restartForcedMarker = "超时强制"

// restartUnstagedMarker is the greppable token for the 1545 anti-silent-loss
// face: a session this restart interrupts but does NOT stage for auto-resume
// (dial off, or an attended caller under the goal_autopilot dial) would
// otherwise reappear after the relaunch as a fenced, nobody-resumes-it
// transcript with no trace of why. Every unstaged session is named on the log
// and the tool-facing note under this marker.
const restartUnstagedMarker = "未入册"

// restartResumeSkippedMarker is the greppable token for the rostered session
// whose post-relaunch resume submit was REFUSED (task 435 note 3, audit-2
// 判词③): it settles out of the fence yet never resumes, so it would idle
// silently — the Warn face names it here, matching the 未入册 family's
// greppable-token convention. Observation only; no retry (task 263: a refused
// resume must not resurrect).
const restartResumeSkippedMarker = "未续跑"

// restartWindowReport summarizes what the grace window did: natural = the
// other tabs settled (or none were busy); cancelled = sessions this path
// cancelled (each staged for auto-resume); forcedPrompt = sessions pushed
// through while holding an unanswered prompt (never cancelled — the prompt is
// the user's decision; the shutdown snapshot closes the card out as cancelled);
// unstaged = interrupted-by-this-restart sessions that did NOT get a resume
// marker (1545: named on every face so they cannot be silently lost).
type restartWindowReport struct {
	natural      bool
	cancelled    []string
	forcedPrompt []string
	unstaged     []string
}

// forcedNote renders the report for the tool face; "" when nothing was forced.
func (r restartWindowReport) forcedNote() string {
	if len(r.cancelled) == 0 && len(r.forcedPrompt) == 0 && len(r.unstaged) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("forced through after the grace window (" + restartForcedMarker + ")")
	if len(r.cancelled) > 0 {
		fmt.Fprintf(&b, "; cancelled and staged for auto-resume: %s", strings.Join(r.cancelled, ", "))
	}
	if len(r.forcedPrompt) > 0 {
		fmt.Fprintf(&b, "; went through past an unanswered prompt (not cancelled): %s", strings.Join(r.forcedPrompt, ", "))
	}
	if len(r.unstaged) > 0 {
		fmt.Fprintf(&b, "; interrupted but NOT staged for auto-resume (%s, review manually after the relaunch): %s", restartUnstagedMarker, strings.Join(r.unstaged, ", "))
	}
	return b.String()
}

// clearRestartPath makes room for the restart family's swap (task 450, plan A)
// and replaces the old any-tab-busy refusal:
//
//  1. stop the heartbeat engine first — no new heartbeat turn may ignite
//     during the window or between the window and the swap;
//  2. grace-wait restartGraceWait for other tabs' active work to finish on
//     its own — a heartbeat's short turn clears for free, zero interruptions;
//  3. whatever is still busy is cancelled — except tabs holding a pending
//     user prompt: Cancel clears approvals (internal/control/cancel.go), and
//     an unanswered approval/ask card is the user's decision point, so the
//     restart does not veto it; such a tab rides through on the shutdown
//     snapshot instead;
//  4. every session this path cancelled is staged for auto-resume
//     unconditionally — "whoever we interrupted, we resume" (see
//     stageInterruptedByRestart).
//
// callerSession (a session path) stays exempt from every step: the
// restart_update tool runs inside that session's own turn, which is the very
// turn the restart ends (task 254 semantics preserved).
func (a *App) clearRestartPath(callerSession string) restartWindowReport {
	restartStopHeartbeat(a)

	deadline := time.Now().Add(restartGraceWait)
	for {
		if len(a.busyRestartTabs(callerSession)) == 0 {
			return restartWindowReport{natural: true}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(restartGracePoll)
	}

	report := restartWindowReport{}
	var toCancel []*WorkspaceTab
	for _, tab := range a.busyRestartTabs(callerSession) {
		sp := restartTabSessionPath(tab)
		if tab.Ctrl != nil && tab.Ctrl.RuntimeStatus().PendingPrompt {
			report.forcedPrompt = append(report.forcedPrompt, sp)
			slog.Warn("restart: grace expired; going through past a pending prompt ("+restartForcedMarker+")", "session", sp)
			continue
		}
		toCancel = append(toCancel, tab)
	}
	for _, tab := range toCancel {
		sp := restartTabSessionPath(tab)
		report.cancelled = append(report.cancelled, sp)
		slog.Warn("restart: grace expired; cancelling the turn ("+restartForcedMarker+")", "session", sp)
		if tab.Ctrl != nil {
			tab.Ctrl.Cancel()
		}
	}
	// Bounded unwind wait (takeover handoff shape): give the cancelled turns a
	// moment to release their session writes before the swap touches the
	// install. Whatever is still unwinding when the bound hits is covered by
	// the shutdown snapshot + interrupted-turn recovery, same as any quit.
	settleDeadline := time.Now().Add(restartCancelSettle)
	for len(toCancel) > 0 && time.Now().Before(settleDeadline) {
		stillBusy := false
		for _, tab := range toCancel {
			if tab.hasActiveRuntimeWork() {
				stillBusy = true
				break
			}
		}
		if !stillBusy {
			break
		}
		time.Sleep(restartCancelSettlePoll)
	}
	for _, sp := range report.cancelled {
		// 1545 anti-silent-loss: a cancelled session whose staging declined
		// (dial off) is interrupted without a resume marker — name it here and
		// on the note, so the post-relaunch fence is never unexplained.
		if !a.stageInterruptedByRestart(sp) {
			report.unstaged = append(report.unstaged, sp)
			slog.Warn("restart: session interrupted but NOT staged for auto-resume ("+restartUnstagedMarker+"; review manually after the relaunch)", "session", sp)
		}
	}
	return report
}

// restartTabSessionPath is the identity a report row carries: the tab's live
// session path, "" when the tab has none yet.
func restartTabSessionPath(tab *WorkspaceTab) string {
	if tab == nil || tab.Ctrl == nil {
		return ""
	}
	return strings.TrimSpace(tab.Ctrl.SessionPath())
}

// restartBusyReason reports why an install swap must wait right now, or "" when
// the path is clear. callerSession (a session path) is exempt from the check:
// the restart_update tool runs inside that session's own turn, which is the
// turn the restart ends (task 254). Task 450 turned this pure query into the
// input of the grace window (clearRestartPath) — the restart entries no longer
// refuse on it; the exemption semantics are byte-for-byte what they were.
func (a *App) restartBusyReason(callerSession string) string {
	if len(a.busyRestartTabs(callerSession)) > 0 {
		return "restart: a turn is running or background jobs are active; stop them first"
	}
	return ""
}

// busyRestartTabs returns the tabs whose active work blocks the restart
// family, minus the callerSession exemption (task 254). Single source of truth
// for the guard query (restartBusyReason) and the grace window
// (clearRestartPath), so the two can never drift apart.
func (a *App) busyRestartTabs(callerSession string) []*WorkspaceTab {
	exempt := ""
	if callerSession != "" {
		exempt = sessionRuntimeKey(callerSession)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*WorkspaceTab, 0, 2)
	for _, tab := range a.tabs {
		if exempt != "" && tab.Ctrl != nil {
			if sp := tab.Ctrl.SessionPath(); sp != "" && sessionRuntimeKey(sp) == exempt {
				continue
			}
		}
		if tab.hasActiveRuntimeWork() {
			out = append(out, tab)
		}
	}
	return out
}

// startDetachedLauncher starts the launcher so that it outlives this process,
// handing it this pid via --wait-for: the launcher then starts the new desktop
// only after this process has fully exited and released the gateway port and
// session locks, mirroring the update helper's instance handoff.
func startDetachedLauncher(launcherPath string, exitingPID int) error {
	args := []string{}
	if exitingPID > 0 {
		args = append(args, "--wait-for", strconv.Itoa(exitingPID))
	}
	cmd := exec.Command(launcherPath, args...)
	cmd.Dir = filepath.Dir(launcherPath)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	configureDetachedRelaunch(cmd, relaunchCreationFlags)
	if err := cmd.Start(); err != nil {
		// Jobs that forbid breakaway fail the whole Start; retry attached to
		// the current job rather than losing the relaunch entirely.
		cmd = exec.Command(launcherPath, args...)
		cmd.Dir = filepath.Dir(launcherPath)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
		configureDetachedRelaunch(cmd, detachedRelaunchFallbackFlags)
		if retryErr := cmd.Start(); retryErr != nil {
			return err
		}
		return cmd.Process.Release()
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

// RestartDesktop relaunches the desktop without publishing a staged build, for settings
// that only take effect at boot: the session store is chosen when the v4 bridge is built
// and boot-time tools are registered once, so both stay stale until the process restarts.
// The alternative is telling the user to close and reopen the app, which loses unsaved
// window state for no benefit.
//
// Deliberately NOT gated on experimental_restart_update. That experiment guards swapping
// the active version - copying binaries in and moving the current.json pointer - which is
// the destructive half. Relaunching the binary already on disk changes nothing about the
// installation, and the settings that need it are ordinary switches rather than
// experiments, so requiring an unrelated experiment to be enabled would be backwards.
func (a *App) RestartDesktop() error {
	if a == nil {
		return fmt.Errorf("restart: no app")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restart: locate executable: %w", err)
	}
	installRoot, err := restartResolveInstallRoot(executable)
	if err != nil || installRoot == "" {
		return fmt.Errorf("restart: this build is not a versioned install, so there is no launcher to hand off to: %w", err)
	}
	launcherPath := filepath.Join(installRoot, installlayout.LauncherBinaryName())
	if info, statErr := os.Lstat(launcherPath); statErr != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("restart: the launcher %s is missing from %s", installlayout.LauncherBinaryName(), installRoot)
	}

	// Task 450 (user ruling 2026-10-02): the plain restart shares the update
	// path's grace window — one restart family, one semantics, no dual-track.
	// No caller session exists on this path, so every busy tab is inside the
	// window's scope: given the grace to finish, cancelled with a resume
	// marker if they don't, pending prompts never vetoed. Audit-2 major fix:
	// the window is side-effectful (heartbeat stop + cancels), so it runs only
	// after the launchability checks — a portable build or a missing launcher
	// must refuse without paying those costs (nothing restores them).
	report := a.clearRestartPath("")
	if note := report.forcedNote(); note != "" {
		slog.Warn("restart: plain restart " + note)
	}

	// A silent restart reads as a dead button, so log the milestone the way the
	// restart-and-update path does.
	slog.Info("restart: relaunching the active version", "installRoot", installRoot)
	if err := restartStartLauncher(launcherPath, os.Getpid()); err != nil {
		slog.Error("restart: launcher start failed", "err", err)
		return fmt.Errorf("restart: start launcher: %w", err)
	}

	// Answer first, exit after: the caller is a UI action that has to get a result.
	go func() {
		time.Sleep(750 * time.Millisecond)
		restartQuit(a)
	}()
	return nil
}
