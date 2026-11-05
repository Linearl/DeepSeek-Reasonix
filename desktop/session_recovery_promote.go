package main

import (
	"log/slog"
	"strings"

	"reasonix/internal/control"
)

// tl1403: handleTabSessionPromoted is the promotion-side mirror of
// handleTabSessionRecovered. The fork moved the tab onto the recovery copy
// (lease, SessionPath, write authority, generation, frontend identity); the
// deferred promotion then moved the controller back onto the main file once
// the main file stayed quiet (tl1352). Without this handler the display face
// stays on the copy while every save lands on the main file — the
// 监测面/写入面 split behind the watchdog early-stop loop (fork→promote→fork,
// desktop.log 2026-11-05 13:55/13:59/14:10).
//
// The controller callback fires on the promotion's save-cycle goroutine; the
// lease handoff below takes the same locks as the fork-time handoff, which is
// proven safe from the same context (handleTabSessionRecovered).
func (a *App) handleTabSessionPromoted(tab *WorkspaceTab) func(control.SessionPromotionInfo) error {
	return func(info control.SessionPromotionInfo) error {
		mainPath := strings.TrimSpace(info.MainPath)
		copyPath := strings.TrimSpace(info.CopyPath)
		if mainPath == "" || copyPath == "" || tab == nil || tab.removed {
			return nil
		}
		a.mu.RLock()
		current := a.tabs[tab.ID]
		ctrlPath := ""
		if tab.Ctrl != nil {
			ctrlPath = strings.TrimSpace(tab.Ctrl.SessionPath())
		}
		a.mu.RUnlock()
		if current != tab {
			return nil
		}
		if ctrlPath != "" && sessionRuntimeKey(ctrlPath) != sessionRuntimeKey(mainPath) {
			// The controller no longer lives on the promoted main path (another
			// conflict forked again before this notification ran). Keep the tab
			// where the writer actually is; the next fork/promotion round
			// notifies again.
			slog.Info("desktop: recovery promotion notification skipped; controller moved on",
				"tab", tab.ID, "main", mainPath, "ctrl", ctrlPath)
			return nil
		}
		if tab.SessionPath != "" && sessionRuntimeKey(tab.SessionPath) == sessionRuntimeKey(mainPath) {
			return nil // already following the main path; nothing to move
		}
		// Lease/write-authority handoff to the main path first — same order as
		// the fork handler, so the controller never saves with an authority that
		// does not cover its live path (the bind acquires the main lease and
		// re-issues the write generation from it).
		if err := a.handoffTabRecoveryLease(tab, mainPath); err != nil {
			// Fail-open for the surface move: the lease error is already logged
			// and surfaced as a recovery-failed event by the handoff itself.
			// Keeping the tab on the copy would leave the write-face split in
			// place, but blocking the notification entirely would hide the
			// promotion from the user forever.
			slog.Warn("desktop: recovery promotion lease handoff failed; moving the surface anyway",
				"tab", tab.ID, "main", mainPath, "err", err)
		}
		invalidateTopicSessionIndexForPath(mainPath)
		a.mu.Lock()
		generation := uint64(0)
		sink := (*tabEventSink)(nil)
		if tab != nil && !tab.removed && a.tabs[tab.ID] == tab {
			oldKey := sessionRuntimeKey(copyPath)
			newKey := sessionRuntimeKey(mainPath)
			if oldKey != "" && newKey != "" && a.detachedSessions[oldKey] == tab {
				delete(a.detachedSessions, oldKey)
				a.ensureDetachedSessionsLocked()
				a.detachedSessions[newKey] = tab
			}
			tab.SessionPath = canonicalTabSessionPath(mainPath)
			// tl1403: same surface-move marker as the fork handler — the
			// hydrate identity changes and the pushed meta carries a strictly
			// advancing generation the frontend can trust. The sink rebind keeps
			// wire events inside the frontend's generation fence.
			tab.SessionGeneration++
			generation = tab.SessionGeneration
			sink = tab.sink
			a.saveTabsLocked()
		}
		a.mu.Unlock()
		if sink != nil {
			sink.setSessionGeneration(generation)
		}
		a.emitRuntimeEvent(tabMetaRefreshEventChannel, TabMetaRefreshEvent{TabID: tab.ID, Meta: a.MetaForTab(tab.ID)})
		a.emitProjectTreeChangedForSessionDirs(sessionDirectoryForPath(mainPath))
		a.invalidatePromptHistoryCache()
		slog.Info("desktop: tab surface followed the recovery promotion back to the main session",
			"tab", tab.ID, "main", mainPath, "copy", copyPath)
		return nil
	}
}
