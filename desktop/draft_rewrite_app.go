package main

import (
	"context"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// 任务 1024（草稿优化）：输入栏改写按钮的桌面绑定面。
//
// 合同（前端 bridge.ts 同名方法）：
//   - 输入：当前 tab、用户草稿全文、前端裁好的最近上下文尾巴（≤10KB，
//     DraftRewriteBudgetBytes）；
//   - 输出：改写后的草稿文本；失败/超时/只读页/无会话一律返回 ""，前端
//     静默降级为「不改写」（立项约束：失败不弹错、不打断输入）；
//   - 通道：控制端口 RewriteDraft → Agent.RewriteDraft —— 辅助模型通道，
//     不进主 turn 上下文、不产生 transcript 消息；结果只回填输入框，绝不
//     自动发送（用户确认后才走正常 submit）。
const draftRewriteTimeout = 60 * time.Second

func (a *App) RewriteDraftForTab(tabID, draft, contextTail string) string {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return ""
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil || a.tabIsReadOnly(tab) {
		return ""
	}
	ctx, cancel := context.WithTimeout(a.ctx, draftRewriteTimeout)
	defer cancel()
	out, err := ctrl.RewriteDraft(ctx, draft, contextTail)
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	return out
}

// draftRewriteContextBudgetBytes pins the tail budget the front end trims to
// (任务 1024 验收：最近 10KB 上下文) so both sides cite one number.
func draftRewriteContextBudgetBytes() int { return agent.DraftRewriteBudgetBytes }
