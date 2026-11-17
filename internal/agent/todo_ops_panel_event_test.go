package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"

	_ "reasonix/internal/tool/builtin"
)

// tl1682: an ops-based todo_write carries only {"ops":[...]} in its raw args —
// the frontend panel parses only the {"todos":[...]} shape, so without a
// synthetic full-list event the live projection resolves empty and the dock
// panel collapses the whole list into the sealed "previous turn" fold. The
// expanded write must land on the event stream the same way a host-advanced
// complete_step does (the advanceCanonicalTodo family).

func syntheticTodoFullListArgs(t *testing.T, sink *recordSink) []string {
	t.Helper()
	var out []string
	for _, e := range sink.kinds(event.ToolResult) {
		if e.Tool.Name != "todo_write" || !strings.HasPrefix(e.Tool.ID, "host-advance-") {
			continue
		}
		out = append(out, e.Tool.Args)
	}
	return out
}

// TestE2EOpsWriteEmitsSyntheticFullList: establish a baseline with a full-list
// write, append a new task via ops, then require the synthetic event to carry
// the expanded full list (old items preserved, new item present) — the exact
// payload the panel's live projection needs.
func TestE2EOpsWriteEmitsSyntheticFullList(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "t0", Name: "todo_write",
			Arguments: `{"todos":[{"content":"alpha","status":"completed","step_id":"s1"},{"content":"beta","status":"in_progress","step_id":"s2"}]}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "t1", Name: "todo_write",
			Arguments: `{"ops":[{"op":"insert","item":{"content":"gamma","status":"pending","step_id":"s3"}}]}`}}},
		testutil.Turn{Text: "appended"},
	)
	sink := &recordSink{}
	a := New(mp, evidenceRegistry(), NewSession("sys"), Options{}, sink)

	if err := a.Run(withNoClosedLoop(context.Background()), "append a task"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := a.CanonicalTodoState()
	if len(got) != 3 || got[0].Content != "alpha" || got[1].Content != "beta" || got[2].Content != "gamma" {
		t.Fatalf("canonical todos after ops write = %+v, want alpha/beta/gamma", got)
	}
	argsList := syntheticTodoFullListArgs(t, sink)
	if len(argsList) == 0 {
		t.Fatal("ops write emitted no synthetic full-list todo_write event; the panel would collapse into the sealed fold")
	}
	last := argsList[len(argsList)-1]
	var payload struct {
		Todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(last), &payload); err != nil {
		t.Fatalf("synthetic args not the full-list JSON shape: %v (args=%q)", err, last)
	}
	if len(payload.Todos) != 3 {
		t.Fatalf("synthetic full list has %d items, want 3 (args=%q)", len(payload.Todos), last)
	}
	for i, want := range []struct{ content, status string }{{"alpha", "completed"}, {"beta", "in_progress"}, {"gamma", "pending"}} {
		if payload.Todos[i].Content != want.content || payload.Todos[i].Status != want.status {
			t.Fatalf("synthetic full list item %d = %+v, want %s/%s", i, payload.Todos[i], want.content, want.status)
		}
	}
}

// TestE2EFullTodoWriteEmitsNoSyntheticFullList: a wholesale {"todos":[...]}
// write already lands on the panel through its own args — a synthetic second
// event would double the transcript card without adding information.
func TestE2EFullTodoWriteEmitsNoSyntheticFullList(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "t0", Name: "todo_write",
			Arguments: `{"todos":[{"content":"alpha","status":"in_progress"},{"content":"beta","status":"pending"}]}`}}},
		testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "t1", Name: "todo_write",
			Arguments: `{"todos":[{"content":"alpha","status":"completed"},{"content":"beta","status":"in_progress"}]}`}}},
		testutil.Turn{Text: "progressed"},
	)
	sink := &recordSink{}
	a := New(mp, evidenceRegistry(), NewSession("sys"), Options{}, sink)

	if err := a.Run(withNoClosedLoop(context.Background()), "progress the list"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if argsList := syntheticTodoFullListArgs(t, sink); len(argsList) != 0 {
		t.Fatalf("full-list writes emitted %d synthetic events, want 0 (args=%v)", len(argsList), argsList)
	}
}

func TestTodoWriteArgsUseOps(t *testing.T) {
	cases := []struct {
		name string
		args string
		want bool
	}{
		{"ops insert", `{"ops":[{"op":"insert","item":{"content":"x","status":"pending"}}]}`, true},
		{"ops replace", `{"ops":[{"op":"replace","step_id":"s1","item":{"content":"y","status":"completed"}}]}`, true},
		{"full todos list", `{"todos":[{"content":"x","status":"pending"}]}`, false},
		{"explicit empty clear", `{"todos":[]}`, false},
		{"both shapes (tool rejects, helper says no)", `{"todos":[],"ops":[]}`, false},
		{"malformed json", `{"ops":`, false},
		{"empty object", `{}`, false},
	}
	for _, tc := range cases {
		if got := todoWriteArgsUseOps([]byte(tc.args)); got != tc.want {
			t.Fatalf("%s: todoWriteArgsUseOps(%q) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}
