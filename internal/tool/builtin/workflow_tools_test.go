package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 任务 934 验收：workflow_* 工具面——seam 未注入的可读失败、参数校验、
// 引擎校验错误原样透传、读写审批分类（ReadOnly 与 safety 表一致）。

const toolsTestDoc = `
version: 1
name: tools-test-wf
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

// fakeWorkflowService 记录调用并回固定结果。
type fakeWorkflowService struct {
	defineRes WorkflowDefineResult
	defineErr error
	listRes   WorkflowListResult
	getRes    WorkflowGetResult
	getErr    error
	runRes    WorkflowRunResult
	runErr    error
	statusRes WorkflowStatusResult
	statusErr error

	lastDoc       string
	lastOverwrite bool
	lastRunName   string
	lastStatusID  string
}

func (f *fakeWorkflowService) DefineWorkflow(_ context.Context, doc string, overwrite bool) (WorkflowDefineResult, error) {
	f.lastDoc, f.lastOverwrite = doc, overwrite
	return f.defineRes, f.defineErr
}
func (f *fakeWorkflowService) ListWorkflows(context.Context) (WorkflowListResult, error) {
	return f.listRes, nil
}
func (f *fakeWorkflowService) GetWorkflow(_ context.Context, name string) (WorkflowGetResult, error) {
	_ = name
	return f.getRes, f.getErr
}
func (f *fakeWorkflowService) RunWorkflow(_ context.Context, name string) (WorkflowRunResult, error) {
	f.lastRunName = name
	return f.runRes, f.runErr
}
func (f *fakeWorkflowService) WorkflowStatus(_ context.Context, runID string) (WorkflowStatusResult, error) {
	f.lastStatusID = runID
	return f.statusRes, f.statusErr
}

func withTestWorkflowService(t *testing.T) *fakeWorkflowService {
	t.Helper()
	svc := &fakeWorkflowService{}
	SetWorkflowService(svc)
	t.Cleanup(func() { SetWorkflowService(nil) })
	return svc
}

func execWorkflowTool(t *testing.T, tool toolShim, args string) string {
	t.Helper()
	out, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("execute %s: %v", tool.Name(), err)
	}
	return out
}

// toolShim 避免测试直接依赖具体结构体名。
type toolShim interface {
	Name() string
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

func TestWorkflowToolsFailReadableWithoutService(t *testing.T) {
	SetWorkflowService(nil)
	for _, tl := range []toolShim{workflowDefine{}, workflowList{}, workflowGet{}, workflowRun{}, workflowStatus{}} {
		_, err := tl.Execute(context.Background(), json.RawMessage(`{}`))
		if err == nil || !strings.Contains(err.Error(), "no workflow service") {
			t.Fatalf("%s without service must fail readable, got %v", tl.Name(), err)
		}
	}
}

func TestWorkflowDefineToolContract(t *testing.T) {
	svc := withTestWorkflowService(t)
	svc.defineRes = WorkflowDefineResult{Name: "wf", Created: true, Nodes: 2, Edges: 1, Gates: 1}

	// 缺 document 必拒。
	if _, err := (workflowDefine{}).Execute(context.Background(), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "document is required") {
		t.Fatalf("missing document must be named, got %v", err)
	}
	out := execWorkflowTool(t, workflowDefine{}, `{"document":"version: 1\nname: wf\nnodes: []\n","overwrite":true}`)
	if svc.lastOverwrite != true || !strings.Contains(svc.lastDoc, "name: wf") {
		t.Fatalf("define args not forwarded: doc=%q overwrite=%v", svc.lastDoc, svc.lastOverwrite)
	}
	if !strings.Contains(out, `"created": true`) {
		t.Fatalf("define receipt unexpected: %s", out)
	}

	// 引擎校验错误原样透传（agent 自纠线索不加壳）。
	svc.defineErr = errDefineReject
	if _, err := (workflowDefine{}).Execute(context.Background(), json.RawMessage(`{"document":"x"}`)); err != errDefineReject {
		t.Fatalf("engine validation error must pass through verbatim, got %v", err)
	}
}

var errDefineReject = errFake("workflow: node 1: field \"prompt\" is not valid on a gate node")

type errFake string

func (e errFake) Error() string { return string(e) }

func TestWorkflowGetListStatusTools(t *testing.T) {
	svc := withTestWorkflowService(t)
	svc.getRes = WorkflowGetResult{Name: "wf", Document: `{"name":"wf"}`}
	svc.listRes = WorkflowListResult{Workflows: []WorkflowSummary{{Name: "wf", Nodes: 2}}}
	svc.statusRes = WorkflowStatusResult{Runs: []WorkflowRunView{{RunID: "wf-1", Workflow: "wf", State: "completed"}}}

	// get：缺 name 必拒；带 name 透传。
	if _, err := (workflowGet{}).Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("get without name must fail")
	}
	out := execWorkflowTool(t, workflowGet{}, `{"name":"wf"}`)
	if !strings.Contains(out, `"name": "wf"`) {
		t.Fatalf("get receipt unexpected: %s", out)
	}

	// list：无参数。
	out = execWorkflowTool(t, workflowList{}, `{}`)
	if !strings.Contains(out, "workflow_list") && !strings.Contains(out, `"name": "wf"`) {
		t.Fatalf("list receipt unexpected: %s", out)
	}

	// status：run_id 透传；空参数（无 run_id）合法。
	execWorkflowTool(t, workflowStatus{}, `{"run_id":"wf-1"}`)
	if svc.lastStatusID != "wf-1" {
		t.Fatalf("status run_id not forwarded: %q", svc.lastStatusID)
	}
	execWorkflowTool(t, workflowStatus{}, `{}`)
	if svc.lastStatusID != "" {
		t.Fatalf("status without run_id must pass empty, got %q", svc.lastStatusID)
	}
}

func TestWorkflowRunToolContract(t *testing.T) {
	svc := withTestWorkflowService(t)
	svc.runRes = WorkflowRunResult{RunID: "wf-abc", Workflow: "wf", State: "running", Hint: "poll workflow_status"}

	if _, err := (workflowRun{}).Execute(context.Background(), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("run without name must be named, got %v", err)
	}
	out := execWorkflowTool(t, workflowRun{}, `{"name":"wf"}`)
	if svc.lastRunName != "wf" {
		t.Fatalf("run name not forwarded: %q", svc.lastRunName)
	}
	if !strings.Contains(out, `"runId": "wf-abc"`) {
		t.Fatalf("run receipt unexpected: %s", out)
	}
	svc.runErr = errFake("boom")
	if _, err := (workflowRun{}).Execute(context.Background(), json.RawMessage(`{"name":"wf"}`)); err == nil {
		t.Fatal("run error must surface")
	}
}

func TestWorkflowToolsApprovalClassification(t *testing.T) {
	// 写类默认需审批：define/run 非只读；list/get/status 只读。
	// safety 表（单一事实源）与 ReadOnly() 的一致性由 safety_contract_test
	// 全量钉死，这里只钉工具本身的读写意图，防两处同改同错。
	readOnly := map[string]bool{
		(workflowDefine{}.Name()): workflowDefine{}.ReadOnly(),
		(workflowList{}.Name()):   workflowList{}.ReadOnly(),
		(workflowGet{}.Name()):    workflowGet{}.ReadOnly(),
		(workflowRun{}.Name()):    workflowRun{}.ReadOnly(),
		(workflowStatus{}.Name()): workflowStatus{}.ReadOnly(),
	}
	expect := map[string]bool{
		"workflow_define": false, "workflow_list": true, "workflow_get": true,
		"workflow_run": false, "workflow_status": true,
	}
	for name, ro := range readOnly {
		if ro != expect[name] {
			t.Fatalf("%s ReadOnly=%v, want %v", name, ro, expect[name])
		}
	}
}
