package main

// 任务 934 —— 686 面板真数据源。workflow 服务（internal/workflow 进程级
// 单例）的状态变化经此 hub 合并后以 "workflow:runs" 运行时事件推送前端；
// 面板打开时另经 App.WorkflowRunsSnapshot 拉一次全量（拉推合流）。
//
// 事件本体（workflow_node 六态节点事件）不走这里：它们由发起会话的
// tabEventSink 在既有 agent:event 通道投递（internal/eventwire 已有 wire
// 形态），前端 feed 在面板侧按 runId 合流——推送面只补运行级状态与 DAG
// 定义，避免节点事件双投。
//
// 生命周期：startup 的后台循环区启动（与 heartbeat 同区），ctx 取消即退出。

import (
	"context"
	"time"

	"reasonix/internal/workflow"
)

// workflowRunDagNodeWire 是 DAG 节点的 wire 形态（前端 WorkflowDagNode 镜像）。
type workflowRunDagNodeWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Phase       string `json:"phase,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Command     string `json:"command,omitempty"`
}

// workflowRunDagEdgeWire 是 DAG 边的 wire 形态。Label 只在门分支边上有值
// （pass/fail），与 686 fixture 的边标签口径一致。
type workflowRunDagEdgeWire struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

// workflowRunDagWire 是运行所装载定义的 wire 形态（前端 WorkflowDag 镜像）。
type workflowRunDagWire struct {
	ID     string                   `json:"id"`
	Name   string                   `json:"name"`
	Phases []string                 `json:"phases,omitempty"`
	Nodes  []workflowRunDagNodeWire `json:"nodes"`
	Edges  []workflowRunDagEdgeWire `json:"edges"`
}

// workflowRunNodeWire 是一个节点的当前态快照（六态契约，见
// internal/event/workflow.go）。
type workflowRunNodeWire struct {
	NodeID    string `json:"nodeId"`
	NodeType  string `json:"nodeType"`
	State     string `json:"state"`
	Round     int    `json:"round,omitempty"`
	Detail    string `json:"detail,omitempty"`
	ChangedAt int64  `json:"changedAt,omitempty"` // unix ms
}

// workflowRunWire 是一次运行的完整 wire 快照。
type workflowRunWire struct {
	RunID     string                `json:"runId"`
	Workflow  string                `json:"workflow"`
	State     string                `json:"state"` // running | completed | failed
	StartedAt int64                 `json:"startedAt,omitempty"`
	EndedAt   int64                 `json:"endedAt,omitempty"`
	Error     string                `json:"error,omitempty"`
	DAG       *workflowRunDagWire   `json:"dag,omitempty"`
	Nodes     []workflowRunNodeWire `json:"nodes"`
}

// workflowDagWireFromDef 把引擎定义映射成面板 DAG 视图。节点名即节点 id
// （685 schema 没有独立显示名）；subagent 节点带 prompt 作下钻指令，gate
// 节点带字面命令。phase 节点既是顶点也是分组键（面板按 phase 着色）。
func workflowDagWireFromDef(wf *workflow.Workflow) *workflowRunDagWire {
	if wf == nil {
		return nil
	}
	wire := &workflowRunDagWire{ID: wf.Name, Name: wf.Name}
	seenPhases := map[string]bool{}
	currentPhase := ""
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type == workflow.NodePhase {
			// phase 既是顶点也是分组键：其后声明的节点归属该 phase（与引擎
			// 的事件 phase 上下文同序语义）。
			if n.Label != "" {
				currentPhase = n.Label
				if !seenPhases[currentPhase] {
					seenPhases[currentPhase] = true
					wire.Phases = append(wire.Phases, currentPhase)
				}
			}
		}
		node := workflowRunDagNodeWire{ID: n.ID, Name: n.ID, Kind: n.Type, Phase: currentPhase}
		if n.Type == workflow.NodeConfirm {
			// confirm 也是一类子代理顶点；target 作为说明面。
			node.Instruction = "independent confirmation of " + n.Target
		}
		if n.Prompt != "" {
			node.Instruction = n.Prompt
		}
		if n.Command != "" {
			node.Command = n.Command
		}
		wire.Nodes = append(wire.Nodes, node)
	}
	for _, e := range wf.Edges {
		label := ""
		switch e.When {
		case workflow.WhenPass:
			label = "pass"
		case workflow.WhenFail:
			label = "fail"
		}
		wire.Edges = append(wire.Edges, workflowRunDagEdgeWire{From: e.From, To: e.To, Label: label})
	}
	return wire
}

// workflowRunsWire 组装当前全部运行的 wire 快照（启动序，最老在前）。
func workflowRunsWire() []workflowRunWire {
	snaps := workflow.Global().Snapshot()
	out := make([]workflowRunWire, 0, len(snaps))
	for i := range snaps {
		s := &snaps[i]
		wire := workflowRunWire{
			RunID:     s.ID,
			Workflow:  s.Workflow,
			State:     string(s.State),
			StartedAt: s.StartedAt.UnixMilli(),
			Error:     s.Err,
			DAG:       workflowDagWireFromDef(s.Def),
		}
		if !s.EndedAt.IsZero() {
			wire.EndedAt = s.EndedAt.UnixMilli()
		}
		for _, n := range s.Nodes {
			wire.Nodes = append(wire.Nodes, workflowRunNodeWire{
				NodeID:    n.ID,
				NodeType:  n.NodeType,
				State:     string(n.State),
				Round:     n.Round,
				Detail:    n.Detail,
				ChangedAt: n.ChangedAt.UnixMilli(),
			})
		}
		out = append(out, wire)
	}
	return out
}

// workflowRunsCoalesce 是推送合并窗口：一次运行一个节点秒级多次变迁，
// 逐事件推送会把面板打爆；窗口内的信号合并为一次快照推送。
const workflowRunsCoalesce = 250 * time.Millisecond

// startWorkflowRunsPump 订阅服务的变更信号，合并后推送全量快照。
func (a *App) startWorkflowRunsPump(ctx context.Context) {
	notify := workflow.Global().Notify()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-notify:
			}
			timer := time.NewTimer(workflowRunsCoalesce)
			drained := false
			for !drained {
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-notify:
				case <-timer.C:
					drained = true
				}
			}
			a.emitWorkflowRuns(ctx)
		}
	}()
}

func (a *App) emitWorkflowRuns(ctx context.Context) {
	a.runtimeEvents.Emit(ctx, "workflow:runs", workflowRunsWire())
}

// WorkflowRunsSnapshot 是面板打开时的全量拉取（Wails 绑定）。
func (a *App) WorkflowRunsSnapshot() []workflowRunWire {
	return workflowRunsWire()
}

// WorkflowCancelRun 中止一次运行（面板停止钮）。未知名或已终态返回 false。
func (a *App) WorkflowCancelRun(runID string) bool {
	return workflow.Global().CancelRun(runID)
}
