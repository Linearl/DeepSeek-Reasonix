package main

// tl1454 —— 工作流模板视图的桥：workflow.Global() 模板服务面的薄转发。写
// 操作（保存/运行）由服务端现读开关自检（关则拒绝并给出下一步）；运行绑定
// 当前标签会话（子代理基底、transcript 归属、gate 工作目录都是它的），确认
// 窗披露面与运行时的 deny 判定同源。

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/workflow"
)

// workflowTemplateSourceWire 是编辑器载入的模板原文与版本。
type workflowTemplateSourceWire struct {
	Document string `json:"document"`
	Version  string `json:"version"`
}

// workflowTemplateRunWire 是面板运行回执（面板凭 runId 切回画布并选中）。
type workflowTemplateRunWire struct {
	RunID string `json:"runId"`
	State string `json:"state"`
}

// WorkflowTemplates 列出全部模板（有效带版本，无效带定位错误）。
func (a *App) WorkflowTemplates() workflow.TemplateListResult {
	return workflow.Global().Templates()
}

// WorkflowTemplateSource 载入一个模板的 canonical JSON。
func (a *App) WorkflowTemplateSource(name string) (workflowTemplateSourceWire, error) {
	doc, version, err := workflow.Global().TemplateSource(name)
	if err != nil {
		return workflowTemplateSourceWire{}, err
	}
	return workflowTemplateSourceWire{Document: doc, Version: version}, nil
}

// WorkflowTemplateInvalidSource 载入一个无效文件的原文（修复用，只读）。
func (a *App) WorkflowTemplateInvalidSource(file string) (string, error) {
	return workflow.Global().InvalidTemplateSource(file)
}

// WorkflowTemplateValidate 校验编辑器文本（不写盘）。
func (a *App) WorkflowTemplateValidate(doc string) workflow.TemplateValidateResult {
	return workflow.Global().ValidateTemplate(doc)
}

// WorkflowTemplateSave 保存（expectedVersion：""=新建，v·…=乐观并发，repair=修无效件）。
func (a *App) WorkflowTemplateSave(doc, expectedVersion string) (workflow.TemplateSaveResult, error) {
	return workflow.Global().SaveTemplate(doc, expectedVersion)
}

// WorkflowTemplateHistory 列出历史版本（新→旧；无历史返回空表）。
func (a *App) WorkflowTemplateHistory(name string) []workflow.TemplateHistoryEntry {
	out := workflow.Global().TemplateHistory(name)
	if out == nil {
		return []workflow.TemplateHistoryEntry{}
	}
	return out
}

// WorkflowTemplateVersionSource 载入一份历史版本原文（保存即恢复）。
func (a *App) WorkflowTemplateVersionSource(name, version string) (string, error) {
	return workflow.Global().TemplateVersionSource(name, version)
}

// templateRunTarget 解析当前标签会话：分支号（基底与归属）+ 工作区根（gate cwd）。
func (a *App) templateRunTarget() (sessionID, workspace string, err error) {
	ctrl := a.activeCtrl()
	if ctrl == nil {
		return "", "", fmt.Errorf("open a session tab first: a workflow template runs on the current tab's session (its workspace and sub-agent tools)")
	}
	path := strings.TrimSpace(ctrl.SessionPath())
	if path == "" {
		return "", "", fmt.Errorf("the current tab has no session yet; send a first message (or open a saved session) before running a workflow template")
	}
	return agent.BranchID(path), strings.TrimSpace(ctrl.WorkspaceRoot()), nil
}

// WorkflowTemplateRunPreview 组装运行前确认窗披露面（gate 命令逐条 deny 预检）。
func (a *App) WorkflowTemplateRunPreview(name string) (workflow.TemplateRunPreview, error) {
	_, workspace, err := a.templateRunTarget()
	if err != nil {
		return workflow.TemplateRunPreview{}, err
	}
	return workflow.Global().PreviewTemplateRun(name, workspace)
}

// WorkflowTemplateRun 在当前标签会话上运行模板（与 workflow_run 同一引擎路径）。
func (a *App) WorkflowTemplateRun(name string) (workflowTemplateRunWire, error) {
	sessionID, workspace, err := a.templateRunTarget()
	if err != nil {
		return workflowTemplateRunWire{}, err
	}
	res, err := workflow.Global().RunTemplate(context.Background(), name, workflow.TemplateRunOptions{SessionID: sessionID, GateDir: workspace})
	if err != nil {
		return workflowTemplateRunWire{}, err
	}
	return workflowTemplateRunWire{RunID: res.RunID, State: res.State}, nil
}
