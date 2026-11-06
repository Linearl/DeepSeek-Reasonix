package main

import (
	"context"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// 任务 1024（草稿优化）：输入栏改写按钮的桌面绑定面。
// 任务 1052：档位参数化——contextBudgetKB 是用户在弹窗里选的上下文长度档
// （2/5/10/20KB；0 = 默认 10KB），Agent 侧按同一档位二次裁剪并守住 20KB 上界。
//
// 合同（前端 bridge.ts 同名方法）：
//   - 输入：当前 tab、用户草稿全文、前端裁好的最近上下文尾巴（≤所选档位，
//     Agent.DraftRewriteContextBudget 把关）、上下文档位 contextBudgetKB；
//   - 输出：改写后的草稿文本；失败/超时/只读页/无会话一律返回 ""，前端
//     静默降级为「不改写」（立项约束：失败不弹错、不打断输入）；
//   - 通道：控制端口 RewriteDraft → Agent.RewriteDraft —— 辅助模型通道，
//     不进主 turn 上下文、不产生 transcript 消息；结果只回填输入框，绝不
//     自动发送（用户确认后才走正常 submit）。
const draftRewriteTimeout = 60 * time.Second

func (a *App) RewriteDraftForTab(tabID, draft, contextTail string, contextBudgetKB int) string {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return ""
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if ctrl == nil || a.tabIsReadOnly(tab) {
		return ""
	}
	return rewriteDraftViaCtrl(a.ctx, ctrl, draft, contextTail, contextBudgetKB)
}

// RewriteDraftForChannel is the tl1441 ③ channel face of 任务 1024 draft
// rewrite. A channel tab owns no session controller, but the rewrite is a pure
// auxiliary-model call (contextTail is passed in explicitly; nothing from the
// owning session leaks into the prompt), so it borrows the first live session
// controller instead of adding a second aux-model plumbing route. Same contract
// as the tab face: failure/timeout/empty silently returns "" and the frontend
// degrades to "no rewrite".
func (a *App) RewriteDraftForChannel(draft, contextTail string, contextBudgetKB int) string {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return ""
	}
	a.mu.RLock()
	ctrl := a.borrowReadyRewriteCtrlLocked()
	a.mu.RUnlock()
	if ctrl == nil {
		return ""
	}
	return rewriteDraftViaCtrl(a.ctx, ctrl, draft, contextTail, contextBudgetKB)
}

// borrowReadyRewriteCtrlLocked picks the controller to host a channel rewrite:
// the active tab first (the one the user is most likely to consider "current"),
// then any other ready, writable tab. Callers must hold a.mu (read).
func (a *App) borrowReadyRewriteCtrlLocked() control.SessionAPI {
	if tab := a.tabs[a.activeTabID]; tab != nil && tab.Ctrl != nil && tab.Ready && !tab.ReadOnly {
		return tab.Ctrl
	}
	for _, id := range a.orderedTabIDsLocked() {
		if tab := a.tabs[id]; tab != nil && tab.Ctrl != nil && tab.Ready && !tab.ReadOnly {
			return tab.Ctrl
		}
	}
	return nil
}

func rewriteDraftViaCtrl(ctx context.Context, ctrl control.SessionAPI, draft, contextTail string, contextBudgetKB int) string {
	rewriteCtx, cancel := context.WithTimeout(ctx, draftRewriteTimeout)
	defer cancel()
	out, err := ctrl.RewriteDraft(rewriteCtx, draft, contextTail, contextBudgetKB)
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	return out
}

// draftRewriteContextBudgetBytes pins the DEFAULT tail budget (任务 1024 验收：
// 最近 10KB 上下文；任务 1052 之后档位由弹窗决定，此默认值仅在档位缺失时
// 兜底) so both sides cite one number.
func draftRewriteContextBudgetBytes() int { return agent.DraftRewriteBudgetBytes }
