package sidecarsweep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 11, 25, 10, 30, 0, 0, time.Local) }

// recycleFixture 造一个「master 已走删除链、sidecar 全套残留」的目录，返回
// 目录路径。孤儿面 = 数据面 4 件 + runtime 1 件。
func recycleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{
		masterName + ".meta",
		masterStem + ".events.jsonl",
		masterStem + ".display-index.json",
		masterName + ".telemetry.json",
		masterName + ".lock",
	} {
		writeFile(t, filepath.Join(dir, name), "residue")
	}
	return dir
}

// TestRecycleMovesIntoRecycleDirWithManifest 回收语义演练（验收2）：--confirm
// 后数据面孤儿全部移入 <dir>/_recycle-YYYYMMDD/（原名保留），manifest.tsv
// 逐行可还原；runtime 句柄未放开时原地不动；源目录不再有孤儿。
func TestRecycleMovesIntoRecycleDirWithManifest(t *testing.T) {
	dir := recycleFixture(t)
	rep, err := ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	rc := Recycle(rep, RecycleOptions{Now: fixedNow})
	if rc.Failed != 0 {
		t.Fatalf("failed rows: %+v", rc.Rows)
	}
	recycleDir := filepath.Join(dir, "_recycle-"+dateForTest)
	for _, name := range []string{
		masterName + ".meta",
		masterStem + ".events.jsonl",
		masterStem + ".display-index.json",
		masterName + ".telemetry.json",
	} {
		if _, err := os.Stat(filepath.Join(recycleDir, name)); err != nil {
			t.Errorf("%s 未入回收区: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s 原位残留", name)
		}
	}
	// runtime 未放开：原地不动。
	if _, err := os.Stat(filepath.Join(dir, masterName+".lock")); err != nil {
		t.Errorf("runtime 句柄缺省不得搬: %v", err)
	}
	// manifest.tsv 记录 4 行搬运（还原依据）。
	b, err := os.ReadFile(filepath.Join(recycleDir, "manifest.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 {
		t.Fatalf("manifest 行数 = %d, want 4", len(lines))
	}
	for _, line := range lines {
		if !strings.Contains(line, filepath.Join(dir, "")) && !strings.Contains(line, dir+string(filepath.Separator)) {
			t.Errorf("manifest 行缺原路径: %q", line)
		}
	}
	// 回执口径。
	if rc.Moved != 4 || rc.Failed != 0 {
		t.Fatalf("moved=%d failed=%d, want 4/0", rc.Moved, rc.Failed)
	}
}

// TestRecycleNeverDeletes 回收语义铁律钉：Recycle 只允许 rename 进回收区与
// manifest 追加两种盘面动作——全树条目数只增不减（回收区+manifest），任何
// 条目的字节都不消失。
func TestRecycleNeverDeletes(t *testing.T) {
	dir := recycleFixture(t)
	before := snapshotTree(t, dir)
	rep, err := ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	Recycle(rep, RecycleOptions{Now: fixedNow, IncludeRuntime: true})
	after := snapshotTree(t, dir)
	// 原 5 条全部还在树上（只是换了父目录）+ 新增回收区目录 + manifest。
	if len(after) < len(before) {
		t.Fatalf("回收后条目数变少：before=%d after=%d（出现删除即红）", len(before), len(after))
	}
	for rel := range before {
		base := filepath.Base(rel)
		found := false
		for afterRel := range after {
			if filepath.Base(afterRel) == base && after[afterRel] == before[rel] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("条目字节丢失: %s", rel)
		}
	}
}

// TestRecycleSkipsRevivedMaster TOCTOU 防复活闸：扫描之后 master 被还原/重建
// （侧车回到活家族）时，搬运跳过该行，条目留在原地。
func TestRecycleSkipsRevivedMaster(t *testing.T) {
	dir := recycleFixture(t)
	rep, err := ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	// 扫描后、搬运前：master 复活。
	writeFile(t, filepath.Join(dir, masterName), "revived")
	rc := Recycle(rep, RecycleOptions{Now: fixedNow})
	if rc.Moved != 0 {
		t.Fatalf("复活后仍搬了 %d 行", rc.Moved)
	}
	for _, r := range rc.Rows {
		if r.Status != "skipped-revived" {
			t.Errorf("status = %s, want skipped-revived (%s)", r.Status, r.Name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, masterName+".meta")); err != nil {
		t.Errorf("侧车应留在原地: %v", err)
	}
}

// TestRecycleIncludeRuntime --include-runtime 放开后无主句柄随批入回收区。
func TestRecycleIncludeRuntime(t *testing.T) {
	dir := recycleFixture(t)
	rep, err := ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	rc := Recycle(rep, RecycleOptions{Now: fixedNow, IncludeRuntime: true})
	if rc.Moved != 5 || rc.Failed != 0 {
		t.Fatalf("moved=%d failed=%d, want 5/0", rc.Moved, rc.Failed)
	}
	if _, err := os.Stat(filepath.Join(dir, "_recycle-"+dateForTest, masterName+".lock")); err != nil {
		t.Errorf("runtime 句柄应入回收区: %v", err)
	}
}

// TestRecycleCollisionSuffix 回收区内同名碰撞加 -dup<N>，绝不覆盖。
func TestRecycleCollisionSuffix(t *testing.T) {
	dir := recycleFixture(t)
	target := filepath.Join(dir, "_recycle-"+dateForTest)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target, masterName+".meta"), "上一批已回收")
	rep, err := ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	rc := Recycle(rep, RecycleOptions{Now: fixedNow})
	if rc.Moved != 4 || rc.Failed != 0 {
		t.Fatalf("moved=%d failed=%d", rc.Moved, rc.Failed)
	}
	// 旧条目未被覆盖（字节不变），新条目落 -dup1。
	if b, _ := os.ReadFile(filepath.Join(target, masterName+".meta")); string(b) != "上一批已回收" {
		t.Fatal("回收区旧条目被覆盖")
	}
	if _, err := os.Stat(filepath.Join(target, masterName+".meta-dup1")); err != nil {
		t.Errorf("碰撞后缀未生效: %v", err)
	}
}

// TestRecycleTitlesWithBackup --include-titles：悬空键移除、原表字节级备份
// 进回收区、活键保留；缺旗时 titles 文件纹丝不动。
func TestRecycleTitlesWithBackup(t *testing.T) {
	newFixture := func(t *testing.T) string {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, otherName), "live")
		writeFile(t, filepath.Join(dir, ".titles.json"),
			`{"`+otherName+`":"活标题","`+masterName+`":"已删标题"}`)
		return dir
	}
	// 缺旗：只列不动。
	dir := newFixture(t)
	rep, err := ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	beforeTitles, _ := os.ReadFile(filepath.Join(dir, ".titles.json"))
	Recycle(rep, RecycleOptions{Now: fixedNow})
	afterTitles, _ := os.ReadFile(filepath.Join(dir, ".titles.json"))
	if string(beforeTitles) != string(afterTitles) {
		t.Fatal("缺 --include-titles 时 titles 被改写")
	}
	// 放开：移除悬空键 + 备份。
	dir = newFixture(t)
	rep, err = ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	rc := Recycle(rep, RecycleOptions{Now: fixedNow, IncludeTitles: true})
	if rc.Failed != 0 {
		t.Fatalf("titles rewrite failed: %+v", rc.Rows)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".titles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "已删标题") || !strings.Contains(string(b), "活标题") {
		t.Fatalf("悬空键清理结果不对: %s", b)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "_recycle-"+dateForTest))
	backupFound := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), titlesBackupPrefix) {
			backupFound = true
			bb, _ := os.ReadFile(filepath.Join(dir, "_recycle-"+dateForTest, e.Name()))
			if string(bb) != string(beforeTitles) {
				t.Fatal("备份与原表字节不一致")
			}
		}
	}
	if !backupFound {
		t.Fatal("原表未备份进回收区")
	}
}

// TestScanRootsTotals 汇总口径：data/runtime 分面计数与字节数。
func TestScanRootsTotals(t *testing.T) {
	dir := recycleFixture(t)
	rep, err := ScanRoots([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Totals.DataOrphans != 4 || rep.Totals.RuntimeOrphans != 1 {
		t.Fatalf("totals data/runtime = %d/%d, want 4/1",
			rep.Totals.DataOrphans, rep.Totals.RuntimeOrphans)
	}
	if rep.Totals.DataBytes <= 0 {
		t.Fatal("data bytes 应为正")
	}
	if rep.Mode != "scan" {
		t.Fatalf("缺省 mode = %s, want scan", rep.Mode)
	}
}
