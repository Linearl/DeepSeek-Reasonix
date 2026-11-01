package control

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestTurnDoneLeavesHeadReferenceEmptyForSchemaOne pins the tl1280 envelope
// shape: the schema-2 head stamping (Session.Head / SetTranscriptHead) left
// with the write-path slow lane, so a terminal turn envelope carries an empty
// HeadID and LeafMessageID for a flat session. The digest/revision pair stays
// the transcript position.
func TestTurnDoneLeavesHeadReferenceEmptyForSchemaOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	reply := [][]provider.Chunk{{{Type: provider.ChunkText, Text: "ok"}, {Type: provider.ChunkDone}}}
	exec := agent.New(&recordingProvider{streams: reply}, tool.NewRegistry(), agent.NewSession("SYS"), agent.Options{}, event.Discard)
	done := make(chan event.Event, 4)
	c := New(Options{Runner: exec, Executor: exec, SystemPrompt: "SYS", SessionDir: dir, SessionPath: path, Label: "test",
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- e
			}
		})})
	t.Cleanup(c.Close)
	for _, input := range []string{"first", "second"} {
		c.Submit(input)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("turn %q did not finish", input)
		}
	}
	records, err := c.TurnEventsAfter(0)
	if err != nil {
		t.Fatalf("TurnEventsAfter: %v", err)
	}
	var terminal int
	for i, record := range slices.Backward(records) {
		if record.Kind == "turn_done" {
			terminal = i
			break
		}
	}
	if ref, ok := exec.Session().Head(); ok {
		t.Fatalf("flat session must not report a schema-2 head, got %+v", ref)
	}
	got := records[terminal]
	if got.HeadID != "" || got.LeafMessageID != "" {
		t.Fatalf("terminal envelope head=%q leaf=%q, want empty for a flat session", got.HeadID, got.LeafMessageID)
	}
	view, err := c.TurnEventReplay(0)
	if err != nil || view.HeadID != "" || view.LeafMessageID != "" {
		t.Fatalf("replay view = %+v err=%v, want empty head reference", view, err)
	}
}
