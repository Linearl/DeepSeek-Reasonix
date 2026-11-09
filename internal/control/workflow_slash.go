package control

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/i18n"
	"reasonix/internal/workflow"
)

// tl1300 —— /workflow 斜杠命令的提交面（desktop/HTTP 的 Submit 路径经
// managementNotice 进这里；参数补全面在 slash.go workflowArgItems）。
//
// 语义：
//   /workflow            — 列已存定义（名字+节点/门计数）与运行提示。
//   /workflow <name>     — 以当前会话运行该定义：与面板运行同一 RunTemplate
//                          通道（会话子代理基底 + transcript 归属 + 工作区
//                          gate + deny 守卫；写面由服务端现读开关自检）。
//
// 运行存活于 turn 之外（自有 context）；节点事件不嵌本会话流（与面板发起
// 的运行同形态），进度经 workflow:runs 推送进工作流面板——斜杠命令、面板
// 运行钮、agent 的 workflow_run 工具共享同一个进程级 service/store（SSOT）。

// workflowSlashText 组装 /workflow 的 notice 文案：无参数=清单，带参数=起跑。
func (c *Controller) workflowSlashText(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		list := workflow.Global().Templates()
		if len(list.Templates) == 0 {
			return i18n.M.SlashWorkflowEmpty
		}
		var b strings.Builder
		b.WriteString(i18n.M.SlashWorkflowListHeader + "\n")
		for _, tpl := range list.Templates {
			fmt.Fprintf(&b, "  %s — %d nodes (%d gates)\n", tpl.Name, tpl.Nodes, tpl.Gates)
		}
		b.WriteString(i18n.M.SlashWorkflowRunHint)
		return strings.TrimRight(b.String(), "\n")
	}
	sessionID, workspace, err := c.workflowRunTarget()
	if err != nil {
		return "workflow: " + err.Error()
	}
	res, err := workflow.Global().RunTemplate(context.Background(), trimmed, workflow.TemplateRunOptions{SessionID: sessionID, GateDir: workspace})
	if err != nil {
		return "workflow: " + err.Error()
	}
	return fmt.Sprintf(i18n.M.SlashWorkflowStartedFmt, res.Workflow, res.RunID)
}

// workflowRunTarget 解析当前会话的运行锚点（镜像 desktop 桥 templateRunTarget
// 的同一套判据）：分支号做子代理基底与 transcript 归属，工作区根做 gate cwd。
// 无会话转录（首条消息未发）时给明确错误与已存在的下一步，绝不静默猜测。
func (c *Controller) workflowRunTarget() (sessionID, workspace string, err error) {
	path := strings.TrimSpace(c.SessionPath())
	if path == "" {
		return "", "", fmt.Errorf("this session has no transcript yet; send a first message before starting a workflow")
	}
	return agent.BranchID(path), strings.TrimSpace(c.WorkspaceRoot()), nil
}
