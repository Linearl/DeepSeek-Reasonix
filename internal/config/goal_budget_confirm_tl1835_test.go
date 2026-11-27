package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// tl1835 — goalMode 激活确认弹窗的两条配置：
//   - goal_budget_confirm（[agent]，默认关）：激活 goalMode 时弹窗让用户
//     调整预算再确认；关=现状直通（默认 2M 不变）。
//   - goal_token_budget（[agent]，既有键）：键语义保持裸 token 不变（向后
//     兼容），M 换算只活在显示/setter 层——默认 2 即 2,000,000。

func TestGoalBudgetConfirmConfigRoundTripTl1835(t *testing.T) {
	cfg := Default()
	cfg.Agent.GoalBudgetConfirm = true
	rendered := RenderTOML(cfg)
	if !strings.Contains(rendered, "goal_budget_confirm = true") {
		t.Fatalf("rendered config omitted goal_budget_confirm switch:\n%s", rendered)
	}
	var decoded Config
	if _, err := toml.Decode(rendered, &decoded); err != nil {
		t.Fatalf("decode rendered config: %v", err)
	}
	if !decoded.Agent.GoalBudgetConfirm {
		t.Fatal("goal_budget_confirm did not round-trip")
	}

	var explicit Config
	if _, err := toml.Decode("[agent]\ngoal_budget_confirm = true\n", &explicit); err != nil {
		t.Fatalf("decode explicit switch: %v", err)
	}
	if !explicit.Agent.GoalBudgetConfirm {
		t.Fatal("explicit TOML switch was ignored")
	}
	if cfg := Default(); cfg.Agent.GoalBudgetConfirm {
		t.Fatal("goal_budget_confirm must default to false (dialog off = 现状直通)")
	}
}

func TestSetGoalBudgetConfirmTl1835(t *testing.T) {
	cfg := Default()
	if err := cfg.SetGoalBudgetConfirm(true); err != nil {
		t.Fatalf("SetGoalBudgetConfirm(true): %v", err)
	}
	if !cfg.Agent.GoalBudgetConfirm {
		t.Fatal("setter did not flip GoalBudgetConfirm on")
	}
	if err := cfg.SetGoalBudgetConfirm(false); err != nil {
		t.Fatalf("SetGoalBudgetConfirm(false): %v", err)
	}
	if cfg.Agent.GoalBudgetConfirm {
		t.Fatal("setter did not flip GoalBudgetConfirm off")
	}
}

func TestSetGoalTokenBudgetTl1835(t *testing.T) {
	cfg := Default()
	if err := cfg.SetGoalTokenBudget(5_000_000); err != nil {
		t.Fatalf("SetGoalTokenBudget(5M): %v", err)
	}
	if cfg.Agent.GoalTokenBudget != 5_000_000 {
		t.Fatalf("GoalTokenBudget = %d, want 5000000 (raw-token semantics unchanged)", cfg.Agent.GoalTokenBudget)
	}
	// 0 = 回到「未配置」语义（心跳路径回落 tl1312 默认 2M），必须合法。
	if err := cfg.SetGoalTokenBudget(0); err != nil {
		t.Fatalf("SetGoalTokenBudget(0): %v", err)
	}
	if cfg.Agent.GoalTokenBudget != 0 {
		t.Fatalf("GoalTokenBudget = %d, want 0 (unset)", cfg.Agent.GoalTokenBudget)
	}
	// 负值不是合法预算：拒写、字段不动。
	if err := cfg.SetGoalTokenBudget(-1); err == nil {
		t.Fatal("SetGoalTokenBudget(-1) must reject negative tokens")
	}
	if cfg.Agent.GoalTokenBudget != 0 {
		t.Fatalf("rejected write mutated GoalTokenBudget = %d, want unchanged 0", cfg.Agent.GoalTokenBudget)
	}
}
