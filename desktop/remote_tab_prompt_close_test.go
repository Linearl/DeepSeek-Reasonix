package main

import (
	"encoding/json"
	"testing"
)

// 任务536：prompt_closed 必须同步清掉 remote tab 的挂起面板缓存——远端会话
// 重连时按 pendingEvents 重放面板，缓存不清就会复活动已死的 ask/approval 卡，
// 用户提交只会得到 "prompt is not pending"（536 现场的 remote 形态）。
func TestExpireRemotePendingPromptEventClearsCachedPanel(t *testing.T) {
	a := &App{remoteTabs: map[string]*remoteTab{}}
	meta := TabMeta{ID: "tab-536"}
	a.remoteTabMu.Lock()
	a.remoteTabs[meta.ID] = &remoteTab{
		runtime: remoteTabRuntimeState{pendingPrompt: true, cancellable: true},
		pendingEvents: map[string]json.RawMessage{
			"ask_request:3":      json.RawMessage(`{"kind":"ask_request","ask":{"id":"3"}}`),
			"approval_request:5": json.RawMessage(`{"kind":"approval_request","approval":{"id":"5"}}`),
		},
	}
	a.remoteTabMu.Unlock()

	// ask 关闭信号（promptId 形态）：只清 ask 缓存，approval 缓存保留。
	a.expireRemotePendingPromptEvent(meta.ID, 1, json.RawMessage(`{"kind":"prompt_closed","promptKind":"ask","promptId":"3"}`))
	a.remoteTabMu.Lock()
	tab := a.remoteTabs[meta.ID]
	_, askLeft := tab.pendingEvents["ask_request:3"]
	_, approvalLeft := tab.pendingEvents["approval_request:5"]
	prompt := tab.runtime.pendingPrompt
	a.remoteTabMu.Unlock()
	if askLeft {
		t.Fatal("prompt_closed(ask) 后 ask_request 缓存仍在，重连会复活死面板")
	}
	if !approvalLeft {
		t.Fatal("prompt_closed(ask) 不得误伤 approval 缓存")
	}
	if !prompt {
		t.Fatal("approval 缓存仍在时 pendingPrompt 不得翻转")
	}

	// approval 关闭信号（itemId 形态 + promptKind=approval）：清掉最后一个，
	// pendingPrompt 随之归零。
	a.expireRemotePendingPromptEvent(meta.ID, 1, json.RawMessage(`{"kind":"prompt_closed","promptKind":"approval","itemId":"5"}`))
	a.remoteTabMu.Lock()
	pending := len(a.remoteTabs[meta.ID].pendingEvents)
	prompt = a.remoteTabs[meta.ID].runtime.pendingPrompt
	a.remoteTabMu.Unlock()
	if pending != 0 {
		t.Fatalf("缓存剩 %d 条，want 0", pending)
	}
	if prompt {
		t.Fatal("缓存清空后 pendingPrompt 必须归零")
	}

	// 缺 id/缺 kind 的坏帧是 fail-safe 无操作（保持 536 前行为）。
	a.expireRemotePendingPromptEvent(meta.ID, 1, json.RawMessage(`{"kind":"prompt_closed"}`))
	a.remoteTabMu.Lock()
	pending = len(a.remoteTabs[meta.ID].pendingEvents)
	a.remoteTabMu.Unlock()
	if pending != 0 {
		t.Fatalf("坏帧不得改缓存状态：剩 %d 条", pending)
	}
}
