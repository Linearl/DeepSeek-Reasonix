package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/capability"
	"reasonix/internal/tool"
)

// TestUseCapabilitySelfHealsNestedCapabilityID covers acceptance 1 of task
// 728: the observed failure shape of the 725 investigation (capability_id
// nested inside the arguments object, target parameters one level deeper)
// resolves end to end, dispatches the inner parameters, and counts one
// self-heal.
func TestUseCapabilitySelfHealsNestedCapabilityID(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{
			"capability_id":"tool:notify",
			"arguments":{"message":"hi","to":"you"}
		}
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
		t.Fatal("target missing after nested-capability_id self-heal")
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestUseCapabilitySelfHealsNestedCapabilityIDMixedParameters covers the
// variant where the model places the target parameters directly next to the
// nested capability_id: promotion keeps the remaining keys as the target
// arguments (task 728 R1 wording).
func TestUseCapabilitySelfHealsNestedCapabilityIDMixedParameters(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	resolved, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{"capability_id":"tool:notify","message":"hi","to":"you"}
	}`))
	if err != nil {
		t.Fatalf("ResolveCall: %v", err)
	}
	if resolved.CapabilityID != "tool:notify" {
		t.Fatalf("CapabilityID = %q, want tool:notify", resolved.CapabilityID)
	}
	if string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("Args = %s, want the remaining keys as target arguments", resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 1 {
		t.Fatalf("SelfHealed = %d, want 1", got)
	}
}

// TestUseCapabilityNestedCapabilityIDRejectedKeepsMissingIDError guards the
// self-heal boundary: a promoted form that cannot satisfy the target schema
// never dispatches; the gate keeps the original missing-id error and no
// self-heal is counted (task 457 discipline).
func TestUseCapabilityNestedCapabilityIDRejectedKeepsMissingIDError(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	_, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{"capability_id":"tool:notify","arguments":{"wrong":"x"}}
	}`))
	if err == nil {
		t.Fatal("expected the original missing-id error when the promoted form fails the target schema")
	}
	if !strings.Contains(err.Error(), "capability_id is required for action=call") {
		t.Fatalf("error = %v, want the original missing-id error", err)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 when the promoted form also fails", got)
	}
}

// TestUseCapabilityMissingCapabilityIDStillFails covers acceptance 2 of task
// 728: a call that genuinely lacks capability_id anywhere keeps failing with
// the original error (never swallowed), now carrying the R3 placement hint.
func TestUseCapabilityMissingCapabilityIDStillFails(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(spreadNotifyTool{})
	audit := &capability.Audit{}
	proxy := NewUseCapabilityTool(context.Background(), nil, nil, reg, nil, audit, nil)

	// Form B of the 725 report: arguments holds only the target parameters.
	_, err := proxy.ResolveCall(t.Context(), json.RawMessage(`{
		"action":"call",
		"arguments":{"message":"hi","to":"you"}
	}`))
	if err == nil {
		t.Fatal("expected the missing-id error for a call without any capability_id")
	}
	if !strings.Contains(err.Error(), "capability_id is required for action=call") {
		t.Fatalf("error = %v, want the missing-id error", err)
	}
	if !strings.Contains(err.Error(), "capability_id must be a top-level field, not inside arguments") {
		t.Fatalf("error = %v, want the R3 top-level placement hint", err)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 for a genuinely missing id", got)
	}
}

// TestUseCapabilityTopLevelCapabilityIDNeverHealed pins zero regression: a
// correct flat call with a top-level capability_id never enters the promotion
// branch and never counts a self-heal.
func TestUseCapabilityTopLevelCapabilityIDNeverHealed(t *testing.T) {
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
	if resolved.CapabilityID != "tool:notify" || string(resolved.Args) != `{"message":"hi","to":"you"}` {
		t.Fatalf("resolved = %q/%s, want the untouched flat call", resolved.CapabilityID, resolved.Args)
	}
	if got := audit.Snapshot().Arguments.SelfHealed; got != 0 {
		t.Fatalf("SelfHealed = %d, want 0 for top-level capability_id", got)
	}
}

// TestPromoteNestedCapabilityIDBoundaries pins the structural tolerance
// boundary of the promotion helper (task 728): only a JSON object carrying a
// non-empty string capability_id promotes.
func TestPromoteNestedCapabilityIDBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantID   string
		wantArgs string
		wantOK   bool
	}{
		{
			name:     "nested envelope shape",
			raw:      `{"capability_id":"tool:task","arguments":{"description":"d"}}`,
			wantID:   "tool:task",
			wantArgs: `{"description":"d"}`,
			wantOK:   true,
		},
		{
			name:     "mixed parameters",
			raw:      `{"capability_id":"tool:notify","message":"hi"}`,
			wantID:   "tool:notify",
			wantArgs: `{"message":"hi"}`,
			wantOK:   true,
		},
		{
			name:     "lone null arguments",
			raw:      `{"capability_id":"tool:notify","arguments":null}`,
			wantID:   "tool:notify",
			wantArgs: `{}`,
			wantOK:   true,
		},
		{name: "absent arguments member", raw: `{"message":"hi"}`, wantOK: false},
		{name: "nested id not a string", raw: `{"capability_id":{"id":"tool:x"}}`, wantOK: false},
		{name: "nested id empty string", raw: `{"capability_id":"  "}`, wantOK: false},
		{name: "not an object", raw: `"tool:notify"`, wantOK: false},
		{name: "absent", raw: ``, wantOK: false},
		{name: "null", raw: `null`, wantOK: false},
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
