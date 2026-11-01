package sessioncatalog

import (
	"context"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// tl1331 (G11 中等修半体): SyncMetadata 清理豁免的「global 行但归属判
// project」例外。
//
// 清理 DELETE 的既有豁免是「session 衍生的 topic 行不删」——catalog_sessions
// 行屏蔽 catalog_topics 行。tl1302 深查证明这正是泄露行永不收敛的通道：
// 0 字节空壳文件让 catalog_sessions(global, T) 永存，global 泄露行被屏蔽
// 到永远。唯一归属契约（project wins）下，「同一 sync 里被喂成 project 的
// topic」在 global scope 的行就是泄露行，豁免必须让位。
//
// 本测钉三面：
//  1. 泄露行收敛——同 sync 喂 project 后，global 行连同其 session 屏蔽一起
//     被 DELETE（pre-tl1331 此处红：屏蔽豁免让泄露行永存）；
//  2. 既有豁免不动——从未被喂入的 session 衍生行（older-CLI 形态）照旧保留；
//  3. project 侧行正常建立。
func TestSyncMetadataCleanupExceptionForProjectFedGlobalRowsTL1331(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	leakPath := filepath.Join(dir, "leak.jsonl")
	legacyPath := filepath.Join(dir, "legacy.jsonl")
	saveTl1331Session(t, leakPath, "leaked question", "leaked answer")
	saveTl1331Session(t, legacyPath, "legacy question", "legacy answer")
	for path, meta := range map[string]agent.BranchMeta{
		leakPath:   {Scope: "global", TopicID: "leak", TopicTitle: "Leaked"},
		legacyPath: {Scope: "global", TopicID: "legacy", TopicTitle: "Legacy"},
	} {
		if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := Open(ctx, Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	// 目录扫描先行：两个 session 行（global scope）+ 两个 session 衍生的
	// topic 行（metadata_present=0）落库——这就是「清理豁免」的屏蔽面。
	if err := catalog.ReconcileDirectory(ctx, DirectoryTarget{Path: dir, Scope: "global"}); err != nil {
		t.Fatal(err)
	}

	projectRoot := filepath.Join(t.TempDir(), "proj")
	// 收敛前状态（1262/1283 之前的世界）：global 喂入仍带着泄露 topic。
	fedGlobal := []ProjectRecord{{Scope: "global", Title: "Global"}}
	fedGlobalTopics := []TopicMetadata{{Scope: "global", TopicID: "leak", Title: "Leaked"}}
	if err := catalog.SyncMetadata(ctx, fedGlobal, fedGlobalTopics); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := catalog.GetTopicWithSessionCount(ctx, TopicKey{Scope: "global", TopicID: "leak"}); err != nil || !ok {
		t.Fatalf("pre-convergence global row missing: ok=%v err=%v", ok, err)
	}

	// 收敛后（tl1262/1283 + tl1331 G10 世界）：leak 只喂 project——global 行
	// 是泄露行，session 屏蔽不得再豁免它。
	fedProject := []ProjectRecord{
		{Scope: "global", Title: "Global"},
		{Scope: "project", WorkspaceRoot: projectRoot, Title: "Proj"},
	}
	fedProjectTopics := []TopicMetadata{{Scope: "project", WorkspaceRoot: projectRoot, TopicID: "leak", Title: "Leaked"}}
	if err := catalog.SyncMetadata(ctx, fedProject, fedProjectTopics); err != nil {
		t.Fatal(err)
	}

	if _, _, ok, err := catalog.GetTopicWithSessionCount(ctx, TopicKey{Scope: "global", TopicID: "leak"}); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("global leak row survived a sync that fed the topic under project scope — the session shield must not exempt project-attributed global rows (tl1331)")
	}
	if _, _, ok, err := catalog.GetTopicWithSessionCount(ctx, TopicKey{Scope: "project", WorkspaceRoot: projectRoot, TopicID: "leak"}); err != nil || !ok {
		t.Fatalf("project-side row missing after the convergence sync: ok=%v err=%v", ok, err)
	}
	// 既有豁免不动：legacy 从未被喂入（older-CLI session 衍生形态），global
	// 行必须照旧保留。
	if _, _, ok, err := catalog.GetTopicWithSessionCount(ctx, TopicKey{Scope: "global", TopicID: "legacy"}); err != nil || !ok {
		t.Fatalf("unfed session-derived global row was removed: ok=%v err=%v — the original exemption must keep holding", ok, err)
	}
}

// saveTl1331Session 落一个带内容的最小转录（shield 需要 catalog_sessions 行）。
func saveTl1331Session(t *testing.T, path string, messages ...string) {
	t.Helper()
	s := agent.NewSession("sys")
	for i, message := range messages {
		role := provider.RoleUser
		if i%2 == 1 {
			role = provider.RoleAssistant
		}
		s.Add(provider.Message{Role: role, Content: message})
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
}
