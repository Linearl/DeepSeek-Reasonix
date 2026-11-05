package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
)

// tl1414：副本行「删除」确认后静默无反应（20261105 用户装机实测，包 75）。
//
// 根因是同一个目录以两种大小写拼写到达比较点：
//  - 面板扫描面（recoveryScanDirectories → recoveryCopiesByMain）用磁盘上
//    的真实目录名拼 copy.path（本机 slug 目录是 `C--Users-…` 大写形态）；
//  - 删除门（sessionDirForPath → knownSessionDirs）解析出的目录走
//    WorkspaceSlug 的折叠形态（internal/config/paths.go 对 Windows 全串
//    ToLower ⇒ `c--users-…` 小写形态）。
// NTFS 对两种拼写都能打开（filepath.Rel 也按同词折叠比较，校验门因此放
// 行），但 TrashRecoveryBranchForced 的 containment 检查
// （filepath.Dir(path) != parentDir）是字节级比较——大小写不同即拒绝，
// 错误原样回到前端，只剩面板底部一行小字结果，用户读到的是「无任何反应」。
//
// 本 harness 用「目录部分大小写翻转」的路径驱动删除入口（与用户现场同一
// 形态），钉两条路径：
//  ① 单删分支：目录拼写不一致时仍须归档成功（文件离开活目录、可恢复）；
//  ② 整对话删除：副本枚举按 sessionDirForPath 的目录拼写建 key、却用扫描
//     拼写的 sessionPath 查（byMain[sessionPath] miss）——副本必须被逐条
//     归档，而不是静默漏删（CopiesArchived 恰好 0、副本文件原地不动）。

// flipDirCase flips the case of every letter in a directory spelling, the
// way two subsystems of one binary produce for the same directory.
func flipDirCase(dir string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		if r >= 'A' && r <= 'Z' {
			return r - 'A' + 'a'
		}
		return r
	}, dir)
}

func TestDeleteRecoveryBranchSurvivesDirCaseSpellingMismatch(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath, copyPath := writeRecoveryCopyFixture(t, dir, "sess-tl1414-casing.jsonl", "sess-tl1414-casing-recovery-0123456789abcdef.jsonl")
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
	})

	// The scan face hands the panel the on-disk spelling; the delete gate
	// resolves the same directory through the slug-folded spelling. Any
	// byte-compare between the two spellings refuses what NTFS itself opens.
	altDir := flipDirCase(filepath.Dir(copyPath))
	oddCopy := filepath.Join(altDir, filepath.Base(copyPath))
	if _, err := os.Stat(oddCopy); err != nil {
		t.Fatalf("the flipped-case spelling must resolve on this filesystem: %v", err)
	}

	app := newBranchDeleteApp(t)
	if err := app.DeleteRecoveryBranch(oddCopy); err != nil {
		t.Fatalf("branch delete must not refuse a directory case-spelling difference (tl1414): %v", err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("archived branch must leave the live directory, stat err=%v", err)
	}
	trashHits := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(p) == filepath.Base(copyPath) {
			trashHits++
		}
		return nil
	})
	if trashHits == 0 {
		t.Fatal("the discarded branch must remain recoverable under the session directory")
	}
}

func TestDeleteConversationWithCopiesArchivesCopiesUnderCaseMismatch(t *testing.T) {
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath, copyPath := writeRecoveryCopyFixture(t, dir, "sess-tl1414-conv-casing.jsonl", "sess-tl1414-conv-casing-recovery-fedcba9876543210.jsonl")
	t.Cleanup(func() {
		_ = os.Remove(mainPath)
		_ = os.Remove(copyPath)
	})

	altDir := flipDirCase(filepath.Dir(mainPath))
	oddMain := filepath.Join(altDir, filepath.Base(mainPath))
	if _, err := os.Stat(oddMain); err != nil {
		t.Fatalf("the flipped-case spelling must resolve on this filesystem: %v", err)
	}

	app := newBranchDeleteApp(t)
	report, err := app.DeleteConversationWithCopies(oddMain)
	if err != nil {
		t.Fatalf("whole-conversation delete must not refuse a directory case-spelling difference (tl1414): %v", err)
	}
	if report.CopiesArchived != 1 {
		t.Fatalf("the copy beside the main must be archived exactly once, got CopiesArchived=%d", report.CopiesArchived)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("the copy must leave the live directory with the conversation, stat err=%v", err)
	}
}
