package main

import (
	"context"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

// tl1723 UI focus 透传钉：CompactForTabWithFocus 把 focus 直送
// CompactForTrigger 的 instructions（trigger 恒为 ui）；CompactForTab 无参
// 默认与既有绑定为 focusless（""），语义与 tl1719 滚合时逐字节一致。
// 收口后（tl1723）持久化在 CompactForTrigger 内部，desktop 层不再补任何
// Snapshot 尾巴——落盘一致性由 control 层 TestTl1723CompactPersistUnifiedPin
// 钉死。

type tl1723FocusCapture struct {
	stubSessionAPI
	instructions []string
	triggers     []string
}

// RuntimeStatus overrides the embedded nil interface: the ensure-workspace
// pre-flight reads it before the compact call reaches the capture. The
// remaining read-only surface mirrors tabScopedActionController — the
// reconcile path touches these before CompactForTrigger fires.
func (c *tl1723FocusCapture) RuntimeStatus() control.RuntimeStatus {
	return control.RuntimeStatus{}
}
func (c *tl1723FocusCapture) SubagentPolicy() string          { return "" }
func (c *tl1723FocusCapture) PlanMode() bool                  { return false }
func (c *tl1723FocusCapture) AutoApproveTools() bool          { return false }
func (c *tl1723FocusCapture) Goal() string                    { return "" }
func (c *tl1723FocusCapture) ToolApprovalMode() string        { return control.ToolApprovalAsk }
func (c *tl1723FocusCapture) WorkspaceRoot() string           { return "" }
func (c *tl1723FocusCapture) SessionDir() string              { return "" }
func (c *tl1723FocusCapture) SessionPath() string             { return "" }
func (c *tl1723FocusCapture) Snapshot() error                 { return nil }

func (c *tl1723FocusCapture) CompactForTrigger(_ context.Context, instructions, trigger string) error {
	c.instructions = append(c.instructions, instructions)
	c.triggers = append(c.triggers, trigger)
	return nil
}

func newTl1723FocusApp(ctrl *tl1723FocusCapture) *App {
	return &App{
		tabs: map[string]*WorkspaceTab{
			"t": {ID: "t", Scope: "global", Ready: true, Ctrl: ctrl, disabledMCP: map[string]ServerView{}},
		},
		tabOrder:    []string{"t"},
		activeTabID: "t",
	}
}

func TestTl1723CompactFocusPassthroughPin(t *testing.T) {
	isolateDesktopUserDirs(t)
	cap := &tl1723FocusCapture{}
	app := newTl1723FocusApp(cap)

	// The button path stays focusless.
	if err := app.CompactForTab("t"); err != nil {
		t.Fatalf("CompactForTab: %v", err)
	}
	// The focused variant hands the focus straight through.
	if err := app.CompactForTabWithFocus("t", "keep the auth section"); err != nil {
		t.Fatalf("CompactForTabWithFocus: %v", err)
	}
	// App.Compact (the bound button entry) is focusless too.
	if err := app.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	wantInstr := []string{"", "keep the auth section", ""}
	wantTrigger := agent.CompactionTriggerUI
	for i := range wantInstr {
		if cap.instructions[i] != wantInstr[i] {
			t.Fatalf("call %d instructions = %q, want %q", i, cap.instructions[i], wantInstr[i])
		}
		if cap.triggers[i] != wantTrigger {
			t.Fatalf("call %d trigger = %q, want %q (ui label must survive the focus passthrough)", i, cap.triggers[i], wantTrigger)
		}
	}
}

func TestTl1723CompactFocusReadOnlyTabDeclines(t *testing.T) {
	isolateDesktopUserDirs(t)
	cap := &tl1723FocusCapture{}
	app := newTl1723FocusApp(cap)
	app.tabs["t"].ReadOnly = true
	if err := app.CompactForTabWithFocus("t", "focus"); err == nil {
		t.Fatal("CompactForTabWithFocus on a read-only tab must decline")
	}
	if len(cap.instructions) != 0 {
		t.Fatalf("read-only tab must not reach the controller (got %d calls)", len(cap.instructions))
	}
}
