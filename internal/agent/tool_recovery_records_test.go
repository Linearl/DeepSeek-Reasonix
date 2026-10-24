package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func recoverySessionWithCall(id string, r *provider.ToolCallRecord) *Session {
	s := NewSession("")
	s.Messages = []provider.Message{{Role: provider.RoleAssistant, ID: "turn-1", ToolCalls: []provider.ToolCall{{ID: id, Name: "write_file", Arguments: `{"path":"x"}`, Recovery: r}}}}
	return s
}

func TestPendingToolRecoverySurvivesUserTailAndIsProviderExcluded(t *testing.T) {
	r := &provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "attempt-1", CallID: "call-1"}, State: provider.ToolRunUnknown, Arguments: json.RawMessage(`{"path":"secret"}`), ReadOnly: false}
	s := recoverySessionWithCall("call-1", r)
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)
	// A subsequent user message must not clear the durable unresolved record.
	s.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	pending := a.PendingToolRecovery()
	if len(pending) != 1 || pending[0].Identity.AttemptID != "attempt-1" {
		t.Fatalf("pending recovery after user tail = %+v", pending)
	}
	model := provider.ModelMessages(s.Snapshot())
	raw, _ := json.Marshal(model)
	if strings.Contains(string(raw), "tool_recovery") {
		t.Fatal("local recovery leaked to provider")
	}
	for _, m := range model {
		if m.ToolCalls != nil && m.ToolCalls[0].Recovery != nil {
			t.Fatal("Recovery metadata leaked into ModelMessages")
		}
	}
}

func TestSetToolRecoveryRecordRejectsStaleAttemptAndDetachesArguments(t *testing.T) {
	original := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "new"}, State: provider.ToolRunStarted, Arguments: json.RawMessage(`{"path":"safe"}`)}
	s := recoverySessionWithCall("call-1", &original)
	stale := original
	stale.Identity.AttemptID = "old"
	if s.setToolRecoveryRecord("call-1", stale) {
		t.Fatal("stale attempt replaced current recovery record")
	}
	updated := original
	updated.State = provider.ToolRunCompleted
	updated.Arguments[0] = 'X'
	if !s.setToolRecoveryRecord("call-1", updated) {
		t.Fatal("current attempt update rejected")
	}
	updated.Arguments[0] = 'Y'
	got := s.toolRecoveryRecord("call-1")
	if got == nil || got.State != provider.ToolRunCompleted || got.Arguments[0] != 'X' {
		t.Fatalf("record was not detached: %+v", got)
	}
}

func TestFinishToolRecoveryFailedWriterIsNotCompleted(t *testing.T) {
	r := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "attempt-1"}, State: provider.ToolRunStarted, ReadOnly: false}
	s := recoverySessionWithCall("call-1", &r)
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)
	a.finishToolRecovery(context.Background(), provider.ToolCall{ID: "call-1"}, toolOutcome{executed: true, output: "partial", errMsg: "write failed"})
	got := s.toolRecoveryRecord("call-1")
	if got == nil || got.State != provider.ToolRunFailed {
		t.Fatalf("failed writer recovery = %+v", got)
	}
}

// recoverySessionWithPairedResult builds the 任务 1143 形态① transcript: an
// assistant tool call whose durable record is stuck unresolved (begin
// checkpoint landed, the finish update was lost to a mid-tool crash / ledger
// reopen) while the transcript itself already carries the paired tool result.
func recoverySessionWithPairedResult(recordState provider.ToolRunState, resultContent string, resultState provider.ToolRunState) (*Session, *provider.ToolCallRecord) {
	r := &provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "attempt-1", CallID: "call-1", CanonicalTool: "dream"}, State: recordState, ReadOnly: true}
	s := NewSession("")
	s.Messages = []provider.Message{
		{Role: provider.RoleAssistant, ID: "turn-1", Content: "", ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "dream", Arguments: `{}`, Recovery: r}}},
		{Role: provider.RoleUser, Content: "汇报一下"},
	}
	result := provider.Message{Role: provider.RoleTool, ToolCallID: "call-1", Name: "dream", Content: resultContent}
	if resultState != "" {
		result.ToolRunState = resultState
	}
	s.Messages = append(s.Messages, result)
	return s, r
}

// 任务 1143 验收①：工具已成功返回（转录配对 completed 结果）+ 纯文本收尾的
// 回合，残留的 started 记录不得再被采集成「中断/结果未知」。
func TestPendingToolRecoveryDropsCallWithPairedCompletedResult(t *testing.T) {
	s, _ := recoverySessionWithPairedResult(provider.ToolRunStarted, `{"saved":5}`, provider.ToolRunCompleted)
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)
	if pending := a.PendingToolRecovery(); len(pending) != 0 {
		t.Fatalf("paired completed result must not surface as interrupted recovery: %+v", pending)
	}
}

// 任务 1143 验收②：工具调用未返回（无结果消息，或仅 interrupted 占位）仍按
// 真中断上报——真中断不漏。
func TestPendingToolRecoveryKeepsGenuinelyInterruptedCall(t *testing.T) {
	// 无任何结果消息。
	s := recoverySessionWithCall("call-1", &provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "attempt-1", CallID: "call-1"}, State: provider.ToolRunStarted, ReadOnly: false})
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)
	if pending := a.PendingToolRecovery(); len(pending) != 1 {
		t.Fatalf("call without any result must stay pending: %+v", pending)
	}
	// 仅占位结果：不是执行证据，不改变真中断判定。
	s2, _ := recoverySessionWithPairedResult(provider.ToolRunStarted, "[no result: the previous turn was interrupted before this tool call completed]", "")
	a2 := New(nil, tool.NewRegistry(), s2, Options{}, event.Discard)
	if pending := a2.PendingToolRecovery(); len(pending) != 1 {
		t.Fatalf("interrupted placeholder must not count as a returned result: %+v", pending)
	}
}

// 写侧结算：回合边界（beginToolRecovery 前置步骤）把转录已反驳的残留记录按
// 证据改章——state 落为结果态，resolution 记 paired_transcript_result，宿主
// 来源；占位/无结果的真中断记录保持原样。
func TestSettlePairedTranscriptResultsRestampsStaleRecord(t *testing.T) {
	s, _ := recoverySessionWithPairedResult(provider.ToolRunStarted, `{"saved":5}`, provider.ToolRunCompleted)
	a := New(nil, tool.NewRegistry(), s, Options{}, event.Discard)
	a.settlePairedTranscriptResults()
	got := s.toolRecoveryRecord("call-1")
	if got == nil || got.State != provider.ToolRunCompleted {
		t.Fatalf("stale record must be re-stamped from the paired result: %+v", got)
	}
	if got.Resolution != pairedTranscriptResultResolution || got.ResolutionSource != "host" || got.ResolvedAt == 0 {
		t.Fatalf("settlement metadata missing: resolution=%q source=%q at=%d", got.Resolution, got.ResolutionSource, got.ResolvedAt)
	}
	// 结算后同样退出 pending 面。
	if pending := a.PendingToolRecovery(); len(pending) != 0 {
		t.Fatalf("settled record must leave the pending set: %+v", pending)
	}
}

// 端到端接线：工具成功+纯文本收尾的回合之后，若记录以 started 残留（模拟
// finish 前进程死亡后的重载复活），下一回合开始时必须被转录证据结算——面板
// 与回合状态（recovery_required）都不再误报。
func TestNewTurnSettlesStaleRecordOfPairedCompletedCall(t *testing.T) {
	reg := tool.NewRegistry()
	var calls int32
	reg.Add(fakeTool{name: "dream", readOnly: true, calls: &calls})
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "dream", `{}`), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "本轮 dream 已保存 5 条。"}, {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "继续。"}, {Type: provider.ChunkDone}},
	}}
	s := NewSession("")
	a := New(prov, reg, s, Options{}, event.Discard)
	ctx := withNoClosedLoop(context.Background())
	if err := a.Run(ctx, "跑一轮 dream"); err != nil {
		t.Fatalf("Run turn 1: %v", err)
	}
	rec := s.toolRecoveryRecord("c1")
	if rec == nil || rec.State != provider.ToolRunCompleted {
		t.Fatalf("successful dream call must settle completed, got %+v", rec)
	}
	// 模拟中断误标形态：finish 更新丢失，记录回退为 started（重载复活）。
	stale := *rec
	stale.State = provider.ToolRunStarted
	stale.Resolution = ""
	stale.ResolvedAt = 0
	if !s.setToolRecoveryRecord("c1", stale) {
		t.Fatal("failed to stage the stale record")
	}
	if pending := a.PendingToolRecovery(); len(pending) != 0 {
		t.Fatalf("形态①（结果已返回）不得因残留 started 被采集成中断: %+v", pending)
	}
	if err := a.Run(ctx, "继续"); err != nil {
		t.Fatalf("Run turn 2: %v", err)
	}
	final := s.toolRecoveryRecord("c1")
	if final == nil || final.State != provider.ToolRunCompleted || final.Resolution != pairedTranscriptResultResolution {
		t.Fatalf("new turn must re-stamp the stale record from the paired result, got %+v", final)
	}
}
