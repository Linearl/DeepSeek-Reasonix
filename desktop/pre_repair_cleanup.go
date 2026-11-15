package main

// tl1603: 改写前备份（*.pre-repair-*）的手动清理桥。
//
// tl1572 交付的只有「查看」（面板分区只读枚举）；用户实测存量 168 条 3.1GB
// 只能看不能清。本文件在只读面之上补清理能力，设计约束=tl1116「无备份不
// 减肥」判据不许破坏：pre-repair 是 Diet/改写的安全退路，只可删「非唯一退
// 路」的——E5 语义两道闸（§ DeletePreRepairBackup）：
//
//  1. 非唯一退路（stat 级，列表初判与删除闸同一判据）：宿主转录还在（活会
//     话的改写前快照，内容另有正本），或宿主已走删除/归档链（其转录/随行
//     备份躺在 <dir>/.trash 桶里，preRepairTrashFamilyHit）——两条都不成立
//     =这份备份是该对话仅存的完整副本，拒绝（tl1116 禁区）。
//  2. 主对话健康（重放级，只在删除时执行）：活宿主经 InspectSessionHealth
//     体检不过（超窗/加载超时）时拒绝——修复失败会话的改写前快照是它唯一
//     的回头路，删退路正是 tl1116 禁止的「无备份减肥」。「已合并进主线」形
//     态由本闸覆盖（合并的前提本就是宿主是权威线）；「超期」形态不做自动
//     判定（non-goal：先给手动入口，过期策略后议）。
//
// 回收语义（任务 855 铁律）：删除必须经平台 API 送进系统回收站（trashWorkspacePaths
// → SHFileOperation FOF_ALLOWUNDO / gio trash），禁止 os.RemoveAll 裸删、
// 禁止自建 .trash 冒充；恢复走 OS 回收站原位还原。
//
// 并发面：备份树永不作为会话加载（一切枚举排除），自己没有 runtime/lease；
// 但删除/归档链会把备份当宿主侧车整批搬进回收站桶（store.SessionTrashSidecars
// 的 glob），所以整段删除持 a.sessionRemovalMu 与那些链互斥——闸与 rename
// 之间不会穿插搬桶（搬桶链都持同一把锁）；延迟搬桶 goroutine（teardown 超时
// 形态）不持锁，与它撞上的表现是 rename ENOENT=诚实失败行，零数据损失。

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// preRepairHealthInspect 是健康闸的读数入口。Var 仅为测试缝（workspaceTrashPlatform
// 同一先例）：健康闸钉「删除前必须体检活宿主」这条路径，拒绝分支用假报告喂。
var preRepairHealthInspect = agent.InspectSessionHealth

// E5 删除闸的拒绝 sentinel。错误文案走「下一步已存在」纪律： sole-copy 的
// 下一步=先走应用内删除/归档链（它们会连备份一起搬走）；unhealthy 的下一步
// =先修复宿主（面板本有修复入口）。
var errPreRepairSoleCopy = errors.New("this backup is the conversation's only surviving copy — archive or delete the conversation in Reasonix first, and this backup will follow it")

// preRepairCleanupFailureReason 是批量回执的稳定拒绝令牌（wire 契约：前端按
// 令牌出三语文案；Detail 带原始细节）。
const (
	preRepairReasonInvalid        = "invalid"         // 路径形态/越界（永远不该发生，防御面）
	preRepairReasonGone           = "gone"            // 枚举后已被别的链搬走
	preRepairReasonSoleCopy       = "sole-copy"       // E5 闸①：仅存副本
	preRepairReasonOwnerUnhealthy = "owner-unhealthy" // E5 闸②：宿主体检不过
	preRepairReasonTrashFailed    = "trash-failed"    // 平台回收站调用失败
)

// preRepairBackupBytes 是备份目录的体量读数（一层平铺 stat 和，与枚举同一口径）。
func preRepairBackupBytes(dir string) int64 {
	var total int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// preRepairBackupLabel 是删除回执里的展示名（与枚举同一来源：备份内 meta
// 标题优先，回退 stem）。
func preRepairBackupLabel(backupDir, stem string) string {
	label := recoveryDisplayName(filepath.Join(backupDir, stem+store.SessionTranscriptSuffix))
	if label == "" {
		label = stem
	}
	return label
}

// preRepairCleanupGate 是 E5 删除闸：校验 backupDir 可删，通过时返回 stem
// 与体量。所有拒绝都各留一行 slog（tl1414 纪律：拒绝必须可归因）。
//
// healthCache 是批量面的宿主健康读数缓存（owner→healthy；nil=单删不缓存）——
// 同一会话的多份备份在批内只重放一次宿主日志。
//
// 路径闸与 validateSessionPath 同一纪律：会话目录包含关系（Rel 不越界）+
// symlink 解析后的二次包含校验；目录名必须能拆出 <stem>.pre-repair-<stamp>
// 形态。任意文件系统路径在这里全部拒绝。
func (a *App) preRepairCleanupGate(backupDir string, healthCache map[string]bool) (stem string, bytes int64, err error) {
	backupDir = filepath.Clean(strings.TrimSpace(backupDir))
	if backupDir == "." || filepath.VolumeName(backupDir) == backupDir {
		slog.Warn("desktop: pre-repair delete refused", "reason", "empty path")
		return "", 0, fmt.Errorf("no backup path given")
	}
	base := filepath.Base(backupDir)
	stem, _, ok := preRepairStemOf(base)
	if !ok {
		slog.Warn("desktop: pre-repair delete refused", "reason", "not a pre-repair backup name", "path", backupDir)
		return "", 0, fmt.Errorf("not a pre-repair backup directory: %s", base)
	}
	parent := filepath.Dir(backupDir)
	info, statErr := os.Lstat(backupDir)
	if statErr != nil {
		slog.Warn("desktop: pre-repair delete refused", "reason", "backup vanished", "path", backupDir, "err", statErr)
		return "", 0, fmt.Errorf("backup no longer exists: %s", base)
	}
	if !info.IsDir() {
		slog.Warn("desktop: pre-repair delete refused", "reason", "backup is not a directory", "path", backupDir)
		return "", 0, fmt.Errorf("not a pre-repair backup directory: %s", base)
	}
	// 已知会话目录包含闸（宿主文件在哪，备份就在哪；列表只见于这些根）。
	if !a.pathInsideKnownSessionDirs(parent, backupDir) {
		slog.Warn("desktop: pre-repair delete refused", "reason", "outside known session dirs", "path", backupDir)
		return "", 0, fmt.Errorf("backup path outside known session directories")
	}
	// ── E5 闸①：非唯一退路 ──
	owner := filepath.Join(parent, stem+store.SessionTranscriptSuffix)
	ownerLive := false
	if oinfo, oerr := os.Lstat(owner); oerr == nil && !oinfo.IsDir() {
		ownerLive = true
	}
	if !ownerLive && !preRepairTrashFamilyHit(parent, stem, map[string]map[string]bool{}) {
		slog.Warn("desktop: pre-repair delete refused", "reason", "sole surviving copy", "path", backupDir, "owner", owner)
		return "", 0, errPreRepairSoleCopy
	}
	// ── E5 闸②：主对话健康（活宿主才体检；桶里宿主的内容已随删除链搬走，
	// 体检无从谈起也不必谈）──
	if ownerLive {
		var report agent.SessionHealthReport
		if cached, hit := healthCache[owner]; hit {
			// 批内缓存只存 verdict；缓存命中即视为健康通过（未通过的宿主在第
			// 一份备份上就已经拒绝，带 codes 的日志已经落过，这里不重放）。
			if !cached {
				return "", 0, fmt.Errorf("the owner session is not healthy (cached verdict) — this backup is its way back, repair the session first: %s", base)
			}
		} else {
			report = preRepairHealthInspect(owner, agent.HealthInspectOptions{})
			if healthCache != nil {
				healthCache[owner] = report.Healthy
			}
			if !report.Healthy {
				codes := make([]string, 0, len(report.Issues))
				for _, issue := range report.Issues {
					codes = append(codes, issue.Code)
				}
				slog.Warn("desktop: pre-repair delete refused", "reason", "owner session unhealthy",
					"path", backupDir, "owner", owner, "codes", strings.Join(codes, ","))
				return "", 0, fmt.Errorf("the owner session is not healthy (%s) — this backup is its way back, repair the session first: %s",
					strings.Join(codes, ","), base)
			}
		}
	}
	return stem, preRepairBackupBytes(backupDir), nil
}

// pathInsideKnownSessionDirs 判 target 是否落在某个已知会话目录内（含 symlink
// 解析后的二次校验，validateSessionPath 同一纪律）。
func (a *App) pathInsideKnownSessionDirs(parent, target string) bool {
	if !pathContainedBy(target, parent) {
		return false
	}
	for _, dir := range a.knownSessionDirs() {
		if pathContainedBy(parent, dir) {
			return true
		}
	}
	return false
}

// pathContainedBy 报告 path 是否位于 root 之内（含相等——备份目录的直接宿主
// 就是会话目录本身，parent==dir 是合法形态；validateSessionPath 的 rel=="."
// 排除针对的是「会话文件==目录」这个不可能形态，目录对目录的包含语义里相等
// 成立）。两侧都做 symlink 解析，解析失败的侧退回原拼写。
func pathContainedBy(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		realPath = path
	}
	rel, err = filepath.Rel(realRoot, realPath)
	return err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)
}

// trashPreRepairBackupDir 是删除执行的单点：E5 闸通过后把整个备份目录送进
// OS 回收站。锁（a.sessionRemovalMu）由调用方持有。
func (a *App) trashPreRepairBackupDir(backupDir string) error {
	if err := trashWorkspacePaths([]string{backupDir}); err != nil {
		slog.Warn("desktop: pre-repair delete failed at the recycle bin", "path", backupDir, "err", err)
		return err
	}
	slog.Info("desktop: pre-repair backup deleted to recycle bin", "path", backupDir)
	return nil
}

// DeletePreRepairBackup 删单个改写前备份目录（面板行内「删除」按钮）。先过
// E5 两道闸（非唯一退路+宿主健康），通过后整目录送 OS 回收站（可从系统回收
// 站原位还原）。
func (a *App) DeletePreRepairBackup(dir string) error {
	backupDir := filepath.Clean(strings.TrimSpace(dir))
	if backupDir == "." {
		return fmt.Errorf("no backup path given")
	}
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	gatedStem, _, err := a.preRepairCleanupGate(backupDir, nil)
	if err != nil {
		return friendlySessionFileError(err)
	}
	if err := a.trashPreRepairBackupDir(backupDir); err != nil {
		return friendlySessionFileError(err)
	}
	slog.Info("desktop: pre-repair backup deleted", "label", preRepairBackupLabel(backupDir, gatedStem), "path", backupDir)
	return nil
}

// PreRepairCleanupFailure 是批量清理回执的一行拒绝（reason=稳定令牌，前端
// 三语；detail=原始细节，诊断用）。
type PreRepairCleanupFailure struct {
	Dir    string `json:"dir"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// PreRepairBatchCleanupView 是批量清理的回执：几条进了回收站（含体量）、
// 几条被拒（逐条带令牌）。Failures 永不为 nil（nil 经 Wails 序列化成 JSON
// null，前端 .filter 直接崩——dag 迁移面同款钉）。
type PreRepairBatchCleanupView struct {
	Trashed      int                       `json:"trashed"`
	TrashedBytes int64                     `json:"trashedBytes"`
	Failed       int                       `json:"failed"`
	Failures     []PreRepairCleanupFailure `json:"failures"`
}

// DeletePreRepairBackups 批量清理改写前备份（面板「清理所选/清理全组」）。
// 逐条过同一 E5 闸（活宿主健康读数在批内按宿主缓存——同一会话的多份备份只
// 重放一次），逐条送回收站：一条被拒/失败不拖累其余，回执逐条可归因。
func (a *App) DeletePreRepairBackups(dirs []string) (PreRepairBatchCleanupView, error) {
	view := PreRepairBatchCleanupView{Failures: []PreRepairCleanupFailure{}}
	// 去重保序：前端勾选集不会有重复，防御面仍按集合语义收口。
	seen := map[string]bool{}
	ordered := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = filepath.Clean(strings.TrimSpace(dir))
		if dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		ordered = append(ordered, dir)
	}
	if len(ordered) == 0 {
		return view, nil
	}
	slog.Info("desktop: pre-repair batch cleanup begin", "requested", len(ordered))
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	// 健康读数批内缓存：同一宿主的多份备份只体检一次（一次重放一份日志）。
	health := map[string]bool{}
	for _, backupDir := range ordered {
		base := filepath.Base(backupDir)
		stem, _, ok := preRepairStemOf(base)
		if !ok {
			view.Failed++
			view.Failures = append(view.Failures, PreRepairCleanupFailure{
				Dir: backupDir, Label: base, Reason: preRepairReasonInvalid,
			})
			continue
		}
		label := preRepairBackupLabel(backupDir, stem)
		_, bytes, err := a.preRepairCleanupGate(backupDir, health)
		if err != nil {
			view.Failed++
			view.Failures = append(view.Failures, PreRepairCleanupFailure{
				Dir: backupDir, Label: label, Reason: preRepairFailureReason(err), Detail: err.Error(),
			})
			continue
		}
		if err := a.trashPreRepairBackupDir(backupDir); err != nil {
			view.Failed++
			view.Failures = append(view.Failures, PreRepairCleanupFailure{
				Dir: backupDir, Label: label, Reason: preRepairReasonTrashFailed, Detail: err.Error(),
			})
			continue
		}
		view.Trashed++
		view.TrashedBytes += bytes
	}
	slog.Info("desktop: pre-repair batch cleanup done",
		"requested", len(ordered), "trashed", view.Trashed, "failed", view.Failed,
		"bytes", view.TrashedBytes)
	return view, nil
}

// preRepairFailureReason 把 E5 闸的错误映射成回执令牌。
func preRepairFailureReason(err error) string {
	switch {
	case errors.Is(err, errPreRepairSoleCopy):
		return preRepairReasonSoleCopy
	case strings.Contains(err.Error(), "owner session is not healthy"):
		return preRepairReasonOwnerUnhealthy
	case strings.Contains(err.Error(), "no longer exists"):
		return preRepairReasonGone
	default:
		return preRepairReasonInvalid
	}
}
