package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// Task 107 P0-②: the model gets a read-only view of the fence, never a way to
// clear it.
func TestToolRecoveryToolIsReadOnlyAndHasNoResolveAction(t *testing.T) {
	tool := NewToolRecoveryTool()
	if !tool.ReadOnly() {
		t.Fatal("tool_recovery must be read-only")
	}
	if tool.Name() != "tool_recovery" {
		t.Fatalf("name = %q", tool.Name())
	}
	schema := string(tool.Schema())
	for _, forbidden := range []string{"confirm", "reject", "retry"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("schema must not offer %q: clearing the fence stays the user's decision", forbidden)
		}
	}
}

func TestToolRecoveryListReportsPendingEffectsWithoutArguments(t *testing.T) {
	a, _, _ := recoveryActionFixture(t)
	ctx := withAgentSelf(context.Background(), a)
	out, err := NewToolRecoveryTool().Execute(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "original") || !strings.Contains(out, "recovery_probe") {
		t.Fatalf("list report = %s, want the pending attempt and tool", out)
	}
	// The stored arguments are the executable payload of the blocked call, so
	// they must never be echoed back.
	if strings.Contains(out, `"arguments"`) {
		t.Fatalf("list report leaked arguments: %s", out)
	}
}

func TestToolRecoveryInspectReportsStateWithoutArguments(t *testing.T) {
	a, probe, _ := recoveryActionFixture(t)
	probe.inspection = tool.EffectInspection{State: "absent", Fenced: true}
	ctx := withAgentSelf(context.Background(), a)
	out, err := NewToolRecoveryTool().Execute(ctx, json.RawMessage(`{"action":"inspect","attempt":"original"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"inspection_state"`) {
		t.Fatalf("inspect report = %s", out)
	}
	if strings.Contains(out, `"arguments"`) {
		t.Fatalf("inspect report leaked arguments: %s", out)
	}
}

func TestToolRecoveryOutsideAnAgentTurnFailsClosed(t *testing.T) {
	if _, err := NewToolRecoveryTool().Execute(context.Background(), json.RawMessage(`{"action":"list"}`)); err == nil {
		t.Fatal("a plain context must not reach the fence")
	}
}
