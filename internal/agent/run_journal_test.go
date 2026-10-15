package agent

// Task 419 slice 2 — write-path integration: fleet / parallel_tasks record
// their group run into internal/runjournal when a path is injected, and do
// nothing differently when it is not (the pre-419 behavior is the control:
// every test in fleet_test.go / parallel_tasks_test.go runs with no path).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/runjournal"
	"reasonix/internal/tool"
)

func openJournal(t *testing.T, path string) *runjournal.Store {
	t.Helper()
	s, err := runjournal.Open(path)
	if err != nil {
		t.Fatalf("open journal for verification: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestFleetRecordsRunJournal runs a real 2-item read-only fleet against the
// static provider and verifies the full trail: header, both dispatches, both
// settles with transcript refs, member artifacts, primary group artifact,
// terminal status.
func TestFleetRecordsRunJournal(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "runjournal.db")
	task := newTestTaskTool(t, parallelStaticProvider{}, tool.NewRegistry(), "sys", "", "", nil).
		WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithScheduler(NewSubagentScheduler(4, 4))
	f := NewFleetTool(task).WithRunJournalPath(dbPath)

	// ParentSession is required for a PERSISTED child (ephemeral runs carry no
	// ref, so no member artifact) — same shape as the parallel refs test.
	ctx := WithParentSession(withCallContext(context.Background(), "fleet-call", event.Discard, nil, false), "fleet-session")
	// Disjoint write claims: the static provider finishes without tools, so
	// no writer tool is needed (same shape as TestFleetParallelDisjointWriters).
	out, err := f.Execute(ctx, json.RawMessage(`{
		"tasks":[
			{"prompt":"research x","description":"research","write_paths":["docs/a.md"]},
			{"prompt":"write y","description":"write","write_paths":["docs/b.md"]}
		]
	}`))
	if err != nil {
		t.Fatalf("fleet Execute: %v", err)
	}
	if !strings.Contains(out, "Completed fleet of 2 tasks") {
		t.Fatalf("unexpected aggregate: %s", out)
	}

	s := openJournal(t, dbPath)
	runs, err := s.ListRuns("", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err = %v", runs, err)
	}
	run := runs[0]
	if run.Status != "completed" || run.Tool != "fleet" || run.ToolCallID != "fleet-call" ||
		run.Total != 2 || run.Label != "fleet(2)" {
		t.Fatalf("run row = %+v", run)
	}
	if run.ResultPreview == "" {
		t.Error("result preview empty — group aggregate should be recorded")
	}

	members, _ := s.ListMembers(run.ID)
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}
	for i, m := range members {
		if m.Status != "completed" {
			t.Errorf("member %d status = %s, want completed", i+1, m.Status)
		}
		if m.MemberID != "fleet-call/fleet-"+string(rune('1'+i)) {
			t.Errorf("member %d id = %q (namespace mismatch)", i+1, m.MemberID)
		}
		if m.Ref == "" {
			t.Errorf("member %d ref empty — persisted child should leave a transcript ref", i+1)
		}
	}

	events, _ := s.ListEvents(run.ID)
	kinds := make([]string, 0, len(events))
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{
		runjournal.EventRunCreated,
		runjournal.EventMemberDispatched,
		runjournal.EventMemberSettled,
		runjournal.EventArtifactPublish,
		runjournal.EventRunEnded,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("trail missing %s: %v", want, kinds)
		}
	}
	// Seq must be strictly monotonic 1..N.
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, e.Seq, i+1)
		}
	}

	// Member artifacts (id = ref) + the primary group result artifact.
	artifacts, _ := s.ListArtifacts(run.ID)
	views := runjournal.ArtifactsOf(artifacts)
	if len(views) != 3 {
		t.Fatalf("artifact views = %d, want 3 (2 members + group): %+v", len(views), views)
	}
	if !views[0].Primary || views[0].ID != run.ID+":result" {
		t.Errorf("first artifact = %+v, want primary group result", views[0])
	}

	// The projection reads back coherently (roster + timeline are pure
	// functions over exactly these rows).
	roster := runjournal.BuildRoster(run, members, events, run.CreatedAt+1000)
	if roster.Health.MembersTerminal != 2 || !roster.Health.Terminal {
		t.Errorf("roster health = %+v, want 2/2 terminal", roster.Health)
	}
	timeline := runjournal.BuildTimeline(members, events)
	if len(timeline) != len(events) {
		t.Errorf("timeline = %d entries, want %d", len(timeline), len(events))
	}
}

// TestParallelTasksRecordsRunJournal is the parallel_tasks counterpart.
func TestParallelTasksRecordsRunJournal(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "runjournal.db")
	task := newTestTaskTool(t, parallelStaticProvider{}, tool.NewRegistry(), "sys", "", "", nil)
	parallel := NewParallelTasksTool(task, tool.NewRegistry()).WithRunJournalPath(dbPath)
	ctx := WithParentSession(withCallContext(context.Background(), "parallel-call", event.Discard, nil, false), "parent-session")

	out, err := parallel.Execute(ctx, json.RawMessage(`{"tasks":[
		{"prompt":"first","description":"alpha"},
		{"prompt":"second","description":"beta"}
	]}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Completed 2 parallel tasks") {
		t.Fatalf("unexpected aggregate: %s", out)
	}

	s := openJournal(t, dbPath)
	runs, err := s.ListRuns("parent-session", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err = %v", runs, err)
	}
	run := runs[0]
	if run.Tool != "parallel_tasks" || run.ToolCallID != "parallel-call" || run.Status != "completed" {
		t.Fatalf("run row = %+v", run)
	}

	members, _ := s.ListMembers(run.ID)
	if len(members) != 2 || members[0].Label != "alpha" || members[1].Label != "beta" {
		t.Fatalf("members = %+v", members)
	}
	if members[0].MemberID != "parallel-call/sub-1" {
		t.Errorf("member 0 id = %q", members[0].MemberID)
	}
	events, _ := s.ListEvents(run.ID)
	if len(events) < 6 { // created + 2×dispatched + 2×settled + ended (+artifact)
		t.Fatalf("events = %d, want >= 6: %+v", len(events), events)
	}
}

// TestRunJournalPreflightFailureRecordsNothing pins the design rule: a fleet
// rejected at preflight never entered runFleet, so no run row exists — a run
// that did not happen must not be recorded as having happened.
func TestRunJournalPreflightFailureRecordsNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "runjournal.db")
	root := t.TempDir()
	task := newTestTaskTool(t, parallelStaticProvider{}, tool.NewRegistry(), "sys", "", "", nil).
		WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithScheduler(NewSubagentScheduler(4, 4))
	f := NewFleetTool(task).WithRunJournalPath(dbPath)

	ctx := withCallContext(context.Background(), "fleet-call", event.Discard, nil, false)
	// Two writers claiming the same path → preflight conflict, nothing starts.
	_, err := f.Execute(ctx, json.RawMessage(`{"tasks":[
		{"prompt":"a","write_paths":["same.md"]},
		{"prompt":"b","write_paths":["same.md"]}
	]}`))
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected conflict, got %v", err)
	}

	s := openJournal(t, dbPath)
	runs, _ := s.ListRuns("", 10)
	if len(runs) != 0 {
		t.Fatalf("preflight failure left %d run rows, want 0", len(runs))
	}
}

// TestRunJournalCancellationEndsRunCancelled: cancelling a live fleet must
// close the journal with the cancelled terminal (no orphan running row) and
// settle every member.
func TestRunJournalCancellationEndsRunCancelled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "runjournal.db")
	root := t.TempDir()
	prov := &fleetCancelProvider{
		started:  make(chan struct{}, 2),
		observed: make(chan struct{}, 2),
		release:  make(chan struct{}),
	}
	task := NewTaskTool(prov, nil, tool.NewRegistry(), 20, 0, 0, 0, 0, 0, 0, 0.0, "", "sys", nil, 0, "", "", nil).
		WithTranscripts(mustSubagentStore(t), root, "base", "high").
		WithScheduler(NewSubagentScheduler(2, 2))
	f := NewFleetTool(task).WithRunJournalPath(dbPath)

	ctx, cancel := context.WithCancel(withCallContext(context.Background(), "fleet-call", event.Discard, nil, false))
	done := make(chan error, 1)
	go func() {
		_, execErr := f.Execute(ctx, json.RawMessage(`{"tasks":[
			{"prompt":"first","write_paths":["first.md"]},
			{"prompt":"second","write_paths":["second.md"]}
		]}`))
		done <- execErr
	}()
	for range 2 {
		select {
		case <-prov.started:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for provider start")
		}
	}
	cancel()
	for range 2 {
		select {
		case <-prov.observed:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for provider cancellation")
		}
	}
	close(prov.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("fleet did not return")
	}

	s := openJournal(t, dbPath)
	runs, err := s.ListRuns("", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err = %v", runs, err)
	}
	if runs[0].Status != "cancelled" {
		t.Errorf("run status = %s, want cancelled (no orphan running row)", runs[0].Status)
	}
	members, _ := s.ListMembers(runs[0].ID)
	for i, m := range members {
		if !terminalJournalStatus(m.Status) {
			t.Errorf("member %d status = %s, want terminal", i+1, m.Status)
		}
	}
	events, _ := s.ListEvents(runs[0].ID)
	last := events[len(events)-1]
	if last.Kind != runjournal.EventRunEnded {
		t.Errorf("last event = %s, want run_ended", last.Kind)
	}
}

// TestRunJournalOpenFailureDoesNotBreakFleet: a journal path that cannot be
// created (parent is a file) must degrade to "no recording" with one log
// line — the fleet itself still completes (the recorder never fails the run).
func TestRunJournalOpenFailureDoesNotBreakFleet(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	dbPath := filepath.Join(blocker, "sub", "runjournal.db")

	task := newTestTaskTool(t, parallelStaticProvider{}, tool.NewRegistry(), "sys", "", "", nil)
	parallel := NewParallelTasksTool(task, tool.NewRegistry()).WithRunJournalPath(dbPath)
	ctx := withCallContext(context.Background(), "parallel-call", event.Discard, nil, false)

	out, err := parallel.Execute(ctx, json.RawMessage(`{"tasks":[
		{"prompt":"first"},{"prompt":"second"}
	]}`))
	if err != nil {
		t.Fatalf("fleet/parallel must survive journal failure, got: %v", err)
	}
	if !strings.Contains(out, "Completed 2 parallel tasks") {
		t.Fatalf("unexpected aggregate: %s", out)
	}
}

// TestRunJournalDisabledPathIsNoop pins the "" control: with no path the
// recorder is nil and every method is a no-op (pre-419 behavior).
func TestRunJournalDisabledPathIsNoop(t *testing.T) {
	var rec *runRecorder
	rec.begin(runjournal.RunSpec{Tool: "fleet"})
	rec.dispatched(1)
	rec.settled(runjournal.MemberSettlement{Idx: 1, Status: "completed"})
	rec.artifact(runjournal.ArtifactSpec{ID: "x"})
	rec.settleRemaining([]runjournal.MemberSettlement{{Idx: 2, Status: "skipped"}})
	rec.end("completed", "preview")
	if rec.runIDOf() != "" {
		t.Fatalf("nil recorder has run id %q", rec.runIDOf())
	}
	if got := RunJournalDBPath(""); got != "" {
		t.Fatalf("RunJournalDBPath(empty) = %q, want empty", got)
	}
	if got := RunJournalDBPath("/sessions"); !strings.HasSuffix(got, "runjournal.db") {
		t.Fatalf("RunJournalDBPath = %q, want .../runjournal.db", got)
	}
}

// TestJournalErrorTextAndStatusMap pins the two small adapters.
func TestJournalErrorTextAndStatusMap(t *testing.T) {
	if errorText(nil) != "" {
		t.Error("nil error must render empty")
	}
	if errorText(errors.New("boom")) != "boom" {
		t.Error("error text lost")
	}
	if journalStatusForRunErr(context.Canceled) != "cancelled" {
		t.Error("context.Canceled must map to cancelled")
	}
	if journalStatusForRunErr(errors.New("x")) != "failed" {
		t.Error("plain error must map to failed")
	}
}

func terminalJournalStatus(s string) bool {
	switch s {
	case "completed", "failed", "cancelled", "skipped":
		return true
	}
	return false
}
