package main

import (
	"errors"
	"log/slog"
	"reasonix/internal/agent"
	"reasonix/internal/config"
	"strings"
	"time"
)

// SetCloseBehavior updates desktop-only window close behavior without rebuilding
// the active controller. It must stay out of provider-visible prompt/request data.
func (a *App) SetCloseBehavior(mode string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopCloseBehavior(mode) })
}

// SetStatusBarStyle updates the desktop status bar metric label style. UI-only,
// no rebuild needed.
func (a *App) SetStatusBarStyle(style string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopStatusBarStyle(style) })
}

// SetStatusBarItems updates the ordered visible desktop status bar items.
// UI-only, no rebuild needed.
func (a *App) SetStatusBarItems(items []string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopStatusBarItems(items) })
}

// SetQuickCommands replaces the composer's quick-command snippets (task 18).
// UI-only: the composer reads them from the settings snapshot, so no rebuild is
// required the way provider or effort changes need one.
func (a *App) SetQuickCommands(entries []config.QuickCommandEntry) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetQuickCommands(entries) })
}

// SetDesktopLanguage updates the desktop UI language and the user-level response
// language preference used by model-facing desktop sessions.
func (a *App) SetDesktopLanguage(lang string) error {
	responseLanguage := ""
	mutate := func(c *config.Config) error {
		if err := c.SetDesktopLanguage(lang); err != nil {
			return err
		}
		if err := c.SetLanguage(lang); err != nil {
			return err
		}
		responseLanguage = c.ResponseLanguage()
		return nil
	}
	err := a.applyConfigOnly(mutate)
	if err != nil {
		return err
	}
	if strings.TrimSpace(lang) != "" && !strings.EqualFold(strings.TrimSpace(lang), "auto") {
		a.setDesktopLocale(lang)
	}
	refreshBackendNoticeLocale(lang)
	a.updateTrayLocale(lang)
	a.applyResponseLanguageToLiveControllers(responseLanguage)
	return nil
}

// SetDesktopCurrency persists a display-only preference and re-selects the
// occurrence-time valuations already stored in each tab. Provider price tables
// and live controllers are intentionally untouched.
func (a *App) SetDesktopCurrency(currency string) error {
	err := a.applyConfigOnly(func(c *config.Config) error {
		return c.SetDesktopCurrency(currency)
	})
	if err != nil {
		return err
	}

	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	a.mu.RLock()
	tabs := append([]*WorkspaceTab(nil), a.runtimeTabsLocked()...)
	a.mu.RUnlock()
	for _, tab := range tabs {
		a.repriceTabUsageForCurrentCurrency(tab)
	}
	return nil
}

func (a *App) desktopEffectivePricingCurrency(cfg *config.Config) string {
	// Display currency only — never the provider list-price region.
	if cfg == nil {
		return ""
	}
	if pref := cfg.DisplayCurrencyPref(); pref != "" {
		return pref
	}
	return cfg.ExplicitDisplayCurrency()
}

func (a *App) desktopOfficialPricingLanguage(cfg *config.Config) string {
	// Used only for display-language adjacent UI; list prices use billing_currency.
	if a.desktopEffectivePricingCurrency(cfg) == "CNY" {
		return "zh"
	}
	return "en"
}

// SetTrayLocale mirrors the resolved desktop UI language into the native tray
// menu. It is runtime-only; the persisted preference remains [desktop].language.
func (a *App) SetTrayLocale(locale string) error {
	a.setDesktopLocale(locale)
	trayLocale := "en"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "zh") {
		trayLocale = "zh"
	}
	a.updateTrayLocale(trayLocale)
	a.emitProjectTreeChanged()
	return nil
}

// SetDesktopAppearance updates only desktop theme preferences. It does not
// rebuild the active controller and must stay out of provider-visible requests.
func (a *App) SetDesktopAppearance(theme, style string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopAppearance(theme, style) })
}

// SetDesktopTerminalTheme updates only the integrated terminal colours. It is
// applied live by the frontend and does not rebuild the active controller.
func (a *App) SetDesktopTerminalTheme(theme string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopTerminalTheme(theme) })
}

// SetDesktopLayoutStyle updates only the desktop layout style. It does not
// rebuild the active controller and must stay out of provider-visible requests.
func (a *App) SetDesktopLayoutStyle(style string) error {
	normalized := ""
	if err := a.applyConfigOnly(func(c *config.Config) error {
		if err := c.SetDesktopLayoutStyle(style); err != nil {
			return err
		}
		normalized = c.DesktopLayoutStyle()
		return nil
	}); err != nil {
		return err
	}
	if singleSurfaceLayoutStyle(normalized) {
		return a.applySingleSurfaceTabPolicy()
	}
	return nil
}

// SetDesktopCheckUpdates updates only the desktop startup update-check
// preference. Manual checks in Settings are unaffected.
func (a *App) SetDesktopCheckUpdates(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopCheckUpdates(enabled) })
}

// SetDesktopUpdateChannel is retained for older Wails clients. The config layer
// clears the retired preference and every updater request uses Stable.
func (a *App) SetDesktopUpdateChannel(channel string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopUpdateChannel(channel) })
}

// SetDesktopTelemetry sets whether the desktop sends the anonymous launch ping.
func (a *App) SetDesktopTelemetry(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopTelemetry(enabled) })
}

// SetSessionStorage selects the conversation store mode (task 155: v3_only,
// dual_write_read_v3, dual_write_read_v4, v4_only). The write side is fixed when
// the process boots - modes 2/3/4 are created at boot and v3_only runs without a
// mirror - so enabling or retiring the mirror takes a restart and the settings
// view flags that. Moving between the two dual-write modes only changes which
// copy history prefers, so it is applied to the live controllers immediately.
func (a *App) SetSessionStorage(mode string) error {
	previous, effective, err := a.setSessionStorageMode(mode)
	if err != nil {
		return err
	}
	if previous != effective && !config.SessionStorageNeedsRestart(previous, effective) {
		a.applySessionStorageReadSide(effective)
	}
	return nil
}

// SetExperimentalRestartUpdate toggles the restart-and-update action (task 81). The
// Settings switch and the restart_and_update tool read the same preference, so the
// error the tool returns stays true: what enables one enables the other.
func (a *App) SetExperimentalRestartUpdate(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalRestartUpdate(enabled) })
}

// SetExperimentalAutonomousUpdate toggles the agent-facing restart_update tool
// (task 254). Tool registration reads the boot snapshot, so the flip applies on
// the next restart — the settings pane says so next to the switch.
func (a *App) SetExperimentalAutonomousUpdate(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalAutonomousUpdate(enabled) })
}

// SetUpdateChime toggles the update-complete chime (task 277). The frontend
// owns the one-shot gate (last-chimed version), so this is a live config flip
// with no restart.
func (a *App) SetUpdateChime(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetUpdateChime(enabled) })
}

// SetUpdateChimeTune picks the update-chime melody (task 512): "nokia" or
// "mario". The frontend owns playback, so this is a live config flip — the
// next chime reads the new tune.
func (a *App) SetUpdateChimeTune(tune string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetUpdateChimeTune(tune) })
}

// SetDesktopForkNotice toggles the fork first-launch notice switch (task 670;
// default on per user ruling). A pure frontend gate, so this is a live config
// flip; re-enabling also clears the 「下次不提醒」 mute (the switch is the only
// UI path back from a stored mute).
func (a *App) SetDesktopForkNotice(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopForkNotice(enabled) })
}

// SetExperimentalFullAccess toggles the full-access (yolo) lab switch
// (task 257). Boot resolves it into the writable-root set and the bash spec,
// so the flip applies on the next restart — the settings pane says so.
func (a *App) SetExperimentalFullAccess(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalFullAccess(enabled) })
}

// SetExperimentalBaseProcess toggles the S1 resident-base-subprocess lab
// switch (design 2026-09-30 §7 R4). Boot resolves it when the base client
// section is built, so the flip applies on the next restart — the settings
// pane says so.
func (a *App) SetExperimentalBaseProcess(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalBaseProcess(enabled) })
}

// SetExperimentalToolOptimizations toggles the 工具优化 family (task 603).
func (a *App) SetExperimentalToolOptimizations(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalToolOptimizations(enabled) })
}

// SetAutonomousUpdateResume sets the auto-resume scope dial (task 254): off
// resumes nothing, goal_autopilot resumes goal runs and autopilot sessions
// that asked for the update, all additionally resumes every mid-turn session.
// Unlike the tool toggle this takes effect live — execute reads it when it
// fires, restore reads it when the new process boots.
func (a *App) SetAutonomousUpdateResume(mode string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetAutonomousUpdateResume(mode) })
}

// SetExperimentalSessionMonitor toggles the left-rail session monitor board (task
// 123): the diagnostics surface for transcript-cache residency and switch cost.
func (a *App) SetExperimentalSessionMonitor(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSessionMonitor(enabled) })
}

// SetExperimentalCascadeApproval toggles task 225: a dispatched session
// forwards its approval prompts to its autopilot parent's Ask channel.
func (a *App) SetExperimentalCascadeApproval(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCascadeApproval(enabled) })
}

// SetExperimentalFallbackModel toggles task 242's quota fallback switch
// (iron rule 2: off by default; settings → 实验特性 → 备用模型).
func (a *App) SetExperimentalFallbackModel(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalFallbackModel(enabled) })
}

// SetFallbackModel writes the task 242 fallback target ("provider/model";
// bare model ids are rejected by the config setter so identity stays
// unambiguous). Empty clears the target.
func (a *App) SetFallbackModel(model string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetFallbackModel(model) })
}

// SetExperimentalCompactModel toggles 任务 707's economic-compaction switch
// (iron rule 2: off by default — off keeps every summary on the conversation
// model). Boot snapshot + model-settings fingerprint: a change re-applies at
// the next run like the other model preferences.
func (a *App) SetExperimentalCompactModel(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCompactModel(enabled) })
}

// SetCompactModel writes the 任务 707 compression target ("provider/model";
// bare model ids are rejected by the config setter so identity stays
// unambiguous). Empty clears the target (switch may stay on, which then keeps
// the conversation model).
func (a *App) SetCompactModel(model string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetCompactModel(model) })
}

// SetSessionCollabGates writes the task-173 collaboration panel gates in one
// call, so the panel cannot half-apply (settings → 实验特性 → 跨会话通信).
func (a *App) SetSessionCollabGates(allowDelete, allowRequireReply, allowReadTail, allowCreate, allowSteer bool, dailySendLimit int) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetSessionCollabGates(&allowDelete, &allowRequireReply, &allowReadTail, &allowCreate, &allowSteer, &dailySendLimit)
	})
}

// SetSessionCollabMailDefaults writes the task-309 mailbox defaults in one
// call (settings → 实验特性 → 跨会话通信): idempotency default, read-receipt
// default, and the default delivery channel.
func (a *App) SetSessionCollabMailDefaults(idempotent, receiptDefault bool, defaultDelivery string) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetSessionCollabMailDefaults(&idempotent, &receiptDefault, &defaultDelivery)
	})
}

// SetSessionCollabBackground toggles task-264 background mode: on, pump
// stand-ups build a detached runtime with no visible tab; off is the baseline.
// A regular panel setting — read live per drain pass, no restart needed.
func (a *App) SetSessionCollabBackground(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetSessionCollabBackground(enabled)
	})
}

// SetSessionCollabReplyNudge toggles the task-530 turn-closure reply reminder
// (settings → 实验特性 → 跨会话通信). The runtime reads it as a boot snapshot
// (agent construction), so a change shows up after a restart — same contract
// as the other agent-behavior dials.
func (a *App) SetSessionCollabReplyNudge(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetSessionCollabReplyNudge(enabled)
	})
}

// SetExperimentalSplitView toggles the tab-bar split view (task 70-1): with it off the
// right-click menu keeps exactly the pre-split item list.
func (a *App) SetExperimentalSplitView(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSplitView(enabled) })
}

// SetExperimentalCollabGroupView toggles the sidebar group-chat entry (task 677):
// with it off the sidebar utility row keeps the pre-409 four-icon layout.
func (a *App) SetExperimentalCollabGroupView(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCollabGroupView(enabled) })
}

// SetExperimentalSessionCollabAutoFold toggles the transcript fold for
// over-long cross-session messages (task 705): off (default) renders every
// cross-session message in full.
func (a *App) SetExperimentalSessionCollabAutoFold(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSessionCollabAutoFold(enabled) })
}

// SetExperimentalTrajectoryView toggles the topicbar "transcript | trajectory"
// view switch and the trajectory surface (task 704): off (default) keeps the
// transcript as the only surface, exactly as before the feature existed.
func (a *App) SetExperimentalTrajectoryView(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalTrajectoryView(enabled) })
}

// SetExperimentalFeedback toggles the agent submit_feedback tool surface and the
// desktop feedback inbox panel (task 121).
func (a *App) SetExperimentalFeedback(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalFeedback(enabled) })
}

// SetExperimentalFeedbackNudge toggles the feedback touchpoint dial (task 172).
// The runtime reads it as a boot snapshot (agent construction), so a change
// shows up after a restart — same contract as the other agent-behavior dials.
func (a *App) SetExperimentalFeedbackNudge(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalFeedbackNudge(enabled) })
}

// SetExperimentalTodoSidebar toggles the right-dock todo tab with its tab
// visibility and wrap settings (task 259). The frontend snapshots the flag at
// boot, so the change shows up after a restart.
func (a *App) SetExperimentalTodoSidebar(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalTodoSidebar(enabled) })
}

// SetExperimentalSubagentPanel toggles the subagent panel package (task 495):
// the right-dock subagent tab plus default-collapsed ended subagent cards in
// deep mode. The frontend snapshots the flag at boot, so the change shows up
// after a restart.
func (a *App) SetExperimentalSubagentPanel(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSubagentPanel(enabled) })
}

// SetExperimentalSessionWall toggles the session graph wall (task 505): the
// command-palette "跳转会话" entry plus the grid wall it opens. The frontend
// snapshots the flag at boot, so the change shows up after a restart.
func (a *App) SetExperimentalSessionWall(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSessionWall(enabled) })
}

// SetExperimentalPromptHistoryPicker toggles the composer history-navigation
// safety rework (task 261, upstream #10425): clock-icon history picker plus
// the narrowed plain-ArrowUp trigger. Boot snapshot, restart to apply.
func (a *App) SetExperimentalPromptHistoryPicker(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalPromptHistoryPicker(enabled) })
}

// SetExperimentalTabCompress toggles the tab-strip adaptive compression (task
// 506): tiered tab-width reduction once more than 8 tabs are open. The
// frontend re-applies the snapshot on settings save, so the change is visible
// without a restart.
func (a *App) SetExperimentalTabCompress(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalTabCompress(enabled) })
}

// SetExperimentalHeartbeatBackground toggles the heartbeat background mode
// (task 742): scheduled runs park their tab detached (task 264 semantics)
// after a successful submit so nothing appears in the tab strip. The engine
// reads the switch at call time, so the next scheduled run applies the change
// without a restart.
func (a *App) SetExperimentalHeartbeatBackground(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalHeartbeatBackground(enabled) })
}

// SetExperimentalSubagentDetail toggles the subagent detail view (task 507):
// a dock row click opens the read-only in-dock detail view with a back
// button; off keeps the inline preview expansion plus the widen affordance.
// The frontend re-applies the snapshot on settings save, so the change is
// visible without a restart.
func (a *App) SetExperimentalSubagentDetail(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSubagentDetail(enabled) })
}

// SetTabPermissionIndicator stores the tab permission indicator setting
// (task 651): "badge" keeps the plan/goal/auto/yolo text badges, "off" hides
// the per-tab permission indicator, "background" paints a low-opacity (10%)
// per-mode tab background instead of the badges; the background face shares
// the badge colour tokens so one tier never shows two different hues. The
// frontend re-applies the snapshot on settings save, so the change is visible
// without a restart.
func (a *App) SetTabPermissionIndicator(mode string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetTabPermissionIndicator(mode) })
}

// Task 262 install-fix: the Wails exposure layer for the intake batch was
// missed while the config layer landed — the frontend's calls hit a missing
// App method at runtime, so the switch clicked but never saved (the installed
// user's "cannot turn it on" report). Eight wrappers, same shape as above.
// The seven lab-intake switches are nil-means-on on the config side; the
// wrapper only needs to forward the explicit value the user clicked.

// SetExperimentalQuickCommands toggles the whole quick-commands surface (262).
func (a *App) SetExperimentalQuickCommands(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalQuickCommands(enabled) })
}

// SetExperimentalCDPDebugPort toggles the WebView2 CDP debug endpoint (task
// 342). Ships off; the browser args are read at startup, so a flip needs a
// restart — the settings card communicates that and raises the restart banner.
func (a *App) SetExperimentalCDPDebugPort(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCDPDebugPort(enabled) })
}

// SetExperimentalZcodeTaskBus toggles the built-in zcode task bus (task 439).
// Ships off; the listener arms at desktop boot, so a flip needs a restart —
// the lab card communicates that.
func (a *App) SetExperimentalZcodeTaskBus(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalZcodeTaskBus(enabled) })
}

// SetExperimentalCompactionParallel toggles parallel chunked compaction (265).
func (a *App) SetExperimentalCompactionParallel(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCompactionParallel(enabled) })
}

// SetExperimentalHighSpeedModel toggles the high-speed model lane (task 318.1,
// iron rule 2: default off; applies from the next boot — boot-snapshot read).
func (a *App) SetExperimentalHighSpeedModel(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalHighSpeedModel(enabled) })
}

// SetProviderModelHighSpeed marks or unmarks one model of one provider as
// high-throughput (task 468). The panel's model dialog writes the list through
// the provider save chain (SaveProvider → saveProviderConfig carries
// HighSpeedModels); this is the direct provider-level setter for programmatic
// callers. Goes through the model-settings apply path like every provider
// write so the runtime picks up the new effective snapshot.
func (a *App) SetProviderModelHighSpeed(provider, model string, enabled bool) error {
	return a.applyModelConfigChange(func(c *config.Config) error { return c.SetProviderModelHighSpeed(provider, model, enabled) })
}

// SetExperimentalProactiveCompact toggles the configurable fold cooldown
// (task 318.2; live-read on the next fold, no restart).
func (a *App) SetExperimentalProactiveCompact(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalProactiveCompact(enabled) })
}

// SetProactiveCompactCooldownMinutes stores the fold cooldown in minutes
// (task 318.2; 0/negative normalize to 10).
func (a *App) SetProactiveCompactCooldownMinutes(minutes int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetProactiveCompactCooldownMinutes(minutes) })
}

// SetExperimentalColdCacheCompact toggles the task-297 cold-cache pass
// (the desktop tick live-reads it; off by default).
func (a *App) SetExperimentalColdCacheCompact(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalColdCacheCompact(enabled) })
}

// SetColdCacheCompactMinBytes stores the context size floor for the pass
// (task 297; 0 = built-in 600 KiB, clamped band rejected outside range).
func (a *App) SetColdCacheCompactMinBytes(bytes int64) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetColdCacheCompactMinBytes(bytes) })
}

// SetColdCacheCompactIdleMinutes stores the idle floor for the pass
// (task 297; 0 = built-in 300 minutes).
func (a *App) SetColdCacheCompactIdleMinutes(minutes int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetColdCacheCompactIdleMinutes(minutes) })
}

// SetExperimentalComposerDraft toggles cross-restart composer draft
// persistence (task 318.3; the frontend live-reads this on settings change).
// SetExperimentalSelectionActions toggles the selection quick-actions
// floating card (task 369). Ships off (fork rule 2): off = the selection
// menu is exactly the pre-369 surface.
func (a *App) SetExperimentalSelectionActions(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		c.Agent.ExperimentalSelectionActions = enabled
		return nil
	})
}

func (a *App) SetExperimentalComposerDraft(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalComposerDraft(enabled) })
}

// SetExperimentalContextBudget toggles the per-turn context-state line (265).
func (a *App) SetExperimentalContextBudget(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalContextBudget(enabled) })
}

// SetExperimentalResearchBudget toggles the read-only budget extension (265).
func (a *App) SetExperimentalResearchBudget(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalResearchBudget(enabled) })
}

// SetPreapproveManagedPaths stores the task-231 master switch plus its four
// checkboxes in one write (so a settings save can never land half-applied).
// All five ship false; the bypass itself only ever arms under autopilot.
func (a *App) SetPreapproveManagedPaths(enabled, skills, hooks, sessionStores, bashEscape bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetPreapproveManagedPaths(enabled, skills, hooks, sessionStores, bashEscape)
	})
}

// SetExperimentalActiveTabResident toggles the task-192 residency policy
// (ships off; applied to the transcript store through the preferences sync).
func (a *App) SetExperimentalActiveTabResident(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetExperimentalActiveTabResident(enabled)
	})
}

// SetExperimentalOpenCodeGoUsage toggles the task-163 usage card (ships off;
// the frontend gate issues no usage query while it is off).
func (a *App) SetExperimentalOpenCodeGoUsage(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetExperimentalOpenCodeGoUsage(enabled)
	})
}

// SetExperimentalQuestionSearch toggles the topic-bar question-search entry (265).
func (a *App) SetExperimentalQuestionSearch(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalQuestionSearch(enabled) })
}

// SetExperimentalSubagentPolicy toggles the delegation-tier entry points (265).
func (a *App) SetExperimentalSubagentPolicy(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSubagentPolicy(enabled) })
}

// SetExperimentalSubagentTps toggles the sub-agent tok/s readouts (265).
func (a *App) SetExperimentalSubagentTps(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSubagentTps(enabled) })
}

// SetExperimentalCompletionSummary toggles the per-turn result notice (265).
func (a *App) SetExperimentalCompletionSummary(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCompletionSummary(enabled) })
}

// SetExperimentalLocalServer toggles the Settings → 本地服务 page (task 130).
func (a *App) SetExperimentalLocalServer(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalLocalServer(enabled) })
}

// SetExperimentalPathRules toggles structured path-scope evaluation (task 134).
func (a *App) SetExperimentalPathRules(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalPathRules(enabled) })
}

// SetExperimentalLifecycleNoiseGate toggles the task-377 lifecycle noise
// triage (skip crash reports for clean-shutdown residue). The gate is read
// once at startup diagnostics setup, so the flip lands on restart; off
// (default) keeps the reporting behavior byte-for-byte unchanged.
func (a *App) SetExperimentalLifecycleNoiseGate(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalLifecycleNoiseGate(enabled) })
}

// SetExperimentalCacheTuning toggles the transcript cache-size controls (task 161).
func (a *App) SetExperimentalCacheTuning(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCacheTuning(enabled) })
}

// SetTranscriptCacheTuning persists the three cache-tuning values (task 161).
func (a *App) SetTranscriptCacheTuning(maxCachedTabs, historyBodyBudgetMb, markdownBudgetMb int) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetTranscriptCacheTuning(maxCachedTabs, historyBodyBudgetMb, markdownBudgetMb)
	})
}

// SetExperimentalTraceAsState toggles Trace-as-State compaction (task 60).
func (a *App) SetExperimentalTraceAsState(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalTraceAsState(enabled) })
}

// SetExperimentalDream toggles dream/distill memory-curation tools (task 115).
func (a *App) SetExperimentalDream(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalDream(enabled) })
}

// SetExperimentalSafetyCostControl toggles the merged unattended-safety
// switch (task 517): one「安全 / 成本控制」knob for the task-244 B1/B2/B3
// guards. B1 reads the saved switch at call time (no restart); B2/B3 ride
// boot snapshots — sessions rebuild on restart.
func (a *App) SetExperimentalSafetyCostControl(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSafetyCostControl(enabled) })
}

// SetSafetyIdleTerminate writes the B1 sub-switch override (task 722).
// Effective at the next heartbeat run — the B1 gate reads the saved config at
// call time, so no restart is needed.
func (a *App) SetSafetyIdleTerminate(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetSafetyIdleTerminate(enabled) })
}

// SetSafetyLoopStreakNote writes the B2 sub-switch override (task 722).
// B2 rides the boot snapshot — sessions pick it up on restart.
func (a *App) SetSafetyLoopStreakNote(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetSafetyLoopStreakNote(enabled) })
}

// SetSafetyEventWaitRecheck writes the B3 sub-switch override (task 722).
// B3 rides the boot snapshot — sessions pick it up on restart.
func (a *App) SetSafetyEventWaitRecheck(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetSafetyEventWaitRecheck(enabled) })
}

// SetExperimentalOrphanHandling toggles the merged orphan switch (task 449):
// both the lease reclaim (task 244 B5) and the recovery-store sweep (B4)
// follow this one key. Read at call time — no restart needed.
func (a *App) SetExperimentalOrphanHandling(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalOrphanHandling(enabled) })
}

// SetExperimentalOrphanLeaseReclaim is the pre-449 binding kept as a delegate
// (the generated wails surface still exports it); it toggles the merged
// orphan switch now.
func (a *App) SetExperimentalOrphanLeaseReclaim(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalOrphanLeaseReclaim(enabled) })
}

// SetExperimentalRecoveryOrphanSweep is the pre-449 binding kept as a delegate
// (the generated wails surface still exports it); it toggles the merged
// orphan switch now.
func (a *App) SetExperimentalRecoveryOrphanSweep(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalRecoveryOrphanSweep(enabled) })
}

// SetExperimentalRuntimeReuse toggles the task-363A runtime assembly reuse
// pool. Read at Build time per new tab — no restart needed for new tabs.
func (a *App) SetExperimentalRuntimeReuse(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalRuntimeReuse(enabled) })
}

// SetExperimentalPerfMonitor toggles the host performance monitor (task 184).
// Restart-scoped: interval and file table are read while the app starts.
func (a *App) SetExperimentalPerfMonitor(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalPerfMonitor(enabled) })
}

// SetPerfMonitorIntervalSeconds sets the sampler interval (task 184); the config
// layer clamps it to 1..300 and 0 restores the 5s default.
func (a *App) SetPerfMonitorIntervalSeconds(seconds int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetPerfMonitorIntervalSeconds(seconds) })
}

// SetExperimentalHeapHighProfile toggles the threshold-triggered heap snapshot
// (task 501). Restart-scoped like the perf monitor itself: the trigger rides
// its sampling loop, which is built once at boot.
func (a *App) SetExperimentalHeapHighProfile(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalHeapHighProfile(enabled) })
}

// perfMonitorHeapHighThresholdForView resolves the heap-high threshold for the
// settings views (task 528): the [desktop] settings-view mirror wins, a 0 there
// falls back to the hand-edited [agent] value, and two zeros mean "built-in
// default 6GB" (the frontend renders that default).
func perfMonitorHeapHighThresholdForView(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	if cfg.Desktop.PerfMonitorHeapHighThresholdMB != 0 {
		return cfg.Desktop.PerfMonitorHeapHighThresholdMB
	}
	return cfg.Agent.PerfMonitorHeapHighThresholdMB
}

// SetPerfMonitorHeapHighThresholdMB sets the heap-high trigger threshold in MiB
// (task 528 UI); the config layer clamps explicit values into 1024..131072 and
// 0 restores the built-in 6GB default. Restart-scoped: the trigger is built
// once at monitor boot.
func (a *App) SetPerfMonitorHeapHighThresholdMB(mb int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetPerfMonitorHeapHighThresholdMB(mb) })
}

// SetSessionCollabHopLimit sets the cross-session chain ceiling (task 204); the config
// layer clamps it, so an out-of-range entry never reaches the file.
func (a *App) SetSessionCollabHopLimit(limit int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetSessionCollabHopLimit(limit) })
}

// SetDetachedIdleReleaseMinutes sets the task-308-O4 idle threshold; the config
// layer clamps it (0..10080). Applied live by the release loop's next tick.
func (a *App) SetDetachedIdleReleaseMinutes(minutes int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDetachedIdleReleaseMinutes(minutes) })
}

// SetGoMemLimitMB sets the task-308-O3 soft memory limit (0 = unbounded) and
// re-applies it to the live runtime immediately: mb>0 installs the limit via
// debug.SetMemoryLimit, mb==0 clears any previously installed limit. The slog
// line inside applyGoMemLimit/clearGoMemLimit is the user-visible evidence —
// the change is never silent.
func (a *App) SetGoMemLimitMB(mb int) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetGoMemLimitMB(mb) }); err != nil {
		return err
	}
	// Re-read through the same view the startup path uses so the live limit
	// always matches what a restart would install.
	if cfg, _, err := a.loadDesktopUserConfigForView(); err == nil {
		if mb > 0 {
			applyGoMemLimit(cfg)
		} else {
			clearGoMemLimit()
		}
	}
	return nil
}

// SetExperimentalAutoLoadOlder toggles the scroll-driven history trigger (fork
// task 160). The explicit "load older" button stays available either way.
func (a *App) SetExperimentalAutoLoadOlder(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalAutoLoadOlder(enabled) })
}

// SetExperimentalSessionCollab toggles multi-session collaboration (task 19).
func (a *App) SetExperimentalSessionCollab(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSessionCollab(enabled) })
}

// GetCollabInboxMergeMode exposes the drain-merge tri-state (task 221:
// off | same_sender | all) to the composer, which needs it for the task-366
// cross-hint: the guidance shelf can only explain "manual merge-next is
// unavailable under the merge-all tier" when it knows the tier.
func (a *App) GetCollabInboxMergeMode() string {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return "off"
	}
	mode := strings.TrimSpace(cfg.Agent.CollabInboxMerge)
	if mode == "" {
		return "off"
	}
	return mode
}

// SetStagingDir stores the fast-switch staging directory override (task 381).
// Light path on purpose: the key is read at restart-and-update time, not by
// the running tab runtime, so no rebuild is attempted and the save cannot be
// bounced by one. Empty restores the default staging directory. A missing
// directory is not an error here — the restart-and-update path names the
// exact path in its error when the artifacts are absent.
func (a *App) SetStagingDir(dir string) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetStagingDir(dir)
	})
}

// SetCollabInboxMerge sets the inbox drain merge tri-state (task 221):
// off | same_sender | all. The config layer normalizes unknown values to off.
func (a *App) SetCollabInboxMerge(mode string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetCollabInboxMerge(mode) })
}

// SetCollabGuidanceMerge toggles the guidance shelf's manual merge-next button
// (task 153). Off keeps the shelf exactly as before the feature existed.
func (a *App) SetCollabGuidanceMerge(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetCollabGuidanceMerge(enabled) })
}

// CreateDreamHeartbeatTask ensures a scheduled dream pass exists so the
// experiment can be exercised without hand-editing heartbeat-tasks.json.
// Returns created=true when a new task was appended; an existing Dream task
// is re-enabled instead of duplicated.
func (a *App) CreateDreamHeartbeatTask() (created bool, err error) {
	cfg, _, lerr := a.loadDesktopUserConfigForView()
	if lerr != nil {
		return false, lerr
	}
	if cfg != nil && !cfg.Agent.ExperimentalDream {
		return false, errors.New("enable the Dream experiment in Settings → Experimental first")
	}
	if a.heartbeat == nil {
		return false, errors.New("heartbeat engine is not running")
	}
	view := a.heartbeat.ReloadConfig()
	const title = "Dream 记忆整理"
	const prompt = "调用 dream 工具跑一轮记忆整理：扫描最近项目会话中的用户偏好，校验后写入项目 memory。若工具不可用或实验未开启，说明原因后结束。完成后用简短要点汇报本轮 dream 结果（新写入几条、跳过几条）。"
	for i, task := range view.Tasks {
		if task.Title == title || strings.Contains(strings.ToLower(task.ID), "dream-curation") {
			if !task.Enabled {
				view.Tasks[i].Enabled = true
				if _, serr := a.heartbeat.ReplaceConfig(HeartbeatConfigUpdate{
					Revision: view.Revision,
					ETag:     view.ETag,
					Tasks:    view.Tasks,
				}); serr != nil {
					return false, serr
				}
			}
			return false, nil
		}
	}
	root := a.activeWorkspaceRoot()
	scope, workspaceRoot := "global", ""
	if root != "" {
		scope, workspaceRoot = "project", root
	}
	view.Tasks = append(view.Tasks, HeartbeatTask{
		ID:                     a.HeartbeatGenerateID(),
		Title:                  title,
		Prompt:                 prompt,
		Interval:               "24h",
		Enabled:                true,
		Scope:                  scope,
		WorkspaceRoot:          workspaceRoot,
		NewConversationEachRun: true,
		ApprovalMode:           "auto",
		CreatedAt:              time.Now().UnixMilli(),
	})
	if _, serr := a.heartbeat.ReplaceConfig(HeartbeatConfigUpdate{
		Revision: view.Revision,
		ETag:     view.ETag,
		Tasks:    view.Tasks,
	}); serr != nil {
		return false, serr
	}
	return true, nil
}

// SetDesktopMetrics sets whether the desktop sends aggregate desktop metrics,
// starting or stopping the live aggregator so the toggle takes effect immediately.
func (a *App) SetDesktopMetrics(enabled bool) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopMetrics(enabled) }); err != nil {
		return err
	}
	switch {
	case enabled && a.metrics.Load() == nil && version != "dev":
		a.metrics.Store(newMetricsAggregator(config.MemoryUserDir()))
		if cfg, err := config.Load(); err == nil {
			a.recordSettingsMetricsSnapshot(cfg)
		}
	case !enabled:
		a.metrics.Store(nil)
	}
	return nil
}

// SetDesktopConversationWidth sets the max transcript width preference.
// standard = 960px fixed; full = 90% of the parent, with a 960px floor. Pure config-only.
func (a *App) SetDesktopConversationWidth(width string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopConversationWidth(width) })
}

// MigrateDesktopPreferences imports old browser-local desktop preferences into
// the user config once. Existing [desktop] values win so stale localStorage never
// overwrites an explicit config edit.
func (a *App) MigrateDesktopPreferences(language, theme, style string) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		if strings.TrimSpace(c.Desktop.Language) == "" {
			if err := c.SetDesktopLanguage(language); err != nil {
				return err
			}
		}
		if strings.TrimSpace(c.Desktop.Theme) == "" && strings.TrimSpace(c.Desktop.ThemeStyle) == "" {
			if err := c.SetDesktopAppearance(theme, style); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetEventsAutoRotation selects the task-333 event-log rotation gate mode
// (off | manual | auto). Unlike the restart-scoped switches above this takes
// effect on the very next save: the config write is followed by a push into
// the agent save path, so no restart is involved.
func (a *App) SetEventsAutoRotation(mode string) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetEventsAutoRotation(mode) }); err != nil {
		return err
	}
	a.pushEventsRotationSettings()
	return nil
}

// SetEventsRotation stores the task-333 auto-mode thresholds (factor 2-16,
// cap in MiB with 0 disabling it) and pushes them live like the mode setter.
func (a *App) SetEventsRotation(factor float64, capMB int64) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetEventsRotation(factor, capMB) }); err != nil {
		return err
	}
	a.pushEventsRotationSettings()
	return nil
}

// pushEventsRotationSettings forwards the normalized gate settings to the
// agent layer after a successful write (task 333). A config-load failure
// leaves the agent on its last pushed value and logs the miss: manual (the
// pre-push default) is today's rotating gate, so if the file says "off" and
// this push fails the gate keeps rotating — the WARN is what makes that
// divergence greppable instead of silent (review finding, 2026-09-28).
func (a *App) pushEventsRotationSettings() {
	cfg, err := config.Load()
	if err != nil {
		slog.Warn("desktop: events rotation settings push skipped (config load failed)", "err", err)
		return
	}
	agent.SetEventsAutoRotation(
		config.EventsAutoRotationMode(cfg),
		config.EventsRotationFactor(cfg),
		config.EventsRotationCapMB(cfg),
	)
}

// SetDagGraphCacheCapacity stores the tunable replayed-graph cache LRU
// capacity (task 196fix2) and pushes it into the agent save path immediately
// — no restart: the next save already judges with the new capacity. The
// config layer refuses out-of-range values (1..16), so the settings UI (task
// 347, cache-tuning page) and the file can never disagree about what is
// stored. A failed load after the write is logged rather than silently
// leaving the agent on the old capacity (same pattern as the 333 push).
func (a *App) SetDagGraphCacheCapacity(capacity int) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetDagGraphCacheCapacity(capacity) }); err != nil {
		return err
	}
	if cfg, err := config.Load(); err == nil {
		agent.SetSessionGraphCacheCapacity(config.DagGraphCacheCapacity(cfg))
	} else {
		slog.Warn("desktop: dag graph cache capacity push skipped (config load failed)", "err", err)
	}
	return nil
}

// SetDagGraphCacheMaxMB stores the task-499 total byte ceiling (MiB) and
// pushes it into the agent save path immediately — no restart. The config
// layer refuses out-of-range values (16..65536), same convention as the
// capacity setter above.
func (a *App) SetDagGraphCacheMaxMB(maxMB int64) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetDagGraphCacheMaxMB(maxMB) }); err != nil {
		return err
	}
	if cfg, err := config.Load(); err == nil {
		agent.SetSessionGraphCacheMaxBytes(config.DagGraphCacheMaxMB(cfg) << 20)
	} else {
		slog.Warn("desktop: dag graph cache byte cap push skipped (config load failed)", "err", err)
	}
	return nil
}

// SetDagGraphCacheEntryMaxMB stores the task-499 per-entry admission ceiling
// (MiB) and pushes it live; the agent reader clamps it under the total.
func (a *App) SetDagGraphCacheEntryMaxMB(maxMB int64) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetDagGraphCacheEntryMaxMB(maxMB) }); err != nil {
		return err
	}
	if cfg, err := config.Load(); err == nil {
		agent.SetSessionGraphCacheEntryMaxBytes(config.DagGraphCacheEntryMaxMB(cfg) << 20)
	} else {
		slog.Warn("desktop: dag graph cache entry cap push skipped (config load failed)", "err", err)
	}
	return nil
}
