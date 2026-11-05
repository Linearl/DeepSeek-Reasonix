package main

import (
	"fmt"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
)

func (a *App) handleTabSessionRecovered(tab *WorkspaceTab) func(control.SessionRecoveryInfo) error {
	return func(info control.SessionRecoveryInfo) error {
		if strings.TrimSpace(info.RecoveryPath) == "" {
			return nil
		}
		if err := copyPinnedContextState(info.OriginalPath, info.RecoveryPath); err != nil {
			return fmt.Errorf("copy pinned context to recovery session: %w", err)
		}
		pinnedState, err := loadPinnedContextState(info.RecoveryPath)
		if err != nil {
			return fmt.Errorf("load recovery pinned context: %w", err)
		}
		if err := a.handoffTabRecoveryLease(tab, info.RecoveryPath); err != nil {
			return err
		}
		meta := info.Meta
		scope := strings.TrimSpace(meta.Scope)
		if scope != "project" {
			scope = "global"
		}
		workspaceRoot := strings.TrimSpace(meta.WorkspaceRoot)
		if scope == "global" {
			workspaceRoot = ""
		}
		invalidateTopicSessionIndexForPath(info.RecoveryPath)
		a.mu.Lock()
		sinkGeneration := uint64(0)
		sink := (*tabEventSink)(nil)
		if tab != nil && !tab.removed {
			oldKey := sessionRuntimeKey(info.OriginalPath)
			newKey := sessionRuntimeKey(info.RecoveryPath)
			if oldKey != "" && newKey != "" && a.detachedSessions[oldKey] == tab {
				delete(a.detachedSessions, oldKey)
				a.ensureDetachedSessionsLocked()
				a.detachedSessions[newKey] = tab
			}
			tab.SessionPath = canonicalTabSessionPath(info.RecoveryPath)
			// tl1403: the fork moves the controller onto a new session file while
			// the open tab keeps streaming against the old surface identity.
			// Bump the tab-local generation (same semantics as the rotation path
			// in handleTabSessionTransition) so the hydrate identity changes and
			// the "tab:meta" push below carries a strictly-advancing marker the
			// frontend can trust as a deliberate surface move. The sink is
			// rebound off-lock right after this block: wire events stamped with
			// the old generation would be dropped by the frontend generation
			// fence and the moved-to surface would go permanently quiet.
			tab.SessionGeneration++
			sinkGeneration = tab.SessionGeneration
			sink = tab.sink
			if a.tabs[tab.ID] == tab {
				a.saveTabsLocked()
			}
		}
		a.mu.Unlock()
		if sink != nil {
			sink.setSessionGeneration(sinkGeneration)
		}
		info.OnCommit(func() {
			if tab != nil {
				tab.setPinnedFiles(pinnedState.Files)
				// tl1403: the open tab's surface follows the fork. The frontend
				// only re-hydrates onto the recovery path when the authoritative
				// meta reaches it — the "tab:meta" push is that channel (the
				// session:recovered event feeds the version catalog, not the
				// open tab). Runs at commit.publish(), i.e. after the controller
				// has installed the recovery path, so MetaForTab reports the
				// moved path.
				a.emitRuntimeEvent(tabMetaRefreshEventChannel, TabMetaRefreshEvent{TabID: tab.ID, Meta: a.MetaForTab(tab.ID)})
			}
		})
		// The fork continues the same conversation, so its cost history moves
		// with it: re-key the in-memory telemetry from the original session to
		// the recovery path and persist the sidecar right away.
		if tab != nil && !tab.removed {
			origKey := sessionRuntimeKey(info.OriginalPath)
			newKey := sessionRuntimeKey(info.RecoveryPath)
			carried := false
			if newKey != "" {
				tab.telemMu.Lock()
				if tab.telemetrySessionKey == origKey || tab.telemetrySessionKey == "" {
					tab.telemetrySessionKey = newKey
					carried = true
				}
				tab.telemMu.Unlock()
			}
			if carried {
				_ = saveTelemetry(info.RecoveryPath+".telemetry.json", tab.telemetrySnapshot())
			}
		}
		a.emitSessionRecoveredAndRefresh(sessionDirectoryForPath(info.RecoveryPath), sessionRecoveryEvent{
			ConversationID:    meta.TopicID,
			ActiveVersionID:   agent.BranchID(info.RecoveryPath),
			RecoveryVersionID: agent.BranchID(info.RecoveryPath),
			OriginalPath:      info.OriginalPath,
			RecoveryPath:      info.RecoveryPath,
			Scope:             scope,
			WorkspaceRoot:     workspaceRoot,
			TopicID:           meta.TopicID,
			TopicTitle:        meta.TopicTitle,
			RecoveryReason:    meta.RecoveryReason,
			RecoveryDigest:    meta.RecoveryDigest,
			RecoveryParentID:  string(meta.ParentID),
			Existing:          info.Existing,
			BaseRevision:      info.BaseRevision,
			DiskRevision:      info.DiskRevision,
			CanContinue:       true,
			RequiresChoice:    false,
		})
		a.invalidatePromptHistoryCache()
		return nil
	}
}
