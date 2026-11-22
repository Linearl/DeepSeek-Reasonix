package main

// tl1746 phase 1 — 任务面板只读直读层（方案文档
// docs/reasonix/report/任务面板设计-demo技术方案用户故事-20261121.md，架构专家
// 对齐定稿 §八：一期纯读零风险）。
//
// 只读红线（SSOT 纪律）：本文件对 sqlite 的全部访问都以 mode=ro 打开
// （readOnlyTasklistDSN），且只执行 SELECT / PRAGMA 读语句——绝不写库；
// 写仍走 tl.py（db=权威源，回写自带 changelog+backup）。runtime query 层的
// 红线由 TestTasklistPanelReadOnly 钉住（经本层 DSN 打开的句柄写库必须失败），
// 静态红线由 TasklistPanelNoWriteSQL 钉住（本文件不得出现写类 SQL 动词）。
//
// 数据面：tasks 表 8 态（pending|in_progress|partial|watching|blocked|done|
// verified|cancelled）。总览口径与 tl.py stats 对齐：
//   total    = COUNT(*)（全量）
//   pending  = 非 done/verified/cancelled（待办，含 blocked/partial/watching）
//   done     = done+verified（完成）
//   cancelled= cancelled
// 分布按原始值 GROUP BY（优先级取 prio_mark 列——tl.py list 显示与 --prio
// 过滤的权威列；priority 为遗留列只作历史留档，不做归并，前端只做显示层
// 归一——与 tl.py stats/list 的逐行原值口径一致，验收对账免换算）。
//
// 缓存：总览 30s TTL（按解析后路径逐库缓存）；QueryTasks 不缓存（1697 行级
// 全量查本身 <10ms 量级）；refresh=true 强制重查（面板手动刷新按钮）。

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 零 CGO（仓内既有依赖，projectiondb/memory 同驱动）
)

// tasklistCacheTTL is the overview cache window (方案 §三.3：30s 缓存).
const tasklistCacheTTL = 30 * time.Second

// tasklistQueryLimit 默认行上限（列表 tab 单次拉取；超限用筛选收窄）。
const tasklistQueryLimit = 500

// TasklistCount is one distribution bucket (status / domain / priority).
type TasklistCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// TasklistOverview is the 总览 tab payload: four stat cards + distributions.
type TasklistOverview struct {
	Path           string          `json:"path"`           // 解析后的实际 db 路径（前端展示/错误引导用）
	Total          int             `json:"total"`          // 全量任务数
	Pending        int             `json:"pending"`        // 待办 = 非 done/verified/cancelled
	Done           int             `json:"done"`           // 完成 = done+verified
	Cancelled      int             `json:"cancelled"`      // 已取消
	ByStatus       []TasklistCount `json:"byStatus"`       // 8 态全量分布
	ByDomain       []TasklistCount `json:"byDomain"`       // 全量按领域
	ByPriority     []TasklistCount `json:"byPriority"`     // 全量按优先级（原值）
	OpenByDomain   []TasklistCount `json:"openByDomain"`   // 未完成按领域（tl.py stats 口径）
	OpenByPriority []TasklistCount `json:"openByPriority"` // 未完成按优先级（原值）
	GeneratedAt    string          `json:"generatedAt"`    // 本次聚合时间（缓存命中时为首次生成时间）
	Cached         bool            `json:"cached"`         // true = 30s 窗口内缓存命中
}

// TasklistTaskRow is one row of the 任务列表 tab.
type TasklistTaskRow struct {
	Num         int    `json:"num"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Priority    string `json:"priority"` // = prio_mark（tl.py list 显示列；priority 为遗留列）
	Domain      string `json:"domain"`
	CreatedAt   string `json:"createdAt"`
	DoneAt      string `json:"doneAt"`
	Leftover    string `json:"leftover"`    // partial 备注
	BlockReason string `json:"blockReason"` // blocked 原因
	GroupTag    string `json:"groupTag"`
}

// TasklistQueryFilters is the 任务列表 tab filter bar（全部可空 = 不过滤）。
type TasklistQueryFilters struct {
	Status      string `json:"status"`      // 精确匹配 8 态之一；"" = 全部
	Priority    string `json:"priority"`    // = prio_mark（tl.py list 显示列；priority 为遗留列）    // 大小写不敏感精确匹配（原值列大小写混存）
	Domain      string `json:"domain"`      // 精确匹配
	Search      string `json:"search"`      // 标题子串（LIKE，%/_ 转义）
	CreatedFrom string `json:"createdFrom"` // YYYY-MM-DD（含），按 created_at 前缀比较
	CreatedTo   string `json:"createdTo"`   // YYYY-MM-DD（含）
	Limit       int    `json:"limit"`       // 行上限；<=0 用 tasklistQueryLimit
}

// TasklistQueryResult is the 任务列表 tab payload.
type TasklistQueryResult struct {
	Path      string            `json:"path"` // 解析后的实际 db 路径
	Rows      []TasklistTaskRow `json:"rows"`
	Total     int               `json:"total"`     // 命中总数（不受 limit 截断影响）
	Limited   bool              `json:"limited"`   // true = rows 被 limit 截断
	ElapsedMs int64             `json:"elapsedMs"` // 本次查询耗时（性能验收 <10ms 量级可见化）
}

// TestTasklistDBResult is the 实验室「测试连接」payload.
type TestTasklistDBResult struct {
	Path      string `json:"path"`     // 解析后的路径
	OK        bool   `json:"ok"`       // true = 可读
	Tasks     int    `json:"tasks"`    // 可读时的任务总数
	Error     string `json:"error"`    // 不可读时的原因（前端直接展示）
	ReadWrite bool   `json:"readOnly"` // 恒 true——探测句柄同样 mode=ro（红线可见化）
}

// tasklistOverviewCacheEntry is one cached overview snapshot.
type tasklistOverviewCacheEntry struct {
	overview  *TasklistOverview
	fetchedAt time.Time
}

// tasklistOverviewCache caches overviews per resolved db path (30s TTL).
var tasklistOverviewCache = struct {
	sync.Mutex
	entries map[string]tasklistOverviewCacheEntry
}{entries: map[string]tasklistOverviewCacheEntry{}}

// tasklistHandles keeps one pooled *sql.DB per resolved path so repeated
// panel queries skip the file-open cost while staying read-only. Handles are
// mode=ro connections; idle pooled connections hold no read locks (WAL), so
// tl.py writes proceed concurrently.
var tasklistHandles sync.Map // map[string]*sql.DB

// defaultTasklistDBPath builds the built-in default database location:
// %APPDATA%/reasonix/global-workspace/docs/reasonix/tasklist/tasklist.db
// (方案 §三.2 定稿：写死默认值+可配置覆盖，不做首开探测).
func defaultTasklistDBPath() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		// Non-Windows / stripped env: os.UserConfigDir is the same directory
		// on Windows and a sane fallback elsewhere (dev machines).
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			appdata = dir
		}
	}
	if appdata == "" {
		return ""
	}
	return filepath.Join(appdata, "reasonix", "global-workspace", "docs", "reasonix", "tasklist", "tasklist.db")
}

// resolveTasklistDBPath applies the override precedence: explicit argument
// (the frontend forwards the settings-view tasklistDbPath value, possibly "")
// wins; empty falls back to the built-in default. Returns "" when neither
// yields a usable location (the caller reports a friendly error).
func resolveTasklistDBPath(override string) string {
	p := strings.TrimSpace(override)
	if p != "" {
		return filepath.Clean(p)
	}
	p = strings.TrimSpace(defaultTasklistDBPath())
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}

// readOnlyTasklistDSN builds a mode=ro file URI for the driver (projectiondb
// diskFileDSN 先例：Windows 盘符路径转 file:///C:/... 形式). mode=ro only —
// 不加 immutable=1：tl.py 活写时 WAL 里可能有未 checkpoint 的新数据，
// immutable 会无视 WAL 读到旧快照。
func readOnlyTasklistDSN(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slash := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && len(slash) >= 2 && slash[1] == ':' {
		// C:/Users/... → /C:/Users/... so the URI becomes file:///C:/Users/...
		slash = "/" + slash
	}
	u := &url.URL{Scheme: "file", Path: slash}
	// busy_timeout：tl.py 拿写锁的瞬间读侧多等 150ms 而非立刻报忙。
	return u.String() + "?_pragma=busy_timeout%28150%29&mode=ro"
}

// openTasklistReadOnly returns the pooled read-only handle for path.
func openTasklistReadOnly(path string) (*sql.DB, error) {
	if h, ok := tasklistHandles.Load(path); ok {
		return h.(*sql.DB), nil
	}
	db, err := sql.Open("sqlite", readOnlyTasklistDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	actual, ok := tasklistHandles.LoadOrStore(path, db)
	if !ok {
		return db, nil
	}
	// Another goroutine won the race; drop ours, use theirs.
	db.Close()
	return actual.(*sql.DB), nil
}

// tasklistDBMissingError is the friendly missing-database message shared by
// the panel error card and the lab 测试连接 row (方案 §三.4：明确错误+引导).
func tasklistDBMissingError(path string) error {
	return fmt.Errorf("任务数据库不存在：%s（可在 实验室 → 可观测性 → 任务面板 修改数据库路径，或先运行 tl.py 建库）", path)
}

// statTasklistDB validates the db file exists before opening (sql.Open is
// lazy; a missing file would otherwise surface as a raw driver error).
func statTasklistDB(path string) error {
	if path == "" {
		return fmt.Errorf("无法确定任务数据库路径（APPDATA 未设置），请在 实验室 → 可观测性 → 任务面板 手动指定数据库路径")
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return tasklistDBMissingError(path)
	}
	return nil
}

// GetTasklistOverview returns the 总览 tab payload (four stat cards +
// distributions). refresh=true bypasses the 30s cache (手动刷新按钮).
func (a *App) GetTasklistOverview(path string, refresh bool) (*TasklistOverview, error) {
	resolved := resolveTasklistDBPath(path)
	if !refresh {
		tasklistOverviewCache.Lock()
		entry, ok := tasklistOverviewCache.entries[resolved]
		tasklistOverviewCache.Unlock()
		if ok && time.Since(entry.fetchedAt) < tasklistCacheTTL {
			cached := *entry.overview
			cached.Cached = true
			return &cached, nil
		}
	}
	overview, err := buildTasklistOverview(resolved)
	if err != nil {
		return nil, err
	}
	tasklistOverviewCache.Lock()
	tasklistOverviewCache.entries[resolved] = tasklistOverviewCacheEntry{overview: overview, fetchedAt: time.Now()}
	tasklistOverviewCache.Unlock()
	cached := *overview
	return &cached, nil
}

// buildTasklistOverview runs the aggregates in one pass over the db.
func buildTasklistOverview(resolved string) (*TasklistOverview, error) {
	if err := statTasklistDB(resolved); err != nil {
		return nil, err
	}
	db, err := openTasklistReadOnly(resolved)
	if err != nil {
		return nil, fmt.Errorf("任务数据库无法打开（只读模式）：%w", err)
	}
	ov := &TasklistOverview{Path: resolved, GeneratedAt: time.Now().Format(time.RFC3339)}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&ov.Total); err != nil {
		return nil, fmt.Errorf("任务数据库读取失败：%w", err)
	}
	const openCond = `WHERE status NOT IN ('done','verified','cancelled')`
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks ` + openCond).Scan(&ov.Pending); err != nil {
		return nil, fmt.Errorf("任务数据库读取失败：%w", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status IN ('done','verified')`).Scan(&ov.Done); err != nil {
		return nil, fmt.Errorf("任务数据库读取失败：%w", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status = 'cancelled'`).Scan(&ov.Cancelled); err != nil {
		return nil, fmt.Errorf("任务数据库读取失败：%w", err)
	}
	groups := []struct {
		dst   *[]TasklistCount
		query string
	}{
		{&ov.ByStatus, `SELECT status, COUNT(*) FROM tasks GROUP BY status ORDER BY COUNT(*) DESC`},
		{&ov.ByDomain, `SELECT COALESCE(domain,''), COUNT(*) FROM tasks GROUP BY domain ORDER BY COUNT(*) DESC`},
		// 优先级分布按 prio_mark（tl.py stats「未完成按优先级」同列口径；
		// 空桶由前端显示为「未标」）。
		{&ov.ByPriority, `SELECT COALESCE(prio_mark,''), COUNT(*) FROM tasks GROUP BY prio_mark ORDER BY COUNT(*) DESC`},
		{&ov.OpenByDomain, `SELECT COALESCE(domain,''), COUNT(*) FROM tasks ` + openCond + ` GROUP BY domain ORDER BY COUNT(*) DESC`},
		{&ov.OpenByPriority, `SELECT COALESCE(prio_mark,''), COUNT(*) FROM tasks ` + openCond + ` GROUP BY prio_mark ORDER BY COUNT(*) DESC`},
	}
	for _, g := range groups {
		rows, err := db.Query(g.query)
		if err != nil {
			return nil, fmt.Errorf("任务数据库读取失败：%w", err)
		}
		buckets := []TasklistCount{}
		for rows.Next() {
			var c TasklistCount
			if err := rows.Scan(&c.Key, &c.Count); err != nil {
				rows.Close()
				return nil, fmt.Errorf("任务数据库读取失败：%w", err)
			}
			buckets = append(buckets, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("任务数据库读取失败：%w", err)
		}
		*g.dst = buckets
	}
	return ov, nil
}

// QueryTasks runs the 任务列表 tab query (filters AND-combined; all empty =
// 全量按 num 倒序).
func (a *App) QueryTasks(path string, filters TasklistQueryFilters) (*TasklistQueryResult, error) {
	resolved := resolveTasklistDBPath(path)
	if err := statTasklistDB(resolved); err != nil {
		return nil, err
	}
	started := time.Now()
	db, err := openTasklistReadOnly(resolved)
	if err != nil {
		return nil, fmt.Errorf("任务数据库无法打开（只读模式）：%w", err)
	}
	where, args := buildTasklistWhere(filters)
	limit := filters.Limit
	if limit <= 0 || limit > 2000 {
		limit = tasklistQueryLimit
	}
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`+where, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("任务数据库读取失败：%w", err)
	}
	rows, err := db.Query(`SELECT num, title, status, COALESCE(prio_mark,''), COALESCE(domain,''), COALESCE(created_at,''), COALESCE(done_at,''), COALESCE(leftover,''), COALESCE(block_reason,''), COALESCE(group_tag,'') FROM tasks`+where+` ORDER BY num DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("任务数据库读取失败：%w", err)
	}
	defer rows.Close()
	out := &TasklistQueryResult{Path: resolved, Total: total, Limited: total > limit}
	for rows.Next() {
		var r TasklistTaskRow
		if err := rows.Scan(&r.Num, &r.Title, &r.Status, &r.Priority, &r.Domain, &r.CreatedAt, &r.DoneAt, &r.Leftover, &r.BlockReason, &r.GroupTag); err != nil {
			return nil, fmt.Errorf("任务数据库读取失败：%w", err)
		}
		out.Rows = append(out.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("任务数据库读取失败：%w", err)
	}
	out.ElapsedMs = time.Since(started).Milliseconds()
	return out, nil
}

// buildTasklistWhere AND-combines the filter bar into a WHERE clause with
// bound parameters (无字符串拼接用户输入——搜索词走参数化+LIKE 转义).
func buildTasklistWhere(filters TasklistQueryFilters) (string, []any) {
	var conds []string
	var args []any
	if s := strings.TrimSpace(filters.Status); s != "" {
		conds = append(conds, `status = ?`)
		args = append(args, s)
	}
	if p := strings.TrimSpace(filters.Priority); p != "" {
		// prio_mark 列 = tl.py list 显示与 --prio 过滤的权威列（priority 为
		// 遗留列，仅历史行有值）；大小写不敏感精确匹配。
		conds = append(conds, `LOWER(COALESCE(prio_mark,'')) = LOWER(?)`)
		args = append(args, p)
	}
	if d := strings.TrimSpace(filters.Domain); d != "" {
		conds = append(conds, `COALESCE(domain,'') = ?`)
		args = append(args, d)
	}
	if q := strings.TrimSpace(filters.Search); q != "" {
		conds = append(conds, `title LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeTasklistLike(q)+"%")
	}
	if from := strings.TrimSpace(filters.CreatedFrom); from != "" {
		conds = append(conds, `COALESCE(created_at,'') >= ?`)
		args = append(args, from)
	}
	if to := strings.TrimSpace(filters.CreatedTo); to != "" {
		conds = append(conds, `COALESCE(created_at,'') < date(?, '+1 day')`)
		args = append(args, to)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// escapeTasklistLike escapes LIKE wildcards in user search input.
func escapeTasklistLike(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(q)
}

// TestTasklistDB probes the configured database for the lab sub-item's
// 测试连接 button. The probe opens the same mode=ro DSN — it never writes.
func (a *App) TestTasklistDB(path string) TestTasklistDBResult {
	resolved := resolveTasklistDBPath(path)
	res := TestTasklistDBResult{Path: resolved, ReadWrite: true}
	if err := statTasklistDB(resolved); err != nil {
		res.Error = err.Error()
		return res
	}
	db, err := openTasklistReadOnly(resolved)
	if err != nil {
		res.Error = fmt.Sprintf("任务数据库无法打开（只读模式）：%v", err)
		return res
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&res.Tasks); err != nil {
		res.Error = fmt.Sprintf("任务数据库读取失败：%v", err)
		return res
	}
	res.OK = true
	return res
}

// OpenTasklistDBLocation reveals the resolved database file in the system
// file manager (lab sub-item 打开所在位置; revealPath launches detached and
// never waits — OpenLogDir precedent).
func (a *App) OpenTasklistDBLocation(path string) error {
	resolved := resolveTasklistDBPath(path)
	if resolved == "" {
		return fmt.Errorf("无法确定任务数据库路径（APPDATA 未设置），请手动指定数据库路径")
	}
	return a.RevealPath(resolved)
}
