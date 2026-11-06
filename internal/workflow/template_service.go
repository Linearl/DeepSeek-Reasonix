package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/permission"
	"reasonix/internal/tool/builtin"
)

// tl1454 —— 用户模板服务面：执行走与 workflow_run 同一 runWorkflow→Engine；
// 本文件只有编辑面（清单/校验/保存/历史）与面板运行边界（gate 在会话工作区
// 执行、过用户 deny 规则）。

// RunOriginPanel 标记由桌面模板视图发起的运行（安全边界随 run 持久化，续跑
// 同样生效）。
const RunOriginPanel = "panel"

// ErrTemplatesDisabled 是开关关闭时写面的拒绝错误（文案给已存在的下一步）。
var ErrTemplatesDisabled = errors.New("workflow templates are an experimental feature that is switched off; enable it in Settings → Lab → Session & task panels → Workflow templates")

// TemplateRunOptions 是面板运行的边界参数（工具路径不带它，行为不变）。
type TemplateRunOptions struct {
	// SessionID 是目标会话分支号：子代理基底、transcript 归属都按它解析。
	SessionID string
	// GateDir 是 gate 命令工作目录（目标会话工作区根）。
	GateDir string
}

// templateRunState 是 RunRecord 上 tl1454 的分组子态（struct-state 棘轮：一
// 个字段承载整组，不平铺标量）。
type templateRunState struct {
	origin  string
	gateDir string
	failure *RunFailure
}

// RunFailure 是运行失败的结构化定位：哪个节点、什么原因码、技术细节。
type RunFailure struct {
	NodeID string `json:"nodeId,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// nodeObservation 是 nodeRecord 上 tl1454 的分组子态（用量/原因/契约提示）。
type nodeObservation struct {
	tokens   int
	requests int
	reason   string
	contract string
}

// NodeUsageView 是节点观测的锁外快照。
type NodeUsageView struct {
	Tokens   int
	Requests int
	Reason   string
	Contract string
}

// RunTemplateView 是运行快照上 tl1454 的附加面（面板模板视图消费；默认视图
// 忽略）。
type RunTemplateView struct {
	Origin       string
	Version      string
	Budget       *Budget
	UsedTokens   int
	UsedRequests int
	Failure      *RunFailure
}

// templatesOn 是开关现读（测试可替换）。
var templatesOn = config.WorkflowTemplatesLive

// failureFrom 把运行错误折叠成结构化失败（nil = 无失败）。
func failureFrom(runErr error) *RunFailure {
	if runErr == nil {
		return nil
	}
	var nodeErr *NodeError
	if errors.As(runErr, &nodeErr) {
		return &RunFailure{NodeID: nodeErr.NodeID, Reason: nodeErr.Reason, Detail: runErr.Error()}
	}
	var budget *BudgetExceededError
	if errors.As(runErr, &budget) {
		return &RunFailure{NodeID: budget.After, Reason: ReasonBudget, Detail: runErr.Error()}
	}
	if errors.Is(runErr, context.Canceled) {
		return &RunFailure{Reason: ReasonCancelled, Detail: runErr.Error()}
	}
	return &RunFailure{Reason: ReasonInternal, Detail: runErr.Error()}
}

// recordObservation 落一条节点观测（任意 goroutine；rec.mu 串行化）。
func (s *Service) recordObservation(runID string, o NodeObservation) {
	s.mu.Lock()
	rec, ok := s.runs[runID]
	s.mu.Unlock()
	if !ok {
		return
	}
	rec.mu.Lock()
	nr, seen := rec.nodes[o.NodeID]
	if !seen {
		nr = &nodeRecord{}
		rec.nodes[o.NodeID] = nr
		rec.order = append(rec.order, o.NodeID)
	}
	nr.obs.tokens += o.Tokens
	nr.obs.requests += o.Requests
	if o.Reason != "" {
		nr.obs.reason = o.Reason
	}
	if o.Contract != "" {
		nr.obs.contract = o.Contract
	}
	rec.mu.Unlock()
	s.touch()
}

// templateViewLocked 组装运行快照的 tl1454 附加面（调用方持 rec.mu）。
func templateViewLocked(rec *RunRecord) RunTemplateView {
	view := RunTemplateView{Origin: rec.tl.origin, Failure: rec.tl.failure}
	if rec.defFingerprint != "" {
		view.Version = versionOf(rec.defFingerprint)
	}
	if rec.def != nil {
		view.Budget = rec.def.Budget
	}
	for _, nr := range rec.nodes {
		view.UsedTokens += nr.obs.tokens
		view.UsedRequests += nr.obs.requests
	}
	return view
}

// gateRunnerFor 选 gate 执行器：面板发起的运行在会话工作区执行并过 deny
// 规则；其余（工具路径）保持进程 cwd + 无守卫的既有行为。
func gateRunnerFor(origin, gateDir string) GateRunner {
	if origin != RunOriginPanel {
		return nil
	}
	return GuardedGateRunner(denyGuardFor(gateDir), ShellGateRunnerIn(gateDir))
}

// denyGuardFor 用目标工作区的生效配置（用户层+项目层）构造 deny 守卫——
// 与 bash 工具同一 permission.Policy 判定。配置读不到时不放行（fail closed
// 只对「读不到配置」生效：返回拒绝，文案点名原因）。
func denyGuardFor(root string) func(string) error {
	return func(command string) error {
		cfg, err := config.LoadForRootWithoutCredentialsReadOnly(root)
		if err != nil || cfg == nil {
			return &GateDeniedError{Command: command, Rule: "permission rules unreadable: " + errString(err)}
		}
		policy := permission.New(cfg.Permissions.Mode, cfg.Permissions.Allow, cfg.Permissions.Ask, cfg.Permissions.Deny)
		args, _ := json.Marshal(map[string]string{"command": command})
		if policy.ExplicitlyDenies("bash", args) {
			return &GateDeniedError{Command: command, Rule: "deny rule in [permissions]"}
		}
		return nil
	}
}

func errString(err error) string {
	if err == nil {
		return "no config"
	}
	return err.Error()
}

// Templates 列出全部模板：有效的带版本，无效的带定位错误（不隐藏、不改写）。
func (s *Service) Templates() TemplateListResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := TemplateListResult{Templates: []TemplateSummary{}}
	for _, name := range s.store.names() {
		wf, err := s.store.load(name)
		if err != nil {
			continue // 无效文件由 invalidFiles 单独列出
		}
		out.Templates = append(out.Templates, summarizeTemplate(wf))
	}
	out.Invalid = s.store.invalidFiles()
	return out
}

// TemplateSource 返回模板的 canonical JSON 与当前版本。
func (s *Service) TemplateSource(name string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wf, err := s.store.load(name)
	if err != nil {
		return "", "", err
	}
	raw, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return "", "", err
	}
	return string(raw), versionOf(defFingerprint(wf)), nil
}

// InvalidTemplateSource 返回无效文件的原文（供用户在编辑器里修复；只读，
// 修好后经 SaveTemplate 写入——写之前旧件先入历史）。
func (s *Service) InvalidTemplateSource(file string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inv := range s.store.invalidFiles() {
		if inv.File == file {
			return s.store.readFile(file)
		}
	}
	return "", fmt.Errorf("workflow template file %q is not in the invalid list", file)
}

// ValidateTemplate 校验（不写盘）。
func (s *Service) ValidateTemplate(doc string) TemplateValidateResult {
	return s.store.validateTemplateDoc([]byte(doc))
}

// RepairVersion 是修复无效文件时的 expectedVersion：只允许覆盖一个当前读
// 不成有效模板的文件（旧原文先入历史）。
const RepairVersion = "repair"

// SaveTemplate 校验并保存一个用户模板。expectedVersion 是编辑器打开时的
// 版本（"" = 新建；RepairVersion = 修复无效文件）：目录 head 已被别处改过
// 则拒绝，不静默覆盖他人修改。
func (s *Service) SaveTemplate(doc, expectedVersion string) (TemplateSaveResult, error) {
	if !templatesOn() {
		return TemplateSaveResult{}, ErrTemplatesDisabled
	}
	wf, d := Diagnose([]byte(doc))
	if d != nil {
		return TemplateSaveResult{}, fmt.Errorf("%s", d.Message)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store.exists(wf.Name) {
		_, loadErr := s.store.load(wf.Name)
		head := s.store.headVersion(wf.Name)
		switch {
		case expectedVersion == RepairVersion && loadErr == nil:
			return TemplateSaveResult{}, fmt.Errorf("workflow %q is a valid template (%s); open it to edit instead of repairing", wf.Name, versionOf(head))
		case expectedVersion == RepairVersion:
		case expectedVersion == "":
			return TemplateSaveResult{}, fmt.Errorf("a workflow named %q already exists; open it to edit, or pick another name", wf.Name)
		case loadErr != nil:
			return TemplateSaveResult{}, fmt.Errorf("workflow %q exists but is not a valid template; open it from the invalid list to repair it", wf.Name)
		case expectedVersion != versionOf(head):
			return TemplateSaveResult{}, fmt.Errorf("workflow %q changed since you opened it (now %s, you edited %s); reload it before saving so nobody's change is overwritten", wf.Name, versionOf(head), expectedVersion)
		}
	}
	created, saved, err := s.store.save([]byte(doc), true)
	if err != nil {
		return TemplateSaveResult{}, err
	}
	fp := defFingerprint(saved)
	return TemplateSaveResult{Name: saved.Name, Created: created, Version: versionOf(fp), Fingerprint: fp}, nil
}

// TemplateHistory 列出模板的历史 head（新→旧）。
func (s *Service) TemplateHistory(name string) []TemplateHistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.versionHistory(name)
}

// TemplateVersionSource 读一份历史 head 原文（载入编辑器；保存即恢复）。
func (s *Service) TemplateVersionSource(name, fingerprint string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.store.loadVersionRaw(name, strings.TrimPrefix(fingerprint, "v·"))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// GatePreview 是确认窗里一条 gate 命令的预检结果。
type GatePreview struct {
	NodeID  string `json:"nodeId"`
	Command string `json:"command"`
	Denied  bool   `json:"denied"`
	Reason  string `json:"reason,omitempty"`
}

// SubagentPreview 是确认窗里一个子代理节点的能力范围。
type SubagentPreview struct {
	NodeID   string   `json:"nodeId"`
	Type     string   `json:"type"`
	Tools    []string `json:"tools,omitempty"`
	AllTools bool     `json:"allTools"`
	ReadOnly bool     `json:"readOnly"`
	Timeout  string   `json:"timeout,omitempty"`
	MaxSteps int      `json:"maxSteps,omitempty"`
}

// TemplateRunPreview 是运行前确认窗的全部披露面。
type TemplateRunPreview struct {
	Name      string            `json:"name"`
	Version   string            `json:"version"`
	Workspace string            `json:"workspace"`
	Gates     []GatePreview     `json:"gates"`
	Subagents []SubagentPreview `json:"subagents"`
	Budget    *Budget           `json:"budget,omitempty"`
	Blocked   bool              `json:"blocked"`
}

// PreviewTemplateRun 组装确认窗披露面：gate 命令逐条过 deny 守卫（与运行时
// 同一判定），子代理工具范围，预算。
func (s *Service) PreviewTemplateRun(name, workspace string) (TemplateRunPreview, error) {
	s.mu.Lock()
	wf, err := s.store.load(name)
	s.mu.Unlock()
	if err != nil {
		return TemplateRunPreview{}, err
	}
	guard := denyGuardFor(workspace)
	out := TemplateRunPreview{Name: wf.Name, Version: versionOf(defFingerprint(wf)), Workspace: workspace, Budget: wf.Budget,
		Gates: []GatePreview{}, Subagents: []SubagentPreview{}}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		switch n.Type {
		case NodeGate:
			g := GatePreview{NodeID: n.ID, Command: n.Command}
			if err := guard(n.Command); err != nil {
				g.Denied, g.Reason = true, err.Error()
				out.Blocked = true
			}
			out.Gates = append(out.Gates, g)
		case NodeSubagent, NodeConfirm:
			out.Subagents = append(out.Subagents, SubagentPreview{
				NodeID: n.ID, Type: n.Type, Tools: n.Tools, AllTools: len(n.Tools) == 0 && n.Type == NodeSubagent,
				ReadOnly: n.Type == NodeConfirm, Timeout: n.Timeout, MaxSteps: n.MaxSteps,
			})
		}
	}
	return out, nil
}

// RunTemplate 从面板运行一个模板：与 workflow_run 同一 runWorkflow → Engine
// 路径；面板边界（会话工作区 + deny 守卫）随 run 持久化。
func (s *Service) RunTemplate(ctx context.Context, name string, opts TemplateRunOptions) (builtin.WorkflowRunResult, error) {
	if !templatesOn() {
		return builtin.WorkflowRunResult{}, ErrTemplatesDisabled
	}
	return s.runWorkflow(ctx, name, "", &opts)
}
