package control

// tl1804 回归：会话内新参与 autopilot 必须把 unattended 姿态立到活控制器上
// ——tl1795 补了「清」向（档位离开），但「建」向缺失：桌面 composer 切上
// autopilot 档后 tab 裸旗已立、活控制器仍带构造期 attended 姿态直到下次
// rebuild，ask 链在无人值守会话里走有人值守等待（该自决的不自决）。
// 镜像 1795 三段：
//  ① arm 后基线：attended 构造的活控制器经 ArmAutopilotPosture 立姿后，
//     高危 ask 按 autopilot 超时语义自答（477 子选项=拒绝式回答+继续），
//     grace 缺省落 DefaultAutopilotApprovalGrace（构造期解析对称）；
//  ② arm→clear 闭环：建/清对称在同一活控制器上往返；
//  ③ 无界拒绝：maxRuntime<=0 的 arm 被拒、姿态保持 attended、ask 继续等
//     （安全向：无界 unattended 不许 programmatic 侧溜进来）；
//  ④ 重复 arm no-op：goal machine 墙钟 deadline 不被顺延；
//  ⑤ goalMachine.armAutopilot 镜像钉：deadline 随旗立起且真实有效。

import (
	"context"
	"errors"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

// tl1804AttendedController 构造一个 attended（非 autopilot）活控制器——
// 桌面 in-session join 前的真实起点。
func tl1804AttendedController(t *testing.T, sink *askTimeoutSink) *Controller {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, sink)
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink:     sink,
	})
	if c.AutopilotActive() {
		t.Fatal("precondition failed: the constructed controller must start attended")
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// ① arm 后高危 ask 按 autopilot 超时语义自答（对标 1795 ① armed 基线）。
func TestTl1804ArmedPostureHighRiskAskSelfAnswers(t *testing.T) {
	sink := &askTimeoutSink{}
	c := tl1804AttendedController(t, sink)
	if !c.ArmAutopilotPosture(0, 20*time.Millisecond, true, false, time.Minute) {
		t.Fatal("ArmAutopilotPosture refused a bounded arm")
	}
	if !c.AutopilotActive() {
		t.Fatal("armed controller must report AutopilotActive")
	}
	if c.autopilotApprovalGrace != DefaultAutopilotApprovalGrace {
		t.Fatalf("grace after arm = %v, want the construction-time default %v (unset preference must not degrade)", c.autopilotApprovalGrace, DefaultAutopilotApprovalGrace)
	}
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("armed ask err = %v, want the timed answers with no error", err)
	}
	if len(answers) == 0 {
		t.Fatal("armed ask returned no answers; the timer never fired (the live posture never reached the ask chain)")
	}
}

// ①b arm 后可逆 ask 到点自决（对标 1795 ③ 的 cleared 镜像）。
func TestTl1804ArmedPostureReversibleAskAutoDecides(t *testing.T) {
	sink := &askTimeoutSink{}
	c := tl1804AttendedController(t, sink)
	if !c.ArmAutopilotPosture(0, 20*time.Millisecond, true, false, time.Minute) {
		t.Fatal("ArmAutopilotPosture refused a bounded arm")
	}
	reversible := []event.AskQuestion{{
		ID:      "q1",
		Prompt:  "Structure the findings as a table or as bullets?",
		Options: []event.AskOption{{Label: "table"}, {Label: "bullets"}},
	}}
	type result struct {
		answers []event.AskAnswer
		err     error
	}
	done := make(chan result, 1)
	go func() {
		answers, err := c.Ask(context.Background(), reversible)
		done <- result{answers, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("armed reversible ask err = %v, want the auto-decide answers with no error", r.err)
		}
		if len(r.answers) == 0 || r.answers[0].Selected[0] != autopilotNoHumanAnswer {
			t.Fatalf("armed reversible answers = %+v, want the decide-for-yourself answer", r.answers)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("armed reversible ask never auto-decided; the live posture never reached the ask chain")
	}
	sawAutoDecide := false
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_auto_decide" {
			sawAutoDecide = true
		}
	}
	if !sawAutoDecide {
		t.Fatalf("notices = %+v, want the autopilot_ask_auto_decide anchor", sink.notices())
	}
}

// ② arm→clear 在同一活控制器上往返（建/清对称的运行时闭环）。
func TestTl1804ArmClearRoundTrip(t *testing.T) {
	sink := &askTimeoutSink{}
	c := tl1804AttendedController(t, sink)
	if !c.ArmAutopilotPosture(0, 20*time.Millisecond, true, false, time.Minute) {
		t.Fatal("arm failed")
	}
	if !c.AutopilotActive() {
		t.Fatal("armed posture must read active")
	}
	c.ClearAutopilotPosture()
	if c.AutopilotActive() {
		t.Fatal("clear after arm must drop the live posture (tl1795 semantics intact on the armed controller)")
	}
	if c.autopilotApprovalGrace != 0 || c.autopilotAskWait != 0 || c.autopilotAskTimeoutEnabled || c.autopilotAskAutoContinue {
		t.Fatal("clear after arm must zero the whole autopilot duration set")
	}
}

// ③ 无界拒绝：maxRuntime<=0 的 arm 不落地，ask 保持有人值守等待。
func TestTl1804UnboundedArmRefused(t *testing.T) {
	sink := &askTimeoutSink{}
	c := tl1804AttendedController(t, sink)
	if c.ArmAutopilotPosture(0, 20*time.Millisecond, true, false, 0) {
		t.Fatal("an unbounded arm must be refused, mirroring the constructor's max-runtime guard")
	}
	if c.AutopilotActive() {
		t.Fatal("refused arm must leave the controller attended")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	answers, err := c.Ask(ctx, askTimeoutHighRiskQuestion)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("post-refusal ask err = %v (answers=%v), want the attended wait past the would-be 20ms timer", err, answers)
	}
	if len(answers) != 0 {
		t.Fatalf("post-refusal answers = %+v, want none", answers)
	}
}

// ④ 已 armed 再 arm 是 no-op：goal machine 墙钟 deadline 不被顺延
// （否则重复切档会无界延长 unattended bound）。
func TestTl1804RearmDoesNotExtendDeadline(t *testing.T) {
	sink := &askTimeoutSink{}
	c := tl1804AttendedController(t, sink)
	if !c.ArmAutopilotPosture(0, 20*time.Millisecond, true, false, time.Minute) {
		t.Fatal("first arm failed")
	}
	c.goals.mu.Lock()
	deadline := c.goals.deadline
	c.goals.mu.Unlock()
	if deadline.IsZero() {
		t.Fatal("armed goal machine must carry a real wall-clock deadline")
	}
	time.Sleep(50 * time.Millisecond)
	if c.ArmAutopilotPosture(0, time.Hour, false, false, time.Hour) {
		t.Fatal("re-arm on an already-armed controller must be a no-op reporting false")
	}
	c.goals.mu.Lock()
	after := c.goals.deadline
	c.goals.mu.Unlock()
	if !after.Equal(deadline) {
		t.Fatalf("deadline moved from %v to %v on re-arm; a repeated tier switch must not extend the unattended bound", deadline, after)
	}
}

// ⑤ goalMachine.armAutopilot 镜像钉：旗与 deadline 同立、到点可判
// （对标 1795 ④ 的 clear 侧）。
func TestTl1804GoalMachineArmAutopilotRaisesDeadline(t *testing.T) {
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink:     event.Discard,
	})
	t.Cleanup(func() { c.Close() })
	if c.goals.autopilotDeadlineReached(time.Now()) {
		t.Fatal("attended goal machine must never read the deadline as reached")
	}
	c.goals.armAutopilot(time.Minute)
	c.goals.mu.Lock()
	deadline := c.goals.deadline
	autopilot := c.goals.autopilot
	c.goals.mu.Unlock()
	if !autopilot || deadline.IsZero() {
		t.Fatalf("armAutopilot must raise the flag and a real deadline, got autopilot=%v deadline=%v", autopilot, deadline)
	}
	if c.goals.autopilotDeadlineReached(time.Now()) {
		t.Fatal("a freshly armed deadline must not read reached")
	}
	// 拨过期：同一姿态下到点判定真实生效（deadline 不是摆设）。
	c.goals.mu.Lock()
	c.goals.deadline = time.Now().Add(-time.Minute)
	c.goals.mu.Unlock()
	if !c.goals.autopilotDeadlineReached(time.Now()) {
		t.Fatal("armed goal machine with an expired deadline must read reached")
	}
}
