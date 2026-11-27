package control

import (
	"testing"

	"reasonix/internal/goaleval"
	"reasonix/internal/provider"
)

// tl1835 — goalMode 激活确认弹窗的 live 生效链：用户在弹窗里把预算改到
// N 并确认后，桌面层把 N 写进 config（持久化）并当场 mirror 进本会话的
// controller（SetGoalTokenBudget）——本 tab 紧接着的 goal 激活立即跑在新
// 预算下，不用重启/新建会话。键语义（goal_token_budget，裸 token）与
// tl1312 的默认 2M 回落都逐位不动。

func TestGoalTokenBudgetLiveSetterTl1835(t *testing.T) {
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("ok")}}
	c, _, _ := goalRuntimeController(t, prov, &fakeGoalEvaluator{outcome: goaleval.OutcomeComplete, reason: "done"})

	// 基线（构造时未配置预算）：心跳回落 tl1312 默认 2M，非心跳不设上限。
	if got := c.goalTaskBudget(true).Tokens; got != defaultGoalContinuationTokenBudget {
		t.Fatalf("baseline heartbeat tokens = %d, want tl1312 default %d", got, defaultGoalContinuationTokenBudget)
	}
	if got := c.goalTaskBudget(false).Tokens; got != 0 {
		t.Fatalf("baseline attended tokens = %d, want 0 (no default for attended goals)", got)
	}

	// live setter：预算当场 mirror 进会话——两条路径都改读新值。
	c.SetGoalTokenBudget(5_000_000)
	if got := c.goalTokenBudget; got != 5_000_000 {
		t.Fatalf("after live set, goalTaskBudget axis = %d, want 5000000", got)
	}
	if got := c.goalTaskBudget(true).Tokens; got != 5_000_000 {
		t.Fatalf("heartbeat tokens after live set = %d, want 5000000 (configured axis wins over default)", got)
	}
	if got := c.goalTaskBudget(false).Tokens; got != 5_000_000 {
		t.Fatalf("attended tokens after live set = %d, want 5000000", got)
	}

	// goal 机器侧：install 时把新预算抄进 tokensLimit（tl1312 同源消费点）。
	if got := c.goals.tokenBudget; got != 5_000_000 {
		t.Fatalf("goalMachine tokenBudget after live set = %d, want 5000000", got)
	}

	// 设回 0 = 回到「未配置」语义：心跳路径重新回落默认 2M。
	c.SetGoalTokenBudget(0)
	if got := c.goalTaskBudget(true).Tokens; got != defaultGoalContinuationTokenBudget {
		t.Fatalf("heartbeat tokens after reset = %d, want tl1312 default %d", got, defaultGoalContinuationTokenBudget)
	}
	if got := c.goals.tokenBudget; got != 0 {
		t.Fatalf("goalMachine tokenBudget after reset = %d, want 0 (unset)", got)
	}
}

// TestGoalTokenBudgetLiveSetLandsOnInstallTl1835 pins the real activation
// shape: budget changed live, THEN the goal installs — the fresh scope runs
// under the new ceiling (run_ceiling consumes goalMachine.tokenBudget at
// install time exactly like a boot-time config).
func TestGoalTokenBudgetLiveSetLandsOnInstallTl1835(t *testing.T) {
	prov := &scriptedTurns{turns: [][]provider.Chunk{textTurn("ok")}}
	c, _, _ := goalRuntimeController(t, prov, &fakeGoalEvaluator{outcome: goaleval.OutcomeComplete, reason: "done"})

	c.SetGoalTokenBudget(3_000_000)
	c.SetGoal("write the report")
	c.goals.mu.Lock()
	tokensLimit := c.goals.tokensLimit
	c.goals.mu.Unlock()
	if tokensLimit != 3_000_000 {
		t.Fatalf("installed tokensLimit = %d, want 3000000 (live budget lands on the fresh scope)", tokensLimit)
	}
}
