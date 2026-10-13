package main

import (
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/store"
)

// kickHistoryIndexRebuild single-flight schedules a background display-index
// rebuild for a live session whose on-disk index did not validate. It never
// blocks the request path.
func (a *App) kickHistoryIndexRebuild(sessionPath string) {
	if strings.TrimSpace(sessionPath) == "" {
		return
	}
	a.historySliceMu.Lock()
	if a.historyIndexRebuilds == nil {
		a.historyIndexRebuilds = map[string]chan struct{}{}
	}
	if _, ok := a.historyIndexRebuilds[sessionPath]; ok {
		a.historySliceMu.Unlock()
		return
	}
	done := make(chan struct{})
	a.historyIndexRebuilds[sessionPath] = done
	a.historySliceMu.Unlock()
	a.goSafe("historyIndexRebuild", func() {
		defer func() {
			a.historySliceMu.Lock()
			close(done)
			delete(a.historyIndexRebuilds, sessionPath)
			a.historySliceMu.Unlock()
		}()
		a.rebuildHistoryIndexForLiveSession(sessionPath)
	})
}

// kickHistoryReadModelRepair single-flights the stronger cold-session repair:
// replay the authoritative event log under the save lock, atomically refresh
// the JSONL random-read model, then publish matching offsets. The cold request
// already returned from its in-memory recovery source before this work starts.
func (a *App) kickHistoryReadModelRepair(sessionPath string) {
	if strings.TrimSpace(sessionPath) == "" {
		return
	}
	key := "read-model:" + agent.CanonicalSessionPath(sessionPath)
	a.historySliceMu.Lock()
	if a.historyIndexRebuilds == nil {
		a.historyIndexRebuilds = map[string]chan struct{}{}
	}
	if _, ok := a.historyIndexRebuilds[key]; ok {
		a.historySliceMu.Unlock()
		return
	}
	done := make(chan struct{})
	a.historyIndexRebuilds[key] = done
	a.historySliceMu.Unlock()
	a.goSafe("historyReadModelRepair", func() {
		defer func() {
			a.historySliceMu.Lock()
			close(done)
			delete(a.historyIndexRebuilds, key)
			a.historySliceMu.Unlock()
		}()
		if err := agent.RepairSessionDisplayReadModel(sessionPath); err != nil {
			slog.Debug("desktop: history read-model repair failed", "path", sessionPath, "err", err)
		}
	})
}

// kickColdHistorySelfHeal single-flights the 任务 836 tail-window follow-up.
// The cold first paint returned from the trailing window before any full pass
// ran, so the two original self-heal moves — the streaming index scan when the
// transcript is the authoritative model, and the stronger read-model repair
// (event-log replay + anchor refresh + index publication) when the anchor is
// behind the ledger — now happen here instead of on the cold-start critical
// path. The scan digest is compared against the ledger identity to pick the
// branch, exactly as the synchronous scan path did.
func (a *App) kickColdHistorySelfHeal(sessionPath string, identity agent.PersistedState) {
	if strings.TrimSpace(sessionPath) == "" {
		return
	}
	key := "cold-heal:" + agent.CanonicalSessionPath(sessionPath)
	a.historySliceMu.Lock()
	if a.historyIndexRebuilds == nil {
		a.historyIndexRebuilds = map[string]chan struct{}{}
	}
	if _, ok := a.historyIndexRebuilds[key]; ok {
		a.historySliceMu.Unlock()
		return
	}
	done := make(chan struct{})
	a.historyIndexRebuilds[key] = done
	a.historySliceMu.Unlock()
	a.goSafe("coldHistorySelfHeal", func() {
		defer func() {
			a.historySliceMu.Lock()
			close(done)
			delete(a.historyIndexRebuilds, key)
			a.historySliceMu.Unlock()
		}()
		scanned, scanErr := agent.ScanSessionDisplayIndex(sessionPath)
		if scanErr == nil && scanned != nil && scanned.ContentDigest == identity.DigestHex {
			scanned.Revision = identity.Revision
			scanned.RevisionKnown = identity.RevisionKnown
			if writeErr := agent.WriteSessionDisplayIndex(store.SessionDisplayIndex(sessionPath), scanned); writeErr != nil {
				slog.Debug("desktop: cold history index republish failed", "path", sessionPath, "err", writeErr)
			}
			return
		}
		if scanErr != nil {
			slog.Debug("desktop: cold history index scan failed, falling back to read-model repair", "path", sessionPath, "err", scanErr)
		}
		if err := agent.RepairSessionDisplayReadModel(sessionPath); err != nil {
			slog.Debug("desktop: cold history self-heal repair failed", "path", sessionPath, "err", err)
		}
	})
}

// rebuildHistoryIndexForLiveSession republishes the display index for a live
// session, but only when the in-memory log is exactly the persisted
// transcript — an append-only tail means the next save will publish a
// covering index anyway, and a scanned .jsonl anchor cannot describe the
// event-log tail.
func (a *App) rebuildHistoryIndexForLiveSession(sessionPath string) {
	a.mu.RLock()
	ctrls := make([]control.SessionAPI, 0, len(a.tabs))
	for _, tab := range a.tabs {
		if tab != nil && tab.Ctrl != nil {
			ctrls = append(ctrls, tab.Ctrl)
		}
	}
	a.mu.RUnlock()
	var ctrl control.SessionAPI
	for _, c := range ctrls {
		if c.SessionPath() == sessionPath {
			ctrl = c
			break
		}
	}
	wc, ok := ctrl.(historyWindowController)
	if !ok {
		return
	}
	ps, ok := wc.SessionPersistedState()
	if !ok || !ps.UnchangedSincePersisted {
		return
	}
	if err := agent.RepairSessionDisplayReadModel(sessionPath); err != nil {
		slog.Debug("desktop: live history read-model rebuild failed", "path", sessionPath, "err", err)
	}
}
