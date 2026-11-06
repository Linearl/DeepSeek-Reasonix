package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1442-ext（2026-11-07 用户裁决 A）：desktop 主链（普通删除 / 终端归档 /
// /clear 硬删）与 recovery trash 链共用 store.SessionTrashSidecars 侧车全集
// 清单 + stem 前缀兜底清扫 + 同格式计数日志。本文件钉四道防线：
//
//  1. 残留=0：带全套侧车的会话经普通删除（deleteSessionFile 搬桶链）、终端
//     归档（trashSessionArtifactsTerminal）、/clear 硬删
//     （removeDesktopSessionArtifacts）后，会话目录 stem 前缀条目一个不剩；
//  2. 桶完整：搬桶链种下的每件（含兜底清扫接的外部 bak）都在桶里；清空回收
//     站整桶同清；
//  3. 回程完整：兜底清扫进桶的条目随还原回到目标 stem（清扫零残留不吞字节）；
//  4. 单一来源钉：desktop 清单是 store 清单的纯派生——改 store 清单函数，
//     desktop 行为同步变；revert 成手写清单立即红。

// extPlantFile 在会话目录里种一个侧车文件（构造副本，不动真实会话文件）。
func extPlantFile(t *testing.T, dir, name string) string {
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

// extPlantDirArtifact 种一个非空目录工件。
func extPlantDirArtifact(t *testing.T, dir, name string) string {
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

// extPlantFullFamily 按磁盘实测全集种上全部 stem 前缀侧车（含外部修复 bak、
// 运行时句柄）。返回种下的条目名集合——不含转录自身。
func extPlantFullFamily(t *testing.T, dir, sessionPath string) map[string]bool {
	t.Helper()
	stem := strings.TrimSuffix(filepath.Base(sessionPath), ".jsonl")
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
		stem + ".pinned-context.json.bak-20261103-2340",
		// 运行时句柄：guard/ClearCleanupPending 负责删除。
		stem + ".jsonl.lock",
		stem + ".jsonl.lease.lock",
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
	planted := make(map[string]bool)
	for _, name := range files {
		extPlantFile(t, dir, name)
		planted[name] = true
	}
	for _, name := range dirs {
		extPlantDirArtifact(t, dir, name)
		planted[name] = true
	}
	return planted
}

// extRuntimeHandleNames 运行时句柄名（guard/ClearCleanupPending 处置，不进桶）。
func extRuntimeHandleNames(sessionPath string) map[string]bool {
	return map[string]bool{
		filepath.Base(store.SessionLockFile(sessionPath)):       true,
		filepath.Base(store.SessionLeaseLock(sessionPath)):      true,
		filepath.Base(store.SessionLeaseInfo(sessionPath)):      true,
		filepath.Base(store.SessionCleanupPending(sessionPath)): true,
	}
}

// extAssertZeroStemResidue 断言删除后会话目录里 stem 前缀条目一个不剩
// （handlesExcept 里的名字除外）。expectedInBucket 非 nil 时断言 bucketsRoot
// 下唯一桶里每件都在。
func extAssertZeroStemResidue(t *testing.T, dir string, stems []string, handlesExcept map[string]bool, bucketsRoot string, expectedInBucket map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var residue []string
	for _, entry := range entries {
		name := entry.Name()
		if name == ".trash" || handlesExcept[name] {
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
		t.Fatalf("deletion left %d stem-prefixed entries behind: %v", len(residue), residue)
	}
	if expectedInBucket == nil {
		return
	}
	buckets, err := os.ReadDir(bucketsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 {
		t.Fatalf("want exactly one trash bucket, got %d", len(buckets))
	}
	bucket := filepath.Join(bucketsRoot, buckets[0].Name())
	for name := range expectedInBucket {
		if _, err := os.Lstat(filepath.Join(bucket, name)); err != nil {
			t.Fatalf("planted artifact %q missing from trash bucket: %v", name, err)
		}
	}
}

// newExtSessionFixture 构造一个带转录的会话（其余侧车按形态另种）。
func newExtSessionFixture(t *testing.T, stem string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, stem+".jsonl")
	if err := os.WriteFile(path, []byte("{\"kind\":\"desktop-session\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// TestDeleteSessionFileLeavesZeroSidecarResidue 验收主钉（普通 delete）：全套
// 侧车的会话删除后 stem 前缀残留 = 0，全部侧车（含兜底 bak）进桶，运行时
// 句柄被 guard/ClearCleanupPending 清掉。
func TestDeleteSessionFileLeavesZeroSidecarResidue(t *testing.T) {
	dir, path := newExtSessionFixture(t, "20260927-020815.856309600-collab")
	planted := extPlantFullFamily(t, dir, path)
	planted[filepath.Base(path)] = true // 转录本体也必须在桶里
	handles := extRuntimeHandleNames(path)

	if err := deleteSessionFile(dir, path); err != nil {
		t.Fatalf("deleteSessionFile: %v", err)
	}
	for name := range handles {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("runtime handle %q survived the delete: %v", name, err)
		}
	}
	expected := map[string]bool{}
	for name := range planted {
		if handles[name] {
			continue
		}
		expected[name] = true
	}
	extAssertZeroStemResidue(t, dir, []string{strings.TrimSuffix(filepath.Base(path), ".jsonl")}, handles, filepath.Join(dir, sessionTrashDir), expected)
}

// TestTerminalArchiveTrashLeavesZeroSidecarResidue 验收钉（归档）：终端归档的
// 无守卫搬桶链同样全集清零、兜底 bak 进桶。
func TestTerminalArchiveTrashLeavesZeroSidecarResidue(t *testing.T) {
	dir, path := newExtSessionFixture(t, "20260923-045044.233465200-collab")
	planted := extPlantFullFamily(t, dir, path)
	planted[filepath.Base(path)] = true
	handles := extRuntimeHandleNames(path)

	if err := trashSessionArtifactsTerminal(dir, path, filepath.Base(path)); err != nil {
		t.Fatalf("trashSessionArtifactsTerminal: %v", err)
	}
	expected := map[string]bool{}
	for name := range planted {
		if handles[name] {
			continue
		}
		expected[name] = true
	}
	extAssertZeroStemResidue(t, dir, []string{strings.TrimSuffix(filepath.Base(path), ".jsonl")}, handles, filepath.Join(dir, sessionTrashDir), expected)
}

// TestClearSessionTeardownLeavesZeroSidecarResidue 验收钉（/clear 硬删链）：
// 无桶形态，全集直删 + 硬兜底，stem 前缀零残留。
func TestClearSessionTeardownLeavesZeroSidecarResidue(t *testing.T) {
	dir, path := newExtSessionFixture(t, "20260927-020815.856309600-collab-clear")
	extPlantFullFamily(t, dir, path)
	handles := extRuntimeHandleNames(path)

	if err := removeDesktopSessionArtifacts(path); err != nil {
		t.Fatalf("removeDesktopSessionArtifacts: %v", err)
	}
	extAssertZeroStemResidue(t, dir, []string{strings.TrimSuffix(filepath.Base(path), ".jsonl")}, handles, "", nil)
}

// TestPurgeTrashedSessionClearsSidecarsInBucket 验收钉（清空回收站）：删除把
// 全集搬进桶后，清空（purge）整桶移除——桶内侧车同清。
func TestPurgeTrashedSessionClearsSidecarsInBucket(t *testing.T) {
	dir, path := newExtSessionFixture(t, "20260923-045044.233465200-collab-purge")
	extPlantFullFamily(t, dir, path)
	if err := deleteSessionFile(dir, path); err != nil {
		t.Fatalf("deleteSessionFile: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(path), filepath.Base(path))
	if err := purgeTrashedSessionFile(dir, trashPath); err != nil {
		t.Fatalf("purgeTrashedSessionFile: %v", err)
	}
	buckets, err := os.ReadDir(filepath.Join(dir, sessionTrashDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		entries, err := os.ReadDir(filepath.Join(dir, sessionTrashDir, bucket.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("empty-trash left %d entries in the bucket: %v", len(entries), names)
		}
	}
}

// TestRestoreRoundTripsSweptStragglers 回程钉：兜底清扫进桶的条目随还原回到
// 目标 stem——清扫的零残留承诺不吞还原会话的字节。
func TestRestoreRoundTripsSweptStragglers(t *testing.T) {
	dir, path := newExtSessionFixture(t, "20261102-021909.744449100-collab")
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	sweptNames := []string{
		stem + ".jsonl.bak-20261104-cas",
		stem + ".pinned-context.json.bak-20261103-2340",
	}
	for _, name := range sweptNames {
		extPlantFile(t, dir, name)
	}
	if err := deleteSessionFile(dir, path); err != nil {
		t.Fatalf("deleteSessionFile: %v", err)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, filepath.Base(path), filepath.Base(path))
	if err := restoreTrashedSessionFile(dir, trashPath); err != nil {
		t.Fatalf("restoreTrashedSessionFile: %v", err)
	}
	for _, name := range sweptNames {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("swept straggler %q not restored: %v", name, err)
		}
		if string(got) != "{}\n" {
			t.Fatalf("restored %q = %q", name, got)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transcript not restored: %v", err)
	}
}

// TestSessionTrashArtifactsDeriveFromStoreInventory 清单单一来源钉：desktop
// 清单（除转录本体）与 store.SessionTrashSidecars 一一对应——src 逐条相等，
// 桶名是 store 条目按 key stem 的剥换。revert 回手写清单（漏 store 条目或多
// 出 store 没有的条目）本钉即红。
func TestSessionTrashArtifactsDeriveFromStoreInventory(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "20260923-045044.233465200-collab.jsonl")
	stem := strings.TrimSuffix(sessionPath, ".jsonl")
	for _, stamp := range []string{"20261102T025757", "20261105T052131"} {
		if err := os.MkdirAll(stem+".pre-repair-"+stamp, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Base(sessionPath)
	artifacts := sessionTrashArtifacts(sessionPath, key)
	if artifacts[0].src != sessionPath || artifacts[0].name != key {
		t.Fatalf("transcript entry = %+v, want {src: sessionPath, name: key}", artifacts[0])
	}
	derived := artifacts[1:]
	storeList := store.SessionTrashSidecars(sessionPath)
	if len(derived) != len(storeList) {
		t.Fatalf("desktop list (%d entries) drifted from the store inventory (%d entries)", len(derived), len(storeList))
	}
	sessionStem := strings.TrimSuffix(filepath.Base(sessionPath), store.SessionTranscriptSuffix)
	keyStem := strings.TrimSuffix(key, store.SessionTranscriptSuffix)
	for i, src := range storeList {
		if derived[i].src != src {
			t.Fatalf("entry %d src = %q, want %q", i, derived[i].src, src)
		}
		wantName := filepath.Base(src)
		if sessionStem != keyStem && strings.HasPrefix(wantName, sessionStem) {
			wantName = keyStem + strings.TrimPrefix(wantName, sessionStem)
		}
		if derived[i].name != wantName {
			t.Fatalf("entry %d bucket name = %q, want %q", i, derived[i].name, wantName)
		}
	}
}

// TestSessionTrashArtifactsBucketsUnderOriginalKey 解耦语义钉：改名后的目标
// （sessionPath 的 stem ≠ key 的 stem）进桶时桶名跟随 key 的 stem——既有
// 「key 是原名」契约不被派生改造破坏。
func TestSessionTrashArtifactsBucketsUnderOriginalKey(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "renamed-live.jsonl")
	key := "20260923-045044.233465200-collab.jsonl"
	artifacts := sessionTrashArtifacts(sessionPath, key)
	telemetry := store.SessionTelemetry(sessionPath)
	found := false
	for _, artifact := range artifacts {
		if artifact.src == telemetry {
			found = true
			if artifact.name != "20260923-045044.233465200-collab.jsonl.telemetry.json" {
				t.Fatalf("renamed-target bucket name = %q, want the key-stem form", artifact.name)
			}
		}
	}
	if !found {
		t.Fatalf("telemetry sidecar missing from the derived list")
	}
	if !slices.ContainsFunc(artifacts, func(a sessionTrashArtifact) bool { return a.src == store.SessionToolStats(sessionPath) }) {
		t.Fatalf("toolstats sidecar missing from the derived list")
	}
}
