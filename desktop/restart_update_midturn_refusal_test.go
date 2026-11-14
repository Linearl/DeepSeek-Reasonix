package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/control"
)

// tl1500 刀④返修（2026-11-09 用户拍板二轮①）+ tl1585 扩全回合（2026-11-14
// 用户令）验收钉。本文件名保留 refusal 作为 git 历史锚点，承载的已是排队语义：
// 任何回合内 restart_update 不再立即重启，而是排队到回合边界自动执行（重型钉见
// autonomous_update_test.go 的 TestHeartbeatTurnExecuteQueuesForTurnBoundary /
// TestForegroundTurnExecuteQueuesForTurnBoundary 族——带 scratch install root、
// 边界后真实走完既有重启路径）。
//
// 诊断报告 §7 定案第一因的机理约束不变：心跳回合 T28 于 11:03:45 执行
// restart_update execute 把宿主拦腰打断（回合内自杀）→ 恢复链 35 分钟失败 →
// a.mu 读者车队。返修改变的只是处置——回合内不杀宿主这一点照旧，拒绝文案
// （cause=mid_turn_host_restart_refused）退役为排队文案
// （cause=mid_turn_host_restart_queued），等待者在本回合收尾（转录落盘、
// TurnDone 发出）后执行同一目标，不再等下一轮心跳周期。tl1585 补前台半边：
// 前台「立即执行」的 quitGrace+relaunch 链与回合收尾之间是竞态（包92 装机
// 实例=拦腰打断），排队把它改成确定性等待。
//
// 钉三态（轻量面，NewApp + 纯 ctx，不碰安装布局）：
//
//	① 心跳回合 ctx execute → 返回排队文案（cause=mid_turn_host_restart_queued）
//	   且 staged 目标不被消费（边界等待者仍会执行它）；
//	② 同回合再调 restart → 去重面：只提示已有排队，槽内仍是第一次的请求；
//	③ tl1585 翻转（原「前台不排队」对照退役）：前台回合（无心跳戳）同样不立即
//	   执行——槽已被①占用时拿到去重提示，staged 目标照旧不被消费，queued 标记
//	   必须出现（重型排队钉见上）。
func TestHeartbeatTurnHostRestartQueuesMidTurn(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = context.Background()
	c := autonomousUpdateController{app: app}

	turnCtx, cancel := context.WithCancel(control.WithHeartbeatTurn(context.Background()))
	defer cancel()

	// staged 一个目标：排队面必须不消费它（与拒绝语义同一条硬约束——目标
	// 归属边界等待者，回合内任何路径都不得动它）。
	app.autonomousMu.Lock()
	app.autonomousPending = pendingUpdateTarget{kind: "staging", version: "v0.0.0-nail"}
	app.autonomousMu.Unlock()

	note, err := c.ExecuteTarget(turnCtx, "C:/fake/session.jsonl")
	if err != nil {
		t.Fatalf("heartbeat-turn execute must queue (not refuse): err=%v", err)
	}
	if !strings.Contains(note, midTurnRestartQueuedMarker) {
		t.Fatalf("queued note must carry cause=%s, got: %v", midTurnRestartQueuedMarker, note)
	}
	app.autonomousMu.Lock()
	stillStaged := app.autonomousPending.kind == "staging"
	queuedAction := ""
	if app.autonomousBoundaryRestart != nil {
		queuedAction = app.autonomousBoundaryRestart.action
	}
	app.autonomousMu.Unlock()
	if !stillStaged {
		t.Fatal("the queue must not consume the staged target — the boundary waiter still executes it")
	}
	if queuedAction != "execute" {
		t.Fatalf("slot must hold the execute request, action=%q", queuedAction)
	}

	// ② 同回合 restart：去重面只提示，槽内仍是第一次的请求（跨动作同样去重）。
	dup, err := c.RestartOnly(turnCtx, "C:/fake/session.jsonl")
	if err != nil {
		t.Fatalf("the dedup face must not error the model into retrying: %v", err)
	}
	if !strings.Contains(dup, midTurnRestartQueuedMarker) || !strings.Contains(dup, "duplicate") {
		t.Fatalf("second call must report the existing queue, got: %v", dup)
	}
	app.autonomousMu.Lock()
	queuedAction = ""
	if app.autonomousBoundaryRestart != nil {
		queuedAction = app.autonomousBoundaryRestart.action
	}
	app.autonomousMu.Unlock()
	if queuedAction != "execute" {
		t.Fatalf("first request must keep owning the slot, action=%q", queuedAction)
	}

	// ③ tl1585 翻转：前台 ctx（无心跳戳）同样不立即执行——槽被①占用 → 去重
	// 提示（仍带 queued 标记），staged 目标照旧不被消费。
	fgNote, err := c.ExecuteTarget(context.Background(), "C:/fake/session.jsonl")
	if err != nil {
		t.Fatalf("foreground execute must queue too (tl1585), not error: %v", err)
	}
	if !strings.Contains(fgNote, midTurnRestartQueuedMarker) {
		t.Fatalf("foreground call must not execute immediately — queue family text required, got: %v", fgNote)
	}
	app.autonomousMu.Lock()
	stillStaged = app.autonomousPending.kind == "staging"
	app.autonomousMu.Unlock()
	if !stillStaged {
		t.Fatal("the foreground call must not consume the staged target either")
	}

	// 收尾：cancel 让边界等待者执行——staged 目标仍在（①②③都没消费它），
	// 等待者走 executeStagedTarget→publish 面，在无安装布局的隔离环境下报普通
	// 错误并打 warn（runQueuedBoundaryRestart 的既定失败面），槽清空即可证明
	// 等待者已醒，测试不留悬挂 goroutine 触碰已拆除的隔离面。
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		app.autonomousMu.Lock()
		empty := app.autonomousBoundaryRestart == nil
		app.autonomousMu.Unlock()
		if empty {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("turn-boundary waiter did not drain the queue slot after cancel")
}
