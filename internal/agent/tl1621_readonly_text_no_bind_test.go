package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1621 钉（验收 1，run_loop 全链）：含裸「只读」子句的 turnInput 不再产生
// ForbidMutation（写工具不被 ConstraintGuard 拦截、实际执行到 Execute），也
// 不再前置 readonlyTurnNotice（notice 分支条件 ForbidMutation &&
// !PlanModeReadOnly 在纯文本路径下与「无冻结」等价）。输入是 heartbeat 误伤
// 实锤原文：中文分号把「只读；暂不实施」切成裸「只读」子句，tl220 的字面子
// 句匹配机制两轮修补后仍被它命中。
//
// 非文本来源由既有钉守，零回退：
//   - plan mode 冻结：internal/control/approval_e2e_test.go
//     TestPlanModeApprovalPostureMatrix（Plan → ForbidMutation → 写被 host 拦）
//   - 子代理 PlanReadOnly 继承：internal/agent/parallel_tasks_test.go
//     TestTaskToolPropagatesInheritedExecutionToSubagents
func TestReadOnlyWordingInTurnInputNoLongerBinds(t *testing.T) {
	var writes int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false, calls: &writes})
	prov := &scriptedProvider{name: "tl1621", turns: [][]provider.Chunk{
		{toolCallChunk("w1", "write_file", `{"path":"report.md"}`)},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "只读；暂不实施"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&writes); got != 1 {
		t.Fatalf("write tool executed %d times, want exactly 1 — a read-only freeze would keep it at 0", got)
	}
	// 无前置提示：本轮发给模型的 user 消息不得带 readonlyTurnNotice 前缀。
	if len(prov.requests) == 0 {
		t.Fatal("provider saw no requests")
	}
	last := prov.requests[len(prov.requests)-1]
	var userText string
	for i := len(last.Messages) - 1; i >= 0; i-- {
		if last.Messages[i].Role == provider.RoleUser {
			userText = last.Messages[i].Content
			break
		}
	}
	if strings.Contains(userText, "ForbidMutation 生效") {
		t.Fatalf("readonly turn notice must not precede the input, got user message: %q", userText)
	}
	if !strings.Contains(userText, "只读") {
		t.Fatalf("sanity: the turn input itself must reach the model verbatim, got: %q", userText)
	}
}
