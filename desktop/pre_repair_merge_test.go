package main

// tl1787 件3/B：pre-repair 备份「合并回主线」桥的桌面侧单测。
//
// 闸面三态 + 端到端一态：
//  ① 孤儿备份（宿主不在）拒绝——没有可回的主线；
//  ② 宿主已归档（宿主与备份同桶，tl1787 新 14 形态）拒绝——先还原再合并；
//  ③ 已知会话目录之外的路径拒绝（与删除闸同一纪律）；
//  ④ 活宿主 + 备份：合并成功（graft 语义由 agent 层单测钉死，这里钉桌面
//     面的回执读数与落盘结果——「合并后历史可查」的清单读数同步）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
)

// seedMergeFixture 建折叠场景：活宿主=折叠后主线（E/D/F/G），备份=折叠前全文
// （A/B/C/D，备份目录 <stem>.pre-repair-<stamp> 内 <stem>.jsonl）。返回备份目录。
func seedMergeFixture(t *testing.T, sessions, stem string) string {
	t.Helper()
	owner := filepath.Join(sessions, stem+".jsonl")
	ownerSession := agent.NewSession("sys")
	ownerSession.Add(provider.Message{Role: provider.RoleUser, Content: "E folded summary"})
	ownerSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "D kept"})
	ownerSession.Add(provider.Message{Role: provider.RoleUser, Content: "F new question"})
	ownerSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "G new answer"})
	if err := ownerSession.Save(owner); err != nil {
		t.Fatalf("save owner: %v", err)
	}
	backup := filepath.Join(sessions, stem+".pre-repair-20261110T010000")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	backupSession := agent.NewSession("sys")
	backupSession.Add(provider.Message{Role: provider.RoleUser, Content: "A original"})
	backupSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "B original"})
	backupSession.Add(provider.Message{Role: provider.RoleUser, Content: "C original"})
	backupSession.Add(provider.Message{Role: provider.RoleAssistant, Content: "D kept"})
	if err := backupSession.Save(filepath.Join(backup, stem+".jsonl")); err != nil {
		t.Fatalf("save backup transcript: %v", err)
	}
	return backup
}

func TestPreRepairMergeRefusesOrphanBackup(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup := seedMergeFixture(t, sessions, "orphan")
	if err := os.Remove(filepath.Join(sessions, "orphan.jsonl")); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	if _, err := app.MergePreRepairBackup(backup); err == nil {
		t.Fatal("an orphan backup has no main line to merge into — must refuse")
	}
}

func TestPreRepairMergeRefusesArchivedOwner(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup := seedMergeFixture(t, sessions, "arch14")
	// tl1787 新 14 案例 14 形态：归档链把宿主转录与备份**一起**搬进回收站桶
	// （store.SessionTrashSidecars 的 glob）——桶内备份旁侧 owner「存在」但
	// 它是归档工件；合并必须先还原对话。
	bucket := filepath.Join(sessions, ".trash", "arch14.jsonl-deleted-1")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(sessions, "arch14.jsonl"), filepath.Join(bucket, "arch14.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, filepath.Join(bucket, filepath.Base(backup))); err != nil {
		t.Fatal(err)
	}
	meta := `{"key":"arch14.jsonl","deletedAt":1729000000000}`
	if err := os.WriteFile(filepath.Join(bucket, sessionTrashMetaFile), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	_, err := app.MergePreRepairBackup(filepath.Join(bucket, filepath.Base(backup)))
	if err == nil {
		t.Fatal("an archived owner must refuse the merge until the conversation is restored")
	}
	if !strings.Contains(err.Error(), "archived") {
		t.Fatalf("err = %v, want the archived-owner reason", err)
	}
}

func TestPreRepairMergeRefusesOutsideSessionDirs(t *testing.T) {
	stagePreRepairFixture(t)
	backup := seedMergeFixture(t, t.TempDir(), " rogue")

	app := &App{}
	if _, err := app.MergePreRepairBackup(backup); err == nil {
		t.Fatal("merge must refuse paths outside known session dirs")
	}
}

func TestPreRepairMergeGraftsIntoLiveOwner(t *testing.T) {
	sessions := stagePreRepairFixture(t)
	backup := seedMergeFixture(t, sessions, "fork14")
	owner := filepath.Join(sessions, "fork14.jsonl")

	app := &App{}
	view, err := app.MergePreRepairBackup(backup)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !view.Merged || view.Grafted <= 0 {
		t.Fatalf("view = %+v, want a real graft", view)
	}
	if view.OwnerPath != owner {
		t.Fatalf("owner = %q, want %q", view.OwnerPath, owner)
	}
	if view.AfterMessages <= view.Grafted {
		t.Fatalf("afterMessages = %d, want the merged total (> grafted)", view.AfterMessages)
	}
	// 落盘核对：主线首条用户消息变为备份的开场轮（旧史在前、新史在后）。
	s, err := agent.LoadSession(owner)
	if err != nil {
		t.Fatalf("load merged owner: %v", err)
	}
	msgs := s.Snapshot()
	if got := agent.UserMessageText(msgs[1]); got != "A original" {
		// msgs[0] 是 system 前导（NewSession 的身份行），msgs[1] 应是接回的
		// 备份开场轮。
		t.Fatalf("first user message after merge = %q, want %q", got, "A original")
	}
}
