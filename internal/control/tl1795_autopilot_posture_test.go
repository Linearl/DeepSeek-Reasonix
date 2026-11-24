package control

// tl1795 回归：task-595 档位离开后，活控制器的 unattended 姿态必须真的落地
// 清除——ask 链（reversibleTimedAsk/autopilotHighRiskAsk 两 timer）读的是
// 构造期副本 c.autopilot，桌面切到 YOLO 后它一直留着 true，用户眼里的有人
// 值守会话里 ask 仍按 autopilot 15s 自决（dec-c9f7a50a 实锤）。
// 三段断言：
//  ① armed 基线：autopilot 控制器的高危 ask 到点自决（护栏：证明 harness
//     真的装上了 timer，不是空转）；
//  ② ClearAutopilotPosture 后：同一个高危 ask 无 timer——等到调用方自己的
//     deadline，绝不自决；
//  ③ 可逆 ask 同理：无 auto_decide notice、无 autopilotNoHumanAnswer。
// 附 AutopilotActive 读数与 goalMachine.clearAutopilot 的 deadline 语义钉。

import (
	"context"
	"errors"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

func tl1795PostureController(t *testing.T, sink *askTimeoutSink) *Controller {
	t.Helper()
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, sink)
	c := New(Options{
		Runner:                     ag,
		Executor:                   ag,
		Sink:                       sink,
		Autopilot:                  true,
		AutopilotMaxRuntime:        time.Hour,
		AutopilotAskWait:           20 * time.Millisecond,
		AutopilotAskTimeoutEnabled: true,
	})
	if !c.AutopilotActive() {
		t.Fatal("precondition failed: autopilot controller must report AutopilotActive")
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// ① armed 基线：同参数 autopilot 控制器的高危 ask 到点自答。
func TestTl1795ArmedBaselineHighRiskAskSelfAnswers(t *testing.T) {
	c, _ := askTimeoutController(t, true, 20*time.Millisecond)
	if !c.AutopilotActive() {
		t.Fatal("precondition failed: autopilot controller must report AutopilotActive")
	}
	answers, err := c.Ask(context.Background(), askTimeoutHighRiskQuestion)
	if err != nil {
		t.Fatalf("armed ask err = %v, want the timed answers with no error", err)
	}
	if len(answers) == 0 {
		t.Fatal("armed ask returned no answers; the timer never fired (harness broken)")
	}
}

// ② 姿态清除后高危 ask 无 timer：等到调用方 deadline，绝不自决。
func TestTl1795ClearedPostureHighRiskAskKeepsWaiting(t *testing.T) {
	sink := &askTimeoutSink{}
	c := tl1795PostureController(t, sink)
	c.ClearAutopilotPosture()
	if c.AutopilotActive() {
		t.Fatal("ClearAutopilotPosture must clear the live posture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	answers, err := c.Ask(ctx, askTimeoutHighRiskQuestion)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleared-posture high-risk ask err = %v (answers=%v), want it to keep waiting past the old 20ms timer", err, answers)
	}
	if len(answers) != 0 {
		t.Fatalf("cleared-posture answers = %+v, want none (no autopilot refusal, no decide-for-yourself)", answers)
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_timeout" {
			t.Fatal("cleared posture must not arm the autopilot timeout notice")
		}
	}
}

// ③ 可逆 ask 同理：清除后无 auto-decide、无 autopilotNoHumanAnswer。
func TestTl1795ClearedPostureReversibleAskKeepsWaiting(t *testing.T) {
	sink := &askTimeoutSink{}
	c := tl1795PostureController(t, sink)
	c.ClearAutopilotPosture()
	reversible := []event.AskQuestion{{
		ID:      "q1",
		Prompt:  "Structure the findings as a table or as bullets?",
		Options: []event.AskOption{{Label: "table"}, {Label: "bullets"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	answers, err := c.Ask(ctx, reversible)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleared-posture reversible ask err = %v (answers=%v), want the attended wait", err, answers)
	}
	for _, n := range sink.notices() {
		if n.Code == "autopilot_ask_auto_decide" {
			t.Fatal("cleared posture must not emit the auto-decide notice")
		}
	}
}

// goalMachine.clearAutopilot：deadline 语义随旗清零（未清前到点为真，清后恒假）。
func TestTl1795GoalMachineClearAutopilotDropsDeadline(t *testing.T) {
	ag := agent.New(nil, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:              ag,
		Executor:            ag,
		Sink:                event.Discard,
		Autopilot:           true,
		AutopilotMaxRuntime: time.Hour,
	})
	c.goals.mu.Lock()
	c.goals.deadline = time.Now().Add(-time.Minute) // 已过期
	c.goals.mu.Unlock()
	if !c.goals.autopilotDeadlineReached(time.Now()) {
		t.Fatal("precondition failed: expired deadline must read reached before the clear")
	}
	c.ClearAutopilotPosture()
	if c.goals.autopilotDeadlineReached(time.Now()) {
		t.Fatal("cleared goal machine must never read the autopilot deadline as reached")
	}
}
