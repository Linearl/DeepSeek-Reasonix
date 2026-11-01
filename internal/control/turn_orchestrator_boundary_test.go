package control

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

func TestTurnOrchestratorCheckpointBoundaryPrecedesUserMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, event.Discard)
	runner := &recordingSessionRunner{session: sess}
	c := New(Options{
		Runner:      runner,
		Executor:    exec,
		SessionDir:  dir,
		SessionPath: path,
		Label:       "test",
	})

	o := newTurnOrchestrator(c)
	if err := o.runTurnWithRawDisplay(context.Background(), "write the test", "write the test", ""); err != nil {
		t.Fatal(err)
	}

	if !c.CheckpointHasBoundary(0) {
		t.Fatal("checkpoint boundary should be available for the orchestrated turn")
	}
	if len(sess.Messages) != 2 || sess.Messages[1].Content != "write the test" {
		t.Fatalf("session messages after turn = %+v, want system + user", sess.Messages)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("saved messages = %d, want system + user", len(loaded.Messages))
	}
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load branch meta ok=%v err=%v", ok, err)
	}
	if meta.UpdatedAt.IsZero() {
		t.Fatal("activity meta should be marked after transcript changes")
	}
	if err := c.Rewind(0, RewindConversation); err != nil {
		t.Fatal(err)
	}
	// tl1280: the schema-2 in-log rewind head left with the write-path slow
	// lane; a conversation rewind now forks a NEW session file seeded with the
	// boundary prefix, and the controller switches onto it. The boundary
	// precedes the user message, so the fork keeps only the system message.
	live := exec.Session()
	if live == nil || len(live.Messages) != 1 {
		t.Fatalf("rewind did not truncate the live transcript: %+v", live)
	}
	if ref, ok := live.Head(); ok {
		t.Fatalf("flat rewind must not report a schema-2 head: head=%+v", ref)
	}
	forkPath := c.SessionPath()
	if forkPath == path {
		t.Fatalf("rewind must switch to a forked session file, still on %q", path)
	}
	parent, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession parent: %v", err)
	}
	if len(parent.Snapshot()) != 2 {
		t.Fatalf("parent transcript = %d messages, want the pre-rewind turn intact", len(parent.Snapshot()))
	}
	fork, err := agent.LoadSession(forkPath)
	if err != nil {
		t.Fatalf("LoadSession fork: %v", err)
	}
	if len(fork.Snapshot()) != 1 {
		t.Fatalf("fork transcript = %d messages, want the boundary prefix (system only)", len(fork.Snapshot()))
	}
	heads, err := agent.ListSessionHeads(forkPath)
	if err != nil || len(heads) != 0 {
		t.Fatalf("ListSessionHeads shim on the fork = %+v err=%v, want empty", heads, err)
	}
}
