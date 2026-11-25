package sidecarsweep

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// ── 夹具 ──────────────────────────────────────────────────────────────────

const (
	masterStem  = "20261101-010101.100000000-testmodel"
	masterName  = masterStem + ".jsonl"
	guardStem   = masterStem
	guardName   = guardStem + ".guardian.jsonl"
	otherStem   = "20261102-020202.200000000-othermodel"
	otherName   = otherStem + ".jsonl"
	dateForTest = "20261125"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDir(t *testing.T, path string, files int) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range files {
		writeFile(t, filepath.Join(path, "blob-"+string(rune('a'+i))+".bin"), "x")
	}
}

// materializeFamily 按 store.SessionStaticTrashSidecars 的构造函数全集，把
// master+guardian 家族的每个工件真实落盘（红-guard 测试的数据面）。
func materializeFamily(t *testing.T, dir, stem string) {
	t.Helper()
	sessionPath := filepath.Join(dir, stem+".jsonl")
	writeFile(t, sessionPath, "transcript")
	writeFile(t, guardPath(sessionPath), "guardian")
	// SessionSidecarFiles(session) ∪ SessionSidecarFiles(guardian) ∪
	// telemetry/toolstats/ckpt/jobs/inbox/reminders/imgpack/guardian+cursor
	for _, p := range store.SessionStaticTrashSidecars(sessionPath) {
		if p == "" {
			continue
		}
		if isDirArtifactPath(p) {
			writeDir(t, p, 1)
		} else {
			writeFile(t, p, "sidecar")
		}
	}
	// pre-repair 备份目录（glob 形态）。
	writeDir(t, filepath.Join(dir, stem+".pre-repair-20261101T000000"), 2)
	// 外部修复副本（含前缀形态）。
	writeFile(t, filepath.Join(dir, stem+".jsonl.bak-20261101-030000-selfheal"), "bak")
	// 运行时句柄（不在 trash 清单内，但字段里真实存在）。
	writeFile(t, store.SessionLockFile(sessionPath), "lock")
	writeFile(t, store.SessionLeaseLock(sessionPath), "lease")
	writeFile(t, store.SessionLeaseInfo(sessionPath), "info")
}

func guardPath(sessionPath string) string {
	return strings.TrimSuffix(sessionPath, ".jsonl") + ".guardian.jsonl"
}

// isDirArtifactPath 按目录工件后缀判定（测试夹具用；与形态表目录行同口径）。
func isDirArtifactPath(p string) bool {
	for _, suffix := range []string{".ckpt", ".jobs", ".inbox", ".reminders", ".imgpack"} {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return strings.Contains(filepath.Base(p), ".pre-repair-")
}

// snapshotTree 遍历目录树做快照（相对路径 + 内容字节 + 目录结构），用于
// 「缺 --confirm 零写盘」的前后比对。
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			snap[rel+"/"] = "dir"
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		snap[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func assertSnapEqual(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("盘面被写入：条目数 %d → %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("盘面被写入：%s 内容变化", k)
		}
	}
}

func findRow(rows []Row, name string) (Row, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return Row{}, false
}

// ── 形态表 vs store 构造函数：全覆盖红-guard ──────────────────────────────

// TestShapeTableCoversStoreConstructors 钉死「store 每个 sidecar 构造函数的
// 产物都被形态表认识」：活 master+guardian 家族按构造函数全集落盘后，扫描
// 必须零孤儿、零不明——store 新增构造函数而不进形态表 = 本测试红（上限+新增
// 即红）。
func TestShapeTableCoversStoreConstructors(t *testing.T) {
	dir := t.TempDir()
	materializeFamily(t, dir, masterStem)
	writeFile(t, filepath.Join(dir, otherName), "other")
	// other 会话只给一个 events 日志（活主成员）。
	writeFile(t, filepath.Join(dir, otherStem+".events.jsonl"), "events")

	dr, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Orphans) != 0 {
		for _, r := range dr.Orphans {
			t.Errorf("误报孤儿: %s (%s) 判据=%s", r.Name, r.Kind, r.Evidence)
		}
	}
	if len(dr.Unknown) != 0 {
		for _, r := range dr.Unknown {
			t.Errorf("形态表漏接（落入不明）: %s", r.Name)
		}
	}
	// masters = 3 条主转录形态：testmodel 的 plain + zst 档案（同 stem 双形态）
	// + other 的 plain。
	if dr.Masters != 3 {
		t.Errorf("masters = %d, want 3", dr.Masters)
	}
}

// ── 只读钉：缺 --confirm 零写盘 ──────────────────────────────────────────

// TestScanIsReadOnly 钉验收1的后半句：扫描前后目录树字节级一致（含内容），
// 缺省模式绝不落盘。
func TestScanIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	materializeFamily(t, dir, masterStem)
	// 造孤儿：把 master 与 guardian 搬走，留全套 sidecar。
	for _, name := range []string{masterName, guardName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, ".titles.json"), `{"`+masterName+`":"旧标题"}`)

	before := snapshotTree(t, dir)
	if _, err := ScanDir(dir); err != nil {
		t.Fatal(err)
	}
	assertSnapEqual(t, dir, before)
}

// ── 孤儿形态逐类判定 ─────────────────────────────────────────────────────

// TestOrphanFormsDetected 逐形态核对判定结果：无主 sidecar（数据面）、无主
// guardian 全家、guardian 档案、外部修复副本、pre-repair 目录、无主运行时
// 句柄、不明条目。孤儿判据逐条注明（Evidence 非空且含主转录缺席事实）。
func TestOrphanFormsDetected(t *testing.T) {
	dir := t.TempDir()
	// 活会话：other 及其 meta（不被误伤）。
	writeFile(t, filepath.Join(dir, otherName), "other")
	writeFile(t, filepath.Join(dir, otherStem+".jsonl.meta"), "meta-of-other")
	// 孤儿家族：master+guardian 已走删除链，sidecar 全套残留（tl1793 §二.4 同款）。
	for _, name := range []string{
		masterName + ".meta",
		masterStem + ".events.jsonl",
		masterStem + ".turns.jsonl",
		masterStem + ".display-index.json",
		masterStem + ".goal-state.json",
		masterName + ".telemetry.json",
		masterName + ".toolstats.json",
		masterStem + ".ckpt",
		masterStem + ".inbox",
		guardName,                            // 无主 guardian 本体
		guardName + ".meta",                  // guardian 的 meta
		guardStem + ".guardian.cursor.json",  // guardian 游标
		guardStem + ".guardian.events.jsonl", // guardian 事件日志
		guardName + ".zst",                   // guardian 档案
		masterStem + ".jsonl.bak-20261101-030000-selfheal",
		masterStem + ".pre-repair-20261101T000000",
		masterName + ".lock",
		masterName + ".lease.json",
	} {
		if strings.HasSuffix(name, ".ckpt") || strings.HasSuffix(name, ".inbox") ||
			strings.HasSuffix(name, ".pre-repair-20261101T000000") {
			writeDir(t, filepath.Join(dir, name), 1)
		} else {
			writeFile(t, filepath.Join(dir, name), "residue")
		}
	}
	// 不明条目：形态表外，只复核不搬。
	writeFile(t, filepath.Join(dir, "mystery-file.xyz"), "???")

	dr, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dr.Masters != 1 {
		t.Fatalf("masters = %d, want 1", dr.Masters)
	}
	// 活会话的 meta 绝不入孤儿。
	if _, hit := findRow(dr.Orphans, otherStem+".jsonl.meta"); hit {
		t.Fatal("活会话 meta 被误判孤儿")
	}
	// 每个孤儿行判据齐备。
	for _, r := range append(append([]Row{}, dr.Orphans...), dr.Deferred...) {
		if r.Evidence == "" || !strings.Contains(r.Evidence, "不在活目录") {
			t.Errorf("%s: 判据未注明主转录缺席: %q", r.Name, r.Evidence)
		}
		if r.MasterStem == "" {
			t.Errorf("%s: master stem 为空", r.Name)
		}
	}
	// 数据面命中抽查。
	for _, name := range []string{
		masterName + ".meta", masterStem + ".events.jsonl", guardName,
		guardStem + ".guardian.cursor.json", guardName + ".zst",
		masterStem + ".jsonl.bak-20261101-030000-selfheal",
		masterStem + ".pre-repair-20261101T000000",
	} {
		r, hit := findRow(dr.Orphans, name)
		if !hit {
			t.Errorf("漏判孤儿: %s", name)
			continue
		}
		if r.Action != "recycle" {
			t.Errorf("%s: action = %s, want recycle", name, r.Action)
		}
	}
	// 运行时句柄：孤儿但默认不搬。
	for _, name := range []string{masterName + ".lock", masterName + ".lease.json"} {
		r, hit := findRow(dr.Orphans, name)
		if !hit {
			t.Errorf("漏判孤儿: %s", name)
			continue
		}
		if r.Action != "runtime" || r.Class != "runtime" {
			t.Errorf("%s: action/class = %s/%s, want runtime", name, r.Action, r.Class)
		}
	}
	// 不明条目：review-only。
	r, hit := findRow(dr.Unknown, "mystery-file.xyz")
	if !hit || r.Action != "review-only" {
		t.Errorf("不明条目应 review-only: hit=%v action=%v", hit, r.Action)
	}
	// 空壳计数为零（本夹具无空主转录）。
	if dr.EmptyMasters != 0 {
		t.Errorf("empty masters = %d, want 0", dr.EmptyMasters)
	}
}

// TestMasterInTrashEvidence 注明「master 躺在 .trash 桶」证据（两代桶名）。
func TestMasterInTrashEvidence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, masterName+".meta"), "residue")
	trash := filepath.Join(dir, ".trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	// 现行代桶名：<stem>.jsonl-deleted-<nano>-<n>
	if err := os.MkdirAll(filepath.Join(trash, masterName+"-deleted-1700000000000-00"), 0o755); err != nil {
		t.Fatal(err)
	}
	dr, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, hit := findRow(dr.Orphans, masterName+".meta")
	if !hit || r.MasterInTrash == "" {
		t.Fatalf("应注明桶证据: hit=%v trash=%q", hit, r.MasterInTrash)
	}
	// 旧代桶名：== <stem>.jsonl
	if err := os.RemoveAll(filepath.Join(trash, masterName+"-deleted-1700000000000-00")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(trash, masterName), 0o755); err != nil {
		t.Fatal(err)
	}
	dr, err = ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, hit = findRow(dr.Orphans, masterName+".meta")
	if !hit || r.MasterInTrash != masterName {
		t.Fatalf("旧代桶名证据缺失: hit=%v trash=%q", hit, r.MasterInTrash)
	}
}

// TestEmptyMasterInfoOnly 空壳主转录（0 字节）只计数提示，其 sidecar 属活家族
// 不判孤儿（空壳处置权在桌面端/用户，本包不越界）。
func TestEmptyMasterInfoOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, masterName), "")
	writeFile(t, filepath.Join(dir, masterName+".meta"), "meta")
	dr, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dr.EmptyMasters != 1 {
		t.Fatalf("empty masters = %d, want 1", dr.EmptyMasters)
	}
	if len(dr.Orphans) != 0 {
		t.Fatalf("空壳的 sidecar 不应判孤儿: %+v", dr.Orphans)
	}
}

// TestCleanupPendingDefersFamily cleanup-pending 标记所在 stem 整族 defer
// （桌面端补收机制持有，本包让位）。
func TestCleanupPendingDefersFamily(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, masterName+".meta"), "residue")
	writeFile(t, filepath.Join(dir, masterStem+".events.jsonl"), "residue")
	// 标记真名是 <stem>.cleanup-pending.json（stem 拼写，store.SessionCleanupPending）。
	writeFile(t, filepath.Join(dir, masterStem+".cleanup-pending.json"), "{}")
	dr, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Orphans) != 0 {
		t.Fatalf("defer 面不得出现在孤儿清单: %+v", dr.Orphans)
	}
	if len(dr.Deferred) != 2 {
		t.Fatalf("deferred = %d, want 2", len(dr.Deferred))
	}
	for _, r := range dr.Deferred {
		if !strings.Contains(r.Evidence, "cleanup-pending") {
			t.Errorf("%s: defer 判据未注明机制持有", r.Name)
		}
	}
}

// TestTitlesDanglingDetected 悬空指针形态：.titles.json 键指向不存在的会话。
func TestTitlesDanglingDetected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, otherName), "live")
	titles := map[string]string{otherName: "活会话", masterName: "已删会话", "not-a-session.jsonl": "悬空"}
	b, _ := json.Marshal(titles)
	writeFile(t, filepath.Join(dir, ".titles.json"), string(b))
	dr, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dr.TitlesDangling)
	want := []string{masterName, "not-a-session.jsonl"}
	if strings.Join(dr.TitlesDangling, "|") != strings.Join(want, "|") {
		t.Fatalf("dangling = %v, want %v", dr.TitlesDangling, want)
	}
}

// TestTrashLooseFiles .trash 顶层散文件仅提示（desktop 领地，不入搬运面）。
func TestTrashLooseFiles(t *testing.T) {
	dir := t.TempDir()
	trash := filepath.Join(dir, ".trash")
	writeDir(t, filepath.Join(trash, "20261101-010101.100000000-testmodel.jsonl"), 1)
	writeFile(t, filepath.Join(trash, "loose-sidecar.jsonl.meta"), "stray")
	dr, err := ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.TrashLoose) != 1 || dr.TrashLoose[0] != "loose-sidecar.jsonl.meta" {
		t.Fatalf("trash loose = %v", dr.TrashLoose)
	}
}
