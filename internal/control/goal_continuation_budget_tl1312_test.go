package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/goaleval"
	"reasonix/internal/provider"
)

// tl1312 — heartbeat goal-mode continuation budget threshold (DP-0012):
// ① an enabled goalMode run with no spend axis configured is bounded by the
//    default ceiling instead of unbounded;
// ② any configured budget keeps its exact current behavior (the default
//    never stacks onto it);
// ③ the real error shape — an open-ended goalText continuing forever inside
//    a heartbeat run — stops on the existing budget_spend chain, and the
//    pause names the ceiling as the default;
// ④ interactive (attended) goal turns keep the no-default contract.
//
// The e2e shapes below use tool-call rounds with usage on purpose: the agent
// task-budget gate (the stop chain the default feeds) is consulted between
// tool rounds, so a text-only script can never cross any spend budget.

// tl1312Threshold restores the production default after a test lowers it.
func tl1312Threshold(t *testing.T, tokens int) {
	t.Helper()
	old := defaultGoalContinuationTokenBudget
	defaultGoalContinuationTokenBudget = tokens
	t.Cleanup(func() { defaultGoalContinuationTokenBudget = old })
}

// tl1312BilledGoalRound is one update_goal(continue) round carrying usage, so
// every replayed round both progresses the goal and bills the task budget
// (goalToolTurn's own tool round ships no usage, so it cannot cross a budget).
func tl1312BilledGoalRound() []provider.Chunk {
	args, err := json.Marshal(map[string]string{"status": goalWireStatus(GoalStatusRunning), "reason": "working on it", "next_action": "next step"})
	if err != nil {
		panic(err)
	}
	id := fmt.Sprintf("ug-%d", goalToolCallSeq.Add(1))
	return []provider.Chunk{
		{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{ID: id, Name: "update_goal", Arguments: string(args)}},
		{Type: provider.ChunkUsage, Usage: &provider.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, RequestCount: 1}},
		{Type: provider.ChunkDone},
	}
}

// completeGoalRound is one update_goal(complete) round (no usage needed: an
// unbounded interactive goal never consults the budget gate).
func completeGoalRound() []provider.Chunk {
	return flattenTurns(goalToolTurn(GoalStatusComplete, "", ""))[0]
}

// billedGoalTurnNotices wires a controller like goalRuntimeController but also
// records every notice text, so assertions can read the pause reason.
func billedGoalTurnNotices(t *testing.T, prov provider.Provider, eval goaleval.Evaluator, opts func(*Options)) (*Controller, *[]string, <-chan event.Event) {
	t.Helper()
	var notices []string
	done := make(chan event.Event, 8)
	cfg := Options{
		GoalEvaluator: eval,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e.Text)
			}
			if e.Kind == event.TurnDone {
				done <- e
			}
		}),
	}
	if opts != nil {
		opts(&cfg)
	}
	ag := agent.New(prov, goalRegistry(), agent.NewSession(""), agent.Options{}, event.Discard)
	cfg.Runner = ag
	cfg.Executor = ag
	c := New(cfg)
	return c, &notices, done
}

// ① unconfigured + heartbeat → the default ceiling is the bound; the same
// controller without the heartbeat marker stays unbounded (④ interactive).
func TestGoalTaskBudgetDefaultAppliesOnlyToHeartbeatTurns(t *testing.T) {
	c := New(Options{})

	if got := c.goalTaskBudget(true).Tokens; got != defaultGoalContinuationTokenBudget {
		t.Fatalf("heartbeat unconfigured budget tokens = %d, want the default ceiling %d", got, defaultGoalContinuationTokenBudget)
	}
	if got := c.goalTaskBudget(false).Tokens; got != 0 {
		t.Fatalf("interactive unconfigured budget tokens = %d, want 0 (no default for attended goals)", got)
	}
	if c.goalSpendBudgetConfigured() {
		t.Fatal("empty configuration reported as configured")
	}
}

// ② a configured token budget wins as-is on both surfaces — the default never
// stacks onto or replaces it.
func TestGoalTaskBudgetConfiguredTokenAxisUnchanged(t *testing.T) {
	c := New(Options{GoalTokenBudget: 500})
	for _, heartbeat := range []bool{true, false} {
		if got := c.goalTaskBudget(heartbeat).Tokens; got != 500 {
			t.Fatalf("heartbeat=%v configured budget tokens = %d, want 500 verbatim", heartbeat, got)
		}
	}
	if !c.goalSpendBudgetConfigured() {
		t.Fatal("token budget not reported as configured")
	}
}

// ② a configured cost axis (tokens unset) stays cost-only: no token default
// stacks on, interactive and heartbeat alike.
func TestGoalTaskBudgetConfiguredCostAxisUnchanged(t *testing.T) {
	c := New(Options{TaskBudget: agent.TaskBudget{Cost: 1.5}})
	for _, heartbeat := range []bool{true, false} {
		b := c.goalTaskBudget(heartbeat)
		if b.Tokens != 0 || b.Cost != 1.5 {
			t.Fatalf("heartbeat=%v budget = %+v, want cost-only unchanged", heartbeat, b)
		}
	}
	if !c.goalSpendBudgetConfigured() {
		t.Fatal("cost budget not reported as configured")
	}
}

// ② an explicitly disabled wall axis (negative = unset per normalize
// semantics) counts as unconfigured, so the heartbeat default still applies.
func TestGoalTaskBudgetDisabledWallAxisStillUnconfigured(t *testing.T) {
	c := New(Options{TaskBudget: agent.TaskBudget{Wall: -1}})
	if c.goalSpendBudgetConfigured() {
		t.Fatal("explicitly disabled wall axis reported as configured")
	}
	if got := c.goalTaskBudget(true).Tokens; got != defaultGoalContinuationTokenBudget {
		t.Fatalf("heartbeat budget tokens = %d, want the default ceiling", got)
	}
}

// ③ the real error shape: an open-ended goalText ("keep going forever") on a
// heartbeat run with nothing configured. The loop now stops on the existing
// budget_spend chain at the default ceiling, and the pause names the default
// instead of reading like a misfire.
func TestHeartbeatGoalRunStopsOnDefaultCeiling(t *testing.T) {
	tl1312Threshold(t, 150)
	prov := &scriptedTurns{turns: [][]provider.Chunk{tl1312BilledGoalRound()}}
	eval := &fakeGoalEvaluator{outcome: goaleval.OutcomeContinue, reason: "ongoing"}
	c, notices, done := billedGoalTurnNotices(t, prov, eval, nil)
	c.SetGoal("keep going forever")

	c.SubmitHeartbeatTurn("keep going forever", "keep going forever")
	<-done

	if c.GoalStatus() != GoalStatusBlocked {
		t.Fatalf("GoalStatus = %q, want a bounded pause", c.GoalStatus())
	}
	if rt := c.GoalRuntime(); rt.StopCause != stopCauseBudgetSpend {
		t.Fatalf("stop cause = %q, want %q (the one living stop cause, untouched)", rt.StopCause, stopCauseBudgetSpend)
	}
	if rt := c.GoalRuntime(); rt.TokensUsed < defaultGoalContinuationTokenBudget {
		t.Fatalf("paused at %d tokens, want the default ceiling actually reached", rt.TokensUsed)
	}
	joined := strings.Join(*notices, "\n")
	if !strings.Contains(joined, "default heartbeat goal ceiling") || !strings.Contains(joined, "goal_token_budget") {
		t.Fatalf("pause notices = %q, want the default-ceiling naming plus the knob", joined)
	}
}

// ② the same heartbeat shape with an explicitly configured token budget: the
// pause behavior is byte-identical to before tl1312 — same stop cause, no
// default-ceiling naming in the reason.
func TestHeartbeatGoalRunConfiguredBudgetKeepsExactBehavior(t *testing.T) {
	prov := &scriptedTurns{turns: [][]provider.Chunk{tl1312BilledGoalRound()}}
	eval := &fakeGoalEvaluator{outcome: goaleval.OutcomeContinue, reason: "ongoing"}
	c, notices, done := billedGoalTurnNotices(t, prov, eval, func(o *Options) { o.GoalTokenBudget = 150 })
	c.SetGoal("keep going forever")

	c.SubmitHeartbeatTurn("keep going forever", "keep going forever")
	<-done

	if c.GoalStatus() != GoalStatusBlocked {
		t.Fatalf("GoalStatus = %q, want the configured-budget pause", c.GoalStatus())
	}
	if rt := c.GoalRuntime(); rt.StopCause != stopCauseBudgetSpend {
		t.Fatalf("stop cause = %q, want %q", rt.StopCause, stopCauseBudgetSpend)
	}
	if joined := strings.Join(*notices, "\n"); strings.Contains(joined, "default heartbeat goal ceiling") {
		t.Fatalf("configured budget must not be named as the default; notices = %q", joined)
	}
}

// ④ an attended (interactive) goal with nothing configured keeps the
// no-default contract: with the threshold lowered to a value the second
// billed round exceeds, the loop still runs to the update_goal(complete)
// report without ever pausing on budget_spend.
func TestInteractiveGoalUnconfiguredStaysUnbounded(t *testing.T) {
	tl1312Threshold(t, 150)
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		tl1312BilledGoalRound(),
		tl1312BilledGoalRound(),
		completeGoalRound(),
		// Text-only closer: the Run ends on a round without tool calls, so the
		// clamped replay never re-fires the complete call into the loop breaker.
		flattenTurns(goalToolTurn(GoalStatusComplete, "", ""))[1],
	}}
	c, _, done := billedGoalTurnNotices(t, prov, &fakeGoalEvaluator{outcome: goaleval.OutcomeContinue, reason: "ongoing"}, nil)
	c.SetGoal("research the subsystem end to end")

	c.Submit("research the subsystem end to end")
	<-done

	if c.GoalStatus() != GoalStatusComplete {
		t.Fatalf("GoalStatus = %q, want complete without a budget pause (rt = %+v)", c.GoalStatus(), c.GoalRuntime())
	}
	if rt := c.GoalRuntime(); rt.StopCause == stopCauseBudgetSpend {
		t.Fatalf("interactive unconfigured goal paused on budget_spend — the default leaked out of the heartbeat surface")
	}
}
