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

// demotedMainlineDeleteVerdict 是刀③的降级主线通道判定（tl1415h）：一个非
// recovery 命名的转录能否经面板分支删除入口归档。
//
// 背景：tl1415f 把「采纳后被降级的扫描起点主线」纳入了面板「清理非主线副本」
// 入口（版本区文件形行——RecoveryCopiesSection cleanupNonMainCopies 的
// fileTargets）。它在 catalog 投影里是普通行（classifyRecoveryLineage 对
// 非 recovered 记录恒给 normal role）、磁盘上是普通命名文件（无 -recovery-
// 后缀），而本入口守卫只认 LooksLikeRecoveryFilename——入口放行的行形态与
// 守卫认的形态脱节，用户点到即拒「not a recovery copy」（2026-11-08 用户
// 现场实报）。取证与语义核对（tl1415 四轮取证 + tl1429d2 回收站同族归组语
// 义）裁定守卫扩认而非入口收窄：这条行正是「用户已显式采纳别的成员后失去
// 主线身份的原主线」，与 1064「用户的判断本身就是覆盖证明」同构。
//
// 三道防线保证「非副本不被误删」：
//  ① 组在——路径必须仍在 catalog 投影的 recovery 组里（recoveryLineageSelection
//     成立，组内有 recovered 组员）；普通独立会话（无组）拒绝。
//  ② 权威指别处——打开漏斗权威（recoveryAuthorityMainPathForOpen：权威#2
//     filing 偏好 / 权威#3 meta 旗标，与面板 Selected 同源）必须命中另一成
//     员；无显式采纳证据（无旗标无 filing）拒绝。权威解析刻意用 ForOpen 版
//     而非 recoveryAuthorityMainPath：后者权威#1 是 tab 当前路径，tab 停在
//     降级文件上时它自命权威、删除永远放不开——正是取证现场「面板标对、
//     打开选错」分裂的同款机制。
//  ③ 仍是现任主线拒绝——权威=自己时返回 currentMain，调用方给
//     errSessionVersionNotDeletable（下一步明确：先切到别的版本）。
//
// 返回 authority=命中的权威路径（空=拒绝，reason 说明原因）。
func (a *App) demotedMainlineDeleteVerdict(catalog *sessioncatalog.Catalog, sessionPath string) (authority string, currentMain bool, reason string) {
	if catalog == nil {
		return "", false, "no session catalog"
	}
	// 打开漏斗权威判定只需要路径（其内部第一步就是按路径重读投影行，与
	// continuePathForOpen 的读法同源）。
	path, _, detail, resolved := a.recoveryAuthorityForOpenVerdict(catalog, sessioncatalog.SessionRecord{Path: sessionPath})
	if !resolved || path == "" {
		if resolved && detail.Filed {
			return "", false, "the lineage has no explicit main-line choice"
		}
		return "", false, "path is not filed in a recovery lineage"
	}
	if sameRecoveryLineagePath(path, sessionPath) {
		return "", true, "still the current main line"
	}
	return path, false, ""
}

// DeleteRecoveryBranch archives one recovery-copy branch the user has discarded
// in the versions panel. Unlike DeleteRecoveryCopy it does not require the
// branch to be covered by its parent: the user has seen its unique-content
// reading and decided, which is the explicit choice TrashRecoveryBranchForced
// exists for.
//
// dir resolves through the same known-session-dirs gate as every other panel
// write, so a path from the project tree (not just the active tab's directory)
// is accepted while an arbitrary filesystem path is still refused.
// Every refusal and every success below leaves one slog line. tl1414 (2026-11-05
// 用户实测)：一次点确认后的删除在 desktop.log 里零痕迹——lockRuntimeMutation 与
// busy 拒全都不落日志，事后只能猜「没调用还是被拒」。诊断可观测性从这条链开始：
// 入口/两道静态门/占用拒/租约拒/trash 失败/成功，六处各留一行。
func (a *App) DeleteRecoveryBranch(path string) error {
	dir, sessionPath, err := a.sessionDirForPath(path)
	if err != nil {
		err = friendlySessionFileError(err)
		slog.Warn("desktop: recovery branch delete refused", "reason", "path outside known session dirs",
			"path", path, "err", err)
		return err
	}
	if !agent.LooksLikeRecoveryFilename(sessionPath) {
		// tl1415h 刀③：非 recovery 命名不再一票拒绝——tl1415f 的面板入口把
		// 「采纳后被降级的扫描起点主线」（普通命名、投影 normal 行）纳入了清
		// 理目标，守卫的文件名形态闸与入口的行形态判定脱节（用户现场：
		// 「not a recovery copy: only discarded branches may be deleted here」
		// 打回）。降级主线通道按 recovery 组语义重新对齐：组内 + 权威命中另
		// 一成员才放行（走同一条 TrashRecoveryBranchForced 的 .trash 可恢复
		// 协议）；无组、无权威、仍是现任主线一律维持拒绝。
		authority, currentMain, reason := a.demotedMainlineDeleteVerdict(a.sessionCatalog.Load(), sessionPath)
		if authority == "" {
			if currentMain {
				slog.Warn("desktop: recovery branch delete refused", "reason", reason, "path", sessionPath)
				return errSessionVersionNotDeletable
			}
			slog.Warn("desktop: recovery branch delete refused", "reason", "not a recovery copy: "+reason,
				"path", sessionPath)
			return fmt.Errorf("not a recovery copy: only discarded branches may be deleted here")
		}
		slog.Info("desktop: recovery branch delete admits demoted mainline",
			"path", sessionPath, "authority", authority)
	}
	if err := func() error {
		defer a.lockRuntimeMutation("delete-recovery-branch")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()
		// A copy open in a tab (read-only ones included) keeps its own DAG state;
		// archiving it under the tab's feet would leave the tab reading a file
		// that no longer exists in the directory listing. tl1414：面板行标签取同族
		// topic title，活 tab 自己的转录本也以副本行的形态出现——这正是用户点删
		// 后「静默无反应」的形态（活副本被拒，另外两份锁空闲），拒绝必须可见。
		if a.sessionOpenInAnyTab(sessionPath) {
			slog.Warn("desktop: recovery branch delete refused", "reason", "session open in a tab",
				"path", sessionPath)
			return errSessionBusyElsewhere
		}
		if err := agent.TrashRecoveryBranchForced(sessionPath, dir); err != nil {
			if errors.Is(err, agent.ErrSessionLeaseHeld) {
				slog.Warn("desktop: recovery branch delete refused", "reason", "session lease held by a live runtime",
					"path", sessionPath, "err", err)
				return errSessionBusyElsewhere
			}
			slog.Warn("desktop: recovery branch delete failed", "path", sessionPath, "err", err)
			return err
		}
		return nil
	}(); err != nil {
		return friendlySessionFileError(err)
	}
	slog.Info("desktop: recovery branch deleted", "path", sessionPath)
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
	// tl1280: the schema-2 retire marker (agent.RetireSessionHead) left with
	// the write-path slow lane; a head row is a leftover the catalog no longer
	// produces, so the retire is a no-op and the catalog still reconciles.
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
