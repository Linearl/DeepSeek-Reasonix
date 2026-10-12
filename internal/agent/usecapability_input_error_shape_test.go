package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestUseCapabilitySelfHealsStringifiedNestedCapabilityID covers the 798
// stringified member of the nested-capability_id family: the model wrapped the
// whole nested envelope in a JSON string ("the 457 habit one level deeper").
// The id promotes, the inner parameters dispatch, and one self-heal counts.
func TestUseCapabilitySelfHealsStringifiedNestedCapabilityID(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":"{\"capability_id\":\"tool:notify\",\"arguments\":{\"message\":\"hi\",\"to\":\"you\"}}"
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if resolved.CapabilityID != "tool:notify" {
		t.Fatalf("CapabilityID = %q, want tool:notify", resolved.CapabilityID)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want the inner target parameters", resolved.Args)
	}
	if resolved.Target == nil {
		t.Fatal("target missing after stringified nested-capability_id self-heal")
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestUseCapabilitySelfHealsStringifiedNestedFullEnvelope covers the 798
// whole-envelope member: a stringified complete call envelope (action inside)
// nested under arguments. The promoted remainder is still an envelope, so the
// task 212 detector takes its inner arguments once before dispatch.
func TestUseCapabilitySelfHealsStringifiedNestedFullEnvelope(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":"{\"action\":\"call\",\"capability_id\":\"tool:notify\",\"arguments\":{\"message\":\"hi\",\"to\":\"you\"}}"
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if resolved.CapabilityID != "tool:notify" {
		t.Fatalf("CapabilityID = %q, want tool:notify", resolved.CapabilityID)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want the inner target parameters", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestUseCapabilitySelfHealsNestedFullEnvelope covers the unstringified
// whole-envelope member (task 798): arguments carries the complete call
// envelope one level deep, action included. Without the 212 remainder unwrap
// the envelope keys would reach the target schema gate and fail the promotion.
func TestUseCapabilitySelfHealsNestedFullEnvelope(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{"action":"call","capability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if resolved.CapabilityID != "tool:notify" {
		t.Fatalf("CapabilityID = %q, want tool:notify", resolved.CapabilityID)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want the inner target parameters", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestUseCapabilityNestedFullEnvelopeRejectedKeepsMissingIDError guards the
// boundary: a whole-envelope promotion whose inner parameters fail the target
// schema never dispatches; the original missing-id error stands and no
// self-heal counts (728 discipline).
func TestUseCapabilityNestedFullEnvelopeRejectedKeepsMissingIDError(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	_, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{"action":"call","capability_id":"tool:notify","arguments":{"wrong":"x"}}
	}`))
	if err == nil {
		t.Fatal("expected the original missing-id error when the promoted inner parameters fail the target schema")
	}
	if !strings.Contains(err.Error(), "capability_id is required for action=call") {
		t.Fatalf("error = %v, want the original missing-id error", err)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 when the promotion also fails", got)
	}
}

// TestPromoteNestedCapabilityIDStringifiedBoundaries pins the structural
// tolerance boundary of the stringified extension (task 798): only a JSON
// string whose content is an object carrying a non-empty string capability_id
// promotes; double-encoded JSON, arrays, scalars, and plain text never do.
func TestPromoteNestedCapabilityIDStringifiedBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantID   string
		wantArgs string
		wantOK   bool
	}{
		{
			name:     "stringified nested envelope",
			raw:      `"{\"capability_id\":\"tool:task\",\"arguments\":{\"description\":\"d\"}}"`,
			wantID:   "tool:task",
			wantArgs: `{"description":"d"}`,
			wantOK:   true,
		},
		{
			name:     "stringified mixed parameters",
			raw:      `"{\"capability_id\":\"tool:notify\",\"message\":\"hi\"}"`,
			wantID:   "tool:notify",
			wantArgs: `{"message":"hi"}`,
			wantOK:   true,
		},
		{
			name:     "stringified whole envelope with action",
			raw:      `"{\"action\":\"call\",\"capability_id\":\"tool:task\",\"arguments\":{\"description\":\"d\"}}"`,
			wantID:   "tool:task",
			wantArgs: `{"description":"d"}`,
			wantOK:   true,
		},
		{
			name:     "stringified with trailing comma",
			raw:      `"{\"capability_id\":\"tool:task\",\"arguments\":{\"description\":\"d\"},}"`,
			wantID:   "tool:task",
			wantArgs: `{"description":"d"}`,
			wantOK:   true,
		},
		{name: "stringified plain text", raw: `"just text"`, wantOK: false},
		{name: "stringified array", raw: `"[1,2]"`, wantOK: false},
		{name: "stringified null", raw: `"null"`, wantOK: false},
		{name: "double-encoded envelope", raw: `"{\\\"capability_id\\\":\\\"tool:task\\\"}"`, wantOK: false},
		{name: "stringified empty id", raw: `"{\"capability_id\":\"  \"}"`, wantOK: false},
		{name: "stringified object without id", raw: `"{\"message\":\"hi\"}"`, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, inner, ok := promoteNestedCapabilityID(json.RawMessage(test.raw))
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v (id=%q args=%s)", ok, test.wantOK, id, inner)
			}
			if test.wantOK {
				if id != test.wantID {
					t.Fatalf("id = %q, want %q", id, test.wantID)
				}
				if string(inner) != test.wantArgs {
					t.Fatalf("inner = %s, want %s", inner, test.wantArgs)
				}
			}
		})
	}
}

// TestCapabilityInputErrorCarriesEnvelopeKeysSummary covers acceptance 2 of
// task 798 end to end: a capabilityInputError (missing capability_id here)
// surfaces the received top-level key list so the model can see the actual
// parameter shape and self-correct in one round. Values never leak.
func TestCapabilityInputErrorCarriesEnvelopeKeysSummary(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "shape-1", Name: "use_capability",
		Arguments: `{"action":"call","arguments":{"message":"hi","to":"you"}}`,
	})
	if !strings.Contains(out.output, "capability_id is required for action=call") {
		t.Fatalf("expected the missing-id error, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, "an object with top-level keys [action, arguments]") {
		t.Fatalf("expected the received keys summary, got:\n%s", out.output)
	}
	if strings.Contains(out.output, `"hi"`) {
		t.Fatal("argument values must never leak into the shape summary")
	}
}

// TestCapabilityInputErrorKeysSummaryOnInspectError pins that the summary
// rides every capabilityInputError path, not just action=call: an inspect
// call missing capability_id names the keys it actually sent.
func TestCapabilityInputErrorKeysSummaryOnInspectError(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "shape-2", Name: "use_capability",
		Arguments: `{"action":"inspect"}`,
	})
	if !strings.Contains(out.output, "capability_id is required for action=inspect") {
		t.Fatalf("expected the inspect missing-id error, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, "an object with top-level keys [action]") {
		t.Fatalf("expected the received keys summary, got:\n%s", out.output)
	}
}

// TestUseCapabilityPromotedCallResultCarriesUsageNote covers the 798
// no-silent-heal principle: a successful call whose capability_id was
// auto-promoted carries the correct-usage note in the model-visible result
// (the audit counter and WARN log are host-side only). This pins the direct
// proxy.Execute path.
func TestUseCapabilityPromotedCallResultCarriesUsageNote(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)

	result, err := proxy.Execute(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":"{\"capability_id\":\"tool:notify\",\"arguments\":{\"message\":\"hi\",\"to\":\"you\"}}"
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "capability_id was auto-promoted from arguments") {
		t.Fatalf("expected the promotion note on the result, got:\n%s", result)
	}
	if !strings.Contains(result, `{"sent":true}`) {
		t.Fatalf("expected the target result next to the note, got:\n%s", result)
	}
}

// TestUseCapabilityPromotedNoteRidesMainlineDispatch pins the note on the
// agent mainline (executeOne → resolved target dispatch), the path real
// provider calls take, for the plain object member of the family (728 shape).
func TestUseCapabilityPromotedNoteRidesMainlineDispatch(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "promote-1", Name: "use_capability",
		Arguments: `{"action":"call","arguments":{"capability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}}`,
	})
	if !strings.Contains(out.output, "capability_id was auto-promoted from arguments") {
		t.Fatalf("expected the promotion note on the mainline result, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, `{"sent":true}`) {
		t.Fatalf("expected the target result next to the note, got:\n%s", out.output)
	}
}

// TestUseCapabilityPromotedNoteRidesPathBoundDispatch pins the note on the
// path-bound subagent proxy, which re-executes the resolved target directly.
func TestUseCapabilityPromotedNoteRidesPathBoundDispatch(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	inner := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	bound := pathBoundCapabilityProxy{inner: inner, resolver: inner}

	result, err := bound.Execute(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{"action":"call","capability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}
	}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "capability_id was auto-promoted from arguments") {
		t.Fatalf("expected the promotion note on the path-bound result, got:\n%s", result)
	}
}

// TestUseCapabilityCorrectCallNeverCarriesPromotionNote guards zero noise: a
// correctly packaged call never sees the promotion note.
func TestUseCapabilityCorrectCallNeverCarriesPromotionNote(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "flat-1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"tool:notify","arguments":{"message":"hi","to":"you"}}`,
	})
	if strings.Contains(out.output, "auto-promoted") {
		t.Fatalf("a correct call must not carry the promotion note, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, `{"sent":true}`) {
		t.Fatalf("expected the plain target result, got:\n%s", out.output)
	}
}

// TestCapabilityInputErrorGuidesThreeObservedRetries pins the 2233 field
// report: one talk_to_session dispatch was retried three times inside a turn
// with different argument layouts (multi-line indented, single-line compact,
// minimal one-line) and each retry was refused for the missing capability_id.
// Every refusal must name the received top-level keys so the NEXT retry can
// fix the packaging instead of thrashing on the same error. Whitespace layout
// must not change the guidance.
func TestCapabilityInputErrorGuidesThreeObservedRetries(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)
	shapes := []struct{ name, arguments string }{
		{"multi-line indented", "{\n\t\"action\":\"call\",\n\t\"arguments\":{\"to\":\"team\",\"message\":\"hi\"}\n}"},
		{"single-line compact", `{"action":"call","arguments":{"to":"team","message":"hi"}}`},
		{"minimal one-line", `{"action":"call","arguments":{"to":"team"}}`},
	}
	for i, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
				ID: fmt.Sprintf("retry-%d", i), Name: "use_capability",
				Arguments: shape.arguments,
			})
			if !strings.Contains(out.output, "capability_id is required for action=call") {
				t.Fatalf("retry %d: expected the missing-id error, got:\n%s", i, out.output)
			}
			if !strings.Contains(out.output, "an object with top-level keys [action, arguments]") {
				t.Fatalf("retry %d: expected the received keys summary, got:\n%s", i, out.output)
			}
			if strings.Contains(out.output, `"team"`) {
				t.Fatalf("retry %d: argument values must never leak into the summary", i)
			}
		})
	}
}

// TestCapabilityInputErrorSummaryNamesStringDegradedArguments pins the 2233
// misdiagnosis concern: when the arguments value degraded to a JSON string
// (the 292 habit), the guidance must say it is a STRING and name the keys
// inside it — never silently present the string's keys as envelope keys.
// a) stringified bare target parameters keep the missing-id refusal and the
//
//	schema-violation path reports the wrapped keys;
//
// b) the whole call arguments degraded to one string reports the wrapped
//
//	envelope keys with the same "JSON string wrapping" naming.
func TestCapabilityInputErrorSummaryNamesStringDegradedArguments(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, nil, nil)
	reg.Add(proxy)
	a := New(nil, reg, NewSession("sys"), Options{}, event.Discard)

	// a) the model nested nothing but the target parameters, stringified.
	// The refusal is the schema face (arguments must be an object) and — the
	// 2233 fix — it must NOT claim a double envelope: the stringified payload
	// is bare target parameters, so the old "take the INNER arguments value"
	// guidance sent the model into a guaranteed second failure.
	out := a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "degraded-1", Name: "use_capability",
		Arguments: `{"action":"call","arguments":"{\"to\":\"team\",\"message\":\"hi\"}"}`,
	})
	if !strings.Contains(out.output, "/arguments: type; expected object") {
		t.Fatalf("expected the arguments-type violation, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, "an object with top-level keys [action, arguments]") {
		t.Fatalf("expected the received keys summary, got:\n%s", out.output)
	}
	if strings.Contains(out.output, "Double-enveloped call detected") {
		t.Fatalf("stringified bare parameters must not be misdiagnosed as a double envelope, got:\n%s", out.output)
	}

	// b) the whole arguments member arrived as one JSON string.
	out = a.executeOne(context.Background(), &a.turn, provider.ToolCall{
		ID: "degraded-2", Name: "use_capability",
		Arguments: `"{\"action\":\"call\",\"arguments\":{\"to\":\"team\",\"message\":\"hi\"}}"`,
	})
	if !strings.Contains(out.output, "argument validation failed") {
		t.Fatalf("expected the schema-violation report, got:\n%s", out.output)
	}
	if !strings.Contains(out.output, "a JSON string wrapping an object with top-level keys [action, arguments]") {
		t.Fatalf("expected the wrapped envelope keys summary, got:\n%s", out.output)
	}
}
