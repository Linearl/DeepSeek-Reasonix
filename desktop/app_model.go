package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/extension/providerext"
	"reasonix/internal/plugin"
	"reasonix/internal/provider"
)

type ModelInfo struct {
	Ref           string `json:"ref"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Current       bool   `json:"current"`
	ContextWindow int    `json:"contextWindow,omitempty"`
	Vision        bool   `json:"vision,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
}

type EffortInfo struct {
	Options   []provider.ReasoningOption `json:"options,omitempty"`
	Supported bool                       `json:"supported"`
	Current   string                     `json:"current"`
	Default   string                     `json:"default"`
	Levels    []string                   `json:"levels"`
	// AliasFold carries the MiMo identity mark (task effortfix2): display
	// surfaces fold the four honest tiers only for this family — never by
	// sniffing the vocabulary's shape.
	AliasFold bool `json:"aliasFold,omitempty"`
}

// Models flattens the configured providers into their (provider, model) pairs —
// the switcher's options — marking the active one. A vendor with a `models` list
// yields one entry per model, all sharing the same endpoint/key. Unconfigured providers are skipped. Result is non-nil: the frontend reads .length, so a nil slice (JSON null) would crash the switcher on an empty list.
func (a *App) Models() []ModelInfo {
	return a.ModelsForTab("")
}

// mergeExtensionModelInfos adds namespaced plugin models from the controller's
// merged provider catalog. Base descriptors are already represented by out;
// plugin refs need no provider-access gate because enabling the package grants access. A nil catalog leaves the config-backed list untouched.
func mergeExtensionModelInfos(out []ModelInfo, catalog []provider.Descriptor, curModel string) []ModelInfo {
	if len(catalog) == 0 {
		return out
	}
	seen := make(map[string]bool, len(out)+len(catalog))
	for _, info := range out {
		seen[info.Ref] = true
	}
	for _, d := range catalog {
		ref := strings.TrimSpace(d.Ref)
		owner := providerext.PluginRefOwner(ref)
		if ref == "" || owner == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		providerName := "plugin/" + owner
		model := strings.TrimPrefix(ref, providerName+"/")
		out = append(out, ModelInfo{Ref: ref, Provider: providerName, Model: model, Current: ref == curModel})
	}
	return out
}

// extensionModelDescriptor finds a plugin-namespaced ref in a controller's
// merged catalog: an exact match, or the prefix form where ref names the
// provider and the descriptor adds the model segment. Non-plugin refs never match — they belong to the config catalog.
func extensionModelDescriptor(catalog []provider.Descriptor, ref string) (provider.Descriptor, bool) {
	ref = strings.TrimSpace(ref)
	if providerext.PluginRefOwner(ref) == "" {
		return provider.Descriptor{}, false
	}
	for _, d := range catalog {
		if d.Ref == ref || strings.HasPrefix(d.Ref, ref+"/") {
			return d, true
		}
	}
	return provider.Descriptor{}, false
}

func modelProviderAccessAllowed(access []string, name string) bool {
	if access == nil {
		return true
	}
	name = strings.TrimSpace(name)
	for _, candidate := range access {
		if strings.TrimSpace(candidate) == name {
			return true
		}
	}
	return false
}

// providerCatalogForTab returns the tab controller's merged provider catalog
// (extension sidecar providers over the config base), or nil when the tab has
// no live controller or no sidecar declared providers.
func (a *App) providerCatalogForTab(tab *WorkspaceTab) []provider.Descriptor {
	if tab == nil {
		return nil
	}
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		return ctrl.ProviderCatalog()
	}
	return nil
}

// SetModel switches the active model and carries the current conversation into the
// new model's session, so the chat continues seamlessly and subsequent turns use
// the new model. No-op if name is already active or the controller is down.
func (a *App) SetModel(name string) error {
	return a.SetModelForTab("", name)
}

// persistTabModelIfCurrent repairs stale model metadata without letting an
// older default overwrite a newer explicit model switch. Model switches use
// the same runtimeRebuildMu, so whichever operation acquires it last owns the persisted provider identity.
func (a *App) persistTabModelIfCurrent(tab *WorkspaceTab, model string) error {
	model = strings.TrimSpace(model)
	if tab == nil || model == "" {
		return nil
	}
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()

	a.mu.RLock()
	if tab.removed || a.tabs[tab.ID] != tab {
		a.mu.RUnlock()
		return fmt.Errorf("tab %q changed while persisting model; retry", tab.ID)
	}
	if tab.Ctrl == nil || strings.TrimSpace(tab.model) != model {
		a.mu.RUnlock()
		return nil
	}
	a.mu.RUnlock()

	path := a.currentSessionPathFor(tab)
	if path == "" {
		return nil
	}
	if err := agent.SetBranchModelPreserveUpdated(path, model); err != nil {
		return fmt.Errorf("persist selected model: %w", err)
	}
	return nil
}

type modelSwitchTiming struct {
	Total          time.Duration
	LockWait       time.Duration
	Prepare        time.Duration
	Config         time.Duration
	Snapshot       time.Duration
	Build          time.Duration
	LeaseAndResume time.Duration
	SwapAndPersist time.Duration
	Outcome        string
}

// resolveTabModelRef validates a user-facing model name against the tab's
// config and returns the canonical "provider/model" ref plus the resolved
// entry, mirroring plugin-namespaced fallback and provider-access gating. Shared by the model-switch fast path and the build+swap path (task 148) so the two cannot drift on what counts as a switchable model. For plugin-namespaced refs the descriptor ref comes back as the canonical ref and pluginRef is true (the entry stays nil — extension sidecars own it).
func (a *App) resolveTabModelRef(tab *WorkspaceTab, workspaceRoot, name string) (string, *config.ProviderEntry, bool, error) {
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return "", nil, false, err
	}
	entry, ok := cfg.ResolveModel(name)
	pluginRef := false
	if !ok {
		// Plugin-namespaced refs belong to extension sidecars: validate them
		// against the tab controller's merged catalog instead of the config.
		if d, found := extensionModelDescriptor(a.providerCatalogForTab(tab), name); found {
			pluginRef = true
			ok = true
			name = d.Ref
		}
	}
	if !ok {
		return "", nil, false, fmt.Errorf("unknown model %q", name)
	}
	if !pluginRef {
		if !modelProviderAccessAllowed(cfg.Desktop.ProviderAccess, entry.Name) {
			return "", nil, false, fmt.Errorf("model %q is not available because provider %q is not added", name, entry.Name)
		}
		name = entry.Name + "/" + entry.Model
	}
	return name, entry, pluginRef, nil
}

// modelSwitchPersonaBoundary reports whether the switch crosses the official
// DeepSeek-V4-Pro persona boundary (task 602). The persona is prepended to the
// cache-stable system prompt at boot (config.ApplyOfficialDeepSeekV4ProPersona) and ReasoningLanguageForEntry pins its auto reasoning language — neither has an override seam, so crossing the boundary keeps the build+swap path, which re-derives the prompt exactly as boot does. Plugin-namespaced targets (nil entry) never apply the persona.
func modelSwitchPersonaBoundary(workspaceRoot, currentRef string, target *config.ProviderEntry) bool {
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return false // unreadable config declines the gate; resolution fails later either way
	}
	current := false
	if ref := strings.TrimSpace(currentRef); ref != "" {
		if e, ok := cfg.ResolveModel(ref); ok {
			current = config.AppliesOfficialDeepSeekV4ProPersona(e)
		}
	}
	targetApplies := target != nil && config.AppliesOfficialDeepSeekV4ProPersona(target)
	return current != targetApplies
}

// modelSwitchIdentity carries what the fast path rebinding writes into the
// controller and the tab: SetModelIdentity fields plus the tab label.
type modelSwitchIdentity struct {
	label      string
	balanceURL string
	balanceKey string
	imageInput *bool
}

// modelSwitchExtras derives the entry-scoped payload the hot switch needs
// (task 602): the agent override scalars plus the controller identity, exactly
// the surfaces a rebuild re-derives from the new entry (boot folds the same pricing/window/high-speed inputs into the executor and the controller).
func modelSwitchExtras(workspaceRoot, ref string, entry *config.ProviderEntry) (agent.ModelOverrideExtras, modelSwitchIdentity, error) {
	if entry == nil {
		// Plugin-namespaced ref: the sidecar owns the entry; the agent-side
		// resolver seam constructs the provider, zero extras keep the
		// construction scalars.
		return agent.ModelOverrideExtras{}, modelSwitchIdentity{}, nil
	}
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return agent.ModelOverrideExtras{}, modelSwitchIdentity{}, err
	}
	extras := agent.ModelOverrideExtras{
		Pricing:         entry.Price,
		ContextWindow:   entry.ContextWindow,
		MaxOutputTokens: entry.MaxOutputTokens,
		HighSpeedModels: boot.HighSpeedModelsFor(cfg, entry.HighSpeedModels),
	}
	// Label mirrors boot's derivation (entry.Model, plus the planner suffix
	// when a planner model is configured). The frozen image gate mirrors boot's
	// capability resolution; a catalog entry without modality data stays conservative exactly as a rebuild would.
	label := entry.Model
	if planner := strings.TrimSpace(cfg.Agent.PlannerModel); planner != "" {
		if pe, ok := cfg.ResolveModel(planner); ok {
			label = entry.Model + " + planner " + pe.Model
		}
	}
	imageEnabled := config.NewModelCapabilityResolver().Resolve(entry).State == config.CapabilitySupported
	identity := modelSwitchIdentity{
		label:      label,
		balanceURL: entry.BalanceURL,
		balanceKey: entry.APIKey(),
		imageInput: &imageEnabled,
	}
	return extras, identity, nil
}

func (a *App) SetModelForTab(tabID, name string) (retErr error) {
	if name == "" {
		return nil
	}
	if a.isRemoteTab(tabID) {
		return a.SetRemoteTabModel(tabID, name)
	}
	if a.ctx == nil {
		return nil
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil
	}
	pendingSequence := a.deferredRebuildSequence(tab.ID)
	a.mu.RLock()
	currentModel := tab.model
	a.mu.RUnlock()
	if name == currentModel {
		return nil
	}
	timing := modelSwitchTiming{}
	totalStarted := time.Now()
	defer a.recordModelSwitchTiming(tab.ID, &timing, totalStarted, &retErr)
	// Same build+swap shape as rebuildSetting: hold the same lock so a settings
	// rebuild and a model switch cannot interleave on one tab.
	stageStarted := time.Now()
	a.runtimeRebuildMu.Lock()
	timing.LockWait = time.Since(stageStarted)
	defer a.runtimeRebuildMu.Unlock()
	stageStarted = time.Now()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	switchSnap := a.tabRuntimeSnapshot(tab)
	switchRef, switchEntry, _, err := a.resolveTabModelRef(tab, switchSnap.workspaceRoot, name)
	if err != nil {
		return err
	}
	timing.Config = time.Since(stageStarted)
	if handled, err := a.tryModelSessionOverride(tab, tabID, switchRef, switchSnap, switchEntry); err != nil {
		return err
	} else if handled {
		return nil
	}
	prevPath := a.sessionPathForSettingsRebuild(tab)
	if a.controllerForTab(tab) == nil && prevPath != "" {
		a.attachExistingSessionRuntime(tab, prevPath, a.ctx)
	}
	if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "model"); err != nil {
		return err
	}
	if err := a.ensureTabControllerWorkspace(tab); err != nil {
		return err
	}
	prevPath = a.sessionPathForSettingsRebuild(tab)
	if a.controllerForTab(tab) == nil && prevPath != "" && a.attachExistingSessionRuntime(tab, prevPath, a.ctx) {
		prevPath = a.reconciledSessionPathForTab(tab)
		if prevPath == "" {
			prevPath = a.currentSessionPathFor(tab)
		}
		if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "model"); err != nil {
			return err
		}
	}
	timing.Prepare = time.Since(stageStarted)
	// Snapshot the tab profile under a.mu: SetModeForTab/SetGoalForTab and the
	// event sink write these fields under the lock while this rebuild runs
	// off-lock.
	stageStarted = time.Now()
	snap := a.tabRuntimeSnapshot(tab)
	runtime := snap.normalizedRuntime()
	// Same resolution as the fast path above (task 148): one helper so the
	// build+swap fallback cannot drift from what the fast path accepts.
	canonicalRef, entry, pluginRef, err := a.resolveTabModelRef(tab, snap.workspaceRoot, name)
	if err != nil {
		return err
	}
	name = canonicalRef
	effortOverride := cloneStringPtr(snap.effort)
	if effortOverride != nil && !pluginRef {
		normalized, err := config.NormalizeEffort(entry, config.EffortDisplay(&config.ProviderEntry{Effort: *effortOverride}))
		if err != nil {
			effortOverride = nil
		} else {
			effortOverride = &normalized
		}
	}
	timing.Config = time.Since(stageStarted)

	stageStarted = time.Now()
	var carried []provider.Message
	oldCtrl := a.controllerForTab(tab)
	if oldCtrl != nil {
		if prevPath == "" {
			prevPath = oldCtrl.SessionPath()
		}
		if err := a.ensureTabSessionLeaseForRebuild(tab, prevPath, "model"); err != nil {
			return err
		}
		if err := a.snapshotTabForAction(tab, "changing model"); err != nil {
			return err
		}
		prevPath = sessionPathAfterSnapshot(oldCtrl, prevPath)
		carried = oldCtrl.History()
	}
	timing.Snapshot = time.Since(stageStarted)

	// Preserve the shared plugin host across controller rebuilds — the tab
	// stays in the same workspace root, so MCP processes must not be restarted.
	sharedHost := a.lookupSharedHost(snap.sharedHostKey)

	stageStarted = time.Now()
	runtimeBuildStart := time.Now()
	slog.Info("desktop: runtime build begin", "tab", tabID, "trigger", "set-model", "model", name, "autopilot", tab.autopilot) // task 196: name who is building a runtime, so a startup burst can be attributed instead of inferred.
	defer logRuntimeBuildEnd("set-model", runtimeBuildStart, sharedHost != nil)                                                // task 334: begin→end pair (begin → function exit).
	newCtrl, err := a.buildModelSwitchController(tab, tabID, name, snap, effortOverride, runtime, oldCtrl, sharedHost, stageStarted, &timing)
	if err != nil {
		return err
	}
	return a.swapModelController(tab, tabID, name, newCtrl, oldCtrl, prevPath, runtime, effortOverride, pendingSequence, carried, &timing)
}

// Task 148 per-request fast path (mirrors the task-334 effort seam in
// SetEffortForTab): a same-provider target arms a session-scoped model
// override on the running controller, so the next request carries the new model with no runtime rebuild. Task 602 lifts the same-provider restriction: every destination-scoped surface (protocol shaping, wire, pricing, window scalars, controller identity) follows the override, so cross-provider targets take this path too. An active turn is no longer a rejection reason — the in-flight request stays frozen and the override lands from the next request freeze — while the build+swap path below keeps its active-work guard for everything that declines here (no controller, persona-boundary crossings, recovery-forked sessions).
// tryModelSessionOverride performs the override attempt described above and
// reports whether the switch was handled without a rebuild. Callers hold
// runtimeRebuildMu and tab.turnStartMu.
func (a *App) tryModelSessionOverride(tab *WorkspaceTab, tabID, switchRef string, switchSnap tabRuntimeSnapshot, switchEntry *config.ProviderEntry) (bool, error) {
	// Alias-folded same-model short circuit: the raw check above runs before
	// resolution; tab.model holds the canonical ref the tab currently runs.
	a.mu.RLock()
	sameModel := switchRef == tab.model
	a.mu.RUnlock()
	if sameModel {
		slog.Info("desktop: model switch", "tab", tabID, "path", "fast-same-model", "model", switchRef) // task 148: same-model exits before arming anything.
		return true, nil
	}
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		if !modelSwitchPersonaBoundary(switchSnap.workspaceRoot, tab.model, switchEntry) {
			if setter, ok := ctrl.(interface {
				SetSessionModelOverride(string, agent.ModelOverrideExtras) bool
			}); ok {
				extras, identity, extrasErr := modelSwitchExtras(switchSnap.workspaceRoot, switchRef, switchEntry)
				if extrasErr != nil {
					return false, extrasErr
				}
				if setter.SetSessionModelOverride(switchRef, extras) {
					if idSetter, ok := ctrl.(interface {
						SetModelIdentity(ref, label, balanceURL, balanceKey string, imageInput *bool)
					}); ok {
						idSetter.SetModelIdentity(switchRef, identity.label, identity.balanceURL, identity.balanceKey, identity.imageInput)
					}
					a.mu.Lock()
					tab.model = switchRef
					tab.Label = identity.label
					a.saveTabsLocked()
					a.mu.Unlock()
					// Same sidecar rationale as the rebuild path: empty sessions do
					// not autosave a turn, so persisting the provider identity here
					// keeps a later startup on the provider the tab actually runs, and keeps last-click-wins across overlapping switches.
					if path := a.currentSessionPathFor(tab); path != "" {
						if err := agent.SetBranchModelPreserveUpdated(path, switchRef); err != nil {
							return false, fmt.Errorf("persist selected model: %w", err)
						}
					}
					// A model switch changes the pricing context; discard the
					// session-local automatic wallet hint, same as the rebuild path.
					tab.clearRuntimeDisplayCurrency()
					slog.Info("desktop: model switch", "tab", tabID, "path", "fast-per-request", "model", switchRef) // task 148/602: fast path observability (paired against runtime build end path=fallback).
					return true, nil
				}
			}
		} else {
			// Task 602: the official DeepSeek-V4-Pro persona is baked into the
			// session's cache-stable system prompt and has no override seam —
			// crossing its boundary keeps the rebuild path, which re-derives the prompt exactly as boot does.
			slog.Info("desktop: model switch", "tab", tabID, "path", "fallback-persona-boundary", "model", switchRef)
		}
	}
	return false, nil
}

// buildModelSwitchController builds the replacement controller for a model
// switch, preserving the shared plugin host and the tab's private temp dir.
func (a *App) buildModelSwitchController(tab *WorkspaceTab, tabID, name string, snap tabRuntimeSnapshot, effortOverride *string, runtime normalizedTabRuntime, oldCtrl control.SessionAPI, sharedHost *plugin.Host, stageStarted time.Time, timing *modelSwitchTiming) (*control.Controller, error) {
	newCtrl, err := boot.Build(a.bootContext(), boot.Options{
		RestartUpdater:             restartUpdaterAdapter{a},
		AutonomousUpdateController: newAutonomousUpdateController(a),
		WorktreeProjectOpener:      appWorktreeProjectOpener{app: a}, // 任务128：会话自主建项目——注册为项目并开后台 tab
		Model:                      name,
		Autopilot:                  tab.autopilot,
		MaxRuntime:                 tab.autopilotMaxRuntime,
		AutopilotApprovalGrace:     tab.autopilotApprovalGrace,
		AutopilotAskTimeoutEnabled: tab.autopilotAskTimeoutEnabled,
		AutopilotAskWait:           tab.autopilotAskWait,
		AutopilotAskAutoContinue:   tab.autopilotAskAutoContinue,
		RequireKey:                 false,
		StatsSource:                "desktop",
		TaskStore:                  a.taskStore(),
		OnConfigLoadWarnings:       a.configLoadWarningsHandler(),
		Sink:                       snap.sink,
		WorkspaceRoot:              snap.workspaceRoot,
		SessionDir:                 sessionDirForSnapshot(snap),
		EffortOverride:             cloneStringPtr(effortOverride),
		SharedHost:                 sharedHost,
		MCPHostProfile:             plugin.HostProfileDesktopApps,
		CleanupPendingReconciler:   reconcileDesktopCleanupPending,
		SubagentParentLive:         a.subagentParentProbeForBuild(tab),
		SessionRecoveryMeta:        a.tabSessionRecoveryMeta(tab),
		PinnedContextLoader:        pinnedContextLoader(snap.workspaceRoot),
		OnSessionRecovered:         a.handleTabSessionRecovered(tab),
		OnSessionTransition:        a.handleTabSessionTransition(tab),
		BeforeInboxDispatch:        a.beforeInboxDispatch,
		OnInboxDispatchExhausted:   a.inboxDispatchExhausted,
		OnSessionTitleChanged:      a.onSessionTitleChanged,
		OnCreateCollabSession:      a.createCollabSession,
		OnSessionStatus:            a.collabSessionStatus,
		OnSessionWorkDetail:        a.collabSessionWorkDetail,
		OnSessionInfo:              a.collabSessionInfo,
		OnSessionGroup:             a.collabSessionGroup,
		OnSessionGroupMatch:        a.collabSessionGroupMatch,
		OnSessionVersions:          a.collabSessionVersions,
		OnAdoptSessionVersion:      a.collabAdoptSessionVersion,
		OnSessionStop:              a.collabSessionStop,
		OnSessionSetModel:          a.collabSessionSetModel,
		OnSessionTurnStatus:        a.collabSessionTurnStatus,
		OnCascadeDelegate:          cascadeDelegateFor,
		OnFallbackSwitch:           a.fallbackSwitch,
		OnDeleteSession:            a.deleteCollabSession,
		OnRenameSession:            a.renameCollabSession,
		OnMoveTopicToGroup:         a.moveCollabTopicToGroup,
		// Keep the private temporary directory across model switches (#7575).
		SessionTemp: sessionTempFromController(oldCtrl),
	})
	if err != nil {
		return nil, err
	}
	timing.Build = time.Since(stageStarted)
	a.bindControllerDisplayRecorder(newCtrl)
	configureControllerRuntime(newCtrl, oldCtrl, runtime)
	return newCtrl, nil
}

// swapModelController authorizes and installs the model-switch controller and
// persists the provider identity in the same rebuild transaction. The caller
// owns the runtime build begin/end log pair.
func (a *App) swapModelController(tab *WorkspaceTab, tabID, name string, newCtrl *control.Controller, oldCtrl control.SessionAPI, prevPath string, runtime normalizedTabRuntime, effortOverride *string, pendingSequence uint64, carried []provider.Message, timing *modelSwitchTiming) error {
	stageStarted := time.Now()
	path := agent.ContinueSessionPath(prevPath, newCtrl.SessionDir(), newCtrl.Label())
	if err := a.ensureTabSessionLeaseForRebuild(tab, path, "model"); err != nil {
		newCtrl.Close()
		return err
	}
	restoredRuntime, err := resumeControllerRuntimeWithMessages(newCtrl, carried, path, runtime)
	if err != nil {
		newCtrl.Close()
		return err
	}
	timing.LeaseAndResume = time.Since(stageStarted)
	stageStarted = time.Now()
	a.mu.Lock()
	if err := a.authorizeTabReplacementLocked(tab, newCtrl, "switching model", "model-switch"); err != nil {
		// The tab was closed/replaced while we built the new controller off-lock;
		// adopting it now would leak the runtime onto an orphaned tab and pin the
		// session lease forever.
		a.mu.Unlock()
		newCtrl.Close()
		tab.releaseSessionLease()
		return err
	}
	tab.Ctrl = newCtrl
	tab.model = name
	tab.effort = cloneStringPtr(effortOverride)
	tab.Label = newCtrl.Label()
	applyNormalizedRuntimeToTabLocked(tab, restoredRuntime)
	// Supersede any in-flight startup build: it would otherwise finish later,
	// overwrite this controller, and release/steal the tab's session lease.
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	if oldCtrl != nil {
		oldCtrl.Close()
	}
	// A refresh queued during this build still owns its newer sequence.
	a.clearDeferredRebuildVersion(tab.ID, pendingSequence)
	a.persistTabSessionPath(tab, path)
	// Keep the provider identity in the session sidecar inside the same
	// runtimeRebuildMu transaction as the controller swap. Empty sessions do
	// not autosave a turn, so without this write a later startup can prefer the outgoing provider from stale metadata. Serializing it here also preserves last-click-wins when a new-session default switch overlaps an explicit model selection.
	if path != "" {
		if err := agent.SetBranchModelPreserveUpdated(path, name); err != nil {
			return fmt.Errorf("persist selected model: %w", err)
		}
	}
	// A model switch changes the pricing context; discard the session-local
	// automatic wallet hint and let the next balance response rebind it.
	tab.clearRuntimeDisplayCurrency()
	a.notifyTabRuntimeRebuilt(tab)
	timing.SwapAndPersist = time.Since(stageStarted)
	return nil
}
