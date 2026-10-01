package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// Task 442 — the composition segments must bucket the visible view exactly:
// system messages → system prompt, skill-tool results → skills, mcp__ schemas
// → MCP tools, other schemas → builtin tools, everything else → messages, and
// the segments must sum to the composition total.
func TestComputeContextCompositionBucketsAndSums(t *testing.T) {
	messages := []provider.Message{
		// 100 / 50 / 30 / 30 / 200 / 10 tokens at ~4 bytes per token.
		{Role: provider.RoleSystem, Content: strings.Repeat("s", 400)},
		{Role: provider.RoleUser, Content: strings.Repeat("u", 200)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("a", 80), ReasoningContent: strings.Repeat("r", 40)},
		{Role: provider.RoleTool, Name: "bash", Content: strings.Repeat("b", 120)},
		{Role: provider.RoleTool, Name: "run_skill", Content: strings.Repeat("k", 800)},
		{Role: provider.RoleTool, Name: "read_only_skill", Content: strings.Repeat("K", 40)},
	}
	schemas := []provider.ToolSchema{
		{Name: "bash", Description: strings.Repeat("d", 200)}, // 50+ tokens
		// Name + Description + Parameters serialized together by json.Marshal;
		// Parameters must be VALID JSON (RawMessage) or Marshal skips the schema.
		{Name: "mcp__fs__read", Description: strings.Repeat("m", 200), Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
	}

	comp := computeContextComposition(messages, schemas)

	if comp.SystemPromptTokens != 100 {
		t.Fatalf("system prompt segment = %d, want 100", comp.SystemPromptTokens)
	}
	if comp.SkillTokens != 210 {
		t.Fatalf("skill segment = %d, want 210 (run_skill + read_only_skill tool results)", comp.SkillTokens)
	}
	if comp.MessageTokens != 110 {
		t.Fatalf("message segment = %d, want 110 (user + assistant incl. reasoning + ordinary tool result)", comp.MessageTokens)
	}
	if comp.BuiltinToolTokens < 50 || comp.BuiltinToolTokens > 200 {
		t.Fatalf("builtin tool segment = %d, want the bash schema's serialized cost (>= 50, < 200)", comp.BuiltinToolTokens)
	}
	if comp.McpToolTokens < 45 {
		t.Fatalf("mcp tool segment = %d, want >= 45 (description + parameters)", comp.McpToolTokens)
	}
	if comp.TotalTokens != comp.SystemPromptTokens+comp.BuiltinToolTokens+comp.SkillTokens+comp.McpToolTokens+comp.MessageTokens {
		t.Fatalf("segments do not sum to total: %+v", comp)
	}
	if comp.TotalTokens <= 0 {
		t.Fatalf("total = %d, want > 0", comp.TotalTokens)
	}
}

func TestComputeContextCompositionEmptyViewIsEmpty(t *testing.T) {
	comp := computeContextComposition(nil, nil)
	if comp.TotalTokens != 0 || comp.SystemPromptTokens != 0 || comp.MessageTokens != 0 {
		t.Fatalf("empty view must measure zero: %+v", comp)
	}
}
