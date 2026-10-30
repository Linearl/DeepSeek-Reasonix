package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1278（中断恢复「结果未知」标注按只读/写分级）后端合同。
//
// 范围裁定：标注/展示层最小改，不动恢复协议——433 白名单自动结算的面不变，
// 本件只做两件事：
//   ③ 模型侧恢复摘要按只读类/写类分级（只读=可安全重试，写类=先查外部状态）；
//   ④ 摘要对 outcome-unknown 条目区分两种中断形态（returned_unverified =
//      结果已产出但未能确认持久化 / no_response = 调用发出未返回）。
// 只读判定复用中断记录的 ReadOnly（beginToolRecovery 从解析后的工具盖章，
// 与 internal/tool 登记表同源）；名字兜底走 tool.SafetyOfName，fail closed。
//
// 主角是 heartbeat_task_list：登记表 ReadOnly=true，但不在 433 白名单里——
// 用户实测「只读工具为什么报结果未知」的正是它（中断后面板挂「结果未知」）。

// gradingFixture builds a session with one interrupted-call record in the
// given shape (state/finishedAt decide the tl1278④ form), as an interruption
// or a storage reopen would leave it.
func gradingFixture(t *testing.T, canonicalTool, args string, readOnly bool, state provider.ToolRunState, finishedAt int64) (*Agent, provider.ToolCall) {
	t.Helper()
	record := provider.ToolCallRecord{
		Identity: provider.ActionIdentity{
			CallID:         "call-graded",
			AttemptID:      "attempt-graded",
			CanonicalTool:  canonicalTool,
			ArgumentDigest: recoveryDigest([]byte(args)),
			ResourceScope:  "session:s-tl1278",
		},
		State:      state,
		ReadOnly:   readOnly,
		Arguments:  json.RawMessage(args),
		FinishedAt: finishedAt,
	}
	call := provider.ToolCall{ID: "call-graded", Name: canonicalTool, Arguments: args, Recovery: &record}
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "work"})
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}})
	return New(nil, tool.NewRegistry(), s, Options{}, &sideEffectTraceSink{}), call
}

// ③ 主路径（无持久 handoff）：注册表只读工具中断后记录保持未决（协议不变、
// 面板照收），但 pendingInterruptedRecovery 采集的条目带 ReadOnly=true 与
// 形态标注——恢复摘要据此分级，不再对只读工具喊「先查外部状态」。
func TestInterruptedReadOnlyRegistryToolGradedInRecoverySummary(t *testing.T) {
	a, _ := gradingFixture(t, "heartbeat_task_list", `{}`, true, provider.ToolRunStarted, 0)

	pending := a.PendingToolRecovery()
	if len(pending) != 1 {
		t.Fatalf("protocol must be untouched: registry read-only stays pending, got %d", len(pending))
	}
	view := a.pendingInterruptedRecovery()
	if view == nil || len(view.UnknownTools) != 1 {
		t.Fatalf("unknown list = %+v, want the heartbeat entry", view)
	}
	entry := view.UnknownTools[0]
	if entry.Name != "heartbeat_task_list" || !entry.ReadOnly {
		t.Fatalf("entry = %+v, want read-only graded heartbeat_task_list", entry)
	}
	if entry.OutcomeForm != outcomeFormNoResponse {
		t.Fatalf("form = %q, want %q (started, never finished)", entry.OutcomeForm, outcomeFormNoResponse)
	}
	block := interruptedRecoveryBlock(view)
	if !strings.Contains(block, "outcome_unknown_tools_read_only_retry_safe:\n- heartbeat_task_list") {
		t.Fatalf("read-only section missing the graded entry:\n%s", block)
	}
	if strings.Contains(block, "outcome_unknown_tools:\n") {
		t.Fatalf("write-class section must stay empty for a read-only call:\n%s", block)
	}
	if !strings.Contains(block, "form="+outcomeFormNoResponse) {
		t.Fatalf("block missing the no_response form marker:\n%s", block)
	}
	if !strings.Contains(block, "re-running them is safe") || !strings.Contains(block, "first inspect side effects and external state") {
		t.Fatalf("tail instruction lost the graded copy (read-only safe / write-class inspect):\n%s", block)
	}
}

// ③ 对照面：写类工具中断保持写类分级——条目不带只读标注、进 outcome_unknown_tools
// 严格段、不进只读段。忽略按钮与原警示的保留由前端测试钉（本件 FE commit）。
func TestInterruptedWriteToolKeepsStrictUnknownSection(t *testing.T) {
	a, _ := gradingFixture(t, "write_file", `{"path":"a.txt"}`, false, provider.ToolRunUnknown, 12345)

	view := a.pendingInterruptedRecovery()
	if view == nil || len(view.UnknownTools) != 1 {
		t.Fatalf("unknown list = %+v, want the write_file entry", view)
	}
	entry := view.UnknownTools[0]
	if entry.ReadOnly {
		t.Fatal("write_file must not be graded read-only")
	}
	if entry.OutcomeForm != outcomeFormReturnedUnverified {
		t.Fatalf("form = %q, want %q (finished with unknown outcome)", entry.OutcomeForm, outcomeFormReturnedUnverified)
	}
	block := interruptedRecoveryBlock(view)
	if !strings.Contains(block, "outcome_unknown_tools:\n- write_file") {
		t.Fatalf("write-class section missing the entry:\n%s", block)
	}
	if strings.Contains(block, "outcome_unknown_tools_read_only_retry_safe") {
		t.Fatalf("read-only section must stay empty for a write call:\n%s", block)
	}
	if !strings.Contains(block, "form="+outcomeFormReturnedUnverified) {
		t.Fatalf("block missing the returned_unverified form marker:\n%s", block)
	}
}

// ④ 形态判定表：started/running 且无 finish 戳 = 发出未返回；其余未决形态
// （unknown 收尾 / failed-effect_unknown）= 已结束未证实。
func TestInterruptedUnknownOutcomeFormTable(t *testing.T) {
	cases := []struct {
		name       string
		state      provider.ToolRunState
		finishedAt int64
		want       string
	}{
		{"started no finish", provider.ToolRunStarted, 0, outcomeFormNoResponse},
		{"running no finish", provider.ToolRunRunning, 0, outcomeFormNoResponse},
		{"unknown finished", provider.ToolRunUnknown, 42, outcomeFormReturnedUnverified},
		{"failed effect unknown", provider.ToolRunFailed, 42, outcomeFormReturnedUnverified},
		{"started with finish stamp", provider.ToolRunStarted, 42, outcomeFormReturnedUnverified},
	}
	for _, tc := range cases {
		got := interruptedUnknownOutcomeForm(provider.ToolCallRecord{State: tc.state, FinishedAt: tc.finishedAt})
		if got != tc.want {
			t.Fatalf("%s: form = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ③ 名字兜底：持久 handoff 里的旧格式条目（无标注、记录查无）按登记表补
// ReadOnly——只读工具进只读段；查无名字 fail closed 进写类段。durable handoff
// 本体不被改写（条目标注只落在返回的视图副本上）。
func TestHandoffLegacyEntriesGradedFromSafetyRegistry(t *testing.T) {
	a, _ := gradingFixture(t, "write_file", `{"path":"a.txt"}`, false, provider.ToolRunUnknown, 1)
	handoff := addInterruptedHandoff(a, []provider.InterruptedToolSummary{
		{ID: "legacy-hb", Name: "heartbeat_task_list"},
		{ID: "legacy-unknown-name", Name: "mcp__srv__mutate"},
	})
	view := a.transcriptInterruptedRecovery()
	if len(view.UnknownTools) != 2 {
		t.Fatalf("unknown list = %+v, want both legacy entries", view.UnknownTools)
	}
	byID := map[string]provider.InterruptedToolSummary{}
	for _, c := range view.UnknownTools {
		byID[c.ID] = c
	}
	if !byID["legacy-hb"].ReadOnly {
		t.Fatal("registry read-only fallback missing for heartbeat_task_list")
	}
	if byID["legacy-hb"].OutcomeForm != "" {
		t.Fatalf("legacy entry without a record must keep an empty form, got %q", byID["legacy-hb"].OutcomeForm)
	}
	if byID["legacy-unknown-name"].ReadOnly {
		t.Fatal("unknown name must fail closed to write-class")
	}
	block := interruptedRecoveryBlock(view)
	if !strings.Contains(block, "outcome_unknown_tools_read_only_retry_safe:\n- heartbeat_task_list") {
		t.Fatalf("graded read-only section missing the legacy entry:\n%s", block)
	}
	if !strings.Contains(block, "outcome_unknown_tools:\n- mcp__srv__mutate") {
		t.Fatalf("fail-closed entry must land in the write-class section:\n%s", block)
	}
	if !strings.Contains(block, "form=") {
		t.Fatalf("ungraded entries must not carry a form marker:\n%s", block)
	}
	if len(handoff.UnknownTools) != 2 || handoff.UnknownTools[0].ReadOnly {
		t.Fatal("durable handoff message was rewritten")
	}
}
