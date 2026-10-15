package main

// Task 419 slice 3a — run journal read face: the Wails bridge methods the
// frontend timeline panel will call (design §7).
//
// Binding pattern follows collab_view_app.go (task 409) and
// collab_channel_app.go (task 803): every call is self-contained — open,
// read, close — so no cross-call file handle survives and a crashed panel
// leaves nothing behind. Read-only: the journal is observational, and the
// desktop never writes it (writes belong to the agent-side recorder).
//
// Degradation is visible, not silent (design §3): an unopenable database
// yields an empty list with the error surfaced in the result envelope, so
// the panel can say "journal unavailable" instead of pretending the history
// is empty.

import (
	"path/filepath"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/runjournal"
)

// RunJournalRunSummary is one list row: the runs-table narrow projection
// (no member bodies, no events — the list face stays cheap).
type RunJournalRunSummary struct {
	ID            string `json:"id"`
	ParentSession string `json:"parentSession"`
	Tool          string `json:"tool"`
	Label         string `json:"label"`
	Status        string `json:"status"`
	Total         int    `json:"total"`
	CreatedAt     int64  `json:"createdAt"`
	EndedAt       int64  `json:"endedAt"`
}

// RunJournalMemberView is one roster row plus its trail-derived span.
type RunJournalMemberView struct {
	Idx       int    `json:"idx"`
	MemberID  string `json:"memberId"`
	Label     string `json:"label"`
	Profile   string `json:"profile"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	Error     string `json:"error"`
	Preview   string `json:"preview"`
	StartedAt int64  `json:"startedAt"`
	EndedAt   int64  `json:"endedAt"`
}

// RunJournalHealthView is the run-level "is it still moving" summary.
type RunJournalHealthView struct {
	Terminal            bool  `json:"terminal"`
	LastProgressAt      int64 `json:"lastProgressAt"`
	ConsecutiveFailures int   `json:"consecutiveFailures"`
	LeftoverRunning     int   `json:"leftoverRunning"`
	MembersTerminal     int   `json:"membersTerminal"`
	MembersTotal        int   `json:"membersTotal"`
}

// RunJournalTimelineEntry is one time-ordered line (the panel renders these
// directly — BuildTimeline output, JSON-tagged).
type RunJournalTimelineEntry struct {
	Seq         int64  `json:"seq"`
	At          int64  `json:"at"`
	Kind        string `json:"kind"`
	MemberIdx   int    `json:"memberIdx"`
	MemberLabel string `json:"memberLabel"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
}

// RunJournalArtifactView is one merged artifact row (ArtifactsOf output).
type RunJournalArtifactView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Ref         string `json:"ref"`
	MemberIdx   int    `json:"memberIdx"`
	Version     int    `json:"version"`
	Primary     bool   `json:"primary"`
	PublishedAt int64  `json:"publishedAt"`
}

// RunJournalRunListResult is the list-face envelope. Error is informational:
// a degraded read returns the empty list WITH the reason, so the panel can
// distinguish "no runs yet" from "journal unavailable".
type RunJournalRunListResult struct {
	Runs        []RunJournalRunSummary `json:"runs"`
	GeneratedAt int64                  `json:"generatedAt"`
	Error       string                 `json:"error,omitempty"`
}

// RunJournalRunDetailResult is the detail face: roster + health + timeline +
// artifacts in one call (the panel renders all four sections).
type RunJournalRunDetailResult struct {
	Run       RunJournalRunSummary      `json:"run"`
	Members   []RunJournalMemberView    `json:"members"`
	Health    RunJournalHealthView      `json:"health"`
	Timeline  []RunJournalTimelineEntry `json:"timeline"`
	Artifacts []RunJournalArtifactView  `json:"artifacts"`
	Error     string                    `json:"error,omitempty"`
}

// openRunJournal builds the per-session journal store the same way boot
// wires the writer: config.SessionDir()/runjournal.db.
func openRunJournal() (*runjournal.Store, error) {
	return runjournal.Open(filepath.Join(config.SessionDir(), "runjournal.db"))
}

// ListRunJournalRuns enumerates the session's recorded runs, newest first.
// parentSession empty = across sessions (the desktop overview face).
func (a *App) ListRunJournalRuns(parentSession string, limit int) RunJournalRunListResult {
	out := RunJournalRunListResult{
		Runs:        []RunJournalRunSummary{},
		GeneratedAt: time.Now().UnixMilli(),
	}
	store, err := openRunJournal()
	if err != nil {
		// Visible degradation: empty list + reason. Never a silent fallback.
		out.Error = err.Error()
		return out
	}
	defer store.Close()
	runs, err := store.ListRuns(parentSession, limit)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	for _, r := range runs {
		out.Runs = append(out.Runs, RunJournalRunSummary{
			ID:            r.ID,
			ParentSession: r.ParentSession,
			Tool:          r.Tool,
			Label:         r.Label,
			Status:        r.Status,
			Total:         r.Total,
			CreatedAt:     r.CreatedAt,
			EndedAt:       r.EndedAt,
		})
	}
	return out
}

// GetRunJournalRunDetail loads one run's roster + health + timeline +
// artifacts (projections are pure functions over the same rows — design §5).
func (a *App) GetRunJournalRunDetail(runID string) RunJournalRunDetailResult {
	out := RunJournalRunDetailResult{
		Members:   []RunJournalMemberView{},
		Timeline:  []RunJournalTimelineEntry{},
		Artifacts: []RunJournalArtifactView{},
	}
	store, err := openRunJournal()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer store.Close()

	run, err := store.GetRun(runID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Run = RunJournalRunSummary{
		ID:            run.ID,
		ParentSession: run.ParentSession,
		Tool:          run.Tool,
		Label:         run.Label,
		Status:        run.Status,
		Total:         run.Total,
		CreatedAt:     run.CreatedAt,
		EndedAt:       run.EndedAt,
	}
	members, err := store.ListMembers(runID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	events, err := store.ListEvents(runID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	artifacts, err := store.ListArtifacts(runID)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	// now bounds trail timestamps (clock skew reads as "now", never as a
	// future event) — the projection's only clock, injected at the edge.
	now := time.Now().UnixMilli()
	roster := runjournal.BuildRoster(run, members, events, now)
	out.Health = RunJournalHealthView{
		Terminal:            roster.Health.Terminal,
		LastProgressAt:      roster.Health.LastProgressAt,
		ConsecutiveFailures: roster.Health.ConsecutiveFailures,
		LeftoverRunning:     roster.Health.LeftoverRunning,
		MembersTerminal:     roster.Health.MembersTerminal,
		MembersTotal:        roster.Health.MembersTotal,
	}
	for _, v := range roster.Members {
		out.Members = append(out.Members, RunJournalMemberView{
			Idx: v.Idx, MemberID: v.MemberID, Label: v.Label, Profile: v.Profile,
			Status: v.Status, Ref: v.Ref, Error: v.Error, Preview: v.Preview,
			StartedAt: v.StartedAt, EndedAt: v.EndedAt,
		})
	}
	for _, e := range runjournal.BuildTimeline(members, events) {
		out.Timeline = append(out.Timeline, RunJournalTimelineEntry{
			Seq: e.Seq, At: e.At, Kind: e.Kind, MemberIdx: e.MemberIdx,
			MemberLabel: e.MemberLabel, Status: e.Status, Detail: e.Detail,
		})
	}
	for _, v := range runjournal.ArtifactsOf(artifacts) {
		out.Artifacts = append(out.Artifacts, RunJournalArtifactView{
			ID: v.ID, Kind: v.Kind, Title: v.Title, Ref: v.Ref,
			MemberIdx: v.MemberIdx, Version: v.Version, Primary: v.Primary,
			PublishedAt: v.PublishedAt,
		})
	}
	return out
}
