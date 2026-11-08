package main

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/control"
)

// tl1500 刀④（装机全局挂死修复件，追加刀）验收钉：无人值守（心跳）回合内
// 禁止宿主自重启类操作。
//
// 诊断报告 §7 定案第一因：心跳回合 T28 于 11:03:45 执行 restart_update
// execute 把宿主拦腰打断（回合内自杀）→ 未决状态残留 → 恢复链 35 分钟失败
// → 11:41 续跑回合再次派发 restart_update + 3 bash 全部零回执 → 权威保存
// 边界停摆 → §2 的 a.mu 读者车队。修复落点（§7.3）：心跳任务的回合内禁止
// restart_update——出包装机使命不变（回合干净收尾落盘后，下一轮 ≤30 分钟
// 再执行装机）。
//
// 钉三态：
//  ① 心跳回合 ctx execute → 拒绝且 cause=mid_turn_host_restart_refused，
//     且 staged 目标不被消费（下一轮仍可执行）；修前红：消费掉 pending 后
//     落入 restartAndUpdateExempt（测试环境报实验开关关），不含 cause。
//  ② 心跳回合 ctx restart → 同族拒绝。
//  ③ 对照：非心跳回合（前台 autopilot 的 task-254 设计面 / UI 面）不被此门
//     拦截——守卫只对盖了心跳戳的回合 ctx 生效。
func TestHeartbeatTurnRefusedHostRestartMidTurn(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	c := autonomousUpdateController{app: app}

	hbCtx := control.WithHeartbeatTurn(context.Background())

	// staged 一个目标，让修前红更锐：修前会消费掉它并真正进入重启机器。
	app.autonomousMu.Lock()
	app.autonomousPending = pendingUpdateTarget{kind: "staging", version: "v0.0.0-nail"}
	app.autonomousMu.Unlock()

	_, err := c.ExecuteTarget(hbCtx, "C:/fake/session.jsonl")
	if err == nil || !strings.Contains(err.Error(), "mid_turn_host_restart_refused") {
		t.Fatalf("heartbeat-turn execute must be refused mid-turn with cause=mid_turn_host_restart_refused, got err=%v", err)
	}
	app.autonomousMu.Lock()
	stillStaged := app.autonomousPending.kind == "staging"
	app.autonomousMu.Unlock()
	if !stillStaged {
		t.Fatal("the mid-turn refusal must not consume the staged target — the next round still executes it")
	}

	// ② restart 同族拒绝。
	if _, err := c.RestartOnly(hbCtx, "C:/fake/session.jsonl"); err == nil || !strings.Contains(err.Error(), "mid_turn_host_restart_refused") {
		t.Fatalf("heartbeat-turn restart must be refused mid-turn with cause=mid_turn_host_restart_refused, got err=%v", err)
	}

	// ③ 对照：非心跳 ctx 走既有路径（无 pending → 普通错误），守卫不得开火。
	if _, err := c.ExecuteTarget(context.Background(), "C:/fake/session.jsonl"); err == nil || strings.Contains(err.Error(), "mid_turn_host_restart_refused") {
		t.Fatalf("non-heartbeat turns keep the designed restart path (task 254); guard must not fire, got err=%v", err)
	}
}
