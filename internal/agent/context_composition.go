package agent

import (
	"encoding/json"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
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
//
// tl1224 阶段二（协议三段式明细）：四个细分字段把 MessageTokens 里「看不见的
// 大头」拆出来单列。核实结论（阶段二实施第一步）：常驻记忆索引、技能/能力
// 目录、环境/工作区快照都随 <session-context> 信封走 host 生成的 user 消息
// （缓存安全设计，不进 system prompt）；每轮召回走最新 user 消息尾部的
// <memory-recall> 块——此前全部埋在 MessageTokens（94.8% 的「消息」行）里，
// 面板无单列。实现上四字段从 MessageTokens 拆出（兄弟分项，非嵌套子集），
// TotalTokens = Σ全部分项，口径不变。
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
	// MessageTokens is the conversation history: user/assistant content
	// (reasoning bodies and tool-call arguments included) plus ordinary tool
	// results — everything in the messages that the four detail fields below
	// do not attribute.
	MessageTokens int `json:"messageTokens"`
	// ImageTokens is the resolution-derived price of every provider-visible
	// image in the live view (任务1040): the headline gauge (ContextUsedTokens)
	// bills images via requestCalibrationShape, so the segmented bar must too —
	// otherwise a screenshot session showed images in the headline and an
	// all-"消息" bar, and the two readouts could never be reconciled.
	ImageTokens int `json:"imageTokens"`
	// TotalTokens is the sum of every segment above including the four tl1224
	// detail fields (they are carved OUT of MessageTokens, not nested inside
	// it) — the composition's own total, the same caliber as the calibrated
	// gauge number (任务1025).
	TotalTokens int `json:"totalTokens"`
	// StandingMemoryTokens is the 跨会话记忆 attribution: the background
	// memory block (pinned guidance + the standing index) carried by
	// <session-context> envelopes in the visible view (envelopes ride
	// host-generated user messages, not the system prompt); zero on sessions
	// without standing memory. The gauge's detail view lists it under the
	// System prompt section (用户定稿：跨会话记忆归属 System prompt 段) so the
	// governance bulk stays visible.
	StandingMemoryTokens int `json:"standingMemoryTokens,omitempty"`
	// CatalogTokens is the skills catalog + capability directory carried by
	// <session-context> envelopes (the tl1224 全量注入 block).
	CatalogTokens int `json:"catalogTokens,omitempty"`
	// RuntimeTokens is the environment/workspace runtime snapshot plus the
	// envelope frame (tag, preamble, digest) of every <session-context> in the
	// view.
	RuntimeTokens int `json:"runtimeTokens,omitempty"`
	// RecallTokens is the per-turn <memory-recall> tail attached to real user
	// messages (cache-safe design: recall never rewrites the prefix).
	RecallTokens int `json:"recallTokens,omitempty"`
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
// keeping Σsegments = TotalTokens = 主读数 by construction. The tl1224 detail
// fields scale by the same factor so every attribution keeps its share; the
// truncation remainder still lands in MessageTokens.
func scaleCompSegments(comp *ContextComposition, target int) {
	contentTotal := comp.SystemPromptTokens + comp.BuiltinToolTokens + comp.SkillTokens + comp.McpToolTokens + comp.MessageTokens +
		comp.StandingMemoryTokens + comp.CatalogTokens + comp.RuntimeTokens + comp.RecallTokens
	if contentTotal <= 0 || comp.ImageTokens >= target {
		comp.TotalTokens = target
		return
	}
	scale := float64(target-comp.ImageTokens) / float64(contentTotal)
	scaleInt := func(v int) int { return int(float64(v) * scale) }
	comp.SystemPromptTokens = scaleInt(comp.SystemPromptTokens)
	comp.BuiltinToolTokens = scaleInt(comp.BuiltinToolTokens)
	comp.SkillTokens = scaleInt(comp.SkillTokens)
	comp.McpToolTokens = scaleInt(comp.McpToolTokens)
	comp.MessageTokens = scaleInt(comp.MessageTokens)
	comp.StandingMemoryTokens = scaleInt(comp.StandingMemoryTokens)
	comp.CatalogTokens = scaleInt(comp.CatalogTokens)
	comp.RuntimeTokens = scaleInt(comp.RuntimeTokens)
	comp.RecallTokens = scaleInt(comp.RecallTokens)
	// Absorb float-truncation remainder into the largest content bucket so
	// Σsegments = TotalTokens exactly (缺口5: 分项和必须等于主读数).
	remainder := (target - comp.ImageTokens) - comp.contentSum()
	comp.MessageTokens += remainder
	comp.TotalTokens = comp.contentSum() + comp.ImageTokens
}

// contentSum is the mutable part of the Σsegments identity: every segment
// except the exact-resolution ImageTokens.
func (c *ContextComposition) contentSum() int {
	return c.SystemPromptTokens + c.BuiltinToolTokens + c.SkillTokens + c.McpToolTokens + c.MessageTokens +
		c.StandingMemoryTokens + c.CatalogTokens + c.RuntimeTokens + c.RecallTokens
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
			content := msg.Content
			// tl1224 阶段二: the per-turn recall tail rides the real user
			// message; pull the <memory-recall> spans out first so 历史消息 and
			// 每轮召回 get honest numbers.
			if spans := memoryRecallSpans(content); len(spans) > 0 {
				for _, span := range spans {
					comp.RecallTokens += textTokens(span)
				}
				content = stripMemoryRecallSpans(content)
			}
			// tl1224 阶段二: <session-context> envelopes (host-generated user
			// messages) split into 跨会话记忆 / 目录 / 运行时快照; the envelope
			// frame (tag, preamble, digest lines) rides with the runtime
			// bucket. History keeps only plain conversation content.
			if snapshot, ok := sessioncontext.Parse(content); ok {
				sections := snapshot.Sections
				comp.StandingMemoryTokens += textTokens(sections.BackgroundMemory)
				comp.CatalogTokens += textTokens(sections.SkillsCatalog) + textTokens(sections.CapabilityCatalog)
				frame := len(content) - len(sections.Environment) - len(sections.Workspace) - len(sections.SkillsCatalog) - len(sections.CapabilityCatalog) - len(sections.BackgroundMemory)
				if frame < 0 {
					frame = 0
				}
				comp.RuntimeTokens += textTokens(sections.Environment) + textTokens(sections.Workspace) + textTokens(strings.Repeat(" ", frame))
			} else {
				comp.MessageTokens += textTokens(content) + textTokens(msg.ReasoningContent)
				for _, call := range msg.ToolCalls {
					comp.MessageTokens += textTokens(call.Arguments)
				}
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
	comp.TotalTokens = comp.contentSum() + comp.ImageTokens
	return comp
}

// memoryRecallSpans returns every complete <memory-recall>…</memory-recall>
// block in content (tl1224 阶段二). The wrapper is the stable contract written
// by memory.buildRecallBlock; anything malformed stays in the history bucket.
func memoryRecallSpans(content string) []string {
	const open = "<memory-recall>"
	const close = "</memory-recall>"
	var spans []string
	for {
		start := strings.Index(content, open)
		if start < 0 {
			return spans
		}
		end := strings.Index(content[start:], close)
		if end < 0 {
			return spans
		}
		stop := start + end + len(close)
		spans = append(spans, content[start:stop])
		content = content[stop:]
	}
}

// stripMemoryRecallSpans removes complete recall blocks; the joining newlines
// the tail format leaves behind stay in the history bucket (a few bytes).
func stripMemoryRecallSpans(content string) string {
	const open = "<memory-recall>"
	const close = "</memory-recall>"
	for {
		start := strings.Index(content, open)
		if start < 0 {
			return content
		}
		end := strings.Index(content[start:], close)
		if end < 0 {
			return content
		}
		stop := start + end + len(close)
		content = content[:start] + content[stop:]
	}
}
