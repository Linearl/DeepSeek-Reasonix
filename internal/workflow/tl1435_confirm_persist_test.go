package workflow

import (
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/jobs"
	"reasonix/internal/tool"
)

// tl1435(附) harness —— 已结束区漏 confirm 节点子代理（装机缺陷）的端到端
// 回填：真实 TaskSubagentRunner（agent.TaskTool + 真实 SubagentStore）跑
// scan → confirm 工作流，断言两类节点的子代理都落进 ListSubagentsByParent
// ——右侧栏已结束区/运行中区的同一数据源。
//
// 缺陷机制：confirm 走 ReadOnlyTaskTool.Execute，read_only_task 交互契约把
// spec.Context.Ephemeral 钉死 → EphemeralSubagentRun → 无 meta 无 transcript
// 无 ref；节点派发又在 turn 外（无会话转录卡），于是 scan/triage 行可见而
// verify（复核）行两条区都不落。修复 = runner 派发 ctx 装
// WithSubagentTranscriptPersistence，read_only_task 见标记放弃 ephemeral。
const confirmPersistDoc = `
version: 1
name: confirm-persist
nodes:
  - id: scan
    type: subagent
    prompt: "scan the module for defects"
    description: 扫描缺陷
  - id: verify
    type: confirm
    target: scan
    description: 独立复核缺陷
edges:
  - {from: scan, to: verify}`

func TestConfirmNodeSubagentPersistsSidecarDirectoryRecord(t *testing.T) {
	sessionDir := t.TempDir()
	prov := &answeringProvider{routes: []route{
		{match: "independent confirmer sub-agent", answer: `{"confirmed": true, "reason": "reproduced"}`},
		{match: "scan the module for defects", answer: "scan done"},
	}}
	parentReg := tool.NewRegistry()
	parentReg.Add(dummyReadOnlyTool{})
	task := agent.NewTaskToolWithOptions(agent.TaskToolOptions{
		Provider:       prov,
		ParentRegistry: parentReg,
		MaxSteps:       20,
		SysPrompt:      "e2e sub-agent",
	}).WithTranscripts(agent.NewSubagentStore(filepath.Join(sessionDir, "subagents")), t.TempDir(), "base-model", "base-effort")

	eng := newTestEngine(NewTaskSubagentRunner(task), nil, &eventRecorder{})
	// service.RunWorkflow 的同款布线（service.go: WithParentSession +
	// jobs.WithSession）——子代理转录按发起会话归属。
	ctx := agent.WithParentSession(jobs.WithSession(t.Context(), "sess-confirm"), "sess-confirm")
	if _, err := eng.Run(ctx, mustParse(t, confirmPersistDoc)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	artifacts, err := agent.ListSubagentsByParent(sessionDir, "sess-confirm")
	if err != nil {
		t.Fatalf("ListSubagentsByParent: %v", err)
	}
	byDescription := map[string]agent.SubagentMeta{}
	for _, a := range artifacts {
		byDescription[a.Meta.Description] = a.Meta
	}
	// 控制组：scan（task 路径）节点子代理照旧落盘——本修复不得动它。
	scan, ok := byDescription["扫描缺陷"]
	if !ok {
		t.Fatalf("scan node child missing from the persisted directory; got %d records", len(artifacts))
	}
	if scan.ParentSession != "sess-confirm" || scan.Status != agent.SubagentCompleted {
		t.Fatalf("scan meta = parent %q status %q, want sess-confirm/completed", scan.ParentSession, scan.Status)
	}
	// 缺陷本体：confirm（只读路径）节点子代理必须同样落盘——修复前
	// EphemeralSubagentRun 连终态都不留，这条记录是 0。
	confirm, ok := byDescription["独立复核缺陷"]
	if !ok {
		t.Fatalf("confirm node child missing from the persisted directory (the defect: read-only dispatch stayed ephemeral); got %d records", len(artifacts))
	}
	if confirm.ParentSession != "sess-confirm" {
		t.Fatalf("confirm meta parent = %q, want sess-confirm (the panel keys rows on it)", confirm.ParentSession)
	}
	if confirm.Status != agent.SubagentCompleted {
		t.Fatalf("confirm meta status = %q, want completed", confirm.Status)
	}
	// 面板行标签读 description-first（tl1209）；复核行的 title 不得退化为
	// 裸工具名——description 缺失才回退 name，这里显式带上了。
	if strings.TrimSpace(confirm.Description) == "" {
		t.Fatalf("confirm meta description must survive to the panel row label")
	}
}
