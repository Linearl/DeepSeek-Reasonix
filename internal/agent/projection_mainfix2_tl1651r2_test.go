package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// tl1651 主修②验收矩阵（先写矩阵后落地）：
//
//	#     验收项                                                       钉
//	R2-1  关闭会话 diet 折叠提交 → 盘上 sidecar 同步作废+日志在场        TestTL1651R2DietRetiresSidecarOnDisk/folded
//	R2-2  diet 未折叠（不超窗）→ sidecar 保留、无 invalidated 日志       TestTL1651R2DietRetiresSidecarOnDisk/unfolded
//	R2-3  作废后转录仍是折叠形态（diet 本体语义零回退）                  …/folded 内断言
//
// 方案定案（详见报告 §主修②）：diet 折薄 canonical 后旧 sidecar 的 covered
// 必然越界（covered_count_overflow），投影无救援价值——「带尾部水位」「加载时
// 迁移」两候选都不可行（前者重写 sidecar 格式触碰 non-goals；后者会让 diet 后
// 尾部新增内容进不了 wire）。最小档=折叠提交事务后显式作废，与活会话路径
// （SelfHealOpenSession 的 InvalidateProjectionIfStale，tl1601）对称。

// tl1651R2OverwindowTranscript builds a system-led transcript whose estimate
// breaches a tiny window, so the diet's strip/fold ladder actually commits.
func tl1651R2OverwindowTranscript() []provider.Message {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "sys"},
	}
	// 一段历史大正文（足够把 300-token 窗口顶到 0.95x 折线上）。
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: strings.Repeat("old history block ", 120)})
	msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: strings.Repeat("old answer block ", 120)})
	// 尾部两条小消息：折叠预算（0.6×window）内保留。
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: "recent question"})
	msgs = append(msgs, provider.Message{Role: provider.RoleAssistant, Content: "recent answer"})
	return msgs
}

func TestTL1651R2DietRetiresSidecarOnDisk(t *testing.T) {
	t.Run("folded_sidecar_retired", func(t *testing.T) {
		dir := t.TempDir()
		path := dir + "/s.jsonl"
		msgs := tl1651R2OverwindowTranscript()
		sess := NewSession("")
		for _, m := range msgs {
			sess.Add(m)
		}
		if err := sess.Save(path); err != nil {
			t.Fatal(err)
		}
		// 预置一个与diet 前转录同代的合法 sidecar（covered 覆盖到倒数第二条）。
		n := len(msgs) - 1
		st := CompactionState{
			SchemaVersion:  compactionStateSchemaV4,
			PromptCacheKey: promptCacheKey("ws", BranchID(path), "m"),
			Projection: ContextProjection{
				Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
				CoveredCount:      n,
				CoveredPrefixHash: coveredPrefixHash(msgs, n),
				PinnedContextHash: pinnedContextCoverageHash(msgs, n),
			},
		}
		if err := SaveCompactionState(path, st); err != nil {
			t.Fatal(err)
		}

		buf, restore := tl1651TestCapture(t, slog.LevelInfo)
		defer restore()

		acts, skip := dietSessionProjection(context.Background(), path, 300)
		if skip != "" {
			t.Fatalf("diet skipped unexpectedly: %s", skip)
		}
		if !dietFoldCommitted(acts) {
			t.Fatalf("diet actions must include a done strip/fold, actions=%+v", acts)
		}

		// 钉 R2-1：盘上 sidecar 已同步作废——下次加载读到的是 sidecar_missing，
		// 而不是 covered_count_overflow 的意外丢弃。
		if _, ok, err := LoadCompactionState(path); err != nil {
			t.Fatal(err)
		} else if ok {
			t.Fatal("stale sidecar must be retired by the diet commit")
		}
		// 钉 R2-3：折叠本体语义零回退——盘上转录确实变小了。
		reloaded, err := LoadSession(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(reloaded.Messages); got >= len(msgs) {
			t.Fatalf("dieted transcript must shrink, before=%d after=%d", len(msgs), got)
		}
		// 日志在场。
		if log := buf.String(); !strings.Contains(log, "context projection invalidated by diet") {
			t.Fatalf("diet invalidation trail missing, log:\n%s", log)
		}
	})

	t.Run("unfolded_sidecar_kept", func(t *testing.T) {
		dir := t.TempDir()
		path := dir + "/s.jsonl"
		msgs := []provider.Message{
			{Role: provider.RoleSystem, Content: "sys"},
			{Role: provider.RoleUser, Content: "small talk"},
			{Role: provider.RoleAssistant, Content: "ok"},
		}
		sess := NewSession("")
		for _, m := range msgs {
			sess.Add(m)
		}
		if err := sess.Save(path); err != nil {
			t.Fatal(err)
		}
		n := len(msgs)
		st := CompactionState{
			SchemaVersion:  compactionStateSchemaV4,
			PromptCacheKey: promptCacheKey("ws", BranchID(path), "m"),
			Projection: ContextProjection{
				Messages:          []provider.Message{{Role: provider.RoleSystem, Content: "sys"}, formatSummaryMessage("earlier work")},
				CoveredCount:      n,
				CoveredPrefixHash: coveredPrefixHash(msgs, n),
				PinnedContextHash: pinnedContextCoverageHash(msgs, n),
			},
		}
		if err := SaveCompactionState(path, st); err != nil {
			t.Fatal(err)
		}

		buf, restore := tl1651TestCapture(t, slog.LevelInfo)
		defer restore()

		_, skip := dietSessionProjection(context.Background(), path, 100000)
		if skip == "" {
			t.Fatal("a fitting projection must report a skip detail")
		}
		if _, ok, err := LoadCompactionState(path); err != nil {
			t.Fatal(err)
		} else if !ok {
			t.Fatal("a skipped diet must keep the healthy sidecar")
		}
		if log := buf.String(); strings.Contains(log, "context projection invalidated by diet") {
			t.Fatalf("no invalidation may be logged for a skipped diet, log:\n%s", log)
		}
	})
}
