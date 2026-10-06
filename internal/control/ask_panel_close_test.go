package control

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/tool"
)

// 任务536：ask 被取消/超时后面板未关闭——后端半边的回归钉。
//
// 现场：ask 超时/被取消后后端静默弃置 prompt，前端面板还开着，用户连填 3 次
// 全被 "prompt is not pending" 拒回。修复后 cancelOwnedPrompt 收口发
// prompt_closed（面板必关的权威信号），这里把两条弃置路径钉死：
//   ① 超时路径（autopilot + task-477 拒绝子选项）；
//   ② 取消路径（attended，调用方 ctx 到期）；
// 并反向钉住正常回答路径不得误发 prompt_closed（面板只能由 prompt_answered
// 关，误发会在答题瞬间闪掉面板）。

type promptEventSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *promptEventSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *promptEventSink) all() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]event.Event(nil), s.events...)
}

func (s *promptEventSink) closedPrompts() []event.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []event.Event
	for _, e := range s.events {
		if e.Kind == event.PromptClosed {
			out = append(out, e)
		}
	}
	return out
}

func askPanelCloseController(t *testing.T, sink *promptEventSink, wait time.Duration) *Controller {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	return New(Options{
		Runner:                     ag,
		Executor:                   ag,
		Sink:                       sink,
		Autopilot:                  true,
		AutopilotMaxRuntime:        time.Minute,
		AutopilotAskWait:           wait,
		AutopilotAskTimeoutEnabled: true,
	})
}

// ① 超时路径：ask 等待超时被弃置时必须发出 prompt_closed，id 与 kind 可对上
// 打开面板的那张卡（面板必关的判据锚）。
func TestAskTimeoutEmitsPromptClosed(t *testing.T) {
	sink := &promptEventSink{}
	c := askPanelCloseController(t, sink, 20*time.Millisecond)
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("ask err = %v, want the task-477 refusal with no error", err)
	}
	if len(answers) == 0 {
		t.Fatal("no refusal answers; the run would see an unanswered ask")
	}
	closed := sink.closedPrompts()
	if len(closed) != 1 {
		t.Fatalf("prompt_closed events = %d (%+v), want exactly 1 on the timeout path", len(closed), closed)
	}
	if closed[0].ItemID == "" {
		t.Fatal("prompt_closed carries no ItemID; the frontend cannot match the panel by id")
	}
	if closed[0].PromptKind != string(PromptAsk) {
		t.Fatalf("prompt_closed kind = %q, want %q", closed[0].PromptKind, PromptAsk)
	}
}

// ② 取消路径：attended ask 被调用方取消（Stop/10min approvalTimeout 同一条
// waitCtx.Done 出口）同样必须发出 prompt_closed——用户明确要求「超时=拒绝
// 合理，但面板必须关」。
func TestAskCancelEmitsPromptClosed(t *testing.T) {
	sink := &promptEventSink{}
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag, Sink: sink})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Ask(ctx, askTimeoutHighRiskQuestion); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("attended ask err = %v, want context.DeadlineExceeded", err)
	}
	closed := sink.closedPrompts()
	if len(closed) != 1 {
		t.Fatalf("prompt_closed events = %d (%+v), want exactly 1 on the cancel path", len(closed), closed)
	}
	if closed[0].PromptKind != string(PromptAsk) {
		t.Fatalf("prompt_closed kind = %q, want %q", closed[0].PromptKind, PromptAsk)
	}
}

// 反向钉：正常回答路径只发 prompt_answered，不得误发 prompt_closed——误发会
// 在用户提交的瞬间把面板关掉，答案却被后端正常接受，前后端状态错位。
func TestAnsweredAskEmitsNoPromptClosed(t *testing.T) {
	sink := &promptEventSink{}
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{Runner: ag, Executor: ag, Sink: sink})
	done := make(chan struct{})
	var askErr error
	go func() {
		defer close(done)
		_, askErr = c.Ask(context.Background(), []event.AskQuestion{{ID: "q1", Prompt: "table or bullets?"}})
	}()
	// 等面板真正挂起（ask 进入 pending 集），再以用户身份回答。
	deadline := time.Now().Add(2 * time.Second)
	id := ""
	for time.Now().Before(deadline) {
		if ids := c.PendingPromptIdentities(); len(ids) > 0 && ids[0].Kind == PromptAsk {
			id = ids[0].PromptID
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("ask never became pending; cannot exercise the answered path")
	}
	if err := c.AnswerQuestionChecked(id, []event.AskAnswer{{QuestionID: "q1", Selected: []string{"table"}}}); err != nil {
		t.Fatalf("answer err = %v, want nil", err)
	}
	<-done
	if askErr != nil {
		t.Fatalf("ask err = %v, want nil after the user answered", askErr)
	}
	if closed := sink.closedPrompts(); len(closed) != 0 {
		t.Fatalf("answered ask emitted prompt_closed (%+v); only prompt_answered may close an answered panel", closed)
	}
	answered := false
	for _, e := range sink.all() {
		if e.Kind == event.PromptAnswered && e.ItemID == id {
			answered = true
		}
	}
	if !answered {
		t.Fatal("answered ask emitted no prompt_answered; the close signal contract changed")
	}
}

// wire 形状：prompt_closed 转发到前端时必须带 promptId/promptKind 关联（与
// 打开面板的 ask_request 同键），old 前端按未知 kind 忽略不受影响。
func TestPromptClosedWireShape(t *testing.T) {
	w := eventwire.ToWire(event.Event{Kind: event.PromptClosed, ItemID: "7", PromptKind: "ask"})
	if w.Kind != "prompt_closed" {
		t.Fatalf("wire kind = %q, want prompt_closed", w.Kind)
	}
	if w.PromptID != "7" || w.PromptKind != "ask" {
		t.Fatalf("wire prompt correlation = (%q, %q), want (7, ask)", w.PromptID, w.PromptKind)
	}
}
