package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 1123：候选链「更长」「更新」两条独立读数——不再用一条合并文案
// （「更长且更新」）替用户把两个独立事实打包成一个「且」字。
//
// 根因（20261024 用户现场，GC开发专员会话）：旧形态只有 LongestPath/
// LongestHead（轮次优先），前端据此打「建议（更长且更新）」——但实际被推
// 荐的长链（6800条/246轮，最后活动 2026-09-28）并不比落选短链（468条/
// 6轮，2026-10-22 折叠）更新，「且更新」这一半是失实的。
//
// 修法：RecoveryChainSet 新增 NewestPath/NewestHead，与 LongestPath/
// LongestHead 独立计算（前者按最后活动时间取最大值，后者维持原轮次优先
// 排序的首个非降级链），前端据此打两条独立标签、各带自己的实际值。
//
// 本测试钉住数据层：构造一个「长但旧」的主转录 + 一个「短但新」的恢复
// 副本（时间戳手工错开），断言 Longest* 与 Newest* 指向不同的链。

func writeSchema2EventLog(t *testing.T, path string, msgCount int, base time.Time) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	head := agent.SessionMainHead
	prev := ""
	for i := 0; i < msgCount; i++ {
		id := agent.NewMessageID()
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		entry := map[string]any{
			"schema_version": 2,
			"type":           "message",
			"id":             id,
			"head":           head,
			"at":             base.Add(time.Duration(i) * time.Second).UTC().Format(time.RFC3339Nano),
			"parent":         prev,
			"digest":         "",
			"msgs":           []any{map[string]any{"role": string(role), "content": "q", "id": id}},
		}
		if err := enc.Encode(entry); err != nil {
			t.Fatal(err)
		}
		prev = id
	}
	if err := os.WriteFile(store.SessionEventLog(path), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
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
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
		_ = os.Remove(store.SessionEventLog(mainPath))
		_ = os.Remove(store.SessionEventLog(copyPath))
	})

	// 主转录：条数多（更长），但最后活动时间早（更旧）——用户现场被误标
	// 「且更新」的那条长链。
	oldBase := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	writeSchema2EventLog(t, mainPath, 40, oldBase)

	// 恢复副本：条数少（更短），但最后活动时间晚（更新）——用户现场被埋没
	// 的那条新链。用真实的 session Save 写入（而不是手写事件日志）——手写的
	// 副本事件日志缺 meta（agent.EnsureBranchMeta 只有在 session 真实保存过
	// 之后才有意义），下游 panelSessionHeads 会因为读不到分支身份而跳过它，
	// 这不是本测试要钉的行为，因此用最贴近生产的一条路（Save）写副本。
	copySession := agent.NewSession("sys")
	copySession.Add(provider.Message{Role: provider.RoleUser, Content: "q1"})
	copySession.Add(provider.Message{Role: provider.RoleAssistant, Content: "a1"})
	copySession.Add(provider.Message{Role: provider.RoleUser, Content: "q2"})
	copySession.Add(provider.Message{Role: provider.RoleAssistant, Content: "a2"})
	if err := copySession.Save(copyPath); err != nil {
		t.Fatalf("save copy session: %v", err)
	}
	// 副本的最后活动时间需要晚于主转录（fixture 前提）：真实 Save 会用当前
	// 时间戳，晚于主转录手写的 2026-09-28——足够"新"，不需要额外伪造时间。

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
	if mainChain.MessageCount <= copyChain.MessageCount {
		t.Fatalf("fixture premise broken: main must be longer than the copy, got main=%d copy=%d", mainChain.MessageCount, copyChain.MessageCount)
	}
	if mainChain.LastActivity >= copyChain.LastActivity {
		t.Fatalf("fixture premise broken: main must be older than the copy, got main=%q copy=%q", mainChain.LastActivity, copyChain.LastActivity)
	}

	// 「更长」按轮次/条数优先——指向主转录。
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

	// 标签值与实际值一致：断言读出的链确实带着「更长」「更新」各自声称的
	// 那个数（条数/轮次、最后活动）。
	if mainChain.Turns == 0 {
		t.Fatalf("the longer chain must carry a real turn count (the longer tag shows it), got %+v", mainChain)
	}
	// 副本用真实 Save 写入，最后活动时间是当前时刻（晚于主转录手写的
	// 2026-09-28），不必断言具体日期——只需要它确实比主转录"更新"，且这个
	// 日期是真实的（不是编造的零值）。
	if copyChain.LastActivity == "" || copyChain.LastActivity[:4] == "0001" {
		t.Fatalf("the newer chain must carry a real last-activity timestamp (not a zero value), got %+v", copyChain)
	}
}
