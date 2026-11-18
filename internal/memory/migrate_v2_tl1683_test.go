package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── tl1683 ②：MigrateV2 的读量与持锁路径回归钉 ────────────────────────────
//
// 归因（docs/reasonix/report/tl1683-boot热路径三期恶化归因-20261118.md §4.2）：
// memory_migrate 段自身工作量只有 295ms（热）/1240ms（冷），P95 却是
// 21～29s——差 22～93 倍。原因是该段在进程级全局互斥 memoryStoreMutationMu
// 里做全量扫描，而同一把锁被记忆写入持有（写入要全量重写 MEMORY.md）。
// 两处修复：
//
//	① 只读文件头判定是否需迁移（不每次 boot 读全部 .md）；
//	② 分类阶段不持锁，只有真正要改写的文件才进锁内。
//
// 锁的语义本身不变（non-goal）：写阶段照旧在锁内，分类阶段照旧不写盘。

// TestMigrateV2HeadClassificationMatchesFullRead1683：头探测的结论必须与
// 「全量读」的结论逐条一致——分类口径没变，只是读少了。
func TestMigrateV2HeadClassificationMatchesFullRead1683(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		body string
	}{
		{"migrated", "---\nid: abc\nrevision: 3\nname: x\n---\n\nbody\n"},
		{"legacy-no-frontmatter", "just a body\n"},
		{"frontmatter-without-id", "---\nrevision: 2\nname: x\n---\n\nbody\n"},
		{"frontmatter-without-revision", "---\nid: abc\nname: x\n---\n\nbody\n"},
		{"frontmatter-zero-revision", "---\nid: abc\nrevision: 0\nname: x\n---\n\nbody\n"},
		{"crlf", "---\r\nid: abc\r\nrevision: 3\r\n---\r\n\r\nbody\r\n"},
		{"unclosed-fence", "---\nid: abc\nrevision: 3\nbody without closing fence\n"},
		{"empty", ""},
		{"body-only-after-blank", "\n\n---\nnot frontmatter\n"},
	}
	for _, tc := range cases {
		path := filepath.Join(dir, tc.name+".md")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatalf("%s: write: %v", tc.name, err)
		}
		want := memoryNeedsMigration(tc.body)
		needs, complete, err := needsMigration(path)
		if err != nil {
			t.Fatalf("%s: needsMigration: %v", tc.name, err)
		}
		if !complete {
			t.Fatalf("%s: 头探测应当能定案（complete=false 会退回全量读）", tc.name)
		}
		if needs != want {
			t.Fatalf("%s: 头探测结论 = %v，全量读结论 = %v，两者必须一致", tc.name, needs, want)
		}
	}
}

// TestMigrateV2HeadFallsBackWhenFrontmatterExceedsHead1683：前置块比头还长时
// 头探测判不了案，必须如实报告「不可判」并退回全量读——不能拿截断的前置块
// 得出「需要迁移」的错结论（那会把已迁移文件重写一遍）。
func TestMigrateV2HeadFallsBackWhenFrontmatterExceedsHead1683(t *testing.T) {
	dir := t.TempDir()
	long := "---\nid: abc\nkeywords: " + strings.Repeat("k", migrateHeaderBytes+64) +
		"\nrevision: 3\n---\n\nbody\n"
	path := filepath.Join(dir, "long.md")
	if err := os.WriteFile(path, []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	needs, complete, err := needsMigration(path)
	if err != nil {
		t.Fatalf("needsMigration: %v", err)
	}
	if complete {
		t.Fatalf("前置块超过 %d 字节的头：必须报告 complete=false（退回全量读）", migrateHeaderBytes)
	}
	if needs {
		t.Fatal("complete=false 时结论不应有意义（调用方退回全量读）")
	}
	// 退回全量读后的最终结论仍是「已迁移」。
	if memoryNeedsMigration(long) {
		t.Fatal("全量读结论应为「已迁移」")
	}
}

// TestMigrateV2StillMigratesLegacyFiles1683：分类口径没变——legacy 文件照样
// 被迁移（带 .bak 备份），已迁移的文件一个字节都不动。
func TestMigrateV2StillMigratesLegacyFiles1683(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.md")
	if err := os.WriteFile(legacy, []byte("a plain legacy fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	migrated := filepath.Join(dir, "already.md")
	before := "---\nid: abc\nrevision: 3\nname: already\n---\n\nbody\n"
	if err := os.WriteFile(migrated, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	store := Store{Dir: dir}
	report, err := store.MigrateV2()
	if err != nil {
		t.Fatalf("MigrateV2: %v", err)
	}
	if report.Migrated != 1 {
		t.Fatalf("Migrated = %d（应为 1：只迁 legacy 那条）", report.Migrated)
	}
	raw, err := os.ReadFile(migrated)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != before {
		t.Fatalf("已迁移文件被改写了：\n%q", string(raw))
	}
	if _, err := os.Stat(legacy + ".bak"); err != nil {
		t.Fatalf("legacy 文件应留下 .bak 备份：%v", err)
	}
	got, ok := loadMemory(legacy)
	if !ok {
		t.Fatal("迁移后应能读回")
	}
	if got.Revision < 1 {
		t.Fatalf("迁移后 revision = %d（应 ≥1）", got.Revision)
	}
}

// TestMigrateV2ClassificationDoesNotHoldGlobalLock1683：分类阶段不得持全局
// 互斥锁。这正是 21～29s 尾巴的机制（归因报告 §4.2）：boot 的扫描排在一次
// 记忆写入（持锁全量重写 MEMORY.md）后面，于是整段等锁。
//
// 断言方式：测试自己占住 memoryStoreMutationMu，MigrateV2 仍必须返回
// （无事可迁 ⇒ 只需分类，不进锁）。
func TestMigrateV2ClassificationDoesNotHoldGlobalLock1683(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.md"),
		[]byte("---\nid: a\nrevision: 1\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := Store{Dir: dir}

	locked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	releaseLock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseLock)
	go func() {
		memoryStoreMutationMu.Lock()
		close(locked)
		<-release
		memoryStoreMutationMu.Unlock()
	}()
	<-locked

	done := make(chan error, 1)
	go func() {
		_, err := store.MigrateV2()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("MigrateV2: %v", err)
		}
	case <-time.After(15 * time.Second):
		releaseLock()
		t.Fatal("全局锁被持有时 MigrateV2 仍未返回——分类阶段不该持锁（尾巴的机制未除）")
	}
	releaseLock()
}

// TestMigrateV2WritePhaseStillTakesGlobalLock1683：写阶段照旧在锁内——锁的
// 语义是 non-goal，本件只缩短持锁路径，不改锁本身。
func TestMigrateV2WritePhaseStillTakesGlobalLock1683(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "legacy.md"),
		[]byte("a plain legacy fact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := Store{Dir: dir}

	locked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	releaseLock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseLock)
	go func() {
		memoryStoreMutationMu.Lock()
		close(locked)
		<-release
		memoryStoreMutationMu.Unlock()
	}()
	<-locked

	done := make(chan MigrationReport, 1)
	go func() {
		report, _ := store.MigrateV2()
		done <- report
	}()
	select {
	case <-done:
		t.Fatal("迁移写阶段未取全局锁——锁语义被改掉了（non-goal）")
	case <-time.After(500 * time.Millisecond):
		// 正确：持锁期间迁移停在锁上。
	}
	releaseLock()
	select {
	case report := <-done:
		if report.Migrated != 1 {
			t.Fatalf("Migrated = %d（应为 1）", report.Migrated)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("释放锁后迁移仍未完成")
	}
}

// BenchmarkMigrateV2Scan1683：分类阶段的读量收益（tl1683 ② 的量化读数）。
//
// 真机形态：881 个 .md / 2.1MB ⇒ 单文件均值约 2.4KB。这里按同样的形态造
// 库，对比「只读文件头」（新）与「全量读全文」（旧）的耗时。
func BenchmarkMigrateV2Scan1683(b *testing.B) {
	const fileCount = 881
	const bodySize = 2400
	body := strings.Repeat("x", bodySize)
	front := "---\nid: abc\nrevision: 3\nname: x\ndescription: d\n---\n\n"
	content := front + body + "\n"

	dir := b.TempDir()
	for i := range fileCount {
		name := filepath.Join(dir, "fact"+strings.Repeat("0", 4-len(itoa(i)))+itoa(i)+".md")
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	store := Store{Dir: dir}

	b.Run("head-only", func(b *testing.B) {
		for b.Loop() {
			if _, err := store.migrationCandidates(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full-read-baseline", func(b *testing.B) {
		for b.Loop() {
			entries, err := os.ReadDir(dir)
			if err != nil {
				b.Fatal(err)
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err != nil {
					b.Fatal(err)
				}
				_ = memoryNeedsMigration(string(raw))
			}
		}
	})
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
