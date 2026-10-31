package main

// tl1227: DAG 读兼容退役第二阶段——存量 schema-2 events 会话日志一次性迁移为
// schema-1 平坦日志。迁移本体在 internal/agent.MigrateSessionDAGLog（tools 侧
// scripts/tl1227-dag-migrate 是同一函数的薄壳），本文件只负责：
//   1. 扫描全库，把"待迁移"与"为什么不能迁"分门别类地报给设置面板；
//   2. 逐会话执行迁移，逐条把进度推给前端（EventsEmit），单会话失败不中断整批。
//
// 与外部脚本的关键差异：脚本要求先退出应用（否则拒绝），因为进程内迁移会撞上
// 本进程自己持有的会话。这里改用两条更精确的判据，从而支持应用内执行：
//   - 本进程打开的会话（tab / detached runtime）一律跳过，标 active；
//   - 其它 runtime 持有的会话由 MigrateSessionDAGLog 内部的租约检查跳过。
// 于是"正在用的那个会话"永远等着用户关掉它再点一次，其余可立即迁移。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// dagMigrationEventsSuffix 与读取端 probe 的后缀口径一致（main/guardian/
// repairlog 三种 sidecar 都以它结尾）。
const dagMigrationEventsSuffix = ".events.jsonl"

// dagMigrationProgressEvent 前端订阅的进度事件名。
const dagMigrationProgressEvent = "dag-migration:progress"

// dagMigrationSessionView 是单个会话在面板上的读数。
type dagMigrationSessionView struct {
	SessionPath string `json:"sessionPath"`
	Label       string `json:"label"`
	LogBytes    int64  `json:"logBytes"`
	Messages    int    `json:"messages"`
	// Status: pending / active / migrated / skipped / failed
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	// Active 表示本进程正在使用该会话；迁移会跳过它，关掉对应会话后可重试。
	Active bool `json:"active,omitempty"`
}

// dagMigrationScanView 是一次全库扫描的结果（只读，不落盘）。
type dagMigrationScanView struct {
	Sessions []dagMigrationSessionView `json:"sessions"`
	Scanned  int                       `json:"scanned"`
	// Backups 是被排除的自愈快照树数量（*.pre-repair-*，永不作为会话加载，
	// 是修复的回滚凭据，改写它等于动唯一值得保留的副本）。
	Backups   int    `json:"backups"`
	ElapsedMs int64  `json:"elapsedMs"`
	Error     string `json:"error,omitempty"`
}

// dagMigrationRunView 是一次 apply 的结果。
type dagMigrationRunView struct {
	Sessions  []dagMigrationSessionView `json:"sessions"`
	Migrated  int                       `json:"migrated"`
	Active    int                       `json:"active"`
	Skipped   int                       `json:"skipped"`
	Failed    int                       `json:"failed"`
	ElapsedMs int64                     `json:"elapsedMs"`
}

// dagMigrationRunning 阻止并发点击：两个并行的写批次没有意义且会互相竞争。
var dagMigrationRunning atomic.Bool

// ScanDagMigration 扫描全库并返回待迁移清单（check 模式，绝不写盘）。
func (a *App) ScanDagMigration() dagMigrationScanView {
	started := time.Now()
	root := dagMigrationProjectsRoot()
	targets, backups, warnings, err := dagMigrationCollect(root)
	view := dagMigrationScanView{Backups: len(backups)}
	for _, w := range warnings {
		if view.Error == "" {
			view.Error = w
		}
	}
	if err != nil {
		view.Error = err.Error()
		view.ElapsedMs = time.Since(started).Milliseconds()
		return view
	}
	active := a.dagMigrationActiveSessions()
	ctx := context.Background()
	for _, sessionPath := range targets {
		view.Scanned++
		report, migErr := dagMigrationRunOne(ctx, sessionPath, agent.SessionDAGMigrationCheck)
		if migErr != nil {
			view.Sessions = append(view.Sessions, dagMigrationSessionView{
				SessionPath: sessionPath,
				Label:       dagMigrationLabel(sessionPath),
				Status:      "failed",
				Detail:      dagMigrationFirstLine(migErr.Error()),
			})
			continue
		}
		if report == nil || report.Status != agent.SessionDAGChecked {
			// 非 schema-2（已平坦 / 损坏 / 他方租约持有）：不是本卡的工作面。
			continue
		}
		_, isActive := active[sessionPath]
		status := "pending"
		if isActive {
			status = "active"
		}
		view.Sessions = append(view.Sessions, dagMigrationSessionView{
			SessionPath: sessionPath,
			Label:       dagMigrationLabel(sessionPath),
			LogBytes:    report.LogBytesBefore,
			Messages:    report.MessageCount,
			Status:      status,
			Active:      isActive,
		})
	}
	view.ElapsedMs = time.Since(started).Milliseconds()
	return view
}

// RunDagMigration 迁移所有可迁移的 schema-2 会话，逐条推送进度。
func (a *App) RunDagMigration() (dagMigrationRunView, error) {
	if !dagMigrationRunning.CompareAndSwap(false, true) {
		return dagMigrationRunView{}, fmt.Errorf("dag migration already running")
	}
	defer dagMigrationRunning.Store(false)

	started := time.Now()
	view := dagMigrationRunView{}
	root := dagMigrationProjectsRoot()
	targets, _, _, err := dagMigrationCollect(root)
	if err != nil {
		return view, err
	}
	ctx := context.Background()

	for i, sessionPath := range targets {
		label := dagMigrationLabel(sessionPath)
		// 本进程持有的会话不能迁（内存态与磁盘会不一致）。这里**每轮现取**
		// 而不在循环外用一份快照：整批可能跑几十秒（4 个会话 380MB），期间
		// 用户开关会话都会改变活跃面，陈旧快照会漏判→误迁正在写的会话。
		if _, isActive := a.dagMigrationActiveSessions()[sessionPath]; isActive {
			view.Active++
			view.Sessions = append(view.Sessions, dagMigrationSessionView{
				SessionPath: sessionPath, Label: label, Status: "active", Active: true,
				Detail: "open in this app; close the session and run again",
			})
			a.emitDagMigrationProgress(i, len(targets), label, "active", 0)
			continue
		}
		report, migErr := dagMigrationRunOne(ctx, sessionPath, agent.SessionDAGMigrationApply)
		if migErr != nil {
			view.Failed++
			// Post-apply 校验失败时备份仍然存在，所以"失败"不等于"数据丢了"。
			view.Sessions = append(view.Sessions, dagMigrationSessionView{
				SessionPath: sessionPath, Label: label, Status: "failed",
				Detail: dagMigrationFirstLine(migErr.Error()),
			})
			a.emitDagMigrationProgress(i, len(targets), label, "failed", 0)
			continue
		}
		if report == nil {
			view.Failed++
			view.Sessions = append(view.Sessions, dagMigrationSessionView{
				SessionPath: sessionPath, Label: label, Status: "failed",
				Detail: "no report returned",
			})
			a.emitDagMigrationProgress(i, len(targets), label, "failed", 0)
			continue
		}
		switch report.Status {
		case agent.SessionDAGMigrated:
			view.Migrated++
			view.Sessions = append(view.Sessions, dagMigrationSessionView{
				SessionPath: sessionPath, Label: label, Status: "migrated",
				LogBytes: report.LogBytesBefore, Messages: report.MessageCount,
			})
			a.emitDagMigrationProgress(i, len(targets), label, "migrated", report.LogBytesBefore)
		case agent.SessionDAGSkipNotDAG:
			// 已平坦：不计入任何计数（这是迁移完成后重跑的常态）。
		case agent.SessionDAGSkipLease:
			view.Skipped++
			view.Sessions = append(view.Sessions, dagMigrationSessionView{
				SessionPath: sessionPath, Label: label, Status: "skipped",
				Detail: "another runtime holds the lease",
			})
			a.emitDagMigrationProgress(i, len(targets), label, "skipped", 0)
		default:
			view.Skipped++
			view.Sessions = append(view.Sessions, dagMigrationSessionView{
				SessionPath: sessionPath, Label: label, Status: "skipped",
				Detail: string(report.Status),
			})
			a.emitDagMigrationProgress(i, len(targets), label, "skipped", 0)
		}
	}
	view.ElapsedMs = time.Since(started).Milliseconds()
	return view, nil
}

// emitDagMigrationProgress 把一条进度推给前端；ctx 缺失（离屏/测试）时静默跳过。
func (a *App) emitDagMigrationProgress(index, total int, label, status string, bytes int64) {
	if a == nil || a.ctx == nil {
		return
	}
	runtimeEventsEmitFallback(a.ctx, dagMigrationProgressEvent, map[string]any{
		"index":   index + 1,
		"total":   total,
		"label":   label,
		"status":  status,
		"bytes":   bytes,
		"elapsed": time.Now().UnixMilli(),
	})
}

// dagMigrationRunOne 隔离单会话：共享 API 很小心，但畸形日志仍可能在某层
// panic，一个坏会话不该带走整批。
func dagMigrationRunOne(ctx context.Context, sessionPath string, mode agent.SessionDAGMigrationMode) (report *agent.SessionDAGMigrationReport, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 8192)
			n := runtime.Stack(buf, false)
			err = fmt.Errorf("panic: %v\n%s", r, strings.TrimSpace(string(buf[:n])))
		}
	}()
	return agent.MigrateSessionDAGLog(ctx, sessionPath, mode)
}

// dagMigrationActiveSessions 返回本进程当前占用的会话路径集合（含 detached
// runtime）。openSessionPaths 按目录过滤，这里需要跨 project 的全集，所以直接
// 读 tab 的 currentSessionPath。
func (a *App) dagMigrationActiveSessions() map[string]struct{} {
	out := map[string]struct{}{}
	if a == nil {
		return out
	}
	a.mu.RLock()
	paths := make([]string, 0, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab != nil {
			paths = append(paths, tab.currentSessionPath())
		}
	}
	a.mu.RUnlock()
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func dagMigrationProjectsRoot() string {
	return filepath.Join(config.MemoryUserDir(), "projects")
}

// dagMigrationCollect 递归收集所有 *.events.jsonl，并把自愈快照树单独分出来。
func dagMigrationCollect(root string) (targets, backups, warnings []string, err error) {
	if _, statErr := os.Stat(root); statErr != nil {
		return nil, nil, nil, statErr
	}
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, walkErr))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, dagMigrationEventsSuffix) {
			return nil
		}
		if dagMigrationIsSnapshot(path) {
			backups = append(backups, path)
			return nil
		}
		targets = append(targets, strings.TrimSuffix(path, dagMigrationEventsSuffix)+".jsonl")
		return nil
	})
	return targets, backups, warnings, walkErr
}

// dagMigrationIsSnapshot 识别 "<session>.pre-repair-<stamp>" 自愈快照树。
func dagMigrationIsSnapshot(path string) bool {
	for _, seg := range strings.Split(path, string(filepath.Separator)) {
		if strings.Contains(seg, ".pre-repair-") {
			return true
		}
	}
	return false
}

func dagMigrationLabel(sessionPath string) string {
	base := strings.TrimSuffix(filepath.Base(sessionPath), ".jsonl")
	if len(base) > 32 {
		base = base[:32] + "~"
	}
	return base
}

func dagMigrationFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ─── tl1287⑤：清理迁移备份 ────────────────────────────────────────────────
//
// 迁移把每个 schema-2 会话的原字节整体留在 <log>.dagbak-tl1227，累计体量已
// 超过被迁移对象本身（用户实测 1,692.8MB）。清理前提两条（缺一不可）：
//   1. 迁移检查归零——扫描不再发现任何待迁移/使用中的 schema-2 会话；
//   2. 至少保留最近一份——dagbak 是 schema-2 原件，误清即永远回不到迁移前，
//      最近一份是最后一份回滚凭据。
// 其余按时间倒序（从新到旧）删除。plan 与 run 各自独立现算清单：run 不信任
// plan 时刻的列表（两次点击之间磁盘可能变化），确认弹层展示的将删清单与
// 体量来自 plan，执行时以 run 现算为准。

// dagMigrationBackupView 是一个 .dagbak-tl1227 备份文件的读数。
type dagMigrationBackupView struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	ModifiedAt int64  `json:"modifiedAt"` // unix millis
}

// dagBackupCleanupPlanView 是「清理迁移备份」的扫描结果（只读，不删任何字节）。
type dagBackupCleanupPlanView struct {
	// Backups 是全部备份按时间从新到旧的列表（确认弹层逐行展示）。
	Backups []dagMigrationBackupView `json:"backups"`
	// Deletable 是除最近一份外的全部（=执行时将删除的清单）。
	Deletable   []dagMigrationBackupView `json:"deletable"`
	KeepPath    string                   `json:"keepPath,omitempty"`
	KeepBytes   int64                    `json:"keepBytes,omitempty"`
	DeleteBytes int64                    `json:"deleteBytes"`
	// RemainingMigrations 是仍未迁移的会话数（待迁移 + 本进程使用中）；
	// >0 时清理被拒绝，前端展示原因。
	RemainingMigrations int    `json:"remainingMigrations"`
	Error               string `json:"error,omitempty"`
}

// dagBackupCleanupRunView 是一次执行的结果。
type dagBackupCleanupRunView struct {
	Deleted      int      `json:"deleted"`
	DeletedBytes int64    `json:"deletedBytes"`
	Failed       int      `json:"failed"`
	KeptPath     string   `json:"keptPath,omitempty"`
	Details      []string `json:"details,omitempty"`
}

// dagBackupCleanupScan 收集全部 .dagbak-tl1227（含自愈快照树内的——它们同属
// 迁移备份、同占体量），按修改时间从新到旧排序。
func dagBackupCleanupScan(root string) ([]dagMigrationBackupView, error) {
	var out []dagMigrationBackupView
	if _, statErr := os.Stat(root); statErr != nil {
		return nil, statErr
	}
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, agent.SessionDAGMigrationBackupSuffix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // 消失中的文件不算失败
		}
		out = append(out, dagMigrationBackupView{
			Path:       path,
			Bytes:      info.Size(),
			ModifiedAt: info.ModTime().UnixMilli(),
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModifiedAt != out[j].ModifiedAt {
			return out[i].ModifiedAt > out[j].ModifiedAt
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// dagBackupRemainingMigrations 复用迁移扫描统计「还剩几个会话没迁完」：
// 待迁移（pending）+ 本进程使用中（active）都算——它们迁完之前 check 不算
// 归零，备份都还有用武之地。
func (a *App) dagBackupRemainingMigrations() int {
	scan := a.ScanDagMigration()
	remaining := 0
	for _, session := range scan.Sessions {
		if session.Status == "pending" || session.Status == "active" {
			remaining++
		}
	}
	return remaining
}

// PlanDagBackupCleanup 是清理前的只读扫描：备份清单、将删清单与体量、
// 保留哪一份，以及迁移是否已归零。
func (a *App) PlanDagBackupCleanup() dagBackupCleanupPlanView {
	view := dagBackupCleanupPlanView{Backups: []dagMigrationBackupView{}, Deletable: []dagMigrationBackupView{}}
	root := dagMigrationProjectsRoot()
	backups, err := dagBackupCleanupScan(root)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	view.Backups = backups
	if len(backups) > 0 {
		view.KeepPath = backups[0].Path
		view.KeepBytes = backups[0].Bytes
	}
	for _, backup := range backups[1:] {
		view.Deletable = append(view.Deletable, backup)
		view.DeleteBytes += backup.Bytes
	}
	view.RemainingMigrations = a.dagBackupRemainingMigrations()
	return view
}

// RunDagBackupCleanup 执行清理：现算清单（不信任 plan 时刻的列表）、复检
// 迁移归零、与迁移互斥，然后从新到旧删除除最近一份外的全部备份。
func (a *App) RunDagBackupCleanup() (dagBackupCleanupRunView, error) {
	if !dagMigrationRunning.CompareAndSwap(false, true) {
		return dagBackupCleanupRunView{}, fmt.Errorf("dag migration already running")
	}
	defer dagMigrationRunning.Store(false)

	view := dagBackupCleanupRunView{}
	if remaining := a.dagBackupRemainingMigrations(); remaining > 0 {
		return view, fmt.Errorf("dag backup cleanup: %d session(s) not yet migrated; migrate them first", remaining)
	}
	backups, err := dagBackupCleanupScan(dagMigrationProjectsRoot())
	if err != nil {
		return view, err
	}
	if len(backups) > 0 {
		view.KeptPath = backups[0].Path
	}
	// 从新到旧删（backups 已按时间倒序；[0] 是保留的最近一份）。
	for _, backup := range backups[1:] {
		if err := os.Remove(backup.Path); err != nil {
			view.Failed++
			view.Details = append(view.Details, fmt.Sprintf("%s: %v", backup.Path, err))
			continue
		}
		view.Deleted++
		view.DeletedBytes += backup.Bytes
	}
	return view, nil
}
