package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"reasonix/internal/tool/builtin"
)

func boolPtr(v bool) *bool { return &v }

func newTestAdapter(t *testing.T) (heartbeatManagerAdapter, *HeartbeatEngine) {
	t.Helper()
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	return heartbeatManagerAdapter{engine: engine}, engine
}

func mustList(t *testing.T, a heartbeatManagerAdapter) builtin.HeartbeatListView {
	t.Helper()
	view, err := a.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestHeartbeatManagerListEmptyConfig(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	if view.Revision != 0 || len(view.Tasks) != 0 {
		t.Fatalf("empty config should report revision 0 and no tasks, got revision=%d tasks=%d", view.Revision, len(view.Tasks))
	}
}

func TestHeartbeatManagerUpsertCreatesWithReceipt(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	res, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"title": true, "prompt": true, "interval": true},
			Title:    "weekly", Prompt: "write the report", Interval: "168h|weekly:fri@18:00",
		},
		ExpectedRevision: view.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.TaskID == "" || res.Revision != 1 {
		t.Fatalf("create receipt wrong: %+v", res)
	}
	if res.IntervalKind != "weekly" || res.ParsedInterval != "168h|weekly:fri@18:00" {
		t.Fatalf("interval receipt wrong: kind=%q parsed=%q", res.IntervalKind, res.ParsedInterval)
	}
	// Persisted state must carry defaults: enabled=true, createdAt stamped.
	fresh := mustList(t, a)
	if fresh.Revision != 1 || len(fresh.Tasks) != 1 {
		t.Fatalf("list after create wrong: revision=%d tasks=%d", fresh.Revision, len(fresh.Tasks))
	}
	task := fresh.Tasks[0]
	if !task.Enabled || task.CreatedAt == 0 {
		t.Fatalf("created task defaults wrong: enabled=%v createdAt=%d", task.Enabled, task.CreatedAt)
	}
	if task.NextRunAt == 0 || task.NextRunHint != "" {
		t.Fatalf("created task next-run projection wrong: at=%d hint=%q", task.NextRunAt, task.NextRunHint)
	}
}

func TestHeartbeatManagerUpsertMergeSemantics(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	created, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"title": true, "prompt": true, "interval": true, "approvalMode": true},
			Title:    "t", Prompt: "p", Interval: "30m", ApprovalMode: "ask",
		},
		ExpectedRevision: view.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Update only the title: prompt/interval/approvalMode must survive.
	updated, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"id": true, "title": true},
			ID:       created.TaskID, Title: "renamed",
		},
		ExpectedRevision: created.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := mustList(t, a)
	task := fresh.Tasks[0]
	if task.Title != "renamed" || task.Prompt != "p" || task.Interval != "30m" || task.ApprovalMode != "ask" {
		t.Fatalf("merge semantics broken: %+v", task)
	}
	if updated.Created {
		t.Fatalf("update reported created=true")
	}
}

func TestHeartbeatManagerUpsertUnknownIDRefused(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	_, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"id": true, "title": true, "prompt": true, "interval": true},
			ID:       "ghost", Title: "t", Prompt: "p", Interval: "30m",
		},
		ExpectedRevision: view.Revision,
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unknown id should be refused, got %v", err)
	}
}

func TestHeartbeatManagerUpsertRevisionConflictNotOverwrite(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	created, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"title": true, "prompt": true, "interval": true},
			Title:    "t", Prompt: "p", Interval: "30m",
		},
		ExpectedRevision: view.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a concurrent writer advancing the file past our snapshot.
	concurrent := mustList(t, a)
	if _, err := a.SetEnabled(builtin.HeartbeatSetEnabledRequest{ID: created.TaskID, Enabled: false, ExpectedRevision: concurrent.Revision}); err != nil {
		t.Fatal(err)
	}
	_, err = a.SetEnabled(builtin.HeartbeatSetEnabledRequest{ID: created.TaskID, Enabled: true, ExpectedRevision: concurrent.Revision})
	if !errors.Is(err, ErrHeartbeatConfigConflict) {
		t.Fatalf("stale revision must surface as ErrHeartbeatConfigConflict, got %v", err)
	}
	var conflictErr = err
	if !strings.Contains(conflictErr.Error(), "current revision is") {
		t.Fatalf("conflict error must name the current revision, got %v", err)
	}
	// The failed write must not have flipped the task back on.
	fresh := mustList(t, a)
	if fresh.Tasks[0].Enabled {
		t.Fatalf("conflicted write overwrote concurrent state: %+v", fresh.Tasks[0])
	}
}

func TestHeartbeatManagerUpsertValidationErrors(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	cases := []struct {
		name     string
		patch    builtin.HeartbeatTaskPatch
		provided []string
		wantErr  string
	}{
		{
			name: "bad interval", wantErr: `field "interval"`,
			patch: builtin.HeartbeatTaskPatch{
				Provided: map[string]bool{"title": true, "prompt": true, "interval": true},
				Title:    "t", Prompt: "p", Interval: "every now and then",
			},
		},
		{
			name: "bad approvalMode", wantErr: `field "approvalMode"`,
			patch: builtin.HeartbeatTaskPatch{
				Provided: map[string]bool{"title": true, "prompt": true, "interval": true, "approvalMode": true},
				Title:    "t", Prompt: "p", Interval: "30m", ApprovalMode: "sudo",
			},
		},
		{
			name: "project without root", wantErr: `field "workspaceRoot"`,
			patch: builtin.HeartbeatTaskPatch{
				Provided: map[string]bool{"title": true, "prompt": true, "interval": true, "scope": true},
				Title:    "t", Prompt: "p", Interval: "30m", Scope: "project",
			},
		},
		{
			name: "bad scope", wantErr: `field "scope"`,
			patch: builtin.HeartbeatTaskPatch{
				Provided: map[string]bool{"title": true, "prompt": true, "interval": true, "scope": true},
				Title:    "t", Prompt: "p", Interval: "30m", Scope: "workspace",
			},
		},
		{
			name: "bad timeWindow", wantErr: `field "timeWindowStart"`,
			patch: builtin.HeartbeatTaskPatch{
				Provided: map[string]bool{"title": true, "prompt": true, "interval": true, "timeWindowStart": true},
				Title:    "t", Prompt: "p", Interval: "30m", TimeWindowStart: "25:99",
			},
		},
		{
			name: "missing title on create", wantErr: `requires field(s) title`,
			patch: builtin.HeartbeatTaskPatch{
				Provided: map[string]bool{"prompt": true, "interval": true},
				Prompt:   "p", Interval: "30m",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{Patch: tc.patch, ExpectedRevision: view.Revision})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
			// Nothing may be persisted by a failed validation.
			fresh := mustList(t, a)
			if len(fresh.Tasks) != 0 {
				t.Fatalf("failed validation persisted a task: %+v", fresh.Tasks)
			}
		})
	}
}

func TestHeartbeatManagerSetEnabledPersists(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	created, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"title": true, "prompt": true, "interval": true},
			Title:    "t", Prompt: "p", Interval: "30m",
		},
		ExpectedRevision: view.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	off, err := a.SetEnabled(builtin.HeartbeatSetEnabledRequest{ID: created.TaskID, Enabled: false, ExpectedRevision: created.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled || off.Revision != created.Revision+1 {
		t.Fatalf("disable receipt wrong: %+v", off)
	}
	task := mustList(t, a).Tasks[0]
	if task.Enabled {
		t.Fatalf("disable not persisted: %+v", task)
	}
	if task.NextRunHint != "disabled" || task.NextRunAt != 0 {
		t.Fatalf("disabled task next-run must be zero with hint, got at=%d hint=%q", task.NextRunAt, task.NextRunHint)
	}
	on, err := a.SetEnabled(builtin.HeartbeatSetEnabledRequest{ID: created.TaskID, Enabled: true, ExpectedRevision: off.Revision})
	if err != nil || !on.Enabled {
		t.Fatalf("re-enable failed: %+v err=%v", on, err)
	}
}

func TestHeartbeatManagerMissingTaskForToggle(t *testing.T) {
	a, _ := newTestAdapter(t)
	view := mustList(t, a)
	_, err := a.SetEnabled(builtin.HeartbeatSetEnabledRequest{ID: "ghost", Enabled: false, ExpectedRevision: view.Revision})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("toggle on unknown id should fail, got %v", err)
	}
}

func TestNextHeartbeatRunAtDailySchedule(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)
	task := HeartbeatTask{ID: "d", Title: "t", Prompt: "p", Interval: "24h|daily@09:00", Enabled: true}
	next, hint := nextHeartbeatRunAt(task, now)
	if hint != "" {
		t.Fatalf("hint=%q", hint)
	}
	want := time.Date(2026, 9, 23, 9, 0, 0, 0, time.Local)
	if !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}
	// Before 09:00 the same day is the next occurrence.
	now2 := time.Date(2026, 9, 22, 8, 0, 0, 0, time.Local)
	next2, _ := nextHeartbeatRunAt(task, now2)
	if !next2.Equal(time.Date(2026, 9, 22, 9, 0, 0, 0, time.Local)) {
		t.Fatalf("same-day 09:00 missed: %v", next2)
	}
}

func TestNextHeartbeatRunAtDurationAndOverdue(t *testing.T) {
	base := time.Date(2026, 9, 22, 9, 0, 0, 0, time.Local)
	now := base.Add(10 * time.Minute)
	task := HeartbeatTask{ID: "i", Title: "t", Prompt: "p", Interval: "30m", Enabled: true, LastRunAt: base.UnixMilli()}
	next, hint := nextHeartbeatRunAt(task, now)
	if hint != "" || !next.Equal(base.Add(30*time.Minute)) {
		t.Fatalf("next=%v hint=%q", next, hint)
	}
	// Past due: the projection reports now with an overdue hint.
	nowLate := base.Add(2 * time.Hour)
	nextLate, hintLate := nextHeartbeatRunAt(task, nowLate)
	if hintLate != "overdue" || !nextLate.Equal(nowLate) {
		t.Fatalf("overdue projection wrong: %v %q", nextLate, hintLate)
	}
}

func TestNextHeartbeatRunAtCron(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local) // a Tuesday
	task := HeartbeatTask{ID: "c", Title: "t", Prompt: "p", Interval: "0 9 * * 1-5", Enabled: true, LastRunAt: now.UnixMilli()}
	next, hint := nextHeartbeatRunAt(task, now)
	if hint != "" {
		t.Fatalf("hint=%q", hint)
	}
	want := time.Date(2026, 9, 23, 9, 0, 0, 0, time.Local) // next weekday 09:00
	if !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}
}

func TestNextHeartbeatRunAtHints(t *testing.T) {
	now := time.Now()
	disabled := HeartbeatTask{ID: "x", Title: "t", Prompt: "p", Interval: "30m", Enabled: false}
	if next, hint := nextHeartbeatRunAt(disabled, now); hint != "disabled" || !next.IsZero() {
		t.Fatalf("disabled hint wrong: %v %q", next, hint)
	}
	invalid := HeartbeatTask{ID: "x", Title: "t", Prompt: "p", Interval: "nonsense", Enabled: true}
	if next, hint := nextHeartbeatRunAt(invalid, now); hint != "invalid-interval" || !next.IsZero() {
		t.Fatalf("invalid hint wrong: %v %q", next, hint)
	}
	// Never ran, no anchor, no window: due on the first tick.
	fresh := HeartbeatTask{ID: "x", Title: "t", Prompt: "p", Interval: "30m", Enabled: true}
	if next, hint := nextHeartbeatRunAt(fresh, now); hint != "due-now" || !next.Equal(now) {
		t.Fatalf("due-now projection wrong: %v %q", next, hint)
	}
}

func TestAdvanceIntoHeartbeatWindow(t *testing.T) {
	// In-window candidate passes through.
	task := HeartbeatTask{TimeWindowStart: "09:00", TimeWindowEnd: "17:00"}
	in := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)
	if got := advanceIntoHeartbeatWindow(task, in, in); !got.Equal(in) {
		t.Fatalf("in-window candidate moved: %v", got)
	}
	// Out-of-window candidate rolls to the next window opening.
	out := time.Date(2026, 9, 22, 20, 0, 0, 0, time.Local)
	want := time.Date(2026, 9, 23, 9, 0, 0, 0, time.Local)
	if got := advanceIntoHeartbeatWindow(task, out, out); !got.Equal(want) {
		t.Fatalf("out-of-window candidate=%v want %v", got, want)
	}
	// Cross-midnight window: 22:00-06:00 keeps 23:00 and moves 12:00 to 22:00 same day.
	x := HeartbeatTask{TimeWindowStart: "22:00", TimeWindowEnd: "06:00"}
	night := time.Date(2026, 9, 22, 23, 0, 0, 0, time.Local)
	if got := advanceIntoHeartbeatWindow(x, night, night); !got.Equal(night) {
		t.Fatalf("cross-midnight in-window moved: %v", got)
	}
	noon := time.Date(2026, 9, 22, 12, 0, 0, 0, time.Local)
	wantOpen := time.Date(2026, 9, 22, 22, 0, 0, 0, time.Local)
	if got := advanceIntoHeartbeatWindow(x, noon, noon); !got.Equal(wantOpen) {
		t.Fatalf("cross-midnight out-window=%v want %v", got, wantOpen)
	}
}

func TestHeartbeatUpsertClearsGoalWhenDisabled(t *testing.T) {
	a, engine := newTestAdapter(t)
	view := mustList(t, a)
	created, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"title": true, "prompt": true, "interval": true, "goalMode": true},
			Title:    "t", Prompt: "p", Interval: "30m", GoalMode: boolPtr(true),
		},
		ExpectedRevision: view.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same invariant as ReplaceTasks: switching goal mode off must clear the
	// goal (app nil here — the call must not panic and must persist).
	if _, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"id": true, "goalMode": true},
			ID:       created.TaskID, GoalMode: boolPtr(false),
		},
		ExpectedRevision: created.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	_ = engine
	fresh := mustList(t, a)
	if fresh.Tasks[0].GoalMode {
		t.Fatalf("goalMode not cleared: %+v", fresh.Tasks[0])
	}
}
