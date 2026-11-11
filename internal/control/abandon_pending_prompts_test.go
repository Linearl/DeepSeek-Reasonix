package control

import (
	"testing"
	"time"

	"reasonix/internal/event"
)

// tl1541 验收①（新钉）：待确认（pendingPrompt）会话的显式确认入口 —— 放弃
// 全部 pending prompt 必须走 cancelOwnedPrompt 漏斗（PromptClosed 事件），
// 徽章与卡片同源死亡，且活回合的等待被前台取消解除（挂死即红）。
func TestAbandonPendingPromptsClearsApprovalAndUnblocksTurn(t *testing.T) {
	approvals := make(chan event.Approval, 1)
	closed := make(chan event.Event, 4)
	done := make(chan event.Event, 1)
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		switch e.Kind {
		case event.ApprovalRequest:
			approvals <- e.Approval
		case event.PromptClosed:
			closed <- e
		case event.TurnDone:
			done <- e
		}
	})})
	runner := &approvalBlockingRunner{c: c}
	c.runner = runner
	t.Cleanup(func() { c.Cancel(); waitIdle(t, c); c.Close() })

	c.Send("needs approval")
	select {
	case <-approvals:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}
	if st := c.RuntimeStatus(); !st.Running || !st.PendingPrompt {
		t.Fatalf("status before abandon = %+v, want running pending", st)
	}

	if dropped := c.AbandonPendingPrompts("test-approval"); dropped != 1 {
		t.Fatalf("AbandonPendingPrompts = %d, want 1", dropped)
	}
	// 桥的配对语义：abandon 之后必须前台取消解除等待方（approval/ask 等待
	// 只认 reply 或 cancelled context，见 close() 的 clearAll+cancel 配对）。
	c.Cancel()
	if c.PendingPrompt() {
		t.Fatal("abandoned approval still counts as pending prompt")
	}
	if ids := c.PendingPromptIdentities(); len(ids) != 0 {
		t.Fatalf("abandoned approval remains registered: %+v", ids)
	}
	select {
	case e := <-closed:
		if e.Kind != event.PromptClosed {
			t.Fatalf("abandon emit = %v, want PromptClosed", e.Kind)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("abandon did not emit PromptClosed")
	}
	// 活回合的等待必须被解除：turn 以 cancelled 收场，不再挂死。
	if terminal := waitTurnDoneEvent(t, done); !terminal.Cancelled {
		t.Fatalf("turn survived prompt abandonment: %+v", terminal)
	}
	waitIdle(t, c)
}

// ask 类型的 pending prompt 同样必须可放弃（PromptAsk cancel 分支）。
func TestAbandonPendingPromptsClearsAskAndUnblocksTurn(t *testing.T) {
	asks := make(chan event.Event, 1)
	done := make(chan event.Event, 1)
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		switch e.Kind {
		case event.AskRequest:
			asks <- e
		case event.TurnDone:
			done <- e
		}
	})})
	c.runner = &askBlockingRunner{c: c}
	t.Cleanup(func() { c.Cancel(); waitIdle(t, c); c.Close() })

	c.Send("ask user")
	request := waitForValue(t, "Ask was not published", asks)
	_ = request
	if dropped := c.AbandonPendingPrompts("test-ask"); dropped != 1 {
		t.Fatalf("AbandonPendingPrompts = %d, want 1", dropped)
	}
	// 同上：abandon + 前台取消是桥的一对动作，等待方靠 cancelled context 解除。
	c.Cancel()
	if c.PendingPrompt() || len(c.PendingPromptIdentities()) != 0 {
		t.Fatal("abandoned ask remains pending")
	}
	if terminal := waitTurnDoneEvent(t, done); !terminal.Cancelled {
		t.Fatalf("turn survived ask abandonment: %+v", terminal)
	}
	waitIdle(t, c)
}

// 负钉（tl1541 验收②）：没有 pending prompt 时确认入口必须是零动作——
// 不丢弃、不发 PromptClosed、不取消回合。
func TestAbandonPendingPromptsIdleIsNoop(t *testing.T) {
	closed := make(chan event.Event, 4)
	c := New(Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.PromptClosed {
			closed <- e
		}
	})})
	t.Cleanup(func() { c.Cancel(); waitIdle(t, c); c.Close() })

	if dropped := c.AbandonPendingPrompts("test-idle"); dropped != 0 {
		t.Fatalf("idle abandon = %d, want 0", dropped)
	}
	select {
	case e := <-closed:
		t.Fatalf("idle abandon emitted %v", e.Kind)
	default:
	}
	if st := c.RuntimeStatus(); st.Running || st.CancelRequested {
		t.Fatalf("idle abandon disturbed the runtime: %+v", st)
	}
}
