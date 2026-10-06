package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// Task 544 — ask 答复后自动续跑的四组合与守卫测试。
//
// 场景形态即现场实锤：第 1 个模型回合发起 ask 并拿到答复，续跑的模型回合
// 被 provider 终态错误打死（script 第 2 步起恒错）。开关两态 ×（人工答复 /
// 超时自动答复）各钉一条，另加 477 关态与用户取消两条守卫。
// 断言锚：ask_auto_continue notice（有/无）、宿主续跑 user 消息（有/无）、
// 标记一次性消费。

// askACScript serves the ask turn first; every later call fails terminally —
// the shape of "the turn died right after the answer was recorded".
type askACScript struct {
	mu    sync.Mutex
	first []provider.Chunk
	calls int
}

func (p *askACScript) Name() string { return "ask-ac-script" }

func (p *askACScript) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		ch := make(chan provider.Chunk, len(p.first))
		for _, c := range p.first {
			ch <- c
		}
		close(ch)
		return ch, nil
	}
	return nil, errors.New("provider: terminal failure after the answered ask (task 544 probe)")
}

func (p *askACScript) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type askACRecorder struct {
	mu      sync.Mutex
	asks    chan string
	notices []event.Event
}

func (s *askACRecorder) Emit(e event.Event) {
	if e.Kind == event.AskRequest {
		select {
		case s.asks <- e.Ask.ID:
		default:
		}
		return
	}
	if e.Kind == event.Notice && e.Code != "" {
		s.mu.Lock()
		s.notices = append(s.notices, e)
		s.mu.Unlock()
	}
}

func (s *askACRecorder) waitAsk(t *testing.T) string {
	t.Helper()
	select {
	case id := <-s.asks:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("ask request never arrived")
		return ""
	}
}

func (s *askACRecorder) noticeCount(code string) int {
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

// askACController builds a controller whose executor carries the real ask tool
// and whose first model turn asks one question.
func askACController(t *testing.T, prov *askACScript, question string, mutate func(*Options)) (*Controller, *askACRecorder) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(agent.NewAskTool())
	executor := agent.New(prov, reg, agent.NewSession("sys"), agent.Options{}, event.Discard)
	recorder := &askACRecorder{asks: make(chan string, 4)}
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

// askACAskTurn builds the first model round: one high-risk ask tool call.
func askACAskTurn(question string) []provider.Chunk {
	args, err := json.Marshal(map[string]any{
		"questions": []map[string]any{{
			"header":   "Scope",
			"question": question,
			"options":  []map[string]string{{"label": "delete it"}, {"label": "keep it"}},
		}},
	})
	if err != nil {
		panic(err)
	}
	return []provider.Chunk{
		toolCallChunk("ask-1", tool.HostAsk, string(args)),
		{Type: provider.ChunkDone},
	}
}

// hostContinuationMessage finds the task-544 host continuation user message in
// the transcript (host origin + the prompt's do-not-re-ask marker).
func hostContinuationMessage(c *Controller) (provider.Message, bool) {
	for _, m := range c.executor.Session().Snapshot() {
		if m.Role == provider.RoleUser && m.Origin == provider.MessageOriginHost &&
			strings.Contains(m.Content, "Do not re-ask the answered questions") {
			return m, true
		}
	}
	return provider.Message{}, false
}

// runAskACTurn drives one foreground turn through the guarded admission (the
// production shape, so the turn ctx is the controller's cancellable one) and
// returns a channel carrying its terminal error.
func runAskACTurn(c *Controller) <-chan error {
	done := make(chan error, 1)
	c.runGuarded(func(ctx context.Context) error {
		err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(ctx, "decide the scope, then finish", "decide the scope, then finish", "")
		done <- err
		return err
	})
	return done
}

// Combination 1 — flag ON × human answer: the terminal stop right after the
// answer is resumed by exactly one host continuation turn that carries the
// recorded decision (验收①).
func TestAskAutoContinueResumesAfterHumanAnswer(t *testing.T) {
	prov := &askACScript{first: askACAskTurn("Delete the stale release branch or keep it?")}
	c, rec := askACController(t, prov, "", func(o *Options) {
		o.AutopilotAskAutoContinue = true
	})
	done := runAskACTurn(c)
	askID := rec.waitAsk(t)
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"keep it"}}})
	<-done

	if n := rec.noticeCount(askAutoContinueNoticeCode); n != 1 {
		t.Fatalf("ask_auto_continue notices = %d, want exactly one", n)
	}
	msg, ok := hostContinuationMessage(c)
	if !ok {
		t.Fatal("no host continuation message in the transcript; the answered ask was not resumed")
	}
	if !strings.Contains(msg.Content, "keep it") {
		t.Fatalf("continuation prompt = %q, want the recorded decision", msg.Content)
	}
	if left := c.consumeTurnAskAnswered(); left != "" {
		t.Fatalf("marker left unconsumed: %q; the continuation must take it one-shot", left)
	}
}

// Combination 2 — flag ON × timeout auto-answer (task 477 refusal): the
// host-refused ask counts as answered, so the stop after it is resumed too
// (验收①的「含超时自动回答」分支).
func TestAskAutoContinueResumesAfterTimeoutRefusal(t *testing.T) {
	prov := &askACScript{first: askACAskTurn("Delete the release branch and force push the result, or keep it?")}
	c, rec := askACController(t, prov, "", func(o *Options) {
		o.Autopilot = true
		o.AutopilotMaxRuntime = time.Minute
		o.AutopilotAskWait = 20 * time.Millisecond
		o.AutopilotAskTimeoutEnabled = true
		o.AutopilotAskAutoContinue = true
	})
	if err := <-runAskACTurn(c); err != nil && errors.Is(err, ErrAutopilotAskUnanswered) {
		t.Fatalf("477 must refuse (no terminal stop) with the sub-option on: %v", err)
	}
	if n := rec.noticeCount("autopilot_ask_timeout"); n != 1 {
		t.Fatalf("autopilot_ask_timeout notices = %d, want the 477 refusal anchor", n)
	}
	if n := rec.noticeCount(askAutoContinueNoticeCode); n != 1 {
		t.Fatalf("ask_auto_continue notices = %d, want exactly one", n)
	}
	if _, ok := hostContinuationMessage(c); !ok {
		t.Fatal("no host continuation message; the timeout-refused ask was not resumed")
	}
}

// Combination 3 — flag OFF × human answer: the guaranteed baseline (铁律 2).
// Same terminal stop, same answered ask, and nothing resumes by itself: no
// notice, no continuation message (关闭态 A/B 对照).
func TestAskAutoContinueOffKeepsIdleStopAfterHumanAnswer(t *testing.T) {
	prov := &askACScript{first: askACAskTurn("Delete the stale release branch or keep it?")}
	c, rec := askACController(t, prov, "", nil)
	done := runAskACTurn(c)
	askID := rec.waitAsk(t)
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"keep it"}}})
	<-done

	if n := rec.noticeCount(askAutoContinueNoticeCode); n != 0 {
		t.Fatalf("off state emitted ask_auto_continue notices = %d, want zero", n)
	}
	if _, ok := hostContinuationMessage(c); ok {
		t.Fatal("off state submitted a host continuation turn; the closed state must stay byte-for-byte")
	}
	if left := c.consumeTurnAskAnswered(); left == "" {
		t.Fatal("off state consumed the answered-ask marker; the marker must survive untouched when the switch is off")
	}
}

// Combination 4a — flag OFF × timeout auto-answer (477 ON): the refusal keeps
// continuing the turn in-process (477's own semantics) and the dead stop after
// it gains no 544 continuation — the two sub-options do not interfere.
func TestAskAutoContinueOffLeaves477RefusalPathUntouched(t *testing.T) {
	prov := &askACScript{first: askACAskTurn("Delete the release branch and force push the result, or keep it?")}
	c, rec := askACController(t, prov, "", func(o *Options) {
		o.Autopilot = true
		o.AutopilotMaxRuntime = time.Minute
		o.AutopilotAskWait = 20 * time.Millisecond
		o.AutopilotAskTimeoutEnabled = true
	})
	<-runAskACTurn(c)

	if n := rec.noticeCount("autopilot_ask_timeout"); n != 1 {
		t.Fatalf("autopilot_ask_timeout notices = %d, want the 477 anchor", n)
	}
	if n := rec.noticeCount(askAutoContinueNoticeCode); n != 0 {
		t.Fatalf("off state emitted ask_auto_continue = %d, want zero", n)
	}
	if _, ok := hostContinuationMessage(c); ok {
		t.Fatal("off state submitted a host continuation behind 477's back")
	}
}

// Combination 4b — flag ON but 477 OFF: the timeout is the task-109 B4
// terminal stop, and nothing was answered, so 544 must NOT resurrect the run
// (与 477 分工的硬边界).
func TestAskAutoContinueOnDoesNotResurrectAskUnansweredStop(t *testing.T) {
	prov := &askACScript{first: askACAskTurn("Delete the release branch and force push the result, or keep it?")}
	c, rec := askACController(t, prov, "", func(o *Options) {
		o.Autopilot = true
		o.AutopilotMaxRuntime = time.Minute
		o.AutopilotAskWait = 20 * time.Millisecond
		o.AutopilotAskAutoContinue = true
	})
	err := <-runAskACTurn(c)
	if err != nil && !errors.Is(err, ErrAutopilotAskUnanswered) && !strings.Contains(err.Error(), "ask") {
		t.Fatalf("turn err = %v, want the ask-unanswered chain", err)
	}
	if n := rec.noticeCount(askAutoContinueNoticeCode); n != 0 {
		t.Fatalf("ask_auto_continue notices = %d, want zero: nothing was answered", n)
	}
	if _, ok := hostContinuationMessage(c); ok {
		t.Fatal("544 resumed a 477-off terminal stop; the timeout contract belongs to 477")
	}
}

// Guard — flag ON but the user cancelled: a stop the user asked for is never
// resumed by itself. Cancel lands while the ask is still pending, so the
// answer arrives at an already-cancelled prompt and the turn ends cancelled.
func TestAskAutoContinueExcludesUserCancel(t *testing.T) {
	prov := &askACScript{first: askACAskTurn("Delete the stale release branch or keep it?")}
	c, rec := askACController(t, prov, "", func(o *Options) {
		o.AutopilotAskAutoContinue = true
	})
	done := runAskACTurn(c)
	askID := rec.waitAsk(t)
	c.Cancel()
	deadline := time.Now().Add(5 * time.Second)
	for c.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	c.AnswerQuestion(askID, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"keep it"}}})
	<-done

	if n := rec.noticeCount(askAutoContinueNoticeCode); n != 0 {
		t.Fatalf("ask_auto_continue notices after user cancel = %d, want zero", n)
	}
	if _, ok := hostContinuationMessage(c); ok {
		t.Fatal("a user-cancelled turn was resumed by the host")
	}
}

// Guard — flag ON but the turn asked nothing and died: a plain provider
// failure keeps the old idle stop (no answered ask, no continuation).
func TestAskAutoContinueNeedsAnAnsweredAsk(t *testing.T) {
	prov := &askACScript{first: textTurn("working on it, no questions yet")}
	c, rec := askACController(t, prov, "", func(o *Options) {
		o.AutopilotAskAutoContinue = true
	})
	<-runAskACTurn(c)

	if n := rec.noticeCount(askAutoContinueNoticeCode); n != 0 {
		t.Fatalf("ask_auto_continue notices = %d, want zero without an answered ask", n)
	}
	if _, ok := hostContinuationMessage(c); ok {
		t.Fatal("a turn with no answered ask was resumed by the host")
	}
}
