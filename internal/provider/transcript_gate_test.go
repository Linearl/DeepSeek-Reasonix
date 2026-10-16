package provider

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func transcriptGateCall(id, args string) Message {
	return Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: id, Name: "read_file", Arguments: args}}}
}

func transcriptGateResult(id string) Message {
	return Message{Role: RoleTool, ToolCallID: id, Name: "read_file", Content: "observed result"}
}

func TestValidateTranscriptValidPairingPreservesBytes(t *testing.T) {
	cases := map[string][]Message{
		"empty":        nil,
		"conversation": {{Role: RoleSystem, Content: "system"}, {Role: RoleUser, Content: "question"}},
		"parallel results reversed": {
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "read_file", Arguments: ` { "path": "one" } `}, {ID: "b", Name: "read_file", Arguments: `{}`}}},
			transcriptGateResult("b"), transcriptGateResult("a"), {Role: RoleAssistant, Content: "done"},
		},
		"id reused in next round": {
			transcriptGateCall("same", `{}`), transcriptGateResult("same"),
			{Role: RoleUser, Content: "again"},
			transcriptGateCall("same", `{"path":"another"}`), transcriptGateResult("same"),
		},
		// 任务 930: all-optional tools (wait) legally emit arguments:"";
		// the gate accepts it as the no-argument form instead of
		// rejecting the whole request forever.
		"no-argument call empty string":    {transcriptGateCall("a", ``), transcriptGateResult("a")},
		"no-argument call whitespace only": {transcriptGateCall("a", " \t\n "), transcriptGateResult("a")},
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			before, err := json.Marshal(msgs)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateTranscript(msgs); err != nil {
				t.Fatalf("valid transcript rejected: %v", err)
			}
			after, err := json.Marshal(msgs)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("validation changed healthy provider bytes")
			}
		})
	}
}

func TestValidateTranscriptRejectsMalformedToolHistory(t *testing.T) {
	cases := map[string][]Message{
		"missing result":             {transcriptGateCall("a", `{}`)},
		"result after user boundary": {transcriptGateCall("a", `{}`), {Role: RoleUser, Content: "next"}, transcriptGateResult("a")},
		"orphan result":              {transcriptGateResult("a")},
		"duplicate result":           {transcriptGateCall("a", `{}`), transcriptGateResult("a"), transcriptGateResult("a")},
		"wrong result id":            {transcriptGateCall("a", `{}`), transcriptGateResult("b")},
		"duplicate parallel id":      {{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "read_file", Arguments: `{}`}, {ID: "a", Name: "read_file", Arguments: `{}`}}}, transcriptGateResult("a")},
		"empty call id":              {transcriptGateCall("", `{}`), transcriptGateResult("")},
	}
	// "" and whitespace-only are no longer listed: 任务 930 accepts them
	// as the legal no-argument form (see TestValidateTranscriptEmptyArgumentsFiveShapes).
	for _, args := range []string{`{"path":`, `[]`, `null`, `"text"`, `true`, `42`, `{} {}`} {
		cases["arguments "+args] = []Message{transcriptGateCall("a", args), transcriptGateResult("a")}
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			before, _ := json.Marshal(msgs)
			if err := ValidateTranscript(msgs); err == nil {
				t.Fatal("malformed transcript accepted")
			}
			after, _ := json.Marshal(msgs)
			if !bytes.Equal(before, after) {
				t.Fatal("validation repaired/mutated canonical history")
			}
		})
	}
}

// TestValidateTranscriptEmptyArgumentsFiveShapes pins 任务 930 S1: the five
// argument shapes a model may emit for a no-argument call. Empty and
// whitespace-only are accepted (the wait poison pill); null, valid JSON
// objects, and invalid JSON keep their pre-930 verdicts. Validation must
// never rewrite the stored bytes.
func TestValidateTranscriptEmptyArgumentsFiveShapes(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		wantErr bool
	}{
		{"empty string", "", false},
		{"whitespace only", " \t\n ", false},
		{"null", "null", true},
		{"valid json object", `{"job_ids":["j1"]}`, false},
		{"invalid json", `{"path":`, true},
	}
	for _, tc := range cases {
		t.Run("ValidateTranscript/"+tc.name, func(t *testing.T) {
			msgs := []Message{transcriptGateCall("a", tc.args), transcriptGateResult("a")}
			before, _ := json.Marshal(msgs)
			err := ValidateTranscript(msgs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTranscript(%q) err=%v, wantErr=%v", tc.args, err, tc.wantErr)
			}
			after, _ := json.Marshal(msgs)
			if !bytes.Equal(before, after) {
				t.Fatal("validation mutated canonical history")
			}
		})
		t.Run("ValidateModelTranscript/"+tc.name, func(t *testing.T) {
			msgs := []Message{transcriptGateCall("a", tc.args), transcriptGateResult("a")}
			before, _ := json.Marshal(msgs)
			err := ValidateModelTranscript(msgs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateModelTranscript(%q) err=%v, wantErr=%v", tc.args, err, tc.wantErr)
			}
			after, _ := json.Marshal(msgs)
			if !bytes.Equal(before, after) {
				t.Fatal("model validation mutated canonical history")
			}
		})
	}
}

// TestRepairRejectedArgumentsNormalizesEmptyOnly pins 任务 930 S2: empty
// arguments are rewritten to "{}" on the outbound copy for every run state —
// including completed no-argument calls (wait) that the never-ran whitelist
// never covered. Non-empty rejected arguments keep the original rule, and the
// stored history is never mutated.
func TestRepairRejectedArgumentsNormalizesEmptyOnly(t *testing.T) {
	completedResult := func(id string) Message {
		return Message{Role: RoleTool, ToolCallID: id, Name: "wait", Content: "done", ToolRunState: ToolRunCompleted}
	}
	cases := []struct {
		name string
		in   []Message
		want []string // outbound arguments per assistant call, in order
	}{
		{
			name: "completed empty call normalized",
			in: []Message{
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "wait", Arguments: ""}}},
				completedResult("a"),
			},
			want: []string{"{}"},
		},
		{
			name: "completed whitespace call normalized",
			in: []Message{
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "wait", Arguments: " \n "}}},
				completedResult("a"),
			},
			want: []string{"{}"},
		},
		{
			name: "mixed batch empty normalized others untouched",
			in: []Message{
				{Role: RoleAssistant, ToolCalls: []ToolCall{
					{ID: "a", Name: "wait", Arguments: ""},
					{ID: "b", Name: "read_file", Arguments: `{"path":"a.go"}`},
					{ID: "c", Name: "read_file", Arguments: `[]`},
				}},
				{Role: RoleTool, ToolCallID: "a", Name: "wait", Content: "done", ToolRunState: ToolRunCompleted},
				{Role: RoleTool, ToolCallID: "b", Name: "read_file", Content: "one", ToolRunState: ToolRunCompleted},
				{Role: RoleTool, ToolCallID: "c", Name: "read_file", Content: "bad", ToolRunState: ToolRunCompleted},
			},
			// completed-but-nonempty invalid ("[]") stays verbatim: only the
			// empty string may be rewritten for executed calls.
			want: []string{"{}", `{"path":"a.go"}`, `[]`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.in)
			got := RepairRejectedArguments(tc.in)
			idx := 0
			for _, m := range got {
				if m.Role != RoleAssistant {
					continue
				}
				for _, c := range m.ToolCalls {
					if idx >= len(tc.want) {
						t.Fatalf("unexpected extra call %q", c.ID)
					}
					if c.Arguments != tc.want[idx] {
						t.Fatalf("call %d arguments=%q, want %q", idx, c.Arguments, tc.want[idx])
					}
					idx++
				}
			}
			if idx != len(tc.want) {
				t.Fatalf("checked %d calls, want %d", idx, len(tc.want))
			}
			after, _ := json.Marshal(tc.in)
			if !bytes.Equal(before, after) {
				t.Fatal("repair mutated stored history")
			}
		})
	}
}

// TestRepairRejectedEmptyPoisonPassesGate is the 任务 930 poison-pill
// acceptance test at the provider layer: a completed wait call with
// arguments:"" must survive repair and then pass validation — before 930 the
// gate rejected it (permanent session poison), after S1+S2 the repaired
// outbound copy is a plain JSON object.
func TestRepairRejectedEmptyPoisonPassesGate(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "wait for jobs"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a", Name: "wait", Arguments: ""}}},
		{Role: RoleTool, ToolCallID: "a", Name: "wait", Content: "No background jobs to wait for.", ToolRunState: ToolRunCompleted},
		{Role: RoleAssistant, Content: "all done"},
	}
	repaired := RepairRejectedArguments(msgs)
	if err := ValidateTranscript(repaired); err != nil {
		t.Fatalf("repaired poison transcript still rejected: %v", err)
	}
	if err := ValidateModelTranscript(repaired); err != nil {
		t.Fatalf("repaired poison transcript rejected by model gate: %v", err)
	}
	if got := repaired[1].ToolCalls[0].Arguments; got != "{}" {
		t.Fatalf("outbound arguments=%q, want %q", got, "{}")
	}
}

func TestTranscriptGateModelProjectionHidesRecoveryArguments(t *testing.T) {
	const secret = "RECOVERY-RAW-ARGUMENT-ONLY"
	recovery := &InterruptedTurnRecovery{Pending: true, ToolCalls: []ToolCallRecord{{
		Identity:  ActionIdentity{CallID: "interrupted", CanonicalTool: "write_file"},
		Arguments: json.RawMessage(`{"content":"` + secret + `"}`), State: ToolRunUnknown,
	}}}
	for _, localOnly := range []bool{false, true} {
		name := "visible message metadata"
		if localOnly {
			name = "local recovery sentinel"
		}
		t.Run(name, func(t *testing.T) {
			stored := []Message{{Role: RoleUser, Content: "continue", LocalOnly: localOnly, InterruptedTurn: recovery}}
			before, _ := json.Marshal(stored)
			model := ModelMessages(stored)
			wire, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), secret) || strings.Contains(string(wire), "interrupted_turn") {
				t.Fatalf("provider projection leaked recovery metadata: %s", wire)
			}
			if err := ValidateTranscript(model); err != nil {
				t.Fatalf("projected transcript rejected: %v", err)
			}
			after, _ := json.Marshal(stored)
			if !bytes.Equal(before, after) {
				t.Fatal("projection deleted local recovery evidence")
			}
		})
	}
}
