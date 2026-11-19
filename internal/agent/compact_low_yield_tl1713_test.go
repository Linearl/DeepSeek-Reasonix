package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
	"reasonix/internal/tool"
)

// tl1713 压缩收纳通道塌陷复现钉与低收益拒绝门验收。
//
// 现场（20261119 21:34→22:28 八连轮）：投影体 = system + 巨型会话上下文
// 载体（~437KB 全量快照）+ 旧摘要共三条消息时，tl1587 段预算的二分前缀
// 搜索（boundedSummaryPrefixEnd，compact_safe_prefix.go）把折叠边界钉死在
// 载体墙内侧——折叠区只剩 [旧摘要] 一条（folded_messages=1，fold_tokens
// ≈旧摘要重写的 14-17K），残留恒 77-81%，pressure 高频复压恶性循环。
//
// 修复契约：自动压力道（trigger=pressure，非 force/mustFree）在花费任何
// 摘要请求、发出任何压缩事件之前按「收纳量过低 × 预计残留过高」拒绝落地
// 并停车；停车期间同水位尝试直接 noop（打断 40 分钟一压循环）；增长跨过
// 步长 / 视图缩水 / 任何成功安装后清场重评。manual（用户裁决）与
// mustFree（物理回收）旁路，正常收纳轮零回退。

// tl1713CarrierWallSession 复刻现场形态：
//   - canonical = [system, user, assistant, user]（投影覆盖 0..3）+ 巨型首条
//     尾轮 + 大量普通轮（视图过 0.8 触发线、低于硬顶）；
//   - 投影体 = [system, 巨型会话上下文载体, 旧摘要]。
//
// 载体计价 ~200K tok（段预算 250K 的 80%）：装下载体后预算只剩 ~48K，
// 首条尾轮（~60K tok）单独越界——二分收敛到「旧摘要之后」，折叠区只剩旧
// 摘要一条（塌陷态）。
func tl1713CarrierWallSession() *Session {
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", 400)})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	// 尾区首条：单独超过「段预算 − 载体 − 指令」的余量（塌陷墙）。
	sess.Add(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("word ", 48000)})
	for range 1100 {
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", 400)})
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	}
	return sess
}

// tl1713SeedCarrierProjection 把载体墙投影装进会话态：body = [system, 载体,
// 旧摘要]，覆盖 canonical 0..3。memoryChars 决定载体体量（800_000 = 现场
// 形态 ~200K tok；2_000 = 健康形态 ~500 tok）。返回种子版本号供安装断言。
func tl1713SeedCarrierProjection(t *testing.T, a *Agent, memoryChars int, withDigest bool) uint64 {
	t.Helper()
	carrier := sessioncontext.Build(sessioncontext.Sections{
		BackgroundMemory: strings.Repeat("m", memoryChars),
	}).Content
	body := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		HostGeneratedUserMessage(carrier),
	}
	if withDigest {
		body = append(body, formatSummaryMessage(strings.Repeat("- kept fact line\n", 250)))
	}
	a.sess.compactionMu.Lock()
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionState = CompactionState{
		SchemaVersion:  compactionStateSchemaCurrent,
		PromptCacheKey: a.currentPromptCacheKeyLocked(),
		Projection: ContextProjection{
			Messages:          body,
			CoveredCount:      4,
			CoveredPrefixHash: coveredPrefixHash(msgs, 4),
			PinnedContextHash: pinnedContextCoverageHash(msgs, 4),
		},
	}
	a.sess.compactionMu.Unlock()
	return a.currentProjectionVersion()
}

func tl1713Agent(t *testing.T, prov provider.Provider, sess *Session) (*Agent, *[]event.Event) {
	t.Helper()
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	a := New(prov, tool.NewRegistry(), sess, Options{
		ContextWindow: 1_000_000,
		CompactRatio:  0.8,
		RecentKeep:    2,
		ArchiveDir:    t.TempDir(),
	}, sink)
	return a, &events
}

// fixture 前置哨：视图必须落在 [触发线, 硬顶) —— 压力道、非 mustFree。
func tl1713RequirePressureBand(t *testing.T, a *Agent) {
	t.Helper()
	est := a.estimatedVisibleRequestTokens(a.modelVisibleMessages())
	if fold, hard := a.compactTrigger(), a.hardInputCeiling(); est < fold || est >= hard {
		t.Fatalf("fixture view = %d tokens, want [%d, %d) for the pressure lane", est, fold, hard)
	}
}

// 塌陷复现 + 低收益拒绝门主钉：载体墙形态的 pressure 轮必须被拒绝——零
// 摘要请求、零压缩事件、投影不动、停车武装；同水位重试继续 noop；增长跨过
// 步长后重评（仍然拒绝，仍零花费）；manual 裁决照常安装并清场。
func TestTl1713CarrierWallRoundDeclinedAndParked(t *testing.T) {
	prov := &segmentStubProvider{window: 1_000_000, bigThreshold: 1 << 30}
	sess := tl1713CarrierWallSession()
	a, events := tl1713Agent(t, prov, sess)
	seeded := tl1713SeedCarrierProjection(t, a, 800_000, true)
	tl1713RequirePressureBand(t, a)

	// 第一轮：拒绝落地，零花费零事件。
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("declined pressure round errored: %v", err)
	}
	if n := len(prov.recorded()); n != 0 {
		t.Fatalf("declined round spent %d summarizer requests, want 0", n)
	}
	if v := a.currentProjectionVersion(); v != seeded {
		t.Fatalf("declined round installed projection version %d, want seed %d", v, seeded)
	}
	if park := a.sess.compaction.lowYieldParkEst.Load(); park <= 0 {
		t.Fatalf("low-yield park did not arm (park=%d)", park)
	}
	for _, e := range *events {
		if e.Kind == event.CompactionStarted || e.Kind == event.CompactionDone {
			t.Fatalf("declined round emitted compaction event %+v, want silence", e.Compaction)
		}
	}

	// 同水位第二轮：停车直接 noop，零花费。
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("parked pressure round errored: %v", err)
	}
	if n := len(prov.recorded()); n != 0 {
		t.Fatalf("parked round spent %d summarizer requests, want 0", n)
	}

	// 增长跨过步长后重评：仍拒绝，仍零花费，停车点前移。
	parkBefore := a.sess.compaction.lowYieldParkEst.Load()
	for i := 0; i < 130; i++ {
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", 400)})
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	}
	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("grown pressure round errored: %v", err)
	}
	if n := len(prov.recorded()); n != 0 {
		t.Fatalf("grown-but-low-yield round spent %d summarizer requests, want 0", n)
	}
	if park := a.sess.compaction.lowYieldParkEst.Load(); park <= parkBefore {
		t.Fatalf("re-declined round did not re-arm the park at the grown view (before=%d after=%d)", parkBefore, park)
	}

	// manual 裁决旁路：照常安装，停车清场。
	if err := a.compact(context.Background(), CompactionTriggerManual, "", false); err != nil {
		t.Fatalf("manual compact: %v", err)
	}
	if n := len(prov.recorded()); n == 0 {
		t.Fatal("manual compact never reached the summarizer")
	}
	if v := a.currentProjectionVersion(); v == seeded {
		t.Fatal("manual compact did not install a projection")
	}
	if park := a.sess.compaction.lowYieldParkEst.Load(); park != 0 {
		t.Fatalf("successful install left the low-yield park armed (park=%d)", park)
	}
	// 同视图可折集合对账（协调线 20261120 验收）：pressure 塌陷轮只枚举
	// ≤2 条（折叠区只剩旧摘要），manual 同视图枚举数百条——塌陷在收纳选择
	// 器（tl1587 段预算在载体墙处的二分收敛），不在会话内容形态。
	manualFolded := 0
	for _, e := range *events {
		if e.Kind == event.CompactionDone && e.Compaction.Trigger == CompactionTriggerManual && e.Compaction.Messages > manualFolded {
			manualFolded = e.Compaction.Messages
		}
	}
	if manualFolded < 100 {
		t.Fatalf("manual folded %d messages on the same view, want a healthy enumeration (the collapse was selector-side, not content-side)", manualFolded)
	}
}

// 正常收纳轮零回退：小载体 + 均匀轮，段预算能装下 ~250 条折叠（收纳比
// ~30% >> 10% 门限）——pressure 轮照常安装。
func TestTl1713HealthyFoldNotDeclined(t *testing.T) {
	prov := &segmentStubProvider{window: 1_000_000, bigThreshold: 1 << 30}
	sess := NewSession("sys")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", 400)})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	for range 800 {
		sess.Add(provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("word ", 800)})
		sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	}
	a, _ := tl1713Agent(t, prov, sess)
	seeded := tl1713SeedCarrierProjection(t, a, 2_000, false) // 小载体（~500 tok），无旧摘要
	tl1713RequirePressureBand(t, a)

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("healthy pressure round errored: %v", err)
	}
	if n := len(prov.recorded()); n == 0 {
		t.Fatal("healthy round never reached the summarizer")
	}
	if v := a.currentProjectionVersion(); v == seeded {
		t.Fatal("healthy round did not install a projection")
	}
	if park := a.sess.compaction.lowYieldParkEst.Load(); park != 0 {
		t.Fatalf("healthy round armed the low-yield park (park=%d)", park)
	}
}

// overflow/mustFree 物理回收道旁路：同一塌陷形态在 overflow 下照旧安装
// （有摘要收，比停在物理水位更优），门不拦。
func TestTl1713OverflowBypassesGate(t *testing.T) {
	prov := &segmentStubProvider{window: 1_000_000, bigThreshold: 1 << 30}
	sess := tl1713CarrierWallSession()
	a, _ := tl1713Agent(t, prov, sess)
	seeded := tl1713SeedCarrierProjection(t, a, 800_000, true)

	if err := prepareContext(context.Background(), a, CompactionTriggerOverflow); err != nil {
		t.Fatalf("overflow round errored: %v", err)
	}
	if n := len(prov.recorded()); n == 0 {
		t.Fatal("overflow round never reached the summarizer")
	}
	if v := a.currentProjectionVersion(); v == seeded {
		t.Fatal("overflow round did not install a projection")
	}
}

// 门判据纯函数钉：收纳比与残留双条件，微会话由残留条件保护；绝对下限随
// 窗口收缩（roundFoldYieldFloor），小窗口不被平顶下限整片误杀。
func TestTl1713LowYieldRoundPredicate(t *testing.T) {
	floor := 8_192
	cases := []struct {
		name        string
		yield       int
		source      int
		floor       int
		wantDecline bool
	}{
		{"collapse-digest-only", 0, 811_000, floor, true},
		{"thin-yield-high-residual", 5_000, 811_000, floor, true},
		{"healthy-yield", 81_100, 811_000, floor, false},
		{"tiny-session-residual-guard", 5_000, 12_000, floor, false},
		{"absolute-floor-boundary", 8_192, 81_920, floor, false},
		{"empty-source", 0, 0, floor, false},
		{"small-window-shrunk-floor-passes", 800, 3_300, 625, false},
		{"small-window-shrunk-floor-declines", 0, 3_300, 625, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lowYieldRound(tc.yield, tc.source, tc.floor); got != tc.wantDecline {
				t.Fatalf("lowYieldRound(%d, %d, %d) = %v, want %v", tc.yield, tc.source, tc.floor, got, tc.wantDecline)
			}
		})
	}
}

// 停车重试步长钉：窗口相关下限封底。
func TestTl1713ParkRetryGrowthTokens(t *testing.T) {
	a := New(&fakeProvider{}, tool.NewRegistry(), &Session{}, Options{}, event.Discard)
	a.contextWindow = 1_000_000
	if got := a.parkRetryGrowthTokens(); got != 62_500 {
		t.Fatalf("parkRetryGrowthTokens(1M) = %d, want 62500", got)
	}
	a.contextWindow = 4_000
	if got := a.parkRetryGrowthTokens(); got != parkRetryGrowthFloorTokens {
		t.Fatalf("parkRetryGrowthTokens(4K) = %d, want the %d floor", got, parkRetryGrowthFloorTokens)
	}
}
