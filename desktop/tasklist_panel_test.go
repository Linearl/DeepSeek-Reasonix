package main

// tl1746 phase 1 tests — 任务面板只读直读层。
//
// 红线两件套：
//   ① TestTasklistPanelReadOnly —— 经本层 DSN（mode=ro）打开的句柄执行写语句
//      必须失败（运行时红线）；
//   ② TestTasklistPanelNoWriteSQL —— tasklist_panel.go 源码静态扫描，禁止
//      写类 SQL 动词出现（静态红线；只扫 runtime 文件，本测试文件的
//      fixture 造库语句不在扫描面——fixture 写的是 t.TempDir() 一次性副本，
//      不触用户库）。
//
// 行为面：总览口径对 tl.py stats（total/pending/done/cancelled + 分布）、
// 列表筛选（状态/优先级大小写不敏感/领域/搜索 LIKE 转义/日期范围/limit）、
// 30s 缓存与 refresh 旁路、缺库友好错误、默认路径解析。

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// seedTasklistDB creates a throwaway tasklist db with a known task set
// (fixture-only writes; production path opens mode=ro exclusively).
func seedTasklistDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasklist.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()
	stmt := `CREATE TABLE IF NOT EXISTS tasks (
		num             INTEGER PRIMARY KEY,
		title           TEXT NOT NULL,
		status          TEXT NOT NULL,
		domain          TEXT,
		priority        TEXT,
		prio_mark       TEXT,
		created_at      TEXT,
		done_at         TEXT,
		leftover        TEXT,
		block_reason    TEXT,
		group_tag       TEXT
	)`
	if _, err := db.Exec(stmt); err != nil {
		t.Fatalf("create tasks table: %v", err)
	}
	rows := []struct {
		num      int
		title    string
		status   string
		domain   string
		prio     string
		mark     string // prio_mark = tl.py list 显示/过滤权威列；row5 留空验证「未标」桶
		created  string
		doneAt   string
		leftover string
		blocked  string
	}{
		{1, "写测试任务一", "done", "UI与交互", "P1", "P1", "2026-11-01", "2026-11-05", "", ""},
		{2, "验证测试任务二", "verified", "基建与工程", "P2", "P2", "2026-11-01", "2026-11-06", "", ""},
		{3, "取消测试任务三", "cancelled", "UI与交互", "P3", "P3", "2026-11-02", "", "", ""},
		{4, "待办测试任务四", "pending", "记忆", "P1", "P1", "2026-11-03", "", "", ""},
		{5, "阻塞任务五（50%_特殊%字符）", "blocked", "记忆", "p2", "", "2026-11-03", "", "", "等上游"},
		{6, "部分完成任务六", "partial", "会话协作与远程", "P2", "P2", "2026-11-04", "", "剩收尾", ""},
		{7, "观察任务七", "watching", "UI与交互", "P0", "P0", "2026-11-10", "", "", ""},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO tasks (num,title,status,domain,priority,prio_mark,created_at,done_at,leftover,block_reason) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.num, r.title, r.status, r.domain, r.prio, r.mark, r.created, r.doneAt, r.leftover, r.blocked); err != nil {
			t.Fatalf("seed row %d: %v", r.num, err)
		}
	}
	return path
}

// resetTasklistCaches clears the package-level caches between tests. Pooled
// handles are CLOSED before removal — otherwise the open file descriptors
// keep Windows from deleting the t.TempDir() fixture at cleanup time.
func resetTasklistCaches(t *testing.T) {
	t.Helper()
	clearTasklistCaches()
	t.Cleanup(clearTasklistCaches)
}

func clearTasklistCaches() {
	tasklistOverviewCache.Lock()
	tasklistOverviewCache.entries = map[string]tasklistOverviewCacheEntry{}
	tasklistOverviewCache.Unlock()
	tasklistHandles.Range(func(key, value any) bool {
		if db, ok := value.(*sql.DB); ok {
			db.Close()
		}
		tasklistHandles.Delete(key)
		return true
	})
}

// TestTasklistPanelReadOnly is the runtime red line: a handle obtained via
// the panel's DSN must reject every write class (SSOT: writes belong to tl.py).
func TestTasklistPanelReadOnly(t *testing.T) {
	path := seedTasklistDB(t)
	db, err := openTasklistReadOnly(path)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	// 本测试不走 resetTasklistCaches——这里显式注册句柄清理（注册晚于
	// TempDir 清理，LIFO 保证先关句柄再删文件，规避 Windows 文件锁）。
	t.Cleanup(clearTasklistCaches)
	for _, stmt := range []string{
		`INSERT INTO tasks (num,title,status) VALUES (99,'x','pending')`,
		`UPDATE tasks SET status='done' WHERE num=1`,
		`DELETE FROM tasks WHERE num=1`,
		`CREATE TABLE tl1746_probe (x INTEGER)`,
		`DROP TABLE tasks`,
		// journal_mode 改写需要动 db 文件头——delete 模式的库上改 WAL 必须被
		// 拒（fixture 建库默认 journal_mode=delete，该探针恒为写类）。
		`PRAGMA journal_mode = WAL`,
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Fatalf("mode=ro handle accepted write: %s", stmt)
		}
	}
	// Reads keep working after all rejected writes.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil || n != 7 {
		t.Fatalf("read after rejected writes: n=%d err=%v", n, err)
	}
	// The fixture file itself must be untouched (no silent side channel).
	fresh, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer fresh.Close()
	var probe int
	if err := fresh.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='tl1746_probe'`).Scan(&probe); err != nil || probe != 0 {
		t.Fatalf("write leaked to the db file: probe=%d err=%v", probe, err)
	}
}

// TestTasklistPanelNoWriteSQL is the static red line: the runtime query file
// must not contain any write-class SQL verb.
func TestTasklistPanelNoWriteSQL(t *testing.T) {
	src, err := os.ReadFile("tasklist_panel.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	// Write-class SQL verbs (word-boundary matched; comments included — the
	// runtime file has no reason to name them, that IS the red line).
	pattern := regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|REPLACE\s+INTO|CREATE\s+TABLE|DROP\s+TABLE|ALTER\s+TABLE|VACUUM|REINDEX|ATTACH|DETACH)\b`)
	if loc := pattern.FindIndex(src); loc != nil {
		line := strings.Count(string(src[:loc[0]]), "\n") + 1
		t.Fatalf("tasklist_panel.go mentions write-class SQL at line %d (%q) — the panel is read-only, refactor the wording",
			line, strings.TrimSpace(string(src[loc[0]:loc[1]])))
	}
}

func TestResolveTasklistDBPath(t *testing.T) {
	// 显式覆盖优先（trim 过的值）。
	if got := resolveTasklistDBPath("  D:/tl/other.db "); got != filepath.Clean("D:/tl/other.db") {
		t.Errorf("override lost: %q", got)
	}
	// 空值回落内置默认：%APPDATA%/reasonix/global-workspace/docs/reasonix/tasklist/tasklist.db
	t.Setenv("APPDATA", filepath.Join("t:", "appdata"))
	want := filepath.Join("t:", "appdata", "reasonix", "global-workspace", "docs", "reasonix", "tasklist", "tasklist.db")
	if got := resolveTasklistDBPath(""); got != filepath.Clean(want) {
		t.Errorf("default path: got %q want %q", got, filepath.Clean(want))
	}
	// APPDATA 缺失且 UserConfigDir 不可用 → 空路径（调用方报友好错误）。
	// Windows 上 os.UserConfigDir 读 APPDATA；置空后 defaultTasklistDBPath
	// 应回空串而不是崩溃。
	t.Setenv("APPDATA", "")
	if got := defaultTasklistDBPath(); got == "" {
		if got := resolveTasklistDBPath(""); got != "" && !strings.Contains(got, "reasonix") {
			t.Errorf("fallback produced an unexpected path: %q", got)
		}
	}
}

func TestGetTasklistOverviewNumbers(t *testing.T) {
	path := seedTasklistDB(t) // 先造库：TempDir 清理后注册，LIFO 让句柄关闭先于删文件
	resetTasklistCaches(t)
	ov, err := (&App{}).GetTasklistOverview(path, true)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	// 口径对齐 tl.py stats：total=7；待办（非 done/verified/cancelled）=4
	// （pending+blocked+partial+watching）；完成=done+verified=2；取消=1。
	if ov.Total != 7 || ov.Pending != 4 || ov.Done != 2 || ov.Cancelled != 1 {
		t.Fatalf("cards: total=%d pending=%d done=%d cancelled=%d", ov.Total, ov.Pending, ov.Done, ov.Cancelled)
	}
	if ov.Path == "" {
		t.Fatal("overview must carry the resolved path")
	}
	find := func(buckets []TasklistCount, key string) int {
		for _, b := range buckets {
			if b.Key == key {
				return b.Count
			}
		}
		return -1
	}
	if find(ov.ByStatus, "done") != 1 || find(ov.ByStatus, "watching") != 1 {
		t.Fatalf("byStatus: %v", ov.ByStatus)
	}
	// 未完成按领域（tl.py stats 第二段）：UI与交互=1（watching）、记忆=2。
	if find(ov.OpenByDomain, "UI与交互") != 1 || find(ov.OpenByDomain, "记忆") != 2 {
		t.Fatalf("openByDomain: %v", ov.OpenByDomain)
	}
	// 优先级分布按 prio_mark（tl.py stats 口径）：P2=2 与空桶（未标）=1
	// 两桶可见；遗留 priority 列不参与。
	if find(ov.ByPriority, "P2") != 2 || find(ov.ByPriority, "") != 1 {
		t.Fatalf("byPriority: %v", ov.ByPriority)
	}
	if find(ov.OpenByPriority, "P1") != 1 || find(ov.OpenByPriority, "") != 1 {
		t.Fatalf("openByPriority: %v", ov.OpenByPriority)
	}
}

func TestGetTasklistOverviewCache(t *testing.T) {
	path := seedTasklistDB(t) // 先造库：TempDir 清理后注册，LIFO 让句柄关闭先于删文件
	resetTasklistCaches(t)
	app := &App{}
	first, err := app.GetTasklistOverview(path, false)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Cached {
		t.Fatal("first read must be a miss")
	}
	second, err := app.GetTasklistOverview(path, false)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.Cached {
		t.Fatal("second read within TTL must be cached")
	}
	forced, err := app.GetTasklistOverview(path, true)
	if err != nil {
		t.Fatalf("forced: %v", err)
	}
	if forced.Cached {
		t.Fatal("refresh=true must bypass the cache")
	}
	// TTL 过期后重新命中 miss（把 entry 时间戳拨回 31s 前）。
	tasklistOverviewCache.Lock()
	for k, e := range tasklistOverviewCache.entries {
		e.fetchedAt = time.Now().Add(-31 * time.Second)
		tasklistOverviewCache.entries[k] = e
	}
	tasklistOverviewCache.Unlock()
	expired, err := app.GetTasklistOverview(path, false)
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	if expired.Cached {
		t.Fatal("entry past the 30s TTL must re-query")
	}
}

func TestQueryTasksFilters(t *testing.T) {
	path := seedTasklistDB(t) // 先造库：TempDir 清理后注册，LIFO 让句柄关闭先于删文件
	resetTasklistCaches(t)
	app := &App{}
	run := func(t *testing.T, f TasklistQueryFilters) *TasklistQueryResult {
		t.Helper()
		res, err := app.QueryTasks(path, f)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		return res
	}
	// 状态筛选。
	res := run(t, TasklistQueryFilters{Status: "pending"})
	if res.Total != 1 || len(res.Rows) != 1 || res.Rows[0].Num != 4 {
		t.Fatalf("status filter: total=%d rows=%v", res.Total, res.Rows)
	}
	// 优先级按 prio_mark 列（tl.py 同口径）：P2 命中 P2×2（row5 prio_mark
	// 为空不命中——遗留 priority 列不参与匹配）；小写 p2 同样命中 2 行
	// （大小写不敏感）。
	res = run(t, TasklistQueryFilters{Priority: "P2"})
	if res.Total != 2 {
		t.Fatalf("priority filter: %d", res.Total)
	}
	res = run(t, TasklistQueryFilters{Priority: "p2"})
	if res.Total != 2 {
		t.Fatalf("priority filter lowercase: %d", res.Total)
	}
	// 领域精确。
	res = run(t, TasklistQueryFilters{Domain: "记忆"})
	if res.Total != 2 {
		t.Fatalf("domain filter: %d", res.Total)
	}
	// 搜索子串。
	res = run(t, TasklistQueryFilters{Search: "任务四"})
	if res.Total != 1 || res.Rows[0].Num != 4 {
		t.Fatalf("search: %v", res.Rows)
	}
	// LIKE 通配符转义：字面 %/_ 不当通配符（任务五标题含 50%_特殊）。
	res = run(t, TasklistQueryFilters{Search: "50%_特殊"})
	if res.Total != 1 || res.Rows[0].Num != 5 {
		t.Fatalf("like-escape search: %v", res.Rows)
	}
	// 日期范围（含端点）。
	res = run(t, TasklistQueryFilters{CreatedFrom: "2026-11-03", CreatedTo: "2026-11-04"})
	if res.Total != 3 { // num 4/5/6
		t.Fatalf("date range: %d", res.Total)
	}
	// limit 截断 + Limited 标记 + num 倒序。
	res = run(t, TasklistQueryFilters{Limit: 3})
	if !res.Limited || res.Total != 7 || len(res.Rows) != 3 {
		t.Fatalf("limit: limited=%v total=%d rows=%d", res.Limited, res.Total, len(res.Rows))
	}
	if res.Rows[0].Num != 7 {
		t.Fatalf("order: first row num=%d want 7 (desc)", res.Rows[0].Num)
	}
	// 复合筛选（AND）。
	res = run(t, TasklistQueryFilters{Domain: "UI与交互", Status: "done"})
	if res.Total != 1 || res.Rows[0].Num != 1 {
		t.Fatalf("compound: %v", res.Rows)
	}
	// 结果携带解析路径与耗时。
	res = run(t, TasklistQueryFilters{})
	if res.Path == "" {
		t.Fatal("result must carry the resolved path")
	}
}

func TestQueryTasksAndOverviewMissingDB(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.db") // 先建目录，同 LIFO 顺序理由
	resetTasklistCaches(t)
	_, err := (&App{}).GetTasklistOverview(missing, true)
	if err == nil || !strings.Contains(err.Error(), "任务数据库不存在") || !strings.Contains(err.Error(), "可观测性") {
		t.Fatalf("missing-db overview error: %v", err)
	}
	_, err = (&App{}).QueryTasks(missing, TasklistQueryFilters{})
	if err == nil || !strings.Contains(err.Error(), "任务数据库不存在") {
		t.Fatalf("missing-db query error: %v", err)
	}
	// 测试连接：缺库 → ok=false + 引导文案；正常库 → ok=true + 行数。
	probe := (&App{}).TestTasklistDB(missing)
	if probe.OK || probe.Error == "" || probe.ReadWrite != true {
		t.Fatalf("probe missing: %+v", probe)
	}
	probe = (&App{}).TestTasklistDB(seedTasklistDB(t))
	if !probe.OK || probe.Tasks != 7 || probe.Error != "" {
		t.Fatalf("probe ok: %+v", probe)
	}
}

func TestTasklistOverviewCacheIsConcurrencySafe(t *testing.T) {
	path := seedTasklistDB(t) // 先造库：TempDir 清理后注册，LIFO 让句柄关闭先于删文件
	resetTasklistCaches(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if _, err := (&App{}).GetTasklistOverview(path, j%2 == 0); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent overview: %v", err)
	}
}

// ----- tl1775 从浏览器打开看板 -----

// stubTasklistBoardOpener replaces the openTasklistBoardFile injection seam
// for the duration of one test（绝不真开浏览器）.
func stubTasklistBoardOpener(t *testing.T, fn func(string) error) {
	t.Helper()
	prev := openTasklistBoardFile
	openTasklistBoardFile = fn
	t.Cleanup(func() { openTasklistBoardFile = prev })
}

func TestOpenTasklistBoardOpensFreshExport(t *testing.T) {
	dbPath := seedTasklistDB(t)
	board := filepath.Join(filepath.Dir(dbPath), tasklistBoardFileName)
	if err := os.WriteFile(board, []byte("<!DOCTYPE html><html><body>board</body></html>"), 0o644); err != nil {
		t.Fatalf("write board: %v", err)
	}
	var got string
	stubTasklistBoardOpener(t, func(p string) error { got = p; return nil })
	res := (&App{}).OpenTasklistBoard(dbPath)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if !res.Opened {
		t.Fatal("Opened must be true after a successful hand-off")
	}
	if res.Stale {
		t.Fatal("fresh export (board mtime >= db mtime) must not be stale")
	}
	if res.Path != board {
		t.Fatalf("board path = %q, want %q", res.Path, board)
	}
	if got != board {
		t.Fatalf("opener received %q, want %q", got, board)
	}
}

func TestOpenTasklistBoardStaleStillOpens(t *testing.T) {
	dbPath := seedTasklistDB(t)
	board := filepath.Join(filepath.Dir(dbPath), tasklistBoardFileName)
	if err := os.WriteFile(board, []byte("<html>stale</html>"), 0o644); err != nil {
		t.Fatalf("write board: %v", err)
	}
	// 看板 mtime 拨早 1h（db 刚写 = now）：html 早于 db → Stale。
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes(board, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	var got string
	stubTasklistBoardOpener(t, func(p string) error { got = p; return nil })
	res := (&App{}).OpenTasklistBoard(dbPath)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if !res.Stale {
		t.Fatal("board older than db must be flagged stale")
	}
	if !res.Opened || got != board {
		t.Fatalf("stale board must still open (opened=%v got=%q)", res.Opened, got)
	}
}

func TestOpenTasklistBoardMissingDB(t *testing.T) {
	stubbed := false
	stubTasklistBoardOpener(t, func(p string) error { stubbed = true; return nil })
	res := (&App{}).OpenTasklistBoard(filepath.Join(t.TempDir(), "nope.db"))
	if res.Error == "" || !strings.Contains(res.Error, "任务数据库不存在") {
		t.Fatalf("missing db must surface the friendly error, got %q", res.Error)
	}
	if res.Opened || stubbed {
		t.Fatal("must not open anything when the db is missing")
	}
}

func TestOpenTasklistBoardNotExported(t *testing.T) {
	dbPath := seedTasklistDB(t)
	var got string
	stubTasklistBoardOpener(t, func(p string) error { got = p; return nil })
	res := (&App{}).OpenTasklistBoard(dbPath)
	if res.Error == "" || !strings.Contains(res.Error, "看板尚未导出") {
		t.Fatalf("missing board must surface the export hint, got %q", res.Error)
	}
	if res.Opened || got != "" {
		t.Fatal("must not open anything when the board is not exported")
	}
}
