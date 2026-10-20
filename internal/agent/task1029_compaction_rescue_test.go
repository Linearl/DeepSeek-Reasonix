package agent

// 任务1029: 压缩降级路径互堵致超窗会话死循环熔断。
// 三层修复各一组验收：
// ① over-ceiling 截断救援失败后换道滑窗（死循环不再发生、换道可达且
//    fail-visible）；
// ② truncateView 的消息级/内容级兜底（巨 unit 中部省略留头尾）；
// ③ tool 结果写入历史时按 maxToolOutputBytes 封顶（源头侧）。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// overWindowDeadlockFixture 复刻 1029 取证样本的死区形态：视口由最后
// minRecentKeep=2 条强制保护的巨消息顶过物理窗口——elide（只到 protect）
// 与 drop（最老 unit 已被保护）两级全部截不动，旧 rescueByTruncation 只能
// 报 "truncated view still >= hard"（实测样本只差 18K 却永远失败）。
func overWindowDeadlockFixture() *Session {
	giant := strings.Repeat("word ", 20000) // ~100K chars ≈ 25K tokens @ 4 chars/token
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: "task"},
		{Role: provider.RoleAssistant, Content: giant},
		{Role: provider.RoleAssistant, Content: giant},
	}
	return &Session{Messages: msgs}
}

// 验收①主链路：模拟超窗会话的 rescue 链路可达成功——summary 永久失败时
// Prepare(Overflow) 不再返回 ErrCompactionRequired，而是返回一个低于 hard
// 也低于滑窗 cap target 的视图；canonical 存储一字不丢（投影语义，非删除）。
func TestTask1029OverCeilingRescueChainReachesSlidingWindow(t *testing.T) {
	sess := overWindowDeadlockFixture()
	a := agentOverForceWindow(t, &fakeProvider{streamErr: errors.New("provider down")}, sess, 40_000)
	hard := a.hardInputCeiling()
	capTarget := slidingWindowCapTarget(a.effectiveContextWindow())

	if err := prepareContext(context.Background(), a, CompactionTriggerOverflow); err != nil {
		t.Fatalf("prepare = %v, want the rescue chain to land an admissible view (死循环不再发生)", err)
	}
	view := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	if view >= hard {
		t.Fatalf("view = %d tokens, still at or above the hard ceiling %d", view, hard)
	}
	if view >= capTarget {
		t.Fatalf("view = %d tokens, want under the cap target %d (产出<target)", view, capTarget)
	}
	if got := len(sess.Messages); got != 4 {
		t.Fatalf("canonical messages = %d, want 4 — rescue must project, never delete", got)
	}
}

// 验收①换道可达：当巨消息属于内容级省略刻意跳过的类别（pinned context
// revision，用户显式锚定的保留内容）时，截断道真的截不动——此时必须换道
// 滑窗，且两道都失败时保持 fail-visible（绝不把发不出去的视图谎报成功），
// 换道尝试以滑窗通知可见。
func TestTask1029LaneSwitchReachableAndFailVisible(t *testing.T) {
	giant := "<pinned_context_revision rev=\"1\">\n" + strings.Repeat("keep ", 20000) + "\n</pinned_context_revision>"
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: giant},
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: giant},
	}
	sess := &Session{Messages: msgs}
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, sess, 40_000)
	hard := a.hardInputCeiling()
	if est := a.estimatedVisibleRequestTokens(a.modelVisibleMessages()); est < hard {
		t.Fatalf("fixture view = %d tokens, under the hard ceiling %d — not exercised", est, hard)
	}

	var notices []event.Event
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeSlidingWindowCap {
			notices = append(notices, e)
		}
	})

	m := a.contextManager()
	_, err := m.rescueByTruncationOrSlidingWindow(ContextPreparePolicy{Trigger: CompactionTriggerOverflow}, hard, errors.New("compaction kept failing"))
	if !errors.Is(err, ErrCompactionRequired) {
		t.Fatalf("rescue = %v, want ErrCompactionRequired (fail-visible, 换道也不谎报)", err)
	}
	if len(notices) == 0 {
		t.Fatal("no sliding-window-cap notice — the lane switch must be visible")
	}
	if view := a.estimatedVisibleRequestTokens(a.modelVisibleMessages()); view < hard {
		// pinned revision 被四道全部保护，视口保持超窗是预期；关键是不崩、
		// 不装假投影、错误如实上抛给熔断器。
		t.Fatalf("view = %d tokens unexpectedly under the ceiling %d — pinned content must not be silently cut", view, hard)
	}
}

// 验收②：巨 unit 消息级截断生效——前两级截不动时，超阈巨消息被中部省略，
// 头尾逐字保留且带可识别标记；已省略的消息重复 rescue 不再叠加（幂等）。
func TestTask1029TruncateElidesOversizedContent(t *testing.T) {
	sess := overWindowDeadlockFixture()
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, sess, 40_000)
	target := a.compactTrigger()

	visible, _ := a.visibleInputForFold(a.sess.compactionState, sess.Messages, 0)
	projected, affected := a.truncateView(visible, target)
	if affected == 0 {
		t.Fatal("truncateView affected nothing — the content-level rung never fired")
	}
	if after := a.estimatedVisibleRequestTokens(projected); after >= target {
		t.Fatalf("projected view = %d tokens, want under target %d", after, target)
	}
	elided := 0
	for _, m := range projected {
		switch {
		case m.Role == provider.RoleSystem:
			if m.Content != "sys" {
				t.Fatalf("system message was touched: %q", m.Content)
			}
		case m.Role == provider.RoleAssistant:
			if !strings.HasPrefix(m.Content, contentElideMarkerPrefix) {
				t.Fatalf("giant message not elided with the marker: %.80q", m.Content)
			}
			if !strings.Contains(m.Content, "\nword ") || !strings.HasSuffix(m.Content, "word ") {
				t.Fatalf("elided message lost its head/tail verbatim: %.120q", m.Content)
			}
			elided++
		}
	}
	if elided != 2 {
		t.Fatalf("elided giant messages = %d, want 2", elided)
	}

	// 幂等：已省略的消息不会被再次切（也无消息可切时 no-op）。
	if _, second := a.truncateView(projected, target); second != 0 {
		t.Fatalf("second pass affected messages, want 0 (idempotent)")
	}
}

// 验收③：tool 结果写入历史时的大小上限——超 maxToolOutputBytes 的输出按
// 工具感知策略封顶，截掉的部分以 RawContent 本地留存；低于上限的输出字节
// 级原样直通且不发通知。
func TestTask1029StoreToolResultCapsAtWrite(t *testing.T) {
	sess := foldableSessionOverForce(1)
	a := agentOverForce(t, &fakeProvider{reply: "digest"}, sess)
	var notices []event.Event
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.Notice && strings.Contains(e.Text, "truncated") {
			notices = append(notices, e)
		}
	})

	huge := strings.Repeat("x", 100_000)
	a.storeBatchToolResult(context.Background(), provider.ToolCall{ID: "call-1", Name: "bash", Arguments: "{}"}, toolOutcome{output: huge, executed: true})
	stored, _ := a.sess.conversation.snapshotMessagesVersion()
	last := stored[len(stored)-1]
	if last.Role != provider.RoleTool {
		t.Fatalf("last message role = %q, want tool", last.Role)
	}
	if len(last.Content) > maxToolOutputBytes {
		t.Fatalf("stored content = %d bytes, want <= %d (源头封顶)", len(last.Content), maxToolOutputBytes)
	}
	if last.RawContent != huge {
		t.Fatalf("RawContent = %d bytes, want the full %d-byte original for local paging", len(last.RawContent), len(huge))
	}
	if len(notices) == 0 {
		t.Fatal("no truncation notice for the capped result")
	}

	a.storeBatchToolResult(context.Background(), provider.ToolCall{ID: "call-2", Name: "bash", Arguments: "{}"}, toolOutcome{output: "ok"})
	stored, _ = a.sess.conversation.snapshotMessagesVersion()
	if got := stored[len(stored)-1].Content; got != "ok" {
		t.Fatalf("under-cap content = %q, want byte-identical passthrough", got)
	}
}
