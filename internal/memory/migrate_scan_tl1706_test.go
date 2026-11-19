package memory

// ── tl1706：MigrateV2 分类相的持久化 per-file verdict（scan marker）────────
//
// 归因（docs/reasonix/report/tl1706-清理后首启慢归因-20261120.md）：分类相
// 对全部事实文件逐个 open+read 头，热页缓存 ~130ms，冷页缓存 14–19s（940 次
// 小文件 open × ~20ms NTFS 冷访问）。「清理/索引失效后首启」恰好落在冷缓存
// 窗口，用户感知为清理后第一次启动显著变慢（真机读数 18718/18817ms）。
//
// 修复：持久化每文件的 (size, mtimeMs) 判定结论（与 recall FTS reconcile 的
// 同一信任模型——原子替换写路径必 bump mtime，stat 未变 ⇒ 内容未变）。
// stat 未变的文件沿用「已迁移」结论不再 open；stat 变了的文件（新增/改写/
// 外部导入 legacy）重新判定。以下钉住这条信任边界：
//
//	① marker 命中时不再读文件内容（冷缓存不再付 15–19s）；
//	② store 真变了（外部导入 legacy / 改写已迁移文件）必须重判并迁移；
//	③ marker 版本不符退回全量扫（旧格式误读防线）；
//	④ 迁移失败不写 marker（下次 boot 全量重扫 = pre-tl1706 行为）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func tl1706V2File(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte("---\nid: t1706-"+name+"\nrevision: 2\nname: "+name+"\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMigrateV2ScanMarkerCarriesVerdicts1706：同一 store 连续两次 MigrateV2，
// 第二次必须走 marker 短路——marker 存在且与本次扫描结论一致。
func TestMigrateV2ScanMarkerCarriesVerdicts1706(t *testing.T) {
	dir := t.TempDir()
	tl1706V2File(t, dir, "alpha")
	tl1706V2File(t, dir, "beta")
	store := Store{Dir: dir}

	if _, err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	markerPath := migrateScanMarkerPath(store)
	first, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("首次扫描应写入 marker：%v", err)
	}
	var marker migrateScanMarker
	if err := json.Unmarshal(first, &marker); err != nil {
		t.Fatalf("marker 应是合法 JSON：%v", err)
	}
	if marker.Version != migrateScanMarkerVersion || len(marker.Files) != 2 {
		t.Fatalf("marker = %+v（应 version=%d 且登记 2 个文件）", marker, migrateScanMarkerVersion)
	}

	if _, err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("store 未变时第二次扫描不应改写 marker")
	}
}

// TestMigrateV2ScanMarkerDetectsLegacyImport1706：marker 命中后外部导入的
// legacy 文件（新文件，不在 marker 清单里）仍必须被迁移——信任边界的主钉。
func TestMigrateV2ScanMarkerDetectsLegacyImport1706(t *testing.T) {
	dir := t.TempDir()
	tl1706V2File(t, dir, "alpha")
	store := Store{Dir: dir}
	if _, err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}

	// 外部导入一个 legacy 文件。
	if err := os.WriteFile(filepath.Join(dir, "imported.md"), []byte("a plain legacy fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := store.MigrateV2()
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 1 {
		t.Fatalf("Migrated = %d（marker 命中后外部导入的 legacy 必须仍被迁移）", report.Migrated)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.md.bak")); err != nil {
		t.Fatalf("导入的 legacy 应留下 .bak：%v", err)
	}
}

// TestMigrateV2ScanMarkerDetectsRewrittenFile1706：已迁移文件被外部改写回
// legacy 形态（size/mtime 变）——重判必须发生，文件必须被重迁。
func TestMigrateV2ScanMarkerDetectsRewrittenFile1706(t *testing.T) {
	dir := t.TempDir()
	path := tl1706V2File(t, dir, "alpha")
	store := Store{Dir: dir}
	if _, err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("overwritten back to legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := store.MigrateV2()
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 1 {
		t.Fatalf("Migrated = %d（被改写回 legacy 的文件必须重迁）", report.Migrated)
	}
}

// TestMigrateV2ScanMarkerVersionMismatchFallsBack1706：版本不符的 marker 必须
// 被当作不存在——退回全量扫描（pre-tl1706 行为），绝不误读旧格式。
func TestMigrateV2ScanMarkerVersionMismatchFallsBack1706(t *testing.T) {
	dir := t.TempDir()
	tl1706V2File(t, dir, "alpha")
	store := Store{Dir: dir}
	if err := os.MkdirAll(filepath.Join(dir, ".index"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign, err := json.Marshal(migrateScanMarker{Version: 999, Files: map[string]string{"*": "*"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(migrateScanMarkerPath(store), foreign, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "legacy.md"), []byte("a plain legacy fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := store.MigrateV2()
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 1 {
		t.Fatalf("Migrated = %d（版本不符的 marker 必须退回全量扫描）", report.Migrated)
	}
}

// TestMigrateV2ScanMarkerNotWrittenWhenMigrationFails1706：迁移写相失败
// （backup 无法落盘）时不得写 marker——下次 boot 必须全量重扫。
func TestMigrateV2ScanMarkerNotWrittenWhenMigrationFails1706(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-fact.md")
	legacy := "---\nname: legacy-fact\ndescription: old file\nmetadata:\n  type: project\n---\n\nlegacy body\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	// 占住 backup 路径使迁移失败。
	if err := os.Mkdir(path+".bak", 0o755); err != nil {
		t.Fatal(err)
	}
	store := Store{Dir: dir}
	if _, err := store.MigrateV2(); err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("migration error = %v, want backup failure", err)
	}
	if _, err := os.Stat(migrateScanMarkerPath(store)); !os.IsNotExist(err) {
		t.Fatalf("迁移失败后 marker 不应存在：stat err = %v", err)
	}
}

// TestMigrateV2ScanMarkerDeletedFilesDropOut1706：marker 清单里已删除的文件
// 不应产生候选或残留——删除走「files 没有 → 忽略」，marker 重写后自然收敛。
func TestMigrateV2ScanMarkerDeletedFilesDropOut1706(t *testing.T) {
	dir := t.TempDir()
	tl1706V2File(t, dir, "alpha")
	tl1706V2File(t, dir, "gone")
	store := Store{Dir: dir}
	if _, err := store.MigrateV2(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	report, err := store.MigrateV2()
	if err != nil {
		t.Fatal(err)
	}
	if report.Migrated != 0 {
		t.Fatalf("Migrated = %d（删除不应触发迁移）", report.Migrated)
	}
	marker, err := os.ReadFile(migrateScanMarkerPath(store))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(marker), "gone.md") {
		t.Fatalf("重写后的 marker 不应再登记已删除文件：\n%s", marker)
	}
}

// TestMigrateV2ScanMarkerConcurrentBoots1706：两个 boot 并发跑 MigrateV2，
// 两者都必须成功返回，最终文件全部 v2、marker 存在——last-writer-wins 的
// marker 写只允许浪费一次重判，不允许错判。
func TestMigrateV2ScanMarkerConcurrentBoots1706(t *testing.T) {
	dir := t.TempDir()
	tl1706V2File(t, dir, "alpha")
	tl1706V2File(t, dir, "beta")
	store := Store{Dir: dir}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			_, errs[slot] = store.MigrateV2()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发 boot #%d: %v", i, err)
		}
	}
	if _, err := os.Stat(migrateScanMarkerPath(store)); err != nil {
		t.Fatalf("并发扫描后 marker 应存在：%v", err)
	}
}
