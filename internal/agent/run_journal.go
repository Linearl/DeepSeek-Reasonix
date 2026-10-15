package agent

// Task 419 slice 2 — run recorder: the agent-side write adapter that funnels
// fleet / parallel_tasks group lifecycle into internal/runjournal.
//
// Discipline (task 419 design §3/§6):
//
//   - Recording must never fail the run. Every method is nil-safe, every
//     storage error is swallowed after one slog line with the stable
//     "runjournal" phrase (repo rule: error paths must log). A journal that
//     broke the pipeline it observes would be worse than no journal.
//   - One recorder per run: open at run start, close at End (no cross-run
//     handle — a background fleet outlives Execute, but its recorder travels
//     inside runFleet's closure, so the handle lifetime is the run's).
//   - Nil store / nil recorder = journaling disabled (empty path, open
//     failure, or a tool that never got WithRunJournalDir): all methods no-op
//     and the pre-419 behavior is byte-identical.

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"reasonix/internal/runjournal"
)

// errorText renders a worker error for the journal's error column ("" for a
// nil error; the store bounds the length).
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// runRecorder writes one run's journal. The zero usable form is nil — every
// method must tolerate a nil receiver.
type runRecorder struct {
	store *runjournal.Store
	runID string
}

// newRunRecorder opens the journal database at dbPath (empty path = off) and
// begins nothing yet. Returns nil when journaling is unavailable so callers
// can assign unconditionally and then call begin.
func newRunRecorder(dbPath string) *runRecorder {
	if dbPath == "" {
		return nil
	}
	store, err := runjournal.Open(dbPath)
	if err != nil {
		slog.Warn("runjournal: open failed, run proceeds without recording",
			"runjournal", "open_failed", "path", dbPath, "err", err)
		return nil
	}
	return &runRecorder{store: store}
}

// RunJournalDBPath is the per-session journal location (design §3): beside
// the transcripts under sessionDir. Empty sessionDir disables recording.
// Exported for boot's tool wiring.
func RunJournalDBPath(sessionDir string) string {
	if sessionDir == "" {
		return ""
	}
	return filepath.Join(sessionDir, "runjournal.db")
}

// begin opens the run's record. On failure the recorder disables itself
// (store closed, methods become no-ops): a run whose header could not be
// written will not produce a half-recorded trail that reads as a lie.
func (r *runRecorder) begin(spec runjournal.RunSpec) {
	if r == nil || r.store == nil {
		return
	}
	runID, err := r.store.BeginRun(spec)
	if err != nil {
		slog.Warn("runjournal: begin failed, disabling recording for this run",
			"runjournal", "begin_failed", "tool", spec.Tool, "err", err)
		_ = r.store.Close()
		r.store = nil
		return
	}
	r.runID = runID
}

// dispatched marks a member running (idx is the 1-based roster position).
func (r *runRecorder) dispatched(idx int) {
	if r == nil || r.store == nil || r.runID == "" {
		return
	}
	if err := r.store.Dispatched(r.runID, idx); err != nil {
		slog.Warn("runjournal: dispatch event failed", "runjournal", "dispatch_failed", "err", err)
	}
}

// settled records a member's terminal outcome (first write wins inside the
// store — a cancelled drain never overwrites a real result). A completed
// member with a transcript ref also publishes its artifact row here, so both
// tools get the member-artifact projection without repeating the logic.
func (r *runRecorder) settled(m runjournal.MemberSettlement) {
	if r == nil || r.store == nil || r.runID == "" {
		return
	}
	if err := r.store.Settled(r.runID, m); err != nil {
		slog.Warn("runjournal: settle event failed", "runjournal", "settle_failed", "idx", m.Idx, "err", err)
		return
	}
	if m.Status == "completed" && m.Ref != "" {
		if err := r.store.PublishArtifact(r.runID, runjournal.ArtifactSpec{
			ID: m.Ref, MemberIdx: m.Idx, Ref: m.Ref, Title: m.Label,
		}); err != nil {
			slog.Warn("runjournal: artifact publish failed", "runjournal", "artifact_failed", "id", m.Ref, "err", err)
		}
	}
}

// artifact publishes a completed artifact row (replay-safe by (id,version)).
func (r *runRecorder) artifact(a runjournal.ArtifactSpec) {
	if r == nil || r.store == nil || r.runID == "" {
		return
	}
	if err := r.store.PublishArtifact(r.runID, a); err != nil {
		slog.Warn("runjournal: artifact publish failed", "runjournal", "artifact_failed", "id", a.ID, "err", err)
	}
}

// end closes the run (terminal status + bounded result preview) and releases
// the database handle. A completed run's group aggregate publishes as the
// primary artifact (design §4: id = run_id+":result"). Idempotent: a second
// end with a nil'd store no-ops.
func (r *runRecorder) end(status, resultPreview string) {
	if r == nil || r.store == nil {
		return
	}
	if r.runID != "" {
		if status == "completed" && resultPreview != "" {
			if err := r.store.PublishArtifact(r.runID, runjournal.ArtifactSpec{
				ID: r.runID + ":result", Kind: "result", Title: "group result",
				Primary: true,
			}); err != nil {
				slog.Warn("runjournal: group artifact publish failed", "runjournal", "artifact_failed", "err", err)
			}
		}
		if err := r.store.End(r.runID, status, resultPreview); err != nil {
			slog.Warn("runjournal: end failed", "runjournal", "end_failed", "err", err)
		}
	}
	_ = r.store.Close()
	r.store = nil
}

// runIDOf exposes the active run id for tests ("" when not recording).
func (r *runRecorder) runIDOf() string {
	if r == nil {
		return ""
	}
	return r.runID
}

// settleRemaining records journal members the workers never settled — items
// the scheduler skipped (dependency failed / fail_fast / cancellation cut the
// queue) arrive here as non-pending results without a worker-side settle.
// First-write-wins in the store makes the worker-settled duplicates no-ops,
// so callers may pass every result unconditionally.
func (r *runRecorder) settleRemaining(results []runjournal.MemberSettlement) {
	if r == nil || r.store == nil || r.runID == "" {
		return
	}
	for _, m := range results {
		r.settled(m)
	}
}

// fleetMemberSpecs builds the roster declaration for a fleet run from its
// specs: idx 1-based, member id namespaced exactly once under the group call
// (the same subID runFleet dispatches with, so trail and event cards agree).
func fleetMemberSpecs(parentID string, specs []ProfileExecSpec) []runjournal.MemberSpec {
	members := make([]runjournal.MemberSpec, 0, len(specs))
	for i := range specs {
		members = append(members, runjournal.MemberSpec{
			Idx:      i + 1,
			MemberID: fmt.Sprintf("%s/fleet-%d", parentID, i+1),
			Label:    specs[i].Task.Description,
			Profile:  specs[i].Worker.Profile,
		})
	}
	return members
}

// parallelMemberSpecs is the parallel_tasks counterpart (sub-N ids).
func parallelMemberSpecs(parentID string, items []parallelTaskItem) []runjournal.MemberSpec {
	members := make([]runjournal.MemberSpec, 0, len(items))
	for i := range items {
		label := items[i].Description
		if label == "" {
			label = fmt.Sprintf("task-%d", i+1)
		}
		members = append(members, runjournal.MemberSpec{
			Idx:      i + 1,
			MemberID: fmt.Sprintf("%s/sub-%d", parentID, i+1),
			Label:    label,
		})
	}
	return members
}
