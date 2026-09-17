package main

import (
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// sessionStorageBootMode returns the conversation-store mode this process
// started with. It is captured on first use — the first settings read after
// startup — and never changes afterwards, so the settings view can tell a
// configured mode apart from the mode the running process actually uses.
func (a *App) sessionStorageBootMode(current string) string {
	if a == nil {
		return current
	}
	a.sessionStorageModeMu.Lock()
	defer a.sessionStorageModeMu.Unlock()
	if !a.sessionStorageBootSet {
		a.sessionStorageBootValue = current
		a.sessionStorageBootSet = true
	}
	return a.sessionStorageBootValue
}

// setSessionStorageMode validates and persists a mode change, then records it in
// the audit log. It returns the mode that was configured before the call and the
// normalized mode now stored. Validation refuses an upgrade that would skip the
// dual-write stages (task 155: every session must be mirrored at least once
// before the read side trusts v4).
func (a *App) setSessionStorageMode(mode string) (string, string, error) {
	normalized, ok := config.NormalizeSessionStorageMode(mode)
	if !ok {
		return "", "", fmt.Errorf("session storage: %q is not a mode (use %s)",
			mode, strings.Join(config.SessionStorageModes, ", "))
	}
	unlock := config.LockUserConfigEdits()
	defer unlock()
	cfg, path, err := a.loadDesktopUserConfigForEdit()
	if err != nil {
		return "", "", err
	}
	previous := config.SessionStorageMode(cfg)
	if err := config.ValidateSessionStorageTransition(previous, normalized, config.SessionStorageHistoryModes()); err != nil {
		return previous, normalized, err
	}
	if err := cfg.SetSessionStorage(normalized); err != nil {
		return previous, normalized, err
	}
	if err := cfg.SaveTo(path); err != nil {
		return previous, normalized, err
	}
	if previous == normalized {
		return previous, normalized, nil
	}
	// Audit every switch (time/from/to/restart) together with the pre-switch
	// inventory of both stores, so a rollback can be checked against what the
	// switch started from. Telemetry never fails the settings write.
	snapshot := config.SessionStorageSnapshot()
	if err := config.AppendSessionStorageChange(config.SessionStorageChange{
		From:    previous,
		To:      normalized,
		Source:  "settings-ui",
		Restart: config.SessionStorageNeedsRestart(previous, normalized),
		V3Count: snapshot.V3Count,
		V3Bytes: snapshot.V3Bytes,
		V4Count: snapshot.V4Count,
		V4Bytes: snapshot.V4Bytes,
	}); err != nil {
		slog.Warn("desktop: session storage audit write failed", "err", err)
	}
	return previous, normalized, nil
}

// applySessionStorageReadSide points every live controller's v4 bridge at the
// configured read side. Only the read dimension can move under a running
// process: modes 2 and 3 write the same pair and differ in which copy history
// prefers, and each read consults the flag.
func (a *App) applySessionStorageReadSide(mode string) {
	if a == nil {
		return
	}
	readsV4 := config.SessionV4ReadsEnabledForMode(mode)
	a.mu.Lock()
	tabs := make([]*WorkspaceTab, 0, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.tabs {
		tabs = append(tabs, tab)
	}
	for _, tab := range a.detachedSessions {
		tabs = append(tabs, tab)
	}
	a.mu.Unlock()
	for _, tab := range tabs {
		if tab == nil {
			continue
		}
		ctrl, ok := a.controllerForTab(tab).(*control.Controller)
		if !ok || ctrl == nil {
			continue
		}
		if bridge := ctrl.SessionV4(); bridge != nil {
			bridge.SetReadsV4(readsV4)
		}
	}
}
