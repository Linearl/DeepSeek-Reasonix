package main

// 任务 922（886 机制①inFlight 残留）：tabEventSink.turn.inFlight 的清理只挂在
// TurnDone fan-out 与 admission.finish 的 started==false 分支上；rebind/epoch
// 窗口内 TurnDone 未回到该 sink 时残留为 true，此后该 tab 上任何 Submit /
// beforeInboxDispatch→beginRuntimeTurn 撞 tryBeginTurn false → ErrTurnRunning，
// dispatchInboxOnce 按设计静默 idle——队首永不认领（886 新6 现场：12:22 后 4 次
// drain 全部空转）。本文件钉死修复的两面：
//   - 接续协议：runtime (re)attach 时 controller 可证空闲（RuntimeStatus.
//     Running 合并 running||finishing，finishGuardedTurn 只在 TurnDone
//     fan-out 完成后才落回 false）而 sink 仍持预留 ⇒ 残留，清掉后队首可认领；
//   - 反向用例：真在飞的 turn（Running==true）预留必须原样保留，由它自己的
//     TurnDone 清理——在飞消息不丢。

import (
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/event"
)

type reservationProbeController struct {
	stubSessionAPI
	running bool
}

func (c *reservationProbeController) RuntimeStatus() control.RuntimeStatus {
	return control.RuntimeStatus{Running: c.running}
}

// TestEmitTurnDoneClearsDetachedSinkReservation 是验收①的 sink 侧合同：rebind
// 把 runtime 移到 detached（setBinding+clearContext fence，不清 turn 预留）后，
// 在飞 turn 的 TurnDone 仍回到该 sink 并清掉预留——超限会话的在飞消息不丢。
func TestEmitTurnDoneClearsDetachedSinkReservation(t *testing.T) {
	sink := &tabEventSink{tabID: "tab-922"}
	if !sink.tryBeginTurn("u-inflight") {
		t.Fatal("failed to reserve the in-flight turn")
	}

	// detachRuntimeForReplacementLocked 的 fence 形状：reroute binding、清 ctx，
	// 不动 turn 预留。
	sink.setBinding("", nil)
	sink.clearContext()

	sink.Emit(event.Event{Kind: event.TurnDone})
	if !sink.tryBeginTurn("u-next") {
		t.Fatal("TurnDone must clear the reservation even while the sink is detached — the in-flight turn's completion still returns to it")
	}
	sink.cancelTurnStart()
}

// TestClearStaleTurnReservationOnIdleAttach 是验收①的接续协议正面用例：残留
// 预留 + controller 可证空闲 ⇒ 清掉，队首恢复可认领（降级后可恢复投递）。
func TestClearStaleTurnReservationOnIdleAttach(t *testing.T) {
	sink := &tabEventSink{tabID: "tab-922"}
	if !sink.tryBeginTurn("u-stale") {
		t.Fatal("failed to simulate the stranded reservation")
	}

	clearStaleTurnReservation(&reservationProbeController{running: false}, sink, "tab-922")

	if !sink.tryBeginTurn("u-recovered") {
		t.Fatal("an idle controller with a held reservation is unambiguous residue — clearing it must make the tab claimable again")
	}
	sink.cancelTurnStart()
}

// TestClearStaleTurnReservationKeepsLiveTurn 是反向用例：真在飞的 turn
// （RuntimeStatus().Running==true，含 finishing 窗口）预留必须原样保留——它
// 自己的 TurnDone 会清；误清会让并发准入穿过 TurnDone fan-out 窗口。
func TestClearStaleTurnReservationKeepsLiveTurn(t *testing.T) {
	sink := &tabEventSink{tabID: "tab-922"}
	if !sink.tryBeginTurn("u-live") {
		t.Fatal("failed to reserve the live turn")
	}

	clearStaleTurnReservation(&reservationProbeController{running: true}, sink, "tab-922")

	if sink.tryBeginTurn("u-intruder") {
		t.Fatal("a live turn's reservation must survive the attach-time heal — its own TurnDone clears it")
	}
	// 在飞 turn 的 TurnDone 照常清理（正向合同不变）。
	sink.Emit(event.Event{Kind: event.TurnDone})
	if !sink.tryBeginTurn("u-next") {
		t.Fatal("the live turn's TurnDone must clear its reservation")
	}
	sink.cancelTurnStart()
}

// TestClearStaleTurnReservationNilSafe 钉死空入参不 panic（tab 无 sink / runtime
// 未发布都是合法瞬时形状）。
func TestClearStaleTurnReservationNilSafe(t *testing.T) {
	clearStaleTurnReservation(nil, &tabEventSink{}, "tab-922")
	clearStaleTurnReservation(&reservationProbeController{}, nil, "tab-922")
	clearStaleTurnReservation(nil, nil, "tab-922")
}
