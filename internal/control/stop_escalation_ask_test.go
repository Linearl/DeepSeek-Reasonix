package control

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// P16（0119 现场「ask 无弹窗 + 连点终止无效」）交叉验收：turn 卡在 Ask 等待里
// 时，P7 三级终止必须覆盖——第一次点击（L1 优雅取消）就要打断 ask 等待并把
// 挂起问题从重放快照里撤下（终止后重连不得复活死问题卡片）；终止后若 turn
// 迟迟不退出，升级链照常武装（L1 无倒计时）；turn 真正结束后状态归零。此前
// P7 测试只压「忽略取消的假 turn」、ask 测试只压裸 ctx cancel，这个交叉点
// （真实 ask 等待 × 停止按钮入口）没有钉子。

// stopAskEscalationFixture 启动一个卡在 c.Ask(ctx, …) 里的 turn：ctx 即 P7
// 取消链上的 turn ctx，与桌面现场同构。返回 release 供测试放行 turn 体
// （模拟收尾缓慢），以便确定性断言升级状态；清理兜底防 goroutine 泄漏。
func stopAskEscalationFixture(t *testing.T) (*Controller, *askProbeSink, <-chan error, func()) {
	t.Helper()
	sink := &askProbeSink{}
	c := New(Options{Sink: sink, SessionDir: t.TempDir()})
	t.Cleanup(c.Close)
	errc := make(chan error, 1)
	var once sync.Once
	fixtureDead := make(chan struct{})
	t.Cleanup(func() { once.Do(func() { close(fixtureDead) }) })
	release := func() { once.Do(func() { close(fixtureDead) }) }
	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		_, askErr := c.Ask(ctx, askProbeQuestions())
		errc <- askErr
		// Ask 已被打断，但 turn 体继续占位（收尾缓慢的现场形状）。
		<-fixtureDead
		return askErr
	})
	<-started
	return c, sink, errc, release
}

func waitProbeAsks(t *testing.T, sink *askProbeSink, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		asks, _ := sink.counts()
		if asks == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("ask request events = %d, want %d within timeout", asks, want)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// 一次点击即打断 ask 等待：Ask 以 ctx 错误返回、挂起问题撤出重放快照、
// 升级链武装在 L1；turn 结束后状态归零。
func TestCancelStopInterruptsAskWait(t *testing.T) {
	shrinkStopEscalationForTest(t, time.Hour, time.Hour) // 只测按压升级，不要保底定时器抢戏
	c, sink, errc, release := stopAskEscalationFixture(t)

	// 面板链前半段：AskRequest 已发出（前端有卡可弹）、问题在重放快照里。
	waitProbeAsks(t, sink, 1)
	if _, pending := c.approval.snapshotPrompts(); len(pending) != 1 {
		t.Fatalf("snapshot listed %d pending ask(s), want the live question replayable", len(pending))
	}

	// 现场动作：终止按钮第一下（L1）。
	c.CancelStop()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Ask returned %v, want context.Canceled — the stop press did not reach the ask wait", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the first stop press did not interrupt the ask wait; the turn stayed blocked")
	}

	// 终止后不得有僵尸问题：快照清空，重放静默（0119 的「终止后仍卡死」反向面）。
	if _, pending := c.approval.snapshotPrompts(); len(pending) != 0 {
		t.Fatalf("snapshot kept %d pending ask(s) after the stop; a reconnect would resurrect a dead question", len(pending))
	}
	c.ReplayPendingPrompts()
	time.Sleep(30 * time.Millisecond)
	if asks, _ := sink.counts(); asks != 1 {
		t.Fatalf("replay re-emitted asks after termination (%d total), want the cancelled question to stay dead", asks)
	}

	// turn 体仍占位：升级链必须武装在 L1（保底方向正确）。
	if st := waitStopLevel(t, c, StopLevelNormal, time.Second); st.StopDeadlineUnix != 0 {
		t.Fatalf("L1 must not carry a countdown deadline, got %d", st.StopDeadlineUnix)
	}

	// 放行 turn 体：正常收尾后升级状态归零，下一次停止重新从 L1 开始。
	release()
	deadline := time.After(2 * time.Second)
	for {
		if st := c.RuntimeStatus(); st.StopLevel == StopLevelNone {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("stop level = %d after the turn finished, want the escalation reset to none", c.RuntimeStatus().StopLevel)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// ask 还在排队（前面压着另一个未答 prompt）时点终止：等待同样要被打断，
// 排队登记撤干净、prompt 锁不泄漏。
func TestCancelStopInterruptsQueuedAsk(t *testing.T) {
	shrinkStopEscalationForTest(t, time.Hour, time.Hour)
	shortenPromptQueueNotice(t)
	sink := &askProbeSink{}
	c := New(Options{Sink: sink, SessionDir: t.TempDir()})
	t.Cleanup(c.Close)
	c.approval.promptMu.Lock() // 站位：更早的 prompt 还没答

	errc := make(chan error, 1)
	var once sync.Once
	fixtureDead := make(chan struct{})
	t.Cleanup(func() { once.Do(func() { close(fixtureDead) }) })
	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		_, askErr := c.Ask(ctx, askProbeQuestions())
		errc <- askErr
		<-fixtureDead
		return askErr
	})
	<-started

	deadline := time.After(2 * time.Second)
	for c.approval.queuedAsks() != 1 {
		select {
		case <-deadline:
			t.Fatal("the ask never registered while queued behind the earlier prompt")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if asks, _ := sink.counts(); asks != 0 {
		t.Fatalf("queued ask emitted %d AskRequest event(s), want it held until its turn", asks)
	}

	// 终止按钮第一下：排队中的 ask 也要被放出来。
	c.CancelStop()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued Ask returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stop press did not interrupt the queued ask wait")
	}
	if got := c.approval.queuedAsks(); got != 0 {
		t.Fatalf("queued asks = %d after the stop, want the registration dropped", got)
	}

	// 被抛弃的等待不得占住 prompt 锁（下一个 prompt 还要用）：先放开测试
	// 自己的站位锁，再轮询确认 abandon-释放协程把锁交还。
	c.approval.promptMu.Unlock()
	acquired := false
	for range 200 {
		if c.approval.promptMu.TryLock() {
			acquired = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !acquired {
		t.Fatal("the cancelled queued ask leaked the prompt lock")
	}
	c.approval.promptMu.Unlock()
}
