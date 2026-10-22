package main

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/botruntime"
	"reasonix/internal/sessioncatalog"
)

// 任务 1064：版本管理面板手动删除分支。
//
// 与 DeleteSession（删整个会话转录）和 DeleteRecoveryCopy（只删「主线已覆盖」
// 的冗余副本，后台清理的守卫版）不同，这里的删除对象是用户在面板里明确勾选
// 的一条分支——副本文件（含主线没有的独有内容）或 v3 日志里的一个非选中
// head。用户的判断本身就是覆盖证明：他看到了该分支的读数（条数/轮次/独有）
// 并决定丢弃它，所以不再要求覆盖证明，直接走用户显式选择对应的 forced 归档
// 路径——落进可恢复的 .trash 布局，与 1.34 时代「丢弃分支」的语义一致。
//
// 安全约束（1064 验收）：
//  ① 当前选中的分支（v3 的 selected head）拒绝删除——后端 RetireSessionHead
//     自带该约束；副本路径没有「选中」概念，前端负责禁用主线本身。
//  ② 回收语义：两路都进可恢复的 .trash（副本走 TrashRecoveryBranchForced 的
//     同一条协议；head 走 retire 标记，独有条目在下一次单写轮换时被裁掉，
//     轮换前可随时切回——比文件回收站更保守）。
//  ③ 关联引用：副本删除清自动会话映射 + 目录缓存 + DAG 图缓存；head 删除
//     让目录 catalog 重新对账，事件树刷新。
//  ④ 删除后列表即时刷新由前端 load() 负责（本函数只保证后端状态已变）。

// errSessionVersionNotDeletable names the refusal when the panel asks to delete
// the conversation's current version.
var errSessionVersionNotDeletable = errors.New("the current version cannot be deleted; switch to another version first")

// DeleteRecoveryBranch archives one recovery-copy branch the user has discarded
// in the versions panel. Unlike DeleteRecoveryCopy it does not require the
// branch to be covered by its parent: the user has seen its unique-content
// reading and decided, which is the explicit choice TrashRecoveryBranchForced
// exists for.
//
// dir resolves through the same known-session-dirs gate as every other panel
// write, so a path from the project tree (not just the active tab's directory)
// is accepted while an arbitrary filesystem path is still refused.
func (a *App) DeleteRecoveryBranch(path string) error {
	dir, sessionPath, err := a.sessionDirForPath(path)
	if err != nil {
		return friendlySessionFileError(err)
	}
	if !agent.LooksLikeRecoveryFilename(sessionPath) {
		return fmt.Errorf("not a recovery copy: only discarded branches may be deleted here")
	}
	if err := func() error {
		defer a.lockRuntimeMutation("delete-recovery-branch")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()
		// A copy open in a tab (read-only ones included) keeps its own DAG state;
		// archiving it under the tab's feet would leave the tab reading a file
		// that no longer exists in the directory listing.
		if a.sessionOpenInAnyTab(sessionPath) {
			return errSessionBusyElsewhere
		}
		if err := agent.TrashRecoveryBranchForced(sessionPath, dir); err != nil {
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				return errSessionBusyElsewhere
			}
			return err
		}
		return nil
	}(); err != nil {
		return friendlySessionFileError(err)
	}
	if err := botruntime.ForgetAutoSessionMappingsForPath(sessionPath); err != nil {
		slog.Warn("desktop: failed to clear auto bot session mapping", "err", err)
	}
	// Same cache hygiene as the guarded copy delete (task 715): an archived
	// branch must stop pinning its replayed graph.
	if freed, ok := agent.InvalidateSessionGraph(sessionPath); ok {
		slog.Info("desktop: invalidated dag graph cache on recovery branch delete",
			"path", sessionPath, "freed_bytes", freed)
	}
	a.removeSessionCatalogPath(sessionPath, "recovery_branch_deleted")
	a.emitProjectTreeChangedForSessionDirs(dir)
	a.invalidatePromptHistoryCache()
	return nil
}

// DeleteSessionVersionHead retires one non-current head of a schema-2 session
// log, as chosen in the versions panel.
//
// "Delete" for a head is the DAG's own retire marker, not a file move: the head
// stops being listed or selectable, and its exclusive events are dropped at the
// next single-writer rotation. Until that rotation the head can still be
// reached by replay with retired included, which is the 1064 recoverability
// promise in its strictest form - the panel cannot lose content the user might
// still want.
func (a *App) DeleteSessionVersionHead(mainPath, headID string) error {
	mainPath, headID = strings.TrimSpace(mainPath), strings.TrimSpace(headID)
	if mainPath == "" || headID == "" {
		return fmt.Errorf("session path and head id are required")
	}
	if _, _, err := a.sessionDirForPath(mainPath); err != nil {
		return friendlySessionFileError(err)
	}
	// The retire marker is appended under the session's own save locks
	// (agent.appendSessionHeadMarker), which the live runtime holds across
	// every save — so the same agent call is safe whether or not a tab has the
	// conversation open. A read-only tab still refuses: it cannot record the
	// marker through its own channel, and silently diverging from what the tab
	// shows would be worse than a refusal.
	if tab, ctrl := a.tabForSessionPath(mainPath); ctrl != nil {
		if a.tabIsReadOnly(tab) {
			return readOnlyChannelErr()
		}
	}
	if err := agent.RetireSessionHead(mainPath, headID); err != nil {
		return friendlySessionFileError(err)
	}
	dir := filepath.Dir(mainPath)
	if catalog := a.sessionCatalog.Load(); catalog != nil {
		target := sessioncatalog.DirectoryTarget{Path: dir, Scope: "global", WorkspaceRoot: ""}
		if err := catalog.ReconcileDirectory(a.bootContext(), target); err != nil {
			// The retire marker is already durable; a failed refresh only delays
			// the panel's next read, so report the refusal without undoing it.
			return errors.New("the version was deleted but the session catalog could not refresh")
		}
	}
	a.emitProjectTreeChangedForSessionDirs(dir)
	a.invalidatePromptHistoryCache()
	return nil
}
