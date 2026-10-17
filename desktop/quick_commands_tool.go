package main

import (
	"reasonix/internal/config"
	"reasonix/internal/tool/builtin"
)

// Agent-facing quick-command management (task 960): adapt the desktop's config
// channel to the builtin.QuickCommandsStore seam so the agent lists / edits
// the composer's snippets through the exact same validation chain the settings
// UI uses — App.SetQuickCommands (applyConfigOnly → Config.SetQuickCommands).
// Nothing here re-implements validation; the config layer stays the single
// source of truth for entry count, title length, and text size.
//
// quickCommandsChangedEvent is emitted after every successful agent-side write
// with the fresh entry list as payload, so the frontend composer mirror can
// refresh without waiting for a settings reload (UI 即时可见).
const quickCommandsChangedEvent = "app:quick-commands-changed"

// newQuickCommandsStoreAdapter is called once during App startup, next to the
// heartbeat-manager wiring.
func newQuickCommandsStoreAdapter(a *App) builtin.QuickCommandsStore {
	return quickCommandsStoreAdapter{app: a}
}

type quickCommandsStoreAdapter struct {
	app *App
}

func (s quickCommandsStoreAdapter) List() ([]config.QuickCommandEntry, error) {
	cfg, _, err := s.app.loadDesktopUserConfigForView()
	if err != nil {
		return nil, err
	}
	// DesktopQuickCommands returns a copy; nil when unset.
	if entries := cfg.DesktopQuickCommands(); entries != nil {
		return entries, nil
	}
	return nil, nil
}

func (s quickCommandsStoreAdapter) Replace(entries []config.QuickCommandEntry) error {
	// Same function the settings UI calls: lock → load → Config.SetQuickCommands
	// validation → save. A validation failure writes nothing.
	if err := s.app.SetQuickCommands(entries); err != nil {
		return err
	}
	// Push the persisted list so the composer mirror updates immediately.
	if fresh, err := s.List(); err == nil {
		runtimeEventsEmitFallback(s.app.ctx, quickCommandsChangedEvent, fresh)
	}
	return nil
}
