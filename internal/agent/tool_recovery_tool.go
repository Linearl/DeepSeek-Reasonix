package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ToolRecoveryTool is the read-only self-diagnosis channel for the recovery
// fence (task 107 P0-②). The fence keeps writes blocked while an earlier
// external effect is unresolved, and clearing it used to be possible only from
// the desktop panel; this tool lets the model say exactly what is pending and
// inspect one attempt. It deliberately has no confirm/reject/retry action:
// those remain the user's decision.
type ToolRecoveryTool struct{}

func NewToolRecoveryTool() *ToolRecoveryTool { return &ToolRecoveryTool{} }

func (*ToolRecoveryTool) Name() string { return "tool_recovery" }

func (*ToolRecoveryTool) Description() string {
	return "Read the recovery fence: which earlier tool calls left an unconfirmed external effect, and what state one of them is in. Use it after a `recovery_required` block so you can state exactly what is pending instead of guessing, then hand the decision to the user. Read-only: it never confirms, rejects, or retries an effect."
}

func (*ToolRecoveryTool) ReadOnly() bool { return true }

func (*ToolRecoveryTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "action":{"type":"string","enum":["list","inspect"],"description":"list = every unresolved effect (attempt id, tool, state); inspect = record an inspection of one attempt and report what it found."},
  "attempt":{"type":"string","description":"Attempt id reported by list. Required for inspect."}
},
"required":["action"]
}`)
}

func (*ToolRecoveryTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Action  string `json:"action"`
		Attempt string `json:"attempt"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("tool_recovery: %w", err)
	}
	a, ok := AgentFromContext(ctx)
	if !ok || a == nil {
		return "", fmt.Errorf("tool_recovery requires an active agent session")
	}
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "list":
		return a.recoveryFenceReport()
	case "inspect":
		attempt := strings.TrimSpace(in.Attempt)
		if attempt == "" {
			return "", fmt.Errorf("tool_recovery inspect: attempt is required (call list first)")
		}
		record, err := a.InspectToolRecovery(ctx, attempt)
		if err != nil {
			return "", fmt.Errorf("tool_recovery inspect: %w", err)
		}
		return marshalRecoveryReport(map[string]any{
			"attempt":          record.Identity.AttemptID,
			"tool":             record.Identity.CanonicalTool,
			"inspection_state": record.InspectionState,
			"inspection_id":    record.InspectionID,
			"read_only":        record.ReadOnly,
			"note": "Reporting only. Only the user can clear the fence: the desktop panel 「中断的工具需要核实」 " +
				"offers Inspect current state / I verified the effect happened / Do not retry, and the host may release " +
				"a barrier on its own when it can fence the effect as absent.",
		})
	default:
		return "", fmt.Errorf("tool_recovery: unsupported action %q (use list or inspect)", in.Action)
	}
}

// recoveryFenceReport lists the unresolved effects. Identities and states only:
// the stored arguments are deliberately never echoed back, because they are the
// executable payload of the blocked call (task 107 P0-②'s hard constraint).
func (a *Agent) recoveryFenceReport() (string, error) {
	pending := a.PendingToolRecovery()
	items := make([]map[string]any, 0, len(pending))
	for _, r := range pending {
		item := map[string]any{
			"attempt":   r.Identity.AttemptID,
			"tool":      r.Identity.CanonicalTool,
			"state":     string(r.State),
			"read_only": r.ReadOnly,
		}
		if r.StartedAt > 0 {
			item["started_at"] = time.UnixMilli(r.StartedAt).UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	report := map[string]any{
		"pending": items,
		"note":    "Writes stay blocked until each pending effect is resolved. Only the user can clear the fence from the desktop panel 「中断的工具需要核实」; the host releases one on its own only when a tool can fence the effect as absent.",
	}
	if len(items) == 0 {
		report["note"] = "No unresolved external effect is pending."
	}
	return marshalRecoveryReport(report)
}

func marshalRecoveryReport(report map[string]any) (string, error) {
	payload, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("tool_recovery: %w", err)
	}
	return string(payload), nil
}

// agentSelfContextKey carries the executing agent into tool calls, so a tool can
// read session-scoped host state (the recovery fence) without importing control.
type agentSelfContextKey struct{}

func withAgentSelf(ctx context.Context, a *Agent) context.Context {
	return context.WithValue(ctx, agentSelfContextKey{}, a)
}

// AgentFromContext returns the agent executing this tool call, if the call came
// from an agent turn.
func AgentFromContext(ctx context.Context) (*Agent, bool) {
	a, ok := ctx.Value(agentSelfContextKey{}).(*Agent)
	return a, ok && a != nil
}
