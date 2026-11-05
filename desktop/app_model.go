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
// entry, mirroring plugin-namespaced fallback and provider-access gating. Shared by the model-switch fast path and the build+swap path (task 148) so the two cannot drift on what counts as a switchable model. For plugin-namespaced refs the descriptor ref comes back as the canonical ref and pluginRef is true (the entry stays nil — extension sidecars own it). tl1389 P0: the caller owns the config load — one load per switch feeds every resolution instead of one per call.
func (a *App) resolveTabModelRef(tab *WorkspaceTab, cfg *config.Config, name string) (string, *config.ProviderEntry, bool, error) {
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

// resolvedTabModelLabel mirrors boot's controller label derivation (the
// entry's model, plus the planner suffix when a planner model is configured)
// for the pre-build tab label (tl1392): the build paths seed tab.Label with
// the raw provider ref the moment they start, and ctrl.Label() only lands at
// publication — so the whole boot window (seconds) showed e.g.
// "glm-coding-plan-cn-39e5…" in the composer. Resolution reuses
// resolveTabModelRef so plugin-namespaced fallback and provider-access gating
// cannot drift from the switch path; a ref that does not resolve (unknown
// model, nil config, no controller for plugin refs) falls back to the raw
// input — the old behavior — rather than failing the caller. cfg is the
// caller's load; call this OFF a.mu (it reads the controller catalog).
func (a *App) resolvedTabModelLabel(tab *WorkspaceTab, cfg *config.Config, ref string) string {
	raw := strings.TrimSpace(ref)
	if raw == "" || cfg == nil {
		return raw
	}
	canonical, entry, pluginRef, err := a.resolveTabModelRef(tab, cfg, raw)
	if err != nil {
		return raw
	}
	name := ""
	if pluginRef {
		// boot synthesizes the plugin entry from the merged resolver catalog
		// and labels it with the entry's model segment (splitProviderRef
		// semantics): the part after the first slash, else the descriptor
		// model.
		if d, found := extensionModelDescriptor(a.providerCatalogForTab(tab), canonical); found {
			if _, model, ok := strings.Cut(d.Ref, "/"); ok && model != "" {
				name = model
			} else {
				name = d.Model
			}
		}
	} else if entry != nil {
		name = entry.Model
	}
	if name == "" {
		return raw
	}
	if planner := strings.TrimSpace(cfg.Agent.PlannerModel); planner != "" {
		if pe, ok := cfg.ResolveModel(planner); ok {
			name = name + " + planner " + pe.Model
		}
	}
	return name
}

// modelSwitchPersonaBoundary reports whether the switch crosses the official
// DeepSeek-V4-Pro persona boundary (task 602). The persona is prepended to the
// cache-stable system prompt at boot (config.ApplyOfficialDeepSeekV4ProPersona) and ReasoningLanguageForEntry pins its auto reasoning language — neither has an override seam, so crossing the boundary keeps the build+swap path, which re-derives the prompt exactly as boot does. Plugin-namespaced targets (nil entry) never apply the persona. tl1389 P0: the caller's per-switch config load feeds the gate.
func modelSwitchPersonaBoundary(cfg *config.Config, currentRef string, target *config.ProviderEntry) bool {
	if cfg == nil {
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
	audioInput *bool // task 1008: the frozen audio gate follows the switched model
}

// modelSwitchExtras derives the entry-scoped payload the hot switch needs
// (task 602): the agent override scalars plus the controller identity, exactly
// the surfaces a rebuild re-derives from the new entry (boot folds the same pricing/window/high-speed inputs into the executor and the controller). tl1389 P0: the caller's per-switch config load feeds the derivation.
func modelSwitchExtras(cfg *config.Config, ref string, entry *config.ProviderEntry) (agent.ModelOverrideExtras, modelSwitchIdentity, error) {
	if entry == nil {
		// Plugin-namespaced ref: the sidecar owns the entry; the agent-side
		// resolver seam constructs the provider, zero extras keep the
		// construction scalars.
		return agent.ModelOverrideExtras{}, modelSwitchIdentity{}, nil
	}
	if cfg == nil {
		return agent.ModelOverrideExtras{}, modelSwitchIdentity{}, fmt.Errorf("config unavailable for model %q", ref)
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
	resolvedCapability := config.NewModelCapabilityResolver().Resolve(entry)
	imageEnabled := resolvedCapability.State == config.CapabilitySupported
	audioEnabled := resolvedCapability.AudioInputEnabled
	identity := modelSwitchIdentity{
		label:      label,
		balanceURL: entry.BalanceURL,
		balanceKey: entry.APIKey(),
		imageInput: &imageEnabled,
		audioInput: &audioEnabled,
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
	// tl1389 P0: one config load feeds the whole switch — fast-path
	// resolution, persona gate, override extras and the effort fold all read
	// this snapshot. Was three+ full loads per switch (one per helper); the
	// rebuild path still loads its own inside boot.Build, unchanged.
	switchCfg, err := config.LoadForRoot(switchSnap.workspaceRoot)
	if err != nil {
		return err
	}
	switchRef, switchEntry, switchPluginRef, err := a.resolveTabModelRef(tab, switchCfg, name)
	if err != nil {
		return err
	}
	timing.Config = time.Since(stageStarted)
	if handled, err := a.tryModelSessionOverride(tab, tabID, switchRef, switchCfg, switchSnap, switchEntry, totalStarted); err != nil {
		return err
	} else if handled {
		return nil
	}
	// tl1389 (the task-1301 effort seam carried to the model side): reaching
	// this point = both fast paths declined and this call will hold for the
	// full runtime rebuild — seconds on recovery-forked sessions, which reads
	// as a frozen composer. Tell the frontend now so the model name shows its
	// "applying" transition; the settle push below and runtime:rebuilt clear
	// it. Fast paths never emit, so normal sessions keep their instant behavior.
	a.notifyTabModelApplying(tab)
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
	// tl1389 P0: on the same root the fast-path resolution already pinned the
	// answer — the config snapshot is shared, and a plugin-namespaced ref can
	// only have resolved when a controller (hence the same merged catalog)
	// was already live, so the attach attempts between the two snapshots
	// cannot move it. A mid-switch root change reloads.
	var canonicalRef string
	var entry *config.ProviderEntry
	var pluginRef bool
	if snap.workspaceRoot == switchSnap.workspaceRoot {
		canonicalRef, entry, pluginRef = switchRef, switchEntry, switchPluginRef
	} else {
		cfg, cfgErr := config.LoadForRoot(snap.workspaceRoot)
		if cfgErr != nil {
			return cfgErr
		}
		canonicalRef, entry, pluginRef, err = a.resolveTabModelRef(tab, cfg, name)
		if err != nil {
			return err
		}
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
// runtimeRebuildMu and tab.turnStartMu. switchStarted is the entry clock of
// SetModelForTab (before the rebuild locks) so the fast-path elapsed_ms on
// the log line measures what the user's click actually waited — the same
// seam task 148 already wired for the effort fast path (task 909: the model
// side had no comparable reading, so "切换模型快不快" was unmeasurable).
// tl1389 P0: switchCfg is the caller's per-switch config load.
func (a *App) tryModelSessionOverride(tab *WorkspaceTab, tabID, switchRef string, switchCfg *config.Config, switchSnap tabRuntimeSnapshot, switchEntry *config.ProviderEntry, switchStarted time.Time) (bool, error) {
	// Alias-folded same-model short circuit: the raw check above runs before
	// resolution; tab.model holds the canonical ref the tab currently runs.
	a.mu.RLock()
	sameModel := switchRef == tab.model
	a.mu.RUnlock()
	if sameModel {
		slog.Info("desktop: model switch", "tab", tabID, "path", "fast-same-model", "model", switchRef, "elapsed_ms", time.Since(switchStarted).Milliseconds()) // task 148: same-model exits before arming anything.
		return true, nil
	}
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		if !modelSwitchPersonaBoundary(switchCfg, tab.model, switchEntry) {
			// Task 1161b: fold the carried effort onto the destination before
			// the arm — a level the destination cannot express (deepseek max →
			// MiMo) would fail the destination's boot gate inside Resolve and
			// decline the whole fast path, and even a successful arm would
			// keep the stale depth riding requests the destination's gate
			// rejects. The fold is the same NormalizeEffort mapping the
			// build+swap path applies (max→high on MiMo, low/medium→high on
			// generic deepseek); a fold error means "no expressible depth" and
			// lands on auto, exactly like the rebuild path's effortOverride
			// normalization. auto (nil/empty) carries as auto — the inherit
			// default never becomes a concrete level.
			foldedEffort := ""
			foldFrom := ""
			foldNote := ""
			foldKnown := false
			if switchEntry != nil {
				foldKnown = true
				a.mu.RLock()
				carried := cloneStringPtr(tab.effort)
				srcRef := tab.model
				a.mu.RUnlock()
				if carried != nil {
					display := config.EffortDisplay(&config.ProviderEntry{Effort: *carried})
					if display == "auto" {
						foldedEffort = "" // auto is the inherit default; it carries as auto on every destination.
					} else if normalized, err := config.NormalizeEffort(switchEntry, display); err != nil {
						foldedEffort = ""
						foldFrom = display
						// Task 1169: behind experimental_effort_cross_model_map,
						// a depth the destination cannot spell is rank-mapped
						// onto its relative slot (deepseek max → the
						// destination's ceiling) instead of collapsing to auto.
						// The source entry rides the same per-root snapshot as
						// the switch, and the read happens only on this
						// already-rare fold-failure path — the direct-hit
						// majority pays nothing. MapEffortAcrossVocabularies
						// returns destination-vocabulary values only (it
						// re-checks the wire gates), so the 1161b
						// vocabulary check downstream keeps holding as the
						// safety net.
						if switchCfg != nil && switchCfg.Agent.ExperimentalEffortCrossModelMap {
							if srcEntry, srcOK := switchCfg.ResolveModel(srcRef); srcOK {
								if mapped, note, mapOK := config.MapEffortAcrossVocabularies(srcEntry, switchEntry, display); mapOK {
									foldedEffort = mapped
									foldNote = note
								}
							}
						}
					} else {
						foldedEffort = normalized
						if normalized != display {
							foldFrom = display
						}
					}
				}
			}
			extras, identity, extrasErr := modelSwitchExtras(switchCfg, switchRef, switchEntry)
			if extrasErr != nil {
				return false, extrasErr
			}
			arm := func() bool {
				if setter, ok := ctrl.(interface {
					SetSessionModelOverride(string, agent.ModelOverrideExtras) bool
				}); ok {
					return setter.SetSessionModelOverride(switchRef, extras)
				}
				return false
			}
			if effortSetter, ok := ctrl.(interface {
				SetSessionModelOverrideWithEffort(string, agent.ModelOverrideExtras, string) bool
			}); ok && foldKnown {
				if !effortSetter.SetSessionModelOverrideWithEffort(switchRef, extras, foldedEffort) {
					return false, nil
				}
			} else if !arm() {
				return false, nil
			}
			if idSetter, ok := ctrl.(interface {
				SetModelIdentity(ref, label, balanceURL, balanceKey string, imageInput, audioInput *bool)
			}); ok {
				idSetter.SetModelIdentity(switchRef, identity.label, identity.balanceURL, identity.balanceKey, identity.imageInput, identity.audioInput)
			}
			a.mu.Lock()
			tab.model = switchRef
			if foldKnown {
				// Task 1161b: the tab now runs the folded depth — the value the
				// destination actually expresses ("" = auto), the same shape the
				// build+swap swapModelController persists.
				tab.effort = cloneStringPtr(&foldedEffort)
			}
			tab.Label = identity.label
			a.saveTabsLocked()
			a.mu.Unlock()
			// Task 1161b: the fold is user-visible state — say so, or a silent
			// max→high reads as the switch having lost the setting. Task 1169:
			// a special mapping (a destination whose only depth is thinking-off)
			// carries its own explanatory note instead of the generic fold copy.
			if foldFrom != "" {
				if foldNote != "" {
					a.noticeForTab(tabID, foldNote)
				} else {
					foldedDisplay := foldedEffort
					if foldedDisplay == "" {
						foldedDisplay = "auto (provider default)"
					}
					a.noticeForTab(tabID, fmt.Sprintf("effort %s is not available on %s; switched to %s", foldFrom, switchEntry.Name, foldedDisplay))
				}
			}
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
			logAttrs := []any{"tab", tabID, "path", "fast-per-request", "model", switchRef, "elapsed_ms", time.Since(switchStarted).Milliseconds()} // task 148/602: fast path observability (paired against runtime build end path=fallback); elapsed_ms: task 909.
			if foldFrom != "" {
				logAttrs = append(logAttrs, "effort_fold", foldFrom+"->"+foldedEffort) // task 1161b: the depth that rode the switch.
			}
			slog.Info("desktop: model switch", logAttrs...)
			// tl1389: the fast path wrote tab.Label/tab.effort with no agent
			// event to carry them — push the settled meta so the composer's
			// model name updates on the event instead of the frontend's own
			// pull chain (MetaForTab → ContextUsage → Effort round trips).
			a.notifyTabMetaRefresh(tab)
			return true, nil
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
	// Task 890: hand the outgoing controller's background-job manager and
	// write lease to the replacement so background jobs survive the swap
	// (nil/nil falls back to a fresh pair — pre-890 behavior).
	handoffJobs, handoffLease := jobsHandoffFrom(oldCtrl)
	// tl1389 P0: feed the 363A runtime-reuse pool into the model-switch
	// rebuild (same shape as the rebind chain in app_session_rebind.go).
	// Acquire the OUTGOING generation's assembly — keyed by the tab's
	// pre-switch root/model/effort identity, the one identity guaranteed warm
	// once the tab has built; the new-model key would be cold on first
	// arrival. Skill/command/hook discovery and the prompt re-scan are
	// model-independent, and boot re-derives the persona on top of the reused
	// prompt (strip+prepend), so a boundary crossing stays correct. A fast
	// effort switch between builds can leave the warm entry under the older
	// effort key — the same accepted miss profile the rebind chain carries.
	// ReuseAssembly is nil when the gate is off or the pool is cold and the
	// build is then byte-identical to the pre-tl1389 one.
	oldEffortKey := ""
	if snap.effort != nil {
		oldEffortKey = *snap.effort
	}
	reuseAssembly := a.acquireRuntimeAssembly(runtimeAssemblyKeyForTab(snap.workspaceRoot, snap.model, oldEffortKey))
	newEffortKey := ""
	if effortOverride != nil {
		newEffortKey = *effortOverride
	}
	newCtrl, assembly, err := boot.BuildWithAssembly(a.bootContext(), boot.Options{
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
		OnSessionPromoted:          a.handleTabSessionPromoted(tab),
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
		OnSessionReopen:            a.collabSessionReopen,
		OnSessionTurnStatus:        a.collabSessionTurnStatus,
		OnCascadeDelegate:          cascadeDelegateFor,
		OnCascadeSourceCapability:  cascadeSourceCapabilityFor,
		OnFallbackSwitch:           a.fallbackSwitch,
		OnDeleteSession:            a.deleteCollabSession,
		OnRegistryShellProbe:       a.resolveRegistryShell,
		OnRegistryShellTrash:       a.trashRegistryShell,
		OnRenameSession:            a.renameCollabSession,
		OnMoveTopicToGroup:         a.moveCollabTopicToGroup,
		OnManageSessions:           a.AgentManageSessions,
		// Keep the private temporary directory across model switches (#7575).
		SessionTemp: sessionTempFromController(oldCtrl),
		// Task 890: the shared pair keeps background jobs alive across the swap.
		// ReuseAssembly lives on the embedded RuntimeReload; promoted names
		// cannot appear in the outer literal, so name the embedded struct
		// (same shape as the rebind chain and the startup build).
		RuntimeReload: boot.RuntimeReload{
			ReuseAssembly: reuseAssembly,
			PreviousPlan:  boot.FullReusePlan(),
		},
		Jobs:           handoffJobs,
		WorkspaceLease: handoffLease,
	})
	if err != nil {
		return nil, err
	}
	// Task 405 Q1: publish the freshly built assembly under the post-switch
	// identity so switching back to this model (or any later same-key
	// rebuild) skips discovery too. No-op when the gate is off or the build
	// reused without producing a new assembly.
	a.storeRuntimeAssembly(runtimeAssemblyKeyForTab(snap.workspaceRoot, name, newEffortKey), assembly)
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
	// Task 890: a handoff-built replacement adopts the outgoing job manager
	// first, then the outgoing close keeps its background jobs running.
	closeReplacedControllerAfterHandoff(newCtrl, oldCtrl)
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
	// tl1389: push the settled meta right behind the runtime:rebuilt fence on
	// the same sink lane — the rebuild wrote tab.Label/tab.effort with no
	// agent event either, and the fence-first ordering keeps the frontend
	// from merging pre-swap state after the new identity.
	a.notifyTabMetaRefresh(tab)
	timing.SwapAndPersist = time.Since(stageStarted)
	return nil
}
