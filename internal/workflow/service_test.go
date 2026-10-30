package workflow

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/jobs"
)

// 任务 934 验收：服务把 685 引擎接到工具面——定义存储、运行登记、按会话
// 基底解析、事件落登记、终局报告落袋、取消。

const svcTestDoc = `
version: 1
name: svc-test-wf
description: service test fixture
nodes:
  - id: probe
    type: subagent
    prompt: report one fact
  - id: check
    type: gate
    command: "exit 0"
edges:
  - from: probe
    to: check
`

// fakeRunner 记录收到的请求，回一段可解析的最终答案。
type fakeRunner struct {
	mu      sync.Mutex
	prompts []string
	fail    error
}

func (f *fakeRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, req.Prompt)
	fail := f.fail
	f.mu.Unlock()
	if fail != nil {
		return "", fail
	}
	return "Final answer: {\"ok\": true}", nil
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService(t.TempDir())
}

func TestServiceDefineListGetRoundtrip(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	res, err := svc.DefineWorkflow(ctx, svcTestDoc, false)
	if err != nil {
		t.Fatalf("define: %v", err)
	}
	if !res.Created || res.Name != "svc-test-wf" || res.Nodes != 2 || res.Gates != 1 {
		t.Fatalf("define result mismatch: %+v", res)
	}

	// 重复定义不覆盖即拒，错误点名现有名字与 overwrite。
	_, err = svc.DefineWorkflow(ctx, svcTestDoc, false)
	if err == nil || !strings.Contains(err.Error(), "overwrite=true") {
		t.Fatalf("duplicate define must refuse with overwrite hint, got %v", err)
	}
	res, err = svc.DefineWorkflow(ctx, svcTestDoc, true)
	if err != nil || res.Created {
		t.Fatalf("overwrite define: created=%v err=%v", res.Created, err)
	}

	list, err := svc.ListWorkflows(ctx)
	if err != nil || len(list.Workflows) != 1 {
		t.Fatalf("list: %d workflows err=%v", len(list.Workflows), err)
	}
	if list.Workflows[0].Subagents != 1 || list.Workflows[0].LoopGates != 0 {
		t.Fatalf("summary mismatch: %+v", list.Workflows[0])
	}

	get, err := svc.GetWorkflow(ctx, "svc-test-wf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(get.Document, `"name": "svc-test-wf"`) {
		t.Fatalf("canonical document unexpected: %s", get.Document)
	}
	if _, err := svc.GetWorkflow(ctx, "no-such"); err == nil {
		t.Fatal("get unknown must fail")
	}
}

func TestServiceDefineRejectsInvalidDoc(t *testing.T) {
	svc := newTestService(t)
	// 门节点携带 subagent 专属字段：lenient 校验必须逐字段点名（引擎原样透传）。
	_, err := svc.DefineWorkflow(context.Background(), `
version: 1
name: bad-wf
nodes:
  - id: g
    type: gate
    command: "exit 0"
    prompt: not-a-gate-field
`, false)
	if err == nil || !strings.Contains(err.Error(), "not valid on a gate node") {
		t.Fatalf("foreign node field must be rejected with the node named, got %v", err)
	}
	list, _ := svc.ListWorkflows(context.Background())
	if len(list.Workflows) != 0 {
		t.Fatalf("rejected document must not persist, got %d", len(list.Workflows))
	}
}

// blockingRunner 挡住节点执行，让测试在运行中观测中间态后再放行。
type blockingRunner struct {
	release chan struct{}
	once    sync.Once
}

func (b *blockingRunner) Run(_ context.Context, _ SubagentRequest) (string, error) {
	<-b.release
	return "Final answer: {}", nil
}

func TestServiceRunStatusAndEvents(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}

	runner := &blockingRunner{release: make(chan struct{})}
	svc.AddRunnerSource(func(sessionID string) (SubagentRunner, bool) {
		if sessionID == "branch-abc" {
			return runner, true
		}
		return nil, false
	})

	// 发起上下文携带事件水槽（模拟 agent executeOne 的调用上下文注入面）。
	sink := &captureSink{}
	callCtx := withCallSink(context.Background(), sink, "branch-abc")

	res, err := svc.RunWorkflow(callCtx, "svc-test-wf")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.RunID == "" || res.State != string(RunRunning) {
		t.Fatalf("run result: %+v", res)
	}

	// 运行中：登记已建、节点排队事件已到、状态可查。
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		if err != nil || len(st.Runs) != 1 {
			return false
		}
		run := st.Runs[0]
		if run.State != string(RunRunning) || len(run.Nodes) == 0 {
			return false
		}
		for _, n := range run.Nodes {
			if n.State != string(event.WorkflowNodeQueued) && n.State != string(event.WorkflowNodeRunning) {
				return false
			}
		}
		return true
	})

	// 事件同时嵌进发起会话的水槽（面板点亮路径）。
	if len(sink.workflowNodes) == 0 {
		t.Fatal("caller sink received no workflow node events")
	}
	for _, info := range sink.workflowNodes {
		if info.RunID != res.RunID || info.Workflow != "svc-test-wf" {
			t.Fatalf("event identity mismatch: %+v", info)
		}
	}

	// 放行 → 运行完成 → 终态 + 报告落袋 + 完成事件。
	close(runner.release)
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunCompleted)
	})
	st, _ := svc.WorkflowStatus(ctx, res.RunID)
	run := st.Runs[0]
	if run.Report == nil || run.Report.Conclusion == "" {
		t.Fatalf("completed run must carry a report: %+v", run.Report)
	}
	if run.EndedAt == 0 {
		t.Fatal("endedAt must be stamped on completion")
	}
	gateSeen := false
	for _, n := range run.Nodes {
		if n.NodeType == "gate" && n.State == string(event.WorkflowNodeGatePassed) {
			gateSeen = true
		}
	}
	if !gateSeen {
		t.Fatalf("gate node must report gate_passed: %+v", run.Nodes)
	}
}

func TestServiceRunWithoutSubstrateFailsReadable(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	_, err := svc.RunWorkflow(ctx, "svc-test-wf")
	if err == nil || !strings.Contains(err.Error(), "no workflow subagent substrate") {
		t.Fatalf("missing substrate must fail readable, got %v", err)
	}
	// 未知运行的状态查询给可读指引。
	_, err = svc.WorkflowStatus(ctx, "wf-nope")
	if err == nil || !strings.Contains(err.Error(), "workflow_status") {
		t.Fatalf("unknown run status must point at workflow_status, got %v", err)
	}
}

func TestServiceFailedRunKeepsReport(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	doc := strings.Replace(svcTestDoc, `"exit 0"`, `"exit 3"`, 1)
	// 门失败且无 fail 分支接收 → 引擎报错中止，但报告必须落袋（point 5）。
	if _, err := svc.DefineWorkflow(ctx, doc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	runner := &fakeRunner{}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunFailed)
	})
	st, _ := svc.WorkflowStatus(ctx, res.RunID)
	run := st.Runs[0]
	if run.Error == "" {
		t.Fatal("failed run must carry the error")
	}
	if run.Report == nil || !strings.Contains(run.Report.Conclusion, "stopped") {
		t.Fatalf("failed run must keep the salvage report: %+v", run.Report)
	}
	if len(run.Nodes) < 2 {
		t.Fatalf("nodes reported before the stop must be preserved: %+v", run.Nodes)
	}
}

func TestServiceCancelRun(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	runner := &blockingRunner{release: make(chan struct{})}
	t.Cleanup(func() { runner.once.Do(func() {}) })
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !svc.CancelRun(res.RunID) {
		t.Fatal("cancel must hit the live run")
	}
	// 取消后引擎在节点边界返回：释放挡板让子代理调用返回，运行归终态。
	close(runner.release)
	waitFor(t, 2*time.Second, func() bool {
		st, err := svc.WorkflowStatus(ctx, res.RunID)
		return err == nil && len(st.Runs) == 1 && st.Runs[0].State == string(RunFailed) && st.Runs[0].Error == "cancelled"
	})
	if svc.CancelRun(res.RunID) {
		t.Fatal("cancel of a finished run still reports true")
	}
}

func TestServiceSnapshotCarriesDefinition(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.DefineWorkflow(ctx, svcTestDoc, false); err != nil {
		t.Fatalf("define: %v", err)
	}
	runner := &fakeRunner{}
	svc.AddRunnerSource(func(string) (SubagentRunner, bool) { return runner, true })
	res, err := svc.RunWorkflow(ctx, "svc-test-wf")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		snaps := svc.Snapshot()
		return len(snaps) == 1 && snaps[0].State == RunCompleted
	})
	snaps := svc.Snapshot()
	if snaps[0].ID != res.RunID || snaps[0].Def == nil || snaps[0].Def.Name != "svc-test-wf" {
		t.Fatalf("snapshot must carry id and definition: %+v", snaps[0])
	}
	if len(snaps[0].Nodes) == 0 {
		t.Fatal("snapshot must carry node states")
	}
}

// captureSink 收 WorkflowNode 事件（其余丢弃）。
type captureSink struct {
	mu            sync.Mutex
	workflowNodes []event.WorkflowNodeInfo
}

func (c *captureSink) Emit(e event.Event) {
	if e.Kind == event.WorkflowNode && e.Workflow != nil {
		c.mu.Lock()
		c.workflowNodes = append(c.workflowNodes, *e.Workflow)
		c.mu.Unlock()
	}
}

// withCallSink 用 agent 的导出入口注入与 executeOne 同构的调用上下文
// （水槽 + 发起会话），workflow_run 由此读取事件面与归属。
func withCallSink(ctx context.Context, sink event.Sink, sessionID string) context.Context {
	ctx = agent.WithToolCallContext(ctx, "call-test", sink, nil, false)
	return jobs.WithSession(ctx, sessionID)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
