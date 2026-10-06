package main

// Task 326 — autopilot keeps a guard task watching the session it runs.
//
// Why a guard exists at all: autopilot is a *state*, not a one-shot goal, and
// it runs unattended. If the session stalls (interrupted turn, cold start that
// never resumed) nobody is there to notice, so a scheduled guard task checks
// it periodically and wakes it. The design questions the task calls out, and
// how each is answered here:
//
//  1. 幂等单例 — the guard's identity is derived from the owner topic
//     (autopilot-guard-<topic>), so every entry point (mode selector,
//     new-session defaults, restart restore, the periodic reconciliation)
//     converges on the *same* task. Creating it twice is structurally
//     impossible rather than merely unlikely, and Ensure also deletes a
//     duplicate that a hand-edit left behind.
//  2. 边沿触发 — creation is driven by "an autopilot session exists now"
//     (observed on the selector edge immediately, and by the reconciliation
//     sweep for every other entry), never per-run: ensure is a no-op when the
//     task is already correct, so repeated observation costs one read.
//  3. 对称清理 — the sweep disables a guard whose owner tab is no longer in
//     autopilot, and deletes one whose owner conversation was deleted (no
//     orphan guards).
//  4. 权限隔离 — the guard task records approvalMode "ask" (the strictest
//     gear), but a guard that wakes its *owner* session never touches that
//     session's approval mode: the wake runs with the owner's own permissions
//     (autopilot is yolo, task 325). A guard that ends up owning its own
//     session gets ask, so it can never carry proxy-approval power.
//  5. 自关闭 — after heartbeatIdleTerminateStrikes consecutive checks in which
//     the owner had nothing to do, the configured policy applies
//     (disable | standby | destroy). The strike ceiling and the IdleStreak
//     field are task 244 B1's, shared so both burn guards count the same way.
//  6. 面板间隔 — desktop.autopilot_guard_interval (minutes) is written into the
//     guard's interval and re-applied to guards that already exist, in place.

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/secrets"
)

// autopilotGuardIDPrefix marks a heartbeat task as an autopilot guard. The id
// is the task's lookup key in the panel and the engine, so deriving it from
// the owner topic is what makes ensure idempotent.
const autopilotGuardIDPrefix = "autoguard-"

// Self-close policies for a watched session that goes quiet (task 326).
const (
	autopilotGuardQuiescentDisable = "disable" // self-disable; re-enabling re-arms it
	autopilotGuardQuiescentStandby = "standby" // stay armed, do nothing
	autopilotGuardQuiescentDestroy = "destroy" // delete the guard task
)

// autopilotGuardPrompt is deliberately a read-only check: the guard runs inside
// the session it watches (ReuseSession), so anything else would let a
// background scheduler drive work nobody asked for. The one permitted action is
// continuing work the owner already had.
const autopilotGuardPrompt = `【autopilot 守护检查】只做只读检查，不要执行任何变更操作（不改配置、不新建会话、不删文件、不动权限、不派发子任务）。
1. 看一眼这个会话正在进行的 autopilot 工作：还有未完成的部分就接着推进；
2. 工作已经完成、且没有新的追加任务时，只回复一行：GUARD_QUIET
不要开启新的工作，不要浏览无关内容。`

// autopilotGuardOwner identifies the session a guard watches. TopicID doubles
// as the guard's run target because the guard reuses that conversation.
type autopilotGuardOwner struct {
	TopicID       string
	Scope         string
	WorkspaceRoot string
}

func autopilotGuardID(topicID string) string {
	return autopilotGuardIDPrefix + strings.TrimSpace(topicID)
}

func isAutopilotGuardTask(t HeartbeatTask) bool {
	return strings.HasPrefix(t.ID, autopilotGuardIDPrefix)
}

// autopilotGuardInterval turns the panel's minute dial into the task's
// interval grammar. Minutes are kept as minutes ("30m", "150m") — parseInterval
// accepts any Go duration, so no hour folding is needed and the value the user
// typed is the value that shows up.
func autopilotGuardInterval(minutes int) string {
	if minutes <= 0 {
		minutes = config.AutopilotGuardDefaultIntervalMinutes
	}
	return fmt.Sprintf("%dm", minutes)
}

func autopilotGuardTask(owner autopilotGuardOwner, interval string) HeartbeatTask {
	scope, workspaceRoot := heartbeatRunScope(owner.Scope, owner.WorkspaceRoot)
	return HeartbeatTask{
		ID:            autopilotGuardID(owner.TopicID),
		Title:         "Autopilot guard: " + owner.TopicID,
		Prompt:        autopilotGuardPrompt,
		Interval:      interval,
		Enabled:       true,
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		// The guard watches the owner's own conversation: waking it appends to
		// the session whose permissions already apply, so the wake inherits the
		// owner's posture instead of a new one.
		TopicID:      owner.TopicID,
		ReuseSession: true,
		ApprovalMode: "ask", // strictest gear — a guard never carries proxy-approval power
		CreatedAt:    time.Now().UnixMilli(),
	}
}

// ── App-side owner discovery ────────────────────────────────────────────────

// autopilotGuardOwners lists the conversations currently in autopilot mode,
// keyed by topic. Holding App.mu is what makes the sweep below race-free with
// a mode switch happening at the same moment.
func (a *App) autopilotGuardOwners() []autopilotGuardOwner {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	out := make([]autopilotGuardOwner, 0, 2)
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || !tab.autopilot {
			continue
		}
		topic := strings.TrimSpace(tab.TopicID)
		if topic == "" || seen[topic] {
			continue
		}
		seen[topic] = true
		out = append(out, autopilotGuardOwner{
			TopicID:       topic,
			Scope:         tab.Scope,
			WorkspaceRoot: tab.WorkspaceRoot,
		})
	}
	return out
}

// ownerTabAutopilot reports whether a tab on topicID is open, and whether that
// tab is still in autopilot mode. found=false means "no open tab" — the caller
// falls back to the topic registry rather than guessing.
func (a *App) ownerTabAutopilot(topicID string) (found, autopilot bool) {
	if a == nil {
		return false, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab != nil && strings.TrimSpace(tab.TopicID) == strings.TrimSpace(topicID) {
			return true, tab.autopilot
		}
	}
	return false, false
}

// ensureAutopilotGuard creates or refreshes the guard for one autopilot
// session. Safe to call from every entry point: the task is keyed by topic, so
// the tenth call is a no-op rather than a tenth task. Task 547 gates the
// whole call behind the auto-creation sub-option (default off): with the
// sub-option off this is a no-op — the off semantics are "do not create",
// never "create then disable".
func (a *App) ensureAutopilotGuard(owner autopilotGuardOwner) {
	if a == nil || a.heartbeat == nil || strings.TrimSpace(owner.TopicID) == "" {
		return
	}
	if !a.autopilotGuardAutocreate() {
		return
	}
	if err := a.heartbeat.EnsureAutopilotGuard(owner, a.autopilotGuardIntervalMinutes()); err != nil {
		log.Printf("[autopilot-guard] ensure for topic %q failed: %s", owner.TopicID, secrets.RedactError(err))
	}
}

// clearAutopilotGuard turns the guard off as soon as the session leaves
// autopilot. The periodic sweep repeats this, so a path that forgets to call it
// still converges within one tick.
func (a *App) clearAutopilotGuard(topicID string) {
	topicID = strings.TrimSpace(topicID)
	if a == nil || a.heartbeat == nil || topicID == "" {
		return
	}
	if err := a.heartbeat.DisableAutopilotGuard(autopilotGuardID(topicID)); err != nil {
		log.Printf("[autopilot-guard] disable for topic %q failed: %s", topicID, secrets.RedactError(err))
	}
}

func (a *App) autopilotGuardIntervalMinutes() int {
	cfg := config.LoadForEdit(config.UserConfigPath())
	return cfg.AutopilotGuardIntervalMinutes()
}

// autopilotGuardAutocreate reports whether autopilot may auto-create guard
// tasks (task 547 sub-option, default off).
func (a *App) autopilotGuardAutocreate() bool {
	cfg := config.LoadForEdit(config.UserConfigPath())
	return cfg.AutopilotGuardAutocreateEnabled()
}

func (a *App) autopilotGuardQuiescentPolicy() string {
	cfg := config.LoadForEdit(config.UserConfigPath())
	return cfg.AutopilotGuardQuiescentPolicy()
}

// autopilotGuardQuiescentPolicy is the engine-side read; it fails closed to
// "disable" when no App is wired (tests, early startup) so a guard can never
// be left running unbounded on a missing config.
func (e *HeartbeatEngine) autopilotGuardQuiescentPolicy() string {
	if e == nil || e.app == nil {
		return autopilotGuardQuiescentDisable
	}
	return e.app.autopilotGuardQuiescentPolicy()
}

// autopilotGuardAutocreate is the engine-side read of the task-547 sub-option;
// it fails closed to "off" when no App is wired (tests, early startup), so a
// missing config can never re-arm guard creation.
func (e *HeartbeatEngine) autopilotGuardAutocreate() bool {
	if e == nil || e.app == nil {
		return false
	}
	return e.app.autopilotGuardAutocreate()
}

// resyncAutopilotGuardInterval re-points every existing guard at the new dial
// without recreating any of them (acceptance: 修改后已有守护任务的间隔同步更新，
// 复用而非重建，仍仅 1 个).
func (a *App) resyncAutopilotGuardInterval(minutes int) {
	if a == nil || a.heartbeat == nil {
		return
	}
	if err := a.heartbeat.SyncAutopilotGuardInterval(minutes); err != nil {
		log.Printf("[autopilot-guard] interval resync failed: %s", secrets.RedactError(err))
	}
}

// ── Engine side ─────────────────────────────────────────────────────────────

// mutateTasks runs one CAS-protected edit of the whole task list. Every guard
// operation goes through it so they share the conflict retry and the
// in-memory/disk snapshot bookkeeping the panel's saves rely on — a guard write
// that skipped recordConfigSnapshotLocked would make the next panel save report
// a phantom conflict.
func (e *HeartbeatEngine) mutateTasks(mutate func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error)) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		expected, err := e.readConfigSnapshot()
		if err != nil {
			return err
		}
		if expected.exists && expected.cfg.SchemaVersion > heartbeatSchemaVersion {
			return fmt.Errorf("heartbeat config schemaVersion %d is newer than this binary supports (%d); upgrade Reasonix before editing", expected.cfg.SchemaVersion, heartbeatSchemaVersion)
		}
		// The closure may reslice (a deletion shrinks the list), so the header
		// it returns is the one that gets persisted — a reslice inside the
		// closure would otherwise be invisible to this caller.
		tasks, changed, err := mutate(append([]HeartbeatTask(nil), expected.cfg.Tasks...))
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if err := e.writeTasks(tasks, expected, true); err != nil {
			if errors.Is(err, ErrHeartbeatConfigConflict) {
				lastErr = err
				continue
			}
			return err
		}
		latest, err := e.readConfigSnapshot()
		if err != nil {
			return err
		}
		e.recordConfigSnapshotLocked(latest)
		e.tasks = latest.cfg.Tasks
		e.prunePendingTopicsLocked(e.tasks)
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return ErrHeartbeatConfigConflict
}

// EnsureAutopilotGuard makes sure exactly one guard task exists for owner and
// that it matches the current panel dial. Existing task = refresh in place
// (never a second one); a duplicate bound to the same conversation is removed
// on the spot, which is the store-layer half of the singleton guarantee.
func (e *HeartbeatEngine) EnsureAutopilotGuard(owner autopilotGuardOwner, intervalMinutes int) error {
	if e == nil || strings.TrimSpace(owner.TopicID) == "" {
		return nil
	}
	wantID := autopilotGuardID(owner.TopicID)
	interval := autopilotGuardInterval(intervalMinutes)
	return e.mutateTasks(func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error) {
		changed := false
		out := make([]HeartbeatTask, 0, len(tasks)+1)
		var found *HeartbeatTask
		for i := range tasks {
			t := tasks[i]
			// Any other guard watching the same conversation is a duplicate —
			// keep none: 单例 means one, not "one per entry point".
			if isAutopilotGuardTask(t) && strings.TrimSpace(t.TopicID) == strings.TrimSpace(owner.TopicID) && t.ID != wantID {
				changed = true
				continue
			}
			if t.ID == wantID {
				cp := t
				found = &cp
				continue
			}
			out = append(out, t)
		}
		if found != nil {
			t := *found
			scope, workspaceRoot := heartbeatRunScope(owner.Scope, owner.WorkspaceRoot)
			if !t.Enabled {
				t.Enabled = true
				changed = true
			}
			if t.Interval != interval {
				t.Interval = interval
				changed = true
			}
			if t.TopicID != owner.TopicID {
				t.TopicID = owner.TopicID
				changed = true
			}
			if t.Scope != scope || t.WorkspaceRoot != workspaceRoot {
				t.Scope, t.WorkspaceRoot = scope, workspaceRoot
				changed = true
			}
			if !t.ReuseSession || t.ApprovalMode != "ask" || t.Prompt != autopilotGuardPrompt {
				// A hand-edited guard that stopped watching its owner (or that
				// was given a different prompt/approval) is restored — the
				// permission isolation in point 4 depends on it.
				t.ReuseSession, t.ApprovalMode, t.Prompt = true, "ask", autopilotGuardPrompt
				changed = true
			}
			out = append(out, t)
		} else {
			out = append(out, autopilotGuardTask(owner, interval))
			changed = true
		}
		return out, changed, nil
	})
}

// DisableAutopilotGuard turns one guard off, leaving it in place so its run
// history and the manual re-enable path survive.
func (e *HeartbeatEngine) DisableAutopilotGuard(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if e == nil || taskID == "" {
		return nil
	}
	return e.mutateTasks(func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error) {
		for i := range tasks {
			if tasks[i].ID == taskID && tasks[i].Enabled {
				tasks[i].Enabled = false
				return tasks, true, nil
			}
		}
		return tasks, false, nil
	})
}

// RemoveAutopilotGuard deletes a guard: the orphan case, where the conversation
// it watched no longer exists (task 326 acceptance: 会话删除 → 无孤儿任务残留).
func (e *HeartbeatEngine) RemoveAutopilotGuard(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if e == nil || taskID == "" {
		return nil
	}
	return e.mutateTasks(func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error) {
		for i := range tasks {
			if tasks[i].ID == taskID {
				out := append([]HeartbeatTask(nil), tasks[:i]...)
				out = append(out, tasks[i+1:]...)
				return out, true, nil
			}
		}
		return tasks, false, nil
	})
}

// SyncAutopilotGuardInterval re-points every guard's interval in place.
func (e *HeartbeatEngine) SyncAutopilotGuardInterval(intervalMinutes int) error {
	if e == nil {
		return nil
	}
	interval := autopilotGuardInterval(intervalMinutes)
	return e.mutateTasks(func(tasks []HeartbeatTask) ([]HeartbeatTask, bool, error) {
		changed := false
		for i := range tasks {
			if isAutopilotGuardTask(tasks[i]) && tasks[i].Interval != interval {
				tasks[i].Interval = interval
				changed = true
			}
		}
		return tasks, changed, nil
	})
}

// ReconcileAutopilotGuards is the 对账 half: it creates the guard an autopilot
// session is missing, disables one whose owner tab left autopilot, and deletes
// one whose owner conversation is gone. It runs on the mode-switch edge and
// once per scheduler tick, so any enable or close path that forgets to notify
// still converges. Task 547 gates the create half behind the auto-creation
// sub-option (default off): with the sub-option off nothing is ever created,
// and guards a previous version already created converge to the new setting —
// an enabled guard is disabled wherever its owner stands. The cleanup half
// (orphan removal, disable-after-exit) runs in both states so an upgrade can
// never leave a running guard the user opted out of.
func (e *HeartbeatEngine) ReconcileAutopilotGuards() {
	if e == nil || e.app == nil {
		return
	}
	autocreate := e.autopilotGuardAutocreate()
	owners := e.app.autopilotGuardOwners()
	intervalMinutes := e.app.autopilotGuardIntervalMinutes()
	for _, owner := range owners {
		if !autocreate {
			// Task 547: off means never create — the sweep's create half is a
			// no-op, the disable below converges what already exists.
			continue
		}
		if err := e.EnsureAutopilotGuard(owner, intervalMinutes); err != nil {
			log.Printf("[autopilot-guard] reconcile ensure for topic %q failed: %s", owner.TopicID, secrets.RedactError(err))
		}
	}

	e.mu.Lock()
	e.adoptExternalEditsLocked()
	guards := make([]HeartbeatTask, 0, len(e.tasks))
	for _, t := range e.tasks {
		if isAutopilotGuardTask(t) {
			guards = append(guards, t)
		}
	}
	e.mu.Unlock()
	if len(guards) == 0 {
		return
	}

	for _, g := range guards {
		owner := strings.TrimSpace(g.TopicID)
		if owner == "" {
			// A guard with no owner can never be reconciled to anything.
			if err := e.RemoveAutopilotGuard(g.ID); err != nil {
				log.Printf("[autopilot-guard] orphan removal of %q failed: %s", g.ID, secrets.RedactError(err))
			}
			continue
		}
		found, autopilotOn := e.app.ownerTabAutopilot(owner)
		if !found && !e.topicRegistered(owner) {
			// No open tab AND no registered conversation: the session was
			// deleted. Delete its guard rather than leaving it to run forever.
			if err := e.RemoveAutopilotGuard(g.ID); err != nil {
				log.Printf("[autopilot-guard] orphan removal of %q failed: %s", g.ID, secrets.RedactError(err))
			}
			continue
		}
		if !g.Enabled {
			continue
		}
		if found && !autopilotOn {
			if err := e.DisableAutopilotGuard(g.ID); err != nil {
				log.Printf("[autopilot-guard] disable of %q failed: %s", g.ID, secrets.RedactError(err))
			}
			continue
		}
		if !autocreate {
			// Task 547 convergence: the sub-option is off but a guard a
			// previous version created is still armed (its owner may still be
			// in autopilot, or the tab is just not open). Disable it — the
			// row survives so its history and the manual re-enable path do
			// too, and re-checking the sub-option revives the same task
			// in place instead of growing a second one.
			slog.Info("autopilot guard disabled — guard auto-creation sub-option is off (task 547)",
				"guard", g.ID, "topic", owner)
			if err := e.DisableAutopilotGuard(g.ID); err != nil {
				log.Printf("[autopilot-guard] opt-out disable of %q failed: %s", g.ID, secrets.RedactError(err))
			}
		}
	}
}

// autopilotGuardOwnerQuiescent reports whether the watched session currently
// has nothing to do — the signal the self-close policy consumes. "Nothing to
// do" is deliberately conservative: no open tab, a still-building controller, a
// running turn, or a goal that is still running all count as *active*, so the
// guard is never torn down on missing information.
func (e *HeartbeatEngine) autopilotGuardOwnerQuiescent(topicID string) bool {
	if e == nil || e.app == nil || strings.TrimSpace(topicID) == "" {
		return false
	}
	a := e.app
	a.mu.Lock()
	var (
		found       bool
		autopilotOn bool
		goal        string
		ctrl        control.SessionAPI
	)
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || strings.TrimSpace(tab.TopicID) != strings.TrimSpace(topicID) {
			continue
		}
		found, autopilotOn, goal, ctrl = true, tab.autopilot, strings.TrimSpace(tab.goal), tab.Ctrl
		break
	}
	a.mu.Unlock()
	if !found || !autopilotOn || ctrl == nil {
		return false
	}
	if goal != "" && !heartbeatGoalTerminal(ctrl.GoalStatus()) {
		return false // still working through the objective: keep the guard armed
	}
	if heartbeatControllerBusy(ctrl) {
		return false
	}
	return true
}

// heartbeatGoalTerminal mirrors the goal engine's own notion of "done" — every
// status that is not running/blocked-pending counts as finished. Blocked is
// terminal here because a blocked goal needs a human, not a background nudge.
func heartbeatGoalTerminal(status string) bool {
	switch status {
	case control.GoalStatusComplete, control.GoalStatusStopped, control.GoalStatusFailed,
		control.GoalStatusCancelled, control.GoalStatusBudgetExhausted,
		control.GoalStatusTimeLimitReached:
		return true
	case control.GoalStatusBlocked, control.GoalStatusRunning:
		return false
	default:
		return false // unknown status: treat as still running (keep the guard)
	}
}

// evaluateAutopilotGuardClose folds one check into the self-close strike count
// and applies the configured policy once the ceiling is reached. It runs after
// the wake, so "已完成仍被拉起 3 次 → 自动禁用" counts the pulls that actually
// happened (task 326 / task 244 B1 share heartbeatIdleTerminateStrikes).
func (e *HeartbeatEngine) evaluateAutopilotGuardClose(t HeartbeatTask, quiet bool) HeartbeatTask {
	if !isAutopilotGuardTask(t) {
		return t
	}
	if !quiet {
		// The owner did something: the guard proved useful, so the streak resets.
		if t.IdleStreak != 0 {
			t.IdleStreak = 0
		}
		return t
	}
	t.IdleStreak++
	if t.IdleStreak < heartbeatIdleTerminateStrikes {
		return t
	}
	policy := e.autopilotGuardQuiescentPolicy()
	if policy == autopilotGuardQuiescentStandby {
		return t // 保留待命: stay armed, the streak simply stops mattering
	}
	t.Enabled = false
	slog.Info("autopilot guard self-closing — watched session idle",
		"guard", t.ID, "topic", t.TopicID, "idle_streak", t.IdleStreak,
		"policy", policy)
	return t
}

// finishAutopilotGuardRun runs after the task's run state has been merged. The
// destroy policy cannot express "delete me" through the run-state merge (that
// channel carries state, not row lifetime), so deletion happens here.
func (e *HeartbeatEngine) finishAutopilotGuardRun(updated HeartbeatTask) {
	if e == nil || !isAutopilotGuardTask(updated) || updated.Enabled {
		return
	}
	if updated.IdleStreak < heartbeatIdleTerminateStrikes {
		return
	}
	if e.autopilotGuardQuiescentPolicy() != autopilotGuardQuiescentDestroy {
		return
	}
	if err := e.RemoveAutopilotGuard(updated.ID); err != nil {
		log.Printf("[autopilot-guard] destroy of %q failed: %s", updated.ID, secrets.RedactError(err))
		return
	}
	log.Printf("[autopilot-guard] destroyed guard %q: watched session idle and policy is destroy", updated.ID)
}
