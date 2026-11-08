package control

import (
	"context"
	"errors"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func manualReadinessController(t *testing.T, turns [][]provider.Chunk) (*Controller, *scriptedTurns) {
	t.Helper()
	reg := tool.NewRegistry()
	reg.Add(minimalFakeTool{name: "write_file"})
	reg.Add(minimalFakeTool{name: "read_file", readOnly: true})
	if todoWrite, ok := tool.LookupBuiltin("todo_write"); ok {
		reg.Add(todoWrite)
	}
	prov := &scriptedTurns{turns: turns}
	executor := agent.New(prov, reg, agent.NewSession("stable-system-prefix"), agent.Options{}, event.Discard)
	c := New(Options{Runner: executor, Executor: executor, Sink: event.Discard})
	t.Cleanup(c.Close)
	return c, prov
}

func syntheticUserTurnCount(messages []provider.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == provider.RoleUser && IsSyntheticUserMessage(message.Content) {
			count++
		}
	}
	return count
}

func TestStandardStopsAfterOneVisibleTurn(t *testing.T) {
	c, prov := manualReadinessController(t, [][]provider.Chunk{
		textTurn("已完成学习说明。下一步可以由你决定。"),
		textTurn("不应被隐藏续跑消费"),
	})
	if err := c.SetQualityFloor(QualityFloorStandard); err != nil {
		t.Fatalf("SetQualityFloor: %v", err)
	}

	if err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(context.Background(), "学习并解释当前内容", "学习并解释当前内容", ""); err != nil {
		t.Fatalf("standard turn returned %v", err)
	}
	if prov.call != 1 {
		t.Fatalf("provider calls = %d, want one visible turn", prov.call)
	}
	if got := syntheticUserTurnCount(c.executor.Session().Snapshot()); got != 0 {
		t.Fatalf("synthetic user turns = %d, want zero", got)
	}
}

// tl1460 语义更新（原 TestStandardStartReplyContinuesCurrentTodoInsideVisibleTurn）：
// 旧契约=可见轮结束后有未清 todo 也不续（静默停）；tl1460 验收第一条改为
// 「非 Goal 会话有待办未完成时轮末不静默停」——审查层显式续一轮（带
// 「因 X 继续」notice），续轮把 todo 清掉后放行停。本钉改为守护新契约：
// 剩余 todo 恰好被审查续轮完成，然后干净停止。
func TestStandardStartLeftoverTodoContinuesViaStopReview(t *testing.T) {
	c, prov := manualReadinessController(t, [][]provider.Chunk{
		{toolCallChunk("todo-1", "todo_write", `{"todos":[{"content":"重写第 4 节","status":"in_progress"}]}`), {Type: provider.ChunkDone}},
		textTurn("让我先列出待办，接下来会重写。"),
		{
			toolCallChunk("write-1", "write_file", `{"path":"PRD.md"}`),
			toolCallChunk("todo-2", "todo_write", `{"todos":[{"content":"重写第 4 节","status":"completed"}]}`),
			{Type: provider.ChunkDone},
		},
		textTurn("第 4 节已经完成重写。"),
	})
	if err := c.SetQualityFloor(QualityFloorStandard); err != nil {
		t.Fatalf("SetQualityFloor: %v", err)
	}
	c.executor.Session().Add(provider.Message{Role: provider.RoleAssistant, Content: "让我写完整新第 4 节。"})

	if err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(context.Background(), "开始", "开始", "开始"); err != nil {
		t.Fatalf("standard start reply returned %v", err)
	}
	// 可见轮 2 次调用（todo_write 轮 + 文本轮）+ 审查续轮 2 次（write/todo_write
	// 轮 + 文本轮）= 4；续轮清掉 todo 后审查放行，不再续。
	if prov.call != 4 {
		t.Fatalf("provider calls = %d, want 4 (visible turn + one review continuation that clears the todo)", prov.call)
	}
	for _, td := range c.executor.CanonicalTodoState() {
		if td.Status != "completed" {
			t.Fatalf("todo %q = %s after the review continuation, want completed", td.Content, td.Status)
		}
	}
	if got := c.stopReviewStreak; got != 0 {
		t.Fatalf("stop review streak = %d after a clean release, want 0", got)
	}
}

// tl1460 语义更新（原 TestStandardStartWithoutConversationDoesNotArmTodoContinuation）：
// 旧契约=无前文就不续；tl1460 下 todo 是模型本轮自己写下的工作清单，未清
// 即审查判据，与是否有前文无关。续轮每次都被 scripted 末元素重复消费、todo
// 恒不清 → 连续续到上限 5 轮后到顶转停（预算兜底可见）。
func TestStandardStartPendingTodosRunToReviewCap(t *testing.T) {
	c, prov := manualReadinessController(t, [][]provider.Chunk{
		{toolCallChunk("todo-1", "todo_write", `{"todos":[{"content":"重写第 4 节","status":"in_progress"}]}`), {Type: provider.ChunkDone}},
		textTurn("等待下一步。"),
		textTurn("不应被隐藏续跑消费"),
	})

	if err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(context.Background(), "开始", "开始", "开始"); err != nil {
		t.Fatalf("standard start reply returned %v", err)
	}
	// 可见轮 2 次 + 5 轮审查续轮（每轮 1 次调用）= 7；到顶转停后不再调用。
	if prov.call != 7 {
		t.Fatalf("provider calls = %d, want 7 (visible turn + 5 capped review continuations)", prov.call)
	}
	if got := c.stopReviewStreak; got != 0 {
		t.Fatalf("stop review streak = %d after the capped stop, want 0", got)
	}
	for _, td := range c.executor.CanonicalTodoState() {
		if td.Status == "completed" {
			t.Fatalf("todo %q = completed, want untouched pending work at the capped stop", td.Content)
		}
	}
}

// tl1460 保留原契约（让位于用户）：c.canceling=true 时审查层在续轮门前
// 让位（runStopReviewContinuations 的 CancelRequested 门），待处理的用户
// 工作优先于任何自动续跑。断言不变，只是守护方从旧机制换成 tl1460 审查层。
func TestStandardTodoContinuationYieldsToPendingUserWork(t *testing.T) {
	c, prov := manualReadinessController(t, [][]provider.Chunk{
		{toolCallChunk("todo-1", "todo_write", `{"todos":[{"content":"重写第 4 节","status":"in_progress"}]}`), {Type: provider.ChunkDone}},
		textTurn("等待下一步。"),
		textTurn("不应被隐藏续跑消费"),
	})
	c.executor.Session().Add(provider.Message{Role: provider.RoleAssistant, Content: "让我写完整新第 4 节。"})
	c.mu.Lock()
	c.canceling = true
	c.mu.Unlock()

	if err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(context.Background(), "开始", "开始", "开始"); err != nil {
		t.Fatalf("standard start reply returned %v", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want pending user work to preempt continuation", prov.call)
	}
}

func TestDeliveryStopsAtReadinessAndWaitsForExplicitRecovery(t *testing.T) {
	c, prov := manualReadinessController(t, [][]provider.Chunk{
		{toolCallChunk("write", "write_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		textTurn("已完成修改，但没有执行验证。"),
		textTurn("显式恢复才允许消费这一轮"),
	})
	if err := c.SetQualityFloor(QualityFloorDelivery); err != nil {
		t.Fatalf("SetQualityFloor: %v", err)
	}

	err := newTurnOrchestrator(c).runGoalLoopWithRawDisplay(context.Background(), "修改 main.go", "修改 main.go", "")
	var readinessErr *agent.FinalReadinessError
	if !errors.As(err, &readinessErr) {
		t.Fatalf("run error = %v, want FinalReadinessError", err)
	}
	if readinessErr.Attempts != 1 {
		t.Fatalf("readiness attempts = %d, want one completed visible turn", readinessErr.Attempts)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want one tool round plus one final answer", prov.call)
	}
	if got := syntheticUserTurnCount(c.executor.Session().Snapshot()); got != 0 {
		t.Fatalf("synthetic user turns = %d, want zero before explicit recovery", got)
	}
	if !c.executor.PrepareFinalReadinessRecovery() {
		t.Fatal("readiness failure did not preserve explicit recovery state")
	}
}
