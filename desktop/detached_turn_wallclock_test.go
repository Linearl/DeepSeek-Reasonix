package main

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/control"
)

// tl1500 刀②（装机全局挂死修复件）验收钉：detached 后台心跳回合（诊断报告
// §2.2 的车队源头——协调线自检 task 264 的失控回合）超过墙钟上限后被取消。
//
// 钉走真实 executeTask 路径（后台模式 + stub 控制器挂住回合），通过
// REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS 把上限缩到测试量级。修前红：
// 没有任何墙钟机制，挂住的回合永不被取消；修后绿：上限到期后走既有取消
// 路径（ctrl.Cancel → 回合收尾 → finishInFlightTurn 清 in_flight_turn 标记、
// 锁面随保存家族释放——那是既有回合结束合同，control 包已有钉覆盖）。
//
// 回合身份防误伤由 guard 钉单独锁（detached_turn_wallclock_guard_test.go，
// 与实现同 commit）：墙钟到期只取消「本回合」，TurnID 已换人的不代按停止。

// wallClockCtrlStub is a heartbeat-run controller stub with cancel accounting.
// Deliberately a private stub (not an extension of heartbeatExecuteTaskCtrlStub)
// so this nail owns its Cancel contract.
type wallClockCtrlStub struct {
	stubSessionAPI
	status       control.RuntimeStatus
	submitted    []string
	approvalMode string
	cancelCalls  int
}

func (s *wallClockCtrlStub) RuntimeStatus() control.RuntimeStatus { return s.status }

func (s *wallClockCtrlStub) SubmitUserTurn(input, display string) {
	s.submitted = append(s.submitted, input)
	s.status.Running = true
	s.status.Cancellable = true
	s.status.TurnID = "turn-wallclock"
}

func (s *wallClockCtrlStub) SubmitHeartbeatTurn(input, display string) {
	s.SubmitUserTurn(input, display)
}

func (s *wallClockCtrlStub) SetToolApprovalMode(mode string) { s.approvalMode = mode }
func (s *wallClockCtrlStub) PlanMode() bool                  { return false }
func (s *wallClockCtrlStub) AutoApproveTools() bool          { return false }
func (s *wallClockCtrlStub) Goal() string                    { return "" }
func (s *wallClockCtrlStub) GoalStatus() string              { return control.GoalStatusStopped }
func (s *wallClockCtrlStub) ToolApprovalMode() string        { return s.approvalMode }
func (s *wallClockCtrlStub) SubagentPolicy() string          { return "" }
func (s *wallClockCtrlStub) SetSessionPath(string)           {}
func (s *wallClockCtrlStub) SessionPath() string             { return "" }
func (s *wallClockCtrlStub) SessionDir() string              { return "" }
func (s *wallClockCtrlStub) Close()                          {}

func (s *wallClockCtrlStub) Cancel() {
	s.cancelCalls++
	s.status.Running = false
	s.status.Cancellable = false
}

// TestDetachedHeartbeatTurnCancelledAtWallClock 是刀②主钉：后台心跳回合超过
// 墙钟上限后被取消（cause=detached_turn_wall_clock_exceeded 的打点在实现里）。
func TestDetachedHeartbeatTurnCancelledAtWallClock(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS", "1")
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.runtimeEvents.emit = func(context.Context, string, ...any) {}
	engine := &HeartbeatEngine{
		app:           app,
		pendingTopics: map[string]heartbeatPendingTopic{},
	}
	engine.heartbeatBackground = func() bool { return true }
	seed := HeartbeatTask{
		ID:           "wall-clock",
		Title:        "Wall clock",
		Prompt:       "runaway turn",
		ApprovalMode: "auto",
	}
	if err := engine.saveTasks([]HeartbeatTask{seed}); err != nil {
		t.Fatal(err)
	}
	engine.ReloadConfig()

	ctrl := &wallClockCtrlStub{}
	publishControllerAfterDelay(t, app, ctrl, 50*time.Millisecond)

	got := engine.executeTask(seed)
	if len(ctrl.submitted) != 1 {
		t.Fatalf("the background run must submit once (submitted %v)", ctrl.submitted)
	}
	_ = got

	// 上限 1s：给 8s 窗口等取消发生。修前这里永远等不到——没有任何墙钟机制。
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.cancelCalls > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the background heartbeat turn was never cancelled past its wall clock (tl1500 刀② red nail); cancelCalls=%d",
		ctrl.cancelCalls)
}
