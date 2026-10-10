package agent

// 任务 717（中断的工具调用记录自动清理）：用户发新消息继续对话 = 对遗留中断
// 记录的忽略表态；该 turn 结束时，turn 开始前就已 pending 的记录被结算为
// dismissed_by_new_turn —— 面板输入（PendingToolRecovery）随之清空，折叠条
// 不再渲染。这里钉住四条生命周期契约：
//  1. Run 全链路：turn 结束 → pre-turn 记录结算（用户可见验收的主路径）；
//  2. turn 内已结算的记录不被覆盖（白名单判定优先于更钝的新回合清理）；
//  3. 本 turn 自己产生的中断不在清理范围（下一个 turn 才接手）；
//  4. 空 prior / nil 接收者是 no-op（无表态、无动作）。

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// interruptedRecord builds the record shape the user actually saw
// （「bash · 结果未知（已记录）」）: an interrupted call that no automatic
// path has judged yet.
func interruptedRecord(t *testing.T, attempt, canonicalTool, args string, state provider.ToolRunState, readOnly bool) provider.ToolCall {
	t.Helper()
	record := provider.ToolCallRecord{
		Identity: provider.ActionIdentity{
			CallID:         "call-" + attempt,
			AttemptID:      attempt,
			CanonicalTool:  canonicalTool,
			ArgumentDigest: recoveryDigest([]byte(args)),
			ResourceScope:  "session:s-717",
		},
		State:     state,
		ReadOnly:  readOnly,
		Arguments: json.RawMessage(args),
	}
	return provider.ToolCall{ID: "call-" + attempt, Name: canonicalTool, Arguments: args, Recovery: &record}
}

// interruptedWriteBash is the whitelist-proof shape: an interrupted
// write-capable bash call that stays pending through construction.
func interruptedWriteBash(t *testing.T, attempt string) provider.ToolCall {
	t.Helper()
	return interruptedRecord(t, attempt, "bash", `{"command":"printf x >> tasklist.md"}`, provider.ToolRunUnknown, false)
}

// newAgentWithRecords mounts the given interrupted-call records on a fresh
// session backed by a scripted text-only provider.
func newAgentWithRecords(t *testing.T, prov *recordingProvider, calls ...provider.ToolCall) *Agent {
	t.Helper()
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "work"})
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: calls})
	return New(prov, tool.NewRegistry(), s, Options{}, event.Discard)
}

// 契约 1（验收主路径）：新 turn 结束，turn 前遗留的未决记录被结算 ——
// PendingToolRecovery 清空（面板判据消失，折叠条不再有输入），记录事实保留、
// resolution 可审计（dismissed_by_new_turn / source=user）。
func TestNewTurnEndSettlesPriorPendingRecords(t *testing.T) {
	prov := &recordingProvider{reply: "好，继续"}
	a := newAgentWithRecords(t, prov, interruptedWriteBash(t, "attempt-old"))

	prior := a.PendingToolRecovery()
	if len(prior) != 1 {
		t.Fatalf("pre-turn pending = %d, want 1", len(prior))
	}
	if err := a.Run(context.Background(), "忽略它，继续"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := a.PendingToolRecovery(); len(got) != 0 {
		t.Fatalf("pending after turn = %d, want 0 (fold-out must not outlive the move-on)", len(got))
	}
	r := a.Session().toolRecoveryRecord("call-attempt-old")
	if r == nil {
		t.Fatal("record lost — settlement must keep the audit facts")
	}
	if r.Identity.CanonicalTool != "bash" || r.Identity.ArgumentDigest == "" {
		t.Fatalf("record facts scrubbed: %+v", r.Identity)
	}
	if r.State != provider.ToolRunNotStarted || r.Resolution != newTurnDismissalResolution {
		t.Fatalf("verdict = state=%s resolution=%q, want not_started/%s", r.State, r.Resolution, newTurnDismissalResolution)
	}
	if r.ResolutionSource != "user" || r.ResolvedAt == 0 {
		t.Fatalf("provenance = source=%q at=%d, want user/<set>", r.ResolutionSource, r.ResolvedAt)
	}
	if r.Arguments == nil {
		t.Fatal("arguments must stay in the session record")
	}
}

// 契约 2：turn 内已结算的记录不被新回合清理覆盖 —— 白名单的 side_effect_free
// 判定比 dismissed_by_new_turn 更准，钝结算不得抹掉它（只结算仍 pending 的）。
func TestNewTurnSettlementDoesNotClobberMidTurnResolutions(t *testing.T) {
	readCall := interruptedRecord(t, "attempt-read", "read_file", `{"path":"a.txt"}`, provider.ToolRunStarted, true)
	frozen := interruptedWriteBash(t, "attempt-frozen")
	a := newAgentWithRecords(t, &recordingProvider{reply: "ok"}, readCall, frozen)

	// 构造结算（519 前移）已把 read_file 判「未生效」；把它冻结回未决态以模拟
	// 「turn 开始时它还 pending」——与本文件其余 fixture 的 refreeze 同型。
	if !a.Session().setToolRecoveryRecord("call-attempt-read", *readCall.Recovery) {
		t.Fatal("could not refreeze the read_file record")
	}

	prior := a.PendingToolRecovery()
	if len(prior) != 2 {
		t.Fatalf("pre-turn pending = %d, want 2", len(prior))
	}
	// turn 内：白名单把 read_file 判「未生效」（beginToolRecovery 的既有路径）。
	a.autoResolveSideEffectFreeRecord(*readCall.Recovery)

	if n := a.ResolveInterruptedByNewTurn(prior); n != 1 {
		t.Fatalf("settled = %d, want 1 (only the still-pending write bash)", n)
	}
	r := a.Session().toolRecoveryRecord("call-attempt-read")
	if r == nil || r.Resolution != sideEffectFreeResolution || r.ResolutionSource != "host" {
		t.Fatalf("mid-turn verdict clobbered: %+v", r)
	}
	if got := a.PendingToolRecovery(); len(got) != 0 {
		t.Fatalf("pending after settlement = %d, want 0", len(got))
	}
}

// 契约 3：结算只针对 pre-turn 快照 —— 本 turn 自己留下的中断记录保持未决，
// 面板继续显示（下一个 turn 结束才接手清理）。
func TestNewTurnSettlementKeepsRecordsThisTurnCreated(t *testing.T) {
	a, sink, call := sideEffectFreeFixture(t, "bash", `{"command":"printf x >> old.md"}`, false)
	refreezeInterruptedRecord(t, a, sink, call)

	prior := a.PendingToolRecovery()
	if len(prior) != 1 {
		t.Fatalf("pre-turn pending = %d, want 1", len(prior))
	}
	// turn 进行中又产生一个新的中断记录（新 attempt）。
	fresh := interruptedWriteBash(t, "attempt-fresh")
	a.Session().Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{fresh}})

	if n := a.ResolveInterruptedByNewTurn(prior); n != 1 {
		t.Fatalf("settled = %d, want 1 (prior record only)", n)
	}
	got := a.PendingToolRecovery()
	if len(got) != 1 || got[0].Identity.AttemptID != "attempt-fresh" {
		t.Fatalf("pending after settlement = %+v, want only attempt-fresh", got)
	}
}

// 契约 4：无 prior（turn 前没有任何未决记录）时结算是 no-op；nil 接收者安全。
func TestNewTurnSettlementEmptyPriorIsNoop(t *testing.T) {
	a := newAgentWithRecords(t, &recordingProvider{reply: "ok"})
	if n := a.ResolveInterruptedByNewTurn([]provider.ToolCallRecord{}); n != 0 {
		t.Fatalf("empty prior settled %d records, want 0", n)
	}
	if n := a.ResolveInterruptedByNewTurn(nil); n != 0 {
		t.Fatalf("nil prior settled %d records, want 0", n)
	}
	var nilAgent *Agent
	if n := nilAgent.ResolveInterruptedByNewTurn(nil); n != 0 {
		t.Fatalf("nil agent settled %d, want 0", n)
	}
}
