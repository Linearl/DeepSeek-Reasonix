package main

// tl1429b-2 回归钉:.trash 白名单。包 79 装机现场:回收站第二条记录(并发
// trash 流的产物,commit A 已断源)清空时报「trashed session path outside
// known session dirs」——严格结构校验对一切非标准桶形判死,而清空/恢复本就
// 是 .trash 内的销毁/取回语义。修复:validateTrashedSessionPath 在严格校验
// 失败后落到统一兜底——目标可证实位于该 dir 的 .trash 桶内(transcript 名 +
// 文件在 + 无逃逸)即放行,trashedSessionDir 全拒时 slog 带严格错误归因。
// 钉三面:桶内非标形放行(清空+恢复)、桶外/逃逸/root 直放仍拒。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"time"
)

// writeStrictlyInvalidTrashRecord 在 dir 的 .trash 下落一条严格校验必拒的
// 记录:3 段 rel(.trash/<bucket>/<nested>/<key>),meta 缺失,transcript 在
// 嵌套层。并发 trash 流竞态留下的正是这类非标桶。
func writeStrictlyInvalidTrashRecord(t *testing.T, dir string) (transcriptPath string) {
	t.Helper()
	transcriptPath = filepath.Join(sessionTrashPath(dir), "nested-form.jsonl", "nested", "nested-form.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(`{"role":"user","content":"stuck record"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return transcriptPath
}

func TestTrashedSessionDirAcceptsTrashContainedNonStandardBucket(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	recordPath := writeStrictlyInvalidTrashRecord(t, dir)
	app := NewApp()

	gotDir, err := app.trashedSessionDir(recordPath)
	if err != nil {
		t.Fatalf("whitelist fallback rejected a provably trash-contained record: %v", err)
	}
	if gotDir != dir {
		t.Fatalf("resolved dir = %s, want %s", gotDir, dir)
	}

	// 清空(销毁语义)必须走通:记录消失。
	if err := app.purgeTrashedSession(recordPath, false); err != nil {
		t.Fatalf("purge through the whitelist fallback: %v", err)
	}
	if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
		t.Fatalf("record survived the purge (err=%v)", err)
	}
}

func TestTrashedSessionDirStillRejectsOutsideAndEscapePaths(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessionTrashPath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	// 活动会话目录里的 transcript 名文件(.trash 之外):不放行。
	live := filepath.Join(dir, "sneaky.jsonl")
	if err := os.WriteFile(live, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.trashedSessionDir(live); err == nil {
		t.Fatal("a .trash-external transcript-named path must stay rejected")
	} else if !strings.Contains(err.Error(), "outside known session dirs") {
		t.Fatalf("unexpected refusal copy: %v", err)
	}

	// 逃逸路径(..):硬拒,白名单永不接。
	escape := filepath.Join(sessionTrashPath(dir), "..", "..", "escaped.jsonl")
	if _, err := app.trashedSessionDir(escape); err == nil {
		t.Fatal("an escaping path must stay rejected")
	}

	// 直接落在 .trash 根(无桶):不放行。
	loose := filepath.Join(sessionTrashPath(dir), "loose.jsonl")
	if err := os.WriteFile(loose, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.trashedSessionDir(loose); err == nil {
		t.Fatal("a transcript sitting directly in the trash root must stay rejected")
	}
}

func TestRestoreSessionWorksThroughWhitelistFallback(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	recordPath := writeStrictlyInvalidTrashRecord(t, dir)
	app := NewApp()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })

	// 恢复(取回语义)与清空同走 trashedSessionDir+validate:兜底放行后走通。
	if err := app.restoreSession(recordPath); err != nil {
		t.Fatalf("restore through the whitelist fallback: %v", err)
	}
	if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
		t.Fatalf("trashed copy survived the restore (err=%v)", err)
	}
}
