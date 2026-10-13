package main

import (
	"path/filepath"
	"testing"
)

// fenceSettleOnlyController 有 fence 结算 capability 但没有 guidance 结算
// capability——钉「旧 controller/纯 fake」下的静默跳过合同。
type fenceSettleOnlyController struct {
	stubSessionAPI
	path        string
	settleCalls int
}

func (c *fenceSettleOnlyController) SessionPath() string { return c.path }

func (c *fenceSettleOnlyController) SetInboxPausedPassive(bool) error { return nil }

func (c *fenceSettleOnlyController) SettleRestartInterruptedEffects() int {
	c.settleCalls++
	return 0
}

func (c *fenceSettleOnlyController) settleCount() int { return c.settleCalls }

// 任务840：计划内切换重启的引导已读确认——桌面恢复点接线钉。
//
// 现场（2026-10-13 用户实测，24.1 无人值守更新重启）：更新前已消费的 3 条
// 引导（守护⑤通报/降级回执/⑤收治）重启后被重新标记「待处理」并带重试按钮。
// 修法 = 名册消费点（计划内切换重启的权威证据，task-254 roster / task-49-A2
// goal 恢复点）在清 recovery pause 之前调用 Controller.SettlePlannedRestart-
// Guidance()，把「已 admit + collab 已读确认」的停靠行结算下架（复用普通重启
// 已有的「恢复路径已读确认」语义到切换链路——435 名册消费点裁决的同款形态）。
//
// 本文件钉桌面层的三个合同：
//  1. 名册命中的恢复点必须恰好调用一次 guidance settle，且在清 pause 之前；
//  2. crash 会话（无名册）不触发（普通重启零影响——不回归面）；
//  3. 中断工具不重放（461-P2 兜底②）早退时不结算（恢复链没接管会话，
//     停靠行保持人审）。

// TestPlannedRestartResumesettleGuidanceBeforeUnpause 钉恢复点的调用顺序：
// guidance settle → unpause。已消费引导在货架第一次快照前就下架，重试按钮
// 不再出现。
func TestPlannedRestartResumeSettlesGuidanceBeforeUnpause(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "update.session.jsonl")
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging under the default dial must succeed")
	}

	ctrl := &fenceProbeController{path: session}
	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(*App, string, string) error { return nil }
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	if got := ctrl.guidanceSettleCount(); got != 1 {
		t.Fatalf("the planned-restart guidance settle must fire exactly once at the roster restore point, got %d", got)
	}
	order := ctrl.callOrderNow()
	if len(order) < 2 || order[0] != "guidance" || order[1] != "unpause" {
		t.Fatalf("guidance settle must run BEFORE the recovery pause clears, call order = %v", order)
	}
}

// TestCrashRestartDoesNotSettleGuidance 是不回归面：无名册会话（真崩溃打断）
// 的恢复点不触发 guidance settle——普通重启/打开不结算任何行，货架照旧。
func TestCrashRestartDoesNotSettleGuidance(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "crashed.session.jsonl")
	app := &App{}

	ctrl := &fenceProbeController{path: session}
	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(*App, string, string) error {
		t.Errorf("a crash-interrupted session must not be auto-resumed")
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	if got := ctrl.guidanceSettleCount(); got != 0 {
		t.Fatalf("a crash-interrupted session's guidance parks must not be settled, got %d settles", got)
	}
	if len(ctrl.callOrderNow()) != 0 {
		t.Fatalf("the crash restore point must not touch the inbox at all: %+v", ctrl.callOrderNow())
	}
}

// interruptedToolController carries the 461-P2 backstop probe with a pending
// interrupted tool call — the skip path needs a controller that actually
// reports it (fenceProbeController deliberately has no probe, so the 435
// resume-with-settle chain keeps its shape).
type interruptedToolController struct {
	fenceProbeController
}

func (c *interruptedToolController) HasPendingToolRecovery() bool { return true }

// TestInterruptedToolResumeKeepsGuidanceParked 钉 461-P2 兜底②的优先级：
// 恢复链决定不重放（中断工具待人审）时，guidance settle 也不跑——恢复链没有
// 接管会话，停靠行维持人审，不因计划内重启被单方面清掉。
func TestInterruptedToolResumeKeepsGuidanceParked(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "tool-stuck.session.jsonl")
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging under the default dial must succeed")
	}

	inner := fenceProbeController{path: session}
	ctrl := &interruptedToolController{fenceProbeController: inner}
	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(*App, string, string) error {
		t.Errorf("an interrupted-tool session must not be auto-resumed")
		return nil
	}
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab)

	if got := ctrl.guidanceSettleCount(); got != 0 {
		t.Fatalf("the interrupted-tool skip must keep guidance parks for review, got %d settles", got)
	}
	if len(ctrl.inboxPausedCalls) != 0 {
		t.Fatalf("the interrupted-tool skip must not touch the pause: %+v", ctrl.inboxPausedCalls)
	}
}

// TestGuidanceSettleWithoutCapabilityIsSilent 钉窄接口合同：无 capability 的
// controller（fake/旧版）下恢复链静默跳过，不 panic、不改行为——与
// restartFenceSettler 同款形态。
func TestGuidanceSettleWithoutCapabilityIsSilent(t *testing.T) {
	isolateDesktopUserDirs(t)
	session := filepath.Join(t.TempDir(), "legacy.session.jsonl")
	app := &App{}
	if !app.stageInterruptedByRestart(session) {
		t.Fatal("precondition: staging under the default dial must succeed")
	}

	// plainFencelessController 只实现 fence settle，不实现 guidance settle。
	ctrl := &fenceSettleOnlyController{path: session}
	tab := &WorkspaceTab{ID: "t1", SessionPath: session, Ready: true, Ctrl: ctrl}

	oldSubmit := restartResumeSubmit
	restartResumeSubmit = func(*App, string, string) error { return nil }
	t.Cleanup(func() { restartResumeSubmit = oldSubmit })

	app.maybeResumeAutonomousUpdateTab(tab) // 不得 panic

	if got := ctrl.settleCount(); got != 1 {
		t.Fatalf("the fence settle capability keeps working: got %d", got)
	}
}
