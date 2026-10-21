package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// 任务 1024（草稿优化）：输入栏改写按钮的辅助模型通道。
//
// 数据纪律：走 provider.StreamAuxiliary（与 websearch/摘要同一条有限重试
// 通道），不进主 turn 上下文、不产生 transcript 消息；只读调用方传入的
// 最近上下文尾巴（前端负责裁剪到 10KB），Agent 不自己去翻 transcript。
// 输出纪律：调用方（桌面）把返回文本回填输入框、不自动发送；失败一律
// ("" , err)，由调用方静默降级为「不改写」。

// DraftRewriteBudgetBytes caps the context tail the caller may attach. The
// front end already trims to 10KB; this constant names the contract so both
// sides cite one number.
const DraftRewriteBudgetBytes = 10 * 1024

const draftRewriteSystemPrompt = `你是输入草稿改写助手。把用户交给你的草稿改写成结构化、无歧义的表达，硬性规则：
1. 保留用户的全部意图、约束与专有名词——不新增需求、不删减需求、不擅自扩大或缩小范围；
2. 多个诉求时分条列出，每条只表达一个动作或一个疑问；
3. 消除指代歧义：能把"它/那个"落到具体对象的就落到具体对象；用户没说清楚的信息不得替用户编造，保留为明确的疑问或待确认项；
4. 不改变语言（中文草稿仍用中文，英文仍用英文），不加客套话、不加解释；
5. 只输出改写后的草稿正文，不要任何前言、后记或说明。`

// RewriteDraft runs one auxiliary rewrite request over the given draft plus a
// caller-trimmed context tail. The returned string is the rewritten draft
// text; an error (or an empty rewrite) means "keep the original" — the caller
// must never surface the error to the user as a blocking dialog.
func (a *Agent) RewriteDraft(ctx context.Context, draft, contextTail string) (string, error) {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return "", errors.New("draft is empty")
	}
	if a == nil || a.svc.prov == nil {
		return "", errors.New("rewrite unavailable: no provider")
	}
	if len(contextTail) > DraftRewriteBudgetBytes {
		// Trim at a rune boundary from the front: the tail is the most recent
		// context, so keep the END of the buffer.
		cut := contextTail[len(contextTail)-DraftRewriteBudgetBytes:]
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
