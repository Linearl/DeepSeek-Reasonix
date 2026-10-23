package main

// tl 1097 — 日志分级保留（B 方案）：全量日志短期保留不变，WARN/ERROR 告警
// 日志单独落盘 + 可配置的时间/大小双维度清理。
//
// 落点：实验室 → 可观测性 → 日志管理（两个 segment 档位 + 当前占用 + 打开目录
// 按钮）。三档位语义（用户 2026-10-23 定稿）：
//   - 时间档：只作用于告警日志（7/30/90/永久）；全量日志固定 25 份 ≈ 2 天，
//     不受任何配置影响（desktopLogMaxBackups 常量，逐字节不变）；
//   - 大小档：只作用于告警日志（100/300/1000MB/无限制）；
//   - 两档冲突（时间未到但空间已满）以**大小档为准**——删到总量 ≤ 硬顶。
//
// 为什么拆文件而不是「单文件+按级别跳过」：滚动日志是整块 4MB 追加文件，无法
// 部分删除；拆出一个仅收 WARN/ERROR 的小子集（体积小 1~2 个数量级）后，
// 时间/大小清理都变成对独立文件的删除操作，简单且可靠。
//
// 不误删边界：清理只碰 desktop-alerts.log 及其滚动份（desktop-alerts.log.N），
// 不碰 repair/、diagnostics/、inbox-recovery.jsonl（tl 1094 自带 5MB×2 固定
// 上限）、会话数据、或用户放进 logs/ 目录的任何其他文件。

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
)

const (
	// desktopAlertLogFileName 与 desktop.log 同目录，前缀不同——两条清理链
	// 互不相认。
	desktopAlertLogFileName = "desktop-alerts.log"
	// desktopAlertLogMaxSizeBytes 单份 1MB（WARN 稀少，小份足够；全量是
	// 4MB 不变）。
	desktopAlertLogMaxSizeBytes = 1 << 20
	// desktopAlertLogMaxBackups 单独设一个份数上限，防止「永久+无限制」以外
	// 的配置下份数失控增长；实际删除由时间/大小两档驱动，本上限是最后防线
	// （保留最近的 N 份）。1200 份 × 1MB ≈ 1.2GB，远超最大档 1GB + 时间档
	// 90 天的可达组合，正常永远不会触到。
	desktopAlertLogMaxBackups = 1200
)

// desktopAlertLogRetentionDaysEffective 把配置值翻译成「保留天数」：
// 0 = 内置默认 7 天；-1 = 永久（0 天保留期 = 不按时间删）；其余值即天数。
func desktopAlertLogRetentionDaysEffective(cfg *config.Config) int {
	if cfg == nil {
		return 7
	}
	switch days := cfg.Desktop.DesktopAlertLogRetentionDays; {
	case days == -1:
		return 0 // 永久：不按时间删
	case days == 0:
		return 7 // 内置默认
	default:
		return days
	}
}

// desktopAlertLogMaxTotalBytesEffective 把配置值翻译成「总量硬顶字节」：
// 0 = 内置默认 100MB；-1 = 无限制（-1 传给 pruneAlertLogFamily 时跳过大小
// 清理）；其余值即 MB。
func desktopAlertLogMaxTotalBytesEffective(cfg *config.Config) int64 {
	if cfg == nil {
		return 100 << 20
	}
	switch mb := cfg.Desktop.DesktopAlertLogMaxTotalMB; {
	case mb == -1:
		return -1 // 无限制
	case mb == 0:
		return 100 << 20 // 内置默认
	default:
		return int64(mb) << 20
	}
}

// alertLogFamilyFile 描述一个属于告警日志家族的文件（当前份或滚动份）。
type alertLogFamilyFile struct {
	path    string
	size    int64
	modTime time.Time
	// rolled 为 true 表示 .N 滚动份（可删）；false 表示当前活跃文件（不可删，
	// 由 rollingLogWriter 持有句柄）。
	rolled bool
}

// listAlertLogFamily 列出 dir 下的告警日志家族文件（desktop-alerts.log +
// desktop-alerts.log.N）。只认这两类精确前缀，不递归、不碰其他文件。
func listAlertLogFamily(dir string) ([]alertLogFamilyFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []alertLogFamilyFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		var rolled bool
		switch {
		case name == desktopAlertLogFileName:
			rolled = false
		case strings.HasPrefix(name, desktopAlertLogFileName+"."):
			// 滚动份必须是纯数字后缀——不匹配 .tmp/.bak 等同前缀杂件。
			suffix := strings.TrimPrefix(name, desktopAlertLogFileName+".")
			if _, err := strconv.Atoi(suffix); err != nil {
				continue
			}
			rolled = true
		default:
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, alertLogFamilyFile{
			path:    filepath.Join(dir, name),
			size:    info.Size(),
			modTime: info.ModTime(),
			rolled:  rolled,
		})
	}
	return files, nil
}

// pruneAlertLogFamily 执行双维度清理，返回被删除的文件路径列表。
//
// 顺序（任务书「时间 + 大小」双维度，任一触发即清理；两档同时紧张时以大小
// 档为准）：
//  1. 先删所有 modTime 超过 retentionDays 的滚动份（时间档）；
//  2. 再删最老的滚动份直到总量 ≤ maxTotalBytes（大小档）。
//
// 当前活跃文件（非滚动份）永不删除——它由 rollingLogWriter 持有句柄，删除
// 会破坏追加语义。它计入总量但不计入可删集合（若单当前文件就超硬顶，说明
// 配置极小，此时不做任何删除——文件本身就是唯一的合法状态）。
func pruneAlertLogFamily(dir string, retentionDays int, maxTotalBytes int64, now time.Time) ([]string, error) {
	files, err := listAlertLogFamily(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	// Step 1: 时间档。
	if retentionDays > 0 {
		cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
		kept := files[:0]
		for _, f := range files {
			if f.rolled && f.modTime.Before(cutoff) {
				if err := os.Remove(f.path); err == nil {
					removed = append(removed, f.path)
					continue
				}
			}
			kept = append(kept, f)
		}
		files = kept
	}
	// Step 2: 大小档（含两档冲突时以大小为准）。按 modTime 从旧到新排序，
	// 从最老的滚动份开始删。
	if maxTotalBytes >= 0 {
		var total int64
		for _, f := range files {
			total += f.size
		}
		if total > maxTotalBytes {
			sort.SliceStable(files, func(i, j int) bool {
				return files[i].modTime.Before(files[j].modTime)
			})
			for _, f := range files {
				if total <= maxTotalBytes {
					break
				}
				if !f.rolled {
					continue
				}
				if err := os.Remove(f.path); err == nil {
					total -= f.size
					removed = append(removed, f.path)
				}
			}
		}
	}
	return removed, nil
}

// alertLogDirPath 解析 logs/desktop 目录路径（与全量日志同目录）。
func alertLogDirPath() string {
	return filepath.Join(config.MemoryUserDir(), desktopLogDirName, desktopLogSubDir)
}

// alertLogDirUsage 汇总告警日志家族的当前占用（字节数 + 文件数），供
// 「当前占用：X MB（N 个文件）」展示。
func alertLogDirUsage(dir string) (bytes int64, count int) {
	files, err := listAlertLogFamily(dir)
	if err != nil {
		return 0, 0
	}
	for _, f := range files {
		bytes += f.size
		count++
	}
	return bytes, count
}

// desktopAlertLogWriter 读取全局告警 writer 句柄（installDesktopAlertLogging
// 已装好时非 nil；否则返回 nil 表示告警路径未启用，split handler 退化为全量
// handler 单链）。
func desktopAlertLogWriter() *alertLogLevelWriter {
	desktopAlertLog.mu.Lock()
	defer desktopAlertLog.mu.Unlock()
	if desktopAlertLog.w == nil {
		return nil
	}
	return &alertLogLevelWriter{w: desktopAlertLog.w, state: &desktopAlertLog}
}

// alertLogLevelWriter 实现 io.Writer 面（供 slog NewTextHandler 使用），但
// 只接受 WARN/ERROR 级别的行——低于 WARN 的行直接丢弃（返回 len(p), nil，
// 让上游日志调用不报错）。level>=WARN 的行转写给底层 rollingLogWriter
// （复用既有类型，文件名不同）。
type alertLogLevelWriter struct {
	w     *rollingLogWriter
	state *alertLogState
}

func (a *alertLogLevelWriter) Write(p []byte) (int, error) {
	if a.w == nil {
		return len(p), nil
	}
	if !alertLineIsWarnOrError(p) {
		return len(p), nil // 静默丢弃，不算错误
	}
	if a.state != nil {
		a.state.maybePruneAlertLog()
	}
	return a.w.Write(p)
}

// alertLineIsWarnOrError 判定一条 slog TextHandler 行是否是 WARN 或 ERROR。
// TextHandler 的 level 键固定为 "level="（slog.LevelKey 常量），值是
// Level.String() 的结果（WARN / ERROR）。判据：行首是 time=（TextHandler 固定
// 顺序 time → level → msg → 其他），紧跟 level=，值恰为 WARN 或 ERROR。
func alertLineIsWarnOrError(p []byte) bool {
	// 允许行首有少量空白（防御式），再找 "level="。
	line := strings.TrimLeft(string(p), " \t")
	if !strings.HasPrefix(line, "time=") {
		return false
	}
	// 跳过 time= 值（RFC3339 带毫秒，无空格）。
	rest := line[len("time="):]
	if idx := strings.IndexByte(rest, ' '); idx >= 0 {
		rest = rest[idx+1:]
	} else {
		return false
	}
	if !strings.HasPrefix(rest, "level=") {
		return false
	}
	rest = rest[len("level="):]
	if idx := strings.IndexByte(rest, ' '); idx >= 0 {
		rest = rest[:idx]
	}
	return rest == "WARN" || rest == "ERROR"
}

// alertLogState 持有告警 writer 句柄与最近一次清理时间（防止高频 WARN 爆发
// 时每次写都触发目录扫描）。
type alertLogState struct {
	mu            sync.Mutex
	w             *rollingLogWriter
	lastPruneAt   time.Time
	pruneInterval time.Duration
}

var desktopAlertLog alertLogState

// installDesktopAlertLogging 在 installDesktopLogging 之后调用：把 WARN/
// ERROR 分流 writer 挂上。配置读取失败时静默跳过（与 installDesktopLogging
// 同款「诊断不阻塞启动」口径）。
func installDesktopAlertLogging() {
	dir := alertLogDirPath()
	w := newRollingLogWriterWithMax(filepath.Join(dir, desktopAlertLogFileName), desktopAlertLogMaxSizeBytes, desktopAlertLogMaxBackups)
	if err := w.ensureLocked(); err != nil {
		return
	}
	desktopAlertLog.mu.Lock()
	if desktopAlertLog.w != nil {
		_ = desktopAlertLog.w.Close()
	}
	desktopAlertLog.w = w
	desktopAlertLog.lastPruneAt = time.Time{}
	desktopAlertLog.pruneInterval = time.Minute
	desktopAlertLog.mu.Unlock()
	// 启动即做一次清理（把上次会话遗留的超期/超量文件清掉）。
	pruneAlertLogNow()
}

// pruneAlertLogNow 立即执行一次双维度清理（供安装时、设置变更后调用）。
func pruneAlertLogNow() {
	cfg, err := config.LoadUserConfigReadOnly()
	if err != nil {
		return
	}
	desktopAlertLog.mu.Lock()
	desktopAlertLog.lastPruneAt = time.Now()
	desktopAlertLog.mu.Unlock()
	_, _ = pruneAlertLogFamily(
		alertLogDirPath(),
		desktopAlertLogRetentionDaysEffective(cfg),
		desktopAlertLogMaxTotalBytesEffective(cfg),
		time.Now(),
	)
}

// maybePruneAlertLog 由 alertLogWriter 在写入前调用，节流触发清理。
func (s *alertLogState) maybePruneAlertLog() {
	s.mu.Lock()
	interval := s.pruneInterval
	if interval <= 0 {
		interval = time.Minute
	}
	if time.Since(s.lastPruneAt) < interval {
		s.mu.Unlock()
		return
	}
	s.lastPruneAt = time.Now()
	s.mu.Unlock()
	pruneAlertLogNow()
}

// compositeSlogHandler 把一条 slog 记录同时交给全量 handler 与告警 handler。
// Enabled 取两者任一为真（告警 handler 的 Enabled 只在 WARN+ 为真）。
type compositeSlogHandler struct {
	full  slog.Handler
	alert slog.Handler
}

func (h *compositeSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.full.Enabled(ctx, level) || h.alert.Enabled(ctx, level)
}

func (h *compositeSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	// 全量 handler 必须处理（它覆盖所有级别）；告警 handler 自己按 Enabled
	// 过滤（低级别不写）。为保持「全量逐字节不变」，全量先写。
	errFull := h.full.Handle(ctx, r)
	errAlert := h.alert.Handle(ctx, r)
	if errFull != nil {
		return errFull
	}
	return errAlert
}

func (h *compositeSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &compositeSlogHandler{full: h.full.WithAttrs(attrs), alert: h.alert.WithAttrs(attrs)}
}

func (h *compositeSlogHandler) WithGroup(name string) slog.Handler {
	return &compositeSlogHandler{full: h.full.WithGroup(name), alert: h.alert.WithGroup(name)}
}
