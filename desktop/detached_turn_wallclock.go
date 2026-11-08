package main

// tl1500 刀②（装机全局挂死修复件）——detached 后台回合墙钟上限。
//
// 诊断报告（worktrees/tl1493/docs/reports-diag/装机挂死诊断-20261108.md）§2.2
// 定案的车队源头：协调线自检心跳（task 264 detached 后台）的回合 10:27 起失
// 控——checkpoint/jsonl 冻结、CPU 268%、goroutine 80→111，保存家族随之停摆
// 并长时占 a.mu 写锁（§3 同类扫描 #7：所有 detached 长回合任务机制上都有此
// 风险）。心跳引擎此前对提交出去的回合没有任何时长上界。
//
// 机制：后台心跳回合提交成功后（与 park 同一 fence，background &&
// !visibleBefore）武装一个墙钟看门狗。到期时若该回合仍在运行（按 TurnID
// 绑定身份，只取消自己提交的回合），走既有取消路径收尾——ctrl.Cancel()
// （internal/control/cancel.go，tl1304 回执语义）→ 回合上下文取消 → 既有
// finishInFlightTurn 清 in_flight_turn 标记、保存家族在回合边界恢复流动。
// cause 打点沿用 1459 的「取消必记因」语义：WARN 行携带
// cause=detached_turn_wall_clock_exceeded。context.Canceled 的取消形状也被
// interrupted_auto_resume 的排除清单显式豁免（不会与续轮机制打架）。
//
// 上限可配置：[agent] detached_turn_wall_clock_minutes（0 = 默认 30，负值 =
// 关闭）；REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS（秒粒度，测试/单进程
// 调试）优先。Goal 模式链式多回合时每回合各自受本上限约束。

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/control"
)

// detachedTurnWallClockDefault is the default per-turn wall-clock cap for
// detached/background heartbeat turns (tl1500 刀② 建议默认 30 分钟).
const detachedTurnWallClockDefault = 30 * time.Minute

// detachedTurnWallClockBindWindow bounds how long the watchdog waits for the
// runtime to surface the submitted turn's ID before binding the cap to it.
// 提交是同步准入，TurnID 正常立即可读；这个窗口只是防御。
const detachedTurnWallClockBindWindow = 3 * time.Second

// detachedTurnWallClockLimit resolves the effective cap: env (seconds, test/
// debug granularity) wins, then the config key, then the default. 0 disables.
func (a *App) detachedTurnWallClockLimit() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("REASONIX_DETACHED_TURN_WALL_CLOCK_SECONDS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n <= 0 {
				return 0 // env<=0 = 显式关闭（逃生口）
			}
			return time.Duration(n) * time.Second
		}
	}
	if cfg, _, err := a.loadDesktopUserConfigForView(); err == nil && cfg != nil {
		switch {
		case cfg.Agent.DetachedTurnWallClockMinutes < 0:
			return 0 // 配置显式关闭
		case cfg.Agent.DetachedTurnWallClockMinutes > 0:
			return time.Duration(cfg.Agent.DetachedTurnWallClockMinutes) * time.Minute
		}
	}
	return detachedTurnWallClockDefault
}

// armDetachedTurnWallClock watches one submitted detached/background turn and
// cancels it through the existing cancel path when it exceeds the cap. Never
// touches a.mu — the captured controller answers for itself.
func (a *App) armDetachedTurnWallClock(taskID, tabID string, ctrl control.SessionAPI) {
	limit := a.detachedTurnWallClockLimit()
	if limit <= 0 || ctrl == nil {
		return
	}
	turnID := ctrl.RuntimeStatus().TurnID
	bindDeadline := time.Now().Add(detachedTurnWallClockBindWindow)
	for turnID == "" && time.Now().Before(bindDeadline) {
		time.Sleep(50 * time.Millisecond)
		turnID = ctrl.RuntimeStatus().TurnID
	}
	a.goSafe("detachedTurnWallClock:"+tabID, func() {
		timer := time.NewTimer(limit)
		defer timer.Stop()
		<-timer.C
		st := ctrl.RuntimeStatus()
		if turnID != "" && st.TurnID != turnID {
			// 绑定的回合已经结束、运行时换到了别的回合（下一轮心跳或用户
			// 回合）——不代别人按停止。
			return
		}
		if !st.Cancellable {
			return // 已结束/收尾中，无需干预
		}
		slog.Warn("desktop: detached heartbeat turn hit the wall clock; cancelling via the existing cancel path",
			"cause", "detached_turn_wall_clock_exceeded",
			"task", taskID, "tab", tabID, "turn", turnID, "limit", limit.String())
		ctrl.Cancel()
	})
}
