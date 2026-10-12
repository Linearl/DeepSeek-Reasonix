package main

// 任务 802 — 人类撤回自己频道消息的 Wails 面（微信式右键撤回的后端口）。
//
// 803 的 7 个 API 不含撤回，而 802 验收（撤回+导出核对）需要人类经 UI 撤回
// 自己的消息。这里只做一薄层：复用 Store.Cancel 既有墓碑语义（仅原发送者
// 可撤、幂等、queued 行停投），请求者固定 human:local——与 803 其余人类
// API 同款（每次调用自包含，用完即关，不持跨调用句柄）。

import (
	"reasonix/internal/collabchannel"
	"reasonix/internal/config"
)

// ChannelCancelFromHuman tombstones one message the local human sent.
// Cancelling another member's message is refused by the store (ErrNotSender).
func (a *App) ChannelCancelFromHuman(ref, messageID string) (collabchannel.CancelResult, error) {
	store, err := collabchannel.Open(config.SessionCollabMailDir())
	if err != nil {
		return collabchannel.CancelResult{}, err
	}
	defer store.Close()
	return store.Cancel(ref, messageID, collabchannel.HumanLocal)
}
