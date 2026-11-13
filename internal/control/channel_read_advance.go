package control

import (
	"log/slog"
	"strings"

	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
)

// tl1563 已读推进（口径 20261112 msg_04cdadbd）：已读判定 =
// 「消息进入成员上下文 且 该成员回合运行中」。成员（AI 会话）消费群聊派活
// 走 inbox 直投（collabchannel drain → MailStore → sessioninbox → 开轮注入
// / drain_inbox），成员不需要也不调用 channel_read，所以 fanout.read_at 在
// 这条链上必须由消费侧代为推进。本 helper 是推进的唯一入口：best-effort，
// 失败只记日志、绝不影响消费主流程（与 task 309 SendReadReceipt 同契约）。
//
// 三个调用点（都是「进入上下文且回合运行中」同时成立的时刻）：
//  1. TrySubmitInboxItem 的 turnStarted 分支 —— 自动开轮把条目注入开工
//     上下文，回合开始运行（主链，验收 1）；被拒分支条目回退 queued，
//     消息未进入上下文，不推进（边界用例 2）。
//  2. drain_inbox 的 MailStore 层 settle 消费（agent 包）—— 成员在回合
//     内显式消费。
//  3. drain_inbox 的 sessioninbox 层 settle 消费（agent 包）—— 同上。
//
// steer 注入面（inbox_steer.go）不挂：群聊 fan-out 恒为 delivery=followup
// （429：空闲成员永不被 steer 唤醒），群聊件到不了那条路；个人直投消息不
// 在 fanout 表里，即使走到推进也按投递编号空匹配、零行移动，无副作用。
func advanceChannelReadOnConsume(member string, deliveryIDs []string) {
	member = strings.TrimSpace(member)
	if member == "" {
		return
	}
	ids := make([]string, 0, len(deliveryIDs))
	for _, id := range deliveryIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	mailDir := config.SessionCollabMailDir()
	store, err := collabchannel.Open(mailDir)
	if err != nil {
		slog.Warn("channel read advance: open store", "member", member, "err", err)
		return
	}
	defer store.Close()
	marked, err := store.MarkReadByDelivery(member, ids)
	if err != nil {
		slog.Warn("channel read advance: mark read", "member", member, "ids", ids, "err", err)
		return
	}
	if marked > 0 {
		slog.Info("channel read advanced on consume", "member", member, "marked", marked, "deliveries", len(ids))
	}
}
