package control

import (
	"errors"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// newTakeoverLadderController binds a controller to the given live session so
// the conflict ladder operates on the same transcript the test grew.
func newTakeoverLadderController(t *testing.T, path string, live *agent.Session) *Controller {
	t.Helper()
	writer, err := agent.AcquireSessionWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry()
	exec := agent.New(&streamlessProvider{}, reg, live, agent.Options{}, event.Discard)
	if err := writer.Bind(exec.Session(), agent.NextSessionWriteGeneration()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Release)
	sink, _, _ := collectSink()
	c := New(Options{
		Runner:      exec,
		Executor:    exec,
		Sink:        sink,
		SessionDir:  filepath.Dir(path),
		SessionPath: path,
	})
	t.Cleanup(func() { c.Close(); c.autosaveWG.Wait() })
	return c
}

// growDivergedHistory builds the tl1409 field shape on disk: the live session
// saves, a second writer advances the ledger and stops, the live session keeps
// growing and its next save returns the diverged conflict. The bool reports
// whether the conflict was produced.
func growDivergedHistory(t *testing.T, path string, live *agent.Session) (*agent.SessionSnapshotConflictError, bool) {
	t.Helper()
	live.AddBatch(provider.Message{Role: "user", Content: "m1"}, provider.Message{Role: "assistant", Content: "m2"})
	if err := live.SaveSnapshot(path); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	other.Add(provider.Message{Role: "user", Content: "dead-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("second writer save: %v", err)
	}
	live.AddBatch(provider.Message{Role: "user", Content: "live3"}, provider.Message{Role: "assistant", Content: "live4"})
	saveErr := live.SaveSnapshot(path)
	var conflict *agent.SessionSnapshotConflictError
	if errors.As(saveErr, &conflict) && conflict != nil && conflict.Kind == agent.SessionSnapshotConflictDiverged {
		return conflict, true
	}
	return nil, false
}

// TestRecoverSnapshotConflictRevLagTakeover pins the tl1409 ladder escalation:
// a dead second writer's divergence (disk revision ahead, live transcript
// ahead, disk now quiet) resolves through the takeover — no recovery fork, no
// disk adoption, the live transcript lands at disk revision+1.
func TestRecoverSnapshotConflictRevLagTakeover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ladder.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}

	c := newTakeoverLadderController(t, path, live)
	recoveredPath, outcome, recoverErr := c.recoverSnapshotConflict(path, conflict, false)
	if recoverErr != nil {
		t.Fatalf("recoverSnapshotConflict: %v", recoverErr)
	}
	if outcome != conflictTookOver {
		t.Fatalf("outcome = %v, want conflictTookOver", outcome)
	}
	if recoveredPath != path {
		t.Fatalf("recovered path = %q, want the original %q", recoveredPath, path)
	}

	// No fork residue: the takeover must not create a recovery copy.
	if got := agent.RecoveryForkCountForTopic(path); got != 0 {
		t.Fatalf("recovery fork count after takeover = %d, want 0", got)
	}
	// The live transcript is on disk, one revision past the dead writer's.
	reloaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Snapshot(); len(got) != 5 || got[4].Content != "live4" {
		t.Fatalf("reload = %d messages, want the live transcript", len(got))
	}
	state, known, err := agent.SessionContentIdentity(path)
	if err != nil || !known {
		t.Fatalf("SessionContentIdentity: known=%v err=%v", known, err)
	}
	if state.Revision != conflict.DiskRevision+1 {
		t.Fatalf("revision after takeover = %d, want %d", state.Revision, conflict.DiskRevision+1)
	}
}

// TestRecoverSnapshotConflictAliveWriterFallsThrough pins the guard: when the
// second writer is alive (it advanced the disk again after the conflict), the
// takeover must decline and leave the verdict to the pre-existing ladder —
// any outcome except the takeover.
func TestRecoverSnapshotConflictAliveWriterFallsThrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ladder-alive.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	other.Add(provider.Message{Role: "user", Content: "alive-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatalf("alive writer save: %v", err)
	}

	c := newTakeoverLadderController(t, path, live)
	_, outcome, recoverErr := c.recoverSnapshotConflict(path, conflict, false)
	if recoverErr != nil {
		t.Fatalf("recoverSnapshotConflict: %v", recoverErr)
	}
	if outcome == conflictTookOver {
		t.Fatal("takeover committed while the disk writer was alive")
	}
}

// TestPersistSessionSnapshotAfterTakeoverIsNoOp pins the post-takeover state:
// once the takeover landed, ordinary snapshot saves classify as up-to-date —
// the ledger and the live session agree, no second-writer conflict resurfaces.
func TestPersistSessionSnapshotAfterTakeoverIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ladder-noop.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	c := newTakeoverLadderController(t, path, live)
	if _, outcome, recoverErr := c.recoverSnapshotConflict(path, conflict, false); recoverErr != nil || outcome != conflictTookOver {
		t.Fatalf("takeover outcome = %v err = %v, want conflictTookOver", outcome, recoverErr)
	}

	err, _ := persistSessionSnapshot(live, path, false)
	if err != nil {
		t.Fatalf("persist after takeover: %v", err)
	}
	// The controller admission path sees no latched error from this save.
	if err := c.turnEventLedgerError(); err != nil {
		t.Fatalf("turn event ledger latched after takeover save: %v", err)
	}
}
