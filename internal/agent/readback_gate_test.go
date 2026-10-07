package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	"reasonix/internal/tool/builtin"
)

// Task 603: readBack — an edit_file/multi_edit call that reads its result back
// files the returned window as fresh read evidence, so a same-file follow-up
// edit passes the write gate on the next provider round without a separate
// read_file. Every other route to that relaxation is closed: the family switch
// must be on, the write must have succeeded, and any later change to the file
// still fails the coverage match.

func readBackRunFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readBackEditTool builds edit_file through the real Workspace assembly with
// the 工具优化 family lit (the same route boot uses), rooted at the fixture's
// directory.
func readBackEditTool(t *testing.T, dir string) tool.Tool {
	t.Helper()
	tools := builtin.Workspace{Dir: dir, ToolOptimizations: true}.Tools("edit_file")
	if len(tools) != 1 {
		t.Fatalf("Workspace assembly returned %d edit_file tools", len(tools))
	}
	return tools[0]
}

func plainEditTool(t *testing.T, dir string) tool.Tool {
	t.Helper()
	tools := builtin.Workspace{Dir: dir}.Tools("edit_file")
	if len(tools) != 1 {
		t.Fatalf("Workspace assembly returned %d edit_file tools", len(tools))
	}
	return tools[0]
}

func TestReadBackEditSatisfiesNextRoundEvidenceGate(t *testing.T) {
	path := readBackRunFixture(t, "alpha\nbeta\ngamma\n")
	second, _ := json.Marshal(map[string]any{"path": path, "old_string": "REPLACED", "new_string": "FINAL"})
	p := &scriptedProvider{turns: [][]provider.Chunk{
		{toolCallChunk("read", "read_file", fmt.Sprintf(`{"path":%q}`, path)), {Type: provider.ChunkDone}},
		{toolCallChunk("edit1", "edit_file", fmt.Sprintf(`{"path":%q,"old_string":"beta","new_string":"REPLACED","readBack":true}`, path)), {Type: provider.ChunkDone}},
		{toolCallChunk("edit2", "edit_file", string(second)), {Type: provider.ChunkDone}},
		textTurn("Done."),
	}}
	writer := readBackEditTool(t, filepath.Dir(path))
	a := newIncompleteReadTestAgent(p, incompleteReadBuiltin(t), NewSession("sys"), event.Discard, writer)
	if err := a.Run(withNoClosedLoop(context.Background()), "Edit the file twice."); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !strings.HasPrefix(string(got), "alpha\nFINAL\ngamma") {
		t.Fatalf("second edit did not land without a re-read: %q (edit2 result: %q)", got, toolResultByID(a.Session(), "edit2"))
	}
	if back := toolResultByID(a.Session(), "edit1"); !strings.Contains(back, "read_back "+path) {
		t.Fatalf("readBack edit did not return the read-back window: %q", back)
	}
}

func TestEditWithoutReadBackStillRequiresAReRead(t *testing.T) {
	path := readBackRunFixture(t, "alpha\nbeta\ngamma\n")
	second, _ := json.Marshal(map[string]any{"path": path, "old_string": "REPLACED", "new_string": "FINAL"})
	p := &scriptedProvider{turns: [][]provider.Chunk{
		{toolCallChunk("read", "read_file", fmt.Sprintf(`{"path":%q}`, path)), {Type: provider.ChunkDone}},
		{toolCallChunk("edit1", "edit_file", fmt.Sprintf(`{"path":%q,"old_string":"beta","new_string":"REPLACED"}`, path)), {Type: provider.ChunkDone}},
		{toolCallChunk("edit2", "edit_file", string(second)), {Type: provider.ChunkDone}},
		textTurn("Done."),
	}}
	// The family switch is lit, but the call does not ask for readBack — the
	// ordinary write-gate behavior must be untouched.
	writer := readBackEditTool(t, filepath.Dir(path))
	a := newIncompleteReadTestAgent(p, incompleteReadBuiltin(t), NewSession("sys"), event.Discard, writer)
	if err := a.Run(withNoClosedLoop(context.Background()), "Edit the file twice."); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "FINAL") {
		t.Fatal("a follow-up edit without readBack must still be blocked until the model re-reads")
	}
	if back := toolResultByID(a.Session(), "edit1"); strings.Contains(back, "read_back ") {
		t.Fatalf("no readBack requested, yet the result carried a window: %q", back)
	}
	if blocked := toolResultByID(a.Session(), "edit2"); !strings.Contains(blocked, "evidence required") {
		t.Fatalf("follow-up edit result = %q, want the read-evidence rejection", blocked)
	}
}

func TestFamilyOffEditWithReadBackParamBehavesAsBefore(t *testing.T) {
	path := readBackRunFixture(t, "alpha\nbeta\ngamma\n")
	second, _ := json.Marshal(map[string]any{"path": path, "old_string": "REPLACED", "new_string": "FINAL"})
	p := &scriptedProvider{turns: [][]provider.Chunk{
		{toolCallChunk("read", "read_file", fmt.Sprintf(`{"path":%q}`, path)), {Type: provider.ChunkDone}},
		{toolCallChunk("edit1", "edit_file", fmt.Sprintf(`{"path":%q,"old_string":"beta","new_string":"REPLACED","readBack":true}`, path)), {Type: provider.ChunkDone}},
		{toolCallChunk("edit2", "edit_file", string(second)), {Type: provider.ChunkDone}},
		textTurn("Done."),
	}}
	// 铁律 2: with the family off the parameter is ignored and the write gate
	// keeps its ordinary behavior — the follow-up edit stays blocked.
	a := newIncompleteReadTestAgent(p, incompleteReadBuiltin(t), NewSession("sys"), event.Discard, plainEditTool(t, filepath.Dir(path)))
	if err := a.Run(withNoClosedLoop(context.Background()), "Edit the file twice."); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "FINAL") {
		t.Fatal("family off must not relax the follow-up edit")
	}
	if back := toolResultByID(a.Session(), "edit1"); strings.Contains(back, "read_back ") {
		t.Fatalf("family off must not return a read-back window: %q", back)
	}
}

func TestReadBackEvidenceCannotSurviveAnOutsideChange(t *testing.T) {
	path := readBackRunFixture(t, "alpha\nbeta\ngamma\n")
	e := readBackEditTool(t, filepath.Dir(path))
	ctx, sink := tool.WithReadBackCollector(context.Background())
	if _, err := e.Execute(ctx, json.RawMessage(fmt.Sprintf(`{"path":%q,"old_string":"beta","new_string":"REPLACED","readBack":true}`, path))); err != nil {
		t.Fatal(err)
	}
	a, ledger := newEvidenceAgent(t, e, true)
	// The finalizer records the window with the post-write snapshot.
	a.task.ledger = ledger
	a.storeBatchToolResult(context.Background(), provider.ToolCall{ID: "c1", Name: "edit_file", Arguments: `{}`}, toolOutcome{
		executed: true,
		readBackObs: &tool.ReadBackObservation{
			Path:       sink.Path,
			StartLine:  sink.StartLine,
			LineHashes: sink.LineHashes,
			Snapshot:   sink.Snapshot,
		},
	})
	// The file changes outside the session after the read-back.
	if err := os.WriteFile(path, []byte("alpha\nbeta\nCHANGED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := provider.ToolCall{Name: "edit_file", Arguments: fmt.Sprintf(`{"path":%q,"old_string":"beta","new_string":"x"}`, path)}
	check := a.checkOperationEvidence(context.Background(), call, e, a.task.ledger.ObservationBoundary())
	if check.Satisfied {
		t.Fatal("a read-back window cannot authorize an edit against changed content — stale detection must still fire")
	}
}

func TestReadBackEvidenceStaysInvisibleInsideItsOwnBatch(t *testing.T) {
	path := readBackRunFixture(t, "alpha\nbeta\ngamma\n")
	e := readBackEditTool(t, filepath.Dir(path))
	// Perform the real edit so the follow-up target exists on disk.
	execCtx, sink := tool.WithReadBackCollector(context.Background())
	if _, err := e.Execute(execCtx, json.RawMessage(fmt.Sprintf(`{"path":%q,"old_string":"beta","new_string":"REPLACED","readBack":true}`, path))); err != nil {
		t.Fatal(err)
	}
	// Simulate the ordered finalizer inside batch N: the frozen boundary B is
	// taken BEFORE the window is filed, so its ledger sequence is past B and
	// the batch's own preflight cannot see it — the model has not seen the
	// result yet.
	ledger := evidence.NewLedger()
	a, _ := newEvidenceAgent(t, e, true)
	boundary := ledger.ObservationBoundary()
	ctx := withObservationBoundary(context.Background(), boundary)
	a.task.ledger = ledger
	a.storeBatchToolResult(ctx, provider.ToolCall{ID: "c1", Name: "edit_file"}, toolOutcome{
		executed: true,
		readBackObs: &tool.ReadBackObservation{
			Path:       sink.Path,
			StartLine:  sink.StartLine,
			LineHashes: sink.LineHashes,
			Snapshot:   sink.Snapshot,
		},
	})
	if got := len(ledger.TextObservations()); got != 1 {
		t.Fatalf("recorded observations = %d, want the read-back window", got)
	}
	call := provider.ToolCall{Name: "edit_file", Arguments: fmt.Sprintf(`{"path":%q,"old_string":"REPLACED","new_string":"x"}`, path)}
	if check := a.checkOperationEvidence(ctx, call, e, boundary); check.Satisfied {
		t.Fatal("a read-back window recorded inside the running batch must stay ineligible until the next provider round")
	}
	if check := a.checkOperationEvidence(context.Background(), call, e, ledger.ObservationBoundary()); !check.Satisfied {
		t.Fatalf("the same window must satisfy the gate once the batch boundary has crossed it: %+v", check)
	}
}

func TestFailedWriteNeverFilesReadBackEvidence(t *testing.T) {
	e := readBackEditTool(t, t.TempDir())
	a, ledger := newEvidenceAgent(t, e, true)
	a.storeBatchToolResult(context.Background(), provider.ToolCall{ID: "c1", Name: "edit_file"}, toolOutcome{
		executed: true,
		errMsg:   "write failed",
		readBackObs: &tool.ReadBackObservation{
			Path:       "C:\\w\\x.txt",
			StartLine:  1,
			LineHashes: hashesFor("alpha"),
			Snapshot:   "ss2:x",
		},
	})
	a.storeBatchToolResult(context.Background(), provider.ToolCall{ID: "c2", Name: "edit_file"}, toolOutcome{
		readBackObs: &tool.ReadBackObservation{
			Path:       "C:\\w\\x.txt",
			StartLine:  1,
			LineHashes: hashesFor("alpha"),
			Snapshot:   "ss2:x",
		},
	})
	if got := len(ledger.TextObservations()); got != 0 {
		t.Fatalf("observations = %d, want none: a failed or unexecuted write never becomes read evidence", got)
	}
}
