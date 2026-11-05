package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"reasonix/internal/agent"
	"reasonix/internal/botruntime"
)

// 任务 1368 线2：版本副本面板的「删除选中对话」——整对话级清除。
//
// deleteHead 只能删单个 head、DeleteRecoveryBranch 只能删单条副本分支，都达
// 不到「这一整个对话（连同它的全部副本）不要了」的清除粒度。大量分叉的对话
// 通常是过时异常会话、工作已交接，用户要的是一次性把主文件、v3 版本（它们
// 住在主文件的事件日志里）、全部副本文件和侧车一起送进可恢复回收站。
//
// 复用两条既有归档链路，不造新机制：
//  ① 副本 → agent.TrashRecoveryBranchForced（1064 的 forced 归档路径：用户
//     在面板里看到读数后显式决定，不要求覆盖证明；副本侧车随
//     recoveryTrashSidecars 一起进 .trash，可还原）。
//  ② 主文件 → a.deleteSession（既有整会话删除链路：运行时拆除 +
//     trashSessionArtifacts 把主文件和全部侧车（含 v3 事件日志）移入回收站，
//     RestoreSession 可整体还原）。
//
// 顺序是先副本后主文件：副本归档是轻量 rename，失败重试成本低；若主文件
// 删除在中途失败，已归档的副本留在回收站（可还原），重试时副本枚举不到、
// 直接走主文件删除——幂等。反过来先删主文件会留下失去主线的 orphan 副本。

// DeleteConversationReport tells the panel what the call actually archived, so
// the outcome line can name both sides (main transcript + copies) instead of
// a bare "deleted".
type DeleteConversationReport struct {
	// MainTrashed reports whether the canonical transcript (with its v3 event
	// log and sidecars) moved to the recoverable trash. An orphan group (main
	// already gone) deletes its copies only, and this stays false.
	MainTrashed bool `json:"mainTrashed"`
	// CopiesArchived counts the recovery copies moved into the recoverable
	// .trash alongside their sidecars.
	CopiesArchived int `json:"copiesArchived"`
}

// DeleteConversationWithCopies archives one conversation and every recovery
// copy beside it.
//
// The path resolves through the same known-session-dirs gate as
// DeleteRecoveryBranch, so a path from the versions panel (any scanned session
// directory, not just the active tab's) is accepted while an arbitrary
// filesystem path is refused. The copies themselves are enumerated against the
// session path's own parent (tl1414) so the lookup and the trash gate see the
// scan face's spelling, not the gate's folded one.
func (a *App) DeleteConversationWithCopies(mainPath string) (DeleteConversationReport, error) {
	report := DeleteConversationReport{}
	_, sessionPath, err := a.sessionDirForPath(mainPath)
	if err != nil {
		return report, friendlySessionFileError(err)
	}

	// ① Archive the copies first (see ordering note above). Each copy walks
	// the same locked path as DeleteRecoveryBranch: dual-open refusal, forced
	// archive into the recoverable .trash, then the same cache hygiene. One
	// unreadable copy aborts the whole conversation delete — a partial
	// "conversation deleted but copies left behind" is exactly the state this
	// button exists to end.
	//
	// tl1414: enumerate and trash against the session path's own parent
	// spelling — the same string the panel scanned — not the gate-resolved
	// `dir`. On Windows the two spellings differ by case (scan face = on-disk
	// name, gate = WorkspaceSlug's folded form); a byMain lookup keyed by one
	// and probed with the other misses silently, so the copies survived a
	// "successful" delete with CopiesArchived=0.
	copyDir := filepath.Dir(sessionPath)
	byMain, _, err := recoveryCopiesByMain(copyDir)
	if err != nil {
		return report, friendlySessionFileError(fmt.Errorf("could not list recovery copies: %w", err))
	}
	for _, copy := range byMain[sessionPath] {
		copyPath := copy.Path
		if err := func() error {
			defer a.lockRuntimeMutation("delete-conversation-copy")()
			a.sessionRemovalMu.Lock()
			defer a.sessionRemovalMu.Unlock()
			if a.sessionOpenInAnyTab(copyPath) {
				return errSessionBusyElsewhere
			}
			if err := agent.TrashRecoveryBranchForced(copyPath, copyDir); err != nil {
				if errors.Is(err, agent.ErrSessionLeaseHeld) {
					return errSessionBusyElsewhere
				}
				return err
			}
			return nil
		}(); err != nil {
			return report, friendlySessionFileError(err)
		}
		if err := botruntime.ForgetAutoSessionMappingsForPath(copyPath); err != nil {
			slog.Warn("desktop: failed to clear auto bot session mapping", "err", err)
		}
		if freed, ok := agent.InvalidateSessionGraph(copyPath); ok {
			slog.Info("desktop: invalidated dag graph cache on conversation delete",
				"path", copyPath, "freed_bytes", freed)
		}
		a.removeSessionCatalogPath(copyPath, "conversation_deleted_with_copies")
		report.CopiesArchived++
	}

	// ② The main transcript: the existing whole-session delete carries the
	// runtime teardown, the trash move of the transcript plus every sidecar
	// (the v3 versions live inside the event log, so they leave together),
	// the topic convergence and the tree refresh. An orphan group (main
	// already gone) has nothing left to remove here.
	if _, err := os.Stat(sessionPath); err != nil {
		if !os.IsNotExist(err) {
			return report, friendlySessionFileError(err)
		}
	} else {
		if err := a.deleteSession(sessionPath); err != nil {
			return report, err
		}
		report.MainTrashed = true
	}
	return report, nil
}
