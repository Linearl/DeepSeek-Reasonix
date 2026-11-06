package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/jobs"
)

// tl1434 验收 harness —— 工作流节点详情「工作过程」的登记面（service 侧）：
// nodeId→子代理执行实例（transcript ref）的 append-only 登记、快照携带、
// 落盘/恢复回填、查询口归属证明与越界降级、legacy 快照（无字段）降级。

// traceRunner 模拟 TaskSubagentRunner.runNode 的生产同形登记路径：装 agent
// ref sink → 执行 → ReportSubagentRef。ref 按 NodeID 派生以便断言。
type traceRunner struct{ refByNode map[string]string }

func (r *traceRunner) Run(ctx context.Context, req SubagentRequest) (string, error) {
	ctx = agent.WithSubagentRefSink(ctx)
	agent.RecordSubagentRef(ctx, r.refByNode[req.NodeID])
	ReportSubagentRef(ctx, req, agent.SubagentRef(ctx))
	return `Final answer: {"ok": true}`, nil
}

func runTraceWorkflow(t *testing.T, dir string, runner SubagentRunner) (*Service, string) {
	t.Helper()
	svc := NewService(dir)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, resumeChainDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner, true })
	res, err := svc.RunWorkflow(withCallSink(ctx, &captureSink{}, "sess-1434"), "resume-chain", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
	st, err := svc.WorkflowStatus(ctx, res.RunID)
	if err != nil || len(st.Runs) != 1 || st.Runs[0].State != string(RunCompleted) {
		t.Fatalf("run did not complete: %v %+v", err, st)
	}
	return svc, res.RunID
}

func nodeTraceFromSnapshot(t *testing.T, snaps []RunSnapshot, runID, nodeID string) RunNodeSnapshot {
	t.Helper()
	for _, s := range snaps {
		if s.ID != runID {
			continue
		}
		for _, n := range s.Nodes {
			if n.ID == nodeID {
				return n
			}
		}
	}
	t.Fatalf("node %s of run %s not in snapshot", nodeID, runID)
	return RunNodeSnapshot{}
}

// 登记面①：派发产出的 ref 落进登记表（append-only），快照带 refs+parents，
// parents = 发起会话（jobs.SessionFromContext 的身份链）。
func TestServiceRecordsSubagentRefsPerNode(t *testing.T) {
	runner := &traceRunner{refByNode: map[string]string{
		"first":  "sa_1434_first",
		"second": "sa_1434_second",
	}}
	svc, runID := runTraceWorkflow(t, t.TempDir(), runner)

	first := nodeTraceFromSnapshot(t, svc.Snapshot(), runID, "first")
	if len(first.SubagentRefs) != 1 || first.SubagentRefs[0] != "sa_1434_first" {
		t.Fatalf("first refs = %v, want [sa_1434_first]", first.SubagentRefs)
	}
	if len(first.SubagentRefParents) != 1 || first.SubagentRefParents[0] != "sess-1434" {
		t.Fatalf("first parents = %v, want [sess-1434]", first.SubagentRefParents)
	}
	second := nodeTraceFromSnapshot(t, svc.Snapshot(), runID, "second")
	if len(second.SubagentRefs) != 1 || second.SubagentRefs[0] != "sa_1434_second" {
		t.Fatalf("second refs = %v, want [sa_1434_second]", second.SubagentRefs)
	}
}

// 登记面②：同节点多次执行（门循环各轮）逐次追加，序=执行序，不覆盖既有条目。
func TestServiceSubagentRefAppendOnlyPerOccurrence(t *testing.T) {
	svc := NewService(t.TempDir())
	rec := &RunRecord{
		ID: "run-append", Workflow: "wf", State: RunRunning, StartedAt: time.Now(),
		nodes: map[string]*nodeRecord{}, sessionID: "sess-1434",
	}
	svc.mu.Lock()
	svc.runs[rec.ID] = rec
	svc.order = append(svc.order, rec.ID)
	svc.mu.Unlock()

	req := SubagentRequest{NodeID: "gate-fix", Occurrence: 1}
	svc.recordSubagentRef(rec.ID, req, "sa_fix_r1", "sess-1434")
	req.Occurrence = 2
	svc.recordSubagentRef(rec.ID, req, "sa_fix_r2", "sess-1434")

	snap := nodeTraceFromSnapshot(t, svc.Snapshot(), rec.ID, "gate-fix")
	if len(snap.SubagentRefs) != 2 || snap.SubagentRefs[0] != "sa_fix_r1" || snap.SubagentRefs[1] != "sa_fix_r2" {
		t.Fatalf("append-only refs = %v, want [sa_fix_r1 sa_fix_r2]", snap.SubagentRefs)
	}
	if len(snap.SubagentRefParents) != 2 || snap.SubagentRefParents[0] != "sess-1434" || snap.SubagentRefParents[1] != "sess-1434" {
		t.Fatalf("parents = %v, want parallel [sess-1434 sess-1434]", snap.SubagentRefParents)
	}
}

// 查询口：归属证明 + 越界/未知降级（明确错误，绝不猜）。
func TestSubagentRefForNodeMembershipAndBounds(t *testing.T) {
	svc := NewService(t.TempDir())
	rec := &RunRecord{
		ID: "run-q", Workflow: "wf", State: RunCompleted, StartedAt: time.Now(),
		nodes: map[string]*nodeRecord{}, sessionID: "sess-1434",
	}
	svc.mu.Lock()
	svc.runs[rec.ID] = rec
	svc.order = append(svc.order, rec.ID)
	svc.mu.Unlock()
	svc.recordSubagentRef(rec.ID, SubagentRequest{NodeID: "plan"}, "sa_plan_1", "sess-1434")

	ref, parent, err := svc.SubagentRefForNode("run-q", "plan", 1)
	if err != nil || ref != "sa_plan_1" || parent != "sess-1434" {
		t.Fatalf("SubagentRefForNode = (%q,%q,%v), want (sa_plan_1,sess-1434,nil)", ref, parent, err)
	}
	if _, _, err := svc.SubagentRefForNode("nope", "plan", 1); err == nil {
		t.Fatal("unknown run must error")
	}
	if _, _, err := svc.SubagentRefForNode("run-q", "ghost", 1); err == nil {
		t.Fatal("unknown node must error")
	}
	if _, _, err := svc.SubagentRefForNode("run-q", "plan", 0); err == nil {
		t.Fatal("occurrence 0 must error")
	}
	if _, _, err := svc.SubagentRefForNode("run-q", "plan", 2); err == nil {
		t.Fatal("out-of-range occurrence must error")
	}
	// 无关联节点（登记表空）：降级错误，文案说明「无已登记执行过程」。
	rec.mu.Lock()
	rec.nodes["bare"] = &nodeRecord{nodeType: NodeSubagent}
	rec.mu.Unlock()
	if _, _, err := svc.SubagentRefForNode("run-q", "bare", 1); err == nil {
		t.Fatal("trace-less node must degrade with an error")
	}
}

// 持久化面：refs+sessionID 随快照落盘 → 新 service 恢复后仍可回放（重启不丢关联）。
func TestSnapshotRoundTripCarriesSubagentTrace(t *testing.T) {
	runner := &traceRunner{refByNode: map[string]string{"first": "sa_persist_first", "second": "sa_persist_second"}}
	dir := t.TempDir()
	_, runID := runTraceWorkflow(t, dir, runner)

	svc2 := NewService(dir)
	svc2.restoreFromDisk()
	restored := nodeTraceFromSnapshot(t, svc2.Snapshot(), runID, "first")
	if len(restored.SubagentRefs) != 1 || restored.SubagentRefs[0] != "sa_persist_first" {
		t.Fatalf("restored refs = %v, want [sa_persist_first]", restored.SubagentRefs)
	}
	if len(restored.SubagentRefParents) != 1 || restored.SubagentRefParents[0] != "sess-1434" {
		t.Fatalf("restored parents = %v, want [sess-1434]", restored.SubagentRefParents)
	}
	ref, parent, err := svc2.SubagentRefForNode(runID, "second", 1)
	if err != nil || ref != "sa_persist_second" || parent != "sess-1434" {
		t.Fatalf("restored query = (%q,%q,%v)", ref, parent, err)
	}
}

// 降级钉：legacy 快照（tl1434 之前，无 subagentRefs/sessionId 字段）恢复后
// 查询口给明确降级错误，面板据此渲染「无已登记记录」，绝不伪造或报错崩溃。
func TestLegacySnapshotWithoutTraceDegrades(t *testing.T) {
	dir := t.TempDir()
	snap := &runSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		RunID:         "legacy-run",
		Workflow:      "resume-chain",
		State:         string(RunCompleted),
		NodeOrder:     []string{"first", "second"},
		Nodes: map[string]nodeSnapshot{
			"first":  {NodeType: NodeSubagent, Status: "done", LastEventState: "completed"},
			"second": {NodeType: NodeSubagent, Status: "done", LastEventState: "completed"},
		},
	}
	if err := saveRunSnapshot(dir, snap); err != nil {
		t.Fatalf("save legacy snapshot: %v", err)
	}
	svc := NewService(dir)
	svc.restoreFromDisk()
	if _, _, err := svc.SubagentRefForNode("legacy-run", "first", 1); err == nil {
		t.Fatal("legacy run must degrade with an explicit no-trace error")
	}
}

// 报告原语钉：空 ref 不上报（ephemeral），无 recorder ctx 不炸（headless），
// parentSession 取派发 ctx 的 jobs 会话身份。
func TestReportSubagentRefContract(t *testing.T) {
	req := SubagentRequest{NodeID: "plan", Occurrence: 1}
	// 空 ref：no-op。
	ReportSubagentRef(context.Background(), req, "")
	// 无 recorder：no-op 不炸。
	ReportSubagentRef(agent.WithSubagentRefSink(context.Background()), req, "sa_x")

	var got struct{ nodeID, ref, parent string }
	ctx := withSubagentRefRecorder(jobs.WithSession(context.Background(), "sess-ctx"), func(r SubagentRequest, ref, parent string) {
		got.nodeID, got.ref, got.parent = r.NodeID, ref, parent
	})
	ReportSubagentRef(ctx, req, "sa_reported")
	if got.nodeID != "plan" || got.ref != "sa_reported" || got.parent != "sess-ctx" {
		t.Fatalf("recorder got (%q,%q,%q), want (plan,sa_reported,sess-ctx)", got.nodeID, got.ref, got.parent)
	}
}

// 落盘形态钉：快照 JSON 里字段名稳定（前端/跨版本读取依赖键名）。
func TestSnapshotJSONFieldNamesForTrace(t *testing.T) {
	dir := t.TempDir()
	snap := &runSnapshot{
		SchemaVersion: snapshotSchemaVersion, RunID: "json-run", Workflow: "wf",
		State: string(RunCompleted), SessionID: "sess-json",
		Nodes: map[string]nodeSnapshot{
			"plan": {NodeType: NodeSubagent, Status: "done", SubagentRefs: []string{"sa_j"}, SubagentRefParents: []string{"sess-json"}},
		},
	}
	if err := saveRunSnapshot(dir, snap); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "json-run", "run.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, key := range []string{`"sessionId":"sess-json"`, `"subagentRefs":["sa_j"]`, `"subagentRefParents":["sess-json"]`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("snapshot json missing %s:\n%s", key, raw)
		}
	}
}
