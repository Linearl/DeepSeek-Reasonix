package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioninbox"
)

// 任务 731 验收映射（desktop 侧异常态自动续轮）：
// ① 构造异常态（回合异常终止 / 579 桥开轮预算用尽）实测自动续轮生效；
// ② 连续续轮有界：3 次后封存并告警，不循环；
// ③ 误判防护：干净完成 / 用户停轮 / 契约型结局 / 墙钟 / 477 终停 / 用户
//    暂停 / 准入竞态都不触发；宽限窗口内的回合启动撤销待发续轮；
// ④ 开关关闭时零注入零留痕（行为与现状一致）。
// 外加：S2 升级直通（无宽限）、并发 fire 认领去重、nil 看门狗安全。

// resumeLog collects submit/notify invocations from timer goroutines without
// data races (the 579 runLog pattern).
type resumeLog struct {
	mu    sync.Mutex
	items []string
}

func (l *resumeLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, s)
}

func (l *resumeLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

func (l *resumeLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.items)
}

type resumeHarness struct {
	w       *autopilotResumeWatchdog
	a       *App
	tab     *WorkspaceTab
	submits *resumeLog
	notices *resumeLog
}

func newResumeHarness(t *testing.T, autopilot bool) *resumeHarness {
	t.Helper()
	tab := &WorkspaceTab{
		ID:          "resume_tab",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	tab.autopilot = autopilot
	a := &App{
		tabs:        map[string]*WorkspaceTab{tab.ID: tab},
		activeTabID: tab.ID,
	}
	w := newAutopilotResumeWatchdog(a)
	w.delay = time.Millisecond
	h := &resumeHarness{w: w, a: a, tab: tab, submits: &resumeLog{}, notices: &resumeLog{}}
	w.submit = func(tabID, prompt string) error {
		if prompt != autopilotAbnormalResumePrompt {
			t.Errorf("resume submit carried an unexpected prompt: %q", prompt)
		}
		h.submits.add(tabID)
		return nil
	}
	w.notify = func(tabID string, level event.Level, code, text string) {
		h.notices.add(code)
	}
	a.autopilotResume = w
	return h
}

// waitResumeSubmits blocks until want submits landed (or the deadline passes)
// and returns what landed.
func waitResumeSubmits(t *testing.T, h *resumeHarness, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := h.submits.count(); got >= want {
			return h.submits.snapshot()
		}
		time.Sleep(5 * time.Millisecond)
	}
	return h.submits.snapshot()
}

// waitNoMoreSubmits gives the (1ms) grace window ample time to elapse and
// fails if anything fired.
func waitNoMoreSubmits(t *testing.T, h *resumeHarness) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	if got := h.submits.snapshot(); len(got) != 0 {
		t.Fatalf("expected zero resume submissions, got %v", got)
	}
}

// waitNoNewSubmits asserts the submit count stays at before across the grace
// window (for harnesses that already carry earlier submissions).
func waitNoNewSubmits(t *testing.T, h *resumeHarness, before int) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	if got := h.submits.count(); got != before {
		t.Fatalf("expected no new submissions beyond %d, got %d", before, got)
	}
}

func abnormalStopEvent() event.Event {
	return event.Event{Kind: event.TurnDone, Err: errors.New("provider stream died mid-turn")}
}

// ①+③ 异常终止触发一轮续轮；干净完成不触发并复位预算。
func TestAutopilotResumeFiresOnAbnormalStop(t *testing.T) {
	h := newResumeHarness(t, true)

	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	waitResumeSubmits(t, h, 1)
	if got := h.notices.count(); got != 1 {
		t.Fatalf("expected one fired notice, got %d (%v)", got, h.notices.snapshot())
	}
	if code := h.notices.snapshot()[0]; code != noticeCodeAutopilotResumeFired {
		t.Fatalf("expected fired notice code, got %q", code)
	}

	// 干净完成：复位链条，不再注入。
	h.w.observeTurnDone(h.tab.ID, event.Event{Kind: event.TurnDone})
	h.w.observeTurnDone(h.tab.ID, event.Event{Kind: event.TurnDone, Err: errors.New("x")})
	waitResumeSubmits(t, h, 2)

	// 普通 idle（无事件）零新增注入。
	waitNoNewSubmits(t, h, 2)
}

// ③ 全排除矩阵：别人的契约原样保留，一个都不续。
func TestAutopilotResumeExclusions(t *testing.T) {
	cases := map[string]event.Event{
		"clean_completion":  {Kind: event.TurnDone},
		"user_cancelled":    {Kind: event.TurnDone, Err: errors.New("x"), Cancelled: true},
		"readiness_outcome": {Kind: event.TurnDone, Err: errors.New("x"), Outcome: event.TurnOutcomeFinalReadiness},
		"wall_clock":        {Kind: event.TurnDone, Err: context.DeadlineExceeded},
		"user_ctx_cancel":   {Kind: event.TurnDone, Err: context.Canceled},
		"ask_477_terminal":  {Kind: event.TurnDone, Err: control.ErrAutopilotAskUnanswered},
		"inbox_user_paused": {Kind: event.TurnDone, Err: sessioninbox.ErrPaused},
		"admission_race":    {Kind: event.TurnDone, Err: control.ErrTurnRunning},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			h := newResumeHarness(t, true)
			h.w.observeTurnDone(h.tab.ID, e)
			waitNoMoreSubmits(t, h)
		})
	}
}

// ② 连续续轮有界：3 次后封存+告警，第 4/5 次异常不再注入。
func TestAutopilotResumeBudgetSealedAfterThree(t *testing.T) {
	h := newResumeHarness(t, true)

	for i := 1; i <= 3; i++ {
		h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
		waitResumeSubmits(t, h, i)
	}
	// 第 4 次异常终止：预算用尽 → 封存 + 告警，不注入。
	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	exhaustedArrived := func() bool {
		for _, code := range h.notices.snapshot() {
			if code == noticeCodeAutopilotResumeExhausted {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !exhaustedArrived() {
		time.Sleep(5 * time.Millisecond)
	}
	if got := h.submits.count(); got != 3 {
		t.Fatalf("expected exactly 3 resume submissions, got %d", got)
	}
	if got := h.notices.snapshot(); len(got) != 4 {
		t.Fatalf("expected 3 fired + 1 exhausted notices, got %v", got)
	}
	// 封存后继续异常：仍然零注入（不循环）。
	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	time.Sleep(100 * time.Millisecond)
	if got := h.submits.count(); got != 3 {
		t.Fatalf("sealed tab resumed again: %d submissions", got)
	}
	if got := h.notices.count(); got != 4 {
		t.Fatalf("exhausted notice should fire once, got %v", h.notices.snapshot())
	}
}

// 干净完成复位预算：封存前链条断裂后重新计次。
func TestAutopilotResumeCleanCompletionResetsBudget(t *testing.T) {
	h := newResumeHarness(t, true)

	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	waitResumeSubmits(t, h, 1)
	h.w.observeTurnDone(h.tab.ID, event.Event{Kind: event.TurnDone}) // clean
	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	waitResumeSubmits(t, h, 2)

	// 用户停轮=接管：撤销待发续轮并复位。
	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	h.w.observeTurnDone(h.tab.ID, event.Event{Kind: event.TurnDone, Err: errors.New("x"), Cancelled: true})
	time.Sleep(100 * time.Millisecond)
	if got := h.submits.count(); got != 2 {
		t.Fatalf("user cancellation must suppress the pending resume: %d submissions", got)
	}
}

// ④ 开关关闭：零注入零告警。
func TestAutopilotResumeSwitchOffNoop(t *testing.T) {
	h := newResumeHarness(t, false)

	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	time.Sleep(100 * time.Millisecond)
	waitNoMoreSubmits(t, h)
	if got := h.notices.count(); got != 0 {
		t.Fatalf("off-autopilot tab must stay silent: %v", h.notices.snapshot())
	}
}

// ③ 宽限窗口内任何回合启动（用户跟进）撤销待发续轮。
func TestAutopilotResumeTurnStartedCancelsPending(t *testing.T) {
	h := newResumeHarness(t, true)

	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	h.w.observeTurnStarted(h.tab.ID)
	waitNoMoreSubmits(t, h)
}

// 同一异常链重复调度不叠加（pending 去重）。
func TestAutopilotResumeScheduleDeduplicates(t *testing.T) {
	h := newResumeHarness(t, true)

	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	h.w.observeTurnDone(h.tab.ID, abnormalStopEvent())
	waitResumeSubmits(t, h, 1)
	time.Sleep(100 * time.Millisecond)
	if got := h.submits.count(); got != 1 {
		t.Fatalf("pending schedule must not duplicate: %d submissions", got)
	}
}

// S2：579 桥预算用尽升级直通（无宽限窗口），共享同一有界预算。
func TestAutopilotResumeEscalateInboxStuck(t *testing.T) {
	h := newResumeHarness(t, true)

	h.w.escalateInboxStuck(h.tab.ID, 3, errors.New("open failed"))
	waitResumeSubmits(t, h, 1)

	// 升级同样受预算约束：用尽后封存。
	for i := 0; i < 5; i++ {
		h.w.escalateInboxStuck(h.tab.ID, 3, errors.New("open failed"))
	}
	waitResumeSubmits(t, h, 3)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.notices.count() < 4 {
		time.Sleep(5 * time.Millisecond)
	}
	exhausted := 0
	for _, code := range h.notices.snapshot() {
		if code == noticeCodeAutopilotResumeExhausted {
			exhausted++
		}
	}
	if exhausted != 1 {
		t.Fatalf("expected exactly one exhausted notice after escalations, got %d (%v)", exhausted, h.notices.snapshot())
	}
	if got := h.submits.count(); got != 3 {
		t.Fatalf("escalations must respect the same bounded budget: %d", got)
	}
	// 封存后继续升级：零注入零新告警。
	h.w.escalateInboxStuck(h.tab.ID, 3, errors.New("open failed"))
	time.Sleep(100 * time.Millisecond)
	if got := h.submits.count(); got != 3 {
		t.Fatalf("sealed tab escalated again: %d submissions", got)
	}
	if got := h.notices.count(); got != 4 {
		t.Fatalf("sealed tab kept notifying: %v", h.notices.snapshot())
	}
}

// App 层 S2 适配器：开关关闭/看门狗未挂时零动作。
func TestEscalateStuckInboxForTabGates(t *testing.T) {
	h := newResumeHarness(t, false)
	h.a.escalateStuckInboxForTab(h.tab.ID, 3, errors.New("open failed"))
	waitNoMoreSubmits(t, h)

	// nil 看门狗（早于 startup / 测试裸 App）安全。
	bare := &App{tabs: map[string]*WorkspaceTab{h.tab.ID: h.tab}, activeTabID: h.tab.ID}
	bare.escalateStuckInboxForTab(h.tab.ID, 3, errors.New("x"))
	bare.observeAutopilotResumeTurnDone(h.tab.ID, abnormalStopEvent())
	bare.observeAutopilotResumeTurnStarted(h.tab.ID)
}

// 并发 fire 认领去重：一次 fire 在飞时（submit 未返回），其余 fire 不得
// 叠加注入——阻塞 stub 撑开竞态窗口后计数。
func TestAutopilotResumeConcurrentFireClaimsOnce(t *testing.T) {
	h := newResumeHarness(t, true)
	release := make(chan struct{})
	var inflight sync.WaitGroup
	inflight.Add(1)
	h.w.submit = func(tabID, prompt string) error {
		h.submits.add(tabID)
		inflight.Done() // 第一次注入已落账，撑住窗口
		<-release
		return nil
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			h.w.fire(h.tab.ID, "concurrent-probe")
		})
	}
	inflight.Wait() // 恰一次提交已发生且尚未返回
	time.Sleep(50 * time.Millisecond)
	if got := h.submits.count(); got != 1 {
		t.Fatalf("in-flight fire must exclude the others, got %d", got)
	}
	close(release)
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	if got := h.submits.count(); got != 1 {
		t.Fatalf("late claimants must not inject after the claim released, got %d", got)
	}
}

// 分类函数纯表：正类 + 全排除项。
func TestAutopilotAbnormalStopClassification(t *testing.T) {
	if !autopilotAbnormalStop(abnormalStopEvent()) {
		t.Fatal("a plain failed terminal turn must classify as abnormal")
	}
	excluded := []event.Event{
		{Kind: event.TurnDone},
		{Kind: event.TurnDone, Err: errors.New("x"), Cancelled: true},
		{Kind: event.TurnDone, Err: errors.New("x"), Outcome: event.TurnOutcomeIncompleteRead},
		{Kind: event.TurnDone, Err: errors.New("x"), Outcome: event.TurnOutcomeRecoveryPaused},
		{Kind: event.TurnDone, Err: errors.New("x"), Outcome: event.TurnOutcomeCompletionUncertain},
		{Kind: event.TurnDone, Err: context.Canceled},
		{Kind: event.TurnDone, Err: context.DeadlineExceeded},
		{Kind: event.TurnDone, Err: control.ErrAutopilotAskUnanswered},
		{Kind: event.TurnDone, Err: sessioninbox.ErrPaused},
		{Kind: event.TurnDone, Err: control.ErrTurnRunning},
	}
	for i, e := range excluded {
		if autopilotAbnormalStop(e) {
			t.Fatalf("case %d must be excluded from abnormal classification", i)
		}
	}
}

// S1 tap 接线：tabEventSink.Emit 上的 TurnDone 进得来看门狗（轻集成）。
func TestSinkEmitFeedsAutopilotResumeWatchdog(t *testing.T) {
	h := newResumeHarness(t, true)
	h.tab.sink = &tabEventSink{tabID: h.tab.ID, app: h.a}

	h.tab.sink.Emit(abnormalStopEvent())
	got := waitResumeSubmits(t, h, 1)
	if len(got) != 1 || !strings.Contains(autopilotAbnormalResumePrompt, "Continue the interrupted work") {
		t.Fatalf("sink tap failed to feed the watchdog: %v", got)
	}
}
