package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// preflightFailureEvent is the ToolResult shape the agent emits for a call the
// bash preflight refused without executing (tl-1213 incident face: a
// duplicated "command" field collapses to "" → "command is required",
// state=not_run).
func preflightFailureEvent() event.Event {
	return event.Event{Kind: event.ToolResult, Tool: event.Tool{
		ID: "call-tl1213", Name: "bash", Err: "command is required",
		Execution: &event.ShellExecution{State: "not_run", FailurePhase: "preflight"},
	}}
}

func shrinkPreflightStallThreshold(t *testing.T, d time.Duration) {
	t.Helper()
	oldAbort := turnPreflightStallAbortThreshold.Load()
	oldStall := turnStallThreshold.Load()
	oldInterval := midTurnSnapshotInterval.Load()
	turnPreflightStallAbortThreshold.Store(int64(d))
	turnStallThreshold.Store(int64(d / 2))
	midTurnSnapshotInterval.Store(int64(10 * time.Millisecond))
	t.Cleanup(func() {
		turnPreflightStallAbortThreshold.Store(oldAbort)
		turnStallThreshold.Store(oldStall)
		midTurnSnapshotInterval.Store(oldInterval)
	})
}

// backdateSilence rewinds only the silence clock. liveness.reset would also
// wipe the preflight marker, which several cases must keep armed.
func backdateSilence(c *Controller, d time.Duration) {
	c.liveness.lastEvent.Store(time.Now().Add(-d).UnixNano())
}

// A1+A2 端到端：preflight 失败是 turn 最后一个工具结果、此后全事件静默超过
// 阈值 → 看门自动终止；TurnDone 携带 TurnPreflightStallAbortError（failed 终
// 态，不是静默 finish，也不是永久 running），且带一条用户可见 abort 告警。
func TestPreflightStallWatchdogAbortsTurnTerminalFailed(t *testing.T) {
	shrinkPreflightStallThreshold(t, 120*time.Millisecond)

	aborts := &atomic.Int64{}
	doneEvents := make(chan event.Event, 4)
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		switch {
		case e.Kind == event.Notice && e.Code == event.NoticeCodeTurnPreflightStallAbort:
			aborts.Add(1)
		case e.Kind == event.TurnDone:
			doneEvents <- e
		}
	})})
	t.Cleanup(c.Close)

	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		c.sink.Emit(preflightFailureEvent())
		<-ctx.Done()
		return ctx.Err()
	})
	<-started

	var td event.Event
	select {
	case td = <-doneEvents:
	case <-time.After(5 * time.Second):
		t.Fatal("preflight-stalled turn was never auto-aborted (still running)")
	}
	var abortErr *TurnPreflightStallAbortError
	if !errors.As(td.Err, &abortErr) {
		t.Fatalf("TurnDone.Err = %v, want *TurnPreflightStallAbortError", td.Err)
	}
	if abortErr.ToolName != "bash" || abortErr.CallID != "call-tl1213" {
		t.Fatalf("abort cause = %+v, want the preflight-failed bash call", abortErr)
	}
	if td.Cancelled {
		t.Fatal("watchdog abort must not present as a user cancellation")
	}
	waitFor(t, "the user-visible abort notice", func() bool { return aborts.Load() == 1 })
}

// A5 面 1：同样静默但没有 preflight 失败标记 → 维持既有 WARN-only 契约，
// 看门绝不自动终止（合法长任务与卡死从外部不可分，只有 preflight 标记使自
// 动终止安全）。
func TestPreflightStallWithoutMarkerKeepsWarnOnly(t *testing.T) {
	shrinkPreflightStallThreshold(t, 80*time.Millisecond)

	aborts := &atomic.Int64{}
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeTurnPreflightStallAbort {
			aborts.Add(1)
		}
	})})
	t.Cleanup(c.Close)
	c.running = true
	// Silence far past the threshold, no preflight marker.
	backdateSilence(c, 2*time.Hour)
	c.warnIfTurnStalled(time.Now())
	time.Sleep(50 * time.Millisecond)
	if aborts.Load() != 0 {
		t.Fatalf("plain silence fired %d aborts, want warn-only", aborts.Load())
	}
}

// A5 面 2：preflight 失败之后来了任何后续工作事件（新派发/兄弟进度/下一个
// 结果）→ 标记清除，看门不再终止（健康重试轮、混合 batch 零误伤）。
func TestPreflightMarkerClearedByLaterWork(t *testing.T) {
	shrinkPreflightStallThreshold(t, 80*time.Millisecond)

	aborts := &atomic.Int64{}
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeTurnPreflightStallAbort {
			aborts.Add(1)
		}
	})})
	t.Cleanup(c.Close)
	c.running = true

	c.sink.Emit(preflightFailureEvent())
	c.sink.Emit(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "call-2", Name: "read_file", Output: "ok"}})
	backdateSilence(c, 2*time.Hour)
	c.warnIfTurnStalled(time.Now())
	time.Sleep(50 * time.Millisecond)
	if aborts.Load() != 0 {
		t.Fatalf("a successful result after the preflight failure still armed %d aborts", aborts.Load())
	}

	// A marker whose only follow-up is provider-side churn (stream attempt
	// frames) must KEEP the marker: that silence is exactly the wedged shape.
	c.sink.Emit(preflightFailureEvent())
	c.sink.Emit(event.Event{Kind: event.StreamAttempt, StreamAttempt: event.StreamAttemptInfo{ID: "sa-1", Action: event.StreamAttemptBegin, Attempt: 1, Max: 3}})
	backdateSilence(c, 2*time.Hour)
	c.warnIfTurnStalled(time.Now())
	waitFor(t, "the abort behind pure stream churn", func() bool { return aborts.Load() == 1 })
}

// A4 终止端到端：看门等待期内用户点停止立即生效，且终态保持“用户中断”
// （Cancelled=true、无 abort 改写）——自动终止与用户停止互不劫持。
func TestUserStopDuringWatchdogWaitStaysInterrupted(t *testing.T) {
	shrinkPreflightStallThreshold(t, 10*time.Minute) // 看门远未到点

	doneEvents := make(chan event.Event, 4)
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			doneEvents <- e
		}
	})})
	t.Cleanup(c.Close)

	started := make(chan struct{})
	c.runGuarded(func(ctx context.Context) error {
		close(started)
		c.sink.Emit(preflightFailureEvent())
		<-ctx.Done()
		return ctx.Err()
	})
	<-started

	// 看门等待期内（静默远小于阈值）：直接驱动一次 tick，确认不 fire。
	c.warnIfTurnStalled(time.Now())
	// 用户点停止（与 Stop 按钮同一入口）。
	c.Cancel()

	var td event.Event
	select {
	case td = <-doneEvents:
	case <-time.After(5 * time.Second):
		t.Fatal("user stop did not end the turn during the watchdog wait")
	}
	if !td.Cancelled {
		t.Fatalf("user stop TurnDone.Cancelled = false, want a plain interruption (event %+v)", td)
	}
	var abortErr *TurnPreflightStallAbortError
	if errors.As(td.Err, &abortErr) {
		t.Fatal("user stop must not be rewritten into the watchdog abort cause")
	}
}

// A3 消息不丢：abort 收束时仍 steer_accepted 的积压项回置 queued+followup
// （对齐投递泵既有被拒 steer 语义），下一轮由泵开轮注入，而不是被 ack 丢掉。
func TestStallAbortRequeuesUnconsumedSteer(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	t.Cleanup(c.Close)
	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "tl1213: queued guidance"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	c.inbox.mu.Lock()
	c.inbox.trackActive(rec.ItemID)
	c.inbox.snapshotCompletion = func() (bool, error) { return true, nil }
	c.inbox.mu.Unlock()

	// 看门在代数 7 的 turn 上 fire：先快照 steer_accepted，再收束。
	c.armSteerRequeue(7)
	// 代数守卫：takeSteerRequeue 对不匹配的代数必须交空集（自愈换轮的陈旧
	// 集合不得污染别的收束）。
	if got := c.takeSteerRequeue(6); got != nil {
		t.Fatalf("generation-mismatched take returned %v, want nil", got)
	}
	// 上面的 take 已按设计清空武装集合；重新武装后再走匹配收束。
	c.armSteerRequeue(7)
	c.onInboxTurnDone(7)
	meta, _, err := st.ReadItem(rec.ItemID)
	if err != nil {
		t.Fatalf("requeued item must stay readable, got err=%v", err)
	}
	if meta.State != sessioninbox.StateQueued || meta.Intent != sessioninbox.IntentFollowup {
		t.Fatalf("requeued item = state %s intent %s, want queued/followup", meta.State, meta.Intent)
	}
}

// A3 对照：未被看门 fire 的正常收束维持既有语义——steer_accepted 项照旧
// ack 出队（flushSteerQueue 的 unapplied 留痕路径不受影响）。
func TestNormalTurnDoneStillAcksAcceptedSteer(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(Options{SessionPath: session, SessionDir: dir, Sink: event.Discard})
	t.Cleanup(c.Close)
	rec, err := c.EnqueueInbox(InboxRequest{Intent: sessioninbox.IntentSteer, Submit: "tl1213: control row"})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.ensureInbox()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(rec.ItemID, sessioninbox.StateSteerAccepted, ""); err != nil {
		t.Fatal(err)
	}
	c.inbox.mu.Lock()
	c.inbox.trackActive(rec.ItemID)
	c.inbox.snapshotCompletion = func() (bool, error) { return true, nil }
	c.inbox.mu.Unlock()

	c.onInboxTurnDone(0)
	if _, _, err := st.ReadItem(rec.ItemID); !errors.Is(err, sessioninbox.ErrNotFound) {
		t.Fatalf("normal close must keep acking accepted steers, got err=%v", err)
	}
}

// 终态改写单元：abort 标记与收束代数不匹配时交 nil；任何 take（匹配与否）
// 都消费掉标记——陈旧标记指向已死的 turn，残留只可能污染后续收束。
func TestTakePreflightStallAbortGenerationGuard(t *testing.T) {
	c := New(Options{Sink: event.Discard})
	t.Cleanup(c.Close)
	armed := &preflightStallAbort{generation: 5, cause: preflightFailureInfo{toolName: "bash", callID: "c1"}, silence: time.Minute}
	c.mu.Lock()
	c.preflightStallAbort = armed
	c.mu.Unlock()

	if got := c.takePreflightStallAbort(&guardedTurnCompletion{generation: 6}); got != nil {
		t.Fatalf("mismatched generation returned %+v, want nil", got)
	}
	c.mu.Lock()
	stillArmed := c.preflightStallAbort != nil
	c.mu.Unlock()
	if stillArmed {
		t.Fatal("a stale marker must be consumed by the mismatched take, not kept")
	}

	c.mu.Lock()
	c.preflightStallAbort = armed
	c.mu.Unlock()
	if got := c.takePreflightStallAbort(&guardedTurnCompletion{generation: 5}); got != armed {
		t.Fatalf("matching take = %+v, want the armed marker", got)
	}
	c.mu.Lock()
	remaining := c.preflightStallAbort != nil
	c.mu.Unlock()
	if remaining {
		t.Fatal("take must consume the marker exactly once")
	}
}

// 看门在压缩进行中绝不强制解缠（任务1147 面：执行器在跑摘要器）。
func TestPreflightStallAbortDeferredWhileCompacting(t *testing.T) {
	shrinkPreflightStallThreshold(t, 80*time.Millisecond)

	aborts := &atomic.Int64{}
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeTurnPreflightStallAbort {
			aborts.Add(1)
		}
	})})
	t.Cleanup(c.Close)
	c.running = true
	c.executor = &agent.Agent{}
	c.executor.CompactionLiveBeginForTest("auto")

	c.sink.Emit(preflightFailureEvent())
	backdateSilence(c, 2*time.Hour)
	c.warnIfTurnStalled(time.Now())
	time.Sleep(50 * time.Millisecond)
	if aborts.Load() != 0 {
		t.Fatalf("compaction window fired %d aborts, want the pass shielded", aborts.Load())
	}
	c.executor.CompactionLiveStopForTest()
	// The shielded tick's own neutral notice re-arms the silence clock (any
	// event does), so re-backdate before judging the post-compaction tick —
	// same discipline as TestStallWhileCompactingUsesNeutralNotice.
	backdateSilence(c, 2*time.Hour)
	c.warnIfTurnStalled(time.Now())
	waitFor(t, "the post-compaction abort", func() bool { return aborts.Load() == 1 })
}
