package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1442-ext（2026-11-07 用户裁决 A）：merge-promote 两处与删除链共用
// store.SessionTrashSidecars 全集清单。本文件钉两案：
//
//  1. C1 promoteRecoveryCopyToMain：被替换的旧主线整家进回收站（全集清单 +
//     兜底清扫 + subagent），winner 副本的侧车全家改嫁主名——两条 stem 下
//     都零残留；
//  2. C2 PromoteRecoveryBranchToMain：副本转正后副本 stem 零残留（static
//     全集换位 + pre-repair 群/清单外残条改嫁），被逐下的旧主线字节在白名单
//     归档里（含 tl1442-ext 补登记的存储形态）。

// extAgentPlantFile 种侧车文件（构造副本，不动真实会话文件）。
func extAgentPlantFile(t *testing.T, dir, name string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

// extAgentPlantDir 种非空目录工件。
func extAgentPlantDir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name, "placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name, "placeholder", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// extAgentPlantFamily 按实测全集种一个 stem 的数据侧车（不含运行时句柄——
// promote 的守卫只 Release 不删锁件，断言按句柄名豁免）。
func extAgentPlantFamily(t *testing.T, dir, stem string, withPreRepair bool) []string {
	t.Helper()
	names := []string{
		stem + ".jsonl.meta",
		stem + ".jsonl.telemetry.json",
		stem + ".jsonl.toolstats.json",
		stem + ".events.jsonl",
		stem + ".guardian.jsonl",
		stem + ".guardian.cursor.json",
		stem + ".guardian.jsonl.meta",
		stem + ".guardian.events.jsonl",
		stem + ".turns.jsonl",
		stem + ".conflicts.jsonl",
		stem + ".repairlog.jsonl",
		stem + ".recovery.json",
		stem + ".goal-state.json",
		stem + ".pinned-context.json",
		// 外部修复 bak（清单外，兜底接）。
		stem + ".jsonl.bak-20261104-cas",
	}
	for _, name := range names {
		extAgentPlantFile(t, dir, name)
	}
	for _, d := range []string{stem + ".ckpt", stem + ".jobs", stem + ".inbox", stem + ".reminders", stem + ".jsonl.imgpack"} {
		extAgentPlantDir(t, dir, d)
		names = append(names, d)
	}
	if withPreRepair {
		for _, stamp := range []string{"20261102T025757", "20261105T052131"} {
			extAgentPlantDir(t, dir, stem+".pre-repair-"+stamp)
			names = append(names, stem+".pre-repair-"+stamp)
		}
	}
	return names
}

// extAgentHandleNames promote 路径的运行时句柄豁免集（守卫/锁机制自有处置）。
func extAgentHandleNames(path string) map[string]bool {
	out := map[string]bool{}
	for _, p := range []string{
		store.SessionLockFile(path),
		store.SessionLeaseLock(path),
		store.SessionLeaseInfo(path),
		store.SessionCleanupPending(path),
	} {
		out[filepath.Base(p)] = true
	}
	return out
}

// extAgentAssertZeroStemResidue 断言 dir 下所有给定 stem 的前缀条目只剩豁免
// 句柄；mustExist 里的每个名字都必须在盘上（改嫁/保留断言）。
func extAgentAssertZeroStemResidue(t *testing.T, dir string, stems []string, handles map[string]bool, mustExist []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var residue []string
	for _, entry := range entries {
		name := entry.Name()
		if name == ".trash" || handles[name] {
			continue
		}
		for _, stem := range stems {
			if strings.HasPrefix(name, stem+".") {
				residue = append(residue, name)
				break
			}
		}
	}
	if len(residue) > 0 {
		t.Fatalf("promotion left %d stem-prefixed entries behind: %v", len(residue), residue)
	}
	for _, name := range mustExist {
		full := name
		if !filepath.IsAbs(full) {
			full = filepath.Join(dir, name)
		}
		if _, err := os.Lstat(full); err != nil {
			t.Fatalf("expected artifact %q missing after promotion: %v", name, err)
		}
	}
}

// TestPromoteRecoveryCopyToMainLeavesZeroResidue C1 主钉：旧主线（含全家侧车
// 与清单外 bak）整家进回收站桶，winner 副本全家（含 pre-repair 群）改嫁主名，
// 派生索引按既有语义丢弃，两条 stem 零残留。
func TestPromoteRecoveryCopyToMainLeavesZeroResidue(t *testing.T) {
	dir := t.TempDir()
	mainPath := seedMainTranscript(t, dir, "topic")
	base := loadSnapshot(t, mainPath)
	// winner = base + 1 turn：完全覆盖旧主线，转正方向安全。
	winnerPath := forkCopyFromSnapshot(t, mainPath, base, []provider.Message{
		{Role: provider.RoleUser, Content: "recovered turn a"},
		{Role: provider.RoleAssistant, Content: "recovered reply b"},
	})

	mainStem := strings.TrimSuffix(filepath.Base(mainPath), ".jsonl")
	winnerStem := strings.TrimSuffix(filepath.Base(winnerPath), ".jsonl")
	mainFamily := extAgentPlantFamily(t, dir, mainStem, true)
	// winner 派生索引（按既有语义应被丢弃而非改嫁）。
	for _, name := range []string{winnerStem + ".event-index.json", winnerStem + ".display-index.json", winnerStem + ".events.jsonl.damaged"} {
		extAgentPlantFile(t, dir, name)
	}
	// winner 数据全家 + pre-repair 群（改嫁断言面）。
	extAgentPlantFamily(t, dir, winnerStem, true)

	if _, _, err := promoteRecoveryCopyToMain(mainPath, winnerPath, dir, false); err != nil {
		t.Fatalf("promoteRecoveryCopyToMain: %v", err)
	}

	// 旧主线整家在回收站桶里（含兜底接的 bak）。
	buckets, err := os.ReadDir(filepath.Join(dir, ".trash"))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 {
		t.Fatalf("want exactly one trash bucket, got %d", len(buckets))
	}
	bucket := filepath.Join(dir, ".trash", buckets[0].Name())
	for _, name := range append([]string{filepath.Base(mainPath), mainStem + ".jsonl.bak-20261104-cas"}, mainFamily...) {
		if _, err := os.Lstat(filepath.Join(bucket, name)); err != nil {
			t.Fatalf("old-main artifact %q missing from trash bucket: %v", name, err)
		}
	}

	// winner 全家改嫁主名（toolstats/guardian 全家/reminders/imgpack/pre-repair
	// 群/bak），派生索引已丢弃。零残留只钉 winner 死 stem——主 stem 是活身份，
	// 它名下应当正好站着这些改嫁来的侧车。
	migrated := []string{
		mainStem + ".jsonl.meta",
		mainStem + ".events.jsonl",
		mainStem + ".turns.jsonl",
		mainStem + ".conflicts.jsonl",
		mainStem + ".repairlog.jsonl",
		mainStem + ".recovery.json",
		mainStem + ".goal-state.json",
		mainStem + ".jsonl.telemetry.json",
		mainStem + ".jsonl.toolstats.json",
		mainStem + ".guardian.jsonl",
		mainStem + ".guardian.cursor.json",
		mainStem + ".guardian.jsonl.meta",
		mainStem + ".guardian.events.jsonl",
		mainStem + ".ckpt",
		mainStem + ".jobs",
		mainStem + ".inbox",
		mainStem + ".reminders",
		mainStem + ".jsonl.imgpack",
		mainStem + ".pre-repair-20261102T025757",
		mainStem + ".pre-repair-20261105T052131",
		mainStem + ".jsonl.bak-20261104-cas",
	}
	handles := extAgentHandleNames(mainPath)
	for name := range extAgentHandleNames(winnerPath) {
		handles[name] = true
	}
	extAgentAssertZeroStemResidue(t, dir, []string{winnerStem}, handles, append(migrated, mainPath))
	for _, dropped := range []string{winnerStem + ".event-index.json", winnerStem + ".display-index.json", winnerStem + ".events.jsonl.damaged", mainStem + ".event-index.json", mainStem + ".display-index.json"} {
		if _, err := os.Lstat(filepath.Join(dir, dropped)); !os.IsNotExist(err) {
			t.Fatalf("derived index %q should be dropped (rebuilt on next load): %v", dropped, err)
		}
	}
}

// TestPromoteRecoveryBranchToMainCopySidecarsFollowOntoMain C2 主钉：fork 副本
// 转正后副本 stem 零残留（static 全集换位 + pre-repair 群/清单外 bak 改嫁），
// 被逐下的旧主线字节（含 tl1442-ext 补登记的 events.jsonl.zst 存储形态）在
// 白名单归档目录里。
func TestPromoteRecoveryBranchToMainCopySidecarsFollowOntoMain(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "topic.jsonl")
	if err := os.WriteFile(mainPath, []byte("{\"role\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	copyID := "topic-recovery-0123456789abcdef"
	copyPath := filepath.Join(dir, copyID+".jsonl")
	if err := os.WriteFile(copyPath, []byte("{\"role\":\"user\"}\n{\"role\":\"assistant\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	copyStem := strings.TrimSuffix(filepath.Base(copyPath), ".jsonl")
	mainStem := strings.TrimSuffix(filepath.Base(mainPath), ".jsonl")
	// 副本数据全家 + pre-repair 群 + 清单外 bak（改嫁断言面）。
	extAgentPlantFamily(t, dir, copyStem, true)
	// 旧主线的归档面：白名单数据侧车 + tl1442-ext 补登记的存储形态。
	for _, name := range []string{
		mainStem + ".jsonl.meta",
		mainStem + ".jsonl.toolstats.json",
		mainStem + ".events.jsonl.zst",
		mainStem + ".jsonl.zst",
		mainStem + ".turns.jsonl.damaged",
	} {
		extAgentPlantFile(t, dir, name)
	}

	if _, err := PromoteRecoveryBranchToMain(copyPath, mainPath); err != nil {
		t.Fatalf("PromoteRecoveryBranchToMain: %v", err)
	}

	// 副本全家改嫁主名。
	migrated := []string{
		mainStem + ".jsonl.telemetry.json",
		mainStem + ".jsonl.toolstats.json",
		mainStem + ".guardian.jsonl",
		mainStem + ".guardian.cursor.json",
		mainStem + ".reminders",
		mainStem + ".jsonl.imgpack",
		mainStem + ".ckpt",
		mainStem + ".pre-repair-20261102T025757",
		mainStem + ".pre-repair-20261105T052131",
		mainStem + ".jsonl.bak-20261104-cas",
	}
	handles := extAgentHandleNames(copyPath)
	for name := range extAgentHandleNames(mainPath) {
		handles[name] = true
	}
	extAgentAssertZeroStemResidue(t, dir, []string{copyStem}, handles, append(migrated, mainPath))

	// 被逐下的旧主线字节整家在白名单归档里（含补登记的存储形态）。
	// backupSessionFiles 以原文件名落盘（rel 键只管准入，名字保留 stem）。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	backupHas := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, mainStem+".pre-repair-") {
			continue
		}
		inner, err := os.ReadDir(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range inner {
			backupHas[f.Name()] = true
		}
	}
	for _, name := range []string{
		mainStem + ".jsonl",                // 旧主线转录
		mainStem + ".jsonl.meta",           // 白名单核心
		mainStem + ".jsonl.toolstats.json", // 白名单核心
		mainStem + ".events.jsonl.zst",     // tl1442-ext 补登记
		mainStem + ".jsonl.zst",            // tl1442-ext 补登记
		mainStem + ".turns.jsonl.damaged",  // tl1442-ext 补登记
	} {
		if !backupHas[name] {
			t.Fatalf("displaced main bytes missing from the allowlist backup: %q (backup has %v)", name, backupHas)
		}
	}
}
