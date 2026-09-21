package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func writeCollabStatusLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Acceptance 1+2 (task 202): engine events are readable in one incremental
// call, and a follow-up read only consumes the new bytes - proven by
// bytes_read, next_offset, and scans==0 rather than by trusting the code.
func TestCollabStatusAppendAndIncrementalRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collab-status.jsonl")
	for i := 0; i < 5; i++ {
		AppendCollabStatusEvent(path, "line-a", "lineA", CollabStatusTurnEnd, fmt.Sprintf("step %d done", i), false)
	}
	first, err := ReadCollabStatusIncremental(path, 0, 0)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if len(first.Events) != 5 || first.Scans != 0 {
		t.Fatalf("first = %d events, scans=%d; want 5 events, 0 scans", len(first.Events), first.Scans)
	}
	if first.NextOffset != first.FileBytes {
		t.Fatalf("next_offset = %d, want file size %d", first.NextOffset, first.FileBytes)
	}

	AppendCollabStatusEvent(path, "line-a", "lineA", CollabStatusCommit, "git commit executed", false)
	second, err := ReadCollabStatusIncremental(path, first.NextOffset, 0)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if len(second.Events) != 1 || second.Events[0].Event != CollabStatusCommit {
		t.Fatalf("incremental = %+v, want exactly the new commit event", second.Events)
	}
	if second.BytesRead >= first.FileBytes {
		t.Fatalf("incremental read %d bytes, want well under a full-file rescan (%d)", second.BytesRead, first.FileBytes)
	}
	if second.Scans != 0 {
		t.Fatalf("scans = %d, want 0 (never rescan)", second.Scans)
	}
}

// Acceptance 1 (batch view): events from every line of the batch land in one
// file, so a single incremental read returns the whole batch's progress.
func TestCollabStatusOneReadCoversEveryLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collab-status.jsonl")
	AppendCollabStatusEvent(path, "wt-a", "lineA", CollabStatusCommit, "commit 4b1eff6ff", false)
	AppendCollabStatusEvent(path, "wt-b", "lineB", CollabStatusToolError, "edit_file: drift", false)
	AppendCollabStatusEvent(path, "wt-c", "lineC", CollabStatusNeedsDecision, "awaiting merge order", true)
	result, err := ReadCollabStatusIncremental(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 3 {
		t.Fatalf("events = %d, want all three lines in one read", len(result.Events))
	}
	decisions := 0
	for _, ev := range result.Events {
		if ev.NeedsDecision {
			decisions++
		}
	}
	if decisions != 1 {
		t.Fatalf("needs_decision flags = %d, want 1", decisions)
	}
}

// Acceptance 3 (A1 fallback coexistence): a hand-written line in the same
// shape - the A1 habit - is just another event to the reader.
func TestCollabStatusA1HandwrittenLineCoexists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collab-status.jsonl")
	AppendCollabStatusEvent(path, "wt-a", "lineA", CollabStatusTurnStart, "kicked off", false)
	handwritten := `{"ts":"2026-09-22T00:00:00Z","line":"lineB","session":"wt-hand","event":"manual","summary":"merge order unclear without a decision","needs_decision":true}`
	writeCollabStatusLines(t, path, []string{
		`{"ts":"2026-09-22T00:00:00Z","line":"lineA","session":"wt-a","event":"turn_start","summary":"kicked off","needs_decision":false}`,
		handwritten,
	})
	result, err := ReadCollabStatusIncremental(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 2 {
		t.Fatalf("events = %d, want engine line + hand-written line", len(result.Events))
	}
	if result.Events[1].Line != "lineB" || result.Events[1].Event != CollabStatusManual || !result.Events[1].NeedsDecision {
		t.Fatalf("hand-written line misread: %+v", result.Events[1])
	}
}

// Task 202: with messaging disabled entirely, the stream still writes and
// reads - the decoupling acceptance.
func TestCollabStatusDecoupledFromMessaging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks", "collab-status.jsonl")
	cfg := SessionCollabConfig{Enabled: false, WorkspaceRoot: dir, CollabStatusPath: path}
	cfg.collabStatusEvent(CollabStatusDelivered, "progress note without messaging", false)
	result, err := ReadCollabStatusIncremental(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].Event != CollabStatusDelivered {
		t.Fatalf("events = %+v, want the delivery event without any messaging dependency", result.Events)
	}
}

// The engine hooks land the promised event kinds on a real turn.
func TestCollabStatusTurnAndToolErrorHooksFire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collab-status.jsonl")
	reg := tool.NewRegistry()
	reg.Add(failingCollabTool{})
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "collab-fail", Arguments: `{}`}}},
		testutil.Turn{Text: "recovered"},
	)
	a := New(mp, reg, NewSession(""), Options{
		CollabStatusPath: path,
		// The event identity derives from the session path's base name.
		SessionPath: filepath.Join(t.TempDir(), "wt-test.jsonl"),
	}, event.Discard)
	if err := a.Run(context.Background(), "trigger a tool failure"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	joined := string(data)
	for _, want := range []string{
		`"event":"turn_start"`,
		`"event":"tool_error"`,
		`"event":"turn_end"`,
		`"session":"wt-test"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("stream missing %q:\n%s", want, joined)
		}
	}
}

type failingCollabTool struct{}

func (failingCollabTool) Name() string        { return "collab-fail" }
func (failingCollabTool) Description() string { return "always fails" }
func (failingCollabTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (failingCollabTool) ReadOnly() bool { return true }
func (failingCollabTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", fmt.Errorf("synthetic failure for the status stream")
}

func TestCollabStatusCommitFromBash(t *testing.T) {
	tests := []struct {
		name, args string
		want       bool
	}{
		{name: "plain commit", args: `{"command":"git commit -m \"fix\""}`, want: true},
		{name: "staged commit", args: `{"command":"git add a.go && git commit -m fix"}`, want: true},
		{name: "ls only", args: `{"command":"git status"}`, want: false},
		{name: "non-git", args: `{"command":"commit -m fake"}`, want: false},
		{name: "no command", args: `{}`, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CollabStatusCommitFromBash(tc.args); got != tc.want {
				t.Fatalf("CollabStatusCommitFromBash(%s) = %v", tc.args, got)
			}
		})
	}
}

// The tail window must never parse a partial first line.
func TestCollabStatusTailWindowDropsPartialHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collab-status.jsonl")
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf(`{"ts":"t%d","session":"s","event":"turn_end","summary":"pad pad pad","needs_decision":false}`, i))
	}
	writeCollabStatusLines(t, path, lines)
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ReadCollabStatusIncremental(path, -1, 300)
	if err != nil {
		t.Fatal(err)
	}
	if result.FileBytes != stat.Size() {
		t.Fatalf("file_bytes = %d, want %d", result.FileBytes, stat.Size())
	}
	if result.Scans != 0 || result.BytesRead > 600 {
		t.Fatalf("bounded read violated: bytes_read=%d scans=%d", result.BytesRead, result.Scans)
	}
	for _, ev := range result.Events {
		if ev.Event == "" || ev.TS == "" {
			t.Fatalf("malformed event survived the window cut: %+v", ev)
		}
	}
}
