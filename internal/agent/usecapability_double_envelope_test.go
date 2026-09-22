package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// doubleEnvelopePingTool is a minimal registry target with a strict schema so
// double-envelope self-healing can be observed end to end (task 212).
type doubleEnvelopePingTool struct{ name string }

func (t *doubleEnvelopePingTool) Name() string        { return t.name }
func (t *doubleEnvelopePingTool) Description() string { return "answers with pong" }
func (t *doubleEnvelopePingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`)
}
func (*doubleEnvelopePingTool) ReadOnly() bool { return true }
func (*doubleEnvelopePingTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return `{"pong":true}`, nil
}

func TestUseCapabilitySelfHealsDoubleEnvelopedArguments(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&doubleEnvelopePingTool{name: "ping"})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:ping",
		"arguments":{"arguments":{"message":"hi"},"capability_id":"tool:ping"}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"message":"hi"}` {
		t.Fatalf("Args = %s, want the unwrapped inner arguments", resolved.Args)
	}
	if resolved.Target == nil {
		t.Fatal("target missing after self-heal")
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

func TestUseCapabilitySelfHealsStringifiedEnvelopeForMCPTool(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&doubleEnvelopePingTool{name: "ping"})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	// The arguments member is a JSON string containing a full call envelope —
	// the exact output-layer shape observed in task 212's field evidence.
	raw := `{
		"action":"call",
		"capability_id":"mcp-tool:svc/ping",
		"arguments":"{\"arguments\":{\"message\":\"hi\"},\"capability_id\":\"mcp-tool:svc/ping\"}"
	}`
	if _, _, _, err := parseUseCapabilityArgs(json.RawMessage(raw)); err != nil {
		t.Fatalf("parseUseCapabilityArgs rejected a stringified envelope: %v", err)
	}
	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"message":"hi"}` {
		t.Fatalf("Args = %s, want the unwrapped inner arguments", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

func TestUseCapabilityPlainCallBehaviorUnchanged(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&doubleEnvelopePingTool{name: "ping"})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:ping",
		"arguments":{"message":"hi"}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"message":"hi"}` {
		t.Fatalf("Args = %s, want untouched plain arguments", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 for plain calls", got)
	}
}

func TestUseCapabilityDoubleEnvelopeStillInvalidKeepsOriginalArgs(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(&doubleEnvelopePingTool{name: "ping"})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	// The inner arguments also miss the schema (message is a number), so
	// self-healing must not fire: the original arguments reach the gate.
	original := `{"arguments":{"message":7},"capability_id":"tool:ping"}`
	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:ping",
		"arguments":`+original+`
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != compactJSON(t, original) {
		t.Fatalf("Args = %s, want the original double envelope %s", resolved.Args, original)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 when the inner form also fails", got)
	}
}

func TestUseCapabilityLegitimateArgumentsFieldNotUnwrapped(t *testing.T) {
	// A target whose own schema legitimately contains an "arguments" member
	// (run_skill's shape) must never be treated as a double envelope.
	reg := tool.NewRegistry()
	reg.Add(&runSkillShapedTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"capability_id":"tool:run_skill_shaped",
		"arguments":{"name":"review","arguments":"audit the diff"}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if string(resolved.Args) != `{"name":"review","arguments":"audit the diff"}` {
		t.Fatalf("Args = %s, want untouched run_skill-shaped arguments", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0", got)
	}
}

type runSkillShapedTool struct{}

func (*runSkillShapedTool) Name() string        { return "run_skill_shaped" }
func (*runSkillShapedTool) Description() string { return "mimics run_skill's schema" }
func (*runSkillShapedTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"arguments":{"type":"string"}},"required":["name","arguments"],"additionalProperties":false}`)
}
func (*runSkillShapedTool) ReadOnly() bool { return true }
func (*runSkillShapedTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "ran", nil
}

func TestDetectDoubleEnvelopedArguments(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantOK  bool
	}{
		{name: "object envelope", raw: `{"arguments":{"a":1},"capability_id":"tool:ping"}`, want: `{"a":1}`, wantOK: true},
		{name: "stringified envelope", raw: `"{\"arguments\":{\"a\":1},\"capability_id\":\"tool:ping\"}"`, want: `{"a":1}`, wantOK: true},
		{name: "action marker only", raw: `{"arguments":{"a":1},"action":"call"}`, want: `{"a":1}`, wantOK: true},
		{name: "plain object", raw: `{"to":"x","message":"hi"}`, wantOK: false},
		{name: "run_skill shape", raw: `{"name":"review","arguments":"task"}`, wantOK: false},
		{name: "arguments without marker", raw: `{"arguments":{"a":1}}`, wantOK: false},
		{name: "foreign key", raw: `{"arguments":{"a":1},"capability_id":"x","title":"t"}`, wantOK: false},
		{name: "string without envelope", raw: `"just a task"`, wantOK: false},
		{name: "empty", raw: ``, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := detectDoubleEnvelopedArguments(json.RawMessage(test.raw))
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v (got %s)", ok, test.wantOK, got)
			}
			if test.wantOK && string(got) != test.want {
				t.Fatalf("inner = %s, want %s", got, test.want)
			}
		})
	}
}

func TestArgumentValidationMessageShowsActualVersusExpected(t *testing.T) {
	target := &doubleEnvelopePingTool{name: "ping"}
	plan := &toolCallPlan{
		permName: "ping",
		execTool: target,
		execArgs: json.RawMessage(`{"arguments":{"message":"hi"},"capability_id":"tool:ping"}`),
		call:     provider.ToolCall{Name: "use_capability"},
		resolved: tool.ResolvedCall{CapabilityID: "tool:ping"},
	}
	result := tool.ValidateArguments(target, plan.execArgs)
	if result.Skipped || len(result.Violations) == 0 {
		t.Fatalf("expected validation violations, got %+v", result)
	}
	msg := argumentValidationMessage(plan, result)
	for _, want := range []string{
		"Actual arguments received: an object with top-level keys [arguments, capability_id]",
		`Minimal valid arguments example: {"message":"…"}`,
		"Double-enveloped call detected",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestDedupeProviderVisibleResultKeepsRepeatedErrors(t *testing.T) {
	a := &Agent{}
	raw := "error: capability unavailable: server not connected\nretry with action=list"
	first := a.dedupeProviderVisibleResult("use_capability", "c1", raw, raw)
	second := a.dedupeProviderVisibleResult("use_capability", "c2", raw, raw)
	if first != raw || second != raw {
		t.Fatalf("repeated error was deduped: first=%q second=%q", first, second)
	}

	// Non-error results keep the existing dedup behavior.
	plain := "plain result"
	if got := a.dedupeProviderVisibleResult("use_capability", "c3", plain, plain); got != plain {
		t.Fatalf("plain first = %q", got)
	}
	if got := a.dedupeProviderVisibleResult("use_capability", "c4", plain, plain); !strings.Contains(got, "duplicate tool result") {
		t.Fatalf("plain second = %q, want dedup notice", got)
	}
}

func compactJSON(t *testing.T, raw string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("compactJSON: %v", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("compactJSON marshal: %v", err)
	}
	return string(b)
}
