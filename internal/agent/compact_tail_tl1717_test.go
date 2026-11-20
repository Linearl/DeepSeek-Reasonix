package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1717（用户拍板级 20261119，三个硬要求直接生效）:
//  1. 尾部保留上限 96K → 32K tokens —— 1M 窗口 tail 恒 32K（原 96K，是
//     Codex 20K 预算的 4.8 倍）；≤200K 窗口 16% 比例逐值不变。
//  2. 图片 token 按分辨率计价（tl1656/任务1025 口径），不按 base64 长度
//     折算 —— tail 记账走 estimateTailMessagesTokens 分项式。
//  3. 尾部至多保留 3 个 user 回合（tail_turns=3 上限，第 4 回合起可折）。
//
// 双路行为一致：全量路（planCompaction→recentTailStart）按双约束定界；
// ranges 路（compactRangesFoldLocked）按构造合规——折叠区到活跃轮为止，
// 安装的投影保留的完成回合数恒 ≤ 上限。两路各钉一支，防只改一路。

// tinyTurns 构造 sys + n 轮极小 user/assistant 交换（每轮计费 ≈ 几十个
// token）——token 预算远装得下全部回合，只有回合上限能收窄尾区。
func tinyTurns(n int) []provider.Message {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	for i := 1; i <= n; i++ {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleUser, Content: "tiny question " + strings.Repeat("q", 20)},
			provider.Message{Role: provider.RoleAssistant, Content: "tiny answer " + strings.Repeat("a", 20)},
		)
	}
	return msgs
}

// countUserTurns counts the verbatim user-authored turns in msgs.
func countUserTurns(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		if IsUserAuthoredTurnMessage(m) {
			n++
		}
	}
	return n
}

// strictAgentOverWindow builds a strictly-alternating agent so the planner's
// census second opinion (remeasure, force||!strict) stays dormant and the
// tail boundary under test comes from the dual constraint alone.
func strictAgentOverWindow(t *testing.T, window int, msgs []provider.Message) *Agent {
	t.Helper()
	return New(&fakeProvider{reply: "digest"}, tool.NewRegistry(), &Session{Messages: msgs}, Options{
		ContextWindow:          window,
		CompactRatio:           0.5,
		RecentKeep:             2,
		ArchiveDir:             t.TempDir(),
		StrictAlternatingRoles: true,
	}, event.Discard)
}

// 硬要求 3（全量路）: token 预算装得下全部 8 个回合时，回合上限仍把保护尾
// 收到 3 个回合——第 4 回合起可折。红绿对照：修复前 tailStart 独自定界，
// 8 个回合全部滞留保护尾。
func TestFullPathTailKeepsAtMostThreeTurns(t *testing.T) {
	a := strictAgentOverWindow(t, 1_000_000, tinyTurns(8))
	msgs := a.sess.conversation.Snapshot()
	head := a.pinnedPrefixLen(msgs)
	budget := a.recentTailBudget()
	if budget != 32*1024 {
		t.Fatalf("fixture 失真：1M 窗口预算 %d ≠ 32K（硬要求 1 未生效）", budget)
	}
	// fixture 守卫：token 独走装得下全部回合——本钉的收窄只能来自回合上限。
	tokenStart := tailStart(msgs, head, budget, a.messageTokens, a.tailFloor())
	if turns := countUserTurns(msgs[tokenStart:]); turns <= recentTailMaxTurns {
		t.Fatalf("fixture 失真：token 独走尾区只有 %d 个回合，复现不出超限", turns)
	}

	_, start, ok := a.planFoldRegion(msgs, false, true, false)
	if !ok {
		t.Fatal("fixture has no foldable region")
	}
	if start <= tokenStart {
		t.Fatalf("折叠起点 %d 未越过 token 独走边界 %d——回合上限没有收窄尾区", start, tokenStart)
	}
	if got := countUserTurns(msgs[start:]); got != recentTailMaxTurns {
		t.Fatalf("保护尾保留 %d 个回合，want %d——回合上限未生效（tl1717 硬要求 3）", got, recentTailMaxTurns)
	}
	if !IsUserAuthoredTurnMessage(msgs[start]) {
		t.Fatalf("尾区边界 %d 落在非回合起点（role=%s）——回合边界截断失效", start, msgs[start].Role)
	}
	// 第 4 回合起可折：4th-newest 回合的 user 消息必须落在折叠区。
	turnIdx := make([]int, 0, 8)
	for i, m := range msgs {
		if IsUserAuthoredTurnMessage(m) {
			turnIdx = append(turnIdx, i)
		}
	}
	fourthNewest := turnIdx[len(turnIdx)-recentTailMaxTurns-1]
	if fourthNewest < head || fourthNewest >= start {
		t.Fatalf("第 4 新回合（idx %d）不在折叠区 [%d,%d)——「第 4 回合起可折」被破坏", fourthNewest, head, start)
	}
	// 尾区按计费尺不超预算（1M 窗口下预算=32K cap）。
	if tail := a.estimatedPromptTokens(modelInputMessages(msgs[start:])); tail > budget {
		t.Fatalf("保护尾计费 %d 超预算 %d——双约束合成了超预算尾区", tail, budget)
	}
}

// 硬要求 1（全量路，行为面）: 1M 窗口下预算=32K（原 96K）——每轮 ~15K
// 计费 token 的会话，token 独走（回合上限未触发）也只能保留 2 个回合，
// 尾区计费读数 ≤ 32768。修复前预算 98304：尾区装下 6 个回合 ≈ 90K，红。
func TestFullPathTailFits32KCapAt1MWindow(t *testing.T) {
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "sys"}}
	for i := 1; i <= 12; i++ {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleUser, Content: "turn " + strings.Repeat("q", 400)},
			provider.Message{Role: provider.RoleAssistant, Content: "work " + strings.Repeat("w", 60000)},
		)
	}
	a := strictAgentOverWindow(t, 1_000_000, msgs)
	snap := a.sess.conversation.Snapshot()
	head := a.pinnedPrefixLen(snap)
	if got := a.recentTailBudget(); got != recentTailBudgetCapTokens {
		t.Fatalf("1M 窗口预算 = %d, want %d（96K 尾部上限未降到 32K）", got, recentTailBudgetCapTokens)
	}
	// fixture 守卫：回合上限在此夹具不触发（每轮 ~15K，3 回合已超预算），
	// 尾区收窄只能来自 32K 预算本身。
	if turnStart := tailTurnBoundary(snap, head); turnStart > tailStart(snap, head, a.recentTailBudget(), a.messageTokens, a.tailFloor()) {
		t.Fatalf("fixture 失真：回合边界 %d 先于 token 边界生效，测不出 cap", turnStart)
	}
	_, start, ok := a.planFoldRegion(snap, false, true, false)
	if !ok {
		t.Fatal("fixture has no foldable region")
	}
	tail := a.estimatedPromptTokens(modelInputMessages(snap[start:]))
	if tail > recentTailBudgetCapTokens {
		t.Fatalf("1M 窗口保护尾计费 %d > 32K cap——96K 尾部残留（tl1717 硬要求 1）", tail)
	}
	if tail < recentTailBudgetCapTokens/2 {
		t.Fatalf("fixture 失真：尾区计费 %d < cap 一半，夹具没顶到预算", tail)
	}
	if got := countUserTurns(snap[start:]); got > recentTailMaxTurns {
		t.Fatalf("保护尾 %d 个回合 > 上限 %d——回合上限未生效", got, recentTailMaxTurns)
	}
}

// 硬要求 2: tail 记账的分项式估计器——文本走既有普查、图片按 tl1656
// 分辨率口径（(w×h)/750 长边降采样 ×1.15 上浮，256 地板），与 base64
// 长度完全无关。夹具 = 一张 1080p 截图 + 20K 字节长文本混排。
func TestEstimateTailMessagesTokensPricesImagesByResolution(t *testing.T) {
	img := pngDataURL(1920, 1080)
	// 1080p 公式钉：长边降到 1568 → (1568×882)/750 = 1844 → ×1.15 → 2121。
	if got := imageEstimateTokens(img); got != 2121 {
		t.Fatalf("1080p 图片计价 = %d, want 2121（tl1656 公式漂移）", got)
	}
	longText := strings.Repeat("word ", 4000) // 20000 字节 ASCII
	msgs := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: longText},
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "screenshot", Images: []string{img}},
	}
	// 分项式精确钉：总读数 = 文本普查 + 图片分辨率价。
	want := estimateMessagesTokens(msgs) + imageEstimateTokens(img)
	if got := estimateTailMessagesTokens(msgs); got != want {
		t.Fatalf("混排读数 %d ≠ 文本普查 %d + 图片 %d——分项式破坏", got, estimateMessagesTokens(msgs), imageEstimateTokens(img))
	}
	// 图片必须真加了价（不是被漏算）。
	if estimateTailMessagesTokens(msgs) <= estimateMessagesTokens(msgs) {
		t.Fatal("图片价没有进 tail 账本——1080p 截图被漏算")
	}
	// base64 长度无关：同分辨率不同载荷（+240KB noise，base64÷4 会读出
	// ~60K tokens、远超 32K 尾部预算）读数逐值不变。
	noisy := pngDataURL(1920, 1080, noise(240_000)...)
	if base64Rate := len(noisy) / 4; base64Rate <= recentTailBudgetCapTokens {
		t.Fatalf("fixture 失真：base64÷4 只读 %d ≤ 32K，区分不出两种口径", base64Rate)
	}
	noisyMsgs := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: longText},
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "screenshot", Images: []string{noisy}},
	}
	if got := estimateTailMessagesTokens(noisyMsgs); got != want {
		t.Fatalf("载荷加长 240KB 后读数 %d ≠ %d——tail 记账按 base64 长度计价（tl1717 硬要求 2 破坏）", got, want)
	}
	// 一张 1080p 截图不得撑爆 32K 尾部预算（旧 base64 口径的失败形态）。
	if est := imageEstimateTokens(noisy); est > recentTailBudgetCapTokens/2 {
		t.Fatalf("单张 1080p 计价 %d 超过尾部预算一半——分辨率口径失真", est)
	}
}

// 硬要求 1 的包络钉: cap 与 floor 在 32K 相遇（studio clamp 上下两段闭合），
// 16% 恰=32K 的窗口逐值不变，819 floor 门语义原样。
func TestRecentTailCapMeetsFloorAt32K(t *testing.T) {
	if recentTailBudgetCapTokens != 32*1024 {
		t.Fatalf("cap = %d, want 32768（tl1717 硬要求 1）", recentTailBudgetCapTokens)
	}
	if recentTailBudgetFloorTokens != recentTailBudgetCapTokens {
		t.Fatalf("floor %d ≠ cap %d——819 包络上下两段未在 32K 相遇", recentTailBudgetFloorTokens, recentTailBudgetCapTokens)
	}
	if got := recentTailBudgetCapTokens / 1024; got != 32 {
		t.Fatalf("cap = %dK, want 32K", got)
	}
	cases := []struct{ window, want int }{
		{200_000, 32_000},    // 16% 恰=32K：预算逐值不变
		{204_799, 32_767},    // cap 阈值前最后一个窗口（204800×16%=32768 起 cap 生效）
		{204_800, 32_768},    // cap 生效
		{256 * 1024, 32_768}, // 819 floor 门上：floor 与 cap 相遇
		{1_000_000, 32_768},  // 1M 窗口 tail 恒 32K
	}
	for _, tc := range cases {
		a := &Agent{agentConfig: agentConfig{contextWindow: tc.window, compactRatio: defaultCompactRatio}}
		if got := a.recentTailBudget(); got != tc.want {
			t.Fatalf("window %d: recentTailBudget = %d, want %d", tc.window, got, tc.want)
		}
	}
	// 812 防线（floor 不下探小窗口）：10K 窗口预算仍是 16%×window。
	small := &Agent{agentConfig: agentConfig{contextWindow: 10_000, compactRatio: defaultCompactRatio}}
	if got := small.recentTailBudget(); got != 1_600 {
		t.Fatalf("10K 窗口预算 = %d, want 1600——小窗口保护被 floor 侵蚀（任务 812 回归）", got)
	}
}

// 任务 812 防线（端到端）: 10K 窗口的会话在双约束下仍有可折区——保护尾
// （预算 1600 + ≤3 回合）吞不掉折叠区，压缩照常触发。
func TestSmallWindowFoldableRegionSurvivesTailPolicy(t *testing.T) {
	a := strictAgentOverWindow(t, 10_000, tinyTurns(20))
	if got := a.recentTailBudget(); got != 1_600 {
		t.Fatalf("10K 窗口预算 = %d, want 1600（任务 812 比例保护）", got)
	}
	msgs := a.sess.conversation.Snapshot()
	head, start, ok := a.planFoldRegion(msgs, false, true, false)
	if !ok {
		t.Fatal("10K 窗口无可折区——保护尾吞掉折叠区（任务 812 回归）")
	}
	if got := countUserTurns(msgs[start:]); got > recentTailMaxTurns {
		t.Fatalf("保护尾 %d 个回合 > 上限 %d", got, recentTailMaxTurns)
	}
	if start-head < minCompactMessages {
		t.Fatalf("折叠区 %d 条 < 最小可折数——小窗口压缩失效", start-head)
	}
}

// 双路一致（ranges 路）: ranges 折叠按构造合规——折叠区到活跃轮为止，
// 装出的投影保留的完成回合数 ≤ 上限；且通道账本（source 读数）按 tl1656
// 分辨率口径给图片计价（不漏算、不按 base64）。修复前后均绿：本钉钉的是
// 「两路对同一尾政策的合规面」，不是行为变更面。
func TestRangesFoldRetainsAtMostTailTurns(t *testing.T) {
	isolateModelRangesConfig(t, 5, 2)
	a := newModelRangesAgent(t, event.Discard, strings.Repeat("n", 600))
	addRangesDelta(a.sess.conversation, 6)
	// 增量段里塞一张带 240KB noise 的 1080p 截图：分辨率价 2121，base64÷4
	// 会读 ~80K——通道账本的读数必须站在分辨率口径上。
	img := pngDataURL(1920, 1080, noise(240_000)...)
	imgPrice := imageEstimateTokens(img)
	a.sess.conversation.Add(provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: "screenshot check", Images: []string{img}})
	a.sess.conversation.Add(provider.Message{Role: provider.RoleAssistant, Content: "seen " + strings.Repeat("v", 1200)})

	view := a.modelVisibleMessages()
	withoutImages := make([]provider.Message, len(view))
	for i, m := range view {
		m.Images = nil
		withoutImages[i] = m
	}
	base := a.estimatedVisibleRequestTokens(withoutImages)

	res, err := a.RangesFoldCompact(context.Background(), tool.RangesFoldRequest{})
	if err != nil {
		t.Fatalf("RangesFoldCompact: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("status = %q reason = %q, want ok", res.Status, res.Reason)
	}
	// 图片进了通道账本：source 读数比去图基线恰好多出分辨率价（不是 0，
	// 也不是 base64÷4 的 ~80K）。
	delta := res.SourceTokens - base
	if delta <= imgPrice/2 || delta >= imgPrice*2 {
		t.Fatalf("ranges source 读数图片增量 %d（基线 %d，图片分辨率价 %d）——通道账本漏算或按 base64 计价（tl1717 硬要求 2，ranges 路）",
			delta, base, imgPrice)
	}
	// 双路一致面：装出的投影保留的完成回合数 ≤ 全量路的回合上限。
	if got := countUserTurns(a.modelVisibleMessages()); got > recentTailMaxTurns {
		t.Fatalf("ranges 装出的投影保留 %d 个回合 > 上限 %d——两路尾政策不一致", got, recentTailMaxTurns)
	}
	if got := countRangesSummaries(a.modelVisibleMessages()); got != 2 {
		t.Fatalf("summaries = %d, want 2（基线 digest + ranges note）", got)
	}
}
