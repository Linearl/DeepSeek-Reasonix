package agent

// 任务 1084：高风险操作授权闸门 + 决策模式二级授权。
//
// 一句话口径（用户定稿）：「遇到对外发送类/高风险操作时，先查授权；授权过就
// 可以做。」授权只有两级由硬到软：①实验室勾选（最硬）②决策模式条目（次硬）。
// 记忆不算授权源。默认不勾 = 出厂五类全要问（保守）。
//
// 运行时四步流程：
//   准备执行动作 → ①属高风险五类吗（否=直接执行零打扰）→ ②该类被实验室勾选
//   授权吗（勾=执行+输出依据；没勾↓）→ ③决策模式有覆盖该类的独立授权裁决吗
//   （功能已开才查；有=执行+依据；没有↓）→ ④ask（轮次未耗尽弹面板；耗尽文本
//   确认本轮不执行）。
// 步骤②③命中须输出依据（「依据：你勾选了X」/「依据：决策模式条目Y」）便于
// 核查。
//
// ── 高风险五类判定 + 诚实声明（必须写进代码注释与文档）─────────────────
// 判定可靠性分级：对外发送 高/数据迁移删除 高/破坏性变更 中/新技术路线 低/
// 付费额度 低。
// 诚实声明：本机制实质靠模型理解规则判断「动作属哪类」，不是字符串硬匹配。
// 允许工具名/命令特征辅助初筛，不得简化为 strings.Contains 白名单就宣称覆盖
// 完备；后两类（新技术路线/付费额度）必然存在漏判误判，漏判兜底是轮次上限
// +复盘而非宣称零漏判。验收不得要求字符串匹配精度断言，只能断言构造用例下
// 行为符合预期。

import (
	"strings"
)

// HighRiskClass 是高风险五类之一，或「非高风险」。
type HighRiskClass string

// 高风险五类常量。可靠性分级见文件头诚实声明。
const (
	// HighRiskNone = 非高风险（绝大多数动作走这里，零打扰）。
	HighRiskNone HighRiskClass = ""
	// HighRiskOutboundSend 对外发送（邮件/公开发布/对外消息）。可靠性：高。
	HighRiskOutboundSend HighRiskClass = "对外发送"
	// HighRiskDataMigrationDelete 数据迁移与删除（批量删除/schema 迁移/
	// git reset --hard/清空会话）。可靠性：高。
	HighRiskDataMigrationDelete HighRiskClass = "数据迁移与删除"
	// HighRiskDestructiveChange 破坏性变更（覆盖未提交/强推）。可靠性：中。
	HighRiskDestructiveChange HighRiskClass = "破坏性变更"
	// HighRiskNewTechRoute 新技术路线选择。可靠性：低（语义判断，无可靠机械
	// 判据）。判定必然漏判误判。
	HighRiskNewTechRoute HighRiskClass = "新技术路线选择"
	// HighRiskPaidQuotaAlloc 付费额度分配。可靠性：低（计费 API 调用可判，
	// 但「分配决策」是语义的）。判定必然漏判误判。
	HighRiskPaidQuotaAlloc HighRiskClass = "付费额度分配"
)

// HighRiskClassReliability 返回类别判定可靠性分级（诚实声明用，便于 UI 提示
// 与复盘）。低可靠性的类别勾选前用户应知情必然漏判误判。
func HighRiskClassReliability(c HighRiskClass) string {
	switch c {
	case HighRiskOutboundSend, HighRiskDataMigrationDelete:
		return "高"
	case HighRiskDestructiveChange:
		return "中"
	case HighRiskNewTechRoute, HighRiskPaidQuotaAlloc:
		return "低"
	default:
		return ""
	}
}

// HighRiskAuthzPolicy 是高风险授权闸门的查询接口（两级授权查询）。
// 实现方由 boot 注入（持有 config 的实验室勾选 + 决策模式二级授权查询）。
type HighRiskAuthzPolicy interface {
	// LabAuthorized 返回该类是否被实验室勾选授权（第一级，最硬）。勾选 =
	// 授权 = 认为不是高危，可直接执行（输出依据「你勾选了X」）。
	LabAuthorized(class HighRiskClass) bool
	// DecisionModeEnabled 返回决策模式实验功能是否已开启（第二级的前提）。
	// 未开启 ⇒ 跳过决策模式查询直接进 ④。
	DecisionModeEnabled() bool
	// DecisionAuthorized 返回决策模式里是否有覆盖该类的独立授权裁决（第二
	// 级，次硬）。匹配保守：只认明确点名该类别的条目，匹配不上当没授权（宁
	// 多问一次不误放行）。保守≠字符串硬匹配——判断裁决是否覆盖仍由模型读文
	// 本，保守体现在门槛高（要求明确点名）非实现方式。返回命中条目名 + 简短
	// 理由（写进输出依据）。
	DecisionAuthorized(class HighRiskClass) (authorized bool, entryName string, reason string)
}

// HighRiskAuthzResult 是两级授权查询的结果。
type HighRiskAuthzResult struct {
	// Class 是判定出的高风险类别（HighRiskNone = 非高风险）。
	Class HighRiskClass
	// Authorized 是两级授权是否命中（true = 可直接执行 + 输出依据）。
	Authorized bool
	// Basis 是授权依据文本（「依据：你勾选了X」/「依据：决策模式条目Y」）。
	// 未命中时为空。
	Basis string
	// NotAuthorizedReason 未命中时的原因（「未匹配到授权」/「决策模式功能未
	// 开启」），便于排查为何走到 ④。不得静默当已授权。
	NotAuthorizedReason string
}

// CheckHighRiskAuthz 是两级授权查询的统一入口（步骤②③）。
//   - 先判类别（非高风险直接返回不授权、无依据——调用方按「非高风险」分支
//     直接执行）；
//   - 再查实验室勾选（第一级）：命中 → Authorized + Basis；
//   - 再查决策模式（第二级，功能已开才查）：命中 → Authorized + Basis（含
//     匹配理由）；
//   - 都没命中 → NotAuthorizedReason。
func CheckHighRiskAuthz(class HighRiskClass, policy HighRiskAuthzPolicy) HighRiskAuthzResult {
	res := HighRiskAuthzResult{Class: class}
	if class == HighRiskNone {
		// 非高风险：不需要授权，调用方直接执行（零打扰）。Authorized 保持
		// false 但 NotAuthorizedReason 空——调用方按 Class==None 分支处理。
		return res
	}
	// 步骤②：实验室勾选（第一级，最硬）。
	if policy != nil && policy.LabAuthorized(class) {
		res.Authorized = true
		res.Basis = "依据：你勾选了「" + string(class) + "」授权"
		return res
	}
	// 步骤③：决策模式二级授权（次硬）。前提：功能已开启。
	if policy == nil || !policy.DecisionModeEnabled() {
		res.NotAuthorizedReason = "决策模式功能未开启"
		return res
	}
	if authorized, entryName, reason := policy.DecisionAuthorized(class); authorized {
		res.Authorized = true
		if reason != "" {
			res.Basis = "依据：决策模式条目「" + entryName + "」——" + reason
		} else {
			res.Basis = "依据：决策模式条目「" + entryName + "」"
		}
		return res
	}
	res.NotAuthorizedReason = "未匹配到授权"
	return res
}

// classifyAskQuestionForHighRisk 是高风险五类判定的辅助初筛（诚实声明：初筛
// 不是硬匹配，模型理解规则判断为主）。这里只对 ask 问题文本做最保守的关键词
// 初筛——命中的按对应类别走授权查询；没命中的按「非高风险」处理（信息补充类
// ask 走轮次上限，不走授权流程）。
// ⚠️ 该初筛只用于 ask 问题（模型自己已判定「我该问一个高风险确认」）；对模型
// 直接执行的动作（工具调用），判定由模型在调用 ask 前完成，不经过此函数。
func classifyAskQuestionForHighRisk(prompt string, headers []string) HighRiskClass {
	text := strings.ToLower(prompt)
	// 数据迁移与删除（可靠性高）：批量删除/迁移/重置类关键词。
	if highRiskContainsAny(text, []string{"删除", "清空", "迁移", "重置", "reset", "delete", "清库", "drop", "truncate"}) {
		return HighRiskDataMigrationDelete
	}
	// 破坏性变更（可靠性中）：覆盖未提交/强推类。
	if highRiskContainsAny(text, []string{"强推", "force push", "覆盖未提交", "丢弃改动", "checkout --", "丢改动", "强制覆盖"}) {
		return HighRiskDestructiveChange
	}
	// 对外发送（可靠性高）：邮件/公开发布/对外消息类。
	if highRiskContainsAny(text, []string{"发邮件", "发送邮件", "公开发布", "发布到", "对外消息", "外发", "邮件发送", "publish", "send email"}) {
		return HighRiskOutboundSend
	}
	// 新技术路线选择（可靠性低）：换技术栈/新方案类。
	if highRiskContainsAny(text, []string{"新技术", "新方案", "换技术栈", "重构方案", "新框架", "技术选型"}) {
		return HighRiskNewTechRoute
	}
	// 付费额度分配（可靠性低）：付费/额度/预算类。
	if highRiskContainsAny(text, []string{"付费", "额度", "预算", "付费额度", "花钱", "成本分配", "quota"}) {
		return HighRiskPaidQuotaAlloc
	}
	return HighRiskNone
}

func highRiskContainsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
