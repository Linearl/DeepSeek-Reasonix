package main

// tl1787 件3/B：改写前备份（*.pre-repair-*）的「合并回主线」桥。
//
// 需求原话：pre-repair 没有提供合并选项，希望实现分叉后的合并，而不是历史
// 消息被折叠后就永久丢失。执行核在 agent.GraftSessionExternalPrefix（1.34
// 合并体系同一 graft 原语：SessionContentPrefixGap 轮次对齐 + graftPrefixOntoLines
// 只增不改 + compactSessionEventLog 索引折叠，promote-prefix-graft 同款），
// 纯本地数据操作零 token；本文件只做四件接线必需的事：
//  1. 路径闸（与删除闸同一纪律：备份形态校验 + knownSessionDirs 包含校验）；
//  2. 目标闸：只允许合并进活宿主——宿主不在（孤儿备份，无可回主线）与宿主
//     已归档（躺进 .trash 桶，先还原对话再合并）都拒绝并给人话原因；
//  3. 锁面与 synthesize-session-heads 同款（runtime 变更互斥 + sessionRemovalMu，
//     与删除/归档链不穿插）；
//  4. 收尾与合成同款（catalog 对账 + 树事件 + 开着的标签页经
//     session:consolidated 事件刷新——tl1402 的原机制，不新造刷新通道）。
//
// non-goals：不改「备份树永不作为会话加载」约束（备份只被当数据读）；不做
// 活宿主健康闸（合并只增不改，快照装载 fail-closed 已拦损坏日志；健康闸的
// 职责是删除保护，合并不是删除）。

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/sessioncatalog"
	"reasonix/internal/store"
)

// PreRepairMergeView 是合并回执：接回多少条、合并后主线条数（「合并后历史
// 可查」的对屏核对数）。merged=false = 主线已包含备份全部历史（幂等出口）。
type PreRepairMergeView struct {
	Merged        bool   `json:"merged"`
	Grafted       int    `json:"grafted"`
	OwnerPath     string `json:"ownerPath"`
	AfterMessages int    `json:"afterMessages"`
}

// MergePreRepairBackup 把 dir（备份目录 <dir>/<stem>.pre-repair-<stamp>）里
// 的主线转录副本合并回旁侧活宿主主线。
func (a *App) MergePreRepairBackup(dir string) (PreRepairMergeView, error) {
	view := PreRepairMergeView{}
	backupDir := filepath.Clean(strings.TrimSpace(dir))
	if backupDir == "." || filepath.VolumeName(backupDir) == backupDir {
		return view, fmt.Errorf("no backup path given")
	}
	base := filepath.Base(backupDir)
	stem, _, ok := preRepairStemOf(base)
	if !ok {
		return view, fmt.Errorf("not a pre-repair backup directory: %s", base)
	}
	parent := filepath.Dir(backupDir)
	if !a.pathInsideKnownSessionDirs(parent, backupDir) {
		slog.Warn("desktop: pre-repair merge refused", "reason", "outside known session dirs", "path", backupDir)
		return view, fmt.Errorf("backup path outside known session directories")
	}
	// 备份内主线转录副本（枚举面同一来源：<stem>.jsonl）。
	transcript := filepath.Join(backupDir, stem+store.SessionTranscriptSuffix)
	if info, err := os.Stat(transcript); err != nil || info.IsDir() {
		return view, fmt.Errorf("this backup has no main transcript inside to merge: %s", base)
	}
	// 目标闸：只合并进活宿主。owner 在桶里=已归档（先还原对话再合并）；
	// owner 不在=孤儿备份（没有可回的主线）。
	owner := filepath.Join(parent, stem+store.SessionTranscriptSuffix)
	if oinfo, oerr := os.Lstat(owner); oerr != nil || oinfo.IsDir() {
		return view, fmt.Errorf("the original conversation no longer exists — an orphan backup has no main line to merge into")
	}
	if preRepairOwnerArchived(owner) {
		return view, fmt.Errorf("the original conversation is archived (in the recoverable trash) — restore it from the trash first, then merge")
	}
	ownerPath := owner
	if _, sessionPath, err := a.sessionDirForPath(ownerPath); err == nil {
		ownerPath = sessionPath
	}
	slog.Info("desktop: pre-repair merge begin", "backup", backupDir, "owner", ownerPath)
	report, runErr := func() (agent.GraftExternalPrefixReport, error) {
		defer a.lockRuntimeMutation("merge-pre-repair-backup")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()
		return agent.GraftSessionExternalPrefix(ownerPath, transcript)
	}()
	if runErr != nil {
		return view, friendlySessionFileError(runErr)
	}
	view = PreRepairMergeView{
		Merged:        report.Grafted > 0,
		Grafted:       report.Grafted,
		OwnerPath:     ownerPath,
		AfterMessages: report.AfterMessages,
	}
	// 收尾与 synthesize-session-heads 同款：catalog 对账（失败只延迟下次
	// 读，不回滚合并）、树事件（侧栏预览刷新）、开着的标签页经
	// session:consolidated 重载转录投影（tl1402 机制，前端零新订阅）。
	if catalog := a.sessionCatalog.Load(); catalog != nil {
		target := sessioncatalog.DirectoryTarget{Path: parent, Scope: "global", WorkspaceRoot: ""}
		if err := catalog.ReconcileDirectory(a.bootContext(), target); err != nil {
			slog.Warn("desktop: pre-repair merge catalog refresh failed", "err", err)
		}
	}
	a.emitProjectTreeChangedForSessionDirs(parent)
	a.invalidatePromptHistoryCache()
	a.emitRuntimeEvent("session:consolidated", map[string]any{
		"mainPath":      ownerPath,
		"promoted":      false,
		"trashed":       []string{},
		"afterMessages": view.AfterMessages,
		"afterTurns":    0,
		"reason":        "pre-repair-merge",
	})
	slog.Info("desktop: pre-repair backup merged into the main line", "grafted", view.Grafted, "owner", ownerPath)
	return view, nil
}
