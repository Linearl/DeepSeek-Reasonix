package main

import (
	"errors"
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

// SetExperimentalFullAccess toggles the full-access (yolo) lab switch
// (task 257). Boot resolves it into the writable-root set and the bash spec,
// so the flip applies on the next restart — the settings pane says so.
func (a *App) SetExperimentalFullAccess(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalFullAccess(enabled) })
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

// SetSessionCollabGates writes the task-173 collaboration panel gates in one
// call, so the panel cannot half-apply (settings → 实验特性 → 跨会话通信).
func (a *App) SetSessionCollabGates(allowDelete, allowRequireReply, allowReadTail, allowCreate, allowSteer bool, dailySendLimit int) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetSessionCollabGates(&allowDelete, &allowRequireReply, &allowReadTail, &allowCreate, &allowSteer, &dailySendLimit)
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

// SetExperimentalSplitView toggles the tab-bar split view (task 70-1): with it off the
// right-click menu keeps exactly the pre-split item list.
func (a *App) SetExperimentalSplitView(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSplitView(enabled) })
}

// SetExperimentalFeedback toggles the agent submit_feedback tool surface and the
// desktop feedback inbox panel (task 121).
func (a *App) SetExperimentalFeedback(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalFeedback(enabled) })
}

// SetExperimentalTodoSidebar toggles the right-dock todo tab with its tab
// visibility and wrap settings (task 259). The frontend snapshots the flag at
// boot, so the change shows up after a restart.
func (a *App) SetExperimentalTodoSidebar(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalTodoSidebar(enabled) })
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

// SetExperimentalCompactionParallel toggles parallel chunked compaction (265).
func (a *App) SetExperimentalCompactionParallel(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCompactionParallel(enabled) })
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

// SetSessionCollabHopLimit sets the cross-session chain ceiling (task 204); the config
// layer clamps it, so an out-of-range entry never reaches the file.
func (a *App) SetSessionCollabHopLimit(limit int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetSessionCollabHopLimit(limit) })
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
	if cfg != nil && !(cfg.Desktop.ExperimentalDream || cfg.Agent.ExperimentalDream) {
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
