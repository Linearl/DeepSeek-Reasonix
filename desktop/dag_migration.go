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
