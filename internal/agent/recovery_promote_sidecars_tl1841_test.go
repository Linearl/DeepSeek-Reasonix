package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tl1841 ①（d1 扫描 G1+G6）：takeover 保全日志 <stem>.conflict-preserved.jsonl
// 必须登记进会话工件清单（store.SessionSidecarFiles ⇒ 静态换位清单同一行生效）
// 与归档白名单——副本转正（PromoteRecoveryBranchToMain）时主线原件先走白名单
// 归档，再进入两步换位；「覆盖不再是销毁」（tl1748 核心承诺）在换位链兑现。
//
// 红测试先行（2026-11-27 实测基线）：修复前主线 preserve 不在换位清单也不在
// 白名单——副本 stem 兜底清扫把副本同名件 os.Rename 到主名（Windows 语义=替换），
// 主线保全日志被无归档销毁。若被弃侧用户消息的唯一副本只在 preserve 文件里
// （relay 有 10 条/16KB 截断上限），这部分消息从此不可恢复。

// tl1841PlantPreserve 种一份 preserve 文件并返回其全文（断言字节级回读）。
func tl1841PlantPreserve(t *testing.T, dir, stem, body string) string {
	t.Helper()
	full := filepath.Join(dir, stem+".conflict-preserved.jsonl")
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return full
}

// tl1841FindBackupDir 定位 main stem 的白名单归档目录（转正先归档的唯一出口）。
func tl1841FindBackupDir(t *testing.T, dir, mainStem string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), mainStem+".pre-repair-") {
			return filepath.Join(dir, entry.Name())
		}
	}
	t.Fatalf("no allowlist backup dir found for main stem %q (promotion must archive before swapping)", mainStem)
	return ""
}

// TestPromoteArchivesMainConflictPreservedBeforeSwap tl1841① 主钉（案 A）：
// 主线与副本各有一份 preserve（内容不同）→ 转正 → 主线原件字节必须整份躺在
// 白名单归档里（归档先于换位），主名 preserve 由副本件接管（身份随行），
// 副本死 stem 零残留。
func TestPromoteArchivesMainConflictPreservedBeforeSwap(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "topic.jsonl")
	if err := os.WriteFile(mainPath, []byte("{\"role\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "topic-recovery-0123456789abcdef.jsonl")
	if err := os.WriteFile(copyPath, []byte("{\"role\":\"user\"}\n{\"role\":\"assistant\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	mainStem := strings.TrimSuffix(filepath.Base(mainPath), ".jsonl")
	copyStem := strings.TrimSuffix(filepath.Base(copyPath), ".jsonl")
	mainPreservedBody := "{\"unique\":\"main-line abandoned-side user message\",\"turn\":7}\n"
	copyPreservedBody := "{\"unique\":\"copy-line preserved turn\",\"turn\":1}\n"
	tl1841PlantPreserve(t, dir, mainStem, mainPreservedBody)
	tl1841PlantPreserve(t, dir, copyStem, copyPreservedBody)

	if _, err := PromoteRecoveryBranchToMain(copyPath, mainPath); err != nil {
		t.Fatalf("PromoteRecoveryBranchToMain: %v", err)
	}

	// ① 归档存在：主线原件字节整份在白名单归档里（不是 staging 残名，不是被截断）。
	backupDir := tl1841FindBackupDir(t, dir, mainStem)
	archived, err := os.ReadFile(filepath.Join(backupDir, mainStem+".conflict-preserved.jsonl"))
	if err != nil {
		t.Fatalf("main conflict-preserved log missing from the pre-promotion archive: %v", err)
	}
	if string(archived) != mainPreservedBody {
		t.Fatalf("archived preserve bytes drifted: got %q want %q", string(archived), mainPreservedBody)
	}

	// ② 主名 preserve 存在且是副本件接管（身份换位语义保留）。
	ontoMain, err := os.ReadFile(filepath.Join(dir, mainStem+".conflict-preserved.jsonl"))
	if err != nil {
		t.Fatalf("conflict-preserved log missing from the main identity after promotion: %v", err)
	}
	if string(ontoMain) != copyPreservedBody {
		t.Fatalf("main-name preserve = %q, want the copy's bytes %q", string(ontoMain), copyPreservedBody)
	}

	// ③ 副本死 stem 零残留（preserve 走静态换位清单，不由兜底清扫改嫁）；
	// 运行时句柄（save/lease 锁、cleanup-pending）由各自机制处置，按
	// tl1442ext 同一豁免集排除。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	handles := extAgentHandleNames(copyPath)
	for name := range extAgentHandleNames(mainPath) {
		handles[name] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		if handles[name] {
			continue
		}
		if strings.HasPrefix(name, copyStem+".") {
			t.Fatalf("copy stem residue after promotion: %s", name)
		}
	}
	// staging 中转名同样零残留。
	if _, err := os.Lstat(filepath.Join(dir, mainStem+".conflict-preserved.jsonl"+promoteStagingSuffix)); !os.IsNotExist(err) {
		t.Fatalf("promote-staging residue survived the transaction: %v", err)
	}
}

// TestPromoteKeepsMainPreservedWhenCopyHasNone tl1841① 案 B：副本没有 preserve
// 时主线原件原地不动、字节不变（换位清单「副本不拥有即原地保留」契约对
// preserve 同样成立——清单登记不得把它变成可删面）。
func TestPromoteKeepsMainPreservedWhenCopyHasNone(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "solo.jsonl")
	if err := os.WriteFile(mainPath, []byte("{\"role\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "solo-recovery-fedcba9876543210.jsonl")
	if err := os.WriteFile(copyPath, []byte("{\"role\":\"user\"}\n{\"role\":\"assistant\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	mainStem := strings.TrimSuffix(filepath.Base(mainPath), ".jsonl")
	preservePath := tl1841PlantPreserve(t, dir, mainStem, "{\"unique\":\"solo preserve\"}\n")

	if _, err := PromoteRecoveryBranchToMain(copyPath, mainPath); err != nil {
		t.Fatalf("PromoteRecoveryBranchToMain: %v", err)
	}

	got, err := os.ReadFile(preservePath)
	if err != nil {
		t.Fatalf("main preserve vanished although the copy owned none: %v", err)
	}
	if string(got) != "{\"unique\":\"solo preserve\"}\n" {
		t.Fatalf("main preserve bytes changed by promotion: %q", string(got))
	}
}
