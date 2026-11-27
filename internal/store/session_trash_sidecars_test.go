package store

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tl1442-ext 清单源钉：SessionTrashSidecars 是删除/换位链侧车全集的唯一事实
// 源。这里钉它的构成（static 全集 + 实存的 pre-repair glob 实例）与 static
// 形态的确定性（identity-swap 按位配对的前提：同会话两个拼写产出等长同序）。
func TestSessionTrashSidecarsComposeStaticAndPreRepairGlob(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260923-045044.233465200-collab.jsonl")
	stem := strings.TrimSuffix(path, SessionTranscriptSuffix)
	for _, stamp := range []string{"20261102T025757", "20261105T052131"} {
		if err := os.MkdirAll(stem+".pre-repair-"+stamp, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	full := SessionTrashSidecars(path)
	static := SessionStaticTrashSidecars(path)
	want := append(append([]string(nil), static...),
		stem+".pre-repair-20261102T025757",
		stem+".pre-repair-20261105T052131",
	)
	if !slices.Equal(full, want) {
		t.Fatalf("SessionTrashSidecars drifted:\n got: %v\nwant: %v", full, want)
	}
	for _, artifact := range full {
		if !strings.HasPrefix(filepath.Base(artifact), strings.TrimSuffix(filepath.Base(path), SessionTranscriptSuffix)+".") {
			t.Fatalf("inventory entry %q does not carry the session stem prefix", artifact)
		}
	}
	// 守卫面：锁三件与 cleanup-pending 是运行时句柄，永不进清单。
	handles := map[string]bool{
		filepath.Base(SessionLockFile(path)):       true,
		filepath.Base(SessionLeaseLock(path)):      true,
		filepath.Base(SessionLeaseInfo(path)):      true,
		filepath.Base(SessionCleanupPending(path)): true,
	}
	for _, artifact := range full {
		if handles[filepath.Base(artifact)] {
			t.Fatalf("runtime handle %q leaked into the sidecar inventory", artifact)
		}
	}
}

// static 形态确定性：两条不同 stem 的清单必须等长同位（tl1390 两步换位按
// 索引配对的前提；glob 形态两侧数量可以不同，所以换位面必须用 static）。
func TestSessionStaticTrashSidecarsDeterministicPairing(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "topic.jsonl")
	copy := filepath.Join(dir, "topic-recovery-0123456789abcdef.jsonl")
	mainList := SessionStaticTrashSidecars(main)
	copyList := SessionStaticTrashSidecars(copy)
	if len(mainList) != len(copyList) {
		t.Fatalf("static lists diverge in length: main %d vs copy %d — identity-swap pairing would misalign", len(mainList), len(copyList))
	}
	mainStem := strings.TrimSuffix(filepath.Base(main), SessionTranscriptSuffix)
	copyStem := strings.TrimSuffix(filepath.Base(copy), SessionTranscriptSuffix)
	for i := range mainList {
		mainRel := strings.TrimPrefix(filepath.Base(mainList[i]), mainStem)
		copyRel := strings.TrimPrefix(filepath.Base(copyList[i]), copyStem)
		if mainRel != copyRel {
			t.Fatalf("static lists diverge at %d: main rel %q vs copy rel %q", i, mainRel, copyRel)
		}
	}
}

// tl1850 收窄钉：清单的 guardian 家族段必须恰好等于 guardian 路径的实际数据
// 面（转录+游标+meta+events+双 index，共 6 件）——此前对 guardianPath 全量
// 重跑 SessionSidecarFiles，把 11 个从未有写入方的形态（goal-state/recovery/
// context/pinned-context/turns×2/conflicts/zst×3/damaged）当成幽灵清单条目
// 派生进每条删除链（tl1849 实证磁盘零实例）。本测试钉「清单里没有→不要求
// 白名单」的收窄语义：11 幽灵键（含 tl1841 曾补登、重核无写入方一并收窄的
// 三审计键）任何一个回流清单即红，此时须同时登记白名单与豁免表（同 commit
// 两处清单纪律）。
func TestSessionStaticTrashSidecarsGuardianFamilyMatchesRealDataFace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topic.jsonl")
	stem := strings.TrimSuffix(filepath.Base(path), SessionTranscriptSuffix)
	guardianPath := SessionGuardianTranscript(path)

	// 实际数据面：6 件（转录+游标+meta+events+双 index）。
	want := map[string]bool{
		filepath.Base(guardianPath):                      true,
		filepath.Base(SessionGuardianCursor(path)):       true,
		filepath.Base(SessionMeta(guardianPath)):         true,
		filepath.Base(SessionEventLog(guardianPath)):     true,
		filepath.Base(SessionEventIndex(guardianPath)):   true,
		filepath.Base(SessionDisplayIndex(guardianPath)): true,
	}
	inventory := SessionStaticTrashSidecars(path)
	got := map[string]bool{}
	for _, artifact := range inventory {
		base := filepath.Base(artifact)
		if strings.HasPrefix(base, stem+".guardian") {
			got[base] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("guardian family inventory drifted off the real data face (tl1850): got %d guardian-shaped entries, want exactly %d: %v", len(got), len(want), got)
	}
	for base := range want {
		if !got[base] {
			t.Fatalf("guardian real-data-face entry %q missing from the inventory (tl1850)", base)
		}
	}

	// 幽灵键黑名单：11 个从未有 guardian 写入方的形态 + tl1841 曾补登、
	// tl1850 重核无写入方一并收窄的三审计键，任何一个回流清单即红。
	ghosts := []string{
		"guardian.goal-state.json",
		"guardian.recovery.json",
		"guardian.context.json",
		"guardian.pinned-context.json",
		"guardian.turns.jsonl",
		"guardian.turns.jsonl.damaged",
		"guardian.conflicts.jsonl",
		"guardian.events.jsonl.zst",
		"guardian.events.jsonl.damaged",
		"guardian.events.jsonl.rotating",
		"guardian.jsonl.zst",
		"guardian.conflict-preserved.jsonl",
		"guardian.splices.json",
		"guardian.rewritelog.jsonl",
	}
	seen := map[string]bool{}
	for _, artifact := range inventory {
		seen[filepath.Base(artifact)] = true
	}
	for _, ghost := range ghosts {
		if seen[stem+"."+ghost] {
			t.Fatalf("phantom guardian shape %q re-entered the inventory — if a guardian-side writer was added on purpose, register the backup allowlist and the exemptions in the same commit", ghost)
		}
	}

	// 空输入卫生：子集构造器对空路径返回 nil（与其他路径构造器同约定）。
	if nilGot := SessionGuardianSidecarFiles(""); nilGot != nil {
		t.Fatalf("SessionGuardianSidecarFiles(\"\") = %v, want nil", nilGot)
	}
}
