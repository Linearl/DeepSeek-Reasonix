package agent

// tl1695 — tl1600 主根因修复钉：active turn 排除使压力压缩的折叠区被钳到
// active（compact_projection.go planFoldRegion），长回合大会话只收走旧前缀
// 小尾巴，压后视图仍过半（tl1600 ②现场：533,877/1M=53.4%），压缩追着回合
// 跑且每次装成都断一次 provider 前缀缓存。
//
// 修复 = 压力触发复用 overflow rescue 已有的 activeTurnFoldBoundary 分割
// 语义（保最新 activeTurnKeepRounds 个 assistant 轮逐字，fold 只覆盖
// prompt+较早已完成轮次）。manual/tool 触发保持用户指定范围语义不变。
//
// tl1713fix 补修（用户 20261120 00:4x 裁定候选乙）：pressure 道的轮内分割
// 升级为完整轮内分割（activeTurnFullSplitBoundary）——分割域从被尾部预算
// 钳短的 [active+1:start) 放宽到完整活跃轮，且轮数不足分割时不再把折叠上
// 界砍回 active（零头折叠/folded=1 形态根因，详见
// compact_active_fullsplit_tl1713fix_test.go）。本文件三钉的 fixture 形态
// （活跃轮 6 轮、预算域内 >2 轮）在新旧语义下折叠边界同点，断言原样保持
// 绿——分割契约「折旧轮全折+保最新 2 轮逐字」不变，只是达成机制更强。
//
// fixture 用 assistant 大消息（非 tool 结果）：prune 道不生效，压力必须走
// 真正的摘要折叠——与 tl1600 现场「116 条消息只值 39K」的长回合形态同构。

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1695SplitFixture builds a session whose ACTIVE turn dwarfs the completed
// history: a small old prefix, then one active user turn carrying six large
// assistant/user rounds. The whole view sits in the pressure band
// (fold, hard) so a pressure Prepare must fold — and the fold can only
// converge if it may split the active turn's older completed rounds.
func tl1695SplitFixture(t *testing.T) (*Agent, *Session, int64) {
	t.Helper()
	const activeCreatedAt = int64(424242)
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
		{Role: provider.RoleUser, Content: strings.Repeat("old request ", 200)},
		{Role: provider.RoleAssistant, Content: strings.Repeat("old answer ", 200)},
		{Role: provider.RoleUser, Content: "huge active turn", CreatedAt: activeCreatedAt},
	}
	for i := range 6 {
		// ≈7.2K billed tokens per round, unique marker per round so the
		// verbatim/summarized assertions can tell rounds apart.
		msgs = append(msgs,
			provider.Message{Role: provider.RoleAssistant, Content: fmt.Sprintf("active round %d body ", i) + strings.Repeat("x", 28780)},
			provider.Message{Role: provider.RoleUser, Content: fmt.Sprintf("round %d ack", i)},
		)
	}
	sess := &Session{Messages: msgs}
	a := New(&fakeProvider{reply: "short digest"}, tool.NewRegistry(), sess, Options{
		ContextWindow: 50_000, CompactRatio: 0.85, RecentKeep: 2,
	}, event.Discard)
	a.activeTurnCreatedAt.Store(activeCreatedAt)
	return a, sess, activeCreatedAt
}

func roundBodyMarker(i int) string { return fmt.Sprintf("active round %d body ", i) }

// 修复前现场复刻（红）：折叠区被钳到 active，第一轮只折 ~1.2K 旧前缀，压后
// 视图仍 ≥fold；第二轮折叠区只剩旧摘要（低于经济地板）→ noop → blocked
// 收据，视图永远收敛不了——「追着回合跑」的测试级复刻。
// 修复后（绿）：折叠区覆盖 prompt+较早轮次，压后视图收敛到 <50% 窗口，最新
// 两轮逐字保留。
func TestTl1695PressureFoldSplitsActiveTurnOlderRounds(t *testing.T) {
	a, _, _ := tl1695SplitFixture(t)

	if est, fold := a.contextManager().currentPrepared().InputTokens, a.compactTrigger(); est < fold {
		t.Fatalf("fixture estimates %d tokens, below the fold trigger %d — it cannot exercise pressure", est, fold)
	}
	if est, hard := a.contextManager().currentPrepared().InputTokens, a.hardInputCeiling(); est >= hard {
		t.Fatalf("fixture estimates %d tokens, at/above the hard ceiling %d — it must sit in the pressure band", est, hard)
	}

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure prepare = %v, want the fold to land", err)
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" || r.Action != "summary" {
		t.Fatalf("receipt = %+v, want an applied summary fold (blocked/noop = the fold clamped to the active turn and nothing was left to fold)", r)
	}

	// 验收①：压后视图收敛到健康水位——低于触发线且低于半窗（tl1600 ②）。
	after := a.contextManager().currentPrepared().InputTokens
	if after >= a.compactTrigger() {
		t.Fatalf("post-fold view = %d tokens, still at/above the fold trigger %d — the active turn was not split", after, a.compactTrigger())
	}
	if half := a.effectiveContextWindow() / 2; after >= half {
		t.Fatalf("post-fold view = %d tokens, not under half the window %d (验收 <50%%)", after, half)
	}

	// 分割契约：最新 activeTurnKeepRounds 个 assistant 轮逐字保留。
	visible := a.modelVisibleMessages()
	var joined strings.Builder
	for _, m := range visible {
		joined.WriteString(m.Content)
	}
	view := joined.String()
	if strings.Contains(view, "huge active turn") {
		t.Fatal("the active turn prompt stayed verbatim; the fold did not cover it")
	}
	for _, i := range []int{0, 1, 2, 3} {
		if strings.Contains(view, roundBodyMarker(i)) {
			t.Fatalf("round %d stayed verbatim in the view; the fold did not cover the older completed rounds", i)
		}
	}
	// activeTurnKeepRounds=2：最新两个 assistant 轮（round4/5）逐字保留。
	for _, i := range []int{4, 5} {
		if !strings.Contains(view, roundBodyMarker(i)) {
			t.Fatalf("round %d missing from the view; the split kept too little", i)
		}
	}
}

// manual 触发保持现状（non-goal 不放开）：用户指定的范围语义下折叠区仍钳
// 到 active，active turn 的 prompt 逐字保留在视图里。
func TestTl1695ManualFoldStillKeepsActiveTurnVerbatim(t *testing.T) {
	a, _, _ := tl1695SplitFixture(t)

	if err := a.CompactNow(context.Background(), ""); err != nil {
		t.Fatalf("manual compact: %v", err)
	}
	r := a.sess.compactionState.LastReceipt
	if r == nil || r.Status != "applied" {
		t.Fatalf("receipt = %+v, want an applied manual fold", r)
	}
	visible := a.modelVisibleMessages()
	found := false
	for _, m := range visible {
		if m.Role == provider.RoleUser && m.Content == "huge active turn" && m.CreatedAt == 424242 {
			found = true
		}
	}
	if !found {
		t.Fatal("manual fold disturbed the active turn; the user-specified range contract changed")
	}
}

// captureSlogTl1695 redirects the default logger into a buffer for the
// duration of the test and returns the buffer (same pattern as the 912/429
// fixtures — these tests are not parallel).
func captureSlogTl1695(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// 观测钉：分割发生的 done 行必须带 split_active=true 与距上次成功压缩的
// 间隔读数（追着回合跑的读数半边；首次压缩读 -1）。
func TestTl1695SplitActiveDoneLineIsObservable(t *testing.T) {
	buf := captureSlogTl1695(t)
	a, _, _ := tl1695SplitFixture(t)

	if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
		t.Fatalf("pressure prepare = %v, want the fold to land", err)
	}
	out := buf.String()
	if !strings.Contains(out, "agent: compaction done") {
		t.Fatalf("no compaction done line in the log: %s", out)
	}
	if !strings.Contains(out, "split_active=true") {
		t.Fatalf("done line missing split_active=true: %s", out)
	}
	if !strings.Contains(out, "compaction_interval_min=") {
		t.Fatalf("done line missing the compaction interval readout: %s", out)
	}
}
