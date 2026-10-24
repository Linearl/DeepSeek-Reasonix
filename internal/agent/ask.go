package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// AskTool lets the model put a structured multiple-choice question (or a few) to
// the user mid-task and get the answer back — for genuine forks the model can't
// resolve from the request or the code (which library, which approach, …) rather
// than guessing or asking in prose. The frontend renders selectable options, the
// user picks, and the choices come back as the tool result. It reaches the user
// through the Asker carried on the call
// context (CallContext); with no asker (headless runs) it returns an explicit
// model-assumption fallback so an autonomous run never blocks or pretends a user
// answered.
//
// 任务 1084：AskTool 携带 ask 轮次上限配置。roundLimitEnabled 开启后，本 turn
// 内 ask 被调用的次数受 roundLimit 约束；高风险确认类 ask（header 含高风险
// 类别标记）不计入上限；信息补充类 ask 受上限约束，耗尽后返回耗尽提示（含
// 类别与待决选项），不静默空转、不按默认做高风险动作、不只说「无法继续」。
type AskTool struct {
	// roundLimitEnabled = cfg.Desktop.ExperimentalAskRoundLimit（boot 注入）。
	roundLimitEnabled bool
	// roundLimit = cfg.Desktop.AskRoundLimit（0=无限制；有效 0 或 2..100）。
	roundLimit int
	// authzPolicy 是高风险五类两级授权闸门（任务 1084）。为 nil 时不做授权
	// 查询（向后兼容，ask 行为不变）。
	authzPolicy HighRiskAuthzPolicy
}

// WithAskRoundLimit 注入 ask 轮次上限配置（任务 1084）。返回接收者自身以
// 支持链式调用。
func (t *AskTool) WithAskRoundLimit(enabled bool, limit int) *AskTool {
	t.roundLimitEnabled = enabled
	t.roundLimit = limit
	return t
}

// WithHighRiskAuthz 注入高风险五类两级授权闸门（任务 1084）。返回接收者
// 自身以支持链式调用。policy 为 nil 时不做授权查询（ask 行为不变）。
func (t *AskTool) WithHighRiskAuthz(policy HighRiskAuthzPolicy) *AskTool {
	t.authzPolicy = policy
	return t
}

// highRiskAskHeaderMarker 是高风险确认类 ask 的 header 前缀约定：模型在为
// 高风险五类动作发起确认 ask 时，header 需以该前缀开头（如
// "高风险确认-数据迁移与删除"）。前缀命中的 ask 不计入轮次上限（必须问的
// 不该被次数拦）。⚠️诚实声明：前缀判定是辅助初筛，模型是否正确加前缀由模型
// 自律——本机制靠模型理解规则判断，非字符串硬匹配；前缀不加 = 该次 ask 按
// 信息补充类计数，不会漏放行高风险动作（高风险动作本身受五类判定+两级授权
// 闸门约束，与 ask 计数独立）。
const highRiskAskHeaderMarker = "高风险确认"

// IsHighRiskConfirmAsk 判断本组问题是否为高风险确认类 ask（不计入轮次上限）。
// 判定：任一 question 的 header 以 highRiskAskHeaderMarker 开头。
func IsHighRiskConfirmAsk(qs []event.AskQuestion) bool {
	for _, q := range qs {
		if strings.HasPrefix(strings.TrimSpace(q.Header), highRiskAskHeaderMarker) {
			return true
		}
	}
	return false
}

// classifyHighRiskFromAskHeaders 从高风险确认类 ask 的 header/prompt 判定五类
// 类别。header 约定「高风险确认-<类别>」，prompt 为辅助初筛。
// ⚠️诚实声明：判定靠模型理解规则（模型自己加前缀 + prompt 语义），不是字符串
// 硬匹配。这里只做最保守的初筛；后两类（新技术路线/付费额度）必然漏判误判。
func classifyHighRiskFromAskHeaders(qs []event.AskQuestion) HighRiskClass {
	for _, q := range qs {
		hdr := strings.TrimSpace(q.Header)
		if !strings.HasPrefix(hdr, highRiskAskHeaderMarker) {
			continue
		}
		// 前缀后的类别名（「高风险确认-数据迁移与删除」→「数据迁移与删除」）。
		rest := strings.TrimSpace(strings.TrimPrefix(hdr, highRiskAskHeaderMarker))
		rest = strings.TrimPrefix(rest, "-")
		rest = strings.TrimPrefix(rest, "：")
		rest = strings.TrimPrefix(rest, ":")
		rest = strings.TrimSpace(rest)
		switch rest {
		case string(HighRiskOutboundSend):
			return HighRiskOutboundSend
		case string(HighRiskDataMigrationDelete):
			return HighRiskDataMigrationDelete
		case string(HighRiskDestructiveChange):
			return HighRiskDestructiveChange
		case string(HighRiskNewTechRoute):
			return HighRiskNewTechRoute
		case string(HighRiskPaidQuotaAlloc):
			return HighRiskPaidQuotaAlloc
		}
		// 前缀有但类别名没写全：退化为对 prompt 做初筛。
		if class := classifyAskQuestionForHighRisk(q.Prompt, nil); class != HighRiskNone {
			return class
		}
	}
	// 没有明确类别：对 prompt 做初筛（覆盖模型只写前缀不写类别的情况）。
	for _, q := range qs {
		if class := classifyAskQuestionForHighRisk(q.Prompt, nil); class != HighRiskNone {
			return class
		}
	}
	return HighRiskNone
}

func NewAskTool() *AskTool { return &AskTool{} }

func (*AskTool) Name() string { return tool.HostAsk }

func (*AskTool) Description() string {
	return "Ask the user one or more multiple-choice questions when you hit a decision that is genuinely theirs to make — one you can't resolve from the request, the code, or sensible defaults. The frontend shows the options for the user to pick; their choices are returned to you. Prefer this over asking in prose for any real fork (which approach, which library, scope). Don't use it for decisions with an obvious default — pick the sensible option and proceed. Tool-approval modes such as YOLO do not answer these questions for the user. Each question has a short `header` (a tab label), the `question` text, 2-4 `options` (each a `label` and optional `description`; put any recommended option first), and `multiSelect` when more than one may apply. A question that matches an already-answered decision is answered from that decision instead of re-asking; if the user explicitly asks you to re-ask a question they already answered, pass `allow_replay=true` so the panel shows again — never set it for retries or repeated scheduling."
}

func (*AskTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "questions":{
    "type":"array",
    "minItems":1,
    "maxItems":3,
    "description":"1-3 related questions to ask together. Same ambiguity is asked only once.",
    "items":{
      "type":"object",
      "properties":{
        "header":{"type":"string","description":"Very short label for the question (a tab title), e.g. \"Library\"."},
        "question":{"type":"string","description":"The full question to ask."},
        "options":{
          "type":"array","minItems":2,"maxItems":4,
          "description":"The choices. Put any recommended option first.",
          "items":{
            "type":"object",
            "properties":{
              "label":{"type":"string","description":"The choice text (concise)."},
              "description":{"type":"string","description":"Optional one-line explanation of the choice."}
            },
            "required":["label"]
          }
        },
        "multiSelect":{"type":"boolean","description":"Allow selecting more than one option."}
      },
      "required":["question","header","options"]
    }
  },
  "decision_id":{"type":"string","description":"Required when reopening a previously accepted decision; cite the original decision_id."},
  "new_evidence":{"type":"string","description":"Required with decision_id when asking again after the user already accepted a consequence."},
  "allow_replay":{"type":"boolean","description":"Set true ONLY when the user explicitly asked to re-ask a question that was already answered (they want to choose again): the host then shows the panel again instead of reusing the accepted decision. Never set it for retries or repeated scheduling — duplicate asks stay deduplicated."}
},
"required":["questions"]
}`)
}

// ReadOnly is true: asking has no host side effects, so it never needs approval
// and stays available in plan mode (clarifying scope while planning is fine).
func (*AskTool) ReadOnly() bool { return true }

func (t *AskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	p, err := parseAskArgs(args)
	if err != nil {
		return "", err
	}

	qs := make([]event.AskQuestion, 0, len(p.Questions))
	for i, q := range p.Questions {
		question := strings.TrimSpace(q.Question)
		if question == "" || len(q.Options) < 2 {
			return "", fmt.Errorf("question %d: a question and at least two options are required", i+1)
		}
		opts := make([]event.AskOption, len(q.Options))
		seenLabels := make(map[string]int, len(q.Options))
		for j, o := range q.Options {
			label := strings.TrimSpace(o.Label)
			if label == "" {
				return "", fmt.Errorf("question %d option %d: label is required", i+1, j+1)
			}
			if prev, ok := seenLabels[label]; ok {
				return "", fmt.Errorf("question %d option %d: duplicate label %q also used by option %d", i+1, j+1, label, prev+1)
			}
			seenLabels[label] = j
			opts[j] = event.AskOption{Label: label, Description: strings.TrimSpace(o.Description)}
		}
		qs = append(qs, event.AskQuestion{
			ID:      fmt.Sprintf("q%d", i+1),
			Header:  strings.TrimSpace(q.Header),
			Prompt:  question,
			Options: opts,
			Multi:   q.MultiSelect,
		})
	}

	id := strings.TrimSpace(p.DecisionID)
	explicitID := id != ""
	if id == "" {
		id = decisionIDForQuestions(qs)
	}

	// ── 任务 1084：高风险五类两级授权闸门（步骤②③）──────────────────
	// 仅对「高风险确认类 ask」（header 含高风险确认前缀）生效：先判类别，
	// 再查两级授权（实验室勾选 → 决策模式条目）。命中 ⇒ 不弹面板，直接返回
	// 「已授权，可执行」+ 依据（便于核查是否被滥用）。没命中 ⇒ 正常弹面板
	// （走 ④ ask）。非高风险 ask（信息补充类）不受此闸门影响，只受轮次上限。
	if t.authzPolicy != nil && IsHighRiskConfirmAsk(qs) {
		class := classifyHighRiskFromAskHeaders(qs)
		if class != HighRiskNone {
			result := CheckHighRiskAuthz(class, t.authzPolicy)
			if result.Authorized {
				return fmt.Sprintf("这一步属于「%s」，你已授权，可直接执行，无需我再询问。\n（%s）", class, result.Basis), nil
			}
			// 未授权：正常弹面板（④ ask）。reason 仅用于日志/复盘，不直接
			// 返回给模型（面板本身已隐含「需要你点头」）。
			_ = result.NotAuthorizedReason
		}
	}

	// ── 任务 1084：ask 轮次上限闸门 ──────────────────────────────────
	// 口径：单 turn 内 ask 工具被调用次数（questions 数组问几个不计）。
	// 高风险确认类 ask（header 含高风险确认前缀）不计入上限（必须问的
	// 不该被次数拦）；信息补充类 ask 按上限计数。
	// 耗尽后行为（唯一情形=高风险+两级都未授权+轮次耗尽）：不执行，输出
	// 「这一步属于<类别>，需要你点头：A 还是 B」把选择留给用户——不静默
	// 空转、不按默认做高风险动作、不只说「无法继续」。
	if t.roundLimitEnabled && t.roundLimit > 0 {
		if !IsHighRiskConfirmAsk(qs) {
			counter := AskRoundCounterFromContext(ctx)
			if counter != nil {
				if *counter >= t.roundLimit {
					return buildAskRoundExhaustedMessage(qs), nil
				}
				*counter++
			}
		}
	}

	// 任务926（显式重发放行）: allow_replay is the caller's declaration that the
	// USER asked to re-ask an already-answered question ("重新提一次"). With it,
	// both reuse arms are skipped and the panel really pops; without it the
	// dedup contract is exactly the old one — repeated scheduling with no user
	// intent stays deduplicated (the ask-storm protection). The unknown-id
	// validation below stays in force either way: allow_replay licenses a
	// re-ask, not a fabricated decision_id.
	if dec, ok := existingDecision(ctx, id); ok {
		if !p.AllowReplay && strings.TrimSpace(p.Evidence) == "" {
			return fmt.Sprintf("Host reused accepted decision %s. The user already chose: %s. Continue with that decision unless you supply decision_id and new_evidence, or set allow_replay=true because the user explicitly asked to re-ask it.", dec.ID, dec.Answer), nil
		}
	} else if !explicitID {
		if dec, matched := matchingExistingDecision(ctx, qs); matched && !p.AllowReplay {
			return fmt.Sprintf("Host reused accepted decision %s for the same ambiguity. The user already chose: %s. Continue with that decision; to reopen it, cite decision_id %s with new_evidence, or set allow_replay=true when the user explicitly asked to re-ask it.", dec.ID, dec.Answer, dec.ID), nil
		}
	} else if explicitID {
		if _, hasAcceptedDecision := firstExistingDecision(ctx); hasAcceptedDecision {
			return "", fmt.Errorf("unknown decision_id %q; cite the original accepted decision_id and include new_evidence to reopen it", id)
		}
	}

	_, _, asker, ok := CallContext(ctx)
	if !ok || asker == nil {
		// Headless / no interactive user: don't block an autonomous run, but make
		// the provenance explicit so the model doesn't treat this as a user choice.
		return "No interactive user answered. This is a model-assumption fallback, not a user answer. Proceed with your best judgment, state the assumption you made, and prefer the safest reversible option when choices differ in risk.", nil
	}

	answers, err := asker.Ask(ctx, qs)
	if err != nil {
		return "", fmt.Errorf("ask: %w", err)
	}
	summary := formatAnswers(qs, answers)
	rememberDecisionForQuestions(ctx, id, qs[0].Prompt, summary, qs)
	return summary + "\n\ndecision_id: " + id, nil
}

// formatAnswers renders the user's selections as a compact, model-facing summary,
// keyed by question header so the model can tell which answer is which. When the
// user picked nothing at all (the "just chat" / dismiss path), it returns an
// explicit stop signal instead of a per-question "(no answer)" — otherwise the
// model reads the empty result as license to proceed and acts unasked.
func formatAnswers(qs []event.AskQuestion, answers []event.AskAnswer) string {
	pick := make(map[string][]string, len(answers))
	for _, a := range answers {
		pick[a.QuestionID] = a.Selected
	}
	answered := 0
	for _, q := range qs {
		if len(pick[q.ID]) > 0 {
			answered++
		}
	}
	if answered == 0 {
		return "The user dismissed the question without choosing — read this as \"don't decide for me, let's just talk.\" Do not pick an option, run a tool, or take any further action toward this; stop and wait for the user's next message."
	}
	var b strings.Builder
	b.WriteString("The user answered:\n")
	for _, q := range qs {
		sel := pick[q.ID]
		label := q.Header
		if label == "" {
			label = q.Prompt
		}
		if len(sel) == 0 {
			fmt.Fprintf(&b, "- %s: (left unanswered — don't assume a choice)\n", label)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", label, strings.Join(sel, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildAskRoundExhaustedMessage 构造轮次耗尽后的提示（任务 1084）。
// 明确排除坏行为：❌耗尽静默停止空转 ❌耗尽按默认做高风险动作 ❌只说「无法
// 继续」不说类别与要什么决定。此函数只在「信息补充类 ask」轮次耗尽时被调用
// （高风险确认类 ask 不计数、不进此分支）——所以这里不需要处理高风险五类
// 的类别名（那走的是 AskTool 之外的高风险授权闸门）。文案把选择留给用户。
func buildAskRoundExhaustedMessage(qs []event.AskQuestion) string {
	var b strings.Builder
	b.WriteString("本轮 ask 轮次已达上限，无法继续弹出选择面板。这一步需要你点头才能继续：\n\n")
	for i, q := range qs {
		fmt.Fprintf(&b, "问题 %d：%s\n", i+1, strings.TrimSpace(q.Prompt))
		for j, o := range q.Options {
			fmt.Fprintf(&b, "  %s. %s", string(rune('A'+j)), strings.TrimSpace(o.Label))
			if desc := strings.TrimSpace(o.Description); desc != "" {
				fmt.Fprintf(&b, "（%s）", desc)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("请直接在对话里回复选项字母（如「A」），我会据此继续；不要让我替你默认选择。")
	return b.String()
}
