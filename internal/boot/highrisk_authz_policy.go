package boot

// 任务 1084：高风险五类两级授权闸门的 boot 侧实现（持有 config）。
//
// 两级授权由硬到软：①实验室勾选（最硬，用户亲手点）②决策模式条目（次硬）。
// 记忆不算授权源。默认不勾 = 出厂五类全要问（保守）。
// 诚实声明：五类判定靠模型理解规则，非字符串硬匹配；后两类必然漏判误判。

import (
	"os"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// highRiskAuthzPolicy 是 agent.HighRiskAuthzPolicy 的 boot 实现，持有 config
// 快照（boot snapshot，重启生效——与其余实验开关一致）。
type highRiskAuthzPolicy struct {
	cfg *config.Config
}

// LabAuthorized 返回该类是否被实验室勾选授权（第一级，最硬）。
func (p *highRiskAuthzPolicy) LabAuthorized(class agent.HighRiskClass) bool {
	if p.cfg == nil {
		return false
	}
	switch class {
	case agent.HighRiskOutboundSend:
		return p.cfg.Desktop.AuthorizeOutboundSend
	case agent.HighRiskDataMigrationDelete:
		return p.cfg.Desktop.AuthorizeDataMigrationDelete
	case agent.HighRiskDestructiveChange:
		return p.cfg.Desktop.AuthorizeDestructiveChange
	case agent.HighRiskNewTechRoute:
		return p.cfg.Desktop.AuthorizeNewTechRoute
	case agent.HighRiskPaidQuotaAlloc:
		return p.cfg.Desktop.AuthorizePaidQuotaAlloc
	default:
		return false
	}
}

// DecisionModeEnabled 返回决策模式实验功能是否已开启（第二级前提）。
func (p *highRiskAuthzPolicy) DecisionModeEnabled() bool {
	if p.cfg == nil {
		return false
	}
	return p.cfg.Agent.ExperimentalDecisionPatterns
}

// decisionAuthzCategoryAliases 是类别在决策模式条目文本里的别名（保守匹配
// 用）。要求条目文本**明确点名**类别（含别名）且含授权语义，才算命中。
var decisionAuthzCategoryAliases = map[agent.HighRiskClass][]string{
	agent.HighRiskOutboundSend:        {"对外发送", "外发", "邮件", "公开发布"},
	agent.HighRiskDataMigrationDelete: {"数据迁移与删除", "数据迁移", "删除", "清空"},
	agent.HighRiskDestructiveChange:   {"破坏性变更", "强推", "强制推送", "覆盖未提交"},
	agent.HighRiskNewTechRoute:        {"新技术路线", "技术选型", "新框架"},
	agent.HighRiskPaidQuotaAlloc:      {"付费额度", "额度分配", "付费"},
}

// decisionAuthzGrantKeywords 是授权语义关键词：条目须同时含类别名 + 其一，
// 才算「明确点名该类别的独立授权裁决」（保守门槛——宁多问一次不误放行）。
var decisionAuthzGrantKeywords = []string{"授权", "不用问", "免询问", "直接执行", "可以做", "放开", "批准"}

// DecisionAuthorized 返回决策模式里是否有覆盖该类的独立授权裁决（第二级，
// 次硬）。匹配保守：只认明确点名该类别的条目，匹配不上当没授权。
// 诚实声明：本实现对 decision-patterns.md 文件内容做最保守的关键词初筛——
// 条目须同时含类别名（别名之一）+ 授权语义关键词，才算命中。这不是完备匹配
// （靠模型/用户写条目时明确点名）；初筛只辅助缩小范围，判定「裁决是否覆盖」
// 本质仍靠条目文本的明确性。后两类（新技术路线/付费额度）必然漏判误判。
func (p *highRiskAuthzPolicy) DecisionAuthorized(class agent.HighRiskClass) (bool, string, string) {
	if p.cfg == nil || !p.cfg.Agent.ExperimentalDecisionPatterns {
		return false, "", ""
	}
	path := strings.TrimSpace(p.cfg.Agent.DecisionPatternFilePath)
	if path == "" {
		path = config.DefaultDecisionPatternFilePath()
	}
	if path == "" {
		return false, "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// 文件不存在/不可读：未匹配到授权（保守）。
		return false, "", ""
	}
	content := string(data)
	aliases := decisionAuthzCategoryAliases[class]
	for _, alias := range aliases {
		idx := strings.Index(content, alias)
		if idx < 0 {
			continue
		}
		// 在类别名附近（前后各 200 字符窗口）找授权语义关键词。
		start := idx - 200
		if start < 0 {
			start = 0
		}
		end := idx + len(alias) + 200
		if end > len(content) {
			end = len(content)
		}
		window := content[start:end]
		for _, kw := range decisionAuthzGrantKeywords {
			if strings.Contains(window, kw) {
				return true, alias, "决策模式条目明确点名「" + alias + "」并含授权语义"
			}
		}
	}
	return false, "", ""
}
