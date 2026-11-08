package main

// tl1429d2 回归钉:回收站「同名多条」返修面(首修 tl1429d 的标题后缀标识被
// 用户打回——打错靶:用户要的是渲染层去重,不是标识)。
// 现场(包 81 装机实测 2026-11-07 10:17 desktop.log):一次归档级联把主线桶
// +17 个 recovery 副本桶搬进 .trash,ListTrashedSessions 逐桶出条且标题同源
// ——用户看到十几条同名记录占独立多行。
// 修 = 数据层补只读同族归组字段(TrashFamilyKey/TrashFamilyCopy)+前端渲染
// 层按族合并(主线一行为主体,副本进计数徽标+展开明细)。数据层约束:桶仍
// 逐条出记录(桶=还原/清除的单桶语义,还原哪条=还原哪个桶),字段只读,
// 不碰 RecoveryCopy 的 coverage 契约(22b334f22,
// TestTrashMarksLegacyRecoverySessionsAsRecovered 钉死),不碰还原/清除路径。

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

const tl1429dMainKey = "20261024-011346.045066400-tl1429d-family.jsonl"

// 主钉:同族(主线桶+副本桶)在回收站里仍逐桶出记录(数据层计数=桶数,
// tl1442-ext 的多桶现场不丢桶);主线与每条副本带同族归组键=主线 stem,
// 副本形状单独落 TrashFamilyCopy;标题不再带 (副本 recovery-) 后缀(打回的
// 旧修法不得回潮);RecoveryCopy 契约不变。还原主线只动主线桶、副本原样
// 留站;逐桶清除对得上桶。
func TestTrashedRecoveryFamilyCopiesCarryGroupingFieldsPerBucket(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	mainBucketPath := stageTl1429dTrashBucket(t, dir, tl1429dMainKey)
	copyHexes := []string{"29384636d8d4c0b2", "c4e94f82676c9547", "fd8f06a5a1ecf671"}
	copyBucketPaths := map[string]string{}
	mainStem := strings.TrimSuffix(tl1429dMainKey, store.SessionTranscriptSuffix)
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
		if record.TrashFamilyKey != mainStem {
			t.Fatalf("record %s family key %q != mainline stem %q — 同族归组键失配", filepath.Base(record.Path), record.TrashFamilyKey, mainStem)
		}
		if filepath.Base(record.Path) == tl1429dMainKey {
			mainline = record
			if record.TrashFamilyCopy {
				t.Fatalf("mainline record flagged TrashFamilyCopy — 主线被误标为副本")
			}
		} else {
			copies++
			if !record.TrashFamilyCopy {
				t.Fatalf("copy record %s carries no TrashFamilyCopy flag — 渲染层无从识别副本", filepath.Base(record.Path))
			}
		}
		// 被打回的标题标识不得回潮:主线与副本的标题都保持原样。
		if strings.Contains(record.Title, "(副本 recovery-") || strings.Contains(record.TopicTitle, "(副本 recovery-") {
			t.Fatalf("record %s title %q carries the rejected copy tag — tl1429d 标识回潮", filepath.Base(record.Path), record.Title)
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
		t.Fatalf("copy-flagged records = %d, want %d", copies, len(copyHexes))
	}
	if mainline.Title != "" && !strings.Contains(mainline.Title, tl1429dFamilyTitle) {
		t.Fatalf("mainline title %q lost the family title %q", mainline.Title, tl1429dFamilyTitle)
	}

	// 还原主线:主线桶整体回位,副本桶原样留站(还原哪条=还原哪个桶),
	// 副本记录的归组字段不受还原影响。
	if err := app.RestoreSession(mainline.Path); err != nil {
		t.Fatalf("RestoreSession(mainline) = %v, want success", err)
	}
	if _, err := os.Stat(filepath.Join(dir, tl1429dMainKey)); err != nil {
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
		if !after[i].TrashFamilyCopy {
			t.Fatalf("post-restore record %s lost its TrashFamilyCopy flag", filepath.Base(after[i].Path))
		}
		if after[i].TrashFamilyKey != mainStem {
			t.Fatalf("post-restore record %s family key %q drifted", filepath.Base(after[i].Path), after[i].TrashFamilyKey)
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
// 的 meta-less 桶同样拿到归组字段(归组只看文件名形状,不依赖 meta)。
func TestMetaLessRecoveryShapedBucketStillGrouped(t *testing.T) {
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
	if !listed[0].TrashFamilyCopy {
		t.Fatalf("meta-less copy-shaped bucket carries no TrashFamilyCopy flag")
	}
	if want := "20261024-011346-tl1429d-metaless"; listed[0].TrashFamilyKey != want {
		t.Fatalf("meta-less bucket family key %q, want %q", listed[0].TrashFamilyKey, want)
	}
	if err := app.PurgeTrashedSession(listed[0].Path); err != nil {
		t.Fatalf("PurgeTrashedSession on meta-less bucket = %v, want success", err)
	}
	if _, err := os.Stat(transcriptPath); !os.IsNotExist(err) {
		t.Fatalf("meta-less bucket survived the purge: %v", err)
	}
}
