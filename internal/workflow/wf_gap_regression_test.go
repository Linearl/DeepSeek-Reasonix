package workflow

// G1/G2 工作流引擎缺口回归钉（审计线缺口分析 2026-11-01，tl1325 修）。
//
// 落库自 docs/report/代码审计-20261031-工作流/回归复现/wf-gap-regression_test.go，
// 按 README 约定：函数名去 TmpGap 前缀，wantCurrentlyPass（证明缺口成立的）
// 断言改写为修复后的正向断言。修法 = 变体3「ctx 旁路取回原文」：
//   - 宿主侧把子代理原文分字段放到 ctx（agent.WithSubagentRawAnswerSink /
//     RecordSubagentRawAnswer / SubagentRawAnswer）；
//   - 引擎侧 TaskSubagentRunner 优先取原文；
//   - helper.go 的 finalAnswerBody/parseResultJSON/parseConfirmed 一行不动
//     （不变性由 TestG1_Invariance_EngineParsersUnchanged 钉住）。

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// hostWrappedAnswer 复刻宿主 composeSubagentAnswer（internal/agent/subagent_report.go）
// 在子代理答案前面插入的包装头。formatCompletionReport 的真实输出形状：
//
//	status: partial (lowered by the host: unbacked criterion claims)
//	summary: {"findings":[…]}
//
//	<子代理原文>
//
// withTrailing 时另含尾随的验收准则行 / host lowered 行 / unresolved 行——
// 四类块少剥一类就仍坏，这正是修法不走字符串剥离的原因。
func hostWrappedAnswer(summaryJSON string, withTrailing bool) string {
	var b strings.Builder
	b.WriteString("status: partial (lowered by the host: unbacked criterion claims)\n")
	b.WriteString("summary: ")
	b.WriteString(summaryJSON)
	if withTrailing {
		b.WriteString("\n  criterion-A passed — go test ./internal/workflow/")
		b.WriteString("\n  host lowered evidence not backed by a receipt")
		b.WriteString("\nunresolved: 未覆盖 desktop/frontend")
	}
	b.WriteString("\n\n")
	b.WriteString(summaryJSON) // 子代理自己的原文
	return b.String()
}

const gapFinderSummary = `{"findings":[{"where":"internal/permission/permission.go","what":"x","evidence":"y","severity":"medium"}],"count":1,"coverage":"c"}`

// TestG1_HostEnvelopeFormsParseViaRawBypass（验收①）：宿主包装头、包装头+尾随块
// 两形态下，引擎拿到的必须是经 ctx 旁路取回的子代理原文，parseResultJSON 全成功；
// 「纯 Final answer:」形态不经过旁路，由 finalAnswerBody 既有能力直接剥前缀，
// 同样解析成功。三形态全部转绿 = G1 修复生效。
func TestG1_HostEnvelopeFormsParseViaRawBypass(t *testing.T) {
	t.Run("包装头", func(t *testing.T) {
		got := resolveThroughBypass(t, hostWrappedAnswer(gapFinderSummary, false))
		obj, err := parseResultJSON(got)
		if err != nil {
			t.Fatalf("包装头形态应经旁路解析成功: %v", err)
		}
		if len(obj["findings"].([]any)) != 1 {
			t.Fatalf("应取到 findings，实际 %v", obj)
		}
	})
	t.Run("包装头+尾随块", func(t *testing.T) {
		got := resolveThroughBypass(t, hostWrappedAnswer(gapFinderSummary, true))
		obj, err := parseResultJSON(got)
		if err != nil {
			t.Fatalf("包装头+尾随块形态应经旁路解析成功: %v", err)
		}
		if len(obj["findings"].([]any)) != 1 {
			t.Fatalf("应取到 findings，实际 %v", obj)
		}
	})
	t.Run("纯 Final answer:", func(t *testing.T) {
		obj, err := parseResultJSON("some prose\nFinal answer: " + gapFinderSummary)
		if err != nil {
			t.Fatalf("Final answer: 形态应由 finalAnswerBody 既有能力解析成功: %v", err)
		}
		if len(obj["findings"].([]any)) != 1 {
			t.Fatalf("应取到 findings，实际 %v", obj)
		}
	})
}

// resolveThroughBypass 模拟宿主侧已把原文记进 ctx 旁路后，引擎侧
// resolvedSubagentAnswer 的取值：原文优先于被包装的 Execute 返回文本。
func resolveThroughBypass(t *testing.T, wrapped string) string {
	t.Helper()
	ctx := agent.WithSubagentRawAnswerSink(context.Background())
	agent.RecordSubagentRawAnswer(ctx, gapFinderSummary) // 宿主在包装前记下原文
	got := resolvedSubagentAnswer(ctx, "node-under-test", wrapped)
	if got != gapFinderSummary {
		t.Fatalf("引擎应取回子代理原文而非宿主包装文本，got:\n%s", got)
	}
	return got
}

// TestG1_Invariance_EngineParsersUnchanged（变体3 验收④）：helper.go 三个解析
// 函数签名与行为不变——宿主包装文本今天解析不动、修复后照样解析不动（证明
// 没有把字符串剥离塞回引擎）；Final answer: 前缀能力保持。
func TestG1_Invariance_EngineParsersUnchanged(t *testing.T) {
	t.Run("parseResultJSON 仍拒绝宿主包装文本", func(t *testing.T) {
		if _, err := parseResultJSON(hostWrappedAnswer(gapFinderSummary, true)); err == nil {
			t.Fatal("引擎解析器未变：包装文本必须仍然失败（原文取回应发生在 runner 层）")
		}
	})
	t.Run("finalAnswerBody 仍只剥 Final answer:", func(t *testing.T) {
		wrapped := "status: complete\nsummary: {\"a\":1}\n\n{\"a\":1}"
		if got := finalAnswerBody(wrapped); !strings.HasPrefix(got, "status:") {
			t.Fatalf("finalAnswerBody 行为未变：不剥宿主包装头，实际 %q", got)
		}
		if got := finalAnswerBody("some prose\nFinal answer: {\"a\":1}"); got != `{"a":1}` {
			t.Fatalf("finalAnswerBody 应保留 Final answer: 剥前缀能力，实际 %q", got)
		}
	})
}

// TestG1_FallbackWhenHostRecordedNothing（变体3 验收③）：宿主未记原文（版本
// 偏差）时引擎回退到 Execute 返回文本，不 panic，且留下可观测日志说明走了回退。
func TestG1_FallbackWhenHostRecordedNothing(t *testing.T) {
	t.Run("无 sink", func(t *testing.T) {
		if got := resolvedSubagentAnswer(context.Background(), "n1", "wrapped"); got != "wrapped" {
			t.Fatalf("回退应返回工具答案，实际 %q", got)
		}
	})
	t.Run("有 sink 但未记录", func(t *testing.T) {
		ctx := agent.WithSubagentRawAnswerSink(context.Background())
		if got := resolvedSubagentAnswer(ctx, "n1", "wrapped"); got != "wrapped" {
			t.Fatalf("回退应返回工具答案，实际 %q", got)
		}
	})
	t.Run("回退路径有 debug 日志", func(t *testing.T) {
		var buf strings.Builder
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		defer slog.SetDefault(prev)
		resolvedSubagentAnswer(context.Background(), "n1", "wrapped")
		if out := buf.String(); !strings.Contains(out, "raw sub-agent answer") {
			t.Fatalf("回退路径应留 debug 日志，实际：%s", out)
		}
	})
}

// ---- G2 ----

// gapCaptureRunner 捕获引擎实际发给子代理的 prompt，并按节点返回答案。
type gapCaptureRunner struct {
	prompts []string
	answer  string            // 默认答案
	byNode  map[string]string // 按节点 ID 指定答案（缺省用 answer）
}

func (r *gapCaptureRunner) Run(_ context.Context, req SubagentRequest) (string, error) {
	r.prompts = append(r.prompts, req.Prompt)
	if a, ok := r.byNode[req.NodeID]; ok {
		return a, nil
	}
	return r.answer, nil
}

const gapFinderAnswer = `{"findings":[{"where":"internal/permission/permission.go:747","what":"示例缺陷","evidence":"示例证据","severity":"medium"}],"count":1,"coverage":"示例覆盖"}`

// gapFinderConfirmDoc 是最小工作流：一个 subagent 产出 findings，一个 confirm 复核。
const gapFinderConfirmDoc = `{
  "version": 1,
  "name": "gap-g2",
  "nodes": [
    {"id":"finder","type":"subagent","prompt":"find issues",
     "result":[{"name":"findings","type":"array"}],
     "findings_field":"findings"},
    {"id":"checker","type":"confirm","target":"finder","prompt":"reproduce each finding"}
  ],
  "edges": [{"from":"finder","to":"checker"}]
}`

// gapRunFinderConfirm 跑完 finder→checker 并返回引擎 run 状态。
func gapRunFinderConfirm(t *testing.T, runner *gapCaptureRunner) *runState {
	t.Helper()
	wf, err := Parse([]byte(gapFinderConfirmDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	eng := New(runner, nil, nil)
	run := newRunState(eng, wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	return run
}

// TestG2_ConfirmPromptCarriesResultContract（验收②）：confirm 节点的 prompt
// 必须带 confirmed 字段契约——否则确认子代理不知道要输出 {"confirmed": …}，
// parseConfirmed 恒 false，复核闸门永失效（G2 根因）。
func TestG2_ConfirmPromptCarriesResultContract(t *testing.T) {
	runner := &gapCaptureRunner{
		answer: `{"confirmed": true, "reason": "all reproduced"}`,
		byNode: map[string]string{"finder": gapFinderAnswer},
	}
	gapRunFinderConfirm(t, runner)

	if len(runner.prompts) != 2 {
		t.Fatalf("应捕获 2 个 prompt（finder + checker），实际 %d", len(runner.prompts))
	}
	confirmPrompt := runner.prompts[1]
	if !strings.Contains(confirmPrompt, `"confirmed"`) {
		t.Fatalf("confirm prompt 应含 confirmed 字段契约：\n%s", confirmPrompt)
	}
	if !strings.Contains(confirmPrompt, "true only when you actually reproduced") {
		t.Fatalf("confirm prompt 应说明 confirmed=true 的判据：\n%s", confirmPrompt)
	}
}

// TestG2_ParseConfirmedSemanticsUnchanged（设计约束钉）：修 G2 靠注入契约，
// 不放宽 parseConfirmed——缺键/纯自然语言仍判 false，显式 true/false 语义保持。
func TestG2_ParseConfirmedSemanticsUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   bool
	}{
		{"显式 true", `{"confirmed": true, "reason": "ok"}`, true},
		{"显式 false", `{"confirmed": false, "reason": "no"}`, false},
		{"缺 confirmed 键（未收到契约时的典型输出）", `{"reason": "ok"}`, false},
		{"纯自然语言", `the finding is confirmed`, false},
	}
	for _, tc := range cases {
		if got := parseConfirmed(tc.answer); got != tc.want {
			t.Fatalf("%s: parseConfirmed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestG2_FindingsPromoteOnConfirmed（验收④ 护栏 + 验收③ 引擎面）：confirm 判
// true ⇒ findings 原地升 verified，且目标节点不得被回写 unconfirmed 污染。
func TestG2_FindingsPromoteOnConfirmed(t *testing.T) {
	runner := &gapCaptureRunner{
		answer: `{"confirmed": true, "reason": "all reproduced"}`,
		byNode: map[string]string{"finder": gapFinderAnswer},
	}
	wf, err := Parse([]byte(gapFinderConfirmDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	rec := &eventRecorder{}
	run := newRunState(New(runner, nil, rec), wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}

	if got := run.status["checker"]; got != stDone {
		t.Fatalf("checker 应为 stDone，实际 %v", got)
	}
	found := false
	for _, v := range run.verifs {
		if strings.Contains(v, "independent confirmation of finder by checker") {
			found = true
		}
	}
	if !found {
		t.Fatalf("confirm 判 true 后应记录复核 verifs，实际 %v", run.verifs)
	}
	fs := run.bag.snapshot()
	if len(fs) != 1 || fs[0].Status != FindingVerified {
		t.Fatalf("findings 应原地升为 verified，实际 %+v", fs)
	}
	for _, e := range rec.events {
		if e.NodeID == "finder" && e.State == event.WorkflowNodeUnconfirmed {
			t.Fatalf("confirmed:true 时目标节点不得被回写 unconfirmed（G2 污染），事件=%+v", e)
		}
	}
}

// TestG2_FalseVerdictStillLabelsTargetUnconfirmed（负向对照）：confirm 判 false
// ⇒ 目标节点仍按既有语义标记 unconfirmed——注入契约不得翻转否定语义。
func TestG2_FalseVerdictStillLabelsTargetUnconfirmed(t *testing.T) {
	runner := &gapCaptureRunner{
		answer: `{"confirmed": false, "reason": "could not reproduce"}`,
		byNode: map[string]string{"finder": gapFinderAnswer},
	}
	wf, err := Parse([]byte(gapFinderConfirmDoc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := wf.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	rec := &eventRecorder{}
	run := newRunState(New(runner, nil, rec), wf)
	if err := run.runAll(context.Background()); err != nil {
		t.Fatalf("runAll: %v", err)
	}
	if len(run.verifs) != 0 {
		t.Fatalf("confirm 判 false 不得记复核 verifs，实际 %v", run.verifs)
	}
	labeled := false
	for _, e := range rec.events {
		if e.NodeID == "finder" && e.State == event.WorkflowNodeUnconfirmed {
			labeled = true
		}
	}
	if !labeled {
		t.Fatal("confirm 判 false 时目标节点应被标记 unconfirmed（既有语义保持）")
	}
	fs := run.bag.snapshot()
	if len(fs) != 1 || fs[0].Status != FindingUnconfirmed {
		t.Fatalf("false 判定时 findings 应保持 unconfirmed，实际 %+v", fs)
	}
}
