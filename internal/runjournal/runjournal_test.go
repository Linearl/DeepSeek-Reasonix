package runjournal

import (
	"path/filepath"
	"strings"
	"testing"
)

// newTestStore opens a journal in a temp dir with a fixed clock the tests
// advance explicitly — projections must never depend on wall time.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "runjournal.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func tick(s *Store, at int64) {
	s.SetClock(func() int64 { return at })
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open("  "); err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestBeginRunWritesRunMembersAndCreatedEvent(t *testing.T) {
	s := newTestStore(t)
	tick(s, 1000)

	runID, err := s.BeginRun(RunSpec{
		ParentSession: "sess-a", ToolCallID: "call-1", Tool: "fleet", Label: "fleet(2)",
		Members: []MemberSpec{
			{Idx: 1, MemberID: "g/fleet-1", Label: "research", Profile: "explore"},
			{Idx: 2, MemberID: "g/fleet-2", Label: "implement"},
		},
	})
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	if runID == "" {
		t.Fatal("empty run id")
	}

	run, err := s.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != "running" || run.Total != 2 || run.ParentSession != "sess-a" ||
		run.ToolCallID != "call-1" || run.Tool != "fleet" {
		t.Fatalf("run row = %+v", run)
	}

	members, err := s.ListMembers(runID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %v, err = %v", members, err)
	}
	if members[0].Status != "pending" || members[0].MemberID != "g/fleet-1" {
		t.Fatalf("member 1 = %+v", members[0])
	}

	events, err := s.ListEvents(runID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, err = %v", events, err)
	}
	if events[0].Kind != EventRunCreated || events[0].At != 1000 || events[0].Seq != 1 {
		t.Fatalf("created event = %+v", events[0])
	}
}

func TestBeginRunRequiresTool(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.BeginRun(RunSpec{ParentSession: "x"}); err == nil {
		t.Fatal("expected error for missing tool")
	}
}

func TestEventSeqMonotonicAcrossAllKinds(t *testing.T) {
	s := newTestStore(t)
	tick(s, 1)
	runID, err := s.BeginRun(RunSpec{Tool: "fleet", Members: []MemberSpec{{Idx: 1, MemberID: "m1"}}})
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	tick(s, 2)
	if err := s.Dispatched(runID, 1); err != nil {
		t.Fatalf("Dispatched: %v", err)
	}
	tick(s, 3)
	if err := s.Settled(runID, MemberSettlement{Idx: 1, Status: "completed", Ref: "ref-1", Preview: "hi"}); err != nil {
		t.Fatalf("Settled: %v", err)
	}
	tick(s, 4)
	if err := s.PublishArtifact(runID, ArtifactSpec{ID: "ref-1", Title: "answer"}); err != nil {
		t.Fatalf("PublishArtifact: %v", err)
	}
	tick(s, 5)
	if err := s.End(runID, "completed", "aggregate"); err != nil {
		t.Fatalf("End: %v", err)
	}

	events, err := s.ListEvents(runID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	want := []string{EventRunCreated, EventMemberDispatched, EventMemberSettled, EventArtifactPublish, EventRunEnded}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, e := range events {
		if e.Kind != want[i] {
			t.Errorf("event %d kind = %s, want %s", i, e.Kind, want[i])
		}
		if e.Seq != int64(i+1) {
			t.Errorf("event %d seq = %d, want %d", i, e.Seq, i+1)
		}
	}
}

func TestSettledFirstWriteWins(t *testing.T) {
	s := newTestStore(t)
	tick(s, 1)
	runID, _ := s.BeginRun(RunSpec{Tool: "fleet", Members: []MemberSpec{{Idx: 1, MemberID: "m1"}}})

	tick(s, 10)
	if err := s.Settled(runID, MemberSettlement{Idx: 1, Status: "completed", Ref: "real", Preview: "answer"}); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	// A cancelled drain racing after the real result must not overwrite it.
	tick(s, 20)
	if err := s.Settled(runID, MemberSettlement{Idx: 1, Status: "cancelled", Error: "drained"}); err != nil {
		t.Fatalf("second settle: %v", err)
	}

	members, _ := s.ListMembers(runID)
	if members[0].Status != "completed" || members[0].Ref != "real" {
		t.Fatalf("member overwritten: %+v", members[0])
	}
	events, _ := s.ListEvents(runID)
	settled := 0
	for _, e := range events {
		if e.Kind == EventMemberSettled {
			settled++
		}
	}
	if settled != 1 {
		t.Fatalf("settled events = %d, want 1 (second settle must be silent)", settled)
	}
}

func TestEndIsIdempotentOnSecondCall(t *testing.T) {
	s := newTestStore(t)
	tick(s, 1)
	runID, _ := s.BeginRun(RunSpec{Tool: "parallel_tasks", Members: []MemberSpec{{Idx: 1, MemberID: "m1"}}})

	tick(s, 100)
	if err := s.End(runID, "completed", "first result"); err != nil {
		t.Fatalf("first End: %v", err)
	}
	tick(s, 200)
	if err := s.End(runID, "failed", "late overwrite"); err != nil {
		t.Fatalf("second End: %v", err)
	}

	run, err := s.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != "completed" || run.ResultPreview != "first result" {
		t.Fatalf("run after double End = %+v (terminal status must stick)", run)
	}
	events, _ := s.ListEvents(runID)
	runEnded := 0
	for _, e := range events {
		if e.Kind == EventRunEnded {
			runEnded++
		}
	}
	if runEnded != 2 {
		t.Fatalf("run_ended events = %d, want 2 (the trail records both attempts)", runEnded)
	}
}

func TestEndBoundsResultPreview(t *testing.T) {
	s := newTestStore(t)
	tick(s, 1)
	runID, _ := s.BeginRun(RunSpec{Tool: "fleet", Members: []MemberSpec{{Idx: 1, MemberID: "m1"}}})

	big := strings.Repeat("x", maxResultPreviewBytes+1024)
	if err := s.End(runID, "completed", big); err != nil {
		t.Fatalf("End: %v", err)
	}
	run, _ := s.GetRun(runID)
	if len(run.ResultPreview) > maxResultPreviewBytes+len("…") {
		t.Fatalf("result preview not bounded: %d bytes", len(run.ResultPreview))
	}
}

func TestPublishArtifactReplaySafeAndVersioned(t *testing.T) {
	s := newTestStore(t)
	tick(s, 1)
	runID, _ := s.BeginRun(RunSpec{Tool: "fleet", Members: []MemberSpec{{Idx: 1, MemberID: "m1"}}})

	spec := ArtifactSpec{ID: "ref-1", Title: "v1", MemberIdx: 1, Ref: "ref-1"}
	tick(s, 10)
	if err := s.PublishArtifact(runID, spec); err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	// Replay of the same (id, version) must not create a second row.
	if err := s.PublishArtifact(runID, spec); err != nil {
		t.Fatalf("replay: %v", err)
	}
	spec.Version = 2
	spec.Title = "v2"
	spec.Primary = true
	tick(s, 11)
	if err := s.PublishArtifact(runID, spec); err != nil {
		t.Fatalf("publish v2: %v", err)
	}

	rows, err := s.ListArtifacts(runID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("artifact rows = %d, want 2 (replay collapsed): %+v", len(rows), rows)
	}
	views := ArtifactsOf(rows)
	if len(views) != 1 || views[0].Title != "v2" || views[0].Version != 2 || !views[0].Primary {
		t.Fatalf("merged view = %+v (latest lifted, primary sticky)", views)
	}
}

func TestPublishArtifactRequiresID(t *testing.T) {
	s := newTestStore(t)
	tick(s, 1)
	runID, _ := s.BeginRun(RunSpec{Tool: "fleet", Members: []MemberSpec{{Idx: 1, MemberID: "m1"}}})
	if err := s.PublishArtifact(runID, ArtifactSpec{Title: "no id"}); err == nil {
		t.Fatal("expected error for empty artifact id")
	}
}

func TestListRunsOrdersNewestFirstAndFiltersBySession(t *testing.T) {
	s := newTestStore(t)
	tick(s, 100)
	if _, err := s.BeginRun(RunSpec{ParentSession: "sess-a", Tool: "fleet"}); err != nil {
		t.Fatalf("run a: %v", err)
	}
	tick(s, 200)
	if _, err := s.BeginRun(RunSpec{ParentSession: "sess-b", Tool: "parallel_tasks"}); err != nil {
		t.Fatalf("run b: %v", err)
	}
	tick(s, 300)
	if _, err := s.BeginRun(RunSpec{ParentSession: "sess-a", Tool: "fleet"}); err != nil {
		t.Fatalf("run c: %v", err)
	}

	all, err := s.ListRuns("", 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("all runs = %v, err = %v", all, err)
	}
	if all[0].CreatedAt != 300 || all[2].CreatedAt != 100 {
		t.Fatalf("order = [%d %d %d], want newest first", all[0].CreatedAt, all[1].CreatedAt, all[2].CreatedAt)
	}

	filtered, err := s.ListRuns("sess-a", 10)
	if err != nil || len(filtered) != 2 {
		t.Fatalf("sess-a runs = %v, err = %v", filtered, err)
	}
}

func TestGetRunUnknownID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetRun("run_missing"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestNilStoreMethodsAreNoop(t *testing.T) {
	// The agent-side recorder treats a nil store as "journaling disabled":
	// every write must be a no-op, never a panic (design §6).
	var s *Store
	if err := s.Dispatched("r", 1); err != nil {
		t.Fatalf("Dispatched on nil: %v", err)
	}
	if err := s.Settled("r", MemberSettlement{Idx: 1, Status: "completed"}); err != nil {
		t.Fatalf("Settled on nil: %v", err)
	}
	if err := s.PublishArtifact("r", ArtifactSpec{ID: "a"}); err != nil {
		t.Fatalf("PublishArtifact on nil: %v", err)
	}
	if err := s.End("r", "completed", ""); err != nil {
		t.Fatalf("End on nil: %v", err)
	}
	if runs, err := s.ListRuns("", 5); runs != nil || err != nil {
		t.Fatalf("ListRuns on nil = %v, %v", runs, err)
	}
	if _, err := s.BeginRun(RunSpec{Tool: "fleet"}); err == nil {
		t.Fatal("BeginRun on nil must error")
	}
}

func TestReopenExistingDatabaseKeepsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runjournal.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	tick(s, 7)
	runID, err := s.BeginRun(RunSpec{ParentSession: "sess", Tool: "fleet"})
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	if err := s.End(runID, "completed", ""); err != nil {
		t.Fatalf("End: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	runs, err := s2.ListRuns("sess", 10)
	if err != nil || len(runs) != 1 || runs[0].ID != runID || runs[0].Status != "completed" {
		t.Fatalf("runs after reopen = %v, err = %v", runs, err)
	}
	// Migration is idempotent: a second Open on the same file must not fail.
	s3, err := Open(path)
	if err != nil {
		t.Fatalf("third open (idempotent migrate): %v", err)
	}
	_ = s3.Close()
}
