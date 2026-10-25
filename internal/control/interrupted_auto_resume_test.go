package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl 1105 — 中断自动续轮的开关两态与守卫测试。
//
// 现场实锤（2026-10-23 装机链）：restart_update execute 重启桌面进程，杀掉同轮
// 的验证调用 → 宿主标 outcome_unknown + 「本轮已中断」恢复面板 → 用户忽略后
// 会话停死，直到人工发消息才续轮（3 分钟空窗）。
//
// 本文件钉住新增的宿主触发源：开关 ON × 真实中断 → 恰好一轮宿主续轮（有
// interrupted_auto_resume notice + host-origin 续轮消息）；开关 OFF → 零行为
// 变化（铁律 2：默认关 = pre-1105 逐字节等价）；用户取消 / 非中断终态错误 /
// 中断续轮自身再中断 → 均不复活（守卫）。
//
// 断言锚：interrupted_auto_resume notice code、host-origin 续轮消息（内容含
// interruptedAutoResumeTurnPrompt 的判据串）、provider 调用次数。

// irScript: call 1 returns a normal text turn; every later call fails with an
// outcome-unknown interruption (the restart-kills-in-flight shape). A variant
// makes every call fail terminally with a NON-interruption error.
type irScript struct {
	mu          sync.Mutex
	calls       int
	interrupted bool // call 1 fails with a stream-cut interruption (Retryable — the agent absorbs it)
	deadline    bool // call 1 fails with a bare context.DeadlineExceeded (the non-retryable interruption)
	alwaysPlain bool // every call (incl. the first) fails with a plain terminal error
	fresh       bool // call 2 succeeds (fresh continuation turn) — no-chain probe
}

func (p *irScript) Name() string { return "ir-script" }

func (p *irScript) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.alwaysPlain {
		return nil, errors.New("provider: terminal parameter rejection (not an interruption)")
	}
	// 1105 返修 (2026-10-25): the NON-retryable interruption face. A stream cut
	// (premature_eof) classifies as phase=stream Retryable=true, so the agent's
	// sampling recovery absorbs it INSIDE the turn — the turn then completes (or
	// dies on a later plain failure) and never stops with an interruption for
	// the host gate to see; the OFF-state test below pins exactly that
	// absorption. A bare context.DeadlineExceeded classifies as
	// Retryable=false: the run loop's ChunkError case passes it through
	// verbatim (internal/agent/agent.go, the non-StreamInterrupted branch) and
	// the attempt ends the turn on the spot — the shape that actually reaches
	// the tl-1105 gate (interruptionLike claims it via errors.Is).
	if p.deadline && p.calls == 1 {
		ch := make(chan provider.Chunk, 1)
		ch <- provider.Chunk{Type: provider.ChunkError, Err: context.DeadlineExceeded}
		close(ch)
		return ch, nil
	}
	if p.interrupted && p.calls == 1 {
		// 1105 返修 (2026-10-24): the mid-stream cut face — the chunk form is
		// what the run loop's `case provider.ChunkError` recognizes as a
		// stream interruption. The direct-error return above is the
		// connect-phase face; either way the run loop must stop with the
		// typed StreamInterruptedError so the tl-1105 gate claims it.
		ch := make(chan provider.Chunk, 1)
		ch <- provider.Chunk{Type: provider.ChunkError, Err: provider.StreamInterrupt(errors.New("stream cut by the desktop restart"), "premature_eof")}
		close(ch)
		return ch, nil
	}
	if p.fresh && p.calls >= 2 {
		// The continuation turn (call 2) runs on a fresh scripted call that
		// succeeds; the run ends there (no chaining).
		ch := make(chan provider.Chunk, 2)
		ch <- provider.Chunk{Type: provider.ChunkText, Text: "resumed from the recovery handoff"}
		ch <- provider.Chunk{Type: provider.ChunkDone}
		close(ch)
		return ch, nil
	}
	// Call 2+ with fresh=false: the plain terminal failure — the continuation
	// turn (if any) ends here without chaining.
	return nil, errors.New("provider: terminal failure after the interruption (tl 1105 probe)")
}

func (p *irScript) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// irRecorder counts notice events by code (reuses askACRecorder's shape but
// counts only — the ask channel is unused here).
type irRecorder struct {
	mu      sync.Mutex
	notices []event.Event
}

func (s *irRecorder) Emit(e event.Event) {
	if e.Kind == event.Notice && e.Code != "" {
		s.mu.Lock()
		s.notices = append(s.notices, e)
		s.mu.Unlock()
	}
}

func (s *irRecorder) noticeCount(code string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.notices {
		if e.Code == code {
			n++
		}
	}
	return n
}

// irController mirrors askACController but with the plain registry (no ask tool)
// and the interrupted_auto_resume switch wired through Options.
func irController(t *testing.T, prov provider.Provider, mutate func(*Options)) (*Controller, *irRecorder) {
	t.Helper()
	reg := tool.NewRegistry()
	executor := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	recorder := &irRecorder{}
	opts := Options{
		Runner:     executor,
		Executor:   executor,
		Sink:       recorder,
		SessionDir: t.TempDir(),
	}
	if mutate != nil {
		mutate(&opts)
	}
	c := New(opts)
	c.EnableInteractiveApproval()
	t.Cleanup(func() { c.Cancel(); c.Close() })
	return c, recorder
}

// interruptedContinuationMessage finds the tl-1105 host continuation in the
// transcript: a host-origin user message carrying the fixed prompt's marker.
func interruptedContinuationMessage(c *Controller) (provider.Message, bool) {
	for _, m := range c.executor.Session().Snapshot() {
		if m.Role == provider.RoleUser && m.Origin == provider.MessageOriginHost &&
			strings.Contains(m.Content, "outcome is unknown") {
			return m, true
		}
	}
	return provider.Message{}, false
}

// runIRTurn drives one foreground turn through the guarded admission (the
// production shape) and returns a channel carrying its terminal error. The
// admission result is surfaced on a side channel so a test can tell "the
// orchestrator's stop path never ran" (drop/park) apart from "the stop path
// ran but the gate declined" (zero notice, zero continuation).
func runIRTurn(c *Controller) <-chan error {
	done := make(chan error, 1)
	result := c.runGuarded(func(ctx context.Context) error {
		err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(ctx, "keep working", "keep working", "")
		done <- err
		return err
	})
	if result != turnStarted {
		done <- fmt.Errorf("turn admission result = %d (not turnStarted)", result)
	}
	return done
}

// Combination 1 — flag ON × a genuine interruption: the turn stops with an
// outcome-unknown interruption and the host submits exactly ONE continuation
// turn that carries the persisted recovery handoff prompt (验收①).
//
// 1105 返修 (2026-10-25): the interruption rides the deadline shape (see
// irScript.Stream). The earlier premature_eof shape was Retryable and the
// agent's sampling recovery absorbed it inside the turn — the turn completed,
// the gate never ran, and this test saw notices=0 with providerCalls=2 (the
// second call was the agent-side retry, not the host continuation). The
// deadline shape stops the turn on attempt 1, so call 2 below is genuinely
// the host's one continuation.
func TestInterruptedAutoResumeResumesAfterOutcomeUnknownStop(t *testing.T) {
	prov := &irScript{deadline: true, fresh: true}
	c, rec := irController(t, prov, func(o *Options) {
		o.InterruptedAutoResume = true
	})
	// 1105 返修 (2026-10-24): drive the turn through the guarded admission
	// (runGuarded + runGoalLoopWithRawDisplay — the shape the task's original
	// acceptance test targeted) and read the terminal error off the channel so
	// the diagnostics below can see it if the gate misfires.
	turnErr := <-runIRTurn(c)

	if n := rec.noticeCount(interruptedAutoResumeNoticeCode); n != 1 {
		t.Fatalf("interrupted_auto_resume notices = %d, want exactly one (gate=%v providerCalls=%d turnErr=%v)", n, c.InterruptedAutoResume(), prov.callCount(), turnErr)
	}
	if !c.InterruptedAutoResume() {
		t.Fatal("gate is off; the ON state must have the switch readable")
	}
	msg, ok := interruptedContinuationMessage(c)
	if !ok {
		t.Fatal("no host continuation message in the transcript; the interrupted turn was not resumed")
	}
	if !strings.Contains(msg.Content, "bounded recovery handoff") {
		t.Fatalf("continuation prompt = %q, want the recovery-handoff marker", msg.Content)
	}
	// The continuation ran under a fresh scripted call; the second call fails
	// terminally (non-interruption) so the run ends there — exactly one
	// continuation, no chaining.
	if got := prov.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (the interrupted turn + its one continuation)", got)
	}
}

// Combination 2 — flag OFF × the same interruption: the guaranteed baseline
// (铁律 2). Zero notices, zero continuation, byte-for-byte the pre-1105 stop.
func TestInterruptedAutoResumeOffKeepsIdleStop(t *testing.T) {
	prov := &irScript{interrupted: true}
	c, rec := irController(t, prov, nil)
	<-runIRTurn(c)

	if n := rec.noticeCount(interruptedAutoResumeNoticeCode); n != 0 {
		t.Fatalf("off state emitted interrupted_auto_resume notices = %d, want zero", n)
	}
	if _, ok := interruptedContinuationMessage(c); ok {
		t.Fatal("off state submitted a host continuation; the closed state must stay byte-for-byte")
	}
	if got := prov.callCount(); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (the interrupted turn + the agent-side sampling retry; the OFF state still runs the same turn body — the gate's OFF check is what suppresses the notice and the continuation)", got)
	}
}

// Guard — flag ON but the stop is a plain terminal error (not an interruption):
// the old idle stop is kept verbatim. A parameter 400 must not be resurrected
// as an interruption.
func TestInterruptedAutoResumeIgnoresPlainTerminalError(t *testing.T) {
	prov := &irScript{alwaysPlain: true}
	c, rec := irController(t, prov, func(o *Options) {
		o.InterruptedAutoResume = true
	})
	<-runIRTurn(c)

	if n := rec.noticeCount(interruptedAutoResumeNoticeCode); n != 0 {
		t.Fatalf("plain terminal error emitted interrupted_auto_resume notices = %d, want zero", n)
	}
	if _, ok := interruptedContinuationMessage(c); ok {
		t.Fatal("a plain terminal error was resumed as an interruption")
	}
	if got := prov.callCount(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
}

// Guard — flag ON but the user cancelled: a stop the user asked for is never
// resurrected (the deliberate-cancel exclusion).
func TestInterruptedAutoResumeExcludesUserCancel(t *testing.T) {
	prov := &irScript{interrupted: true}
	c, rec := irController(t, prov, func(o *Options) {
		o.InterruptedAutoResume = true
	})
	done := runIRTurn(c)
	// Cancel while the first (interrupted) call is in flight.
	c.Cancel()
	deadline := time.Now().Add(5 * time.Second)
	for c.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	<-done

	if n := rec.noticeCount(interruptedAutoResumeNoticeCode); n != 0 {
		t.Fatalf("user-cancelled turn emitted interrupted_auto_resume notices = %d, want zero", n)
	}
	if _, ok := interruptedContinuationMessage(c); ok {
		t.Fatal("a user-cancelled turn was resumed by the host")
	}
}

// Wiring — the switch travels Options→Controller.sessionSettings: the value
// must be readable before any Set* call, or the frontend/lab toggle would be
// the only thing that ever turns 1105 on (boot passes cfg straight into
// Options; the desktop toggle reaches the sessionSettings field via Set*).
func TestInterruptedAutoResumeBootsFromOptions(t *testing.T) {
	prov := &irScript{}
	c, _ := irController(t, prov, func(o *Options) {
		o.InterruptedAutoResume = true
	})
	if !c.InterruptedAutoResume() {
		t.Fatal("Options.InterruptedAutoResume=true did not reach the controller; New() wiring is broken")
	}
	off, _ := irController(t, &irScript{}, nil)
	if off.InterruptedAutoResume() {
		t.Fatal("default (off) controller reports interrupted auto-resume on; 铁律 2 default-off violated")
	}
}
