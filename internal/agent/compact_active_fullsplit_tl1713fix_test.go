package agent

// tl1713fix 补修钉：pressure 自动道活跃轮砍区放宽/移除（用户 20261120 00:4x
// 裁定候选乙）。
//
// 砍区结构 bug（任务书口径修正块②）：planFoldRegion 先按 minCompactMessages
// 规划折叠区，活跃轮砍区（activeTurnFoldBoundary / start=active 钳制）在其后
// 发生且无二次下限校验——活跃轮轮数不足以分割（≤activeTurnKeepRounds 个
// assistant 轮）时折叠上界被砍回活跃轮边界，折叠区只剩 [head:active] 零头
// （旧摘要等），folded=1 形态照折或 blocked。并发不一致防线已由 tl1669 await
// 接管，砍区存在理由消失。
//
// 修复契约：
//   - pressure 自动道（非 mustFree）改完整轮内分割：分割域=完整活跃轮（不再
//     被尾部预算边界钳短），折旧轮全部折叠、保最新 activeTurnKeepRounds 个
//     assistant 轮逐字，边界=replay-safe 单元边界；轮数不足分割时不再把折叠
//     上界砍回活跃轮边界（保持外层规划边界——planCompaction 已校验下限）。
//   - overflow/mustFree 硬顶路径的活跃轮内折叠保护（保最新 2 轮+单元边界）
//     原样保留——100% 超窗唯一兜底，零回退。
//   - manual 保持用户指定范围语义（整轮保逐字）。
//   - tl1713 低收益拒绝门正交保留（首轮交付，本轮未动）。

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

const (
	// tl1713fixFewRoundsTurnPrompt / tl1713fixRoundMarker：视图逐字断言的
	// 定位标记——prompt 必须随 fold 走（修前豁免钳制下它恒在 view=红形态），
	// 最新 activeTurnKeepRounds 个 assistant 轮必须逐字保留。
	tl1713fixFewRoundsTurnPrompt = "few-rounds active turn prompt"
	tl1713fixRoundMarkerFmt      = "tl1713fix round %d body"
)

// tl1713fixFewRoundsSession 构造「活跃轮 assistant 轮数 ≤ activeTurnKeepRounds」
// 的 pressure 带内会话：旧前缀（system+旧摘要+多轮实质内容）承担过线主量，
// 活跃轮两轮大 assistant 消息保证「保最新 2 轮」=全保——修前豁免钳制把折叠
// 上界砍回 active，prompt 与活跃轮永远逐字占视图；修后折叠覆盖 prompt。
func tl1713fixFewRoundsSession(t *testing.T) (*Agent, *Session, *[]event.Event) {
	t.Helper()
	const activeCreatedAt = int64(424_242)
	// 旧前缀 A：24 对中小 user/assistant 轮，折叠区实质收益来源（过低收益门）。
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		formatSummaryMessage(strings.Repeat("- kept fact line\n", 400)),
	}
	for i := range 24 {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("old question %d ", i) + strings.Repeat("w", 3600)},
			provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("old answer %d ", i) + strings.Repeat("w", 3600)},
		)
	}
	msgs = append(msgs,
		provider.Message{Role: provider.RoleUser, Content: tl1713fixFewRoundsTurnPrompt, CreatedAt: activeCreatedAt},
	)
	// 活跃轮 N=2（≤ keep）：两轮大 assistant 消息——「保最新 2 轮」=全保，
	// 折叠的合法上界=prompt（含更早内容）。
	for i := range 2 {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf(tl1713fixRoundMarkerFmt, i) + " " + strings.Repeat("x", 360_000)},
			provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("round %d ack", i)},
		)
	}
	sess := &Session{Messages: msgs}
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	a := New(&fakeProvider{reply: "short digest"}, tool.NewRegistry(), sess, Options{
		ContextWindow: 262_144, CompactRatio: 0.8, RecentKeep: 2,
		ArchiveDir: t.TempDir(),
	}, sink)
	a.activeTurnCreatedAt.Store(activeCreatedAt)
	return a, sess, &events
}

// fixture 前置哨：视图必须落在 [触发线, 硬顶)——pressure 带、非 mustFree。
func tl1713fixRequirePressureBand(t *testing.T, a *Agent) {
	t.Helper()
	est := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	if fold, hard := a.compactTrigger(), a.hardInputCeiling(); est < fold || est >= hard {
		t.Fatalf("fixture view = %d tokens, want [%d, %d) for the pressure band", est, fold, hard)
	}
}

func tl1713fixJoinedView(a *Agent) string {
	var joined strings.Builder
	for _, m := range a.modelVisibleMessages() {
		joined.WriteString(m.Content)
	}
	return joined.String()
}

// 主复现钉（验收 1：活跃轮轮数≤2 场景修前红修后绿）：pressure 折叠必须覆盖
// prompt（完整轮内分割下 prompt 随 fold 走），不再被豁免钳制钉在视图里；
// 最新 2 轮逐字保留；视图收敛到触发线下；fold plan 观测行不再出现「折叠区
// 零头」形态。
func TestTl1713fixPressureFoldCoversPromptOnFewRounds(t *testing.T) {
	a, _, _ := tl1713fixFewRoundsSession(t)
	tl1713fixRequirePressureBand(t, a)

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure prepare = %v, want the fold to land", err)
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" || r.Action != "summary" {
		t.Fatalf("receipt = %+v, want an applied summary fold", r)
	}

	// 验收①：压后视图收敛到触发线下。
	after := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	if after >= a.compactTrigger() {
		t.Fatalf("post-fold view = %d tokens, still at/above the trigger %d", after, a.compactTrigger())
	}

	view := tl1713fixJoinedView(a)
	// 完整轮内分割：prompt 随 fold 走（修前豁免钳制下它恒逐字=红形态）。
	if strings.Contains(view, tl1713fixFewRoundsTurnPrompt) {
		t.Fatal("the active turn prompt stayed verbatim; the fold did not cover it " +
			"(the few-rounds clamp kept the fold clamped to the active turn)")
	}
	// 保最新 activeTurnKeepRounds=2 轮逐字。
	for _, i := range []int{0, 1} {
		if !strings.Contains(view, fmt.Sprintf(tl1713fixRoundMarkerFmt, i)) {
			t.Fatalf("round %d missing from the view; the split kept too little", i)
		}
	}
}

// overflow 零回退对照（验收 2）：同一 fixture 走 overflow（恒 mustFree）道，
// 活跃轮内折叠保护原样——轮数不足分割时整轮豁免，prompt 逐字保留。
func TestTl1713fixOverflowLaneStillKeepsFewRoundsTurnVerbatim(t *testing.T) {
	a, _, _ := tl1713fixFewRoundsSession(t)

	if err := prepareContext(context.Background(), a, CompactionTriggerOverflow); err != nil {
		t.Fatalf("overflow prepare = %v", err)
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied overflow fold", r)
	}
	if view := tl1713fixJoinedView(a); !strings.Contains(view, tl1713fixFewRoundsTurnPrompt) {
		t.Fatal("overflow fold disturbed the few-rounds active turn; the mustFree " +
			"keep-the-turn protection regressed (100% 超窗唯一兜底红线)")
	}
}


// tl1713fixMsg builds one message (fixture shorthand).
func tl1713fixMsg(role provider.Role, content string) provider.Message {
	return provider.Message{Role: role, Content: content}
}

// 纯函数形态契约钉（验收矩阵 A2-A5）：完整轮内分割边界在各活跃轮形态下的
// 落点——>2 轮=第 3 新 assistant 单元结束边界（折旧轮全折）；≤2 轮=最早
// assistant 单元起点（prompt 折、全部 assistant 轮完整保留）；无 assistant
// 单元=active（调用方保持外层边界）；所有边界都落在 replay-safe 单元边界
// 上（assistant 工具调用与其结果同进同出，不切散）。
func TestTl1713fixFullSplitBoundaryShapes(t *testing.T) {
	const active = 1 // [sys, prompt, ...] fixture 里活跃轮 prompt 的索引
	prompt := tl1713fixMsg(provider.RoleUser, tl1713fixFewRoundsTurnPrompt)
	sys := tl1713fixMsg(provider.RoleSystem, "sys")
	round := func(name string) provider.Message {
		return tl1713fixMsg(provider.RoleAssistant, name+" body")
	}
	ack := func(name string) provider.Message {
		return tl1713fixMsg(provider.RoleUser, name+" ack")
	}
	toolRound := func(name string) []provider.Message {
		call := round(name)
		call.ToolCalls = []provider.ToolCall{{ID: name + "-call", Name: "read"}}
		return []provider.Message{
			call,
			tl1713fixMsg(provider.RoleTool, name+" result"),
		}
	}

	t.Run("more rounds than keep folds the third newest", func(t *testing.T) {
		msgs := append([]provider.Message{sys, prompt},
			round("a0"), ack("a0"), round("a1"), ack("a1"), round("a2"), ack("a2"), round("a3"))
		// body=msgs[2:]（a0..a3 七条）。倒序数 assistant：a3、a2、a1——第
		// 3 新=a1（body 索引 2，unit [2,3)），其结束边界=1+1+3=5 即折叠上
		// 界：a0/a1 随 fold 走，a2/a3 完整保留。
		if got := activeTurnFullSplitBoundary(msgs, active); got != 5 {
			t.Fatalf("boundary = %d, want 5 (end of the third-newest assistant unit)", got)
		}
	})
	t.Run("two rounds fold only the prompt", func(t *testing.T) {
		msgs := append([]provider.Message{sys, prompt}, round("a0"), ack("a0"), round("a1"), ack("a1"))
		// 最早 assistant 轮 a0 的起点（msgs 索引 2）：prompt 随 fold 走，
		// 两轮（含 ack）完整保留。
		if got := activeTurnFullSplitBoundary(msgs, active); got != 2 {
			t.Fatalf("boundary = %d, want 2 (start of the oldest assistant round)", got)
		}
	})
	t.Run("one round folds only the prompt", func(t *testing.T) {
		msgs := append([]provider.Message{sys, prompt}, round("a0"))
		if got := activeTurnFullSplitBoundary(msgs, active); got != 2 {
			t.Fatalf("boundary = %d, want 2 (start of the only assistant round)", got)
		}
	})
	t.Run("no assistant unit keeps the caller boundary", func(t *testing.T) {
		msgs := []provider.Message{sys, prompt, ack("orphan")}
		if got := activeTurnFullSplitBoundary(msgs, active); got != active {
			t.Fatalf("boundary = %d, want active %d (nothing to anchor on)", got, active)
		}
	})
	t.Run("guards", func(t *testing.T) {
		msgs := []provider.Message{sys, prompt, round("a0")}
		if got := activeTurnFullSplitBoundary(msgs, -1); got != -1 {
			t.Fatalf("boundary(active=-1) = %d, want -1", got)
		}
		if got := activeTurnFullSplitBoundary(msgs[:2], active); got != active {
			t.Fatalf("boundary(turn with no body) = %d, want active %d", got, active)
		}
	})
	t.Run("tool group moves as one unit", func(t *testing.T) {
		// 折旧轮带工具组：call+result 必须同进 fold（边界落在 unit 结束处，
		// 不把 result 切进保留区）。
		tool0 := toolRound("t0")
		msgs := append([]provider.Message{sys, prompt}, tool0...)
		msgs = append(msgs, round("a1"), ack("a1"), round("a2"), ack("a2"))
		// body=msgs[2:]：t0-call unit=[0,2)（call+result），a1=[2,3)，a2=[4,5)。
		// 倒序数 assistant：a2、a1、t0-call——第 3 新=t0-call，其 unit 结束
		// 边界=1+1+2=4：fold=[0,4) 含 call+result 整组，保留区从 a1 开始。
		if got := activeTurnFullSplitBoundary(msgs, active); got != 4 {
			t.Fatalf("boundary = %d, want 4 (end of the tool-call unit: call+result fold together)", got)
		}
		if msgs[4].Role == provider.RoleTool {
			t.Fatal("the kept region starts on an orphan tool result: the tool group was split")
		}
	})
}

// planFoldRegion 层红绿钉（验收 1 的白盒半边）：活跃轮轮数 ≤ keep 时——
// overflow/mustFree 语义（fullSplit=false）保留整轮豁免钳制（保护红线），
// pressure 完整轮内分割（fullSplit=true）不再把折叠上界砍回 active，折叠
// 覆盖 prompt、全部 assistant 轮逐字保留。窗口 0 使外层规划边界确定为
// len(msgs)（无预算/回合上限干扰），豁免与分割的差异全部来自砍区分支本体。
func TestTl1713fixPressurePlanFewRoundsNotClamped(t *testing.T) {
	const activeCreatedAt = int64(424_242)
	msgs := []provider.Message{
		tl1713fixMsg(provider.RoleSystem, "sys"),
		tl1713fixMsg(provider.RoleUser, "old question"),
		tl1713fixMsg(provider.RoleAssistant, "old answer"),
		{Role: provider.RoleUser, Content: tl1713fixFewRoundsTurnPrompt, CreatedAt: activeCreatedAt},
		tl1713fixMsg(provider.RoleAssistant, fmt.Sprintf(tl1713fixRoundMarkerFmt, 0)+" body"),
		tl1713fixMsg(provider.RoleUser, "round 0 ack"),
		tl1713fixMsg(provider.RoleAssistant, fmt.Sprintf(tl1713fixRoundMarkerFmt, 1)+" body"),
		tl1713fixMsg(provider.RoleUser, "round 1 ack"),
	}
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{reply: "digest"}, tool.NewRegistry(), sess, Options{
		RecentKeep: 2, // 窗口 0：planCompaction 走无窗口分支，start₀=len(msgs)
	}, event.Discard)
	a.activeTurnCreatedAt.Store(activeCreatedAt)

	msgs = a.sess.conversation.Snapshot()
	head, start, ok := a.planFoldRegion(msgs, false, true, false) // overflow/mustFree 语义对照
	if !ok {
		t.Fatal("fixture has no foldable region")
	}
	active := a.activeTurnStart(msgs)
	if active != 3 {
		t.Fatalf("fixture active turn = %d, want 3", active)
	}
	if start != active {
		t.Fatalf("overflow lane boundary = %d, want the few-rounds clamp at active %d (mustFree keep-the-turn protection must stay)", start, active)
	}

	_, fullStart, _ := a.planFoldRegion(msgs, false, true, true) // pressure 完整轮内分割
	if fullStart != 4 {                                          // 最早 assistant 轮起点：prompt 随 fold 走
		t.Fatalf("full-split boundary = %d, want 4 (start of the oldest assistant round; the prompt folds)", fullStart)
	}
	if fullStart-head < minCompactMessages {
		t.Fatalf("full-split boundary %d leaves a %d-message fold below minCompactMessages %d — the zero-head fold is back",
			fullStart, fullStart-head, minCompactMessages)
	}
	// 保留区：全部 assistant 轮逐字保留（N ≤ keep 时没有折旧轮可折），
	// prompt 不在保留区。
	var kept strings.Builder
	for _, m := range msgs[fullStart:] {
		kept.WriteString(m.Content)
		kept.WriteString("\n")
	}
	for _, i := range []int{0, 1} {
		if !strings.Contains(kept.String(), fmt.Sprintf(tl1713fixRoundMarkerFmt, i)) {
			t.Fatalf("round %d missing from the kept region; the split kept too little", i)
		}
	}
	if strings.Contains(kept.String(), tl1713fixFewRoundsTurnPrompt) {
		t.Fatal("the prompt stayed in the kept region; the fold was still clamped to the active turn")
	}
}

// planFoldRegion 层下限兜底钉（验收矩阵 A5）：完整域边界会留下低于
// minCompactMessages 的零头折叠区时，保持外层规划边界——二次下限校验堵住
// 「砍完剩零头也照折」的结构缺口（任务书口径修正块②）。
func TestTl1713fixPressurePlanKeepsOuterBoundaryWhenSplitWouldLeaveZeroHead(t *testing.T) {
	const activeCreatedAt = int64(424_242)
	// active 紧跟 head（旧前缀只有 system），活跃轮唯一 assistant 轮：
	// 完整域边界=active+1 → start-head=1 < minCompactMessages=2 → 必须保持
	// 外层规划边界而不是砍出一条折叠。
	msgs := []provider.Message{
		tl1713fixMsg(provider.RoleSystem, "sys"),
		{Role: provider.RoleUser, Content: tl1713fixFewRoundsTurnPrompt, CreatedAt: activeCreatedAt},
		tl1713fixMsg(provider.RoleAssistant, "only round body"),
	}
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{reply: "digest"}, tool.NewRegistry(), sess, Options{
		RecentKeep: 2, // 窗口 0：外层规划边界确定 = len(msgs)
	}, event.Discard)
	a.activeTurnCreatedAt.Store(activeCreatedAt)

	msgs = a.sess.conversation.Snapshot()
	_, outerStart, ok := a.planCompaction(msgs, minCompactMessages, false)
	if !ok {
		t.Fatal("fixture has no foldable region for the outer plan")
	}
	head, start, ok := a.planFoldRegion(msgs, false, true, true)
	if !ok {
		t.Fatal("planFoldRegion reported no foldable region")
	}
	if head != a.pinnedPrefixLen(msgs) || start != outerStart {
		t.Fatalf("boundary = [%d,%d), want the outer planning region [%d,%d) — a zero-head fold must not be planned",
			head, start, a.pinnedPrefixLen(msgs), outerStart)
	}
}
