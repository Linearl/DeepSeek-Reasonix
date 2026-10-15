package runjournal

import "testing"

// The projection rules are pinned with hand-built rows (design §5): no
// database, no clock — every input is explicit, so a regression in status
// precedence, absence readings, or time clamping fails here rather than in
// an expensive fixture.

func TestBuildRosterDerivesSpansHealthAndStreak(t *testing.T) {
	run := RunRow{ID: "r1", Status: "running", Total: 3}
	members := []MemberRow{
		{Idx: 1, MemberID: "m1", Label: "research", Status: "completed", Ref: "ref-1"},
		{Idx: 2, MemberID: "m2", Label: "implement", Status: "running"},
		{Idx: 3, MemberID: "m3", Label: "review", Status: "pending"},
	}
	events := []EventRow{
		{Seq: 1, At: 100, Kind: EventRunCreated},
		{Seq: 2, At: 200, Kind: EventMemberDispatched, MemberIdx: 1},
		{Seq: 3, At: 300, Kind: EventMemberDispatched, MemberIdx: 2},
		{Seq: 4, At: 400, Kind: EventMemberSettled, MemberIdx: 1, Detail: "completed ref=ref-1"},
		{Seq: 5, At: 500, Kind: EventMemberDispatched, MemberIdx: 3},
	}
	view := BuildRoster(run, members, events, 600)

	if view.Health.Terminal {
		t.Error("running run reported terminal")
	}
	if view.Health.LastProgressAt != 500 {
		t.Errorf("lastProgressAt = %d, want 500 (latest progress event)", view.Health.LastProgressAt)
	}
	if view.Health.MembersTerminal != 1 || view.Health.MembersTotal != 3 {
		t.Errorf("members progress = %d/%d, want 1/3", view.Health.MembersTerminal, view.Health.MembersTotal)
	}
	if view.Health.LeftoverRunning != 0 {
		t.Errorf("leftoverRunning = %d on a live run, want 0 (running members are normal)", view.Health.LeftoverRunning)
	}
	if view.Health.ConsecutiveFailures != 0 {
		t.Errorf("consecutiveFailures = %d, want 0", view.Health.ConsecutiveFailures)
	}
	if len(view.Members) != 3 {
		t.Fatalf("members = %d, want 3", len(view.Members))
	}
	m1 := view.Members[0]
	if m1.StartedAt != 200 || m1.EndedAt != 400 {
		t.Errorf("member 1 span = [%d,%d], want [200,400] (trail-derived)", m1.StartedAt, m1.EndedAt)
	}
	if m2 := view.Members[1]; m2.StartedAt != 300 || m2.EndedAt != 0 {
		t.Errorf("member 2 span = [%d,%d], want [300,0] (no settle yet)", m2.StartedAt, m2.EndedAt)
	}
}

func TestBuildRosterTerminalRunCountsLeftoverRunning(t *testing.T) {
	run := RunRow{ID: "r1", Status: "completed", Total: 2}
	members := []MemberRow{
		{Idx: 1, Status: "completed"},
		// Stale row: the run ended but this member never settled (crashed
		// writer) — the health face must surface it, not paper over it.
		{Idx: 2, Status: "running"},
	}
	view := BuildRoster(run, members, []EventRow{
		{Seq: 1, At: 10, Kind: EventRunCreated},
		{Seq: 2, At: 20, Kind: EventMemberSettled, MemberIdx: 1, Detail: "completed"},
		{Seq: 3, At: 30, Kind: EventRunEnded, Detail: "completed"},
	}, 40)

	if !view.Health.Terminal {
		t.Error("completed run not reported terminal")
	}
	if view.Health.LeftoverRunning != 1 {
		t.Errorf("leftoverRunning = %d, want 1", view.Health.LeftoverRunning)
	}
	// run_ended is a phase transition, not progress: lastProgress stays at
	// the settle (30 would be wrong — nothing moved after that).
	if view.Health.LastProgressAt != 20 {
		t.Errorf("lastProgressAt = %d, want 20 (run_ended is not progress)", view.Health.LastProgressAt)
	}
}

func TestBuildRosterTrailingFailureStreak(t *testing.T) {
	// Settlement order: completed, failed, failed → streak of 2 at the tail.
	members := []MemberRow{
		{Idx: 1, Status: "completed"},
		{Idx: 2, Status: "failed"},
		{Idx: 3, Status: "failed"},
	}
	events := []EventRow{
		{Seq: 1, At: 10, Kind: EventMemberSettled, MemberIdx: 1, Detail: "completed ref=r1"},
		{Seq: 2, At: 20, Kind: EventMemberSettled, MemberIdx: 2, Detail: "failed err=boom"},
		{Seq: 3, At: 30, Kind: EventMemberSettled, MemberIdx: 3, Detail: "failed err=boom"},
	}
	view := BuildRoster(RunRow{Status: "failed", Total: 3}, members, events, 40)
	if view.Health.ConsecutiveFailures != 2 {
		t.Errorf("consecutiveFailures = %d, want 2 (contiguous failed tail)", view.Health.ConsecutiveFailures)
	}

	// failed → cancelled → failed: the cancelled settle in the middle breaks
	// the streak, so the trailing run counts only the last failure.
	events = []EventRow{
		{Seq: 1, At: 10, Kind: EventMemberSettled, MemberIdx: 1, Detail: "failed err=boom"},
		{Seq: 2, At: 20, Kind: EventMemberSettled, MemberIdx: 2, Detail: "cancelled err=ctx"},
		{Seq: 3, At: 30, Kind: EventMemberSettled, MemberIdx: 3, Detail: "failed err=boom"},
	}
	members = []MemberRow{
		{Idx: 1, Status: "failed"},
		{Idx: 2, Status: "cancelled"},
		{Idx: 3, Status: "failed"},
	}
	view = BuildRoster(RunRow{Status: "failed", Total: 3}, members, events, 40)
	if view.Health.ConsecutiveFailures != 1 {
		t.Errorf("consecutiveFailures = %d, want 1 (cancelled breaks the streak)", view.Health.ConsecutiveFailures)
	}
}

func TestBuildRosterClampsFutureEventTimesToNow(t *testing.T) {
	// Clock skew: an event stamped beyond the read time reads as now, never
	// as a future activity the UI cannot render.
	members := []MemberRow{{Idx: 1, Status: "running"}}
	events := []EventRow{
		{Seq: 1, At: 9999, Kind: EventMemberDispatched, MemberIdx: 1},
	}
	view := BuildRoster(RunRow{Status: "running", Total: 1}, members, events, 500)
	if view.Members[0].StartedAt != 500 {
		t.Errorf("startedAt = %d, want clamped to now=500", view.Members[0].StartedAt)
	}
	if view.Health.LastProgressAt != 500 {
		t.Errorf("lastProgressAt = %d, want 500", view.Health.LastProgressAt)
	}
}

func TestBuildRosterEmptyTrailIsAnswerableNotZeroish(t *testing.T) {
	// A run whose events vanished (registry gap) still answers: empty roster
	// is a valid answer, health fields are zero-valued but Total carries.
	view := BuildRoster(RunRow{Status: "running", Total: 0}, nil, nil, 100)
	if view.Members == nil {
		t.Error("members must be a non-nil empty slice for JSON []")
	}
	if view.Health.MembersTotal != 0 || view.Health.LastProgressAt != 0 {
		t.Errorf("empty-trail health = %+v", view.Health)
	}
}

func TestBuildTimelineLabelsAndSettledStatus(t *testing.T) {
	members := []MemberRow{
		{Idx: 1, Label: "research"},
		{Idx: 2, Label: "implement"},
	}
	events := []EventRow{
		{Seq: 1, At: 10, Kind: EventRunCreated, Detail: "tool=fleet members=2"},
		{Seq: 2, At: 20, Kind: EventMemberDispatched, MemberIdx: 1},
		{Seq: 3, At: 30, Kind: EventMemberSettled, MemberIdx: 1, Detail: "completed ref=ref-1"},
		{Seq: 4, At: 40, Kind: EventRunEnded, Detail: "completed"},
	}
	tl := BuildTimeline(members, events)
	if len(tl) != 4 {
		t.Fatalf("timeline = %d entries, want 4", len(tl))
	}
	if tl[0].MemberLabel != "" || tl[1].MemberLabel != "research" {
		t.Errorf("labels = [%q %q], want [\"\" \"research\"]", tl[0].MemberLabel, tl[1].MemberLabel)
	}
	if tl[2].Status != "completed" || tl[2].MemberLabel != "research" {
		t.Errorf("settled entry = %+v (status parsed, label joined)", tl[2])
	}
	if tl[3].Status != "" {
		t.Errorf("run_ended entry carries status %q, want \"\" (status parsing is settled-only)", tl[3].Status)
	}
	// Seq order is preserved — the UI must not re-sort.
	for i := range tl {
		if tl[i].Seq != int64(i+1) {
			t.Errorf("entry %d seq = %d, want %d", i, tl[i].Seq, i+1)
		}
	}
}

func TestArtifactsOfPrimaryFirstAndLatestWins(t *testing.T) {
	rows := []ArtifactRow{
		{RunID: "r", ID: "a1", Kind: "result", Version: 1, Title: "a1-v1", MemberIdx: 1, Status: "completed", PublishedAt: 10},
		{RunID: "r", ID: "a2", Kind: "result", Version: 1, Title: "a2-v1", MemberIdx: 2, Status: "completed", PublishedAt: 20},
		{RunID: "r", ID: "a1", Kind: "result", Version: 2, Title: "a1-v2", MemberIdx: 1, Primary: true, Status: "completed", PublishedAt: 30},
	}
	views := ArtifactsOf(rows)
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2 (merged by id)", len(views))
	}
	if views[0].ID != "a1" || !views[0].Primary {
		t.Errorf("first view = %+v, want primary a1 hoisted", views[0])
	}
	if views[0].Title != "a1-v2" || views[0].Version != 2 {
		t.Errorf("a1 view = %+v, want latest version's fields lifted", views[0])
	}
	if views[1].ID != "a2" || views[1].Primary {
		t.Errorf("second view = %+v, want non-primary a2 in publish order", views[1])
	}
}

func TestArtifactsOfDropsNonCompleted(t *testing.T) {
	// Defensive read-side filter: a failed publish never shows up, even if a
	// future writer put the row in the table.
	rows := []ArtifactRow{
		{RunID: "r", ID: "a1", Version: 1, Title: "good", Status: "completed", PublishedAt: 1},
		{RunID: "r", ID: "a2", Version: 1, Title: "bad", Status: "failed", PublishedAt: 2},
	}
	views := ArtifactsOf(rows)
	if len(views) != 1 || views[0].ID != "a1" {
		t.Fatalf("views = %+v, want only the completed one", views)
	}
}

func TestArtifactsOfEmptyIsNonNil(t *testing.T) {
	views := ArtifactsOf(nil)
	if views == nil {
		t.Fatal("ArtifactsOf must return an empty non-nil slice for JSON []")
	}
}

func TestRunStatusTerminal(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{{"running", false}, {"completed", true}, {"failed", true}, {"cancelled", true}, {"", false}} {
		if got := RunStatusTerminal(tc.status); got != tc.want {
			t.Errorf("RunStatusTerminal(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}
