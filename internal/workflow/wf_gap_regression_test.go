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
