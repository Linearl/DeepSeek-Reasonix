package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1442（2026-11-05 用户实测：删分支 5 次 4 例残留 1~83 文件）：分支删除链的
// 侧车清理以前只覆盖 SessionSidecarFiles 核心 + telemetry + 三个目录，漏掉了
// toolstats 计数、guardian 全家、自愈 pre-repair 备份目录、reminders 队列、
// 旧版 imgpack 目录——这些就是用户盘上的残留。本文件钉三道防线：
//
//  1. 残留=0：带全套侧车的分支 TrashRecoveryBranchForced 后，会话目录里
//     stem 前缀条目一个不剩（锁句柄由 guard 随释放删除）；
//  2. 五个用户实测形态各一钉（toolstats+inbox 形态 / guardian 六件套 /
//     pre-repair 目录群 / 外部修复 bak 文件（兜底清扫接）/ 全家桶）；
//  3. 侧车全集清单钉：recoveryTrashSidecars 的产出与登记清单全等——将来
//     新增数据侧车必须显式扩这份清单（黑名单式判断随新类型漏网的教训，
//     见 sessionBackupAllowlist 注释）。

// plantFile 在会话目录里种一个侧车文件（构造副本，不动用户真实会话文件）。
func plantFile(t *testing.T, dir, name string) string {
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

// plantDirArtifact 种一个非空目录工件（ckpt/jobs/inbox/pre-repair 等）。
func plantDirArtifact(t *testing.T, dir, name string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(full, "placeholder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "placeholder", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

// newBranchFixture 构造一个带转录的分支（其余侧车按形态另种）。
func newBranchFixture(t *testing.T, stem string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, stem+".jsonl")
	if err := os.WriteFile(path, []byte("{\"kind\":\"branch\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// plantFullFamily 按磁盘实测全集种上全部 stem 前缀侧车（含外部修复 bak 与
// 运行时句柄），返回种下的条目名集合——不含转录自身。
func plantFullFamily(t *testing.T, dir, branchPath string) map[string]bool {
	t.Helper()
	stem := strings.TrimSuffix(filepath.Base(branchPath), ".jsonl")
	files := []string{
		stem + ".jsonl.meta",
		stem + ".jsonl.telemetry.json",
		stem + ".jsonl.toolstats.json",
		stem + ".events.jsonl",
		stem + ".events.jsonl.zst",
		stem + ".events.jsonl.damaged",
		stem + ".events.jsonl.rotating",
		stem + ".jsonl.zst",
		stem + ".turns.jsonl",
		stem + ".turns.jsonl.damaged",
		stem + ".event-index.json",
		stem + ".display-index.json",
		stem + ".conflicts.jsonl",
		stem + ".repairlog.jsonl",
		stem + ".recovery.json",
		stem + ".context.json",
		stem + ".goal-state.json",
		stem + ".pinned-context.json",
		stem + ".guardian.jsonl",
		stem + ".guardian.jsonl.meta",
		stem + ".guardian.events.jsonl",
		stem + ".guardian.display-index.json",
		stem + ".guardian.event-index.json",
		stem + ".guardian.cursor.json",
		// 外部修复工具的备份形态（不在显式清单，兜底清扫接）。
		stem + ".jsonl.bak-20261104-cas",
		stem + ".jsonl.meta.bak-20261104-cas",
		// 运行时句柄：guard 随释放删除；先种上验证不会被清扫误搬。
		stem + ".jsonl.lease.json",
		stem + ".cleanup-pending.json",
	}
	dirs := []string{
		stem + ".ckpt",
		stem + ".jobs",
		stem + ".inbox",
		stem + ".reminders",
		stem + ".jsonl.imgpack",
		stem + ".pre-repair-20261102T025757",
		stem + ".pre-repair-20261105T052131",
	}
	planted := make(map[string]bool, len(files)+len(dirs)+1)
	for _, name := range files {
		plantFile(t, dir, name)
		planted[name] = true
	}
	for _, name := range dirs {
		plantDirArtifact(t, dir, name)
		planted[name] = true
	}
	return planted
}

// assertZeroStemResidue 断言删除后会话目录里 stem 前缀条目一个不剩，
// .trash 桶里种下的每件（含转录本体）都在。
func assertZeroStemResidue(t *testing.T, dir, branchPath string, planted map[string]bool) {
	t.Helper()
	stem := strings.TrimSuffix(filepath.Base(branchPath), ".jsonl")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var residue []string
	for _, entry := range entries {
		name := entry.Name()
		if name == ".trash" || !strings.HasPrefix(name, stem+".") {
			continue
		}
		residue = append(residue, name)
	}
	if len(residue) > 0 {
		t.Fatalf("deleted branch left %d stem-prefixed entries behind: %v", len(residue), residue)
	}
	// 桶内取回验证：.trash 下唯一桶，种下的每件都在桶里。
	buckets, err := os.ReadDir(filepath.Join(dir, ".trash"))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 {
		t.Fatalf("want exactly one trash bucket, got %d", len(buckets))
	}
	bucket := filepath.Join(dir, ".trash", buckets[0].Name())
	for name := range planted {
		if _, err := os.Lstat(filepath.Join(bucket, name)); err != nil {
			t.Fatalf("planted artifact %q missing from trash bucket: %v", name, err)
		}
	}
}

// runtimeHandleNamesOwnedByGuard 运行时句柄名——guard/ClearCleanupPending
// 负责删除，不进桶，所以桶内容断言要把它们从 planted 集里剔除。
func runtimeHandleNamesOwnedByGuard(branchPath string) map[string]bool {
	return map[string]bool{
		filepath.Base(store.SessionLockFile(branchPath)):       true,
		filepath.Base(store.SessionLeaseLock(branchPath)):      true,
		filepath.Base(store.SessionLeaseInfo(branchPath)):      true,
		filepath.Base(store.SessionCleanupPending(branchPath)): true,
	}
}

// plantedForBucket = planted - 运行时句柄（guard 随释放删，不进桶）。
func plantedForBucket(branchPath string, planted map[string]bool) map[string]bool {
	handles := runtimeHandleNamesOwnedByGuard(branchPath)
	out := make(map[string]bool, len(planted))
	for name := range planted {
		if handles[name] {
			continue
		}
		out[name] = true
	}
	return out
}

// TestTrashRecoveryBranchForcedLeavesZeroStemResidue 是验收主钉：全套侧车的
// 分支删除后 stem 前缀残留 = 0，全部侧车进桶，运行时句柄被 guard 清掉。
func TestTrashRecoveryBranchForcedLeavesZeroStemResidue(t *testing.T) {
	dir, path := newBranchFixture(t, "20260923-045044.233465200-collab-recovery-c42c106f1f71ccd9")
	planted := plantFullFamily(t, dir, path)
	planted[filepath.Base(path)] = true // 转录本体也必须在桶里

	if err := TrashRecoveryBranchForced(path, dir); err != nil {
		t.Fatalf("TrashRecoveryBranchForced: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("branch transcript still in the session directory: %v", err)
	}
	assertZeroStemResidue(t, dir, path, plantedForBucket(path, planted))
	// 运行时句柄必须被 guard 清掉（不是被搬进桶）：删除后锁三件与
	// cleanup-pending 在盘上任何 stem 前缀位置都不该有（上面已验目录侧，
	// 这里再钉名字面，防的是「清扫搬走后 guard 重建」这类回归）。
	key := filepath.Base(path)
	for _, name := range []string{
		key + ".lock",
		key + ".lease.lock",
		key + ".lease.json",
	} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("runtime handle %q survived the delete: %v", name, err)
		}
	}
}

// TestTrashRecoveryBranchForcedFieldResidueShapes 把 2026-11-05 用户实测的
// 五个残留形态各钉一案（构造副本演练，不动真实会话文件）。
func TestTrashRecoveryBranchForcedFieldResidueShapes(t *testing.T) {
	cases := []struct {
		name string
		stem string
		seed func(t *testing.T, dir, path string) map[string]bool
	}{
		{
			// 形态①（用户样本 20260927-020815...-recovery-db0301da280e0311）：
			// telemetry + toolstats + inbox 残留——删除链一直漏 toolstats。
			name: "toolstats_telemetry_inbox",
			stem: "20260927-020815.856309600-collab-recovery-db0301da280e0311",
			seed: func(t *testing.T, dir, path string) map[string]bool {
				stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
				names := []string{
					stem + ".jsonl.telemetry.json",
					stem + ".jsonl.toolstats.json",
				}
				for _, n := range names {
					plantFile(t, dir, n)
				}
				plantDirArtifact(t, dir, stem+".inbox")
				return nameSet(append(names, stem+".inbox"), filepath.Base(path))
			},
		},
		{
			// 形态②（用户样本 20260923-045044...-recovery-c42c106f1f71ccd9 全家）：
			// guardian 六件套残留。
			name: "guardian_family",
			stem: "20260923-045044.233465200-collab-recovery-c42c106f1f71ccd9",
			seed: func(t *testing.T, dir, path string) map[string]bool {
				stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
				names := []string{
					stem + ".guardian.jsonl",
					stem + ".guardian.jsonl.meta",
					stem + ".guardian.events.jsonl",
					stem + ".guardian.display-index.json",
					stem + ".guardian.event-index.json",
					stem + ".guardian.cursor.json",
				}
				for _, n := range names {
					plantFile(t, dir, n)
				}
				return nameSet(names, filepath.Base(path))
			},
		},
		{
			// 形态③（用户样本 20260927-020815.856309600-collab 系）：自愈
			// pre-repair 备份目录群（实测一颗树上最多 5 个）。
			name: "pre_repair_dirs",
			stem: "20260927-020815.856309600-collab-recovery-0123456789abcdef",
			seed: func(t *testing.T, dir, path string) map[string]bool {
				stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
				var names []string
				for _, stamp := range []string{
					"20261102T025757", "20261103T160416", "20261105T052131",
					"20261105T063012", "20261105T071030",
				} {
					plantDirArtifact(t, dir, stem+".pre-repair-"+stamp)
					names = append(names, stem+".pre-repair-"+stamp)
				}
				return nameSet(names, filepath.Base(path))
			},
		},
		{
			// 形态④（用户样本 20261102-021909...-recovery-9318b1f6f31a394e）：
			// 外部修复工具的 .bak-<stamp>-<tag> 备份文件——显式清单不认识，
			// 由 stem 前缀兜底清扫接住并在日志留 straggler 记录。
			name: "repair_bak_files",
			stem: "20261102-021909.744449100-mimo-token-plan-mimo-v2.6-flash-recovery-9318b1f6f31a394e",
			seed: func(t *testing.T, dir, path string) map[string]bool {
				stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
				names := []string{
					stem + ".jsonl.bak-20261104-cas",
					stem + ".jsonl.meta.bak-20261104-cas",
					stem + ".pinned-context.json.bak-20261103-2340",
				}
				for _, n := range names {
					plantFile(t, dir, n)
				}
				return nameSet(names, filepath.Base(path))
			},
		},
		{
			// 形态⑤：83 件级全家桶（实测上限形态）——全集一次种满。
			name: "full_family",
			stem: "20260923-045044.233465200-collab-recovery-c42c106f1f71ccd9",
			seed: func(t *testing.T, dir, path string) map[string]bool {
				planted := plantFullFamily(t, dir, path)
				planted[filepath.Base(path)] = true
				// seed 的返回值是桶内容期望；运行时句柄由 guard/ClearCleanupPending
				// 删除，不进桶。
				return plantedForBucket(path, planted)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path := newBranchFixture(t, tc.stem)
			planted := tc.seed(t, dir, path)
			if err := TrashRecoveryBranchForced(path, dir); err != nil {
				t.Fatalf("TrashRecoveryBranchForced: %v", err)
			}
			assertZeroStemResidue(t, dir, path, planted)
		})
	}
}

func nameSet(names []string, extra ...string) map[string]bool {
	out := make(map[string]bool, len(names)+len(extra))
	for _, n := range names {
		out[n] = true
	}
	for _, n := range extra {
		out[n] = true
	}
	return out
}

// TestRecoveryTrashSidecarsCoversFullInventory 是清单驱动钉（防再漏）：
// recoveryTrashSidecars 的产出必须与登记的侧车全集全等。新增数据侧车时
// 先在 store 加构造器、再扩这里的期望清单——两个动作都要过评审，缺一即红。
func TestRecoveryTrashSidecarsCoversFullInventory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260923-045044.233465200-collab-recovery-c42c106f1f71ccd9.jsonl")
	stem := strings.TrimSuffix(path, ".jsonl")
	// pre-repair glob 的两个实例要实存才进清单。
	for _, stamp := range []string{"20261102T025757", "20261105T052131"} {
		if err := os.MkdirAll(stem+".pre-repair-"+stamp, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := recoveryTrashSidecars(path)
	gotNames := make([]string, 0, len(got))
	for _, p := range got {
		gotNames = append(gotNames, filepath.Base(p))
	}
	slices.Sort(gotNames)

	base := func(p string) string { return filepath.Base(p) }
	guardian := store.SessionGuardianTranscript(path)
	want := []string{}
	for _, p := range store.SessionSidecarFiles(path) {
		want = append(want, base(p))
	}
	// guardian 家族：guardian 转录按会话层保存，自带同一套数据侧车 + 游标。
	for _, p := range store.SessionSidecarFiles(guardian) {
		want = append(want, base(p))
	}
	want = append(want,
		base(store.SessionGuardianTranscript(path)),
		base(store.SessionGuardianCursor(path)),
		// 运行时计数与遥测（2026-11-05 实测缺口）。
		base(store.SessionTelemetry(path)),
		base(store.SessionToolStats(path)),
		// 目录五件 + 自愈备份目录两实例（glob 产全路径，统一按文件名比对）。
		base(store.SessionCheckpointDir(path)),
		base(store.SessionJobsDir(path)),
		base(store.SessionInboxDir(path)),
		base(store.SessionRemindersDir(path)),
		base(store.SessionImagePackDir(path)),
		base(stem+".pre-repair-20261102T025757"),
		base(stem+".pre-repair-20261105T052131"),
	)
	slices.Sort(want)
	if !slices.Equal(gotNames, want) {
		t.Fatalf("recoveryTrashSidecars inventory drifted:\n got (%d): %v\nwant (%d): %v",
			len(gotNames), gotNames, len(want), want)
	}
}

// TestMoveRecoveryTrashArtifactsCountsAndFlagsStragglers 钉计数报告（删除
// 日志计数的数据面）：文件/目录分开数、类别按 stem 相对后缀命名、显式清单
// 外的 stem 前缀条目进 Stragglers（日志扩清单的信号）、运行时句柄不被清扫
// 误搬（guard 自己删）。
func TestMoveRecoveryTrashArtifactsCountsAndFlagsStragglers(t *testing.T) {
	dir, path := newBranchFixture(t, "20260927-020815.856309600-collab-recovery-db0301da280e0311")
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	// 显式清单内的：meta（文件）+ ckpt（目录）。
	plantFile(t, dir, stem+".jsonl.meta")
	plantDirArtifact(t, dir, stem+".ckpt")
	// 显式清单外的：外部 bak 备份（straggler）。
	plantFile(t, dir, stem+".jsonl.bak-20261104-cas")
	// 运行时句柄：必须在清扫后原地不动（guard 负责删）。
	leaseJSON := plantFile(t, dir, stem+".jsonl.lease.json")

	itemDir := filepath.Join(dir, "bucket-stage")
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := moveRecoveryTrashArtifacts(dir, path, itemDir)
	if err != nil {
		t.Fatalf("moveRecoveryTrashArtifacts: %v", err)
	}
	if report.Files != 2 { // 清单内 meta + 兜底 bak
		t.Fatalf("report.Files = %d, want 2 (meta + bak)", report.Files)
	}
	if report.Dirs != 1 { // ckpt
		t.Fatalf("report.Dirs = %d, want 1 (ckpt)", report.Dirs)
	}
	if !slices.Contains(report.Stragglers, stem+".jsonl.bak-20261104-cas") {
		t.Fatalf("Stragglers = %v, want the unlisted bak file flagged", report.Stragglers)
	}
	for _, category := range []string{"jsonl.meta", "ckpt", "jsonl.bak-20261104-cas"} {
		if !slices.Contains(report.Categories, category) {
			t.Fatalf("Categories = %v, want %q named by its stem-relative suffix", report.Categories, category)
		}
	}
	if _, err := os.Lstat(leaseJSON); err != nil {
		t.Fatalf("runtime handle lease.json must stay for the guard to delete: %v", err)
	}
	for _, name := range []string{stem + ".jsonl.meta", stem + ".ckpt", stem + ".jsonl.bak-20261104-cas"} {
		if _, err := os.Lstat(filepath.Join(itemDir, name)); err != nil {
			t.Fatalf("%q missing from item dir: %v", name, err)
		}
	}
}
