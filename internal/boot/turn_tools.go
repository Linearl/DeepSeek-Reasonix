package boot

import (
	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

func registerInteractiveAgentTools(reg *tool.Registry) {
	registerInteractiveAgentToolsWithConfig(reg, nil)
}

// registerInteractiveAgentToolsWithConfig 注册 ask 工具并注入配置（任务 1084）：
// ask 轮次上限（ExperimentalAskRoundLimit + AskRoundLimit）+ 高风险五类两级
// 授权闸门（实验室勾选 + 决策模式条目）。cfg 为 nil 时保持原语义（不启用轮次
// 上限、不做授权查询）。
func registerInteractiveAgentToolsWithConfig(reg *tool.Registry, cfg *config.Config) {
	ask := agent.NewAskTool()
	if cfg != nil {
		ask = ask.WithAskRoundLimit(cfg.Desktop.ExperimentalAskRoundLimit, cfg.Desktop.AskRoundLimit)
		ask = ask.WithHighRiskAuthz(&highRiskAuthzPolicy{cfg: cfg})
	}
	reg.Add(ask)
}
