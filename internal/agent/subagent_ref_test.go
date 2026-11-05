package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// tl1434 验收 harness —— 工作流节点「工作过程」只读回放的登记面（agent 侧）：
// ref sink 的 ctx 携带回读 + RunProfileSpec 在 ref 生成处的真实登记接线。
// 降级面（无 sink / ephemeral 空 ref）必须 no-op，不伪造身份。

func TestSubagentRefSinkRoundtrip(t *testing.T) {
	ctx := WithSubagentRefSink(context.Background())
	if got := SubagentRef(ctx); got != "" {
		t.Fatalf("fresh sink must read empty, got %q", got)
	}
	RecordSubagentRef(ctx, "sa_20261106_000000_abc")
	if got := SubagentRef(ctx); got != "sa_20261106_000000_abc" {
		t.Fatalf("SubagentRef = %q, want the recorded ref", got)
	}
}

func TestSubagentRefSinkNoSinkAndEmptyRefAreNoop(t *testing.T) {
	// 无 sink：普通委派路径，Record 无处落、读恒空。
	plain := context.Background()
	RecordSubagentRef(plain, "sa_x")
	if got := SubagentRef(plain); got != "" {
		t.Fatalf("sinkless ctx must stay empty, got %q", got)
	}
	// 有 sink 但空 ref（ephemeral 运行）：不覆盖、不伪造。
	ctx := WithSubagentRefSink(context.Background())
	RecordSubagentRef(ctx, "")
	if got := SubagentRef(ctx); got != "" {
		t.Fatalf("empty ref must be a no-op, got %q", got)
	}
}

// RunProfileSpec 接线钉：持久化父会话下，ref 生成处必须把 ref 登记进派发方
// 装的 sink（工作流 runner 就靠它建立 nodeId→执行实例关联）。
func TestRunProfileSpecRecordsSubagentRefIntoSink(t *testing.T) {
	rec := &recordSink{}
	ctx := withCallContext(context.Background(), "task-1434", rec, nil, false)
	ctx = WithParentSession(ctx, "sess-1434")
	ctx = WithSubagentRefSink(ctx)
	task := newTestTaskTool(t, reasoningTextProvider{}, tool.NewRegistry(), "sys", "", "", nil)

	out, err := task.RunProfileSpec(ctx, ProfileExecSpec{
		Task: TaskSpec{Objective: "do the thing"}, Grant: CapabilityGrant{AllowNoTools: true},
		Worker: WorkerSpec{Kind: "task", Name: "task", SystemPrompt: "sys"},
	})
	if err != nil {
		t.Fatalf("RunProfileSpec: %v", err)
	}
	if !strings.Contains(out, "final answer") {
		t.Fatalf("result = %q, want the child's final answer", out)
	}
	if ref := SubagentRef(ctx); !validSubagentRef(ref) {
		t.Fatalf("RunProfileSpec recorded no valid ref into the sink (got %q)", ref)
	}
}

// 降级钉：ephemeral 运行（无持久化父会话）不产 ref，sink 必须保持为空——
// 工作流查看面按空 ref 降级为「无关联」，绝不拿到伪身份。
func TestRunProfileSpecEphemeralRunLeavesSinkEmpty(t *testing.T) {
	rec := &recordSink{}
	ctx := withCallContext(context.Background(), "task-1434-eph", rec, nil, false)
	ctx = WithSubagentRefSink(ctx)
	task := newTestTaskTool(t, reasoningTextProvider{}, tool.NewRegistry(), "sys", "", "", nil)

	if _, err := task.RunProfileSpec(ctx, ProfileExecSpec{
		Task: TaskSpec{Objective: "do the thing"}, Grant: CapabilityGrant{AllowNoTools: true},
		Worker: WorkerSpec{Kind: "task", Name: "task", SystemPrompt: "sys"},
	}); err != nil {
		t.Fatalf("RunProfileSpec: %v", err)
	}
	if ref := SubagentRef(ctx); ref != "" {
		t.Fatalf("ephemeral run must leave the sink empty, got %q", ref)
	}
}
