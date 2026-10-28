package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
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

// tl1224 阶段二验收（三段式统计归属）：<session-context> 信封拆为跨会话记忆/
// 目录/运行时快照三桶（信封帧归运行时），<memory-recall> 尾部单列召回桶，
// 历史消息只收普通对话内容；Σ全部分项仍等于 TotalTokens（缺口5 不变量）。
func TestComputeContextCompositionAttributesEnvelopeAndRecall(t *testing.T) {
	snapshot := sessioncontext.Build(sessioncontext.Sections{
		Environment:       strings.Repeat("e", 200),
		Workspace:         strings.Repeat("w", 100),
		SkillsCatalog:     strings.Repeat("c", 160),
		CapabilityCatalog: strings.Repeat("p", 240),
		BackgroundMemory:  strings.Repeat("m", 400),
	})
	recall := "<memory-recall>\n" + strings.Repeat("f", 200) + "\n</memory-recall>"
	history := strings.Repeat("q", 120)
	messages := []provider.Message{
		{Role: provider.RoleUser, Content: snapshot.Content},
		{Role: provider.RoleUser, Content: history + "\n\n" + recall},
		{Role: provider.RoleAssistant, Content: strings.Repeat("a", 80)},
	}

	comp := computeContextComposition(messages, nil, coldTextTokens)

	if got, want := comp.StandingMemoryTokens, coldTextTokens(strings.Repeat("m", 400)); got != want {
		t.Fatalf("standing memory attribution = %d, want %d (BackgroundMemory section)", got, want)
	}
	if got, want := comp.CatalogTokens, coldTextTokens(strings.Repeat("c", 160)+strings.Repeat("p", 240)); got != want {
		t.Fatalf("catalog attribution = %d, want %d (skills + capability catalog sections)", got, want)
	}
	if got, want := comp.RuntimeTokens, coldTextTokens(strings.Repeat("e", 200)+strings.Repeat("w", 100)); got < want {
		t.Fatalf("runtime attribution = %d, want >= %d (environment + workspace, frame adds more)", got, want)
	}
	if got, want := comp.RecallTokens, coldTextTokens(recall); got != want {
		t.Fatalf("recall attribution = %d, want %d (<memory-recall> tail)", got, want)
	}
	if got, want := comp.MessageTokens, coldTextTokens(history+"\n\n")+coldTextTokens(strings.Repeat("a", 80)); got != want {
		t.Fatalf("history attribution = %d, want %d (plain user text + assistant; recall stripped)", got, want)
	}
	// Σ全部分项 = TotalTokens（tl1224 拆分后口径不变的验收读数）。
	if got, want := comp.TotalTokens, comp.SystemPromptTokens+comp.BuiltinToolTokens+comp.SkillTokens+comp.McpToolTokens+comp.MessageTokens+comp.StandingMemoryTokens+comp.CatalogTokens+comp.RuntimeTokens+comp.RecallTokens+comp.ImageTokens; got != want {
		t.Fatalf("total = %d, want the sum of every segment including the tl1224 detail fields (%d)", got, want)
	}
	// 无信封无召回的会话不产生任何幽灵细分行（旧宿主/纯对话前端全零）。
	plain := computeContextComposition([]provider.Message{
		{Role: provider.RoleUser, Content: strings.Repeat("u", 100)},
	}, nil, coldTextTokens)
	if plain.StandingMemoryTokens != 0 || plain.CatalogTokens != 0 || plain.RuntimeTokens != 0 || plain.RecallTokens != 0 {
		t.Fatalf("plain conversation must leave every detail field at zero: %+v", plain)
	}
}
