package main

// Task 419 slice 3a — bridge read-face tests. TestMain sandboxes the state
// home, so config.SessionDir() points into a scratch dir: the test seeds a
// journal there, then exercises the two Wails methods through the same
// open-read-close path production uses.

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/runjournal"
)

func seedRunJournal(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll(config.SessionDir(), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(config.SessionDir(), "runjournal.db")
	// Remove a leftover from another test in this package run.
	_ = os.Remove(path)
	store, err := runjournal.Open(path)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer store.Close()
	runID, err := store.BeginRun(runjournal.RunSpec{
		ParentSession: "sess-bridge", ToolCallID: "call-1", Tool: "fleet", Label: "fleet(2)",
		Members: []runjournal.MemberSpec{
			{Idx: 1, MemberID: "call-1/fleet-1", Label: "research"},
			{Idx: 2, MemberID: "call-1/fleet-2", Label: "implement"},
		},
	})
	if err != nil {
		t.Fatalf("begin run: %v", err)
	}
	if err := store.Dispatched(runID, 1); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := store.Settled(runID, runjournal.MemberSettlement{
		Idx: 1, Status: "completed", Ref: "sa_ref_1", Preview: "done",
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	// Artifact publishing is the agent recorder's job (member ref at settle,
	// group aggregate at end) — the raw store does not auto-publish, so the
	// seed mirrors those two calls.
	if err := store.PublishArtifact(runID, runjournal.ArtifactSpec{
		ID: "sa_ref_1", MemberIdx: 1, Ref: "sa_ref_1", Title: "research",
	}); err != nil {
		t.Fatalf("publish member artifact: %v", err)
	}
	if err := store.PublishArtifact(runID, runjournal.ArtifactSpec{
		ID: runID + ":result", Title: "group result", Primary: true,
	}); err != nil {
		t.Fatalf("publish group artifact: %v", err)
	}
	if err := store.End(runID, "completed", "aggregate preview"); err != nil {
		t.Fatalf("end: %v", err)
	}
	return runID
}

func TestListRunJournalRunsReturnsSeededRun(t *testing.T) {
	runID := seedRunJournal(t)

	res := (&App{}).ListRunJournalRuns("sess-bridge", 10)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if len(res.Runs) != 1 {
		t.Fatalf("runs = %+v, want 1", res.Runs)
	}
	got := res.Runs[0]
	if got.ID != runID || got.Tool != "fleet" || got.Status != "completed" || got.Total != 2 {
		t.Fatalf("summary = %+v", got)
	}
	if got.CreatedAt <= 0 || res.GeneratedAt <= 0 {
		t.Fatalf("timestamps missing: run=%d envelope=%d", got.CreatedAt, res.GeneratedAt)
	}
}

func TestListRunJournalRunsFilterMissIsEmptyNotError(t *testing.T) {
	seedRunJournal(t)
	res := (&App{}).ListRunJournalRuns("no-such-session", 10)
	if res.Error != "" {
		t.Fatalf("filter miss must not error: %s", res.Error)
	}
	if len(res.Runs) != 0 {
		t.Fatalf("runs = %+v, want empty", res.Runs)
	}
	if res.Runs == nil {
		t.Fatal("runs must be non-nil empty slice for JSON []")
	}
}

func TestGetRunJournalRunDetailProjectsAllSections(t *testing.T) {
	runID := seedRunJournal(t)

	res := (&App{}).GetRunJournalRunDetail(runID)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if res.Run.ID != runID || res.Run.Label != "fleet(2)" {
		t.Fatalf("run = %+v", res.Run)
	}
	// Roster: member 1 settled with span; member 2 left running → the health
	// face reports it as leftover only when the run is terminal — it IS
	// terminal here (End ran), so leftoverRunning = 1.
	if len(res.Members) != 2 || res.Members[0].Status != "completed" || res.Members[0].Ref != "sa_ref_1" {
		t.Fatalf("members = %+v", res.Members)
	}
	if res.Members[0].StartedAt == 0 || res.Members[0].EndedAt == 0 {
		t.Fatalf("member 1 span missing: %+v", res.Members[0])
	}
	if !res.Health.Terminal || res.Health.LeftoverRunning != 1 || res.Health.MembersTerminal != 1 {
		t.Fatalf("health = %+v", res.Health)
	}
	// Timeline: created + dispatched + settled + artifact + ended, seq 1..N.
	if len(res.Timeline) < 4 {
		t.Fatalf("timeline = %+v", res.Timeline)
	}
	for i, e := range res.Timeline {
		if e.Seq != int64(i+1) {
			t.Fatalf("timeline seq not monotonic at %d: %+v", i, e)
		}
	}
	if res.Timeline[len(res.Timeline)-1].Kind != runjournal.EventRunEnded {
		t.Fatalf("last entry = %+v, want run_ended", res.Timeline[len(res.Timeline)-1])
	}
	// Artifacts: the member ref published at settle + the primary group
	// result published at end.
	if len(res.Artifacts) < 1 {
		t.Fatalf("artifacts = %+v", res.Artifacts)
	}
	if !res.Artifacts[0].Primary {
		t.Fatalf("first artifact must be the primary group result: %+v", res.Artifacts[0])
	}
}

func TestGetRunJournalRunDetailUnknownIDSurfacesError(t *testing.T) {
	seedRunJournal(t)
	res := (&App{}).GetRunJournalRunDetail("run_missing")
	if res.Error == "" {
		t.Fatal("unknown run id must surface an error, not an empty success")
	}
}

func TestRunJournalBridgeDegradationIsVisible(t *testing.T) {
	// Break the journal location: put a file where the session dir must be,
	// so Open fails. The bridge must return an empty result WITH the reason —
	// the panel can then say "journal unavailable" instead of "no runs".
	// (TestMain pins the state home; replacing the dir is package-wide state,
	// so this test runs in a subprocess-free window after removing the db and
	// making the session dir read-only-ish: a file in place of the dir.)
	dir := config.SessionDir()
	_ = os.RemoveAll(dir)
	if err := os.WriteFile(dir, []byte("not a dir"), 0o644); err != nil {
		t.Skipf("cannot replace session dir on this platform: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(dir)
	})

	res := (&App{}).ListRunJournalRuns("", 10)
	if res.Error == "" {
		t.Fatal("degraded read must surface the reason")
	}
	if len(res.Runs) != 0 {
		t.Fatalf("degraded read must return empty runs: %+v", res.Runs)
	}
	detail := (&App{}).GetRunJournalRunDetail("run_any")
	if detail.Error == "" {
		t.Fatal("degraded detail must surface the reason")
	}

	// Restore for any later test in the package.
	_ = os.Remove(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("restore session dir: %v", err)
	}
}
