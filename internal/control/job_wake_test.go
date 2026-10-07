package control

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// 任务553 验收面：父 turn 结束（会话空闲）→ 本会话后台 job 完成 → 自动续轮，
// 模型读到 <background-jobs> 摘要；预算/让位/合并/默认关/日志逐条成立。

// wakeRecordingProvider records every request and replays scripted chunks
// (clamped to the last script), so multiple automatic turns share one script.
type wakeRecordingProvider struct {
	mu       sync.Mutex
	requests []provider.Request
	streams  [][]provider.Chunk
}

func (p *wakeRecordingProvider) Name() string { return "wake-recording" }

func (p *wakeRecordingProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	i := len(p.requests) - 1
	p.mu.Unlock()
	if i >= len(p.streams) {
		i = len(p.streams) - 1
	}
	ch := make(chan provider.Chunk, len(p.streams[i]))
	for _, c := range p.streams[i] {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (p *wakeRecordingProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *wakeRecordingProvider) userTextAt(i int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.requests) {
		return ""
	}
	return lastUserMessage(p.requests[i].Messages)
}

// wakeGateProvider blocks the FIRST stream until the test releases it, so a
// user turn can be held open across a job completion.
type wakeGateProvider struct {
	mu       sync.Mutex
	requests []provider.Request
	entered  chan struct{}
	release  chan struct{}
}

func newWakeGateProvider() *wakeGateProvider {
	return &wakeGateProvider{entered: make(chan struct{}), release: make(chan struct{})}
}

func (p *wakeGateProvider) Name() string { return "wake-gate" }

func (p *wakeGateProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	n := len(p.requests)
	p.mu.Unlock()
	if n == 1 {
		select {
		case <-p.entered:
		default:
			close(p.entered)
		}
		<-p.release
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func (p *wakeGateProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *wakeGateProvider) userTextAt(i int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.requests) {
		return ""
	}
	return lastUserMessage(p.requests[i].Messages)
}

func newWakeTestController(t *testing.T, prov provider.Provider, wake BackgroundJobWakeOptions) (*Controller, *jobs.Manager, string) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(fakeControlTool{name: "read_file"})
	ag := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	jm := jobs.NewManager(event.Discard)
	t.Cleanup(jm.Close)
	path := filepath.Join(t.TempDir(), "wake-session.jsonl")
	c := New(Options{
		Runner:            ag,
		Executor:          ag,
		Jobs:              jm,
		SessionPath:       path,
		BackgroundJobWake: wake,
	})
	t.Cleanup(c.Close)
	// 测试注入短合并窗（生产默认 2s）；状态全部挂 c.mu，这里同包直改。
	c.mu.Lock()
	c.wake.coalesceWindow = 30 * time.Millisecond
	c.mu.Unlock()
	return c, jm, path
}

func startDoneJob(t *testing.T, jm *jobs.Manager, session, label string, body func(context.Context, io.Writer) (string, error)) string {
	t.Helper()
	j := jm.StartForSession(session, "bash", label, body)
	// 等 job goroutine 完全退出（done 在 recordCompletion 之后才关，摘要必然
	// 已入队）——逐 job 确认，避免把前一个 job 的未 drain 摘要当成这个的。
	waitForWakeCondition(t, "job "+label+" to finish", func() bool {
		return !jm.HasUnfinishedForSession(session)
	})
	return j.ID
}

func waitForWakeCondition(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// 验收① 端到端：空闲会话的后台 job 完成 → 自动续轮，模型请求里同时看到
// <background-jobs> 摘要与自动轮标识；转录同样落了这条自动轮用户消息。
func TestBackgroundWakeEndToEndModelSeesSummary(t *testing.T) {
	logs := captureSlog(t)
	prov := &wakeRecordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "handled the background result"},
		{Type: provider.ChunkDone},
	}}}
	c, jm, path := newWakeTestController(t, prov, BackgroundJobWakeOptions{Enabled: true})

	jobID := startDoneJob(t, jm, agent.BranchID(path), "compile", func(_ context.Context, _ io.Writer) (string, error) {
		return "build succeeded", nil
	})

	waitForWakeCondition(t, "the wake turn to reach the provider", func() bool { return prov.count() >= 1 })
	got := prov.userTextAt(0)
	if !strings.Contains(got, "<background-jobs>") {
		t.Fatalf("wake turn input missing <background-jobs> block:\n%s", got)
	}
	if !strings.Contains(got, jobID) || !strings.Contains(got, "compile") ||
		!strings.Contains(got, "done") {
		t.Fatalf("wake turn input missing the completion summary for %s:\n%s", jobID, got)
	}
	if !strings.Contains(got, backgroundWakeTurnPrompt) {
		t.Fatalf("wake turn input missing the automatic-turn marker:\n%s", got)
	}
	// 转录可见（f）：自动轮的用户消息进了会话转录。
	msgs := c.History()
	found := false
	for _, m := range msgs {
		if m.Role == provider.RoleUser && strings.Contains(m.Content, backgroundWakeTurnPrompt) {
			found = true
		}
	}
	if !found {
		t.Fatalf("wake turn user message not found in the transcript (%d messages)", len(msgs))
	}
	if !strings.Contains(logs(), "background job wake admitted") {
		t.Fatalf("wake admission not logged:\n%s", logs())
	}
	// 摘要已随唤醒轮 drain，不残留。
	if jm.HasCompletedNotesForSession(agent.BranchID(path)) {
		t.Fatal("completion summary left queued after the wake turn drained it")
	}
	waitIdle(t, c)
}

// 验收② 防自驱（预算）：窗口预算 1 → 第二次完成只降级为 Notice（摘要留队列
// 等下一个真实 turn），不再开第二轮。
func TestBackgroundWakeBudgetExhaustsToNoticeOnly(t *testing.T) {
	logs := captureSlog(t)
	prov := &wakeRecordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	c, jm, path := newWakeTestController(t, prov, BackgroundJobWakeOptions{
		Enabled:           true,
		MaxTurnsPerWindow: 1,
		ThrottleSeconds:   1,
	})
	session := agent.BranchID(path)

	startDoneJob(t, jm, session, "job-one", func(_ context.Context, _ io.Writer) (string, error) {
		return "first", nil
	})
	waitForWakeCondition(t, "the first wake turn", func() bool { return prov.count() >= 1 })
	waitIdle(t, c) // 第一轮完全收尾，第二发火直达预算闸而非让位路径

	startDoneJob(t, jm, session, "job-two", func(_ context.Context, _ io.Writer) (string, error) {
		return "second", nil
	})
	// 合并窗(30ms)+余量：预算用尽后不再有第二轮。
	time.Sleep(200 * time.Millisecond)
	if got := prov.count(); got != 1 {
		t.Fatalf("provider called %d times after budget exhaustion, want 1 (degraded to Notice-only)", got)
	}
	if !jm.HasCompletedNotesForSession(session) {
		t.Fatal("over-budget summary vanished instead of waiting for the next real turn")
	}
	if !strings.Contains(logs(), "background job wake degraded") ||
		!strings.Contains(logs(), "budget_exhausted") {
		t.Fatalf("budget degradation not logged:\n%s", logs())
	}
	// 降级不丢内容：下一个真实用户轮仍带走摘要（今日行为）。
	c.Submit("continue please")
	waitForWakeCondition(t, "the legacy user turn to drain the summary", func() bool {
		return prov.count() >= 2 && strings.Contains(prov.userTextAt(1), "<background-jobs>")
	})
	waitIdle(t, c)
}

// 验收② 防自驱（节流）：两次完成间隔小于会话级节流 → 第二次降级。
func TestBackgroundWakeThrottlesSecondWake(t *testing.T) {
	logs := captureSlog(t)
	prov := &wakeRecordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	c, jm, path := newWakeTestController(t, prov, BackgroundJobWakeOptions{
		Enabled:           true,
		MaxTurnsPerWindow: 10,
		ThrottleSeconds:   3600,
	})
	session := agent.BranchID(path)

	startDoneJob(t, jm, session, "job-one", func(_ context.Context, _ io.Writer) (string, error) {
		return "first", nil
	})
	waitForWakeCondition(t, "the first wake turn", func() bool { return prov.count() >= 1 })
	waitIdle(t, c) // 第一轮完全收尾，第二发火直达节流闸而非让位路径

	startDoneJob(t, jm, session, "job-two", func(_ context.Context, _ io.Writer) (string, error) {
		return "second", nil
	})
	time.Sleep(200 * time.Millisecond)
	if got := prov.count(); got != 1 {
		t.Fatalf("provider called %d times under throttle, want 1", got)
	}
	if !strings.Contains(logs(), "reason=throttled") {
		t.Fatalf("throttle degradation not logged:\n%s", logs())
	}
	if !jm.HasCompletedNotesForSession(session) {
		t.Fatal("throttled summary vanished instead of waiting for the next real turn")
	}
	waitIdle(t, c)
}

// 验收③ 让位：用户 turn 在途时唤醒让位（不抢占），turn 结束后 finish 钩子
// 重踢完成续轮；用户输入与完成摘要都不被吞。
func TestBackgroundWakeYieldsToRunningTurnThenKicksAfterFinish(t *testing.T) {
	logs := captureSlog(t)
	prov := newWakeGateProvider()
	c, jm, path := newWakeTestController(t, prov, BackgroundJobWakeOptions{Enabled: true})
	session := agent.BranchID(path)

	go c.Submit("long user task")
	<-prov.entered // 用户 turn 已在模型流中（compose 已完成）

	startDoneJob(t, jm, session, "during-turn", func(_ context.Context, _ io.Writer) (string, error) {
		return "finished while the user turn ran", nil
	})
	// 合并窗(30ms)+余量：让位路径必须只留日志、不开轮。
	time.Sleep(200 * time.Millisecond)
	if got := prov.count(); got != 1 {
		t.Fatalf("wake turn preempted the running user turn (%d requests), want 1", got)
	}
	if !strings.Contains(logs(), "background job wake yielded") ||
		!strings.Contains(logs(), "reason=turn_running") {
		t.Fatalf("yield not logged:\n%s", logs())
	}

	// 释放用户 turn → gate 重开 → finish 钩子重踢 → 续轮带走摘要。
	close(prov.release)
	waitForWakeCondition(t, "the post-finish wake turn", func() bool { return prov.count() >= 2 })
	if got := prov.userTextAt(0); !strings.Contains(got, "long user task") {
		t.Fatalf("user turn input corrupted:\n%s", got)
	}
	if got := prov.userTextAt(1); !strings.Contains(got, "<background-jobs>") ||
		!strings.Contains(got, "during-turn") || !strings.Contains(got, "done") {
		t.Fatalf("wake turn after finish missing the queued summary:\n%s", got)
	}
	waitIdle(t, c)
}

// 验收④ 合并：窗口内两个 job 完成 → 只产生一轮唤醒，摘要同行带上两者。
func TestBackgroundWakeMergesCompletionsIntoOneTurn(t *testing.T) {
	prov := &wakeRecordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	c, jm, path := newWakeTestController(t, prov, BackgroundJobWakeOptions{Enabled: true})
	session := agent.BranchID(path)

	startDoneJob(t, jm, session, "job-a", func(_ context.Context, _ io.Writer) (string, error) {
		return "result a", nil
	})
	time.Sleep(5 * time.Millisecond)
	startDoneJob(t, jm, session, "job-b", func(_ context.Context, _ io.Writer) (string, error) {
		return "result b", nil
	})

	waitForWakeCondition(t, "the merged wake turn", func() bool { return prov.count() >= 1 })
	time.Sleep(200 * time.Millisecond) // 留给任何（不该有的）第二轮
	if got := prov.count(); got != 1 {
		t.Fatalf("%d wake turns for two completions inside one window, want 1", got)
	}
	got := prov.userTextAt(0)
	if !strings.Contains(got, "job-a") || !strings.Contains(got, "job-b") {
		t.Fatalf("merged wake turn missing one of the two summaries:\n%s", got)
	}
	waitIdle(t, c)
}

// 验收⑤ 默认关：开关关闭时完成不触发任何轮（行为与今日完全等价），
// 摘要仍由下一个真实用户轮照常携带。
func TestBackgroundWakeDisabledKeepsLegacyBehavior(t *testing.T) {
	prov := &wakeRecordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	// 不设 BackgroundJobWake（零值 = 关）。
	c, jm, path := newWakeTestController(t, prov, BackgroundJobWakeOptions{})
	session := agent.BranchID(path)

	startDoneJob(t, jm, session, "quiet", func(_ context.Context, _ io.Writer) (string, error) {
		return "done quietly", nil
	})
	time.Sleep(250 * time.Millisecond) // 合并窗的数十倍：任何误唤醒都会现形
	if got := prov.count(); got != 0 {
		t.Fatalf("disabled wake still started %d turn(s)", got)
	}
	if !jm.HasCompletedNotesForSession(session) {
		t.Fatal("disabled wake lost the completion summary")
	}
	// 今日行为：摘要搭下一个用户轮。
	c.Submit("what happened?")
	waitForWakeCondition(t, "the legacy user turn", func() bool { return prov.count() >= 1 })
	if got := prov.userTextAt(0); !strings.Contains(got, "<background-jobs>") ||
		!strings.Contains(got, "quiet") {
		t.Fatalf("legacy drain lost the summary:\n%s", got)
	}
	waitIdle(t, c)
}

// 边界 a：跨会话隔离——共享 Manager 下，A 会话的完成只唤醒 A。
func TestBackgroundWakeCrossSessionIsolation(t *testing.T) {
	reg := tool.NewRegistry()
	jm := jobs.NewManager(event.Discard)
	t.Cleanup(jm.Close)

	newProv := func() *wakeRecordingProvider {
		return &wakeRecordingProvider{streams: [][]provider.Chunk{{
			{Type: provider.ChunkText, Text: "ok"},
			{Type: provider.ChunkDone},
		}}}
	}
	provA, provB := newProv(), newProv()
	agA := agent.New(provA, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	agB := agent.New(provB, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	pathA := filepath.Join(t.TempDir(), "a.jsonl")
	pathB := filepath.Join(t.TempDir(), "b.jsonl")
	ctrlA := New(Options{Runner: agA, Executor: agA, Jobs: jm, SessionPath: pathA,
		BackgroundJobWake: BackgroundJobWakeOptions{Enabled: true}})
	ctrlB := New(Options{Runner: agB, Executor: agB, Jobs: jm, SessionPath: pathB,
		BackgroundJobWake: BackgroundJobWakeOptions{Enabled: true}})
	t.Cleanup(ctrlA.Close)
	t.Cleanup(ctrlB.Close)
	ctrlA.mu.Lock()
	ctrlA.wake.coalesceWindow = 30 * time.Millisecond
	ctrlA.mu.Unlock()
	ctrlB.mu.Lock()
	ctrlB.wake.coalesceWindow = 30 * time.Millisecond
	ctrlB.mu.Unlock()

	startDoneJob(t, jm, agent.BranchID(pathA), "owned-by-a", func(_ context.Context, _ io.Writer) (string, error) {
		return "a result", nil
	})
	waitForWakeCondition(t, "session A's wake turn", func() bool { return provA.count() >= 1 })
	time.Sleep(100 * time.Millisecond)
	if got := provB.count(); got != 0 {
		t.Fatalf("session B woke %d time(s) for session A's job", got)
	}
	waitIdle(t, ctrlA)
}

// 生命周期：Close 反注册观察者——关闭后其他会话仍能继续用共享 Manager，
// 已关控制器不再被唤醒。
func TestBackgroundWakeCloseDetachesObserver(t *testing.T) {
	prov := &wakeRecordingProvider{streams: [][]provider.Chunk{{
		{Type: provider.ChunkText, Text: "ok"},
		{Type: provider.ChunkDone},
	}}}
	c, jm, path := newWakeTestController(t, prov, BackgroundJobWakeOptions{Enabled: true})
	session := agent.BranchID(path)
	c.Close()

	startDoneJob(t, jm, session, "after-close", func(_ context.Context, _ io.Writer) (string, error) {
		return "too late", nil
	})
	time.Sleep(150 * time.Millisecond)
	if got := prov.count(); got != 0 {
		t.Fatalf("closed controller woke %d time(s) after Close", got)
	}
	if c.Running() {
		t.Fatal("closed controller resumed running")
	}
}
