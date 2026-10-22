package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// 任务 1024（草稿优化）：输入栏改写按钮的辅助模型通道。
// 任务 1052：上下文尾巴预算参数化（2/5/10/20KB 四档，用户拍板降成本）。
//
// 数据纪律：走 provider.StreamAuxiliary（与 websearch/摘要同一条有限重试
// 通道），不进主 turn 上下文、不产生 transcript 消息；只读调用方传入的
// 最近上下文尾巴（前端按档位裁剪，此处按同一档位二次把关），Agent 不自己
// 去翻 transcript。
// 输出纪律：调用方（桌面）把返回文本回填输入框、不自动发送；失败一律
// ("" , err)，由调用方静默降级为「不改写」。

// DraftRewriteBudgetBytes is the DEFAULT context-tail budget (任务 1024 的旧固定
// 10KB；任务 1052 之后仅作缺省档，档位由调用方按用户选择传入）。
const DraftRewriteBudgetBytes = 10 * 1024

// DraftRewriteMaxBudgetBytes is the largest tier the picker offers (任务 1052：
// 20KB)。调用方传入超过此值的预算一律按此值截断——档位是用户可见契约，桌面
// 端弹窗只提供 2/5/10/20 四档，Go 侧守住上界防误传/防放大注入。
const DraftRewriteMaxBudgetBytes = 20 * 1024

const draftRewriteSystemPrompt = `你是输入草稿改写助手。把用户交给你的草稿改写成结构化、无歧义的表达，硬性规则：
1. 保留用户的全部意图、约束与专有名词——不新增需求、不删减需求、不擅自扩大或缩小范围；
2. 多个诉求时分条列出，每条只表达一个动作或一个疑问；
3. 消除指代歧义：能把"它/那个"落到具体对象的就落到具体对象；用户没说清楚的信息不得替用户编造，保留为明确的疑问或待确认项；
4. 不改变语言（中文草稿仍用中文，英文仍用英文），不加客套话、不加解释；
5. 只输出改写后的草稿正文，不要任何前言、后记或说明。`

// DraftRewriteContextBudget turns a caller-chosen tier in KB (任务 1052 档位)
// into the byte budget actually applied: 0/negative falls back to the 1024
// default (DraftRewriteBudgetBytes), anything above the 20KB tier is clamped to
// DraftRewriteMaxBudgetBytes. Exported for the desktop binding to cite the same
// numbers without re-deriving them.
func DraftRewriteContextBudget(budgetKB int) int {
	if budgetKB <= 0 {
		return DraftRewriteBudgetBytes
	}
	b := budgetKB * 1024
	if b > DraftRewriteMaxBudgetBytes {
		return DraftRewriteMaxBudgetBytes
	}
	return b
}

// RewriteDraft runs one auxiliary rewrite request over the given draft plus a
// caller-trimmed context tail. budgetKB is the tier the user picked in the
// composer dialog (任务 1052: 2/5/10/20; 0 = default 10KB); the tail is trimmed
// to that budget (keeping the newest bytes). The returned string is the
// rewritten draft text; an error (or an empty rewrite) means "keep the
// original" — the caller must never surface the error to the user as a blocking
// dialog.
func (a *Agent) RewriteDraft(ctx context.Context, draft, contextTail string, budgetKB int) (string, error) {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return "", errors.New("draft is empty")
	}
	if a == nil || a.svc.prov == nil {
		return "", errors.New("rewrite unavailable: no provider")
	}
	if budget := DraftRewriteContextBudget(budgetKB); len(contextTail) > budget {
		// Trim at a rune boundary from the front: the tail is the most recent
		// context, so keep the END of the buffer.
		cut := contextTail[len(contextTail)-budget:]
		if i := strings.IndexRune(cut, '\n'); i >= 0 && i+1 < len(cut) {
			cut = cut[i+1:]
		}
		contextTail = cut
	}
	var b strings.Builder
	b.WriteString(draftRewriteSystemPrompt)
	b.WriteString("\n\n")
	if tail := strings.TrimSpace(contextTail); tail != "" {
		b.WriteString("最近对话上下文（仅供理解指代，不要复述）：\n")
		b.WriteString(tail)
		b.WriteString("\n\n")
	}
	b.WriteString("待改写草稿：\n")
	b.WriteString(draft)

	req := provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: b.String()},
		},
		MaxTokens: 4096,
	}
	ch, err := provider.StreamAuxiliary(provider.WithRecoverySleeper(ctx, recoverySleep), a.svc.prov, req)
	if err != nil {
		return "", fmt.Errorf("rewrite stream: %w", err)
	}
	var out strings.Builder
	for c := range ch {
		switch c.Type {
		case provider.ChunkText:
			out.WriteString(c.Text)
		case provider.ChunkError:
			if c.Err != nil {
				return "", fmt.Errorf("rewrite failed: %w", c.Err)
			}
		}
	}
	rewritten := strings.TrimSpace(out.String())
	// Guard against a model that answers with a refusal or an empty body:
	// an empty or obviously non-rewritten answer degrades to "no rewrite".
	if rewritten == "" {
		return "", errors.New("rewrite produced no text")
	}
	return rewritten, nil
}
