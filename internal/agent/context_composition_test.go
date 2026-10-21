package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// coldTextTokens pins the cold ~4-bytes-per-token fallback (no session
// calibration), matching the byte-based estimator the pre-任务1025 composition
// used — so bucketing tests keep their exact token arithmetic while the agent
// method injects the session-calibrated ratio.
func coldTextTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

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

	comp := computeContextComposition(messages, schemas, coldTextTokens)

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
	comp := computeContextComposition(nil, nil, coldTextTokens)
	if comp.TotalTokens != 0 || comp.SystemPromptTokens != 0 || comp.MessageTokens != 0 {
		t.Fatalf("empty view must measure zero: %+v", comp)
	}
}

// 任务1025（缺口5验收）：composition TotalTokens 与 gauge 主读数
// （estimatedVisibleRequestTokens）同口径——用同一 normalized 视图与校准估算器
// 后，分项和必须等于主读数，不再出现「566.3K vs 488.5K」的对不上。
func TestContextCompositionTotalMatchesGaugeHeadline(t *testing.T) {
	a := &Agent{sess: sessionRuntime{output: outputBudgetState{}}}
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: strings.Repeat("sys-prompt-", 100)},
		{Role: provider.RoleUser, Content: strings.Repeat("question-", 200)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("answer-", 150), ReasoningContent: strings.Repeat("think-", 60)},
	}
	a.sess.conversation = NewSession("")
	a.sess.conversation.AddBatch(msgs...)

	gauge := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	comp := a.ContextComposition()
	if comp.TotalTokens <= 0 {
		t.Fatalf("composition total = %d, want > 0", comp.TotalTokens)
	}
	if comp.TotalTokens != gauge {
		t.Fatalf("composition total = %d, want the gauge headline %d (缺口5: 分项和必须等于主读数)", comp.TotalTokens, gauge)
	}
}
