package main

import (
	"context"
	"testing"
	"time"
)

// tl1500 刀② guard 钉：墙钟到期只取消「自己提交的那个回合」。回合身份按
// TurnID 绑定——到期时 TurnID 已换成别的回合（下一轮心跳或用户回合）不代
// 按停止；回合已结束（不可取消）同样不干预。上限关闭（env<=0 / 负值配置）
// 时不武装。
func TestDetachedTurnWallClockGuardOnlyCancelsOwnTurn(t *testing.T) {
	app := NewApp()
	app.ctx = context.Background()
	t.Setenv("REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS", "1")

	// 命中：同回合仍可取消 → Cancel 被调用。
	hits := &wallClockCtrlStub{}
	hits.status.TurnID = "turn-a"
	hits.status.Running = true
	hits.status.Cancellable = true
	app.armDetachedTurnWallClock("task-hit", "tab-hit", hits)
	waitForWallClockCancel(t, hits, "own running turn must be cancelled at the wall clock")

	// 身份漂移：到期时 TurnID 已换人 → 不代按停止。
	drift := &wallClockCtrlStub{}
	drift.status.TurnID = "turn-a"
	drift.status.Running = true
	drift.status.Cancellable = true
	app.armDetachedTurnWallClock("task-drift", "tab-drift", drift)
	drift.status.TurnID = "turn-user"
	drift.status.Cancellable = true
	time.Sleep(1500 * time.Millisecond)
	if drift.cancelCalls != 0 {
		t.Fatal("a wall clock bound to turn-a must not cancel turn-user (identity guard)")
	}

	// 已结束：不可取消 → 不干预。
	done := &wallClockCtrlStub{}
	done.status.TurnID = "turn-b"
	app.armDetachedTurnWallClock("task-done", "tab-done", done)
	time.Sleep(1500 * time.Millisecond)
	if done.cancelCalls != 0 {
		t.Fatal("a finished turn must not be cancelled at the wall clock")
	}

	// 关闭：env<=0 → 不武装。
	app2 := NewApp()
	app2.ctx = context.Background()
	t.Setenv("REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS", "0")
	off := &wallClockCtrlStub{}
	off.status.TurnID = "turn-c"
	off.status.Running = true
	off.status.Cancellable = true
	app2.armDetachedTurnWallClock("task-off", "tab-off", off)
	time.Sleep(600 * time.Millisecond)
	if off.cancelCalls != 0 {
		t.Fatal("a disabled wall clock must not arm (0 env = off)")
	}
}

// TestDetachedTurnWallClockLimitResolution pins the limit resolution order:
// env seconds wins, negative config disables, zero config = default 30m.
func TestDetachedTurnWallClockLimitResolution(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()

	t.Setenv("REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS", "45")
	if got := app.detachedTurnWallClockLimit(); got != 45*time.Second {
		t.Fatalf("env override = %v, want 45s", got)
	}
	t.Setenv("REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS", "0")
	if got := app.detachedTurnWallClockLimit(); got != 0 {
		t.Fatalf("env<=0 = %v, want disabled", got)
	}
	t.Setenv("REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS", "")
	if got := app.detachedTurnWallClockLimit(); got != detachedTurnWallClockDefault {
		t.Fatalf("default = %v, want %v", got, detachedTurnWallClockDefault)
	}
}

func waitForWallClockCancel(t *testing.T, ctrl *wallClockCtrlStub, why string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.cancelCalls > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s (no cancel within 8s)", why)
}
