package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

// spreadNotifyTool mirrors the observed fourth-variant target: a two-parameter
// MCP-shaped tool (message+to) with a strict schema, so spread-parameter
// merging can be observed end to end (task 457).
type spreadNotifyTool struct{}

func (spreadNotifyTool) Name() string        { return "notify" }
func (spreadNotifyTool) Description() string { return "sends a notification" }
func (spreadNotifyTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"},"to":{"type":"string"}},"required":["message","to"],"additionalProperties":false}`)
}
func (spreadNotifyTool) ReadOnly() bool { return true }
func (spreadNotifyTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return `{"sent":true}`, nil
}

// TestUseCapabilitySelfHealsSpreadTopLevelParameters covers acceptance 1 of
// task 457: envelope-foreign top-level parameters (message+to) with no
// arguments object merge into one object, dispatch, and count one self-heal.
func TestUseCapabilitySelfHealsSpreadTopLevelParameters(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:notify",
		"message":"hi",
		"to":"you"
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want the merged spread parameters", resolved.Args)
	}
	if resolved.Target == nil {
		t.Fatal("target missing after spread-parameter self-heal")
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestUseCapabilitySpreadParametersRejectedKeepsOriginalArgs guards the
// self-heal boundary: spread parameters that cannot satisfy the target schema
// are never dispatched; the gate receives the arguments exactly as sent.
func TestUseCapabilitySpreadParametersRejectedKeepsOriginalArgs(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:notify",
		"wrong":"x"
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{}` {
		t.Fatalf("Args = %s, want the untouched empty arguments", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 when the merged form also fails", got)
	}
}

// TestUseCapabilitySpreadIgnoredWhenArgumentsPresent covers acceptance 3 of
// task 457: a normal flat call that CARRIES an arguments object is never
// rewritten, even when unrelated top-level keys ride along.
func TestUseCapabilitySpreadIgnoredWhenArgumentsPresent(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:notify",
		"arguments":{"message":"hi","to":"you"},
		"extraneous":"ignored"
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want verbatim flat arguments", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 when arguments were provided", got)
	}
}

// TestParseUseCapabilityArgsStringifiedIndentedMCPArguments covers acceptance
// 2 of task 457 at the parse layer: pretty-printed (newline+indent) JSON and
// a trailing comma inside a stringified arguments value both parse.
func TestParseUseCapabilityArgsStringifiedIndentedMCPArguments(t *testing.T) {
	raw := `{
		"action":"call",
		"capability_id":"mcp-tool:svc/notify",
		"arguments":"{\n  \"message\": \"hi\",\n  \"to\": \"you\",\n}"
	}`
	args, action, id, err := parseUseCapabilityArgs(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parseUseCapabilityArgs rejected an indented stringified object: %v", err)
	}
	if action != "call" || id != "mcp-tool:svc/notify" {
		t.Fatalf("action/id = %q/%q, want call/mcp-tool:svc/notify", action, id)
	}
	if string(args.Arguments) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Arguments = %s, want the parsed object", args.Arguments)
	}
	if !args.healedStringified {
		t.Fatal("healedStringified = false, want true")
	}
	if len(args.spreadParameters) != 0 {
		t.Fatalf("spreadParameters = %v, want none when arguments were present as a string", args.spreadParameters)
	}
}

// TestUnwrapStringifiedJSONObject pins the tolerance boundary of the
// stringified-JSON parse (task 457).
func TestUnwrapStringifiedJSONObject(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   string
		wantOK bool
	}{
		{name: "plain object", raw: `"{\"message\":\"hi\"}"`, want: `{"message":"hi"}`, wantOK: true},
		{name: "indented object", raw: "\"{\\n  \\\"message\\\": \\\"hi\\\"\\n}\"", want: `{"message":"hi"}`, wantOK: true},
		{name: "trailing comma", raw: `"{\"message\":\"hi\",}"`, want: `{"message":"hi"}`, wantOK: true},
		{name: "empty object", raw: `"{}"`, want: `{}`, wantOK: true},
		{name: "plain text", raw: `"just a task"`, wantOK: false},
		{name: "stringified array", raw: `"[1,2]"`, wantOK: false},
		{name: "double-encoded", raw: `"\"{}\""`, wantOK: false},
		{name: "malformed inner", raw: `"{"`, wantOK: false},
		{name: "not a string", raw: `{"message":"hi"}`, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := unwrapStringifiedJSONObject(json.RawMessage(test.raw))
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v (got %s)", ok, test.wantOK, got)
			}
			if test.wantOK && string(got) != test.want {
				t.Fatalf("unwrapped = %s, want %s", got, test.want)
			}
		})
	}
}

// TestSpreadFailureMessageShowsSnapshotAndExample covers acceptance 4 of task
// 457: a spread-parameter call that reaches the gate as empty arguments fails
// with the received shape, a minimal valid example, and no execution.
func TestSpreadFailureMessageShowsSnapshotAndExample(t *testing.T) {
	target := spreadNotifyTool{}
	blocked, msg := hostValidateBeforeDispatch(target, json.RawMessage(`{}`), "tool:notify")
	if !blocked {
		t.Fatal("expected the gate to block empty arguments for notify")
	}
	for _, want := range []string{
		"Actual arguments received: an object with top-level keys []",
		`Minimal valid arguments example: {"message":"…","to":"…"}`,
		"The target was not executed.",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

// TestParseUseCapabilityArgsSpreadCaptureOnlyForCall pins that spread capture
// never leaks into discovery or decline actions.
func TestParseUseCapabilityArgsSpreadCaptureOnlyForCall(t *testing.T) {
	args, action, _, err := parseUseCapabilityArgs(json.RawMessage(`{
		"action":"search",
		"query":"notify",
		"whatever":1
	}`))
	if err != nil {
		t.Fatalf("parseUseCapabilityArgs: %v", err)
	}
	if action != "search" {
		t.Fatalf("action = %q, want search", action)
	}
	if len(args.spreadParameters) != 0 {
		t.Fatalf("spreadParameters = %v, want none for action=search", args.spreadParameters)
	}
}

// TestUseCapabilitySpreadHealPlainCallUnchanged re-runs the plain-call
// invariant (task 212) against the spread heal path: a valid flat call with a
// single parameter resolves byte-for-byte and never counts a self-heal.
func TestUseCapabilitySpreadHealPlainCallUnchanged(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:notify",
		"arguments":{"message":"hi","to":"you"}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want untouched flat arguments", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 for plain calls", got)
	}
}
