package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
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
func (a *App) stageAutonomousUpdateResume(callerSession string) {
	callerSession = strings.TrimSpace(callerSession)
	if callerSession == "" {
		return
	}
	mode := a.autonomousUpdateResumeMode()
	if mode == "off" {
		slog.Info("restart: auto-resume off; not staging", "session", callerSession)
		return
	}
	if mode != "all" && !a.sessionRunsAutopilot(callerSession) {
		slog.Info("restart: not staging auto-resume; session is not on autopilot", "session", callerSession)
		return
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
		return
	}
	slog.Info("restart: auto-resume staged", "mode", mode, "sessions", len(entries))
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

// maybeResumeAutonomousUpdateTab continues a session this process restarted via
// the restart_update tool (task 254). Called from the same restore point as
// maybeResumeAutopilotTab: the tab is live, so the normal admission path can
// accept or refuse the submit. The consumed entry is dropped even when the
// submit fails — a session that fails to resume here should not resurrect on
// some later unrelated restart.
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
	if tab.Ctrl != nil {
		if err := tab.Ctrl.SetInboxPaused(false); err != nil {
			slog.Debug("desktop: clearing recovery pause for auto-resume", "tab", tab.ID, "err", err)
		}
	}
	id := tab.ID
	go func() {
		if err := a.SubmitToTab(id, autonomousUpdateResumePrompt); err != nil {
			slog.Debug("desktop: autonomous-update resume skipped", "tab", id, "err", err)
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
