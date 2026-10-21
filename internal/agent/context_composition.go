package agent

import (
	"encoding/json"
	"strings"

	"reasonix/internal/provider"
)

// Task 442 — context composition segments for the composer gauge popup.
//
// 任务1025（缺口5修复）：分段量表与 gauge 主读数（ContextUsedTokens）统一口径
// ——分段用与主读数相同的 normalized 请求视图（normalizeModelRequestMessages）
// 与相同的校准估算器（estimatedShapeTokens：文本走校准比/冷值，图片走分辨率
// 折算）。此前分段用静态 ~4字节/token（estimateTokens），主读数用校准比，
// 导致分段和（如 488.5K）与主读数（如 566.3K）永远对不上（缺口5）。现在
// TotalTokens = Σ分项，且与主读数同口径——前端可断言 Σshare 对应总数。
//
// Skills are attributed where they really live: a skill body enters the
// transcript as the tool result of run_skill / read_only_skill / read_skill,
// so those tool results form the skill segment. Everything that is not a
// system message, a tool schema, or a skill tool result — user prompts,
// assistant output, ordinary tool results — is the message segment.
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
	// ImageTokens is the resolution-derived price of every provider-visible
	// image in the live view (任务1040): the headline gauge (ContextUsedTokens)
	// bills images via requestCalibrationShape, so the segmented bar must too —
	// otherwise a screenshot session showed images in the headline and an
	// all-"消息" bar, and the two readouts could never be reconciled.
	ImageTokens int `json:"imageTokens"`
	// TotalTokens is the sum of the segments above — the composition's own
	// total, now the same caliber as the calibrated gauge number (任务1025).
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
	// 任务1025（缺口5修复）：分段用与 gauge 主读数（ContextUsedTokens →
	// estimatedVisibleRequestTokens）完全相同的 normalized 视图与校准估算器，
	// 所以 TotalTokens（Σ分项）与主读数同口径、可对账。textTokens 走
	// requestCalibrationShape 单串校准（冷值 ~4字节/token），图片走分辨率折算。
	//
	// The gauge headline counts framing bytes (4/message, 8/tool-call, 8/schema)
	// that content-only segment measurement omits; scale the content segments so
	// their sum equals the headline total exactly — Σ分项 = TotalTokens = 主读数.
	visible := a.normalizeModelRequestMessages(a.modelVisibleMessages())
	schemas := a.providerToolSchemas()
	textTokens := func(s string) int {
		if s == "" {
			return 0
		}
		return a.estimatedShapeTokens(requestCalibrationShape{requestChars: int64(len(s))})
	}
	comp := computeContextComposition(visible, schemas, textTokens)
	gauge := a.estimatedVisibleRequestTokens(visible)
	if gauge > 0 && comp.TotalTokens > 0 && gauge != comp.TotalTokens {
		scaleCompSegments(&comp, gauge)
	}
	return comp
}

// scaleCompSegments scales the content segments (not images, whose resolution
// price is already exact) so their sum equals the gauge headline total. The
// framing-byte remainder is absorbed proportionally by the largest segments,
// keeping Σsegments = TotalTokens = 主读数 by construction.
func scaleCompSegments(comp *ContextComposition, target int) {
	contentTotal := comp.SystemPromptTokens + comp.BuiltinToolTokens + comp.SkillTokens + comp.McpToolTokens + comp.MessageTokens
	if contentTotal <= 0 || comp.ImageTokens >= target {
		comp.TotalTokens = target
		return
	}
	scale := float64(target-comp.ImageTokens) / float64(contentTotal)
	comp.SystemPromptTokens = int(float64(comp.SystemPromptTokens) * scale)
	comp.BuiltinToolTokens = int(float64(comp.BuiltinToolTokens) * scale)
	comp.SkillTokens = int(float64(comp.SkillTokens) * scale)
	comp.McpToolTokens = int(float64(comp.McpToolTokens) * scale)
	comp.MessageTokens = int(float64(comp.MessageTokens) * scale)
	// Absorb float-truncation remainder into the largest content bucket so
	// Σsegments = TotalTokens exactly (缺口5: 分项和必须等于主读数).
	remainder := (target - comp.ImageTokens) - (comp.SystemPromptTokens + comp.BuiltinToolTokens + comp.SkillTokens + comp.McpToolTokens + comp.MessageTokens)
	comp.MessageTokens += remainder
	comp.TotalTokens = comp.SystemPromptTokens + comp.BuiltinToolTokens + comp.SkillTokens + comp.McpToolTokens + comp.MessageTokens + comp.ImageTokens
}

// computeContextComposition is the pure measurement core, split from the
// method so tests can pin the bucketing without building a live agent. The
// textTokens estimator is injected so the agent method can pass the session's
// calibrated ratio (estimatedShapeTokens) while tests pass a fixed cold
// estimator — one caliber for admission, the UI gauge and the composition bar.
func computeContextComposition(messages []provider.Message, schemas []provider.ToolSchema, textTokens func(string) int) ContextComposition {
	comp := ContextComposition{}
	for _, msg := range messages {
		if msg.LocalOnly {
			continue
		}
		// 任务1040: images price by resolution, never by content bytes — the
		// same caliber the headline gauge (requestCalibrationShape →
		// imageEstimateTokensOf) speaks. Without this bucket a screenshot-only
		// message contributed 0 bytes to the bar while the headline billed
		// ~2K tokens per image.
		for _, img := range msg.Images {
			comp.ImageTokens += imageEstimateTokens(img)
		}
		switch {
		case msg.Role == provider.RoleSystem:
			comp.SystemPromptTokens += textTokens(msg.Content) + textTokens(msg.ReasoningContent)
		case msg.Role == provider.RoleTool && skillToolNames[msg.Name]:
			comp.SkillTokens += textTokens(msg.Content)
		default:
			comp.MessageTokens += textTokens(msg.Content) + textTokens(msg.ReasoningContent)
			for _, call := range msg.ToolCalls {
				comp.MessageTokens += textTokens(call.Arguments)
			}
		}
	}
	for _, schema := range schemas {
		b, err := json.Marshal(schema)
		if err != nil {
			continue
		}
		if strings.HasPrefix(schema.Name, "mcp__") {
			comp.McpToolTokens += textTokens(string(b))
		} else {
			comp.BuiltinToolTokens += textTokens(string(b))
		}
	}
	comp.TotalTokens = comp.SystemPromptTokens + comp.BuiltinToolTokens + comp.SkillTokens + comp.McpToolTokens + comp.MessageTokens + comp.ImageTokens
	return comp
}
