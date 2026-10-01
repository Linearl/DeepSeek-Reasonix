package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"reasonix/internal/tool/builtin"
)

// Agent-facing heartbeat management (task 201): adapt the desktop engine to
// the builtin.HeartbeatManager seam so the agent tools list / upsert / toggle
// tasks through the engine's own parsers and CAS save path. The engine stays
// the single source of truth for the field contract — nothing here re-parses
// a config file or duplicates an interval grammar.

// newHeartbeatManagerAdapter is called once during App startup, after the
// engine exists.
func newHeartbeatManagerAdapter(e *HeartbeatEngine) builtin.HeartbeatManager {
	return heartbeatManagerAdapter{engine: e}
}

type heartbeatManagerAdapter struct {
	engine *HeartbeatEngine
}

func (a heartbeatManagerAdapter) ListTasks() (builtin.HeartbeatListView, error) {
	e := a.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	// Read straight from disk so the agent sees external edits immediately,
	// exactly like the panel does on reload.
	snapshot, err := e.readConfigSnapshot()
	if err != nil {
		return builtin.HeartbeatListView{}, err
	}
	e.recordConfigSnapshotLocked(snapshot)
	e.tasks = snapshot.cfg.Tasks
	e.prunePendingTopicsLocked(e.tasks)
	view := builtin.HeartbeatListView{Revision: snapshot.cfg.Revision, ETag: snapshot.view().ETag, Tasks: make([]builtin.HeartbeatTaskView, 0, len(snapshot.cfg.Tasks))}
	now := time.Now()
	for _, t := range snapshot.cfg.Tasks {
		view.Tasks = append(view.Tasks, heartbeatTaskToView(t, now))
	}
	return view, nil
}

func heartbeatTaskToView(t HeartbeatTask, now time.Time) builtin.HeartbeatTaskView {
	next, hint := nextHeartbeatRunAt(t, now)
	return builtin.HeartbeatTaskView{
		ID:                     t.ID,
		Title:                  t.Title,
		Prompt:                 t.Prompt,
		Interval:               t.Interval,
		Enabled:                t.Enabled,
		Scope:                  t.Scope,
		WorkspaceRoot:          t.WorkspaceRoot,
		ApprovalMode:           t.ApprovalMode,
		TimeWindowStart:        t.TimeWindowStart,
		TimeWindowEnd:          t.TimeWindowEnd,
		NotifyChannels:         t.NotifyChannels,
		NewConversationEachRun: t.NewConversationEachRun,
		ReuseSession:           t.ReuseSession,
		Provider:               t.Provider,
		Model:                  t.Model,
		GoalMode:               t.GoalMode,
		GoalText:               t.GoalText,
		TopicID:                t.TopicID,
		LastRunAt:              t.LastRunAt,
		CreatedAt:              t.CreatedAt,
		RunCount:               len(t.RunHistory),
		NextRunAt:              unixMilliOrZero(next),
		NextRunHint:            hint,
	}
}

func (a heartbeatManagerAdapter) UpsertTask(req builtin.HeartbeatUpsertRequest) (builtin.HeartbeatUpsertResult, error) {
	e := a.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	expected, err := e.readConfigSnapshot()
	if err != nil {
		return builtin.HeartbeatUpsertResult{}, err
	}
	if expected.cfg.Revision != req.ExpectedRevision {
		return builtin.HeartbeatUpsertResult{}, heartbeatRevisionConflict(expected.cfg.Revision)
	}
	tasks := append([]HeartbeatTask(nil), expected.cfg.Tasks...)
	patch := req.Patch
	now := time.Now()
	var edited HeartbeatTask
	created := false
	if strings.TrimSpace(patch.ID) == "" {
		if msg := heartbeatMissingRequired(patch, "title", "prompt", "interval"); msg != "" {
			return builtin.HeartbeatUpsertResult{}, errors.New(msg)
		}
		created = true
		edited = HeartbeatTask{
			ID:        generateHeartbeatTaskID(),
			Title:     patch.Title,
			Prompt:    patch.Prompt,
			Interval:  patch.Interval,
			Enabled:   true,
			CreatedAt: now.UnixMilli(),
		}
		if err := applyHeartbeatPatch(&edited, patch); err != nil {
			return builtin.HeartbeatUpsertResult{}, err
		}
		tasks = append(tasks, edited)
	} else {
		idx := -1
		for i := range tasks {
			if tasks[i].ID == patch.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return builtin.HeartbeatUpsertResult{}, fmt.Errorf("task id %q does not exist — call heartbeat_task_list for current ids; refusing to create a task with a hand-picked id (omit id to create)", patch.ID)
		}
		edited = tasks[idx]
		if err := applyHeartbeatPatch(&edited, patch); err != nil {
			return builtin.HeartbeatUpsertResult{}, err
		}
		tasks[idx] = edited
	}
	if err := validateHeartbeatTaskEdit(edited); err != nil {
		return builtin.HeartbeatUpsertResult{}, err
	}
	// A task that stopped being goal-driven must not leave its goal running
	// (same invariant as ReplaceTasks, #31).
	var clearedGoal string
	if prev := heartbeatTaskByID(expected.cfg.Tasks, edited.ID); prev != nil && prev.GoalMode && !edited.GoalMode {
		clearedGoal = prev.TopicID
	}
	if err := e.writeTasks(tasks, expected, true); err != nil {
		if errors.Is(err, ErrHeartbeatConfigConflict) {
			return builtin.HeartbeatUpsertResult{}, heartbeatRevisionConflict(expected.cfg.Revision)
		}
		return builtin.HeartbeatUpsertResult{}, err
	}
	latest, err := e.readConfigSnapshot()
	if err != nil {
		return builtin.HeartbeatUpsertResult{}, err
	}
	e.recordConfigSnapshotLocked(latest)
	e.tasks = latest.cfg.Tasks
	e.prunePendingTopicsLocked(e.tasks)
	if clearedGoal != "" && e.app != nil {
		e.app.ClearGoalForHeartbeatTopic(clearedGoal)
	}
	next, hint := nextHeartbeatRunAt(edited, now)
	kind, parsed, _ := classifyHeartbeatInterval(edited.Interval) // already validated above
	return builtin.HeartbeatUpsertResult{
		TaskID:         edited.ID,
		Created:        created,
		Revision:       latest.cfg.Revision,
		ParsedInterval: parsed,
		IntervalKind:   kind,
		NextRunAt:      unixMilliOrZero(next),
		NextRunHint:    hint,
	}, nil
}

func (a heartbeatManagerAdapter) SetEnabled(req builtin.HeartbeatSetEnabledRequest) (builtin.HeartbeatSetEnabledResult, error) {
	e := a.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	expected, err := e.readConfigSnapshot()
	if err != nil {
		return builtin.HeartbeatSetEnabledResult{}, err
	}
	if expected.cfg.Revision != req.ExpectedRevision {
		return builtin.HeartbeatSetEnabledResult{}, heartbeatRevisionConflict(expected.cfg.Revision)
	}
	tasks := append([]HeartbeatTask(nil), expected.cfg.Tasks...)
	idx := -1
	for i := range tasks {
		if tasks[i].ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return builtin.HeartbeatSetEnabledResult{}, fmt.Errorf("task id %q does not exist — call heartbeat_task_list for current ids", req.ID)
	}
	tasks[idx].Enabled = req.Enabled
	if err := e.writeTasks(tasks, expected, true); err != nil {
		if errors.Is(err, ErrHeartbeatConfigConflict) {
			return builtin.HeartbeatSetEnabledResult{}, heartbeatRevisionConflict(expected.cfg.Revision)
		}
		return builtin.HeartbeatSetEnabledResult{}, err
	}
	latest, err := e.readConfigSnapshot()
	if err != nil {
		return builtin.HeartbeatSetEnabledResult{}, err
	}
	e.recordConfigSnapshotLocked(latest)
	e.tasks = latest.cfg.Tasks
	e.prunePendingTopicsLocked(e.tasks)
	next, hint := nextHeartbeatRunAt(tasks[idx], time.Now())
	return builtin.HeartbeatSetEnabledResult{
		TaskID:      tasks[idx].ID,
		Enabled:     tasks[idx].Enabled,
		Revision:    latest.cfg.Revision,
		NextRunAt:   unixMilliOrZero(next),
		NextRunHint: hint,
	}, nil
}

func heartbeatRevisionConflict(current uint64) error {
	return fmt.Errorf("%w (expected revision %d stale; current revision is %d) — call heartbeat_task_list again, re-apply your edit on top of the fresh list, and retry with the new expected_revision; do not blind-retry", ErrHeartbeatConfigConflict, current-1, current)
}

// heartbeatMissingRequired names every required-for-create field the caller
// omitted, so one round trip reports all gaps.
func heartbeatMissingRequired(patch builtin.HeartbeatTaskPatch, fields ...string) string {
	var missing []string
	for _, f := range fields {
		switch f {
		case "title":
			if strings.TrimSpace(patch.Title) == "" {
				missing = append(missing, f)
			}
		case "prompt":
			if strings.TrimSpace(patch.Prompt) == "" {
				missing = append(missing, f)
			}
		case "interval":
			if strings.TrimSpace(patch.Interval) == "" {
				missing = append(missing, f)
			}
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("new task requires field(s) %s", strings.Join(missing, ", "))
}

// applyHeartbeatPatch merges the provided fields onto task. Merge semantics:
// absent keys keep the current value; a present empty string clears a string
// field. Provided keys were already validated by the tool layer.
func applyHeartbeatPatch(t *HeartbeatTask, patch builtin.HeartbeatTaskPatch) error {
	provided := patch.Provided
	set := func(key string) bool { return provided != nil && provided[key] }
	if set("title") {
		t.Title = patch.Title
	}
	if set("prompt") {
		t.Prompt = patch.Prompt
	}
	if set("interval") {
		t.Interval = patch.Interval
	}
	if patch.Enabled != nil {
		t.Enabled = *patch.Enabled
	}
	if set("scope") {
		t.Scope = patch.Scope
	}
	if set("workspaceRoot") {
		t.WorkspaceRoot = patch.WorkspaceRoot
	}
	if set("approvalMode") {
		t.ApprovalMode = patch.ApprovalMode
	}
	if set("timeWindowStart") {
		t.TimeWindowStart = patch.TimeWindowStart
	}
	if set("timeWindowEnd") {
		t.TimeWindowEnd = patch.TimeWindowEnd
	}
	if patch.NotifyChannels != nil {
		t.NotifyChannels = patch.NotifyChannels
	}
	if patch.NewConversationEachRun != nil {
		t.NewConversationEachRun = *patch.NewConversationEachRun
	}
	if patch.ReuseSession != nil {
		t.ReuseSession = *patch.ReuseSession
	}
	if set("provider") {
		t.Provider = patch.Provider
	}
	if set("model") {
		t.Model = patch.Model
	}
	if patch.GoalMode != nil {
		t.GoalMode = *patch.GoalMode
	}
	if set("goalText") {
		t.GoalText = patch.GoalText
	}
	return nil
}

// validateHeartbeatTaskEdit rejects an invalid task with the offending field
// named. The scheduler would otherwise silently skip the task (its due check
// treats an unparsable interval as never due), which is exactly the failure
// mode task 201 removes.
func validateHeartbeatTaskEdit(t HeartbeatTask) error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("field \"id\": must not be empty")
	}
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("field \"title\": must not be empty")
	}
	if strings.TrimSpace(t.Prompt) == "" {
		return fmt.Errorf("field \"prompt\": must not be empty")
	}
	if _, _, err := classifyHeartbeatInterval(t.Interval); err != nil {
		return err
	}
	switch t.Scope {
	case "", "global", "project":
	default:
		return fmt.Errorf("field \"scope\": %q is invalid; use \"\" or \"global\" for the global workspace, \"project\" for a project-root task", t.Scope)
	}
	if t.Scope == "project" && strings.TrimSpace(t.WorkspaceRoot) == "" {
		return fmt.Errorf("field \"workspaceRoot\": required when scope=\"project\"")
	}
	switch strings.ToLower(strings.TrimSpace(t.ApprovalMode)) {
	case "", "ask", "auto", "yolo":
	default:
		return fmt.Errorf("field \"approvalMode\": %q is invalid; use \"ask\", \"auto\", or \"yolo\" (empty defaults to \"yolo\")", t.ApprovalMode)
	}
	if t.TimeWindowStart != "" {
		if _, _, ok := parseHeartbeatClock(t.TimeWindowStart); !ok {
			return fmt.Errorf("field \"timeWindowStart\": %q is not a valid HH:MM time", t.TimeWindowStart)
		}
	}
	if t.TimeWindowEnd != "" {
		if _, _, ok := parseHeartbeatClock(t.TimeWindowEnd); !ok {
			return fmt.Errorf("field \"timeWindowEnd\": %q is not a valid HH:MM time", t.TimeWindowEnd)
		}
	}
	return nil
}

// classifyHeartbeatInterval identifies which of the engine's three interval
// grammars applies and returns a parsed receipt. The grammars are tried in
// the engine's own precedence order (cron, named schedule, duration), so the
// classification matches how heartbeatTaskDueAt will actually schedule it.
func classifyHeartbeatInterval(interval string) (kind, parsed string, err error) {
	s := strings.TrimSpace(interval)
	if s == "" {
		return "", "", fmt.Errorf("field \"interval\": must not be empty")
	}
	if isCronExpr(s) {
		return "cron", s, nil
	}
	if sched, ok := parseHeartbeatSchedule(s); ok {
		return sched.kind, s, nil
	}
	if idx := strings.Index(s, "|"); idx >= 0 {
		// "fallback|named" forms fall back to a duration; parse the fallback
		// part so an unusable fallback fails here instead of silently skipping.
		s = s[:idx]
	}
	d, perr := parseInterval(s)
	if perr != nil || d <= 0 {
		return "", "", fmt.Errorf("field \"interval\": %q is not a valid interval; use a duration like \"30m\"/\"24h\", a 5-field cron expression like \"0 9 * * 1-5\", or a named schedule like \"168h|weekly:fri@18:00\"", interval)
	}
	return "duration", d.String(), nil
}

// generateHeartbeatTaskID mirrors the panel's random-id generator
// (App.HeartbeatGenerateID) so agent-created ids are indistinguishable.
func generateHeartbeatTaskID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// ── Next-run projection ─────────────────────────────────────────────────────

// nextHeartbeatRunAt projects when the task will fire next, using the same
// grammar precedence as heartbeatTaskDueAt (cron, named schedule, duration)
// plus the time-window adjustment. This is a display projection for the agent
// tools, not the scheduling source of truth; the scheduler keeps firing on
// heartbeatTaskDueAt.
func nextHeartbeatRunAt(t HeartbeatTask, now time.Time) (time.Time, string) {
	if !t.Enabled {
		return time.Time{}, "disabled"
	}
	kind, _, err := classifyHeartbeatInterval(t.Interval)
	if err != nil {
		return time.Time{}, "invalid-interval"
	}
	var next time.Time
	switch kind {
	case "cron":
		next = nextHeartbeatCronAt(t.Interval, now)
	case "duration":
		next = nextHeartbeatDurationAt(t, now)
	default:
		// Named schedules (daily/weekly/biweekly/monthly/yearly).
		next = nextHeartbeatScheduleAt(t, now, kind)
	}
	if next.IsZero() {
		return time.Time{}, "invalid-interval"
	}
	next = advanceIntoHeartbeatWindow(t, next, now)
	if !next.After(now) {
		if now.Sub(next) < 2*time.Minute {
			return now, "due-now"
		}
		return now, "overdue"
	}
	return next, ""
}

// nextHeartbeatCronAt scans minute-by-minute (the cron grammar's resolution)
// for the next matching minute within the following 8 days; a valid cron
// always matches within a week.
func nextHeartbeatCronAt(expr string, now time.Time) time.Time {
	candidate := now.Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(0, 0, 8)
	for candidate.Before(limit) {
		if cronDue(expr, candidate) {
			return candidate
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}
}

func nextHeartbeatDurationAt(t HeartbeatTask, now time.Time) time.Time {
	d, err := parseInterval(t.Interval)
	if err != nil || d <= 0 {
		return time.Time{}
	}
	base := t.LastRunAt
	if base == 0 {
		base = t.CreatedAt
	}
	if base == 0 {
		// Never ran and no anchor: the scheduler fires on the first tick, so
		// the honest projection is "now".
		return now
	}
	return time.UnixMilli(base).Add(d)
}

// nextHeartbeatScheduleAt mirrors previousHeartbeatScheduleAt forwards: the
// next calendar occurrence of the named schedule at or after now.
func nextHeartbeatScheduleAt(t HeartbeatTask, now time.Time, kind string) time.Time {
	s, ok := parseHeartbeatSchedule(t.Interval)
	if !ok || !s.hasRules {
		return time.Time{}
	}
	switch kind {
	case "daily":
		candidate := dateAt(now.Year(), now.Month(), now.Day(), s.hour, s.minute, now.Location())
		if !candidate.After(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate
	case "weekly":
		return nextHeartbeatWeeklyAt(s, now, 7, time.Time{})
	case "biweekly":
		anchor := heartbeatScheduleAnchor(t, now)
		return nextHeartbeatWeeklyAt(s, now, 14, anchor)
	case "monthly":
		return nextHeartbeatMonthlyAt(s, now)
	case "yearly":
		return nextHeartbeatYearlyAt(s, now)
	}
	return time.Time{}
}

func nextHeartbeatWeeklyAt(s heartbeatSchedule, now time.Time, windowDays int, anchor time.Time) time.Time {
	var best time.Time
	for offset := windowDays; offset >= 0; offset-- {
		day := now.AddDate(0, 0, offset)
		for _, wd := range s.days {
			if day.Weekday() != wd {
				continue
			}
			candidate := dateAt(day.Year(), day.Month(), day.Day(), s.hour, s.minute, now.Location())
			if !candidate.After(now) {
				continue
			}
			if !anchor.IsZero() && weeksBetween(weekStart(anchor), weekStart(candidate))%2 != 0 {
				continue
			}
			if best.IsZero() || candidate.Before(best) {
				best = candidate
			}
		}
	}
	return best
}

func nextHeartbeatMonthlyAt(s heartbeatSchedule, now time.Time) time.Time {
	candidate := monthlyCandidate(now.Year(), now.Month(), s.day, s.hour, s.minute, now.Location())
	if !candidate.After(now) {
		nextMonth := now.AddDate(0, 1, 0)
		candidate = monthlyCandidate(nextMonth.Year(), nextMonth.Month(), s.day, s.hour, s.minute, now.Location())
	}
	return candidate
}

func nextHeartbeatYearlyAt(s heartbeatSchedule, now time.Time) time.Time {
	month := time.Month(s.month)
	candidate := monthlyCandidate(now.Year(), month, s.day, s.hour, s.minute, now.Location())
	if !candidate.After(now) {
		candidate = monthlyCandidate(now.Year()+1, month, s.day, s.hour, s.minute, now.Location())
	}
	return candidate
}

// advanceIntoHeartbeatWindow pushes a projection that falls outside the
// task's time window to the next window opening (start inclusive). An empty
// window changes nothing. If start is unset only end constrains; a projection
// past end rolls to the next day's midnight-anchored window, mirroring how
// heartbeatWithinTimeWindow defers out-of-window ticks to a later tick inside
// the window.
func advanceIntoHeartbeatWindow(t HeartbeatTask, candidate, now time.Time) time.Time {
	if t.TimeWindowStart == "" && t.TimeWindowEnd == "" {
		return candidate
	}
	startH, startM, startOK := parseHeartbeatClock(t.TimeWindowStart)
	_, _, endOK := parseHeartbeatClock(t.TimeWindowEnd)
	if !startOK && !endOK {
		return candidate
	}
	within := func(ts time.Time) bool {
		task := HeartbeatTask{TimeWindowStart: t.TimeWindowStart, TimeWindowEnd: t.TimeWindowEnd}
		return heartbeatWithinTimeWindow(task, ts)
	}
	if within(candidate) {
		return candidate
	}
	if !startOK {
		// End-only window: next opening is the next midnight.
		midnight := dateAt(candidate.Year(), candidate.Month(), candidate.Day(), 0, 0, candidate.Location()).AddDate(0, 0, 1)
		if midnight.Before(candidate) {
			midnight = midnight.AddDate(0, 0, 1)
		}
		return midnight
	}
	open := dateAt(candidate.Year(), candidate.Month(), candidate.Day(), startH, startM, candidate.Location())
	if !open.After(candidate) {
		open = open.AddDate(0, 0, 1)
	}
	return open
}
