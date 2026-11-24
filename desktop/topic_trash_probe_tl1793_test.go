package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/store"
)

// tl1793 验收2：对话归档后按 topic 打开不再静默铸空壳。
//
// 20261124 真机链：对话的会话文件已随归档进回收站，topic 注册行未收敛 →
// 用户点击侧栏该对话 → findTopicSessionForTarget/tl1197 跨 scope 都只找活
// 文件返回空 → openTopicTabWithActivation 落进新建分支，createEmptySessionFile
// 铸出 0 字节投影+继承 topic 标题的 meta——「原始对话变空壳」（空壳桶实证：
// .jsonl 0 字节、无事件日志、meta.topic_title=原对话名、created_at=新建时刻）。
// 本组测试钉住：回收站有该 topic 的归档证据时拒绝新建并明说去向；无证据的
// 真新话题新建能力保持不变（tl1197 既有钉零回退）。

// writeArchivedTrashBucket lays down one trash bucket whose session meta
// carries topicID — the same shape the trash listing and restore flow read
// (bucket dir named by the transcript key, primary transcript beside its
// SessionMeta sidecar).
func writeArchivedTrashBucket(t *testing.T, dir, key, topicID string) {
	t.Helper()
	bucket := filepath.Join(dir, sessionTrashDir, key)
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatalf("mkdir bucket: %v", err)
	}
	primary := filepath.Join(bucket, key)
	if err := os.WriteFile(primary, []byte(""), 0o644); err != nil {
		t.Fatalf("write bucket transcript: %v", err)
	}
	meta := map[string]string{"id": key, "topic_id": topicID}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.SessionMeta(primary), b, 0o644); err != nil {
		t.Fatalf("write bucket meta: %v", err)
	}
}

// ① 主回归：topic 的会话全在回收站（同 topic 桶在场）时，按 topic 打开
// 返回归档态错误，且活目录不新增任何会话文件（不再铸空壳）。
func TestOpenGlobalTabRefusesArchivedTopicInsteadOfMintingShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_20261005-121306_tl1793"

	globalSessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(globalSessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	writeArchivedTrashBucket(t, globalSessionDir, "20261005-121308.838083100-collab.jsonl", topicID)
	before := sessionJSONLNames(t, globalSessionDir)

	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenGlobalTab(topicID)
	if !errors.Is(err, errTopicOpenArchived) {
		t.Fatalf("OpenGlobalTab err = %v, want errTopicOpenArchived", err)
	}
	_ = meta
	after := sessionJSONLNames(t, globalSessionDir)
	if len(after) != len(before) {
		t.Fatalf("global sessions dir grew %v -> %v, want no minted shell", before, after)
	}
}

// ② 同目录的回收站桶属于别的 topic（真·无此对话证据）时不误伤：新建能力
// 保持（tl1197 brand-new 钉的同语义扩展——有桶但不同 topic）。
func TestOpenGlobalTabStillMintsWhenTrashBucketIsAnotherTopic(t *testing.T) {
	isolateDesktopUserDirs(t)
	globalSessionDir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(globalSessionDir, 0o755); err != nil {
		t.Fatalf("mkdir global sessions: %v", err)
	}
	writeArchivedTrashBucket(t, globalSessionDir, "20260901-000000.000000000-other.jsonl", "topic_other_tl1793")

	app := NewApp()
	app.ctx = context.Background()
	t.Cleanup(func() { app.shutdown(context.Background()) })

	if _, err := app.OpenGlobalTab("topic_fresh_tl1793"); err != nil {
		t.Fatalf("OpenGlobalTab for an unrelated topic must still mint: %v", err)
	}
}

// ③ 探针单测：跨目录扫描命中/未命中与证据缺失形态。
func TestTopicArchivedInTrashDirsEvidence(t *testing.T) {
	dir := t.TempDir()
	writeArchivedTrashBucket(t, dir, "20261124-082936.241807500-plan.jsonl", "topic_hit")

	if !topicArchivedInTrashDirs([]string{dir}, "topic_hit") {
		t.Fatal("bucket with matching topic_id must count as archived evidence")
	}
	if topicArchivedInTrashDirs([]string{dir}, "topic_miss") {
		t.Fatal("bucket of another topic must not match")
	}
	if topicArchivedInTrashDirs([]string{dir}, "") {
		t.Fatal("empty topic id must short-circuit")
	}
	if topicArchivedInTrashDirs([]string{filepath.Join(dir, "missing")}, "topic_hit") {
		t.Fatal("missing trash dir carries no evidence")
	}
}
