package main

import (
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
)

func (a *App) recordModelSwitchTiming(tabID string, timing *modelSwitchTiming, started time.Time, result *error) {
	timing.Total = time.Since(started)
	if *result != nil {
		timing.Outcome = "failed"
	} else {
		timing.Outcome = "ok"
	}
	slog.Debug(
		"desktop: model switch timing",
		"tab", tabID,
		"outcome", timing.Outcome,
		"total_ms", timing.Total.Milliseconds(),
		"lock_wait_ms", timing.LockWait.Milliseconds(),
		"prepare_ms", timing.Prepare.Milliseconds(),
		"config_ms", timing.Config.Milliseconds(),
		"snapshot_ms", timing.Snapshot.Milliseconds(),
		"build_ms", timing.Build.Milliseconds(),
		"lease_resume_ms", timing.LeaseAndResume.Milliseconds(),
		"swap_persist_ms", timing.SwapAndPersist.Milliseconds(),
	)
	if a.modelSwitchTimingHook != nil {
		a.modelSwitchTimingHook(*timing)
	}
}

func (a *App) ModelsForTab(tabID string) []ModelInfo {
	if cur, ok := a.remoteTabCurrentModel(tabID); ok {
		if !a.remoteTabLocalProxy(tabID) {
			if infos, err := a.remoteServeModelsForTab(tabID, cur); err == nil {
				return infos
			}
			return []ModelInfo{}
		}
		return a.remoteProxyModelCatalog(cur)
	}
	a.mu.RLock()
	curModel := ""
	workspaceRoot := ""
	var ctrl control.SessionAPI
	if tab := a.tabByIDLocked(tabID); tab != nil {
		curModel = tab.model
		workspaceRoot = tab.WorkspaceRoot
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	return a.desktopModelCatalog(curModel, workspaceRoot, ctrl)
}

func (a *App) remoteProxyModelCatalog(curModel string) []ModelInfo {
	cfg, err := config.Load()
	if err != nil {
		return []ModelInfo{}
	}
	canonical := curModel
	if current, ok := cfg.ResolveModel(curModel); ok {
		canonical = current.Name + "/" + current.Model
	}
	out := []ModelInfo{}
	for i := range cfg.Providers {
		entry := &cfg.Providers[i]
		if !pickerProviderVisible(cfg, entry) {
			continue
		}
		for _, model := range entry.ChatModelList() {
			ref := entry.Name + "/" + model
			out = append(out, configuredModelInfo(cfg, entry.Name, model, ref == canonical))
		}
	}
	return out
}

// pickerProviderVisible reports whether a configured connection may appear in
// the model picker. Three states, three different rules (tl1654 round 3):
//
//   - 已删 (the config has no section for it): the connection never reaches
//     this loop. The loader no longer materializes a provider the user deleted
//     — a stale provider_access entry or a historical model ref used to bring
//     it back, which is how the deleted "MiMo API" group kept reappearing in
//     the picker.
//   - hidden (the section is there, hidden = true): the user parked the
//     connection on purpose. Its section stays and its refs keep resolving;
//     only the picker entry is dropped (task 279).
//   - a live section: visible when the access list grants it and its
//     credential is present.
func pickerProviderVisible(cfg *config.Config, p *config.ProviderEntry) bool {
	if cfg == nil || p == nil {
		return false
	}
	if !modelProviderAccessAllowed(cfg.Desktop.ProviderAccess, p.Name) {
		return false
	}
	if !p.Configured() {
		return false
	}
	if p.Hidden {
		return false
	}
	return true
}

func (a *App) desktopModelCatalog(curModel, workspaceRoot string, ctrl control.SessionAPI) []ModelInfo {
	// A cold extension catalog fetch can block on a sidecar RPC, so read it
	// before loading config and without holding App.mu.
	var extensionCatalog []provider.Descriptor
	if ctrl != nil {
		extensionCatalog = ctrl.ProviderCatalog()
	}
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return []ModelInfo{}
	}
	if entry, ok := cfg.ResolveModel(curModel); ok {
		curModel = entry.Name + "/" + entry.Model
	}
	out := []ModelInfo{}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if !pickerProviderVisible(cfg, p) {
			continue
		}
		for _, m := range p.ChatModelList() {
			ref := p.Name + "/" + m
			out = append(out, configuredModelInfo(cfg, p.Name, m, ref == curModel))
		}
	}
	return mergeExtensionModelInfos(out, extensionCatalog, curModel)
}

// configuredModelInfo projects the same per-model overrides and capability
// resolver used by settings, without guessing capabilities for unknown models.
func configuredModelInfo(cfg *config.Config, name, model string, current bool) ModelInfo {
	info := ModelInfo{Ref: name + "/" + model, Provider: name, Model: model, Current: current}
	// tl1547 (A1): carry the connection's display label so every surface that
	// holds only a ref (session-header trigger, sidebar hover) can show the
	// same name the settings panel does — display only; Provider stays the
	// stable routing identity.
	if p, ok := cfg.Provider(name); ok && p != nil {
		info.ProviderLabel = strings.TrimSpace(p.DisplayName)
	}
	if entry, ok := cfg.ResolveModel(info.Ref); ok {
		// tl1594: entry.DisplayName is the CONNECTION's display label (settings
		// displayName — it rides the ResolveModel copy of the provider entry).
		// ModelInfo.DisplayName is the per-model display tier (frontend
		// modelRefLabel.ts contract); stamping the connection label here made
		// every model under a renamed custom endpoint read as the endpoint name
		// in the switcher entries. The connection label lives on
		// info.ProviderLabel above; entries fall back to the bare model id.
		info.ContextWindow = entry.ContextWindow
		capability := config.NewModelCapabilityResolver().Resolve(entry)
		info.Vision = string(capability.State) == "supported"
	}
	return info
}
