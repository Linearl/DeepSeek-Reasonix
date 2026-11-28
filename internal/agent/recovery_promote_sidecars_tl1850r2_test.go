package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1850r2 回归修复钉：guardian.rewritelog.jsonl 的入队源就是 guardian 自己
// ——internal/guardian/guardian.go 的 normalizeAlternation 在合并连续用户
// 消息后 gs.sess.Rewrite(out, "guardian_merge")，经 Session.Rewrite →
// queueRewriteAttributionLocked → flushRewriteAttribution（persisted.path
// 即 guardian 转录路径）落 <stem>.guardian.rewritelog.jsonl。磁盘实锤
// （2026-11-24 活跃写入，reason=guardian_merge）。tl1850 收窄时枚举了四个
// 入队源漏数了这个第五源，误判「生产零写入」，白名单键与派生清单键同删
// ——转正链「先归档后换位」对它零归档（backupSessionFiles 白名单 skip，
// 只留一条 Info），副本兜底清扫改嫁 = 无归档覆盖销毁。
//
// 红基线（2026-11-28 修复前实测）：主线 guardian rewritelog 不进白名单
// 归档目录，换位后副本件覆盖主名、主线原件字节销毁。

// tl1850r2WriteGuardianRewritelog 走真实 guardian_merge 写链（非静态栽种）：
// 会话持久基线武装到 guardian 转录路径，Rewrite("guardian_merge") 完成-时
// 落盘，返回侧车路径与写入字节（供归档字节级对读）。
func tl1850r2WriteGuardianRewritelog(t *testing.T, guardianPath, tag string) (string, string) {
	t.Helper()
	gs := NewSession("system")
	gs.Add(provider.Message{Role: provider.RoleUser, Content: tag + " turn one"})
	gs.Add(provider.Message{Role: provider.RoleAssistant, Content: tag + " turn two"})
	if err := gs.SaveSnapshot(guardianPath); err != nil {
		t.Fatalf("SaveSnapshot(guardian): %v", err)
	}
	msgs := append([]provider.Message(nil), gs.Snapshot()[:1]...)
	gs.Rewrite(msgs, "guardian_merge")
	logPath := store.SessionRewriteAttributionLog(guardianPath)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("guardian_merge chain did not write the rewritelog sidecar: %v", err)
	}
	if !strings.Contains(string(data), "guardian_merge") {
		t.Fatalf("rewritelog record lost the guardian_merge reason: %q", string(data))
	}
	return logPath, string(data)
}

// TestPromoteArchivesMainGuardianRewritelogBeforeSwap 主钉：主线 rewritelog
// 由真实 guardian_merge 写链落盘、副本 rewritelog 栽种异字节 → 转正 →
// 主线原件字节必须整份躺在白名单归档里（归档先于换位），主名由副本件接管
// （身份随行），副本死 stem 零残留。
func TestPromoteArchivesMainGuardianRewritelogBeforeSwap(t *testing.T) {
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
	guardianPath := store.SessionGuardianTranscript(mainPath)

	// 主线：真实 guardian_merge 写链（tl1850 漏数的第五入队源）。
	mainLog, mainBytes := tl1850r2WriteGuardianRewritelog(t, guardianPath, "main-line")
	// 写链落盘形态必须与白名单键拼写逐字节同形（rel = guardian + 后缀常量）。
	if want := mainStem + ".guardian" + store.SessionRewriteAttributionLogSuffix; filepath.Base(mainLog) != want {
		t.Fatalf("guardian rewritelog shape %q drifted off the allowlist key spelling %q", filepath.Base(mainLog), want)
	}
	// 副本：栽种异字节身份（换位接管面）。
	copyBody := "{\"reason\":\"guardian_merge\",\"unique\":\"copy-line fold\"}\n"
	copyLog := filepath.Join(dir, copyStem+".guardian"+store.SessionRewriteAttributionLogSuffix)
	if err := os.WriteFile(copyLog, []byte(copyBody), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := PromoteRecoveryBranchToMain(copyPath, mainPath); err != nil {
		t.Fatalf("PromoteRecoveryBranchToMain: %v", err)
	}

	// ① 归档存在：主线原件字节整份在白名单归档里（tl1850 回归即在此断红：
	// 白名单缺键 → backupSessionFiles skip → 归档目录无此件）。
	backupDir := tl1841FindBackupDir(t, dir, mainStem)
	archived, err := os.ReadFile(filepath.Join(backupDir, filepath.Base(mainLog)))
	if err != nil {
		t.Fatalf("main guardian rewritelog missing from the pre-promotion archive: %v", err)
	}
	if string(archived) != mainBytes {
		t.Fatalf("archived guardian rewritelog bytes drifted:\n got: %q\nwant: %q", string(archived), mainBytes)
	}

	// ② 主名 rewritelog 是副本件接管（身份换位语义保留）。
	ontoMain, err := os.ReadFile(filepath.Join(dir, filepath.Base(mainLog)))
	if err != nil {
		t.Fatalf("guardian rewritelog missing from the main identity after promotion: %v", err)
	}
	if string(ontoMain) != copyBody {
		t.Fatalf("main-name guardian rewritelog = %q, want the copy's bytes %q", string(ontoMain), copyBody)
	}

	// ③ 副本死 stem 零残留（rewritelog 走静态换位清单，不由兜底清扫改嫁）。
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
	if _, err := os.Lstat(filepath.Join(dir, filepath.Base(mainLog)+promoteStagingSuffix)); !os.IsNotExist(err) {
		t.Fatalf("promote-staging residue survived the transaction: %v", err)
	}
}

// TestPromoteKeepsMainGuardianRewritelogWhenCopyHasNone 案 B：副本没有
// rewritelog 时主线原件原地不动、字节不变（换位清单「副本不拥有即原地
// 保留」契约对 guardian rewritelog 同样成立——清单登记不得把它变成可删面）。
func TestPromoteKeepsMainGuardianRewritelogWhenCopyHasNone(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "solo.jsonl")
	if err := os.WriteFile(mainPath, []byte("{\"role\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(dir, "solo-recovery-fedcba9876543210.jsonl")
	if err := os.WriteFile(copyPath, []byte("{\"role\":\"user\"}\n{\"role\":\"assistant\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	guardianPath := store.SessionGuardianTranscript(mainPath)
	mainLog, mainBytes := tl1850r2WriteGuardianRewritelog(t, guardianPath, "solo")

	if _, err := PromoteRecoveryBranchToMain(copyPath, mainPath); err != nil {
		t.Fatalf("PromoteRecoveryBranchToMain: %v", err)
	}

	got, err := os.ReadFile(mainLog)
	if err != nil {
		t.Fatalf("main guardian rewritelog vanished although the copy owned none: %v", err)
	}
	if string(got) != mainBytes {
		t.Fatalf("main guardian rewritelog bytes drifted: got %q want %q", string(got), mainBytes)
	}
}
