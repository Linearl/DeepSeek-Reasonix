package agent

import (
	"encoding/json"
	"strings"

	"reasonix/internal/provider"
)

// Task 442 — context composition segments for the composer gauge popup.
//
// The gauge headline (ContextUsedTokens) measures the next request with the
// calibration machinery; the popup's segmented bar only needs each segment's
// SHARE of the composition, so every segment is measured with the same
// byte-based estimator (estimateTokens, ~4 bytes per token) and the shares
// sum to 100% by construction. Skills are attributed where they really live:
// a skill body enters the transcript as the tool result of run_skill /
// read_only_skill / read_skill, so those tool results form the skill segment.
// Everything that is not a system message, a tool schema, or a skill tool
// result — user prompts, assistant output, ordinary tool results — is the
// message segment.
type ContextComposition struct {
	// SystemPromptTokens covers every system-role message in the visible view
	// (the base prompt plus host/extension system content).
	SystemPromptTokens int `json:"systemPromptTokens"`
	// BuiltinToolTokens is the JSON schema cost of every provider-visible tool
	// that is not MCP-backed.
	BuiltinToolTokens int `json:"builtinToolTokens"`
	// SkillTokens is the tool-result cost of skill bodies delivered through
	// the skill tools the visible view carries.
	SkillTokens int `json:"skillTokens"`
	// McpToolTokens is the JSON schema cost of mcp__-prefixed tools.
	McpToolTokens int `json:"mcpToolTokens"`
	// MessageTokens is everything else: user/assistant content (reasoning
	// bodies and tool-call arguments included) plus ordinary tool results.
	MessageTokens int `json:"messageTokens"`
	// TotalTokens is the sum of the segments above — the composition's own
	// total, kept separate from the calibrated gauge number.
	TotalTokens int `json:"totalTokens"`
}

// skillToolNames mirrors the routing list in usecapability.resolveSkillCall:
// a tool result with one of these names carries a rendered skill body.
var skillToolNames = map[string]bool{
	"run_skill":       true,
	"read_only_skill": true,
	"read_skill":      true,
}

// ContextComposition measures the visible view's segments. A zero TotalTokens
// means no data yet (no executor/session) — callers hide the segmented bar.
func (a *Agent) ContextComposition() ContextComposition {
	if a == nil {
		return ContextComposition{}
	}
	session := a.Session()
	if session == nil {
		return ContextComposition{}
	}
	return computeContextComposition(a.modelVisibleMessages(), a.providerToolSchemas())
}

// computeContextComposition is the pure measurement core, split from the
// method so tests can pin the bucketing without building a live agent.
func computeContextComposition(messages []provider.Message, schemas []provider.ToolSchema) ContextComposition {
	comp := ContextComposition{}
	for _, msg := range messages {
		size := len(msg.Content) + len(msg.ReasoningContent)
		for _, call := range msg.ToolCalls {
			size += len(call.Arguments)
		}
		switch {
		case msg.Role == provider.RoleSystem:
			comp.SystemPromptTokens += estimateTokensFromBytes(size)
		case msg.Role == provider.RoleTool && skillToolNames[msg.Name]:
			comp.SkillTokens += estimateTokensFromBytes(size)
		default:
			comp.MessageTokens += estimateTokensFromBytes(size)
		}
	}
	for _, schema := range schemas {
		b, err := json.Marshal(schema)
		if err != nil {
			continue
		}
		if strings.HasPrefix(schema.Name, "mcp__") {
			comp.McpToolTokens += estimateTokens(string(b))
		} else {
			comp.BuiltinToolTokens += estimateTokens(string(b))
		}
	}
	comp.TotalTokens = comp.SystemPromptTokens + comp.BuiltinToolTokens + comp.SkillTokens + comp.McpToolTokens + comp.MessageTokens
	return comp
}
