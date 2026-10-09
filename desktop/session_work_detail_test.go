package main

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
)

// workDetailController 只重载 667 明细读数用到的方法；其余调用走
// stubSessionAPI 的 panic 默认（生产读者直呼端口，不吞错误）。
type workDetailController struct {
	stubSessionAPI
	status control.RuntimeStatus
	jobs   []jobs.View
	path   string
}

func (c *workDetailController) RuntimeStatus() control.RuntimeStatus { return c.status }
func (c *workDetailController) Jobs() []jobs.View {
	return append([]jobs.View(nil), c.jobs...)
}
func (c *workDetailController) SessionPath() string { return c.path }

// sessionFileWithContact 落一个带 ContactID 的空会话文件：
// agent.SessionContactID 从该文件的 BranchMeta 解析 contact_id，
// sessionCollabTargets 正是以这条链把 tab 映射进协作目录。
func sessionFileWithContact(t *testing.T, dir, stem, contact string) string {
	t.Helper()
	p := filepath.Join(dir, stem+".jsonl")
	if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agent.UpdateBranchMeta(p, true, func(m *agent.BranchMeta) error {
		m.ContactID = contact
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCollabSessionWorkDetailSameSourceAsPanels pins the 667 same-source rule:
// workDetail 的任务行与「运行中面板」(task 440, activeWorkForController) 读数
// 一致、子代理行与「胶囊计数」(task 557, RunningSubagents) 读数一致——工具接口
// 只是同一运行时状态层的另一张脸，绝不各造一套数。
func TestCollabSessionWorkDetailSameSourceAsPanels(t *testing.T) {
	dir := t.TempDir()
	visiblePath := sessionFileWithContact(t, dir, "visible", "sc_visible")
	detachedPath := sessionFileWithContact(t, dir, "detached", "sc_detached")

	visibleCtrl := &workDetailController{
		status: control.RuntimeStatus{Running: true},
		path:   visiblePath,
		jobs: []jobs.View{
			{ID: "job-live", Kind: "bash", Label: "live", Status: "running", StartedAt: 1700000000000, Tps: 7},
			{ID: "job-dead", Kind: "task", Label: "dead", Status: "interrupted", StartedAt: 1700000000001},
		},
	}
	detachedCtrl := &workDetailController{
		status: control.RuntimeStatus{},
		path:   detachedPath,
		jobs: []jobs.View{
			{ID: "job-det", Kind: "task", Label: "det", Status: "running", StartedAt: 1700000000002},
		},
	}

	visibleTab := &WorkspaceTab{ID: "tab-a", TopicTitle: "可见页", Ctrl: visibleCtrl}
	detachedTab := &WorkspaceTab{ID: "tab-b", TopicTitle: "分离页", Ctrl: detachedCtrl}
	app := &App{
		tabs:             map[string]*WorkspaceTab{"tab-a": visibleTab},
		tabOrder:         []string{"tab-a"},
		detachedSessions: map[string]*WorkspaceTab{"sess-b": detachedTab},
		activeTabID:      "tab-a",
	}
	visibleTab.sink = &tabEventSink{tabID: visibleTab.ID, app: app}

	// 前台子代理经真实生命周期链进入 557 注册表；call_9 是并行派发批次键。
	visibleTab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_running", Ref: "sa_1", Skill: "调研子代理",
		StartUnixMs: 1700000000003, ParentToolCallID: "call_9",
	})
	visibleTab.sink.RecordSubagentLifecycle(event.SubagentLifecycleInfo{
		Phase: "child_running", Ref: "sa_2", Skill: "审计子代理",
		StartUnixMs: 1700000000004,
	})

	// 可见 tab：任务行必须与面板逐行一致（含 interrupted 被面板跳过的口径）；
	// Tps 对照 job manager 原始行（任务弹窗 jobsForCtrl 的同源字段，440 面板
	// 的 ActiveWorkView 不携带它）。
	detail, known := app.collabSessionWorkDetail("sc_visible")
	if !known {
		t.Fatal("the visible tab's contact must be resolvable")
	}
	panel := activeWorkForController(visibleCtrl)
	if len(detail.Tasks) != len(panel.Jobs) || len(detail.Tasks) != 1 {
		t.Fatalf("task rows = %+v, want the panel's %d non-interrupted rows", detail.Tasks, len(panel.Jobs))
	}
	if detail.Tasks[0].ID != panel.Jobs[0].ID || detail.Tasks[0].Status != panel.Jobs[0].Status || detail.Tasks[0].StartedAt != panel.Jobs[0].StartedAt {
		t.Fatalf("task row %+v must match the panel row %+v", detail.Tasks[0], panel.Jobs[0])
	}
	rawJobs := visibleCtrl.Jobs()
	if detail.Tasks[0].Tps != rawJobs[0].Tps || detail.Tasks[0].Kind != rawJobs[0].Kind || detail.Tasks[0].Label != rawJobs[0].Label {
		t.Fatalf("task row %+v must match the job manager row %+v", detail.Tasks[0], rawJobs[0])
	}

	// 子代理行必须与胶囊读数逐行一致（同 tab 过滤 + 批次键透传）。
	capsule := app.RunningSubagents()
	if len(detail.Subagents) != 2 || len(capsule) != 2 {
		t.Fatalf("subagent rows = %+v, capsule = %+v, want the same 2 running rows", detail.Subagents, capsule)
	}
	if detail.Subagents[0].Ref != "sa_1" || detail.Subagents[0].ParentToolCallID != "call_9" {
		t.Fatalf("batch key must pass through: %+v", detail.Subagents[0])
	}
	if detail.Subagents[1].ParentToolCallID != "" {
		t.Fatalf("a row without a parent tool-call id must carry an empty batch: %+v", detail.Subagents[1])
	}
	if detail.Subagents[0].StartedAt != 1700000000003 || detail.Subagents[1].StartedAt != 1700000000004 {
		t.Fatalf("startedAt must come from the lifecycle telemetry: %+v", detail.Subagents)
	}

	// detached 运行时：同函数可达，任务行来自 target.ctrl。
	detail, known = app.collabSessionWorkDetail("sc_detached")
	if !known || len(detail.Tasks) != 1 || detail.Tasks[0].ID != "job-det" {
		t.Fatalf("detached detail = %+v known=%v, want its own job row", detail, known)
	}

	// 目录里不存在的 contact：诚实 known=false。
	if _, known := app.collabSessionWorkDetail("sc_nobody"); known {
		t.Fatal("an unresolvable contact must answer known=false")
	}
	if _, known := app.collabSessionWorkDetail(""); known {
		t.Fatal("an empty contact must answer known=false")
	}
}
