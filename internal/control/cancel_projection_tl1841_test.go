package control

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// tl1841 ③（d1 扫描 G3）：cancel 清理截断（replaceSessionAfterCancel）必须
// 与同构路径 rewind（rewind.go:83,110）和自愈 diet（selfheal_overflow_diet.go:278）
// 一样通知投影层（InvalidateProjectionIfStale）。
//
// 触发面：回合中折叠（compact_active_turn activeTurnFoldBoundary）把活跃轮前段
// 冻进 covered 前缀后，cancel 回退到轮起点即投影失配。修复前进程内靠 bounded
// stale splice 自愈（多付一次非计划重建）；**重启**则 covered_count_overflow/
// hash 失配 → 投影 drop → 全量回退重建 episode——20261116/20261117「装机重启
// 投影失效」事故家族的非计划入口之一。
//
// 钉两面：
//  A. 截断边界落进 covered 前缀 ⇒ 投影当场失效（版本翻新）+ 盘上失配侧车
//     同步退役（重启不再撞 drop）；
//  B. 尾部截断（边界在 covered 之外）⇒ 投影保持（零成本返回契约，对齐
//     TestTailRewindKeepsCompactionProjection 的尾回溯契约）。

// tl1841CancelFixture 造一个「投影已安装、covered 前缀跨过夹在中间的 bulk」
// 的控制器（夹具体量红线同 TestTailRewindKeepsCompactionProjection：15×100
// 行 ≈2.2k tokens，值得折叠且离触发线与硬上限均有余量）。
func tl1841CancelFixture(t *testing.T) (*Controller, *agent.Agent) {
	t.Helper()
	dir := t.TempDir()
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		textTurn("first answer"),
		textTurn("second answer"),
	}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{
		ContextWindow: 10_000, CompactRatio: 0.80, RecentKeep: 2,
	}, event.Discard)
	c := New(Options{
		Runner:     ag,
		Executor:   ag,
		SessionDir: dir,
		Label:      "test",
		Sink:       event.Discard,
	})
	c.SetSessionPath(agent.NewSessionPath(dir, "test"))
	ctx := context.Background()
	if err := c.runTurnWithRaw(ctx, "first prompt", "first prompt"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	big := strings.Repeat("line\n", 100)
	sess := ag.Session()
	for i := range 15 {
		id := strings.Join([]string{"bulk", string(rune('a' + i))}, "-")
		sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file", Arguments: "{}"}}})
		sess.Add(provider.Message{Role: provider.RoleTool, ToolCallID: id, Name: "read_file", Content: big})
	}
	if err := c.runTurnWithRaw(ctx, "second prompt", "second prompt"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if err := ag.CompactNow(ctx, ""); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return c, ag
}

// TestCancelTruncationIntoCoveredPrefixInvalidatesProjection tl1841③ 钉 A：
// cancel 截断进 covered 前缀 ⇒ 投影当场失效 + 盘上失配侧车退役（重启不 drop）。
func TestCancelTruncationIntoCoveredPrefixInvalidatesProjection(t *testing.T) {
	c, ag := tl1841CancelFixture(t)
	before := ag.ContextMaintenanceSnapshot()
	if before.ProjectionVersion == 0 {
		t.Fatalf("pre-cancel snapshot = %+v, want an installed projection", before)
	}
	path := c.SessionPath()
	if _, ok, err := agent.LoadCompactionState(path); err != nil || !ok {
		t.Fatalf("pre-cancel projection sidecar missing: ok=%v err=%v", ok, err)
	}

	// 截断进 covered 前缀：只留 system+首条 user（bulk 与两轮都在 covered 里）。
	msgs := ag.Session().Snapshot()
	if len(msgs) < 4 {
		t.Fatalf("fixture transcript too short to truncate into the covered prefix: %d msgs", len(msgs))
	}
	c.replaceSessionAfterCancel(msgs[:2])

	after := ag.ContextMaintenanceSnapshot()
	if after.ProjectionVersion == before.ProjectionVersion {
		t.Fatalf("projection version %d unchanged after a cancel truncation into the covered prefix — the cancel path must notify the projection layer (rewind parity, tl1841 G3)", before.ProjectionVersion)
	}
	// 盘上失配侧车已退役：重启不再撞 covered_count_overflow/hash 失配的 drop。
	if _, ok, err := agent.LoadCompactionState(path); err != nil || ok {
		t.Fatalf("stale projection sidecar survived the cancel truncation: ok=%v err=%v (restart would drop it as covered_count_overflow)", ok, err)
	}
}

// TestCancelTailTruncationKeepsProjection tl1841③ 钉 B：cancel 只删 covered
// 之外的尾部 ⇒ 投影保持（InvalidateProjectionIfStale 的零成本返回契约）。
func TestCancelTailTruncationKeepsProjection(t *testing.T) {
	c, ag := tl1841CancelFixture(t)
	before := ag.ContextMaintenanceSnapshot()
	if before.ProjectionVersion == 0 {
		t.Fatalf("pre-cancel snapshot = %+v, want an installed projection", before)
	}

	// 尾部截断：只去掉最后一条（covered 前缀之外）。
	msgs := ag.Session().Snapshot()
	c.replaceSessionAfterCancel(msgs[:len(msgs)-1])

	after := ag.ContextMaintenanceSnapshot()
	if after.ProjectionVersion != before.ProjectionVersion {
		t.Fatalf("projection version %d -> %d on a tail-only cancel truncation; the covered prefix stayed byte-identical so the fold must be kept",
			before.ProjectionVersion, after.ProjectionVersion)
	}
	if after.ProjectedTokens >= after.FoldTrigger {
		t.Fatalf("post-cancel view %d tokens at or above fold %d, want the compacted size kept", after.ProjectedTokens, after.FoldTrigger)
	}
}
