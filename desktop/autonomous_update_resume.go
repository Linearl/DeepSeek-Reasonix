package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/secrets"
)

// Task 254: the auto-resume half of the autonomous update.
//
// The task-49-A2 mechanism already resumes an unattended run whose GOAL was
// running when the process died (the goal sidecar carries the "working" mark).
// An autopilot session executing restart_update has no such sidecar to lean
// on, so execute writes an explicit marker naming the calling session; after
// the restart, the restored tab is told to continue. The marker is consumed on
// first use — a later ordinary restart does not resume anything.
//
// Both paths submit through SubmitToTab's normal admission, so a tab that is
// somehow already running refuses the second prompt instead of queueing it.

// autonomousUpdateResumePrompt is what a session resumed after an autonomous
// update is told. Like autopilotResumePrompt it states the fact and the
// expectation without inventing work.
const autonomousUpdateResumePrompt = "The app updated and restarted itself while this autopilot session was working. Continue from where it stopped; do not restate the plan or wait for input."

// autonomousUpdateResumeFile lives under the user state dir; one entry per
// session that asked for an update-driven restart and has not been resumed yet.
type autonomousUpdateResumeFile struct {
	Sessions []autonomousUpdateResumeEntry `json:"sessions"`
}

type autonomousUpdateResumeEntry struct {
	Path     string `json:"path"`
	StagedAt int64  `json:"stagedAt"`
}

// autonomousUpdateResumePath returns the marker file's location.
func autonomousUpdateResumePath() string {
	return filepath.Join(config.MemoryUserDir(), "autonomous-update-resume.json")
}

// stageAutonomousUpdateResume records sessions for auto-resume after the
// restart, scoped by the autonomous_update_resume dial (task 254):
//   - "off": stage nothing — the update lands, nothing continues by itself.
//   - "goal_autopilot" (default): stage the calling session when it runs on
//     autopilot. Goal-driven sessions are covered by the task-49 sidecar path.
//   - "all": stage the caller (any mode) plus every other session with active
//     work, so a multi-session batch survives the restart intact.
//
// Returns whether the caller was staged; a false return means the caller's own
// turn is interrupted by this restart without a resume marker — the 1545
// anti-silent-loss face names it on the tool text instead.
func (a *App) stageAutonomousUpdateResume(callerSession string) bool {
	callerSession = strings.TrimSpace(callerSession)
	if callerSession == "" {
		return false
	}
	mode := a.autonomousUpdateResumeMode()
	if mode == "off" {
		slog.Info("restart: auto-resume off; not staging", "session", callerSession)
		return false
	}
	if mode != "all" && !a.sessionRunsAutopilot(callerSession) {
		slog.Info("restart: not staging auto-resume; session is not on autopilot", "session", callerSession)
		return false
	}
	entries := []autonomousUpdateResumeEntry{{Path: callerSession, StagedAt: time.Now().Unix()}}
	if mode == "all" {
		seen := map[string]bool{callerSession: true}
		for _, path := range a.sessionsWithActiveWork() {
			if !seen[path] {
				seen[path] = true
				entries = append(entries, autonomousUpdateResumeEntry{Path: path, StagedAt: time.Now().Unix()})
			}
		}
	}
	state := readAutonomousUpdateResumeFile()
	for _, entry := range entries {
		replaced := false
		for i := range state.Sessions {
			if state.Sessions[i].Path == entry.Path {
				state.Sessions[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			state.Sessions = append(state.Sessions, entry)
		}
	}
	if err := writeAutonomousUpdateResumeFile(state); err != nil {
		slog.Error("restart: staging auto-resume failed", "session", callerSession, "err", err)
		return false
	}
	slog.Info("restart: auto-resume staged", "mode", mode, "sessions", len(entries))
	return true
}

// autonomousUpdateResumeMode reads the dial live: execute applies it when it
// fires and the fresh process applies it when it boots, so no restart is
// needed for the dial itself (unlike the tool-registration switch).
func (a *App) autonomousUpdateResumeMode() string {
	cfg, err := config.Load()
	if err != nil {
		return "goal_autopilot"
	}
	return cfg.AutonomousUpdateResumeMode()
}

// sessionsWithActiveWork returns the session paths of every tab currently
// running a turn, holding a pending prompt, or owning background jobs — the
// set "all" stages for resume. The caller's own session is included; the
// stage path deduplicates.
func (a *App) sessionsWithActiveWork() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.tabs))
	for _, tab := range a.tabs {
		if tab == nil || tab.Ctrl == nil || !tab.hasActiveRuntimeWork() {
			continue
		}
		if sp := tab.Ctrl.SessionPath(); strings.TrimSpace(sp) != "" {
			out = append(out, sp)
		}
	}
	return out
}

// stageInterruptedByRestart records a session THIS restart path cancelled
// (task 450, plan A): "whoever we interrupted, we resume". Unlike
// stageAutonomousUpdateResume the autopilot check does not apply — the
// interruption was our own doing, not a resume-policy decision, so the entry
// rides into the same task-254 roster regardless of the session's mode. Only
// the dial's explicit "off" opt-out is honored: at off the restore gate
// (tabs.go) skips every resume path, so a staged entry could only resurface on
// some much later restart with the dial flipped — staging then would be a
// deferred surprise resume, not a repair. Returns whether the entry was
// staged (a false return feeds the 1545 unstaged face).
func (a *App) stageInterruptedByRestart(sessionPath string) bool {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return false
	}
	if a.autonomousUpdateResumeMode() == "off" {
		slog.Info("restart: auto-resume off; interrupted session not staged", "session", sessionPath)
		return false
	}
	state := readAutonomousUpdateResumeFile()
	replaced := false
	for i := range state.Sessions {
		if state.Sessions[i].Path == sessionPath {
			state.Sessions[i].StagedAt = time.Now().Unix()
			replaced = true
			break
		}
	}
	if !replaced {
		state.Sessions = append(state.Sessions, autonomousUpdateResumeEntry{Path: sessionPath, StagedAt: time.Now().Unix()})
	}
	if err := writeAutonomousUpdateResumeFile(state); err != nil {
		slog.Error("restart: staging interrupted-session resume failed", "session", sessionPath, "err", err)
		return false
	}
	slog.Info("restart: interrupted session staged for auto-resume", "session", sessionPath)
	return true
}

// restartResumeSubmit is the submit seam for the resume chain, mirroring the
// restart-family process seams (restartQuit etc.): tests record the continue
// prompt instead of driving a wails submission. Assigned in init — a package-
// level literal here makes the var-initialization dependency analysis walk the
// whole SubmitToTab call graph, which closes a cycle through the restore path.
var restartResumeSubmit func(a *App, tabID, prompt string) error

func init() {
	restartResumeSubmit = func(a *App, tabID, prompt string) error {
		return a.SubmitToTab(tabID, prompt)
	}
}

// restartFenceSettler is the narrow control surface for the task-435 fence
// handoff: the roster-resumed session's leftover effect records are settled
// host-side so the review panel does not light up for a planned restart
// interruption. Type-asserted, not a SessionAPI member — a fake or an older
// controller without the capability simply keeps its fence.
type restartFenceSettler interface {
	SettleRestartInterruptedEffects() int
}

// settleRestartFenceForTab clears the tab session's pending effect records via
// SettleRestartInterruptedEffects (task 435). Called from the roster-consume
// restore point BEFORE the continue prompt is submitted, so the panel's first
// post-ready probe already sees an empty pending set — the panel must not
// flash for an interruption this very chain owns.
func (a *App) settleRestartFenceForTab(tab *WorkspaceTab) int {
	if tab == nil || tab.Ctrl == nil {
		return 0
	}
	settler, ok := tab.Ctrl.(restartFenceSettler)
	if !ok {
		return 0
	}
	settled := settler.SettleRestartInterruptedEffects()
	if settled > 0 {
		slog.Info("desktop: restart-interrupted session resumed; pending effects settled without the review panel", "tab", tab.ID, "settled", settled)
	}
	return settled
}

// maybeResumeAutonomousUpdateTab continues a session this process restarted via
// the restart_update tool (task 254). Called from the same restore point as
// maybeResumeAutopilotTab: the tab is live, so the normal admission path can
// accept or refuse the submit. The consumed entry is dropped even when the
// submit fails — a session that fails to resume here should not resurrect on
// some later unrelated restart.
//
// Task 435 (the fence half of 谁断谁续): consuming the entry also settles the
// session's pending effect records (settleRestartFenceForTab) — the roster
// entry is the marker that separates a planned restart interruption from a
// genuine crash, so only roster sessions skip the review panel.
func (a *App) maybeResumeAutonomousUpdateTab(tab *WorkspaceTab) {
	if a == nil || tab == nil {
		return
	}
	a.mu.RLock()
	ready := tab.Ready && !tab.removed
	sessionPath := ""
	if ready {
		sessionPath = tab.currentSessionPath()
	}
	a.mu.RUnlock()
	if !ready || sessionPath == "" {
		return
	}
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) == 0 {
		return
	}
	matched := false
	remaining := state.Sessions[:0]
	for _, entry := range state.Sessions {
		if entry.Path == sessionPath {
			matched = true
			continue
		}
		remaining = append(remaining, entry)
	}
	if !matched {
		return
	}
	_ = writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: remaining})
	// Task 263 fix 2: the auto-resume decides "continue" for this session, so
	// it also clears the recovery pause (and its banner data) here — otherwise
	// the ForcePause notice and the automatic resume fight over the same
	// decision: the user sees "review before resuming" while the resume is
	// already on its way. One channel owns the choice; SetPaused(false) also
	// zeroes Recovered/RecoveredN, which is what the banner renders.
	// Task 300: passive form — clearing the pause must not also dispatch the
	// recovered items (SetInboxPaused unpauses WITH maybeDispatchInbox), or the
	// leftover guidance content replays into the conversation on its own. True
	// pending work stays paused-for-review reachable via /queue; the notice
	// (the "tell") is already gone because we own the continue decision.
	if tab.Ctrl != nil {
		if err := tab.Ctrl.SetInboxPausedPassive(false); err != nil {
			slog.Debug("desktop: clearing recovery pause for auto-resume", "tab", tab.ID, "err", err)
		}
	}
	// Task 435: settle before the submit lands, so the panel's first probe sees
	// an empty pending set (不弹 fence). If the submit is then refused the
	// session idles without a fence — logged below by the submit seam's caller
	// at Warn under the 未续跑 marker (audit-2 判词③: observation only, no
	// retry — 263 ruled a refused resume must not resurrect); the record facts
	// survive in the transcript for manual follow-up.
	a.settleRestartFenceForTab(tab)
	id := tab.ID
	go func() {
		if err := restartResumeSubmit(a, id, autonomousUpdateResumePrompt); err != nil {
			// codeql[go/clear-text-logging] the flagged chain only carries the
			// provider env-var NAME from config validation errors, never the
			// key value; RedactError also strips any provider-echoed key text.
			slog.Warn("desktop: autonomous-update resume skipped ("+restartResumeSkippedMarker+"; review the session manually)", "tab", id, "err", secrets.RedactError(err))
			return
		}
		slog.Info("desktop: autonomous-update run resumed", "tab", id)
	}()
}

// sessionRunsAutopilot reports whether the tab bound to sessionPath runs on
// autopilot. The live tab flag is the same one restore reads, so the marker
// decision and the resumed runtime agree.
func (a *App) sessionRunsAutopilot(callerSession string) bool {
	key := sessionRuntimeKey(callerSession)
	if key == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.tabs {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		if sp := tab.Ctrl.SessionPath(); sp != "" && sessionRuntimeKey(sp) == key {
			return tab.autopilot
		}
	}
	// A tool call always comes from a live tab, so reaching this point means
	// the caller's tab was not found; do not stage a resume for an unknown
	// session.
	return false
}

func readAutonomousUpdateResumeFile() autonomousUpdateResumeFile {
	state := autonomousUpdateResumeFile{}
	data, err := os.ReadFile(autonomousUpdateResumePath())
	if err != nil {
		return state
	}
	_ = json.Unmarshal(data, &state)
	return state
}

func writeAutonomousUpdateResumeFile(state autonomousUpdateResumeFile) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(autonomousUpdateResumePath()), 0o755); err != nil {
		return err
	}
	tmp := autonomousUpdateResumePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, autonomousUpdateResumePath())
}
