package main

import (
	"context"
	"log/slog"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// 任务 1016 —— 意见箱并入信件中心的桌面接线（采集层双写 + 兜底扫描）。
//
// 双写：submit_feedback 工具写完 md（权威源）后调用 builtin 的通知钩子，由
// newFeedbackMailNotifier 构造的 agent.FeedbackMailConfig 投递一条引用 md
// 文件名的通知消息到固定名「意见箱」会话（会话首次使用时创建，contactId
// 经地址注册表持久化，转线只更新注册表）。通知失败绝不影响提交结果。
//
// 兜底：本进程周期性跑一遍 BackfillFeedbackNotifications——扫 feedback-inbox
// 下缺少 notified 标记的 md 并补投通知，然后把标记写回 md。这覆盖「消息
// 丢失 / 降级 / 开轮失败」的已知缺陷面（1016 可靠性兜底②），且天然幂等：
// 有标记的 md 永远不会被重复投递。
const feedbackBackfillInterval = 15 * time.Minute

// newFeedbackMailNotifier builds the agent-side notifier with the desktop's
// session factory (create_collab_session implementation) and the cached
// identity scan for the registry re-resolve path.
func newFeedbackMailNotifier(a *App) agent.FeedbackMailConfig {
	return agent.FeedbackMailConfig{
		SessionDir:    config.SessionDir(),
		WorkspaceRoot: globalWorkspaceRoot(),
		MailDir:       config.SessionCollabMailDir(),
		// Host-injected resolver: reuse the panel's 30s-cached identity scan
		// so each submission costs no directory rescan (task 600 pattern).
		ResolveSession: func(contactID string) (string, bool) {
			for _, id := range scanIdentityDirectoryCached() {
				if id.ContactID == contactID {
					return id.SessionPath, true
				}
			}
			return "", false
		},
		CreateSession: a.createCollabSession,
	}
}

// startFeedbackBackfillLoop arms the periodic 兜底扫描 (task 1016 可靠性兜底②):
// notes whose md lacks the notified marker get their notification delivered and
// the marker stamped. Runs in the background, never blocks startup.
func (a *App) startFeedbackBackfillLoop(ctx context.Context) {
	a.goSafe("feedbackBackfill", func() {
		ticker := time.NewTicker(feedbackBackfillInterval)
		defer ticker.Stop()
		mail := newFeedbackMailNotifier(a)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				res, err := mail.BackfillFeedbackNotifications(ctx)
				if err != nil {
					slog.Warn("feedback: backfill pass failed", "err", err)
				} else if res.Delivered > 0 || res.Errors > 0 {
					slog.Info("feedback: backfill pass", "delivered", res.Delivered, "errors", res.Errors)
				}
			}
		}
	})
}
