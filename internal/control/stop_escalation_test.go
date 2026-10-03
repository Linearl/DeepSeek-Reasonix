package control

import (
	"context"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// 任务461-P7 三级终止验收：假 turn 忽略 ctx cancel（模拟卡在工具调用里的
// 会话）→ 第一次停止(优雅，L1) → 第二次(15s 宽限+倒计时，L2) → 第三次
// (即刻 force，L3)；或第二次后等满宽限自动升级；不点则保底自动宽限→force；
// turn 结束后状态归零，下一次停止重新从 L1 开始。

func shrinkStopEscalationForTest(t *testing.T, autoGrace, forceGrace time.Duration) {
	t.Helper()
	oldAuto, oldForce := stopAutoGraceAfter, stopForceGrace
	stopAutoGraceAfter, stopForceGrace = autoGrace, forceGrace
	t.Cleanup(func() { stopAutoGraceAfter, stopForceGrace = oldAuto, oldForce })
}

// stopEscalationFixture starts one turn whose body ignores cancellation (the
// wedged-tool scenario) and returns the controller plus observation handles.
// The body never returns on its own — the escalation must cut it loose — but
// it unwinds at test cleanup so goleak sees no leaked goroutine.
func stopEscalationFixture(t *testing.T) (*Controller, context.Context, chan struct{}) {
	t.Helper()
	c := New(Options{Sink: event.Discard})
	t.Cleanup(c.Close)
	forceStarted := make(chan struct{})
	fixtureDead := make(chan struct{})
	t.Cleanup(func() { close(fixtureDead) })
	var bodyCtx context.Context
	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		bodyCtx = ctx
		close(started)
		// The wedged-tool shape: the turn ctx fires at L1 but the body keeps
		// holding the turn open (a hung tool call ignoring cancellation).
		<-ctx.Done()
		close(forceStarted)
		<-fixtureDead
		return ctx.Err()
	})
	<-started
	return c, bodyCtx, forceStarted
}

func waitStopLevel(t *testing.T, c *Controller, want int, timeout time.Duration) RuntimeStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if st := c.RuntimeStatus(); st.StopLevel == want {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	st := c.RuntimeStatus()
	t.Fatalf("stop level = %d (deadline %d), want %d within %v", st.StopLevel, st.StopDeadlineUnix, want, timeout)
	return st
}

// The acceptance choreography: click → L1 (turn ctx cancelled, graceful
// window) → click → L2 (grace countdown) → click → L3 (force fires).
func TestCancelStopThreePressEscalation(t *testing.T) {
	shrinkStopEscalationForTest(t, time.Hour, time.Hour) // no auto escalation; presses drive everything
	c, bodyCtx, forceStarted := stopEscalationFixture(t)

	c.CancelStop()
	st := waitStopLevel(t, c, StopLevelNormal, time.Second)
	if st.StopDeadlineUnix != 0 {
		t.Fatalf("L1 must not carry a countdown deadline, got %d", st.StopDeadlineUnix)
	}
	select {
	case <-bodyCtx.Done():
	default:
		t.Fatal("L1 must cancel the turn context (the graceful half)")
	}
	select {
	case <-agent.StopForceDone(bodyCtx):
		t.Fatal("L1 must not fire the force half")
	default:
	}
	// The turn body ignores cancellation: the turn keeps running (no reset).
	if st := c.RuntimeStatus(); st.StopLevel == StopLevelNone {
		t.Fatal("L1 must stay armed while the turn ignores cancellation")
	}

	c.CancelStop()
	st = waitStopLevel(t, c, StopLevelGrace, time.Second)
	if st.StopDeadlineUnix <= time.Now().Unix() {
		t.Fatalf("L2 must carry a future countdown deadline, got %d", st.StopDeadlineUnix)
	}

	c.CancelStop()
	st = waitStopLevel(t, c, StopLevelForce, time.Second)
	select {
	case <-agent.StopForceDone(bodyCtx):
	default:
		t.Fatal("L3 must fire the executor force signal")
	}
	select {
	case <-forceStarted:
	default:
		t.Fatal("the force fixture must observe the turn context cancellation")
	}
}

// A press during the grace jumps straight to force (ruling ② 可随时再点直接跳).
func TestCancelStopDuringGraceJumpsToForce(t *testing.T) {
	shrinkStopEscalationForTest(t, time.Hour, time.Hour)
	c, bodyCtx, _ := stopEscalationFixture(t)

	c.CancelStop()
	waitStopLevel(t, c, StopLevelNormal, time.Second)
	c.CancelStop()
	waitStopLevel(t, c, StopLevelGrace, time.Second)
	c.CancelStop()
	waitStopLevel(t, c, StopLevelForce, time.Second)
	select {
	case <-agent.StopForceDone(bodyCtx):
	default:
		t.Fatal("the grace press must fire the force signal immediately")
	}
}

// ④ 保底: a stop the turn never acknowledges auto-opens the grace after
// stopAutoGraceAfter, and the grace auto-fires force after stopForceGrace.
func TestCancelStopAutoEscalationWithoutPresses(t *testing.T) {
	shrinkStopEscalationForTest(t, 80*time.Millisecond, 70*time.Millisecond)
	c, bodyCtx, _ := stopEscalationFixture(t)

	c.CancelStop()
	waitStopLevel(t, c, StopLevelNormal, time.Second)
	waitStopLevel(t, c, StopLevelGrace, 2*time.Second)
	waitStopLevel(t, c, StopLevelForce, 2*time.Second)
	select {
	case <-agent.StopForceDone(bodyCtx):
	default:
		t.Fatal("the automatic escalation must end in force")
	}
}

// The escalation state dies with the turn: after a normal completion the next
// stop starts fresh at L1 (level 0 → a fresh graceful cancel), never armed.
func TestCancelStopResetsWhenTurnCompletes(t *testing.T) {
	shrinkStopEscalationForTest(t, time.Hour, time.Hour)
	c := New(Options{Sink: event.Discard})
	t.Cleanup(c.Close)
	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		return nil
	})
	<-started
	deadline := time.Now().Add(2 * time.Second)
	for c.RuntimeStatus().Running && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	c.CancelStop() // nothing live: behaves like the ordinary no-op cancel
	if st := c.RuntimeStatus(); st.StopLevel != StopLevelNone {
		t.Fatalf("a completed turn must reset the escalation state, got level %d", st.StopLevel)
	}
}

// The grace countdown firing on its own ends in force (ruling ②'s 自动升级).
func TestGraceCountdownAutoFires(t *testing.T) {
	shrinkStopEscalationForTest(t, time.Hour, 60*time.Millisecond)
	c, bodyCtx, _ := stopEscalationFixture(t)

	c.CancelStop()
	waitStopLevel(t, c, StopLevelNormal, time.Second)
	c.CancelStop()
	waitStopLevel(t, c, StopLevelGrace, time.Second)
	waitStopLevel(t, c, StopLevelForce, 2*time.Second)
	select {
	case <-agent.StopForceDone(bodyCtx):
	default:
		t.Fatal("an expired countdown must fire the force signal")
	}
}
