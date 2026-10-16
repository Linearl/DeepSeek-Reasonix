package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// validGateArguments reports whether a tool call's argument payload satisfies
// the gate's JSON-object contract. Empty and whitespace-only arguments are
// accepted as the legal no-argument form (equivalent to "{}"): all-optional
// tools such as wait legitimately emit arguments:"" (task 930), and rejecting
// them made the whole request unsendable — a permanent poison pill once the
// call had already executed. The stored bytes are never rewritten here;
// outbound normalization happens in RepairRejectedArguments.
func validGateArguments(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	var args map[string]json.RawMessage
	return json.Unmarshal([]byte(raw), &args) == nil && args != nil
}

// ValidateTranscript validates a request view without changing history or
// inventing tool results. IDs are scoped to one assistant batch.
func ValidateTranscript(msgs []Message) error {
	pending := map[string]string{}
	for i, m := range msgs {
		if m.Role != RoleTool && len(pending) != 0 {
			return fmt.Errorf("transcript gate: unanswered tool calls before message %d", i)
		}
		for _, c := range m.ToolCalls {
			if m.Role != RoleAssistant || strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Name) == "" {
				return fmt.Errorf("transcript gate: invalid tool identity at message %d", i)
			}
			if _, exists := pending[c.ID]; exists {
				return fmt.Errorf("transcript gate: duplicate call ID at message %d", i)
			}
			if !validGateArguments(c.Arguments) {
				return fmt.Errorf("transcript gate: tool arguments must be a JSON object at message %d", i)
			}
			pending[c.ID] = c.Name
		}
		if m.Role == RoleTool {
			name, found := pending[m.ToolCallID]
			if !found || (m.Name != "" && m.Name != name) {
				return fmt.Errorf("transcript gate: orphan or mismatched result at message %d", i)
			}
			delete(pending, m.ToolCallID)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("transcript gate: unanswered tool calls")
	}
	return nil
}

// ValidateModelTranscript uses the adapters' pairing-normalized view. Invalid
// arguments are rejected before normalizers can silently replace them.
func ValidateModelTranscript(msgs []Message) error {
	view := ModelMessages(msgs)
	for i, m := range view {
		for _, c := range m.ToolCalls {
			if !validGateArguments(c.Arguments) {
				return fmt.Errorf("transcript gate: tool arguments must be a JSON object at message %d", i)
			}
		}
	}
	view = append([]Message(nil), SanitizeToolPairing(view)...)
	// Some compatible gateways stream by index and omit IDs. Validate using
	// request-local positional identities, retaining their existing wire path.
	var empty []ToolCall
	for i := range view {
		m := &view[i]
		if len(m.ToolCalls) > 0 {
			empty = nil
			m.ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
			for j := range m.ToolCalls {
				if m.ToolCalls[j].ID == "" {
					m.ToolCalls[j].ID = fmt.Sprintf("legacy-%d-%d", i, j)
					empty = append(empty, m.ToolCalls[j])
				}
			}
		} else if m.Role == RoleTool && m.ToolCallID == "" {
			for j, c := range empty {
				if c.Name == m.Name {
					m.ToolCallID = c.ID
					empty = append(empty[:j], empty[j+1:]...)
					break
				}
			}
		}
	}
	return ValidateTranscript(view)
}

// RepairRejectedArguments changes only the outbound copy of calls the host
// proved never ran. Their validation-error result remains available for the
// model to correct its next proposal; stored arguments remain inspectable.
//
// Empty (or whitespace-only) arguments are additionally normalized to "{}"
// for every call, executed or not (task 930): they carry no parameters to
// preserve, and a completed no-argument call (wait etc.) previously fell
// through the never-ran whitelist below, leaving the stored empty string to
// be re-rejected on every future request — a permanent poison pill. Only the
// empty string is rewritten; non-empty rejected arguments keep the original
// never-ran-only rule.
func RepairRejectedArguments(msgs []Message) []Message {
	out := append([]Message(nil), msgs...)
	for i, m := range out {
		if m.Role != RoleAssistant {
			continue
		}
		for j, c := range m.ToolCalls {
			if strings.TrimSpace(c.Arguments) == "" {
				out[i].ToolCalls = append([]ToolCall(nil), out[i].ToolCalls...)
				out[i].ToolCalls[j].Arguments = "{}"
				continue
			}
			var args map[string]json.RawMessage
			if json.Unmarshal([]byte(c.Arguments), &args) == nil && args != nil {
				continue
			}
			for k := i + 1; k < len(out) && out[k].Role == RoleTool; k++ {
				r := out[k]
				if r.ToolCallID == c.ID && r.Name == c.Name && (r.ToolRunState == ToolRunNotStarted || r.ToolRunState == ToolRunCancelled) {
					out[i].ToolCalls = append([]ToolCall(nil), out[i].ToolCalls...)
					out[i].ToolCalls[j].Arguments = "{}"
					break
				}
			}
		}
	}
	return out
}
