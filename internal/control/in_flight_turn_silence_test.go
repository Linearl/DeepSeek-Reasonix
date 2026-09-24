package control

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestConcurrentWriterLocalRaceStaysSilent pins task 203's misreport half: an
// in-process fork with no dual open (a long task's interleaved saves, a merge
// injection) must not claim "another window or process" — no notice, the fork
// is logged upstream instead. The version split still happens at save time.
func TestConcurrentWriterLocalRaceStaysSilent(t *testing.T) {
	SetConcurrentDualTabProbe(func(string) bool { return false })
	t.Cleanup(func() { SetConcurrentDualTabProbe(nil) })

	dir := t.TempDir()
	path := filepath.Join(dir, "shared.jsonl")
	const systemPrompt = "SYS"
	reply := [][]provider.Chunk{{{Type: provider.ChunkText, Text: "ok"}, {Type: provider.ChunkDone}}}
	sinkA, sinkB := &noticeSink{}, &noticeSink{}
	execA := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlA := New(Options{Runner: execA, Executor: execA, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "a", Sink: sinkA})
	if err := ctrlA.RunTurn(context.Background(), "first from A"); err != nil {
		t.Fatal(err)
	}
	loaded, err := agent.LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	execB := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession(systemPrompt), agent.Options{}, event.Discard)
	ctrlB := New(Options{Runner: execB, Executor: execB, SystemPrompt: systemPrompt, SessionDir: dir, SessionPath: path, Label: "b", Sink: sinkB})
	ctrlB.Resume(loaded, path)
	if err := ctrlA.RunTurn(context.Background(), "second from A"); err != nil {
		t.Fatal(err)
	}
	if err := ctrlB.RunTurn(context.Background(), "second from B"); err != nil {
		t.Fatal(err)
	}
	// The fork still happened — B kept going on a fresh head — but no notice
	// fired: same process, no dual open, the old copy blamed another window.
	if notice, ok := sinkB.lastNotice(); ok {
		t.Fatalf("local race must stay silent, got notice %+v", notice)
	}
	if notice, ok := sinkA.lastNotice(); ok {
		t.Fatalf("the other in-process writer must stay silent too, got notice %+v", notice)
	}
}
