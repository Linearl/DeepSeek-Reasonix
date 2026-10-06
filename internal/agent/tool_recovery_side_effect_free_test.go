package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// sideEffectTraceSink records the task-433 auto-resolution notices so tests
// can assert the「留痕」contract: every auto-judged call leaves one notice
// naming the tool and the verdict (never arguments or output).
type sideEffectTraceSink struct {
	fail    bool
	notices []event.Event
}

func (*sideEffectTraceSink) Emit(event.Event) {}
func (s *sideEffectTraceSink) EmitChecked(e event.Event) error {
	if e.Kind == event.Notice && e.Code == noticeCodeSideEffectFreeAutoResolved {
		s.notices = append(s.notices, e)
	}
	if s.fail && e.RecoveryCheckpoint {
		return errors.New("injected storage failure")
	}
	return nil
}

// sideEffectFreeFixture builds a session carrying one interrupted-call record
// (as beginToolRecovery leaves it: started, outcome not yet known).
func sideEffectFreeFixture(t *testing.T, canonicalTool, args string, readOnly bool) (*Agent, *sideEffectTraceSink, provider.ToolCall) {
	t.Helper()
	sink := &sideEffectTraceSink{}
	record := provider.ToolCallRecord{
		Identity: provider.ActionIdentity{
			CallID:         "call-interrupted",
			AttemptID:      "attempt-interrupted",
			CanonicalTool:  canonicalTool,
			ArgumentDigest: recoveryDigest([]byte(args)),
			ResourceScope:  "session:s-433",
		},
		State:     provider.ToolRunStarted,
		ReadOnly:  readOnly,
		Arguments: json.RawMessage(args),
	}
	call := provider.ToolCall{ID: "call-interrupted", Name: canonicalTool, Arguments: args, Recovery: &record}
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "work"})
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{call}})
	return New(nil, tool.NewRegistry(), s, Options{}, sink), sink, call
}

// unknownOutcome mirrors what an interruption produces: the call ran into a
// cancelled/deadline context, so its effect cannot be classified locally.
func unknownOutcome() toolOutcome {
	return toolOutcome{executed: true, errMsg: "context cancelled", runState: provider.ToolRunUnknown}
}

// refreezeInterruptedRecord restores the fixture record to its unresolved
// stored shape (started, verdict pending) and drops the construction-time
// settle notice. Task 519: agent.New settles leftover whitelisted records at
// construction, so the finishToolRecovery tests re-freeze the record (and the
// notice counter) afterwards to exercise the on-interruption path in
// isolation: "this interruption left exactly one trace".
func refreezeInterruptedRecord(t *testing.T, a *Agent, sink *sideEffectTraceSink, call provider.ToolCall) {
	t.Helper()
	frozen := provider.ToolCallRecord{
		Identity:  call.Recovery.Identity,
		State:     provider.ToolRunStarted,
		ReadOnly:  call.Recovery.ReadOnly,
		Arguments: append(json.RawMessage(nil), call.Recovery.Arguments...),
	}
	if !a.Session().setToolRecoveryRecord(call.ID, frozen) {
		t.Fatal("could not refreeze the interrupted record")
	}
	sink.notices = nil
}

func writePlanFor(id string) *toolCallPlan {
	return &toolCallPlan{
		call:     provider.ToolCall{ID: id, Name: "write_file", Arguments: `{}`},
		permName: "write_file",
		permArgs: json.RawMessage(`{}`),
		cctx:     context.Background(),
	}
}

func addInterruptedHandoff(a *Agent, unknown []provider.InterruptedToolSummary) *provider.InterruptedTurnRecovery {
	r := &provider.InterruptedTurnRecovery{Pending: true, UnknownTools: unknown}
	a.Session().Add(provider.Message{Role: provider.RoleTool, LocalOnly: true, InterruptedTurn: r})
	return r
}

// 验收①（task 433）：模拟中断只读 bash → 记录自动判「未生效」，面板判据
// （PendingToolRecovery）为空、围栏不再拦截后续写、notice 留痕、prompt 收尾
// 把它归入 not_started（可立即重发，自动续轮）。
func TestInterruptedReadOnlyBashAutoResolvesAndContinues(t *testing.T) {
	a, sink, call := sideEffectFreeFixture(t, "bash", `{"command":"ls -la"}`, false)
	refreezeInterruptedRecord(t, a, sink, call)
	a.finishToolRecovery(context.Background(), call, unknownOutcome())

	if got := a.PendingToolRecovery(); len(got) != 0 {
		t.Fatalf("pending effects = %d, want 0 (panel must not light up)", len(got))
	}
	r := a.Session().toolRecoveryRecord("call-interrupted")
	if r == nil {
		t.Fatal("record lost")
	}
	if r.State != provider.ToolRunNotStarted || r.Resolution != sideEffectFreeResolution {
		t.Fatalf("verdict = state=%s resolution=%q, want not_started/%s", r.State, r.Resolution, sideEffectFreeResolution)
	}
	if r.ResolutionSource != "host" || r.ResolvedAt == 0 {
		t.Fatalf("resolution provenance = source=%q at=%d", r.ResolutionSource, r.ResolvedAt)
	}
	if unresolvedToolRecord(*r) {
		t.Fatal("auto-resolved record still counts as unresolved")
	}
	if len(sink.notices) != 1 {
		t.Fatalf("auto-resolution notices = %d, want 1", len(sink.notices))
	}
	if !strings.Contains(sink.notices[0].Text, "bash") || !strings.Contains(sink.notices[0].Text, "not started") {
		t.Fatalf("notice must name the tool and the verdict, got: %q", sink.notices[0].Text)
	}
	if strings.Contains(sink.notices[0].Text, "ls -la") {
		t.Fatal("notice must not carry command text")
	}
	// Task 482（fence 退役）: run 收尾不再 join barrier 错误（finishRunRecovery
	// 已撤）；同一回合内后续写也不再被拦。
	if err := a.beginToolRecovery(context.Background(), writePlanFor("next-write")); err != nil {
		t.Fatalf("write blocked after side-effect-free auto-resolution: %v", err)
	}
}

// 验收①续：中断打包（outcome_unknown_tools）里的只读 bash 被重分类为
// not_started —— 模型被告知可以立即重发，而不是先去核实不可能存在的外部效果。
func TestInterruptedReadOnlyBashReclassifiedInPromptHandoff(t *testing.T) {
	a, sink, call := sideEffectFreeFixture(t, "bash", `{"command":"grep -rn todo ."}`, false)
	refreezeInterruptedRecord(t, a, sink, call)
	a.finishToolRecovery(context.Background(), call, unknownOutcome())
	handoff := addInterruptedHandoff(a, []provider.InterruptedToolSummary{{ID: "call-interrupted", Name: "bash"}})

	view := a.transcriptInterruptedRecovery()
	if view == nil || len(view.UnknownTools) != 0 {
		t.Fatalf("outcome_unknown list = %+v, want empty after reclassification", view)
	}
	if len(view.NotStartedTools) != 1 || view.NotStartedTools[0].Name != "bash" {
		t.Fatalf("not_started list = %+v, want the bash entry", view.NotStartedTools)
	}
	block := interruptedRecoveryBlock(view)
	if strings.Contains(block, "outcome_unknown_tools") {
		t.Fatalf("prompt block still lists the call as outcome-unknown:\n%s", block)
	}
	if !strings.Contains(block, "not_started_tools") || !strings.Contains(block, "bash") {
		t.Fatalf("prompt block missing the not_started re-plan entry:\n%s", block)
	}
	if len(handoff.UnknownTools) != 1 {
		t.Fatal("durable handoff message was rewritten")
	}
}

// 验收②（task 433 + 482 fence 退役）：中断写 bash → 记录保持未决（面板与
// 跨会话分类的输入不受影响），但写不再被拦、run 也不再以 barrier 错误收尾。
func TestInterruptedWriteBashKeepsManualReview(t *testing.T) {
	a, sink, call := sideEffectFreeFixture(t, "bash", `{"command":"printf x >> tasklist.md"}`, false)
	a.finishToolRecovery(context.Background(), call, unknownOutcome())

	r := a.Session().toolRecoveryRecord("call-interrupted")
	if r == nil || r.State != provider.ToolRunUnknown || r.Resolution != "" {
		t.Fatalf("write bash must stay undecided, got state=%s resolution=%q", r.State, r.Resolution)
	}
	if got := a.PendingToolRecovery(); len(got) != 1 {
		t.Fatalf("pending effects = %d, want 1 (record stays visible to the panel)", len(got))
	}
	if len(sink.notices) != 0 {
		t.Fatalf("write bash must not be auto-resolved, got %d notice(s)", len(sink.notices))
	}
	// Task 482（fence 退役）: the undecided record no longer blocks the next write.
	if err := a.beginToolRecovery(context.Background(), writePlanFor("unblocked-write")); err != nil {
		t.Fatalf("write blocked with an undecided effect after fence removal: %v", err)
	}
}

// 验收③（task 433）：428 场景 —— ask 挂起被取消。ask 无外部副作用，中断后
// 自动判「未生效」：不进围栏、面板判据为空、prompt 收尾可立即重发。
func TestInterruptedAskNeverEntersFence(t *testing.T) {
	a, sink, call := sideEffectFreeFixture(t, "ask", `{"questions":[{"question":"which?","header":"h","options":[{"label":"a"},{"label":"b"}]}]}`, true)
	refreezeInterruptedRecord(t, a, sink, call)
	a.finishToolRecovery(context.Background(), call, unknownOutcome())

	if got := a.PendingToolRecovery(); len(got) != 0 {
		t.Fatalf("pending effects = %d, want 0 (428: no panel for a hung ask)", len(got))
	}
	r := a.Session().toolRecoveryRecord("call-interrupted")
	if r == nil || r.State != provider.ToolRunNotStarted || r.Resolution != sideEffectFreeResolution {
		t.Fatalf("ask verdict = state=%s resolution=%q", r.State, r.Resolution)
	}
	if len(sink.notices) != 1 {
		t.Fatalf("ask auto-resolution notices = %d, want 1", len(sink.notices))
	}
	if err := a.beginToolRecovery(context.Background(), writePlanFor("after-ask")); err != nil {
		t.Fatalf("ask interruption must not block the next write: %v", err)
	}
	addInterruptedHandoff(a, []provider.InterruptedToolSummary{{ID: "call-interrupted", Name: "ask"}})
	view := a.pendingInterruptedRecovery()
	if len(view.UnknownTools) != 0 || len(view.NotStartedTools) != 1 {
		t.Fatalf("handoff view = unknown:%d not_started:%d, want 0/1", len(view.UnknownTools), len(view.NotStartedTools))
	}
}

// 重载场景（task 519 结算前移）：记录从存储加载进来仍是未决态（进程内没跑过
// finish），agent 构造（会话加载）时就地把白名单记录判「未生效」——降级后的
// 记录行不再为只读遗留记录停留，写也不被拦（beginToolRecovery 里的结算调用
// 保留作兜底，构造后的新注入仍由它接住）。
func TestNewSettlesLeftoverSideEffectFreeRecords(t *testing.T) {
	a, sink, _ := sideEffectFreeFixture(t, "bash", `{"command":"git status --short"}`, false)
	if got := a.PendingToolRecovery(); len(got) != 0 {
		t.Fatalf("construction must settle leftover whitelisted records, pending = %d", len(got))
	}
	r := a.Session().toolRecoveryRecord("call-interrupted")
	if r == nil || r.Resolution != sideEffectFreeResolution || r.State != provider.ToolRunNotStarted {
		t.Fatalf("leftover verdict = state=%s resolution=%q, want not_started/%s", r.State, r.Resolution, sideEffectFreeResolution)
	}
	if len(sink.notices) != 1 {
		t.Fatalf("settle notices = %d, want 1", len(sink.notices))
	}
	// 遗留写类记录不受构造结算影响：仍保持未决（审计事实）。
	wa, _, _ := sideEffectFreeFixture(t, "bash", `{"command":"rm -rf build"}`, false)
	if got := wa.PendingToolRecovery(); len(got) != 1 {
		t.Fatalf("write-capable leftover must stay pending, got %d", len(got))
	}
}

// 留痕是尽力而为：落盘检查点失败时内存判定保留（回滚会复活一道不可能对应
// 任何真实效果的屏障），只降级为告警 —— 与 host-verified-absent 路径同约。
func TestSideEffectFreeResolutionSurvivesStorageFailure(t *testing.T) {
	a, sink, call := sideEffectFreeFixture(t, "read_file", `{"path":"a.txt"}`, true)
	refreezeInterruptedRecord(t, a, sink, call)
	sink.fail = true
	a.finishToolRecovery(context.Background(), call, unknownOutcome())
	if got := a.PendingToolRecovery(); len(got) != 0 {
		t.Fatalf("in-memory verdict must survive a storage failure, pending = %d", len(got))
	}
}

// prompt 收尾的名字兜底：无记录可查的旧格式条目按工具名判定 —— ask 直接
// 放行，bash 判不准（无参数）保持 outcome-unknown 人工核实。
func TestHandoffReclassificationWithoutRecordFallsBackToName(t *testing.T) {
	a, _, _ := sideEffectFreeFixture(t, "bash", `{"command":"rm -rf build"}`, false)
	handoff := addInterruptedHandoff(a, []provider.InterruptedToolSummary{
		{ID: "legacy-ask", Name: "ask"},
		{ID: "legacy-bash", Name: "bash"},
	})
	view := a.transcriptInterruptedRecovery()
	if len(view.UnknownTools) != 1 || view.UnknownTools[0].Name != "bash" {
		t.Fatalf("unknown list = %+v, want only the argless bash entry", view.UnknownTools)
	}
	if len(view.NotStartedTools) != 1 || view.NotStartedTools[0].Name != "ask" {
		t.Fatalf("not_started list = %+v, want the ask entry", view.NotStartedTools)
	}
	if len(handoff.UnknownTools) != 2 {
		t.Fatal("durable handoff message was rewritten")
	}
}

// 白名单判定表：在册工具直接放行；bash 只认 shellsafe 静态证实的只读探测，
// 重定向/tee/写形态/网络命令/动态替换/声明写目录一律 fail closed；口径外的
// 工具（写文件、网络、MCP、未知）一律不在册 —— 宁可多人工不可误放。
func TestInterruptedCallSideEffectFreeTable(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want bool
	}{
		{"ask", "ask", `{}`, true},
		{"read_file", "read_file", `{"path":"a"}`, true},
		{"glob", "glob", `{}`, true},
		{"grep", "grep", `{}`, true},
		{"ls", "ls", `{}`, true},
		{"todo_write", "todo_write", `{"todos":[]}`, true},
		{"bash ls", "bash", `{"command":"ls -la /tmp"}`, true},
		{"bash grep pipeline", "bash", `{"command":"grep -r todo . | head -5"}`, true},
		{"bash git status", "bash", `{"command":"git status --short"}`, true},
		{"bash git log", "bash", `{"command":"git log --oneline -5"}`, true},
		{"bash cat stderr dup", "bash", `{"command":"cat foo.txt 2>&1"}`, true},
		{"bash powershell reader", "bash", `{"command":"Get-Content a.txt"}`, true},
		{"bash output redirect", "bash", `{"command":"echo hi > out.txt"}`, false},
		{"bash append redirect", "bash", `{"command":"echo hi >> tasklist.md"}`, false},
		{"bash tee", "bash", `{"command":"ls | tee out.txt"}`, false},
		{"bash rm", "bash", `{"command":"rm -rf build"}`, false},
		{"bash mv", "bash", `{"command":"mv a b"}`, false},
		{"bash git commit", "bash", `{"command":"git commit -m x"}`, false},
		{"bash network", "bash", `{"command":"curl https://example.com"}`, false},
		{"bash dynamic substitution", "bash", `{"command":"echo $(rm -rf /)"}`, false},
		{"bash declared write dirs", "bash", `{"command":"ls","additional_write_dirs":["/tmp/x"]}`, false},
		{"bash empty command", "bash", `{"command":"  "}`, false},
		{"bash malformed args", "bash", `not-json`, false},
		{"bash missing args", "bash", ``, false},
		{"write_file excluded", "write_file", `{}`, false},
		{"web_fetch excluded", "web_fetch", `{}`, false},
		{"mcp tool excluded", "mcp__srv__query", `{}`, false},
		{"empty name excluded", "", `{}`, false},
	}
	for _, tc := range cases {
		if got := interruptedCallSideEffectFree(tc.tool, json.RawMessage(tc.args)); got != tc.want {
			t.Fatalf("%s: interruptedCallSideEffectFree(%q, %s) = %v, want %v", tc.name, tc.tool, tc.args, got, tc.want)
		}
	}
}
