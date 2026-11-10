package control

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// tl1459 leftover B pins (保存主路径纵深②): the tool checkpoint's conflict
// escalation ladder. Before this file the checkpoint path owned two mid-turn
// escalations (pending-rewrite, same-revision rewrite) and returned every
// diverged conflict unchanged — which failTurnEventLedger turned into a turn
// cancel. The rev-lag shape (a second writer advanced the ledger and died,
// live transcript kept growing) is exactly what the turn-boundary ladder
// already resolves via agent.SaveAdoptOverStaleDisk; these pins make the
// checkpoint path run the same mid-turn-safe escalation (same session object,
// same path — no rebind) and keep the terminal conflict — cause-carrying —
// for every shape the takeover declines.

// TestCheckpointConflictRevLagTakeoverKeepsCheckpointAlive: the tl1409 field
// shape hitting the mid-turn tool checkpoint no longer kills the turn path.
// The checkpoint escalates to the rev-lag takeover, the transcript lands at
// disk revision+1, and the durable turn-event ledger is never latched — the
// failTurnEventLedger cancel branch (the incident's exit 5) does not fire.
func TestCheckpointConflictRevLagTakeoverKeepsCheckpointAlive(t *testing.T) {
	logs := captureForkTraceLog(t)
	path := filepath.Join(t.TempDir(), "checkpoint-takeover.jsonl")
	live := agent.NewSession("sys")
	conflict, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	if conflict.DiskRevision <= conflict.BaseRevision {
		t.Fatalf("fixture shape = base %d disk %d, want the rev-lag order", conflict.BaseRevision, conflict.DiskRevision)
	}
	c := newTakeoverLadderController(t, path, live)

	if err := c.checkpointToolTranscript(); err != nil {
		t.Fatalf("checkpoint after rev-lag takeover escalation = %v, want nil (the takeover resolves the shape)", err)
	}
	// The cancel trigger stays unlatched: goalCancelCause would classify this
	// turn end as a user/context cancel, not a durability kill.
	if err := c.turnEventLedgerError(); err != nil {
		t.Fatalf("turn event ledger latched after checkpoint takeover: %v", err)
	}
	// No recovery fork residue and no disk adoption: the live transcript won.
	if got := agent.RecoveryForkCountForTopic(path); got != 0 {
		t.Fatalf("recovery fork count after checkpoint takeover = %d, want 0", got)
	}
	reloaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("reload after checkpoint takeover: %v", err)
	}
	if got := reloaded.Snapshot(); len(got) != 5 || got[4].Content != "live4" {
		t.Fatalf("reload = %d messages, want the live transcript (system + 4)", len(got))
	}
	state, known, err := agent.SessionContentIdentity(path)
	if err != nil || !known {
		t.Fatalf("SessionContentIdentity: known=%v err=%v", known, err)
	}
	if state.Revision != conflict.DiskRevision+1 {
		t.Fatalf("revision after checkpoint takeover = %d, want %d", state.Revision, conflict.DiskRevision+1)
	}
	// The escalation is visible: one traced decision naming the takeover rung
	// in checkpoint mode (结论可见, same contract as the turn-boundary ladder).
	rec := onlyForkTraceDecision(t, logs, forkDecisionRewrite)
	if rec["fork_reason"] != forkReasonRevLagTakeover || rec["mode"] != "checkpoint" {
		t.Fatalf("takeover trace = %v, want fork_reason=%s mode=checkpoint", rec, forkReasonRevLagTakeover)
	}
}

// TestCheckpointConflictDeclinedTakeoverStillReturnsConflict keeps the
// terminal honest: when every escalation declines (here: the "dead" writer
// wrote again, so the disk-still-stale guard refuses), the checkpoint returns
// the conflict unchanged — the durable-sink kill branch stays the last resort,
// and its cause text carries the conflict for goalCancelDurability.
func TestCheckpointConflictDeclinedTakeoverStillReturnsConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint-declined.jsonl")
	live := agent.NewSession("sys")
	_, ok := growDivergedHistory(t, path, live)
	if !ok {
		t.Fatal("diverged conflict was not produced by the fixture")
	}
	// The second writer is alive: it advances the disk again after the conflict.
	other, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(provider.Message{Role: provider.RoleUser, Content: "alive-tail"})
	if err := other.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
	c := newTakeoverLadderController(t, path, live)

	err = c.checkpointToolTranscript()
	if !errors.Is(err, agent.ErrSessionSnapshotConflict) {
		t.Fatalf("checkpoint err = %v, want the snapshot conflict returned unchanged", err)
	}
	var shaped *agent.SessionSnapshotConflictError
	if !errors.As(err, &shaped) || shaped == nil {
		t.Fatalf("checkpoint err = %v, want the structured conflict for the cause trail", err)
	}
	// The kill-path cause must name the conflict: failTurnEventLedger records
	// this error and the goal stop notice renders it as the durability reason.
	if !strings.Contains(shaped.Error(), "session snapshot conflicts with newer transcript") {
		t.Fatalf("conflict text = %q, want the sentinel phrase", shaped.Error())
	}
}
