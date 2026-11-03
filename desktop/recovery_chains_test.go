package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// 任务 1123：候选链「更长」「更新」两条独立读数——不再用一条合并文案
// （「更长且更新」）替用户把两个独立事实打包成一个「且」字。
//
// 根因（20261024 用户现场，GC开发专员会话）：旧形态只有 LongestPath/
// LongestHead（轮次优先），前端据此打「建议（更长且更新）」——但实际被推荐的长链
// （6800条/246轮，最后活动 2026-09-28）并不比落选短链（468条/
// 6轮，2026-10-22 折叠）更新，「且更新」这一半是失实的。
//
// tl1355（20261102 用户报）：tl1227/tl1280 把 schema-2 头重放退役后，
// ListSessionHeads 成为恒空垫片，候选链全部退化成 0·0+无预览——本测试
// 自 tl1236 的 SKIP 中复活：链读数已改为会话文件内容扫描合成
// （agent.ScanSessionChainViews），夹具随之从手写 schema-2 事件日志换成
// 真实 Save 写出的 schema-1 转录（与面板生产数据同形），场景重新可达且
// 显式在测，非静默。
//
// 断言三层：
//  1. 读数修复——每条候选链带真实条数/轮次/预览/最后活动（不再是 0·0+空）；
//  2. 「更长」按轮次指向主转录、「更新」按最后活动独立指向副本（两个事实
//     可分离、各带各的实际值）；
//  3. 覆盖关系由内容扫描标注——严格前缀副本 Covered=true，主转录不被覆盖。

// saveChainFixture 用真实 Save 写一份 schema-1 会话。base 决定消息时间戳
// （Save 持久化 CreatedAt，链读数的最后活动首源就是它）。
func saveChainFixture(t *testing.T, path string, pairs int, title string, base time.Time) {
	t.Helper()
	sess := agent.NewSession("sys")
	for i := 0; i < pairs; i++ {
		sess.Add(provider.Message{
			Role:      provider.RoleUser,
			Content:   title + " 问题" + string(rune('一'+i)),
			CreatedAt: base.Add(time.Duration(i) * time.Minute).UnixMilli(),
		})
		sess.Add(provider.Message{
			Role:      provider.RoleAssistant,
			Content:   title + " 回答" + string(rune('一'+i)),
			CreatedAt: base.Add(time.Duration(i)*time.Minute + 30*time.Second).UnixMilli(),
		})
	}
	if err := sess.Save(path); err != nil {
		t.Fatalf("save %s: %v", path, err)
	}
}

func TestListRecoveryChainsSplitsLongerAndNewer(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "chains-1123-long-old.jsonl")
	copyPath := filepath.Join(dir, "chains-1123-long-old-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		for _, p := range []string{mainPath, copyPath, mainPath + ".meta", copyPath + ".meta", mainPath + ".lock", copyPath + ".lock"} {
			_ = os.Remove(p)
		}
	})

	// 主转录：3 轮问答（更长），消息时间戳 2026-11-01（更旧）——用户现场被
	// 误标「且更新」的那条长链。Save 持久化 CreatedAt（最后活动首源），mtime
	// 只是内容无时间戳时的兜底，故新旧由消息时间戳决定，断言确定。
	saveChainFixture(t, mainPath, 3, "长会话", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))

	// 恢复副本：1 轮问答（更短），消息时间戳 2026-11-02（更新）——用户现场
	// 被埋没的那条新链。内容=主转录的前段，覆盖判定应把它标为被主转录覆盖。
	saveChainFixture(t, copyPath, 1, "长会话", time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))

	app := newBranchDeleteApp(t)
	set, err := app.ListRecoveryChains(mainPath)
	if err != nil {
		t.Fatalf("list recovery chains: %v", err)
	}
	if len(set.Chains) < 2 {
		t.Fatalf("fixture must yield at least 2 candidate chains (main + copy): %+v", set)
	}

	var mainChain, copyChain *RecoveryChainView
	for i := range set.Chains {
		switch set.Chains[i].Path {
		case mainPath:
			mainChain = &set.Chains[i]
		case copyPath:
			copyChain = &set.Chains[i]
		}
	}
	if mainChain == nil || copyChain == nil {
		t.Fatalf("fixture must place both the main transcript and the copy in the chain set: %+v", set)
	}

	// ① 读数修复：真实条数/轮次/预览/最后活动，不再是 0·0+空预览。
	if mainChain.MessageCount <= copyChain.MessageCount {
		t.Fatalf("fixture premise broken: main must be longer than the copy, got main=%d copy=%d", mainChain.MessageCount, copyChain.MessageCount)
	}
	if mainChain.Turns != 3 {
		t.Fatalf("main turns = %d, want 3（真实轮次，非 0·0 退化）", mainChain.Turns)
	}
	if copyChain.Turns != 1 {
		t.Fatalf("copy turns = %d, want 1", copyChain.Turns)
	}
	if mainChain.Preview == "" || copyChain.Preview == "" {
		t.Fatalf("chain rows must carry a real preview (0·0+无预览是退化现场), main=%q copy=%q", mainChain.Preview, copyChain.Preview)
	}
	if mainChain.LastActivity == "" || mainChain.LastActivity[:4] == "0001" {
		t.Fatalf("main must carry a real last-activity reading, got %q", mainChain.LastActivity)
	}
	if copyChain.LastActivity == "" || copyChain.LastActivity[:4] == "0001" {
		t.Fatalf("copy must carry a real last-activity reading, got %q", copyChain.LastActivity)
	}
	if mainChain.LastActivity >= copyChain.LastActivity {
		t.Fatalf("fixture premise broken: main must be older than the copy, got main=%q copy=%q", mainChain.LastActivity, copyChain.LastActivity)
	}
	// 无 DAG 后行身份=文件：HeadID 恒空，主转录行 Selected=当前会话。
	if mainChain.HeadID != "" || copyChain.HeadID != "" {
		t.Fatalf("head ids are retired with the DAG; rows identify by file, got main=%q copy=%q", mainChain.HeadID, copyChain.HeadID)
	}
	if !mainChain.Selected || copyChain.Selected {
		t.Fatalf("the main row is the live conversation (Selected), copy is not: %+v / %+v", mainChain, copyChain)
	}
	// ③ 覆盖关系：副本内容是主转录的严格前缀 → Covered；主转录不被覆盖。
	if !copyChain.Covered {
		t.Fatalf("the prefix copy must be marked covered by content scan: %+v", copyChain)
	}
	if mainChain.Covered {
		t.Fatalf("the main transcript must not be marked covered: %+v", mainChain)
	}

	// ② 「更长」按轮次/条数优先——指向主转录。
	if set.LongestPath != mainPath {
		t.Fatalf("Longest must point at the longer (main) chain, got %q", set.LongestPath)
	}
	// 「更新」按最后活动时间独立计算——必须指向副本，不能再跟着 Longest 一起
	// 指向主转录（这正是「更长且更新」合并文案的失实来源）。
	if set.NewestPath != copyPath {
		t.Fatalf("Newest must point at the more recently active chain (the copy), not follow Longest — got NewestPath=%q LongestPath=%q", set.NewestPath, set.LongestPath)
	}
	if set.NewestPath == set.LongestPath {
		t.Fatalf("Longer and newer must be independently computable and can point at different chains: got both=%q", set.NewestPath)
	}
}
