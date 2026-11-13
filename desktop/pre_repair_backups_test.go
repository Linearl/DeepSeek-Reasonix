package main

// tl1572: 改写前备份枚举的后端单测。
//
// 验收看四个面：①存量备份全量可发现（含宿主已删的孤儿备份）；②读数
// （transcript/owner/label/bytes/files/stamp）各就各位；③非备份目录与备份
// 树内部不再下探（一棵树一个单元）；④新备份排前。只读契约（零写入零改
// 名）由实现形态保证——本测试断言枚举前后目录树逐字节不变。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePreRepairTree(t *testing.T, dir, stem, stamp string, transcriptBytes int) string {
	t.Helper()
	backup := filepath.Join(dir, stem+".pre-repair-"+stamp)
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if transcriptBytes > 0 {
		if err := os.WriteFile(filepath.Join(backup, stem+".jsonl"), make([]byte, transcriptBytes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// meta sidecar（备份白名单成员）——展示名的标题来源。
	meta := `{"topic_title":"备份标题` + stamp + `"}`
	if err := os.WriteFile(filepath.Join(backup, stem+".jsonl.meta"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return backup
}

func TestPreRepairBackupsEnumerate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	sessions := filepath.Join(root, "proj", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	// 宿主会话 + 两次修复的两份备份（新的后落盘）。
	if err := os.WriteFile(filepath.Join(sessions, "s1.jsonl"), []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := writePreRepairTree(t, sessions, "s1", "20261105T020006", 100)
	time.Sleep(10 * time.Millisecond)
	newest := writePreRepairTree(t, sessions, "s1", "20261107T030000", 200)
	// 孤儿备份：宿主已删，仅存备份树。
	orphanDir := filepath.Join(root, "proj2", "sessions")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePreRepairTree(t, orphanDir, "gone", "20261101T010000", 50)
	// 干扰项：缺「.」连接（SSOT 语法=store.SessionTrashSidecars 的 glob
	// `stem+".pre-repair-"+*`）与完全无关的普通目录。
	if err := os.MkdirAll(filepath.Join(sessions, "s1.prerepair-20261105T020006"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sessions, "plain-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	views := preRepairBackupViewsIn([]string{root})
	if len(views) != 3 {
		t.Fatalf("views = %d (%+v), want 3", len(views), views)
	}
	// 新的在前。
	if views[0].Dir != newest || views[1].Dir != old {
		t.Fatalf("order must be newest first, got %+v", views)
	}
	got := views[0]
	if got.Transcript != filepath.Join(newest, "s1.jsonl") {
		t.Fatalf("transcript = %q", got.Transcript)
	}
	if got.OwnerPath != filepath.Join(sessions, "s1.jsonl") {
		t.Fatalf("owner = %q", got.OwnerPath)
	}
	if got.Label != "备份标题20261107T030000" {
		t.Fatalf("label = %q, want backup-side meta title", got.Label)
	}
	if got.Stamp != "20261107T030000" {
		t.Fatalf("stamp = %q", got.Stamp)
	}
	// s1.jsonl(200B) + s1.jsonl.meta（字节数按实际写入计）。
	if got.Files != 2 {
		t.Fatalf("files = %d, want 2", got.Files)
	}
	if got.Bytes <= 200 {
		t.Fatalf("bytes = %d, must exceed the transcript alone", got.Bytes)
	}
	if got.ModifiedAt == "" {
		t.Fatal("modifiedAt must be set")
	}
	// 孤儿备份：owner 为空但照常列出（宿主仅存的完整副本）。
	orphan := views[2]
	if orphan.OwnerPath != "" {
		t.Fatalf("orphan owner = %q, want empty", orphan.OwnerPath)
	}
	if orphan.Transcript != filepath.Join(orphan.Dir, "gone.jsonl") {
		t.Fatalf("orphan transcript = %q", orphan.Transcript)
	}
}

// TestPreRepairBackupsReadOnly 枚举是纯读：前后目录树逐字节一致。
func TestPreRepairBackupsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	sessions := filepath.Join(root, "proj", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	backup := writePreRepairTree(t, sessions, "s1", "20261105T020006", 100)

	snapshot := func() map[string]int64 {
		out := map[string]int64{}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				t.Fatalf("stat %s: %v", path, statErr)
			}
			out[path] = info.Size()
			return nil
		})
		return out
	}
	before := snapshot()
	_ = preRepairBackupViewsIn([]string{root})
	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("tree changed: before=%v after=%v", before, after)
	}
	for path, size := range before {
		if after[path] != size {
			t.Fatalf("file %s changed size %d -> %d", path, size, after[path])
		}
	}
	_ = backup
}

// TestPreRepairBackupsNoDescendIntoBackupTree 一棵备份树一个单元：树内部的
// 再深目录（异常嵌套）不产生第二条读数。
func TestPreRepairBackupsNoDescendIntoBackupTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	sessions := filepath.Join(root, "proj", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	backup := writePreRepairTree(t, sessions, "s1", "20261105T020006", 100)
	// 异常形态：备份树内部又套一层备份目录（不该被单独计数）。
	nested := filepath.Join(backup, "s1.pre-repair-20261105T020007")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	views := preRepairBackupViewsIn([]string{root})
	if len(views) != 1 {
		t.Fatalf("views = %d (%+v), want 1 (the outer tree only)", len(views), views)
	}
	if views[0].Dir != backup {
		t.Fatalf("dir = %q, want the outer tree", views[0].Dir)
	}
}

// TestPreRepairBackupsMissingTranscript 备份目录缺主线转录（被清空/失败形态）
// 时照常列出，transcript 为空，label 回退 stem。
func TestPreRepairBackupsMissingTranscript(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	sessions := filepath.Join(root, "proj", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(sessions, "s9.pre-repair-20261105T020006")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "s9.jsonl.meta"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	views := preRepairBackupViewsIn([]string{root})
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	if views[0].Transcript != "" {
		t.Fatalf("transcript = %q, want empty", views[0].Transcript)
	}
	if views[0].Label != "s9" {
		t.Fatalf("label = %q, want stem fallback", views[0].Label)
	}
}
