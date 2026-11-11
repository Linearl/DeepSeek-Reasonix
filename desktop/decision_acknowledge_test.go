package main

import (
	"strings"
	"testing"

	"reasonix/internal/control"
)

// tl1541 桥钉：AcknowledgePendingPromptsForTab 的 desktop 侧行为契约。
type acknowledgeFakeSession struct {
	control.SessionAPI
	status     control.RuntimeStatus
	prompts    []control.PromptIdentity
	abandoned  int
	cancelled  int
	lastReason string
}

func (s *acknowledgeFakeSession) RuntimeStatus() control.RuntimeStatus { return s.status }

func (s *acknowledgeFakeSession) PendingPromptIdentities() []control.PromptIdentity {
	return s.prompts
}

func (s *acknowledgeFakeSession) AbandonPendingPrompts(reason string) int {
	s.abandoned = len(s.prompts)
	s.lastReason = reason
	s.prompts = nil
	return s.abandoned
}

func (s *acknowledgeFakeSession) Cancel() { s.cancelled++ }

// legacySession 模拟不带放弃能力的运行时（老构建/别的 SessionAPI 实现）：
// 刻意不实现 AbandonPendingPrompts，桥的接口断言必须走 ok=false 分支。
type legacySession struct {
	control.SessionAPI
	status control.RuntimeStatus
}

func (s *legacySession) RuntimeStatus() control.RuntimeStatus { return s.status }

func newAcknowledgeTestApp(t *testing.T, ctrl control.SessionAPI) *App {
	t.Helper()
	isolateDesktopUserDirs(t)
	tab := &WorkspaceTab{
		ID: "tab-ack", Scope: "global", TopicID: "topic_ack", TopicTitle: "ack",
		Ready: true, disabledMCP: map[string]ServerView{}, Ctrl: ctrl,
	}
	return &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, tabOrder: []string{tab.ID}, activeTabID: tab.ID}
}

// 钉①（验收①）：活回合 + pending prompt → 放弃全部 + 前台取消（等待解除配对）。
func TestAcknowledgePendingPromptsForTabDropsAndCancelsLiveTurn(t *testing.T) {
	ctrl := &acknowledgeFakeSession{
		status: control.RuntimeStatus{Running: true, PendingPrompt: true, Cancellable: true},
		prompts: []control.PromptIdentity{
			{PromptID: "1", Kind: control.PromptApproval},
			{PromptID: "2", Kind: control.PromptAsk},
		},
	}
	app := newAcknowledgeTestApp(t, ctrl)
	installNoopRuntimeEvents(app)

	dropped, err := app.AcknowledgePendingPromptsForTab("tab-ack")
	if err != nil {
		t.Fatalf("AcknowledgePendingPromptsForTab: %v", err)
	}
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if ctrl.abandoned != 2 || ctrl.lastReason != "user-acknowledged" {
		t.Fatalf("abandon bookkeeping = %d/%q, want 2/user-acknowledged", ctrl.abandoned, ctrl.lastReason)
	}
	if ctrl.cancelled != 1 {
		t.Fatalf("live turn cancelled %d times, want exactly 1", ctrl.cancelled)
	}
}

// 负钉（验收②前半）：没有 pending prompt → 零动作，回合不被取消。
func TestAcknowledgePendingPromptsForTabIdleIsNoop(t *testing.T) {
	ctrl := &acknowledgeFakeSession{status: control.RuntimeStatus{Running: true}}
	app := newAcknowledgeTestApp(t, ctrl)
	installNoopRuntimeEvents(app)

	dropped, err := app.AcknowledgePendingPromptsForTab("tab-ack")
	if err != nil {
		t.Fatalf("AcknowledgePendingPromptsForTab: %v", err)
	}
	if dropped != 0 || ctrl.abandoned != 0 || ctrl.cancelled != 0 {
		t.Fatalf("idle acknowledge = dropped %d abandoned %d cancelled %d, want all zero", dropped, ctrl.abandoned, ctrl.cancelled)
	}
}

// 空闲回合（无活 turn）残留 prompt → 只放弃，不取消（无等待方可解除）。
func TestAcknowledgePendingPromptsForTabIdleTurnSkipsCancel(t *testing.T) {
	ctrl := &acknowledgeFakeSession{
		status:  control.RuntimeStatus{PendingPrompt: true},
		prompts: []control.PromptIdentity{{PromptID: "9", Kind: control.PromptApproval}},
	}
	app := newAcknowledgeTestApp(t, ctrl)
	installNoopRuntimeEvents(app)

	dropped, err := app.AcknowledgePendingPromptsForTab("tab-ack")
	if err != nil {
		t.Fatalf("AcknowledgePendingPromptsForTab: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if ctrl.cancelled != 0 {
		t.Fatalf("idle turn cancelled %d times, want 0", ctrl.cancelled)
	}
}

// 不带放弃能力的控制器 → 明确报错，文案不含虚构指引。
func TestAcknowledgePendingPromptsForTabUnsupportedController(t *testing.T) {
	app := newAcknowledgeTestApp(t, &legacySession{status: control.RuntimeStatus{PendingPrompt: true}})
	installNoopRuntimeEvents(app)

	_, err := app.AcknowledgePendingPromptsForTab("tab-ack")
	if err == nil {
		t.Fatal("unsupported controller must error")
	}
	const want = "cannot acknowledge pending prompts"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// 未知 tab → workspace 未就绪错误（nil ctrl 路径）。
func TestAcknowledgePendingPromptsForTabUnknownTab(t *testing.T) {
	app := newAcknowledgeTestApp(t, &acknowledgeFakeSession{})
	installNoopRuntimeEvents(app)

	if _, err := app.AcknowledgePendingPromptsForTab("missing"); err == nil {
		t.Fatal("unknown tab must error")
	}
}
