package workflow

import "reasonix/internal/tool/builtin"

// 工具视图投影（任务 934；tl1454 自 service.go 原样抽出，控制文件体量）。

func reportView(r *WorkflowReport) *builtin.WorkflowReportView {
	out := &builtin.WorkflowReportView{
		Conclusion: r.Conclusion,
		Verified:   r.Verified,
		NotCovered: r.NotCovered,
		Findings:   make([]builtin.WorkflowFindingView, 0, len(r.Findings)),
	}
	for _, f := range r.Findings {
		out.Findings = append(out.Findings, builtin.WorkflowFindingView{
			Where: f.Where, What: f.What, Evidence: f.Evidence, Status: f.Status, Severity: f.Severity,
		})
	}
	return out
}

func summarize(wf Workflow) builtin.WorkflowSummary {
	sum := builtin.WorkflowSummary{
		Name:          wf.Name,
		Description:   wf.Description,
		SchemaVersion: wf.Version,
		Nodes:         len(wf.Nodes),
		Edges:         len(wf.Edges),
	}
	for i := range wf.Nodes {
		switch wf.Nodes[i].Type {
		case NodeSubagent:
			sum.Subagents++
		case NodeGate:
			sum.Gates++
			if wf.Nodes[i].Loop != nil {
				sum.LoopGates++
			}
		case NodeJoin:
			sum.Joins++
		case NodePhase:
			sum.Phases++
		case NodeConfirm:
			sum.Confirms++
		}
	}
	return sum
}

func defineResult(wf *Workflow, created bool) builtin.WorkflowDefineResult {
	sum := summarize(*wf)
	return builtin.WorkflowDefineResult{
		Name:      wf.Name,
		Created:   created,
		Nodes:     sum.Nodes,
		Edges:     sum.Edges,
		Gates:     sum.Gates,
		LoopGates: sum.LoopGates,
	}
}
