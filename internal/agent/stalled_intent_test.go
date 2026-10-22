package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// #6 P0-c: the model announcing the next step instead of taking it.
func TestShouldNudgeStalledIntent(t *testing.T) {
	for _, text := range []string{
		"I'll now run the tests to confirm.",
		"Next, I will check the failing package.",
		"Let me now verify the build.",
		"About to patch the handler.",
	} {
		if !shouldNudgeStalledIntent(text) {
			t.Errorf("%q announces the next step and must be nudged", text)
		}
	}
	for _, text := range []string{
		"",
		"Done: the tests pass and the build is clean.",
		"The fix is applied; here is the diff.",
	} {
		if shouldNudgeStalledIntent(text) {
			t.Errorf("%q is a final answer and must not be nudged", text)
		}
	}
}

// 任务 1069 验收①：中文会话能命中——每个中文未来时短语 × 每个双字动词的
// 组合放在尾部最后一句里，都必须命中。
func TestShouldNudgeStalledIntentChinese(t *testing.T) {
	for _, phrase := range stalledIntentCNPromptPhrases {
		for _, verb := range stalledIntentCNVerbs {
			text := "工作已收尾。" + phrase + "我来" + verb + "一下后续细节。"
			if !shouldNudgeStalledIntent(text) {
				t.Errorf("%q (短语=%q 动词=%q 同句) announces the next step and must be nudged", text, phrase, verb)
			}
		}
	}
	// 句尾不带句号也要命中（尾句切分不能依赖标点收尾）。
	if !shouldNudgeStalledIntent("接下来我来检查一下") {
		t.Error("尾句无句号的中文承诺句必须命中")
	}
}

// 任务 1069 验收②：同句切分——两动词/短语分处不同句不误判；否定排除
// （完成态/转述主语）双向生效；尾句锚定（承诺句在正文中部不越界命中）。
func TestShouldNudgeStalledIntentChineseRejects(t *testing.T) {
	for _, text := range []string{
		"",                             // 空文本
		"已检查完毕。",                     // 完成态
		"不需要进一步操作。",                  // 否定排除
		"我已修复了这个问题。",                 // 完成态「已」
		"你需要验证这个结果。",                 // 转述主语「你需要」
		"先看看现在的状态。",                  // 单字动词「看」不在动词表，不得误伤
		"我会继续完善。",                    // 「完善」不在动词表
		"之前先修复过，现在已检查完毕。",           // 尾句完成态，正文承诺句不得越界命中
		"接下来我来修复这个问题。已检查完毕。",       // 承诺句在前、完成句在尾——尾句锚定，不得命中
		"这个问题需要处理。接下来我来看看日志。",      // 短语句只含动词、动词句只含短语——分句不命中
	} {
		if shouldNudgeStalledIntent(text) {
			t.Errorf("%q must not be nudged", text)
		}
	}
	// 否定排除是双向的：同句内出现否定标记，即便未来时标记+双字动词都满足也
	// 不命中（「接下来我来检查。不过此前已修复，无需再验证。」——尾句含
	// 「此前」「无需」）。
	if shouldNudgeStalledIntent("接下来我来检查。不过此前已修复，无需再验证。") {
		t.Error("尾句含否定标记时不得命中")
	}
	// 英文完成句不得命中（英文路径原有语义，本件零回归）。
	if shouldNudgeStalledIntent("The fix is applied; here is the diff.") {
		t.Error("英文完成句不得命中")
	}
}

// 任务 1069 验收②补充：尾句取最后一个非空句——尾部空行/空句不能让承诺句
// 「越界」被跳过。
func TestLastCNSentenceSkipsEmptyTail(t *testing.T) {
	if got := lastCNSentence("接下来我来修复。"); got != "接下来我来修复" {
		t.Errorf("lastCNSentence(正常尾句) = %q, 想要 %q", got, "接下来我来修复")
	}
	if got := lastCNSentence("接下来我来修复。\n\n"); got != "接下来我来修复" {
		t.Errorf("lastCNSentence(尾部空行) = %q, 想要跳过空句取最后一句", got)
	}
	if got := lastCNSentence(""); got != "" {
		t.Errorf("lastCNSentence(空) = %q, 想要空串", got)
	}
}

// 任务 1069 验收③：notice code 独立——run_loop 的 stalled-intent 路径发
// event.NoticeCodeStalledIntent（"stalled_intent"），不再是 executor_handoff。
func TestNoticeCodeStalledIntentIndependent(t *testing.T) {
	if event.NoticeCodeStalledIntent != "stalled_intent" {
		t.Fatalf("NoticeCodeStalledIntent = %q, want %q", event.NoticeCodeStalledIntent, "stalled_intent")
	}
	if event.NoticeCodeStalledIntent == event.NoticeCodeExecutorHandoff {
		t.Fatal("stalled-intent 必须用独立 notice code，不能与 executor_handoff 复用")
	}
}

// 任务 1069 验收③：run_loop 端到端——一次中文「宣布下一步」的回答触发恰好
// 一次 nudge，notice code = stalled_intent；cap 达到后精确不再发。
func TestStalledIntentNudgeE2EChinese(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{Text: "配置已更新完毕。接下来我来检查一下服务是否正常启动。"},
		testutil.Turn{Text: "服务已确认正常。"},
		testutil.Turn{Text: "服务已确认正常，无进一步操作。"},
	)
	sink := &stalledNudgeSink{}
	a := New(mp, tool.NewRegistry(), NewSession(""),
		Options{StalledIntentNudge: true}, sink)

	if err := a.Run(context.Background(), "更新配置并检查服务状态"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 2 {
		t.Fatalf("provider calls = %d, want work round + exactly one nudge round", got)
	}
	if n := countStalledIntentNotices(sink); n != 1 {
		t.Fatalf("stalled_intent notices = %d, want exactly 1", n)
	}
	if n := countStalledIntentMessages(a); n != 1 {
		t.Fatalf("stalled-intent repair messages = %d, want exactly 1", n)
	}
	// 第二次 Run：回答为完成态，不再触发（cap=1）。
	if err := a.Run(context.Background(), "继续"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if got := mp.CallCount(); got != 3 {
		t.Fatalf("provider calls after run 2 = %d, want no extra nudge round", got)
	}
	if n := countStalledIntentNotices(sink); n != 1 {
		t.Fatalf("stalled_intent notices after run 2 = %d, want still 1 (cap reached)", n)
	}
}

// 任务 1069 验收④：开关默认关——未配置 Options.StalledIntentNudge 的会话必须
// 逐字零行为（无 nudge 消息、无额外模型轮次、无 notice）；总闸
// hostContinuationEnabled（Goal/review/guardian/只读子代理）路径不动（不动
// Err==nil 出口的既有语义）。
func TestStalledIntentNudgeE2EOffByDefault(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{Text: "配置已更新完毕。接下来我来检查一下服务是否正常启动。"},
	)
	sink := &stalledNudgeSink{}
	a := New(mp, tool.NewRegistry(), NewSession(""), Options{}, sink)

	if err := a.Run(context.Background(), "更新配置并检查服务状态"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mp.CallCount(); got != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 with the nudge off", got)
	}
	if n := countStalledIntentNotices(sink); n != 0 {
		t.Fatalf("stalled_intent notices = %d, want 0 with the nudge off", n)
	}
	if n := countStalledIntentMessages(a); n != 0 {
		t.Fatalf("stalled-intent repair messages = %d, want 0 with the nudge off", n)
	}
}

// 任务 1069 验收②补充：cap 断言——intentNudges 达 cap 后精确断言不再发
// notice；cap=2 时第二次承诺句仍触发、第三次不再触发。
func TestStalledIntentNudgeCapEnforced(t *testing.T) {
	// cap=1（默认）：两次 Run 都是中文承诺句，只有第一轮触发。
	mp := testutil.NewMock("m",
		testutil.Turn{Text: "接下来我来检查一下。"},
		testutil.Turn{Text: "接下来我来验证一下。"},
		testutil.Turn{Text: "已验证完毕。"},
	)
	sink := &stalledNudgeSink{}
	a := New(mp, tool.NewRegistry(), NewSession(""),
		Options{StalledIntentNudge: true}, sink)
	for i := 0; i < 2; i++ {
		if err := a.Run(context.Background(), fmt.Sprintf("turn %d", i)); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if n := countStalledIntentNotices(sink); n != 1 {
		t.Fatalf("stalled_intent notices = %d, want exactly 1 (cap=1 across 2 runs)", n)
	}

	// cap=2：第二次承诺句仍触发、第三次不再触发。
	mp2 := testutil.NewMock("m",
		testutil.Turn{Text: "接下来我来检查一下。"},
		testutil.Turn{Text: "接下来我来验证一下。"},
		testutil.Turn{Text: "接下来我来修复一下。"},
		testutil.Turn{Text: "已修复完毕。"},
		testutil.Turn{Text: "已修复完毕，全部检查验证通过。"},
	)
	sink2 := &stalledNudgeSink{}
	a2 := New(mp2, tool.NewRegistry(), NewSession(""),
		Options{StalledIntentNudge: true, StalledIntentNudgeLimit: 2}, sink2)
	for i := 0; i < 3; i++ {
		if err := a2.Run(context.Background(), fmt.Sprintf("turn %d", i)); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if n := countStalledIntentNotices(sink2); n != 2 {
		t.Fatalf("stalled_intent notices = %d, want exactly 2 (cap=2 across 3 runs)", n)
	}
}

// Task 117: normalizeStalledIntentNudgeLimit clamps to [1, hardCap].
func TestNormalizeStalledIntentNudgeLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, maxStalledIntentNudges},
		{-1, maxStalledIntentNudges},
		{1, 1},
		{2, 2},
		{3, 3},
		{4, maxStalledIntentNudgeHardCap},
		{100, maxStalledIntentNudgeHardCap},
	}
	for _, c := range cases {
		if got := normalizeStalledIntentNudgeLimit(c.in); got != c.want {
			t.Errorf("normalizeStalledIntentNudgeLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// Task 117: stalledIntentNudgeEnabled fires for ContinuationExplicitFlow OR
// the config opt-in; neither alone is enough for a plain disabled agent.
func TestStalledIntentNudgeEnabled(t *testing.T) {
	// Default: disabled.
	a := &Agent{}
	if a.stalledIntentNudgeEnabled(context.Background()) {
		t.Error("default agent must not enable stalled-intent nudge")
	}
	// Config opt-in.
	a2 := &Agent{stalledIntentNudge: true}
	if !a2.stalledIntentNudgeEnabled(context.Background()) {
		t.Error("config opt-in must enable stalled-intent nudge")
	}
	// ContinuationExplicitFlow via context.
	a3 := &Agent{}
	ctx := WithContinuationPolicy(context.Background(), ContinuationExplicitFlow)
	if !a3.stalledIntentNudgeEnabled(ctx) {
		t.Error("ContinuationExplicitFlow context must enable stalled-intent nudge")
	}
}

// Task 117: stalledIntentNudgeCap returns the configured limit or the default.
func TestStalledIntentNudgeCap(t *testing.T) {
	a := &Agent{}
	if got := a.stalledIntentNudgeCap(); got != maxStalledIntentNudges {
		t.Errorf("default cap = %d, want %d", got, maxStalledIntentNudges)
	}
	a2 := &Agent{stalledIntentNudgeLimit: 2}
	if got := a2.stalledIntentNudgeCap(); got != 2 {
		t.Errorf("configured cap = %d, want 2", got)
	}
	// Hard cap is enforced at construction time via normalize, but the getter
	// also guards against a zero/limit set after construction.
	a3 := &Agent{stalledIntentNudgeLimit: 0}
	if got := a3.stalledIntentNudgeCap(); got != maxStalledIntentNudges {
		t.Errorf("zero limit cap = %d, want %d", got, maxStalledIntentNudges)
	}
}

// stalledNudgeSink collects events for stalled-intent assertions.
type stalledNudgeSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *stalledNudgeSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func countStalledIntentNotices(s *stalledNudgeSink) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.Kind == event.Notice && e.Code == event.NoticeCodeStalledIntent {
			n++
		}
	}
	return n
}

// countStalledIntentMessages counts host-injected stalled-intent repair
// messages in the session.
func countStalledIntentMessages(a *Agent) int {
	n := 0
	for _, m := range a.Session().Messages {
		if m.Origin == provider.MessageOriginHost && strings.Contains(m.Content, stalledIntentRetryMessage()) {
			n++
		}
	}
	return n
}

// Task 1070: the Standard-flow continuation total gate opens the bounded
// helpers for runs tagged ContinuationStandardFlow, but only when the agent's
// boot snapshot armed it; a tag without the snapshot is inert, an armed
// snapshot without a tag (ordinary run) stays closed, and the ExplicitFlow
// dedicated flows never depend on the switch.
func TestHostContinuationStandardFlowGate(t *testing.T) {
	ctxStandard := WithContinuationPolicy(context.Background(), ContinuationStandardFlow)
	ctxExplicit := WithContinuationPolicy(context.Background(), ContinuationExplicitFlow)

	// Default off: the tag alone must not open the gate (关态 = 现状逐字等价).
	if (&Agent{}).hostContinuationEnabled(ctxStandard) {
		t.Error("StandardFlow tag without the lab switch must keep the gate closed")
	}
	// Default off, no tag: pre-1070 behaviour (nothing reaches the gate).
	if (&Agent{}).hostContinuationEnabled(context.Background()) {
		t.Error("an agent with no policy and no tag must keep the gate closed")
	}
	// Lab switch on: the tag opens the gate.
	armed := &Agent{standardContinuation: true}
	if !armed.hostContinuationEnabled(ctxStandard) {
		t.Error("lab switch on + StandardFlow tag must open the gate")
	}
	// Lab switch on, no tag: still closed (the controller owns the tag).
	if armed.hostContinuationEnabled(context.Background()) {
		t.Error("lab switch on without a StandardFlow tag must keep the gate closed")
	}
	// ExplicitFlow dedicated flows are unaffected by the switch in either state.
	if !(&Agent{}).hostContinuationEnabled(ctxExplicit) {
		t.Error("ExplicitFlow must open the gate regardless of the lab switch (off)")
	}
	if !armed.hostContinuationEnabled(ctxExplicit) {
		t.Error("ExplicitFlow must open the gate regardless of the lab switch (on)")
	}
	// Agent-level policy (non-context) follows the same rules.
	if (&Agent{continuationPolicy: ContinuationStandardFlow}).hostContinuationEnabled(context.Background()) {
		t.Error("agent-level StandardFlow policy without the lab switch must stay closed")
	}
	if !(&Agent{continuationPolicy: ContinuationStandardFlow, standardContinuation: true}).hostContinuationEnabled(context.Background()) {
		t.Error("agent-level StandardFlow policy with the lab switch must open the gate")
	}

	// stalledIntentNudgeEnabled rides the same gate（1069 判定器联调路径：
	// Standard 开闸后无需单独打开 stalled_intent_nudge 也能修复）。
	if (&Agent{}).stalledIntentNudgeEnabled(ctxStandard) {
		t.Error("stalled-intent repair must stay closed on a bare StandardFlow tag")
	}
	if !(&Agent{standardContinuation: true}).stalledIntentNudgeEnabled(ctxStandard) {
		t.Error("stalled-intent repair must open with the lab switch + StandardFlow tag")
	}
 (1070 引擎侧：新增 ContinuationStandardFlow（与 ExplicitFlow 不同值）+ hostContinuationEnabled 双门（控制端 Standard 标签 ∧ agent boot 快照 standardContinuation，默认关=关态逐字等价现状）；4 个专用流设置点（只读子代理/pinned context/guardian/agent Options）零改动零回归；EBM 与 goal 边界仍只认 ExplicitFlow；测试：两态矩阵（标签无快照不生效/快照无标签不生效/开闸两门齐备才开）+ ExplicitFlow 两态不受开关影响 + Standard todo 续跑同脚本 4 调用 + stalledIntent（1069 判定器面）经 Standard 标签开闸后 1 次修复联调断言)
}
