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

// restartInterruptedToolMarker is the greppable token for the 任务461-P2 兜底②
// face: an auto-resume that would replay a turn whose last step was an
// interrupted tool call (unresolved effect records still on the session) is
// skipped instead — the 「中断的工具需要核实」 review panel stays up for manual
// review, and the skip is named here so an idling session is never silent
// (same greppable-token convention as 未入册/未续跑).
const restartInterruptedToolMarker = "中断工具不重放"

// restartInterruptedToolProbe is the narrow controller surface for the 兜底②
// check: does the session still carry unresolved tool-recovery records? The
// real Controller implements it (internal/control); a fake or an older
// controller without the capability simply never triggers the backstop —
// the pre-461 resume behavior, byte for byte.
type restartInterruptedToolProbe interface {
	HasPendingToolRecovery() bool
}

// tabHasInterruptedToolCall reports whether the tab's session ended its last
// run inside a tool call whose outcome is still unknown. Reading it through
// the narrow probe keeps the restore point working against every controller
// shape the tests already stub.
func tabHasInterruptedToolCall(tab *WorkspaceTab) bool {
	if tab == nil || tab.Ctrl == nil {
		return false
	}
	probe, ok := tab.Ctrl.(restartInterruptedToolProbe)
	return ok && probe.HasPendingToolRecovery()
}

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
		// Task 450 note2: dedupe on the runtime key, not the raw spelling —
		// two tabs (or a tab and the caller) can hold the same session under
		// different path spellings.
		seen := map[string]bool{sessionRuntimeKey(callerSession): true}
		for _, path := range a.sessionsWithActiveWork() {
			key := sessionRuntimeKey(path)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, autonomousUpdateResumeEntry{Path: path, StagedAt: time.Now().Unix()})
		}
	}
	state := readAutonomousUpdateResumeFile()
	for _, entry := range entries {
		key := sessionRuntimeKey(entry.Path)
		replaced := false
		for i := range state.Sessions {
			// Task 450 note2: fold both sides through sessionRuntimeKey so an
			// entry written under another spelling of the same session is
			// refreshed in place instead of duplicated.
			if sessionRuntimeKey(state.Sessions[i].Path) == key {
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
//
// Task 450 note2: the dedupe folds both sides through sessionRuntimeKey — the
// same session may already sit in the roster under another spelling (e.g. a
// 254-path staging) and must be refreshed, not duplicated. The stored Path
// keeps the trimmed spelling it was given; only comparisons normalize.
func (a *App) stageInterruptedByRestart(sessionPath string) bool {
	sessionPath = strings.TrimSpace(sessionPath)
	key := sessionRuntimeKey(sessionPath)
	if key == "" {
		return false
	}
	if a.autonomousUpdateResumeMode() == "off" {
		slog.Info("restart: auto-resume off; interrupted session not staged", "session", sessionPath)
		return false
	}
	state := readAutonomousUpdateResumeFile()
	replaced := false
	for i := range state.Sessions {
		if sessionRuntimeKey(state.Sessions[i].Path) == key {
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

// tl1366: the two greppable tokens of the resume-delivery chain. The 包68
// incident (2026-11-03 12:17-12:28) showed the roster resume firing and the
// submit succeeding, and the resumed turn still dying without a trace inside
// the fresh process's boot-time maintenance window — the session sat silent
// until the user typed. Both faces are named so an undelivered resume can
// never be silent again (1545 anti-silent-loss, same convention as
// 未入册/未续跑/中断工具不重放):
//   - 维护未清: the boot-time maintenance (task-920 compaction) did not
//     settle within resumeMaintenanceWaitBound; the submit goes through
//     anyway and the task-920 park path remains the backstop.
//   - 续跑落空: the submit was accepted but the transcript never grew —
//     the resumed turn neither appended its prompt nor left any other trace.
//     One bounded retry follows; a second fall-through gives up named.
const (
	resumeMaintenanceBusyMarker = "维护未清"
	resumeFallThroughMarker     = "续跑落空"
)

// Resume-delivery knobs (tl1366). Vars, not consts, so tests can shrink the
// windows; production runs on the defaults below.
var (
	// resumeMaintenanceWaitBound is how long the resume waits for the fresh
	// session's boot-time maintenance to settle before submitting. The
	// incident's boot compaction ran 2m50s before burning; five minutes
	// covers it with headroom without stalling the wake-up meaningfully
	// (the alternative — submitting immediately — is exactly the park-
	// behind-compaction shape the incident killed on).
	resumeMaintenanceWaitBound = 5 * time.Minute
	// resumeMaintenancePoll is the maintenance-settle check cadence.
	resumeMaintenancePoll = 500 * time.Millisecond
	// resumeLivenessWindow is how long a submitted resume watches for the
	// transcript to grow (the continue prompt landing in the session). A
	// living turn appends its user message within seconds of starting; a
	// window this size catches the silent-death shape without mistaking a
	// still-settling controller for a dead one (maintenance already cleared
	// before the submit, and a park — if any — drains on the settle hook).
	resumeLivenessWindow = 60 * time.Second
	// resumeLivenessPoll is the transcript-growth check cadence.
	resumeLivenessPoll = 500 * time.Millisecond
)

// restartMaintenanceProbe is the narrow control surface for the tl1366
// maintenance wait: is the fresh controller still inside a boot-time
// context-maintenance transaction (task-920 compaction)? The real Controller
// implements it (internal/control); a fake or an older controller without
// the capability simply never triggers the wait — the pre-tl1366 submit
// timing, byte for byte.
type restartMaintenanceProbe interface {
	MaintenanceInFlight() bool
}

// restartResumeLivenessProbe is the narrow control surface for the tl1366
// delivery check: a cheap transcript-length read. Growth past the submit-time
// baseline is the receipt that the continue prompt actually landed in the
// session; a fake without the capability skips the check and keeps the
// pre-tl1366 fire-and-forget shape.
type restartResumeLivenessProbe interface {
	HistoryLen() int
}

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

// plannedGuidanceSettler is the narrow control surface for the 任务840
// planned-restart read confirmation: the roster-resumed session's
// admission-proven guidance parks (RestartOrphan / SteerUnapplied whose collab
// mail was delivered) are settled host-side so the guidance shelf does not
// replay consumed messages with a retry button after an unattended update
// restart. Same type-asserted shape as restartFenceSettler — a fake or an
// older controller without the capability simply keeps its shelf rows.
type plannedGuidanceSettler interface {
	SettlePlannedRestartGuidance() int
}

// settlePlannedRestartGuidanceForTab settles the tab session's parked guidance
// rows via SettlePlannedRestartGuidance (任务840). Called from both roster
// consumption restore points BEFORE the recovery pause is cleared, so the
// shelf's first post-ready snapshot no longer carries the consumed rows.
func (a *App) settlePlannedRestartGuidanceForTab(tab *WorkspaceTab) int {
	if tab == nil || tab.Ctrl == nil {
		return 0
	}
	settler, ok := tab.Ctrl.(plannedGuidanceSettler)
	if !ok {
		return 0
	}
	settled := settler.SettlePlannedRestartGuidance()
	if settled > 0 {
		slog.Info("desktop: planned-restart session resumed; consumed guidance rows settled off the shelf", "tab", tab.ID, "settled", settled)
	}
	return settled
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
//
// Task 450 note2 (audit-2): the entry was staged from the tab's
// Ctrl.SessionPath() spelling before the restart, while this consumption side
// reads tab.currentSessionPath() — which inside the two-phase recovery-handoff
// window legitimately returns the lease-backed tab form, a different spelling
// of the SAME session. A raw == comparison would miss it and silently drop the
// resume, so both sides fold through sessionRuntimeKey before comparing.
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
	sessionKey := sessionRuntimeKey(sessionPath)
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) == 0 {
		return
	}
	matched := false
	remaining := state.Sessions[:0]
	for _, entry := range state.Sessions {
		if sessionRuntimeKey(entry.Path) == sessionKey {
			matched = true
			continue
		}
		remaining = append(remaining, entry)
	}
	if !matched {
		return
	}
	_ = writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: remaining})
	// 任务461-P2 兜底②: the session's last step was an interrupted tool call
	// (unresolved effect records) — do NOT replay "continue" into it, or a
	// session stuck on a hung tool re-invokes the same tool and re-blocks. The
	// roster entry stays consumed, the fence is NOT settled (the 「中断的工具需要
	// 核实」 review panel is exactly the manual-review face this case wants),
	// and the recovery pause stays up so the banner does not fight the review.
	// Check runs BEFORE the pause-clear/settle/submit chain below.
	if tabHasInterruptedToolCall(tab) {
		slog.Warn("desktop: autonomous-update resume skipped — the session's last step was an interrupted tool call ("+restartInterruptedToolMarker+"; review the pending tools manually)", "tab", tab.ID, "session", sessionPath)
		return
	}
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
	// 任务840: the read confirmation runs BEFORE the pause clears — admission-
	// proven guidance parks whose collab mail was delivered settle off the
	// shelf here, so the fresh shelf snapshot never shows the consumed rows
	// with a retry button.
	a.settlePlannedRestartGuidanceForTab(tab)
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
		a.resumeRosterSession(tab, id, sessionKey, autonomousUpdateResumePrompt)
	}()
}

// resumeTabStillMatches reports whether the tab still holds the session this
// resume chain owns. The delivery chain runs across seconds-to-minutes (the
// maintenance wait and the liveness window); a tab whose session was swapped,
// closed, or torn down mid-chain must not receive a stale continue prompt.
func (a *App) resumeTabStillMatches(tab *WorkspaceTab, sessionKey string) bool {
	if a == nil || tab == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !tab.Ready || tab.removed || tab.Ctrl == nil {
		return false
	}
	return sessionRuntimeKey(tab.currentSessionPath()) == sessionKey
}

// resumeDelivered watches the tab's transcript for growth past baseline — the
// receipt that the continue prompt landed in the session (a living turn
// appends its user message within seconds of starting). Returns false when
// the window closes with zero growth (the silent-death shape) or the tab
// stopped matching mid-watch.
func (a *App) resumeDelivered(tab *WorkspaceTab, sessionKey string, baseline int) bool {
	probe, ok := tab.Ctrl.(restartResumeLivenessProbe)
	if !ok {
		return true // no probe → assume delivered (pre-tl1366 shape)
	}
	deadline := time.Now().Add(resumeLivenessWindow)
	for {
		if probe.HistoryLen() > baseline {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(resumeLivenessPoll)
		if !a.resumeTabStillMatches(tab, sessionKey) {
			return false
		}
	}
}

// resumeRosterSession delivers the roster continue prompt (task 254) with the
// tl1366 delivery guarantees:
//
//  1. Wait out the fresh controller's boot-time maintenance (task-920
//     compaction, bounded by resumeMaintenanceWaitBound) before submitting.
//     The 包68 incident: the submit fired at tab-restore, parked behind a
//     2m50s boot compaction on a 980k-token session, the compaction burned
//     ("conversation changed while compression was running"), and the
//     drained resume turn died without appending anything — the session sat
//     silent until the user typed. Submitting into a settled controller
//     removes the park-burn-drain race at its source.
//  2. After an accepted submit, verify delivery via transcript growth; a
//     fall-through is named (续跑落空) and retried exactly once. A second
//     fall-through gives up named — task 263: a failed resume must not
//     resurrect in a loop.
//  3. A REFUSED submit (err) is never retried (task 263 / audit-2 判词③),
//     same as the pre-tl1366 chain.
//
// Sessions whose controller lacks the narrow probes keep the pre-tl1366
// fire-and-forget behavior byte for byte.
func (a *App) resumeRosterSession(tab *WorkspaceTab, tabID, sessionKey, prompt string) {
	baseline0 := -1
	for attempt := 1; ; attempt++ {
		if !a.resumeTabStillMatches(tab, sessionKey) {
			slog.Info("desktop: roster resume abandoned — the tab no longer holds the session (swapped, closed, or tearing down)", "tab", tabID, "attempt", attempt)
			return
		}
		// The maintenance wait runs before EVERY submit attempt (not just the
		// first): a maintenance transaction that starts between the liveness
		// expiry and the retry would otherwise park the retry behind it —
		// the exact park-burn-drain shape this chain exists to avoid — and a
		// later settle would then run BOTH bodies (duplicate continue prompt).
		if probe, ok := tab.Ctrl.(restartMaintenanceProbe); ok && probe.MaintenanceInFlight() {
			deadline := time.Now().Add(resumeMaintenanceWaitBound)
			for probe.MaintenanceInFlight() {
				if !time.Now().Before(deadline) {
					slog.Warn("desktop: roster resume submitting past unsettled boot maintenance ("+resumeMaintenanceBusyMarker+"; the task-920 park path remains the backstop)", "tab", tabID, "attempt", attempt, "waited", resumeMaintenanceWaitBound.String())
					break
				}
				time.Sleep(resumeMaintenancePoll)
				if !a.resumeTabStillMatches(tab, sessionKey) {
					slog.Info("desktop: roster resume abandoned while waiting out boot maintenance — the tab no longer holds the session", "tab", tabID, "attempt", attempt)
					return
				}
			}
			// On the retry path the wait above may have let the FIRST submit's
			// parked body drain and land after all (its transcript growth is
			// the same receipt). Delivering then means the resume already
			// happened — re-submitting would only double the continue prompt,
			// so the late delivery retires the chain.
			if attempt > 1 && baseline0 >= 0 {
				if probe, ok := tab.Ctrl.(restartResumeLivenessProbe); ok && probe.HistoryLen() > baseline0 {
					slog.Info("desktop: autonomous-update resume delivered late (the first submit landed once maintenance settled)", "tab", tabID)
					return
				}
			}
		}
		baseline := -1
		if probe, ok := tab.Ctrl.(restartResumeLivenessProbe); ok {
			baseline = probe.HistoryLen()
			if attempt == 1 {
				baseline0 = baseline
			}
		}
		err := restartResumeSubmit(a, tabID, prompt)
		if err != nil {
			// codeql[go/clear-text-logging] the flagged chain only carries the
			// provider env-var NAME from config validation errors, never the
			// key value; RedactError also strips any provider-echoed key text.
			slog.Warn("desktop: autonomous-update resume skipped ("+restartResumeSkippedMarker+"; review the session manually)", "tab", tabID, "attempt", attempt, "err", secrets.RedactError(err))
			return
		}
		if baseline < 0 {
			// No liveness probe → the pre-tl1366 fire-and-forget contract.
			slog.Info("desktop: autonomous-update run resumed", "tab", tabID)
			return
		}
		if a.resumeDelivered(tab, sessionKey, baseline) {
			if attempt > 1 {
				slog.Info("desktop: autonomous-update resume delivered on retry", "tab", tabID, "attempt", attempt)
			} else {
				slog.Info("desktop: autonomous-update run resumed", "tab", tabID)
			}
			return
		}
		if attempt > 1 {
			slog.Warn("desktop: roster resume produced no transcript growth after one retry ("+resumeFallThroughMarker+"; giving up — review the session manually)", "tab", tabID)
			return
		}
		slog.Warn("desktop: roster resume produced no transcript growth ("+resumeFallThroughMarker+"; retrying once)", "tab", tabID, "window", resumeLivenessWindow.String())
	}
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
