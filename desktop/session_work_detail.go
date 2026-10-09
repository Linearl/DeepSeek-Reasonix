package main

import (
	"strings"

	"reasonix/internal/agent"
)

// collabSessionWorkDetail (任务 667) answers one contact's runtime work
// detail for get_session_status's opt-in include_tasks_and_subagents flag.
//
// 数据源纪律（与 440 面板/557 胶囊同源，不新造状态）：
//   - tasks = ctrl.Jobs() —— 桌面「运行中面板」(task 440, activeWorkForController)
//     渲染的同一批后台任务行，同样跳过 interrupted（该面板的既有口径）；
//   - subagents = a.RunningSubagents() 按 tabID 过滤 —— 「胶囊计数」(task 557)
//     统计的同一批前台运行中子代理行，行里的 ParentToolCallID 即派发批次键。
//
// 解析走 sessionCollabLiveTargets：可见 tab 的控制器从 tab.Ctrl 直接读
// （纯读访问器，不触发租约恢复一类的副作用）；detached 运行时自带 ctrl。
// known=false 表示本进程看不到该会话的运行时——与 SessionStatus 探针同一
// 规则：诚实上报，绝不伪装成空集。
func (a *App) collabSessionWorkDetail(contactID string) (agent.SessionWorkDetail, bool) {
	contactID = strings.TrimSpace(contactID)
	if contactID == "" {
		return agent.SessionWorkDetail{}, false
	}
	for _, target := range a.sessionCollabLiveTargets(nil) {
		if target.contactID != contactID {
			continue
		}
		ctrl := target.ctrl
		if ctrl == nil && target.tabID != "" {
			if tab := a.tabByID(target.tabID); tab != nil {
				ctrl = tab.Ctrl
			}
		}
		detail := agent.SessionWorkDetail{
			Tasks:     []agent.TaskRuntimeRow{},
			Subagents: []agent.SubagentRuntimeRow{},
		}
		if ctrl != nil {
			for _, job := range ctrl.Jobs() {
				// 与 activeWorkForController 同一口径：interrupted 已经停摆，
				// 面板不渲染，这里也不报，保证工具读数与面板可对账。
				if job.Status == "interrupted" {
					continue
				}
				detail.Tasks = append(detail.Tasks, agent.TaskRuntimeRow{
					ID: job.ID, Kind: job.Kind, Label: job.Label,
					Status: job.Status, StartedAt: job.StartedAt, Tps: job.Tps,
				})
			}
		}
		if target.tabID != "" {
			for _, row := range a.RunningSubagents() {
				if row.TabID != target.tabID {
					continue
				}
				detail.Subagents = append(detail.Subagents, agent.SubagentRuntimeRow{
					Ref: row.Ref, Name: row.Name, Status: "running",
					StartedAt: row.StartedAt, ParentToolCallID: row.ParentToolCallID,
				})
			}
		}
		return detail, true
	}
	return agent.SessionWorkDetail{}, false
}
