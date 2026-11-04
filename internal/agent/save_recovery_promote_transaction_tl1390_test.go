package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1390 验收：转正搬运的事务性。
//
// 2026-11-04 事故：转正把副本侧车逐个 rename 到主名，turns.jsonl 被
// 活跃 turn 的账本 O_APPEND 句柄占住而失败时，排在前面的 meta/event log
// 已经搬走——半搬现场让副本下一次保存 stat 到事件日志缺失（tl1313
// 「event log vanished」WARN 的伴生来源），且此后每轮转正只剩残件可搬、
// 永久 declined。本文件钉住三面：
//  1. 成功路径：主侧车已存在时两步换位（staging）照样一次成功，staging
//     全部清理（tl1390 ①「可覆盖既有侧车」）；
//  2. 失败路径（Windows 句柄占用，与事故同形）：decline 后零残留——副本
//     侧车全在副本名、主侧车字节原位、无 .promote-staging；
//  3. 崩溃续跑：上次崩溃留下的 .promote-staging 按三态还原后转正照常完成
//     （tl1390 ②「幂等续跑」）。
const (
	tl1390MainStem  = "topic"
	tl1390CopyStem  = "topic-recovery-21b887801d82d805"
	tl1390MainPath  = tl1390MainStem + ".jsonl"
	tl1390CopyPath  = tl1390CopyStem + ".jsonl"
	tl1390MainBytes = "MAIN-OLD-BYTES"
	tl1390CopyBytes = "COPY-NEW-BYTES"
)

func writeTL1390File(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedTL1390Promotion(t *testing.T, dir string, withMainSidecars, withCopySidecars bool) string {
	t.Helper()
	main := filepath.Join(dir, tl1390MainPath)
	copyPath := filepath.Join(dir, tl1390CopyPath)
	writeTL1390File(t, main, tl1390MainBytes)
	writeTL1390File(t, copyPath, tl1390CopyBytes)
	if withMainSidecars {
		writeTL1390File(t, store.SessionMeta(main), "meta-old")
		writeTL1390File(t, store.SessionEventLog(main), "events-old")
		writeTL1390File(t, store.SessionTurnEventLog(main), "turns-old")
	}
	if withCopySidecars {
		writeTL1390File(t, store.SessionMeta(copyPath), "meta-new")
		writeTL1390File(t, store.SessionEventLog(copyPath), "events-new")
		writeTL1390File(t, store.SessionTurnEventLog(copyPath), "turns-new")
	}
	return dir
}

func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	leftovers, err := filepath.Glob(filepath.Join(dir, "*"+promoteStagingSuffix))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("staging leftovers after promotion: %v", leftovers)
	}
}

func Test1390PromotionSwapsPreexistingMainSidecarsAndCleansStaging(t *testing.T) {
	dir := seedTL1390Promotion(t, t.TempDir(), true, true)
	main := filepath.Join(dir, tl1390MainPath)
	copyPath := filepath.Join(dir, tl1390CopyPath)

	archived, err := PromoteRecoveryBranchToMain(copyPath, main)
	if err != nil {
		t.Fatalf("promotion with preexisting main sidecars: %v", err)
	}
	if archived == "" {
		t.Fatal("displaced main bytes must be archived by the allowlist backup")
	}
	// 主名全族 = 副本字节（换位落位），副本名全族消失。
	for _, p := range []string{main, store.SessionMeta(main), store.SessionEventLog(main), store.SessionTurnEventLog(main)} {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read promoted %s: %v", p, err)
		}
		if string(raw) == tl1390MainBytes || strings.HasSuffix(string(raw), "-old") {
			t.Fatalf("%s still holds pre-promotion bytes: %q", p, string(raw))
		}
	}
	for _, p := range []string{copyPath, store.SessionMeta(copyPath), store.SessionEventLog(copyPath), store.SessionTurnEventLog(copyPath)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s must be gone after promotion, lstat err=%v", p, err)
		}
	}
	assertNoStagingLeftovers(t, dir)
}

// 与 2026-11-04 事故同形：主名（或副本名）侧车被打开句柄占住时转正必须
// 干净 decline，且绝不留下半搬现场。Windows 的 os.Open 共享模式不含
// FILE_SHARE_DELETE，这是事故的实机机制；非 Windows 无法用句柄复现该
// 失败形态，跳过（成功路径与崩溃续跑两腿在所有平台覆盖事务代码）。
func Test1390PromotionDeclinesCleanlyWhenSidecarHandleHeld(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skipf("open-handle rename denial is the Windows mechanism (%s renames open files)", runtime.GOOS)
	}
	dir := seedTL1390Promotion(t, t.TempDir(), true, true)
	main := filepath.Join(dir, tl1390MainPath)
	copyPath := filepath.Join(dir, tl1390CopyPath)

	// 事故形态一（15:11）：源侧车（副本 turns.jsonl）被活跃账本写手占住。
	heldSrc, err := os.OpenFile(store.SessionTurnEventLog(copyPath), os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, promoErr := PromoteRecoveryBranchToMain(copyPath, main)
	heldSrc.Close()
	if promoErr == nil {
		t.Fatal("promotion must decline while a copy sidecar handle is open")
	}
	if !strings.Contains(promoErr.Error(), "being used by another process") {
		t.Fatalf("decline should name the sharing violation, got: %v", promoErr)
	}
	assertPromotionLeftNoTrace(t, dir, main, copyPath)

	// 事故形态二（15:13+）：目标侧车（主名 turns.jsonl）被占住。
	heldDst, err := os.OpenFile(store.SessionTurnEventLog(main), os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, promoErr = PromoteRecoveryBranchToMain(copyPath, main)
	heldDst.Close()
	if promoErr == nil {
		t.Fatal("promotion must decline while a main sidecar handle is open")
	}
	assertPromotionLeftNoTrace(t, dir, main, copyPath)
}

func assertPromotionLeftNoTrace(t *testing.T, dir, main, copyPath string) {
	t.Helper()
	// 副本侧车全在副本名、字节未动（转录=COPY-NEW-BYTES，侧车=*-new）。
	for _, p := range []struct{ path, want string }{
		{copyPath, tl1390CopyBytes},
		{store.SessionMeta(copyPath), "meta-new"},
		{store.SessionEventLog(copyPath), "events-new"},
		{store.SessionTurnEventLog(copyPath), "turns-new"},
	} {
		raw, err := os.ReadFile(p.path)
		if err != nil || string(raw) != p.want {
			t.Fatalf("declined promotion must leave copy sidecars on recovery names intact: %s err=%v raw=%q", p.path, err, string(raw))
		}
	}
	// 主侧车字节原位（-old 尾缀）。
	for _, p := range []struct{ path, want string }{
		{store.SessionMeta(main), "meta-old"},
		{store.SessionEventLog(main), "events-old"},
		{store.SessionTurnEventLog(main), "turns-old"},
	} {
		raw, err := os.ReadFile(p.path)
		if err != nil || string(raw) != p.want {
			t.Fatalf("declined promotion must keep main sidecar bytes in place: %s err=%v raw=%q", p.path, err, string(raw))
		}
	}
	// 无 staging 残留。
	assertNoStagingLeftovers(t, dir)
}

func Test1390PromotionResumesFromCrashedStagingResidue(t *testing.T) {
	t.Run("pair-completed", func(t *testing.T) {
		// 崩溃点在配对完成之后：副本侧车已不在、主名已是副本字节、staging
		// 持有原始主字节。续跑应丢弃 staging 并把转录也搬过来。
		dir := seedTL1390Promotion(t, t.TempDir(), true, true)
		main := filepath.Join(dir, tl1390MainPath)
		copyPath := filepath.Join(dir, tl1390CopyPath)
		// 人工构造「meta 配对已完成」现场：副本 meta 已落主名（rename 即移走），
		// staging 持有原始主字节。
		if err := os.Rename(store.SessionMeta(copyPath), store.SessionMeta(main)); err != nil {
			t.Fatal(err)
		}
		writeTL1390File(t, store.SessionMeta(main)+promoteStagingSuffix, "meta-old")
		if _, err := PromoteRecoveryBranchToMain(copyPath, main); err != nil {
			t.Fatalf("resume from completed-pair residue: %v", err)
		}
		assertNoStagingLeftovers(t, dir)
		if _, err := os.Lstat(copyPath); !os.IsNotExist(err) {
			t.Fatal("copy transcript must rename onto the main name")
		}
	})
	t.Run("pair-interrupted", func(t *testing.T) {
		// 崩溃点在换位中间：主侧车已挪 staging、副本侧车还在原地。续跑应
		// 先归还主字节再照常换位，最终转正完整成功。
		dir := seedTL1390Promotion(t, t.TempDir(), true, true)
		main := filepath.Join(dir, tl1390MainPath)
		copyPath := filepath.Join(dir, tl1390CopyPath)
		if err := os.Rename(store.SessionEventLog(main), store.SessionEventLog(main)+promoteStagingSuffix); err != nil {
			t.Fatal(err)
		}
		if _, err := PromoteRecoveryBranchToMain(copyPath, main); err != nil {
			t.Fatalf("resume from interrupted-pair residue: %v", err)
		}
		assertNoStagingLeftovers(t, dir)
		raw, err := os.ReadFile(store.SessionEventLog(main))
		if err != nil || string(raw) != "events-new" {
			t.Fatalf("main event log must hold the promoted copy bytes, err=%v raw=%q", err, string(raw))
		}
	})
}
