package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/sentinel"
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
// Settings switch and the restart_update tool's set_target+execute path read the same
// preference (the host's swap refuses while it is off), so the error the tool returns
// stays true: what enables one enables the other.
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

// SetExperimentalToolOptimizations toggles the 工具优化 family (task 603).
// 任务 943：族拆两键后本 setter 只管 readBack 面。
func (a *App) SetExperimentalToolOptimizations(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalToolOptimizations(enabled) })
}

// SetExperimentalMultiEditInjection toggles the multi_edit injection face
// (task 943 split of the 工具优化 family): on, multi_edit joins the
// model-visible tools. Boot snapshot — restart to apply.
func (a *App) SetExperimentalMultiEditInjection(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalMultiEditInjection(enabled) })
}

// SetExperimentalCapabilityGroups toggles the capability-group disclosure
// layer (tasks 953/954/955, one shared switch): use_capability group rows +
// group cards, the session_hub L0 aggregation entry, and the standing
// directory's group attribution. Boot snapshot — restart (or a new session)
// to apply. Task 979 wires the settings-page entry that 953 deferred.
func (a *App) SetExperimentalCapabilityGroups(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCapabilityGroups(enabled) })
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
//
// 任务 856 起为 legacy setter：面板只调用 SetExperimentalCollabHub，本方法仅为
// 旧 wailsjs 绑定保留（decode 继承负责存量值迁移）。
func (a *App) SetExperimentalCollabGroupView(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCollabGroupView(enabled) })
}

// SetExperimentalCollabHub toggles the unified collaboration-hub entry (task
// 856 ③): with it off the utility row keeps the pre-856 layout (no hub icon).
func (a *App) SetExperimentalCollabHub(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalCollabHub(enabled) })
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

// SetExperimentalSidebarHoverLeft toggles the left-sidebar hover-to-expand
// with its 300ms mis-touch guard and 15s delayed re-collapse (task 826).
// Pure frontend gate: the settings save re-applies the boot snapshot, so the
// change is visible without a restart.
func (a *App) SetExperimentalSidebarHoverLeft(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSidebarHoverLeft(enabled) })
}

// SetExperimentalSidebarHoverRight toggles the right-dock hover-to-expand
// (task 826) — the left switch's counterpart, fully independent. Pure
// frontend gate, visible without a restart.
func (a *App) SetExperimentalSidebarHoverRight(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSidebarHoverRight(enabled) })
}

// SetExperimentalApprovalSelect toggles the composer permission-tier picker
// form (任务 940): off (default) keeps the segmented button group, on swaps it
// for a compact dropdown. Pure frontend gate: the settings save re-applies the
// boot snapshot, so the change is visible without a restart.
func (a *App) SetExperimentalApprovalSelect(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalApprovalSelect(enabled) })
}

// SetExperimentalTableWrap toggles transcript markdown-table re-flow (任务
// 959 ①): off (default) keeps the wide-table layout with horizontal scroll,
// on wraps cells to the current pane width. Pure frontend gate: the settings
// save re-applies the boot snapshot, so the change is visible without a
// restart.
func (a *App) SetExperimentalTableWrap(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalTableWrap(enabled) })
}

// SetExperimentalSendTargetSelect toggles the composer split-pane send-target
// picker form (任务 959 ②): off (default) keeps the three-button group, on
// swaps it for a compact dropdown (task-940 form mechanism reused). Pure
// frontend gate: the settings save re-applies the boot snapshot, so the
// change is visible without a restart.
func (a *App) SetExperimentalSendTargetSelect(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalSendTargetSelect(enabled) })
}

// SetExperimentalDraftRewrite toggles the composer 草稿优化 lab switch
// (任务 1024): off (default) renders no rewrite button and no layout change; on
// adds it to the composer toolbar, left of the send button. Pure frontend gate:
// the settings save re-applies the boot snapshot, no restart needed.
func (a *App) SetExperimentalDraftRewrite(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalDraftRewrite(enabled) })
}

// SetExperimentalStartupSplash toggles the DSH-style startup splash overlay
// (tl 1132): off (default) never renders the splash, on shows it until the
// core UI is mounted then fades it out. Pure frontend gate: the settings save
// re-applies the boot snapshot, no restart needed.
func (a *App) SetExperimentalStartupSplash(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalStartupSplash(enabled) })
}

// SetStandardContinuation (任务 1070) toggles the Standard-flow same-Run
// continuation total gate (lab switch standard_continuation). The config save
// is the durable half; the live half mirrors the flag into every running
// controller so the next run of an already-open session applies it without a
// restart. The agent side still requires its own boot snapshot (kill switch),
// so an off snapshot keeps the pre-1070 behaviour regardless of this flag.
func (a *App) SetStandardContinuation(enabled bool) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetStandardContinuation(enabled) }); err != nil {
		return err
	}
	a.mirrorStandardContinuationToControllers(enabled)
	return nil
}

// mirrorStandardContinuationToControllers pushes the task-1070 flag into every
// live controller (same snapshot-mirror shape as applyResponseLanguageToLiveControllers).
func (a *App) mirrorStandardContinuationToControllers(enabled bool) {
	a.mu.RLock()
	tabs := append([]*WorkspaceTab(nil), a.runtimeTabsLocked()...)
	a.mu.RUnlock()
	for _, tab := range tabs {
		if tab == nil {
			continue
		}
		if setter, ok := a.controllerForTab(tab).(interface{ SetStandardContinuation(bool) }); ok {
			setter.SetStandardContinuation(enabled)
		}
	}
}

// SetExperimentalWorkflowPanel toggles the workflow visualization panel
// (task 686): the command-palette entry opening the DAG flow view. Pure
// frontend gate: the settings save re-applies the boot snapshot, so the
// change is visible without a restart.
func (a *App) SetExperimentalWorkflowPanel(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalWorkflowPanel(enabled) })
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

// SetExperimentalACPListen toggles the resident ACP listener (task 790).
// Ships off; the listener arms at desktop boot, so a flip needs a restart —
// the lab card communicates that.
func (a *App) SetExperimentalACPListen(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalACPListen(enabled) })
}

// SetACPListenPort records the resident ACP listener's loopback port (task
// 790). Read once at desktop boot; 0 keeps the 8790 default.
func (a *App) SetACPListenPort(port int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetACPListenPort(port) })
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

// SetExperimentalBashHeavyGuard toggles the task-575 heavy-bash write guard
// (ships off; the guard reads the config at write time, no rebuild needed —
// task 815 adds the lab entry in the optimistic-parallel card).
func (a *App) SetExperimentalBashHeavyGuard(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetExperimentalBashHeavyGuard(enabled)
	})
}

// SetExperimentalEditEvidenceRelaxed toggles the task-789 relaxed edit
// evidence gate (ships off; restart to apply — the tool surface is composed
// per session; task 815 lands the settings-page entry for the unified
// registration window).
func (a *App) SetExperimentalEditEvidenceRelaxed(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error {
		return c.SetExperimentalEditEvidenceRelaxed(enabled)
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

// SetExperimentalHomePointer toggles the task-846 change-data-home lab card.
// Ships off (铁律 2); the wizard's write path re-checks the switch.
func (a *App) SetExperimentalHomePointer(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalHomePointer(enabled) })
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

// SetExperimentalDecisionPatterns toggles the task-801 decision-pattern
// extraction + solidification lab switch. Applies on restart (boot snapshot).
func (a *App) SetExperimentalDecisionPatterns(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetExperimentalDecisionPatterns(enabled) })
}

// SetStalledIntentNudge toggles the task-1069 stalled-intent nudge lab switch
// (模型提前结束判定器续轮提醒). Applies on restart (boot snapshot), like every
// boot-resolved switch.
func (a *App) SetStalledIntentNudge(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetStalledIntentNudge(enabled) })
}

// SetDecisionPatternFilePath overrides the solidified decision-patterns file
// location (task 801 修订七). Empty restores the default global-workspace/docs
// path. The heartbeat solidification, its backups, and the autopilot injection
// all read this config.
func (a *App) SetDecisionPatternFilePath(path string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDecisionPatternFilePath(path) })
}

// SetDecisionPatternThreshold sets the consolidation trigger threshold (task
// 801 修订六; 1..500, default 5 in config).
func (a *App) SetDecisionPatternThreshold(n int) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDecisionPatternThreshold(n) })
}

// SetDecisionPatternAutoApprove toggles the skip-approval option (task 801
// 修订六; default off = heartbeat reports and waits for the user).
func (a *App) SetDecisionPatternAutoApprove(enabled bool) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDecisionPatternAutoApprove(enabled) })
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

// SetSentinelPenetrationGuard 写任务 895 穿透封堵实验开关（[sentinel]
// penetration_guard，用户全局段）。落盘后同步拨运行期原子变量——判定路径
// 每次调用都读该开关，新旧工具调用即时跟随，无需重启。
func (a *App) SetSentinelPenetrationGuard(enabled bool) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetSentinelPenetrationGuard(enabled) }); err != nil {
		return err
	}
	sentinel.SetPenetrationGuard(enabled)
	return nil
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

// decisionPatternPathForView resolves the solidified decision-patterns file
// path for the settings views (task 801): an empty config value renders as the
// effective default global-workspace/docs path, so the panel never shows a
// blank location. An unresolvable user dir renders empty (solidification then
// reports itself unavailable).
func decisionPatternPathForView(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if p := strings.TrimSpace(cfg.Agent.DecisionPatternFilePath); p != "" {
		return p
	}
	return config.DefaultDecisionPatternFilePath()
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

// SetDesktopAlertLogRetentionDays sets the tl-1097 alert-log retention dial
// (0 = built-in default 7 days; -1 = forever; explicit values clamped into
// [1, 90]). After the config write it triggers an immediate prune so the next
// tick of the alert-log writer (or this call, for a shrink) already reflects
// the new policy.
func (a *App) SetDesktopAlertLogRetentionDays(days int) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopAlertLogRetentionDays(days) }); err != nil {
		return err
	}
	pruneAlertLogNow()
	return nil
}

// SetDesktopAlertLogMaxTotalMB sets the tl-1097 alert-log size ceiling
// (0 = built-in default 100MB; -1 = unlimited; explicit values clamped into
// [1, 10240]). Same immediate-prune behaviour as the retention setter.
func (a *App) SetDesktopAlertLogMaxTotalMB(mb int) error {
	if err := a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopAlertLogMaxTotalMB(mb) }); err != nil {
		return err
	}
	pruneAlertLogNow()
	return nil
}

// AlertLogUsage reports the current on-disk usage of the WARN/ERROR alert log
// family (desktop-alerts.log + rolled .N files) in bytes and file count, for
// the 「当前占用：X MB（N 个文件）」 display row.
func (a *App) AlertLogUsage() AlertLogUsageView {
	bytes, count := alertLogDirUsage(alertLogDirPath())
	return AlertLogUsageView{Bytes: bytes, Files: count, Dir: alertLogDirPath()}
}

// AlertLogUsageView is the JSON payload for AlertLogUsage.
type AlertLogUsageView struct {
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
	Dir   string `json:"dir"`
}

// OpenLogDir opens the logs directory (logs/desktop) in the system file
// manager, non-blocking: revealPath launches explorer via a detached command
// (see workspace_files_app.go defaultRevealPath — it never Waits on the child),
// so this returns immediately and never hangs the frontend bridge call.
func (a *App) OpenLogDir() error {
	dir := alertLogDirPath()
	if dir == "" {
		return fmt.Errorf("log directory is unavailable")
	}
	// Create the directory if it does not exist yet (fresh install) so the
	// file manager opens a real folder instead of erroring.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return a.RevealPath(dir)
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

// dreamCurationTaskTitle / dreamCurationTaskPrompt 预设 Dream 记忆整理任务的
// 固定内容（tl 1142：prompt 固化协调线 2026-10-24 七条记忆整理原则——旧版只覆盖
// 「写入」，缺整理原则，导致从预设新建的任务只会新增不会查重归并）。
const (
	dreamCurationTaskTitle  = "Dream 记忆整理"
	dreamCurationTaskPrompt = "你是记忆整理任务，每轮两段，全部完成后用简短要点汇报。\n" +
		"一、dream 偏好收集：调用 dream 工具扫描最近项目会话中的用户偏好，校验后写入项目 memory；" +
		"若工具不可用或实验未开启，说明原因后继续第二段。汇报新写入几条、跳过几条。\n" +
		"二、记忆整理（按七条原则处置近期记忆；动作用 memory:remember / memory:forget）：\n" +
		"① 查重优先：真重复组合并为一条最全版本，删其余；\n" +
		"② 过时条目更新不新建：被新事实取代的记忆在原条目上改；\n" +
		"③ 修正类必须回写原条目，或原条加「已被 XXX 修正」标注；\n" +
		"④ 战役状态类归并压缩：多版合并为一条最新状态 + 历史一句话；\n" +
		"⑤ 用户偏好 / feedback 类不删（长期资产）；\n" +
		"⑥ 判据 / 坑位类同族合并并互相加关联；\n" +
		"⑦ 每处理一组在汇报里列「合并组：A+B→C」。\n" +
		"安全边界：每轮处置 ≤15 条；删除前先读原文确认独立价值已被覆盖；一律走 memory 工具通道，禁止直接改 memory 文件；工具不可用则说明原因结束。"
)

// heartbeatApprovalFromGlobalDefault maps the global desktop default tool
// approval posture to a heartbeat task's approvalMode (tl 1142). ask stays ask;
// unknown/empty values normalize to ask (same fallback the settings page
// displays); "auto" — and only "auto" — is demoted to "yolo": an unattended
// heartbeat run has nobody present to answer auto's planning questions, so
// following "auto" verbatim would deadlock the run.
func heartbeatApprovalFromGlobalDefault(mode string) string {
	if config.NormalizeToolApprovalMode(mode) == "auto" {
		return "yolo"
	}
	return config.NormalizeToolApprovalMode(mode)
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
	title, prompt := dreamCurationTaskTitle, dreamCurationTaskPrompt
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
		// tl 1142: follow the global default, demoting "auto" to "yolo" —
		// same rule as the panel's manual-create path; the old hard-coded
		// "auto" risked deadlocking unattended runs on planning questions.
		ApprovalMode: heartbeatApprovalFromGlobalDefault(cfg.DesktopDefaultToolApprovalMode()),
		CreatedAt:    time.Now().UnixMilli(),
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

// CreateDecisionPatternHeartbeatTask ensures the task-801 consolidation
// heartbeat exists (修订六：开关联动「自动创建自动化任务」按钮). Returns
// created=true when a new task was appended; an existing task is re-enabled
// instead of duplicated. Unlike the dream curation task this one reuses one
// conversation across runs (ReuseSession, 修订六第 4 条) so历次检查/汇报/批准
// 在同一会话内连续承载.
func (a *App) CreateDecisionPatternHeartbeatTask() (created bool, err error) {
	cfg, _, lerr := a.loadDesktopUserConfigForView()
	if lerr != nil {
		return false, lerr
	}
	if cfg != nil && !cfg.Agent.ExperimentalDecisionPatterns {
		return false, errors.New("enable the decision-pattern experiment in Settings → Experimental first")
	}
	if a.heartbeat == nil {
		return false, errors.New("heartbeat engine is not running")
	}
	view := a.heartbeat.ReloadConfig()
	const title = "决策模式整合"
	const prompt = "决策模式整合检查（任务 801）：调用 decision_pattern_solidify(action=\"list\") 查看上次固化后的新增决策记忆。" +
		"未达阈值：简短说明「本轮无整合（x/阈值 y 条）」后直接结束，不要写任何文件。" +
		"达标：通读条目归纳稳定决策模式（去偶发、留共性），按「场景→决策→理由→来源」四段式在对话中完整汇报；" +
		"list 结果 autoApprove=false（默认）时等待用户在本对话中明确批准后，再调用 decision_pattern_solidify(action=\"write\", patterns=[...]) 固化；" +
		"autoApprove=true 时可直接写入。若此前你已汇报且用户已明确批准 → 直接执行写入。" +
		"工具不可用或实验未开启：说明原因后结束。"
	for i, task := range view.Tasks {
		if task.Title == title || strings.Contains(strings.ToLower(task.ID), "decision-pattern") {
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
		ID:            a.HeartbeatGenerateID(),
		Title:         title,
		Prompt:        prompt,
		Interval:      "24h",
		Enabled:       true,
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		// 修订六第 4 条：整合心跳固定复用同一对话——首个 run 创建并绑定
		// TopicID，此后每 run 在其中追加，历次检查/汇报/批准上下文连贯。
		ReuseSession: true,
		ApprovalMode: "auto",
		CreatedAt:    time.Now().UnixMilli(),
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
