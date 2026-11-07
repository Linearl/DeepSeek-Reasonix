package main

// tl1429d 回归钉:回收站「同名多条」修复面。
// 现场(包 81 装机实测 2026-11-07 10:17 desktop.log):一次归档级联把主线桶
// +17 个 recovery 副本桶搬进 .trash,ListTrashedSessions 逐桶出条且标题同源
// ——用户看到十几条同名记录,分不清哪条是主线、哪条能还原什么。
// 修 = 副本形状的记录标题带 (副本 recovery-<hex>) 标识(用户裁决的选项 B):
// 主线是唯一不带后缀的记录。还原/清除保持单桶语义,还原哪条=还原哪个桶。
// 刻意不动 RecoveryCopy:它的契约是「已证明未被续写且被母本覆盖,可安全清
// 理」(22b334f22;TestTrashMarksLegacyRecoverySessionsAsRecovered 钉死)——
// 副本桶内没有母本,coverage 证明必败,形状推断不够格。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/store"
)

const tl1429dFamilyTitle = "TL1429d 族"

func writeTl1429dBucketTranscript(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"tl1429d turn"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write bucketed transcript: %v", err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt:  time.Now().Add(-time.Minute),
		UpdatedAt:  time.Now(),
		TopicTitle: tl1429dFamilyTitle,
	}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
}

// stageTl1429dTrashBucket 布一个完整 trash 桶(transcript+meta),返回桶内
// transcript 路径——即 ListTrashedSessions 出条、Restore/Purge 消费的 path。
func stageTl1429dTrashBucket(t *testing.T, dir, key string) string {
	t.Helper()
	itemDir := filepath.Join(dir, sessionTrashDir, key)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatalf("mkdir trash bucket: %v", err)
	}
	transcriptPath := filepath.Join(itemDir, key)
	writeTl1429dBucketTranscript(t, transcriptPath)
	meta, err := json.MarshalIndent(trashedSessionMeta{Key: key, DeletedAt: time.Now().UnixMilli()}, "", "  ")
	if err != nil {
		t.Fatalf("marshal trash meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, sessionTrashMetaFile), meta, 0o644); err != nil {
		t.Fatalf("write trash meta: %v", err)
	}
	return transcriptPath
}

// 主钉:同族(主线桶+副本桶)在回收站里逐桶出条,唯一不带副本标识的是主线;
// 每条副本凭 recovery-<hex> 互异可辨;还原主线不动副本桶,逐桶清除对得上;
// 记录数=桶数(计数属性:逐条记录与徽章/清空链的输入一致)。
func TestTrashedRecoveryFamilyCopiesAreIdentifiedPerBucket(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	mainKey := "20261024-011346.045066400-tl1429d-family.jsonl"
	mainBucketPath := stageTl1429dTrashBucket(t, dir, mainKey)
	copyHexes := []string{"29384636d8d4c0b2", "c4e94f82676c9547", "fd8f06a5a1ecf671"}
	copyBucketPaths := map[string]string{}
	mainStem := strings.TrimSuffix(mainKey, store.SessionTranscriptSuffix)
	for _, hex := range copyHexes {
		copyKey := mainStem + "-recovery-" + hex + store.SessionTranscriptSuffix
		copyBucketPaths[hex] = stageTl1429dTrashBucket(t, dir, copyKey)
	}

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })
	listed := app.ListTrashedSessions()
	if len(listed) != 1+len(copyHexes) {
		t.Fatalf("ListTrashedSessions = %d records, want 1 mainline + %d copies (计数=桶数)", len(listed), len(copyHexes))
	}

	var mainline *SessionMeta
	copies := 0
	for i := range listed {
		record := &listed[i]
		if filepath.Base(record.Path) == mainKey {
			mainline = record
			if strings.Contains(record.Title, "(副本 recovery-") {
				t.Fatalf("mainline record title %q carries a copy tag — 主线被误标", record.Title)
			}
			continue
		}
		copies++
		if !strings.Contains(record.Title, "(副本 recovery-") {
			t.Fatalf("copy record title %q carries no copy tag — 同名淹没回潮", record.Title)
		}
		if !strings.Contains(record.Title, tl1429dFamilyTitle) {
			t.Fatalf("copy record title %q lost the family title %q", record.Title, tl1429dFamilyTitle)
		}
		// 前端显示优先 topicTitle(title 兜底),副本标识必须两处都落。
		if record.TopicTitle != record.Title {
			t.Fatalf("copy record topicTitle %q != tagged title %q — 标识会被显示优先级吞掉", record.TopicTitle, record.Title)
		}
		// RecoveryCopy 契约不变:无 coverage 证明不得标记(22b334f22)。
		if record.RecoveryCopy {
			t.Fatalf("copy record %s marked recoveryCopy without coverage proof — 安全清理契约被形状推断稀释", filepath.Base(record.Path))
		}
	}
	if mainline == nil {
		t.Fatal("mainline record missing from the trash list")
	}
	if copies != len(copyHexes) {
		t.Fatalf("tagged copy records = %d, want %d", copies, len(copyHexes))
	}
	// 每个副本的 hex 标识互异且对得上自己的桶。
	for _, hex := range copyHexes {
		tag := "(副本 recovery-" + hex[:8] + ")"
		found := false
		for i := range listed {
			if strings.Contains(listed[i].Title, tag) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no copy record tagged %s — 副本与桶名对不上", tag)
		}
	}

	// 还原主线:主线桶整体回位,副本桶原样留站(还原哪条=还原哪个桶)。
	if err := app.RestoreSession(mainline.Path); err != nil {
		t.Fatalf("RestoreSession(mainline) = %v, want success", err)
	}
	if _, err := os.Stat(filepath.Join(dir, mainKey)); err != nil {
		t.Fatalf("mainline transcript not restored to the session dir: %v", err)
	}
	if _, err := os.Stat(mainBucketPath); !os.IsNotExist(err) {
		t.Fatalf("mainline bucket survived the restore: %v", err)
	}
	after := app.ListTrashedSessions()
	if len(after) != len(copyHexes) {
		t.Fatalf("post-restore trash = %d records, want %d copies", len(after), len(copyHexes))
	}
	for i := range after {
		if !strings.Contains(after[i].Title, "(副本 recovery-") {
			t.Fatalf("post-restore record %s lost its copy tag", filepath.Base(after[i].Path))
		}
	}

	// 逐桶清除一个副本:对得上,其余副本不受影响。
	if err := app.PurgeTrashedSession(copyBucketPaths[copyHexes[0]]); err != nil {
		t.Fatalf("PurgeTrashedSession(copy) = %v, want success", err)
	}
	if _, err := os.Stat(copyBucketPaths[copyHexes[0]]); !os.IsNotExist(err) {
		t.Fatalf("purged copy bucket survived: %v", err)
	}
	final := app.ListTrashedSessions()
	if len(final) != len(copyHexes)-1 {
		t.Fatalf("post-purge trash = %d records, want %d", len(final), len(copyHexes)-1)
	}
}

// meta-less 兜底(tl1429b)不回归:无 meta 的 unique 桶仍可见可清;副本形状
// 的 meta-less 桶同样拿到标识(标识只看文件名形状,不依赖 meta)。
func TestMetaLessRecoveryShapedBucketStillIdentified(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	key := "20261024-011346-tl1429d-metaless-recovery-0123456789abcdef.jsonl"
	itemDir := filepath.Join(dir, sessionTrashDir, key)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatalf("mkdir trash bucket: %v", err)
	}
	// 故意不写 .trash-meta.json——「move 成功、meta 写入前崩溃」的桶形态。
	transcriptPath := filepath.Join(itemDir, key)
	writeTl1429dBucketTranscript(t, transcriptPath)

	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })
	listed := app.ListTrashedSessions()
	if len(listed) != 1 {
		t.Fatalf("ListTrashedSessions = %d records, want the meta-less bucket listed", len(listed))
	}
	if !strings.Contains(listed[0].Title, "(副本 recovery-01234567)") {
		t.Fatalf("meta-less bucket title %q carries no copy tag", listed[0].Title)
	}
	if err := app.PurgeTrashedSession(listed[0].Path); err != nil {
		t.Fatalf("PurgeTrashedSession on meta-less bucket = %v, want success", err)
	}
	if _, err := os.Stat(transcriptPath); !os.IsNotExist(err) {
		t.Fatalf("meta-less bucket survived the purge: %v", err)
	}
}
