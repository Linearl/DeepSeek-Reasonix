package control

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"

	_ "reasonix/internal/tool/builtin"
)

// tl1472 双通道创建 + activation 的 controller 级端到端钉。
// 人通道原语义由 goal_test.go 既有用例回归（零变化）；本文件钉模型通道。

func goalRegistryWithCreate() *tool.Registry {
	reg := tool.NewRegistry()
	for _, name := range []string{"update_goal", "create_goal"} {
		if t, ok := tool.LookupBuiltin(name); ok {
			reg.Add(t)
		}
	}
	return reg
}

func createGoalToolTurn(goalText, summary string) [][]provider.Chunk {
	args, err := json.Marshal(map[string]string{"goal": goalText, "summary": summary})
	if err != nil {
		panic(err)
	}
	return [][]provider.Chunk{
		{toolCallChunk("cg-obj-1", "create_goal", string(args)), {Type: provider.ChunkDone}},
		textTurn("goal created"),
	}
}

func TestCreateGoalModelChannelEndToEnd(t *testing.T) {
	prov := &scriptedTurns{turns: flattenTurns(
		createGoalToolTurn("tidy the docs", "Tidy the docs index"),
		goalToolTurn(GoalStatusRunning, "working through the list", "next entry"),
		goalToolTurn(GoalStatusComplete, "", ""),
	)}
	ag := agent.New(prov, goalRegistryWithCreate(), agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 16)
	var createdNotices int
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone || e.Kind == event.Notice {
				if e.Code == event.NoticeCodeGoalCreated {
					createdNotices++
				}
				events <- e
			}
		}),
	})

	// 模型通道：普通聊天轮（无目标模式）里模型自立 goal。
	c.Submit("please organize a goal for tidying the docs")
	waitForTurnDone(t, events)

	// 每个 goalToolTurn = 2 次 provider call（tool-call 轮 + 文本轮）：
	// create 轮 2 + armed 续跑轮 2 + complete 轮 2 = 6。
	if prov.call != 6 {
		t.Fatalf("provider calls = %d, want 6 (create turn + armed continuation + complete)", prov.call)
	}
	if createdNotices != 1 {
		t.Fatalf("goal_created notices = %d, want 1", createdNotices)
	}
	if got := c.GoalStatus(); got != GoalStatusComplete {
		t.Fatalf("goal should complete through the loop, got %q", got)
	}
	view := c.GoalObject()
	if view.Phase != GoalPhaseComplete || view.Activation != GoalActivationDisarmed {
		t.Fatalf("object view after completion: phase=%q activation=%q", view.Phase, view.Activation)
	}
	if view.Summary != "Tidy the docs index" {
		t.Fatalf("summary must survive to the visibility carrier: %q", view.Summary)
	}
}

func TestCreateGoalRefusesLiveGoalAtController(t *testing.T) {
	prov := &scriptedTurns{turns: flattenTurns([][]provider.Chunk{textTurn("plain answer")})}
	ag := agent.New(prov, goalRegistryWithCreate(), agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 8)
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone || e.Kind == event.Notice {
				events <- e
			}
		}),
	})
	c.SetGoal("user's own goal")
	_, err := c.CreateGoalByModel("model goal", "model summary")
	if err == nil {
		t.Fatalf("占位锁：live goal 必须拒绝模型立新")
	}
	if !strings.Contains(err.Error(), "user's own goal") || !strings.Contains(err.Error(), "active") {
		t.Fatalf("refusal must be readable (existing goal + phase): %v", err)
	}
	if got := c.Goal(); got != "user's own goal" {
		t.Fatalf("禁静默替换: goal=%q", got)
	}
}

func TestUserMessageDisarmsGoalLoopControllerPin(t *testing.T) {
	// 人通道 stint 全程：/goal set（armed）→ continue → complete，自动续跑
	// 一路到完成（原语义零变化）。
	prov := &scriptedTurns{turns: flattenTurns(
		goalToolTurn(GoalStatusRunning, "work in progress", "next step"),
		goalToolTurn(GoalStatusComplete, "", ""),
	)}
	ag := agent.New(prov, goalRegistry(), agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 16)
	c := New(Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone || e.Kind == event.Notice {
				events <- e
			}
		}),
	})

	c.Submit("/goal ship the redesign")
	waitForTurnDone(t, events)
	// 每个 goalToolTurn = 2 次 provider call：/goal set 轮 2 + complete 轮 2。
	if prov.call != 4 {
		t.Fatalf("armed goal should run to completion: calls=%d want 4", prov.call)
	}
	if got := c.GoalStatus(); got != GoalStatusComplete {
		t.Fatalf("GoalStatus() = %q, want complete", got)
	}
}

// TestDisarmForIncomingMessageGrantAndDisarm pins the submit-side ① wiring:
// the armed stint's one-shot grant shields the FIRST message after an explicit
// human install/resume (otherwise /goal resume could never take effect), and
// the next message is a real takeover — the object stays active+running with
// the permit withdrawn.
func TestDisarmForIncomingMessageGrantAndDisarm(t *testing.T) {
	c := New(Options{
		Executor: agent.New(nil, nil, agent.NewSession(""), agent.Options{}, event.Discard),
		Sink:     event.Discard,
	})
	c.SetGoal("ship it")
	if got := c.GoalObject().Activation; got != GoalActivationArmed {
		t.Fatalf("fresh install must arm: %q", got)
	}
	// Stint grant: the first message after the human install is its input.
	if c.disarmGoalForIncomingMessage("pin") {
		t.Fatalf("the stint grant must shield the first message after install")
	}
	if got := c.GoalObject().Activation; got != GoalActivationArmed {
		t.Fatalf("grant must keep the stint armed: %q", got)
	}
	// Grant consumed (the turn that carried it started): a further message is
	// a takeover — disarm 全集①.
	if !c.disarmGoalForIncomingMessage("pin") {
		t.Fatalf("a message after the granted one must disarm")
	}
	view := c.GoalObject()
	if view.Activation != GoalActivationDisarmed || view.Phase != GoalPhaseActive {
		t.Fatalf("takeover must disarm while keeping the object: %+v", view)
	}
	if got := c.GoalStatus(); got != GoalStatusRunning {
		t.Fatalf("disarmed goal stays running (状态牌与续跑解耦): %q", got)
	}

	// /goal resume 是人类直接请求 ⇒ 重新 arm。
	if !c.ResumeGoal() {
		t.Fatalf("running goal must resume")
	}
	if got := c.GoalObject().Activation; got != GoalActivationArmed {
		t.Fatalf("resume must re-arm: %q", got)
	}
}
