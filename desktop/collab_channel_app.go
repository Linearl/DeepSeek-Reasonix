package main

// Task 803 — 群聊 channel 人类化后端（Phase A）的 Wails 面。
//
// 人类以固定身份 human:local 参与 349 channel：建群（自己=群主）、入群、
// 退群、发言、邀请/移除成员（群主权限）、读消息。所有写路径都走
// collabchannel.Store 的既有方法（发言 = Publish 同链路，铁律 8 不建第二
// 通道；风暴治理=小时上限对人类发言同样生效）；人类消息的 fan-out 行在
// drain 时跳过邮箱投递只落库（人类在 UI 读，不收 agent 唤醒邮件）。
//
// 绑定模式沿 collab_view_app.go（任务 409）：每次调用自包含，用完即关，
// 不持有跨调用的文件句柄。

import (
	"context"
	"time"

	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
)

// channelHumanSpawn kicks the background fan-out drain after a human publish.
// Package seam so tests capture the job instead of racing the production
// goroutine (same shape as agent.channelSpawn for the tool face).
var channelHumanSpawn = func(job func()) { go job() }

// channelHumanDrainTimeout bounds one background fan-out pass (same ceiling
// as the tool face's channelDrainTimeout).
const channelHumanDrainTimeout = 10 * time.Minute

// ChannelCreateForHuman builds a channel with the local human as its owner
// (任务 803 ③; 群主可为对话或人类 — here the human founds it). The human is
// always a founding member.
func (a *App) ChannelCreateForHuman(name, topic string, hourlyLimit int) (collabchannel.Channel, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.Channel{}, err
	}
	defer store.Close()
	return store.CreateChannelWithOwner(name, topic, hourlyLimit, collabchannel.MemberTypeHuman, collabchannel.HumanLocal)
}

// ChannelJoinHuman brings the local human into an existing channel.
func (a *App) ChannelJoinHuman(ref string) (collabchannel.Channel, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.Channel{}, err
	}
	defer store.Close()
	if err := store.JoinHuman(ref); err != nil {
		return collabchannel.Channel{}, err
	}
	return store.Channel(ref)
}

// ChannelLeaveHuman removes the local human from a channel. Refused when the
// human owns the channel (最后一个群主退出拒绝 — same rule as every owner).
func (a *App) ChannelLeaveHuman(ref string) error {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Leave(ref, collabchannel.HumanLocal)
}

// ChannelDissolveForHuman destroys the channel outright (tl1217) — owner-only
// via Store.Dissolve. This is the owner's missing exit: Leave refuses the
// owner, so before this port a human-founded group could neither grow (invite
// breakage, tl1216) nor die. The frontend gates it behind an explicit
// double-click confirmation; the port itself stays honest about permission.
func (a *App) ChannelDissolveForHuman(ref string) error {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Dissolve(ref, collabchannel.HumanLocal)
}

// ChannelSendFromHuman publishes one message as human:local through the SAME
// Publish path the agent tools use (风暴治理/小时上限对人类发言同样生效) and
// kicks the background drain.
func (a *App) ChannelSendFromHuman(ref, body string) (collabchannel.Message, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.Message{}, err
	}
	msg, err := store.Publish(ref, collabchannel.HumanLocal, body)
	_ = store.Close()
	if err != nil {
		return collabchannel.Message{}, err
	}
	mailDir := config.SessionCollabMailDir()
	channelHumanSpawn(func() {
		drain, err := collabchannel.Open(mailDir)
		if err != nil {
			return
		}
		defer drain.Close()
		ctx, cancel := context.WithTimeout(context.Background(), channelHumanDrainTimeout)
		defer cancel()
		_, _ = drain.DrainFanout(ctx)
	})
	return msg, nil
}

// ChannelInvite adds session members to a channel — owner-only (任务 803 ③
// 群主权限; the caller is the human, so the human must own the channel).
func (a *App) ChannelInvite(ref string, contactIDs []string) (collabchannel.Channel, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.Channel{}, err
	}
	defer store.Close()
	if err := store.Invite(ref, collabchannel.HumanLocal, contactIDs...); err != nil {
		return collabchannel.Channel{}, err
	}
	return store.Channel(ref)
}

// ChannelSetDeputy appoints the single deputy manager (任务 856 ⑤): only the
// HUMAN owner of a human-owned channel, and only a session member. The deputy
// gains the invite permission (same as the owner); removal stays owner-only.
func (a *App) ChannelSetDeputy(ref, contactID string) (collabchannel.Channel, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.Channel{}, err
	}
	defer store.Close()
	return store.SetDeputy(ref, contactID)
}

// ChannelUnsetDeputy clears the deputy appointment (idempotent).
func (a *App) ChannelUnsetDeputy(ref string) (collabchannel.Channel, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.Channel{}, err
	}
	defer store.Close()
	return store.UnsetDeputy(ref)
}

// ChannelRemoveMember kicks one member — owner-only, never the owner.
func (a *App) ChannelRemoveMember(ref, contactID string) (collabchannel.Channel, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.Channel{}, err
	}
	defer store.Close()
	if err := store.RemoveMember(ref, collabchannel.HumanLocal, contactID); err != nil {
		return collabchannel.Channel{}, err
	}
	return store.Channel(ref)
}

// ChannelMessagesForHuman reads a channel's history the way the human UI
// consumes it: recent lines oldest-first with per-recipient fan-out rows, and
// — 任务 803 ⑥ — every line the UI can show is marked read for human:local
// (UI 可见即已读; no mailbox round-trip exists to drive it).
// limit <= 0 uses 50; hard max 200 (a chat tail, same as ReadCollabChannel).
func (a *App) ChannelMessagesForHuman(ref string, sinceMs int64, limit int) (CollabChannelReadView, error) {
	out := CollabChannelReadView{Messages: []CollabChannelMessageView{}}
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return out, err
	}
	defer store.Close()

	channel, err := store.Channel(ref)
	if err != nil {
		return out, err
	}
	out.Channel = channel

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	messages, err := store.Messages(ref, sinceMs, limit)
	if err != nil {
		return out, err
	}

	// 803 ⑥: UI 可见即已读 — settle any still-queued human rows (visibility
	// replaces the skipped mail delivery) and mark them read. No-op for lines
	// without human fan-out rows (e.g. the human's own messages).
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		ids = append(ids, m.ID)
	}
	if len(ids) > 0 {
		_, _ = store.MarkHumanVisible(ref, ids)
	}

	fanout, err := store.FanoutStates(ref)
	if err != nil {
		return out, err
	}
	byMessage := make(map[string][]collabchannel.FanoutRow, len(messages))
	for _, row := range fanout {
		byMessage[row.MessageID] = append(byMessage[row.MessageID], row)
	}
	out.Messages = make([]CollabChannelMessageView, 0, len(messages))
	for _, m := range messages {
		// 802: 墓碑时间随行透出（撤回渲染与导出核对都需要）。
		view := CollabChannelMessageView{ID: m.ID, Sender: m.Sender, Body: m.Body, At: m.At, CancelledAt: m.CancelledAt}
		// FanoutStates orders by (message at, member) — keep that order so the
		// member annotation reads in the roster's own order.
		view.Fanout = byMessage[m.ID]
		out.Messages = append(out.Messages, view)
	}
	return out, nil
}
