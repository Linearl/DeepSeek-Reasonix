package config

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"reasonix/internal/billing"
	"reasonix/internal/provider"
)

type RenderScope string

const (
	RenderScopeFull    RenderScope = "full"
	RenderScopeUser    RenderScope = "user"
	RenderScopeProject RenderScope = "project"
)

// RenderTOML renders the config as annotated TOML in the `reasonix setup` house style:
// comments preserved, system_prompt as a multi-line string, helpful hints. The
// output round-trips back through Load (see render_test.go).
func RenderTOML(c *Config) string {
	return RenderTOMLForScope(c, RenderScopeFull)
}

// RenderTOMLForScope renders an annotated TOML file for a specific persistence
// target. User configs can carry desktop and account-level preferences; project
// reasonix.toml stays focused on project behavior and intentionally excludes
// desktop-only preferences.
func RenderTOMLForScope(c *Config, scope RenderScope) string {
	if c == nil {
		c = Default()
	}
	switch scope {
	case RenderScopeUser, RenderScopeProject:
	default:
		scope = RenderScopeFull
	}
	if scope == RenderScopeProject {
		c = projectScopedConfigForRender(c)
	}
	defaults := Default()
	var b strings.Builder

	b.WriteString("# Reasonix configuration.\n")
	fmt.Fprintf(&b, "# Resolution order: flag > ./reasonix.toml > %s > built-in defaults.\n", userConfigDisplayPath())
	b.WriteString("# Fields marked user/global only are not overridden by ./reasonix.toml.\n")
	b.WriteString("# Secrets are named via api_key_env and stored in Reasonix's global .env; never put keys here.\n\n")

	fmt.Fprintf(&b, "config_version = %d   # schema marker for diagnostics; old versions may ignore it\n", configVersion(c))
	fmt.Fprintf(&b, "default_model = %q\n", c.DefaultModel)
	// Conversation store mode (task 155). Rendered unconditionally: it lives on
	// the top-level Config rather than [desktop], and a hand-added line used to be
	// dropped by the next settings save.
	fmt.Fprintf(&b, "session_storage = %q   # v3_only (default) | dual_write_read_v3 | dual_write_read_v4 | v4_only (needs a restart)\n", SessionStorageMode(c))
	// Automatic event-log rotation gate + thresholds (task 333). Rendered
	// unconditionally for the same reason as session_storage above: top-level
	// scalars dropped from this table are silently lost on the next save.
	fmt.Fprintf(&b, "events_auto_rotation = %q   # off | manual (default) | auto — oversized event-log rotation gate\n", EventsAutoRotationMode(c))
	fmt.Fprintf(&b, "events_rotation_factor = %.1f   # auto only: rotate above this multiple of live content (2-16)\n", EventsRotationFactor(c))
	fmt.Fprintf(&b, "events_rotation_cap_mb = %d   # auto only: rotate above this many MiB (0 = off); thresholds OR together\n", EventsRotationCapMB(c))
	if c.Language != "" {
		fmt.Fprintf(&b, "language      = %q   # ui/model language; empty = auto-detect from $LANG / $REASONIX_LANG\n", c.Language)
	} else {
		b.WriteString("# language      = \"zh\"   # ui/model language; empty = auto-detect from $LANG / $REASONIX_LANG\n")
	}
	if scope != RenderScopeProject {
		fmt.Fprintf(&b, "credentials_store = %q   # legacy compatibility; provider keys are saved in Reasonix's global .env\n", normalizeCredentialsStore(c.CredentialsStore))
	}
	b.WriteString("\n")

	if shouldRenderUI(c, defaults, scope) {
		b.WriteString("[ui]\n")
		fmt.Fprintf(&b, "theme = %q   # auto|dark|light; CLI colors only; REASONIX_THEME can override per run\n", c.UITheme())
		if style := c.UIThemeStyle(); style != "" {
			fmt.Fprintf(&b, "theme_style = %q   # CLI accent palette; REASONIX_THEME_STYLE can override per run\n", style)
		} else {
			b.WriteString("# theme_style = \"graphite\"   # graphite|aurora|slate|carbon|nocturne|amber and legacy aliases\n")
		}
		if layout := c.UIShortcutLayout(); layout != "classic" {
			fmt.Fprintf(&b, "shortcut_layout = %q   # classic|desktop; compatibility setting; Shift+Tab toggles Plan, Ctrl+Y toggles YOLO\n", layout)
		} else {
			b.WriteString("# shortcut_layout = \"desktop\"   # classic|desktop; compatibility setting; Shift+Tab toggles Plan, Ctrl+Y toggles YOLO\n")
		}
		if strings.TrimSpace(c.UI.CursorShape) != "" {
			fmt.Fprintf(&b, "cursor_shape = %q   # block|underline|bar; text input cursor shape\n", c.UICursorShape())
		} else {
			b.WriteString("# cursor_shape = \"bar\"   # block|underline|bar; text input cursor shape\n")
		}
		if strings.TrimSpace(c.UI.CloseBehavior) != "" && scope == RenderScopeProject {
			fmt.Fprintf(&b, "close_behavior = %q   # legacy desktop close behavior; prefer [desktop].close_behavior in user config\n", c.DesktopCloseBehavior())
		}
		if c.UI.ShowReasoning {
			b.WriteString("show_reasoning = true   # CLI: show thinking text by default; false = collapsed (toggle with Ctrl+O)\n")
		} else {
			b.WriteString("# show_reasoning = true   # CLI: show thinking text by default; false = collapsed (toggle with Ctrl+O)\n")
		}
		fmt.Fprintf(&b, "show_turn_usage = %v   # CLI/TUI: show per-request token and cost receipts in the transcript\n", c.UI.ShowTurnUsage)
		b.WriteString("\n")
	}

	if scope != RenderScopeProject {
		b.WriteString("[desktop]\n")
		if lang := c.DesktopLanguage(); lang != "" {
			fmt.Fprintf(&b, "language = %q   # desktop UI language; empty/auto = browser/OS auto-detect\n", lang)
		} else {
			b.WriteString("# language = \"zh\"   # desktop UI language; empty/auto = browser/OS auto-detect\n")
		}
		// Legacy desktop.currency is still emitted when set so older binaries keep
		// reading the preference; new writers also own [billing].display_currency.
		if currency := c.DesktopCurrency(); currency != "" {
			fmt.Fprintf(&b, "currency = %q   # legacy display currency; prefer [billing].display_currency\n", currency)
		}
		fmt.Fprintf(&b, "layout_style = %q   # desktop layout: workbench|creation; legacy classic migrates to workbench\n", c.DesktopLayoutStyle())
		fmt.Fprintf(&b, "theme = %q   # desktop only: auto|dark|light\n", c.DesktopTheme())
		fmt.Fprintf(&b, "terminal_theme = %q   # integrated terminal: auto|dark|light; auto follows the desktop app\n", c.DesktopTerminalTheme())
		if style := c.DesktopThemeStyle(); style != "" {
			fmt.Fprintf(&b, "theme_style = %q   # desktop accent palette\n", style)
		} else {
			b.WriteString("# theme_style = \"graphite\"   # graphite|aurora|slate|carbon|nocturne|amber and legacy aliases\n")
		}
		if opener := c.DesktopExternalOpener(); opener != "" {
			fmt.Fprintf(&b, "external_opener = %q   # desktop Open control: installed application id\n", opener)
		} else {
			b.WriteString("# external_opener = \"vscode\"   # desktop Open control: installed application id\n")
		}
		fmt.Fprintf(&b, "close_behavior = %q   # desktop: quit|background when the window close button is clicked\n", c.DesktopCloseBehavior())
		fmt.Fprintf(&b, "status_bar_style = %q   # desktop: icon|text metric labels in the bottom status bar\n", c.DesktopStatusBarStyle())
		b.WriteString("status_bar_style_initialized = true   # icon default upgrade applied; preserve later user choices\n")
		fmt.Fprintf(&b, "status_bar_items = %s   # desktop: ordered visible bottom status bar items\n", renderStringArray(c.DesktopStatusBarItems()))
		fmt.Fprintf(&b, "default_tool_approval_mode = %q   # desktop: Ask/Auto/YOLO default for newly-created sessions\n", c.DesktopDefaultToolApprovalMode())
		// Autopilot is opt-in and only meaningful with a bound, so its keys are rendered
		// together. Writing the flag without the limit used to lose both: this renderer
		// writes a fixed set of keys, so an unlisted one was dropped and the settings
		// switch flipped straight back to off.
		if c.Desktop.Autopilot || strings.TrimSpace(c.Desktop.AutopilotMaxRuntime) != "" || strings.TrimSpace(c.Desktop.AutopilotApprovalGrace) != "" || c.Desktop.AutopilotGuardInterval != 0 || strings.TrimSpace(c.Desktop.AutopilotGuardQuiescent) != "" {
			fmt.Fprintf(&b, "autopilot = %v   # desktop: start new sessions unattended (requires autopilot_max_runtime)\n", c.Desktop.Autopilot)
			if runtime := strings.TrimSpace(c.Desktop.AutopilotMaxRuntime); runtime != "" {
				fmt.Fprintf(&b, "autopilot_max_runtime = %q   # desktop: wall-clock bound for an unattended run, e.g. 8h\n", runtime)
			}
			if grace := strings.TrimSpace(c.Desktop.AutopilotApprovalGrace); grace != "" {
				fmt.Fprintf(&b, "autopilot_approval_grace = %q   # desktop: wait for a human this long before the reviewer decides\n", grace)
			}
			// Task 326: both guard dials live in the same fixed-key block for the
			// same reason — an unlisted key is dropped on save and the switch the
			// user just set flips straight back to its default.
			if c.Desktop.AutopilotGuardInterval != 0 {
				fmt.Fprintf(&b, "autopilot_guard_interval = %d   # desktop: autopilot guard task interval in minutes; 0/absent = default (task 326)\n", c.Desktop.AutopilotGuardInterval)
			}
			if policy := strings.TrimSpace(c.Desktop.AutopilotGuardQuiescent); policy != "" {
				fmt.Fprintf(&b, "autopilot_guard_quiescent = %q   # desktop: guard self-close policy once the watched session goes quiet: disable | standby | destroy (task 326)\n", policy)
			}
		}
		fmt.Fprintf(&b, "check_updates = %v   # desktop: check for new versions on startup\n", c.DesktopCheckUpdates())
		// Experimental switches are rendered unconditionally, for the same reason the
		// autopilot block above is explicit: this renderer writes a fixed key set, so a
		// key it does not list is dropped on save and the settings switch flips straight
		// back to off. Both experiment switches shipped broken until 2026-09-15 (task
		// 81's restart-and-update and task 123's session monitor could never be enabled).
		fmt.Fprintf(&b, "experimental_restart_update = %v   # desktop: show the restart-and-update action (task 81)\n", c.Desktop.ExperimentalRestartUpdate)
		fmt.Fprintf(&b, "experimental_selection_actions = %v   # desktop: selection quick-actions (translate/explain floating card) (task 369)\n", c.Agent.ExperimentalSelectionActions)
		if dir := strings.TrimSpace(c.Desktop.StagingDir); dir != "" {
			fmt.Fprintf(&b, "staging_dir = %q   # desktop: fast-switch staging override (task 381); empty = the default staging directory\n", dir)
		}
		fmt.Fprintf(&b, "experimental_autonomous_update = %v   # desktop: register the agent restart_update tool (task 254; boot snapshot)\n", c.Desktop.ExperimentalAutonomousUpdate)
		fmt.Fprintf(&b, "experimental_lifecycle_noise_gate = %v   # desktop: task 377 - skip crash reports for clean-shutdown lifecycle residue (shutting_down/healthy)\n", c.Desktop.ExperimentalLifecycleNoiseGate)
		fmt.Fprintf(&b, "autonomous_update_resume = %q   # desktop: auto-resume after an update restart: off | goal_autopilot | all (task 254)\n", c.AutonomousUpdateResumeMode())
		fmt.Fprintf(&b, "autopilot_proxy_scope = %q   # desktop: autopilot proxy-approval scope: related (level 1) | all (level 2) (task 388)\n", c.AutopilotProxyScopeLevel())
		if p := strings.TrimSpace(c.Desktop.AutopilotProxyManifest); p != "" {
			fmt.Fprintf(&b, "autopilot_proxy_manifest = %q   # desktop: natural-language allow/deny manifest for the proxy reviewer (task 388)\n", p)
		}
		// Task 277: same fixed-key-set rule — a missing line would flip the
		// switch back to off on the next save (the 81/123 lesson).
		fmt.Fprintf(&b, "update_chime = %v   # desktop: play the ~3s update-complete chime on the first launch after a version swap (task 277)\n", c.Desktop.UpdateChime)
		fmt.Fprintf(&b, "experimental_session_monitor = %v   # desktop: left-rail session monitor board (task 123)\n", c.Desktop.ExperimentalSessionMonitor)
		fmt.Fprintf(&b, "experimental_split_view = %v   # desktop: tab-bar split view (task 70-1)\n", c.Desktop.ExperimentalSplitView)
		fmt.Fprintf(&b, "experimental_feedback = %v   # desktop: agent submit_feedback tool + feedback inbox panel (task 121)\n", c.Desktop.ExperimentalFeedback)
		// Task 172: fixed-key-set rule — an unlisted key would be dropped on
		// every save and the touchpoint switch would flip itself back off.
		fmt.Fprintf(&b, "experimental_feedback_nudge = %v   # desktop: feedback touchpoints — invite after a completed turn (T1) + steer note (T2); parent experimental_feedback wins (task 172)\n", c.Desktop.ExperimentalFeedbackNudge)
		fmt.Fprintf(&b, "experimental_parallel_full_access = %v   # desktop: trust managed worktree roots as write surfaces (task 127); env REASONIX_PARALLEL_FULL_ACCESS=1 also enables\n", c.Desktop.ExperimentalParallelFullAccess)
		fmt.Fprintf(&b, "experimental_todo_sidebar = %v   # desktop: right-dock todo tab + tab visibility/wrap settings (task 259; boot snapshot)\n", c.Desktop.ExperimentalTodoSidebar)
		// Task 495: fixed-key-set rule - an unlisted key would be dropped on every
		// save and the switch would flip itself back off (81/123 lesson).
		fmt.Fprintf(&b, "experimental_subagent_panel = %v   # desktop: right-dock subagent tab + default-collapsed ended subagent cards in deep mode (task 495; boot snapshot)\n", c.Desktop.ExperimentalSubagentPanel)
		// Task 261: fixed-key-set rule - an unlisted key would be dropped on every
		// save and the switch would flip itself back off (81/123 lesson).
		fmt.Fprintf(&b, "experimental_prompt_history_picker = %v   # desktop: composer clock-icon history picker + narrowed ArrowUp trigger (task 261; boot snapshot)\n", c.Desktop.ExperimentalPromptHistoryPicker)
		// Task 265 lab intake: render-surface features, nil-means-on pointers.
		fmt.Fprintf(&b, "experimental_question_search = %v   # desktop: topic-bar search-my-questions entry (task 265; boot snapshot)\n", c.DesktopQuestionSearchEnabled())
		fmt.Fprintf(&b, "experimental_subagent_tps = %v   # desktop: ~N tok/s readouts on sub-agent cards and the job table (task 265; boot snapshot)\n", c.DesktopSubagentTpsEnabled())
		fmt.Fprintf(&b, "experimental_completion_summary = %v   # desktop: per-turn result notice on the transcript (task 265; boot snapshot)\n", c.DesktopCompletionSummaryEnabled())
		fmt.Fprintf(&b, "experimental_quick_commands = %v   # desktop: gate the whole quick-commands surface (task 262; boot snapshot)\n", c.Desktop.ExperimentalQuickCommands)
		// Task 342: same fixed-key-set rule — an unlisted key is dropped on
		// every save and the lab switch would flip itself back off.
		fmt.Fprintf(&b, "experimental_cdp_debug_port = %v   # desktop: WebView2 CDP debug endpoint (task 342; loopback-only random port; boot snapshot, restart to apply)\n", c.Desktop.ExperimentalCDPDebugPort)
		// Task 385a: fixed-key-set rule — an unlisted key would be dropped on
		// every save and the lab switch would flip itself back off.
		fmt.Fprintf(&b, "experimental_output_style_ui = %v   # desktop: gate the lab 回答风格 (output style) section (task 385a; UI surface only)\n", c.Desktop.ExperimentalOutputStyleUI)
		// Task 439: fixed-key-set rule — an unlisted key would be dropped on
		// every save and the lab switch would flip itself back off.
		fmt.Fprintf(&b, "experimental_zcode_task_bus = %v   # desktop: built-in zcode task bus (task 439; embedded 127.0.0.1:8787 bus MCP at boot, [serve.bus_mcp] role table; restart to apply)\n", c.Desktop.ExperimentalZcodeTaskBus)
		fmt.Fprintf(&b, "experimental_path_rules = %v   # desktop: structured path-scope evaluation (docs/PATH_SCOPE_RULES.md, task 134)\n", c.Desktop.ExperimentalPathRules)
		fmt.Fprintf(&b, "experimental_local_server = %v   # desktop: expose Settings → Local server (task 130)\n", c.Desktop.ExperimentalLocalServer)
		fmt.Fprintf(&b, "max_cached_tabs = %d   # desktop: resident tab-state limit for the LRU prune (0 = unlimited, task 161)\n", c.Desktop.MaxCachedTabs)
		fmt.Fprintf(&b, "history_body_budget_mb = %d   # desktop: transcript body cache ceiling in MiB (task 161; 0 = default 192)\n", c.Desktop.HistoryBodyBudgetMb)
		fmt.Fprintf(&b, "markdown_budget_mb = %d   # desktop: markdown parse-cache ceiling in MiB (task 161; 0 = default 256)\n", c.Desktop.MarkdownBudgetMb)
		fmt.Fprintf(&b, "experimental_cache_tuning = %v   # desktop: expose Settings → 缓存大小调整 (task 161); off = user values ignored, defaults apply\n", c.Desktop.ExperimentalCacheTuning)
		fmt.Fprintf(&b, "experimental_trace_as_state = %v   # desktop: settings-view mirror of [agent] trace_as_state (task 60)\n", c.Desktop.ExperimentalTraceAsState)
		fmt.Fprintf(&b, "experimental_dream = %v   # desktop: settings-view mirror of [agent] experimental_dream (task 115)\n", c.Desktop.ExperimentalDream)
		fmt.Fprintf(&b, "experimental_session_collab = %v   # desktop: settings-view mirror of [agent] experimental_session_collab (task 19)\n", c.Desktop.ExperimentalSessionCollab)
		fmt.Fprintf(&b, "experimental_auto_load_older = %v   # desktop: settings-view mirror of [agent] experimental_auto_load_older (fork task 160)\n", c.Desktop.ExperimentalAutoLoadOlder)
		fmt.Fprintf(&b, "experimental_perf_monitor = %v   # desktop: settings-view mirror of [agent] experimental_perf_monitor (task 184)\n", c.Desktop.ExperimentalPerfMonitor)

		fmt.Fprintf(&b, "experimental_autonomous_idle_terminate = %v   # desktop: settings-view mirror of [agent] experimental_autonomous_idle_terminate (task 244 B1)\n", c.Desktop.ExperimentalAutonomousIdleTerminate)
		fmt.Fprintf(&b, "experimental_loop_streak_note = %v   # desktop: settings-view mirror of [agent] experimental_loop_streak_note (task 244 B2)\n", c.Desktop.ExperimentalLoopStreakNote)
		fmt.Fprintf(&b, "experimental_event_wait_recheck = %v   # desktop: settings-view mirror of [agent] experimental_event_wait_recheck (task 244 B3)\n", c.Desktop.ExperimentalEventWaitRecheck)

		fmt.Fprintf(&b, "experimental_orphan_handling = %v   # desktop: settings-view mirror of [agent] experimental_orphan_handling (task 449: merged 244 B5 lease reclaim + B4 recovery sweep)\n", c.Desktop.ExperimentalOrphanHandling)

		// Task 449: legacy keys are read-only (folded into
		// experimental_orphan_handling at load) but still rendered so an older
		// binary reading this file sees the migrated-off state instead of a
		// stale on.
		fmt.Fprintf(&b, "experimental_orphan_lease_reclaim = %v   # desktop: legacy key (task 244 B5), migrated into experimental_orphan_handling (task 449)\n", c.Desktop.ExperimentalOrphanLeaseReclaim)

		fmt.Fprintf(&b, "experimental_recovery_orphan_sweep = %v   # desktop: legacy key (task 244 B4), migrated into experimental_orphan_handling (task 449)\n", c.Desktop.ExperimentalRecoveryOrphanSweep)
		fmt.Fprintf(&b, "experimental_model_capability_filter = %v   # desktop: settings-view mirror of [agent] experimental_model_capability_filter (task 244 B9)\n", c.Desktop.ExperimentalModelCapabilityFilter)
		fmt.Fprintf(&b, "experimental_runtime_reuse = %v   # desktop: settings-view mirror of [agent] experimental_runtime_reuse (task 363A)\n", c.Desktop.ExperimentalRuntimeReuse)
		fmt.Fprintf(&b, "perf_monitor_interval_seconds = %d   # desktop: settings-view mirror of [agent] perf_monitor_interval_seconds (task 184; 0 = 5s)\n", c.Desktop.PerfMonitorIntervalSeconds)
		fmt.Fprintf(&b, "session_collab_hop_limit = %d   # desktop: settings-view mirror of [agent] session_collab_hop_limit (task 204)\n", c.Desktop.SessionCollabHopLimit)
		fmt.Fprintf(&b, "detached_idle_release_minutes = %d   # desktop: settings-view mirror of [agent] detached_idle_release_minutes (task 308-O4; 0 = never release)\n", c.Desktop.DetachedIdleReleaseMinutes)
		fmt.Fprintf(&b, "go_mem_limit_mb = %d   # desktop: settings-view mirror of [agent] go_mem_limit_mb (task 308-O3; 0 = unbounded)\n", c.Desktop.GoMemLimitMB)
		fmt.Fprintf(&b, "collab_inbox_merge = %q   # desktop: settings-view mirror of [agent] collab_inbox_merge (task 221; off | same_sender | all)\n", NormalizeCollabInboxMerge(c.Desktop.CollabInboxMerge))
		fmt.Fprintf(&b, "collab_guidance_merge = %v   # desktop: settings-view mirror of [agent] collab_guidance_merge (task 153)\n", c.Desktop.CollabGuidanceMerge)
		fmt.Fprintf(&b, "telemetry = %v   # desktop: anonymous launch ping + scrubbed next-launch native crash diagnostics; never content\n", c.DesktopTelemetry())
		fmt.Fprintf(&b, "metrics = %v   # desktop: aggregate quality/lifecycle metrics (anonymous signal/bucket counts); never content\n", c.DesktopMetrics())
		// A non-nil empty slice is intentional: provider_access = [] means the
		// user removed every desktop access entry. Omitting it would make the next
		// load treat the config as legacy and infer access again.
		if c.Desktop.ProviderAccess != nil {
			fmt.Fprintf(&b, "provider_access = %s   # desktop settings: providers shown on Settings > Model > Access\n", renderStringArray(c.Desktop.ProviderAccess))
		}
		renderDesktopSessionExperience(&b, c)
		renderDesktopReasoningDisplayMode(&b, c)
		fmt.Fprintf(&b, "display_mode = %q   # desktop: standard|compact transcript display mode\n", c.DesktopDisplayMode())
		if width := c.DesktopConversationWidth(); width == "full" {
			fmt.Fprintf(&b, "conversation_width = %q   # desktop: standard|full transcript width; empty = standard\n", width)
		}
		// This renderer writes a fixed set of keys, and an unlisted key is dropped on every save.
		// quick_commands was unlisted, so a snippet the user added lived in memory until the next
		// write and was then gone: the settings list read back empty, search found nothing, and
		// the composer's + menu (which renders its quick-command section only when the list is
		// non-empty) never showed it. Same failure the autopilot keys above are commented about.
		//
		// Written inline rather than as [[desktop.quick_commands]]: an array table switches the
		// TOML current table, so every key rendered after it would join the last entry.
		if len(c.Desktop.QuickCommands) > 0 {
			fmt.Fprintf(&b, "quick_commands = %s   # desktop: composer + menu snippets; title is the label, text is inserted verbatim\n",
				renderQuickCommandArray(c.Desktop.QuickCommands))
		}
		b.WriteString("\n")
		b.WriteString("[billing]\n")
		if pref := c.DisplayCurrencyPref(); pref != "" {
			fmt.Fprintf(&b, "display_currency = %q   # auto|CNY|USD; display only — does not rewrite provider list prices\n", pref)
		} else {
			b.WriteString("# display_currency = \"auto\"   # auto|CNY|USD; display only — does not rewrite provider list prices\n")
		}
		b.WriteString("\n")
	} else if c.Desktop.ProviderAccess != nil {
		// provider_access is intentionally mergeable across user and project
		// configs. It is the only desktop field written to reasonix.toml: local
		// providers then appear in that workspace's desktop model switcher without
		// copying user-global appearance or security preferences into the project.
		b.WriteString("[desktop]\n")
		fmt.Fprintf(&b, "provider_access = %s   # providers available to this workspace in the desktop model switcher\n\n", renderStringArray(c.Desktop.ProviderAccess))
	}

	if scope != RenderScopeProject {
		if c.CLITelemetryConfigured() {
			b.WriteString("[telemetry]\n")
			fmt.Fprintf(&b, "cli_metrics = %q   # CLI content-free usage metrics: auto|on|off; auto requires a local interactive terminal\n\n", c.CLITelemetryMode())
		}

		b.WriteString("[notifications]\n")
		fmt.Fprintf(&b, "enabled = %v   # system notifications for CLI and desktop turns; default off\n", c.Notifications.Enabled)
		fmt.Fprintf(&b, "turn_done = %v   # notify when a turn finishes\n", c.Notifications.TurnDone)
		fmt.Fprintf(&b, "approval_request = %v   # notify when a tool approval is waiting\n", c.Notifications.ApprovalRequest)
		fmt.Fprintf(&b, "ask_request = %v   # notify when a question is waiting\n", c.Notifications.AskRequest)
		b.WriteString("\n")
	}

	if shouldRenderNetwork(c, defaults, scope) {
		b.WriteString("[network]\n")
		fmt.Fprintf(&b, "proxy_mode = %q   # auto|env|custom|off; auto currently uses env proxy\n", c.NetworkProxyMode())
		if c.Network.ProxyURL != "" {
			fmt.Fprintf(&b, "proxy_url  = %q   # custom override, e.g. socks5://127.0.0.1:7890\n", c.Network.ProxyURL)
		} else {
			b.WriteString("# proxy_url  = \"socks5://127.0.0.1:7890\"   # optional custom override\n")
		}
		if c.Network.NoProxy != "" {
			fmt.Fprintf(&b, "no_proxy   = %q   # honored for proxy_mode = \"custom\"\n", c.Network.NoProxy)
		} else {
			b.WriteString("# no_proxy   = \"localhost,127.0.0.1,.local\"   # honored for proxy_mode = \"custom\"\n")
		}
		b.WriteString("\n[network.proxy]\n")
		proxyType := c.Network.Proxy.Type
		if proxyType == "" {
			proxyType = "socks5"
		}
		fmt.Fprintf(&b, "type = %q   # http|https|socks5|socks5h\n", proxyType)
		if c.Network.Proxy.Server != "" {
			fmt.Fprintf(&b, "server = %q\n", c.Network.Proxy.Server)
		} else {
			b.WriteString("# server = \"127.0.0.1\"\n")
		}
		if c.Network.Proxy.Port > 0 {
			fmt.Fprintf(&b, "port = %d\n", c.Network.Proxy.Port)
		} else {
			b.WriteString("# port = 7890\n")
		}
		if c.Network.Proxy.Username != "" {
			fmt.Fprintf(&b, "username = %q\n", c.Network.Proxy.Username)
		} else {
			b.WriteString("# username = \"\"\n")
		}
		if c.Network.Proxy.Password != "" {
			fmt.Fprintf(&b, "password = %q   # supports ${VAR} expansion\n", c.Network.Proxy.Password)
		} else {
			b.WriteString("# password = \"${REASONIX_PROXY_PASSWORD}\"   # optional; supports ${VAR} expansion\n")
		}
		b.WriteString("\n")
	}
	if shouldRenderEnvironment(c, defaults, scope) {
		renderEnvironmentConfig(&b, c.Environment)
	}

	b.WriteString("[agent]\n")
	if shouldRenderSystemPrompt(c, defaults, scope) {
		b.WriteString("system_prompt = \"\"\"\n")
		b.WriteString(c.Agent.SystemPrompt)
		b.WriteString("\"\"\"\n")
	} else {
		b.WriteString("# system_prompt = \"\"\"...\"\"\"   # omit to use the built-in prompt for this version\n")
	}
	if c.Agent.SystemPromptFile != "" {
		fmt.Fprintf(&b, "system_prompt_file = %q\n", c.Agent.SystemPromptFile)
	} else {
		b.WriteString("# system_prompt_file = \"prompts/system.md\"   # project paths stay in <workspace>; user paths may fall back to <reasonix home>\n")
	}
	fmt.Fprintf(&b, "temperature       = %s\n", formatFloat(c.Agent.Temperature))
	renderRecoveryAndCompletionValidation(&b, c)
	if lang := c.ReasoningLanguage(); lang != "auto" {
		fmt.Fprintf(&b, "reasoning_language = %q   # visible reasoning language: auto|zh|en\n", lang)
	} else {
		b.WriteString("# reasoning_language = \"zh\"   # visible reasoning language: auto|zh|en\n")
	}
	fmt.Fprintf(&b, "compact_ratio       = %s   # sole auto trigger; presets 0.70/0.80/0.85 (default 0.80)\n", formatFloat(c.Agent.CompactRatio))
	if c.Agent.Keep != nil {
		fmt.Fprintf(&b, "keep                = %s   # deprecated compatibility field; ignored at runtime\n", renderStringArray(c.Agent.Keep))
	} else {
		b.WriteString("# keep                = [\"errors\"]   # deprecated compatibility field; ignored at runtime\n")
	}
	if c.Agent.RecentKeep > 0 {
		fmt.Fprintf(&b, "recent_keep         = %d   # deprecated compatibility field; ignored at runtime\n", c.Agent.RecentKeep)
	} else {
		b.WriteString("# recent_keep         = 2   # deprecated compatibility field; ignored at runtime\n")
	}
	renderAgentSafetyControls(&b, c, scope)
	renderAgentModelAssignments(&b, c)
	if c.Agent.SubagentEffort != "" {
		fmt.Fprintf(&b, "subagent_effort = %q   # default effort for subagent entry points\n", c.Agent.SubagentEffort)
	} else {
		b.WriteString("# subagent_effort = \"high\"   # optional default effort for subagents\n")
	}
	if len(c.Agent.SubagentEfforts) > 0 {
		fmt.Fprintf(&b, "subagent_efforts = %s   # per-tool/skill effort overrides\n", renderStringMap(c.Agent.SubagentEfforts))
	} else {
		b.WriteString("# subagent_efforts = { review = \"max\", task = \"high\" }   # per-tool/skill effort overrides\n")
	}
	if c.Agent.MaxSubagentDepth != defaults.Agent.MaxSubagentDepth {
		fmt.Fprintf(&b, "max_subagent_depth = %d   # nested subagent delegation depth; 1 restores the old single-layer boundary\n", c.Agent.MaxSubagentDepth)
	} else {
		b.WriteString("# max_subagent_depth = 2   # nested subagent delegation depth; set 1 to disable nested delegation\n")
	}
	// Task 265 audit-3 M1: render the STORED tier (normalized, no switch
	// overlay). Rendering through DefaultSubagentPolicy would drop the saved
	// value whenever the lab intake switch is off — the same "saving a switch
	// deletes the key" failure as 2026-09-15. The switch still forces light at
	// the consumer (DefaultSubagentPolicy), not here.
	policy := c.normalizedStoredSubagentPolicy()
	if policy != "light" {
		fmt.Fprintf(&b, "subagent_policy = %q   # default sub-agent delegation tier for new sessions: light|balanced|aggressive\n", policy)
	} else {
		b.WriteString("# subagent_policy = \"light\"   # default sub-agent delegation tier for new sessions: light|balanced|aggressive\n")
	}
	if c.Agent.MaxSubagentConcurrency != defaults.Agent.MaxSubagentConcurrency {
		fmt.Fprintf(&b, "max_subagent_concurrency = %d   # session-wide sub-agent concurrency (task/fleet/skills)\n", c.Agent.MaxSubagentConcurrency)
	} else {
		b.WriteString("# max_subagent_concurrency = 6   # session-wide sub-agent concurrency (task/fleet/skills)\n")
	}
	if c.Agent.MaxParallelWriters != defaults.Agent.MaxParallelWriters {
		fmt.Fprintf(&b, "max_parallel_writers = %d   # concurrent writers with non-overlapping write_paths\n", c.Agent.MaxParallelWriters)
	} else {
		b.WriteString("# max_parallel_writers = 3   # concurrent writers with non-overlapping write_paths\n")
	}
	if tier := strings.TrimSpace(c.Agent.ApprovalTier); tier != "" {
		fmt.Fprintf(&b, "approval_tier = %q   # unattended approval decision-maker: guardian|parent|human\n", tier)
	} else {
		b.WriteString("# approval_tier = \"guardian\"   # unattended approval decision-maker: guardian|parent|human\n")
	}
	// Always render so turning the experiment off is recorded (same lesson as
	// the desktop experimental_* switches: omit-on-off springs back to true).
	fmt.Fprintf(&b, "experimental_dream = %v   # task 115: enable dream/distill memory-curation tools\n", c.Agent.ExperimentalDream)
	fmt.Fprintf(&b, "experimental_session_collab = %v   # task 19: multi-session collaboration tools (141-145)\n", c.Agent.ExperimentalSessionCollab)
	// Task 265 lab intake: existing behaviour given an off switch, so the
	// pointers render through their nil-means-on helpers (an absent key and an
	// explicit true render identically, and turning one off stays recorded).
	fmt.Fprintf(&b, "experimental_compaction_parallel = %v   # task 265: parallel chunked-compaction fragments (off = upstream serial baseline)\n", c.CompactionParallelEnabled())
	fmt.Fprintf(&b, "experimental_context_budget = %v   # task 265: per-turn context-state line (#9520; compress tool not gated here)\n", c.ContextBudgetEnabled())
	fmt.Fprintf(&b, "experimental_research_budget = %v   # task 265: read-only soft-budget extension via extend_research_budget (#10054)\n", c.ResearchBudgetEnabled())
	fmt.Fprintf(&b, "experimental_subagent_policy = %v   # task 265: delegation-tier entry points (off forces new sessions to light)\n", c.SubagentPolicyIntakeEnabled())
	fmt.Fprintf(&b, "experimental_full_access = %v   # task 257: full access (yolo) — all declared write dirs pass preflight, bash runs unwrapped (restart to apply)\n", c.Agent.ExperimentalFullAccess)
	// Task 231: the master switch and its four independent checkboxes all render
	// explicitly — omit-on-off would silently spring a saved checkmark back off
	// on the next render (the 81/123 lost-save lesson).
	fmt.Fprintf(&b, "experimental_preapprove_managed = %v   # task 231: master switch — under autopilot the checked classes below skip their approval prompt (restart to apply)\n", c.Agent.ExperimentalPreapproveManagedPaths)
	fmt.Fprintf(&b, "preapprove_skills = %v   # task 231: checkbox — skill-directory writes under autopilot skip approval\n", c.Agent.PreapproveManagedSkills)
	fmt.Fprintf(&b, "preapprove_hooks = %v   # task 231: checkbox — settings.json writes under autopilot (HIGHEST RISK: prompt injection can rewrite hooks)\n", c.Agent.PreapproveManagedHooks)
	fmt.Fprintf(&b, "preapprove_session_stores = %v   # task 231: checkbox — session-store writes under autopilot skip approval\n", c.Agent.PreapproveManagedStores)
	fmt.Fprintf(&b, "preapprove_bash_escape = %v   # task 231: checkbox — bash sandbox-escape approvals under autopilot\n", c.Agent.PreapproveManagedBashEscape)
	// Task 192: renders explicitly — omit-on-off would silently disable a
	// saved-on residency policy on the next render (81/123 lost-save lesson).
	fmt.Fprintf(&b, "experimental_active_tab_resident = %v   # task 192: keep the active/running tab resident across switches (zero-reload), exemption capped at 2, overruns logged (restart to apply)\n", c.Agent.ExperimentalActiveTabResident)
	// S1 底座常驻子进程（设计 2026-09-30 §7 R4）：explicit render — omit-on-off
	// would silently spring a saved-on switch back off on the next render
	// (81/123 lost-save lesson).
	fmt.Fprintf(&b, "experimental_base_process = %v   # S1: resident base subprocess (off = pure inline, pre-S1 behaviour; on = remote preferred with inline fallback; consumer lands with S1b)\n", c.Agent.ExperimentalBaseProcess)
	// Task 196fix2: explicit render — 0 keeps the built-in 3; an omitted line
	// would let the next render drop a tuned value (81/123 lost-save lesson).
	fmt.Fprintf(&b, "dag_graph_cache_capacity = %d   # task 196: replayed-graph cache LRU capacity across saves (0 = built-in 3, range 1-16)\n", c.Agent.DagGraphCacheCapacity)
	// Task 163: explicit render — omit-on-off would silently disable a
	// saved-on usage card on the next render (81/123 lost-save lesson).
	fmt.Fprintf(&b, "experimental_opencode_go_usage = %v   # task 163: OpenCode Go subscription usage card (5h/7d/month windows; no query while off)\n", c.Agent.ExperimentalOpenCodeGoUsage)
	fmt.Fprintf(&b, "experimental_auto_load_older = %v   # fork task 160: load older history by scrolling up at the transcript top\n", c.Agent.ExperimentalAutoLoadOlder)
	fmt.Fprintf(&b, "experimental_perf_monitor = %v   # task 184: host performance monitor (5s samples of memory/IO/key files)\n", c.Agent.ExperimentalPerfMonitor)

	fmt.Fprintf(&b, "experimental_autonomous_idle_terminate = %v   # task 244 B1: heartbeat self-disables after 3 consecutive runs with no conversation history\n", c.Agent.ExperimentalAutonomousIdleTerminate)
	fmt.Fprintf(&b, "experimental_loop_streak_note = %v   # task 244 B2: bounded neutral Continue. note instead of an immediate second text-repeat pause\n", c.Agent.ExperimentalLoopStreakNote)
	fmt.Fprintf(&b, "experimental_event_wait_recheck = %v   # task 244 B3: re-evaluate the event_wait checker before returning (recheckSatisfied field)\n", c.Agent.ExperimentalEventWaitRecheck)

	fmt.Fprintf(&b, "experimental_orphan_handling = %v   # task 449: merged orphan switch — B5 reclaim a session lease whose recorded owner process is dead (live foreign owners still respected) + B4 settle recovery-store operations past the covered durable sequence at open\n", c.Agent.ExperimentalOrphanHandling)

	// Task 449: legacy keys stay rendered (post-migration they read false) so
	// the render face keeps the pinned task-244 round-trip and an older binary
	// never sees a stale on.
	fmt.Fprintf(&b, "experimental_orphan_lease_reclaim = %v   # task 244 B5: legacy key, migrated into experimental_orphan_handling (task 449)\n", c.Agent.ExperimentalOrphanLeaseReclaim)

	fmt.Fprintf(&b, "experimental_recovery_orphan_sweep = %v   # task 244 B4: legacy key, migrated into experimental_orphan_handling (task 449)\n", c.Agent.ExperimentalRecoveryOrphanSweep)
	fmt.Fprintf(&b, "experimental_model_capability_filter = %v   # task 244 B9: reject a per-task model that lacks a capability the task needs (explained rejection instead of silent degradation)\n", c.Agent.ExperimentalModelCapabilityFilter)
	// Task 363A: runtime assembly reuse pool. Unconditional render —
	// omit-on-default would let a hand-added line vanish on the next save.
	fmt.Fprintf(&b, "experimental_runtime_reuse = %v   # task 363A: reuse the runtime assembly (prompt/skills/commands/hooks/registry) across tabs with the same root+model+effort instead of full rebuild\n", c.Agent.ExperimentalRuntimeReuse)
	fmt.Fprintf(&b, "perf_monitor_interval_seconds = %d   # task 184: sampler interval in seconds (default 5, clamped 1..300)\n", c.Agent.PerfMonitorIntervalSeconds)
	// Unconditional render — omit-on-default would let a hand-added line vanish
	// on the next save (same rule as its sampler sibling above).
	fmt.Fprintf(&b, "perf_monitor_heap_interval_seconds = %d   # perf monitor heap-profile dump interval in seconds (0 = built-in default 60; explicit values clamped 10..3600)\n", c.Agent.PerfMonitorHeapIntervalSeconds)
	fmt.Fprintf(&b, "session_collab_hop_limit = %d   # task 204: cross-session chain ceiling (default 5, clamped 3..1000; 0 = default)\n", c.Agent.SessionCollabHopLimit)
	// Task 308-O4: detached idle runtime release threshold. Unconditional
	// render — omit-on-default would let a hand-added line vanish on the next save.
	fmt.Fprintf(&b, "detached_idle_release_minutes = %d   # task 308-O4: release a detached session's runtime after this many idle minutes (0 = never, clamped 0..10080; env REASONIX_DETACHED_IDLE_RELEASE_MINUTES overrides)\n", c.Agent.DetachedIdleReleaseMinutes)
	// Task 308-O3: soft memory limit (debug.SetMemoryLimit). Unconditional
	// render — omit-on-default would let a hand-added line vanish on the next save.
	fmt.Fprintf(&b, "go_mem_limit_mb = %d   # task 308-O3: soft memory limit in MB via debug.SetMemoryLimit (0 = unbounded, clamped 0..65536)\n", c.Agent.GoMemLimitMB)
	// Task 309: mailbox defaults for talk_to_session. Unconditional render —
	// omit-on-default would let a hand-added line vanish on the next save.
	fmt.Fprintf(&b, "session_collab_mail_idempotent_default = %v   # task 309: same-content redeliveries dedup onto the original mail (default on)\n", c.Agent.SessionCollabMailIdempotentDefault)
	fmt.Fprintf(&b, "session_collab_mail_receipt_default = %v   # task 309: ask for a read receipt on every send unless the call overrides (default off)\n", c.Agent.SessionCollabMailReceiptDefault)
	fmt.Fprintf(&b, "session_collab_default_delivery = %q   # task 309: channel used when a call omits delivery (steer | followup; default steer)\n", c.Agent.SessionCollabDefaultDelivery)
	// Task 173: the collaboration panel gates. All default off, so the rendered
	// block states them explicitly (omit-on-off would spring them back to off).
	fmt.Fprintf(&b, "session_collab_allow_delete = %v   # task 173: expose delete_session (settings → 实验特性 → 跨会话通信)\n", c.Agent.SessionCollabAllowDelete)
	fmt.Fprintf(&b, "session_collab_allow_require_reply = %v   # task 173: allow require_reply on talk_to_session\n", c.Agent.SessionCollabAllowRequireReply)
	fmt.Fprintf(&b, "session_collab_allow_read_tail = %v   # task 173: expose read_session_tail\n", c.Agent.SessionCollabAllowReadTail)
	fmt.Fprintf(&b, "session_collab_allow_create = %v   # task 173: expose create_collab_session\n", c.Agent.SessionCollabAllowCreate)
	fmt.Fprintf(&b, "session_collab_allow_steer = %v   # task 173: allow delivery=steer on talk_to_session\n", c.Agent.SessionCollabAllowSteer)
	fmt.Fprintf(&b, "session_collab_background = %v   # task 264: background-woken sessions stay out of the tab bar (detached stand-up; delivery unchanged)\n", c.Agent.SessionCollabBackground)
	fmt.Fprintf(&b, "session_collab_daily_send_limit = %d   # task 173: per-session daily outgoing cap (0 = no cap)\n", c.Agent.SessionCollabDailySendLimit)
	fmt.Fprintf(&b, "experimental_cascade_approval = %v   # task 225: forward a dispatched session's approvals to its task source (off by default)\n", c.Agent.ExperimentalCascadeApproval)
	fmt.Fprintf(&b, "experimental_fallback_model = %v   # task 242: switch to fallback_model after quota-class exhaustion (off by default)\n", c.Agent.ExperimentalFallbackModel)
	fmt.Fprintf(&b, "fallback_model = %q   # task 242: provider/model pair used when the primary is quota-exhausted (empty = keep primary)\n", c.Agent.FallbackModel)
	fmt.Fprintf(&b, "experimental_high_speed_model = %v   # task 318.1: allow the high-speed model lane for highSpeedModels (off by default)\n", c.Agent.ExperimentalHighSpeedModel)
	fmt.Fprintf(&b, "experimental_proactive_compact = %v   # task 318.2: use proactive_compact_cooldown_minutes instead of the hard-coded 10min fold cooldown (off by default)\n", c.Agent.ExperimentalProactiveCompact)
	fmt.Fprintf(&b, "proactive_compact_cooldown_minutes = %d   # task 318.2: model-driven fold cooldown in minutes (default 10; used only when the switch above is on)\n", c.Agent.ProactiveCompactCooldownMinutes)
	fmt.Fprintf(&b, "experimental_cold_cache_compact = %v   # task 297: compact a long conversation once before it goes cold (off by default)\n", c.Agent.ExperimentalColdCacheCompact)
	fmt.Fprintf(&b, "cold_cache_compact_min_bytes = %d   # task 297: visible-context size floor in bytes (0 = built-in 614400 = 600 KiB)\n", c.Agent.ColdCacheCompactMinBytes)
	fmt.Fprintf(&b, "cold_cache_compact_idle_minutes = %d   # task 297: idle minutes before the cold-cache pass fires (0 = built-in 300 = 5h)\n", c.Agent.ColdCacheCompactIdleMinutes)
	fmt.Fprintf(&b, "experimental_composer_draft = %v   # task 318.3: persist composer drafts across restarts (off by default)\n", c.Agent.ExperimentalComposerDraft)
	fmt.Fprintf(&b, "collab_inbox_merge = %q   # task 221: inbox drain merge mode (off | same_sender | all; default off)\n", NormalizeCollabInboxMerge(c.Agent.CollabInboxMerge))
	fmt.Fprintf(&b, "collab_guidance_merge = %v   # task 153: guidance shelf manual merge-next button\n", c.Agent.CollabGuidanceMerge)
	fmt.Fprintf(&b, "experimental_collab_background_delivery = %v   # task 224: collab delivery opens tabs inactive (woken conversation runs in background)\n", c.Agent.ExperimentalCollabBackgroundDelivery)
	fmt.Fprintf(&b, "perf_monitor_retention_hours = %d   # task 184: how long samples are kept (default 48h)\n", c.Agent.PerfMonitorRetentionHours)
	if len(c.Agent.PerfMonitorPaths) > 0 {
		quoted := make([]string, 0, len(c.Agent.PerfMonitorPaths))
		for _, pattern := range c.Agent.PerfMonitorPaths {
			quoted = append(quoted, fmt.Sprintf("%q", pattern))
		}
		fmt.Fprintf(&b, "perf_monitor_paths = [%s]   # task 184: glob patterns for the file-size table\n", strings.Join(quoted, ", "))
	}
	fmt.Fprintf(&b, "trace_as_state = %v   # task 60: Trace-as-State compaction (reasoning in summaries, re-read routing, guarded folds)\n", c.Agent.TraceAsState)
	fmt.Fprintf(&b, "stalled_intent_nudge = %v   # task 117: nudge when model announces next step instead of taking it\n", c.Agent.StalledIntentNudge)
	if c.Agent.StalledIntentNudgeLimit > 0 {
		fmt.Fprintf(&b, "stalled_intent_nudge_limit = %d   # max stalled-intent nudges per run (1-3; default 1)\n", c.Agent.StalledIntentNudgeLimit)
	}
	fmt.Fprintf(&b, "readiness_catch_up = %v   # task 117: let ordinary turns cite missing delivery evidence before pausing\n", c.Agent.ReadinessCatchUp)
	if c.Agent.ReadinessCatchUpLimit > 0 {
		fmt.Fprintf(&b, "readiness_catch_up_limit = %d   # max delivery catch-up rounds per run (1-2; default 1)\n", c.Agent.ReadinessCatchUpLimit)
	}
	fmt.Fprintf(&b, "plan_research_gate = %v   # task 118: require a read-only investigation (or a stated reason) before a plan lands\n", c.Agent.PlanResearchGate)
	if c.Agent.PlanResearchGateLimit > 0 {
		fmt.Fprintf(&b, "plan_research_gate_limit = %d   # max plan research-gate rounds per run (1-2; default 1)\n", c.Agent.PlanResearchGateLimit)
	}
	fmt.Fprintf(&b, "read_only_task_background = %v   # task 118: offer run_in_background on read_only_task\n", c.Agent.ReadOnlyTaskBackground)
	if c.Agent.SubagentDefaultSteps > 0 {
		fmt.Fprintf(&b, "subagent_default_steps = %d   # task 118: override default non-review sub-agent step budget (0 = formula)\n", c.Agent.SubagentDefaultSteps)
	}
	if c.Agent.OutputStyle != "" {
		fmt.Fprintf(&b, "output_style = %q   # persona/tone folded into the prompt\n", c.Agent.OutputStyle)
	} else {
		b.WriteString("# output_style = \"explanatory\"   # explanatory | learning | concise | custom; empty = default\n")
	}
	b.WriteString("\n")

	if shouldRenderProviders(c, defaults, scope) {
		for _, p := range c.Providers {
			b.WriteString("[[providers]]\n")
			fmt.Fprintf(&b, "name        = %q\n", p.Name)
			fmt.Fprintf(&b, "kind        = %q\n", p.Kind)
			fmt.Fprintf(&b, "base_url    = %q\n", p.BaseURL)
			if p.ChatURL != "" {
				fmt.Fprintf(&b, "chat_url    = %q   # legacy OpenAI chat endpoint override\n", p.ChatURL)
			}
			if p.RequestURL != "" {
				fmt.Fprintf(&b, "request_url = %q   # exact provider request URL; no path completion\n", p.RequestURL)
			}
			if len(p.Models) > 0 {
				fmt.Fprintf(&b, "models      = %s\n", renderStringArray(p.Models))
				if p.Default != "" {
					fmt.Fprintf(&b, "default     = %q\n", p.Default)
				}
			} else if p.Model != "" {
				fmt.Fprintf(&b, "model       = %q\n", p.Model)
			}
			if p.ModelsURL != "" {
				fmt.Fprintf(&b, "models_url  = %q   # auto-fetch models from this URL on startup\n", p.ModelsURL)
			}
			renderProviderIdentity(&b, p.APIKeyEnv, p.DisplayName)
			renderProviderHidden(&b, p.Hidden)
			if p.PresetID != "" {
				fmt.Fprintf(&b, "preset_id   = %q   # curated preset identity; settings UI uses it to avoid duplicate installs\n", p.PresetID)
			}
			if p.PresetVersion > 0 {
				fmt.Fprintf(&b, "preset_version = %d\n", p.PresetVersion)
			}
			if len(p.Headers) > 0 {
				fmt.Fprintf(&b, "headers     = %s   # extra static request headers; keep secrets in api_key_env\n", renderStringMap(p.Headers))
			}
			if len(p.ExtraBody) > 0 {
				fmt.Fprintf(&b, "extra_body  = %s   # extra top-level JSON request body fields for compatible gateways\n", renderAnyMap(p.ExtraBody))
			}
			if p.AuthHeader {
				b.WriteString("auth_header = true   # Anthropic-compatible: send Authorization: Bearer <api_key> instead of x-api-key\n")
			}
			if p.ResponsesMode != "" {
				fmt.Fprintf(&b, "responses_mode = %q   # responses provider: stateless|stateful\n", p.ResponsesMode)
			}
			if p.ResponsesStateful != nil {
				fmt.Fprintf(&b, "responses_stateful = %t   # legacy responses mode switch\n", *p.ResponsesStateful)
			}
			if p.BalanceURL != "" {
				fmt.Fprintf(&b, "balance_url = %q   # optional; wallet-balance endpoint shown in the status bar\n", p.BalanceURL)
			}
			if p.ContextWindow > 0 {
				fmt.Fprintf(&b, "context_window = %d   # tokens; compaction triggers near this limit\n", p.ContextWindow)
			}
			if p.MaxOutputTokens != 0 {
				fmt.Fprintf(&b, "max_output_tokens = %d   # per-turn total output; 0 = provider auto (official DeepSeek 384K, omit when safe); positive = cost cap; negative = force-omit; never affects compact_ratio\n", p.MaxOutputTokens)
			} else {
				b.WriteString("# max_output_tokens = 0       # recommended: official DeepSeek omits the field (server 384K ceiling)\n")
				b.WriteString("# max_output_tokens = 32768   # optional cost cap\n")
				b.WriteString("# max_output_tokens = 65536   # optional cost cap\n")
				b.WriteString("# max_output_tokens = 131072  # optional cost cap\n")
			}
			if p.Price != nil {
				fmt.Fprintf(&b, "price       = %s   # provider-wide fallback, per 1M tokens\n", renderPricingInline(p.Price))
			}
			if len(p.Prices) > 0 {
				fmt.Fprintf(&b, "prices      = %s   # per-model prices, per 1M tokens\n", renderPricingMap(p.Prices))
			}
			if cur := strings.TrimSpace(p.BillingCurrency); cur != "" {
				fmt.Fprintf(&b, "billing_currency = %q   # frozen list-price currency; independent of display_currency\n", billing.NormalizeCurrency(cur))
			}
			if mode := strings.TrimSpace(p.BillingMode); mode != "" && mode != "payg" {
				fmt.Fprintf(&b, "billing_mode = %q   # payg|subscription_equivalent\n", mode)
			}
			if p.Thinking != "" {
				fmt.Fprintf(&b, "thinking    = %q\n", p.Thinking)
			}
			if p.Effort != "" {
				fmt.Fprintf(&b, "effort      = %q\n", p.Effort)
			}
			if p.Vision {
				b.WriteString("vision      = true   # provider accepts image input for all listed models\n")
			}
			if p.VisionModels != nil {
				fmt.Fprintf(&b, "vision_models = %s   # models in this provider that accept image input\n", renderStringArray(p.VisionModels))
			}
			if p.VisionDetail != "" {
				fmt.Fprintf(&b, "vision_detail = %q   # openai image detail hint: low|high; empty = auto\n", p.VisionDetail)
			}
			if p.WebSearch != nil {
				fmt.Fprintf(&b, "web_search  = %t   # independent web_search tool; omitted defaults on for supported official DeepSeek APIs\n", *p.WebSearch)
			}
			if p.ReasoningProtocol != "" {
				fmt.Fprintf(&b, "reasoning_protocol = %q   # auto|deepseek|glm|kimi-k3|openai|none; overrides model/endpoint reasoning detection\n", p.ReasoningProtocol)
			}
			if len(p.SupportedEfforts) > 0 {
				fmt.Fprintf(&b, "supported_efforts = %s   # custom /effort levels exposed by this provider; overrides the built-in Kind/BaseURL default\n", renderStringArray(p.SupportedEfforts))
			}
			if p.DefaultEffort != "" {
				fmt.Fprintf(&b, "default_effort    = %q   # used when /effort is auto or unset; must be one of supported_efforts\n", p.DefaultEffort)
			}
			if len(p.ModelOverrides) > 0 {
				fmt.Fprintf(&b, "model_overrides   = %s   # per-model context/output/reasoning/vision overrides for mixed gateways\n", renderModelOverrides(p.ModelOverrides))
			}
			if p.NoProxy {
				b.WriteString("no_proxy    = true   # reach this base_url directly, never via the proxy\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("[tools]\n")
	if len(c.Tools.Enabled) == 0 {
		b.WriteString("enabled = []   # empty = all built-in tools\n")
	} else {
		b.WriteString("enabled = [")
		for i, t := range c.Tools.Enabled {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", t)
		}
		b.WriteString("]\n")
	}
	fmt.Fprintf(&b, "bash_timeout_seconds = %d   # foreground safety cap; set 0 for no tool-local cap\n", c.BashTimeoutSeconds())
	fmt.Fprintf(&b, "mcp_startup_timeout_seconds = %d   # background initialize + tools/list safety cap; per-plugin overrides may raise it\n", c.MCPStartupTimeoutSeconds())
	fmt.Fprintf(&b, "mcp_call_timeout_seconds = %d   # default MCP call safety cap; per-plugin/tool overrides may raise it\n\n", c.MCPCallTimeoutSeconds())

	b.WriteString("[tools.background_jobs]\n")
	fmt.Fprintf(&b, "stalled_warning_seconds = %d   # heads-up once per background job after this many quiet seconds; a quiet job is not necessarily stuck; 0 disables\n\n", c.BackgroundJobStalledWarningSeconds())

	b.WriteString("[tools.shell]\n")
	if c.Tools.Shell.Prefer != "" {
		fmt.Fprintf(&b, "prefer = %q   # auto|bash|powershell|pwsh; empty/default = auto-detect\n", c.Tools.Shell.Prefer)
	} else {
		b.WriteString("# prefer = \"auto\"   # auto|bash|powershell|pwsh; empty/default = auto-detect\n")
	}
	if c.Tools.Shell.Path != "" {
		fmt.Fprintf(&b, "path   = %q   # absolute path to the shell executable; empty = PATH lookup\n\n", c.Tools.Shell.Path)
	} else {
		b.WriteString("# path   = \"/opt/homebrew/bin/bash\"   # absolute path to the shell executable; empty = PATH lookup\n\n")
	}

	renderLSPConfig(&b, c.LSP)

	b.WriteString("[skills]\n")
	if len(c.Skills.Paths) > 0 {
		fmt.Fprintf(&b, "paths = %s   # extra custom skill roots\n", renderStringArray(c.Skills.Paths))
	} else {
		b.WriteString("# paths = [\"~/my-skills\", \"../shared/skills\"]   # extra custom skill roots\n")
	}
	if len(c.Skills.ExcludedPaths) > 0 {
		fmt.Fprintf(&b, "excluded_paths = %s   # skill roots hidden from discovery\n", renderStringArray(c.Skills.ExcludedPaths))
	} else {
		b.WriteString("# excluded_paths = [\"~/.agents/skills\"]   # hide convention roots without deleting folders\n")
	}
	if c.Skills.DisableImplicitInvocation {
		b.WriteString("disable_implicit_invocation = true   # keep /skill explicit; hide skill discovery and tools from the model\n")
	} else {
		b.WriteString("# disable_implicit_invocation = false   # keep skills available for automatic model invocation\n")
	}
	if c.Skills.MaxDepth != 0 {
		fmt.Fprintf(&b, "max_depth = %d   # nested scan depth; default 3, set 1 for legacy root-only discovery\n", c.SkillMaxDepth())
	} else {
		b.WriteString("# max_depth = 3   # nested scan depth; set 1 for legacy root-only discovery\n")
	}
	if disabled := c.DisabledSkillNames(); len(disabled) > 0 {
		fmt.Fprintf(&b, "disabled_skills = %s   # hidden from the prompt, slash invocation, and skill tools\n\n", renderStringArray(disabled))
	} else {
		b.WriteString("# disabled_skills = [\"review\"]   # hide noisy or unwanted skills\n\n")
	}

	b.WriteString("[permissions]\n")
	b.WriteString("# Per-call gating. mode = writer fallback when no rule matches: ask|allow|deny.\n")
	b.WriteString("# Readers always default to allow. Precedence: deny > ask > allow > fallback.\n")
	b.WriteString("# Rules are \"Tool\" or \"Tool(specifier)\"; e.g. Bash(go test:*), Edit(src/**).\n")
	mode := c.Permissions.Mode
	if mode == "" {
		mode = "ask"
	}
	fmt.Fprintf(&b, "mode  = %q\n", mode)
	if c.Permissions.AllowDynamicBash {
		b.WriteString("allow_dynamic_bash = true   # advanced: let mode=allow cover command substitution and interpreter -c/-e\n")
	} else {
		b.WriteString("# allow_dynamic_bash = false   # advanced opt-in; deny/ask and exact rules still take precedence\n")
	}
	b.WriteString(renderRuleList("deny", c.Permissions.Deny, `["Bash(rm -rf*)", "Bash(git push*)"]   # hard-blocked in every mode`))
	b.WriteString(renderRuleList("allow", c.Permissions.Allow, `["Bash(go test:*)", "Bash(git status:*)"]   # never prompted`))
	b.WriteString(renderRuleList("ask", c.Permissions.Ask, `["Edit(src/**)"]   # force a prompt even if otherwise allowed`))
	b.WriteString("\n")

	b.WriteString("[sandbox]\n")
	b.WriteString("# Confine tool blast radius. File-writers (write_file/edit_file/multi_edit/move_file)\n")
	b.WriteString("# may only write under workspace_root (empty = current dir) and allow_write extras.\n")
	b.WriteString("# bash = \"enforce\" jails each command in an OS sandbox when available;\n")
	b.WriteString("# without one, bash execution is refused. Empty defaults to enforce on macOS/Linux.\n")
	b.WriteString("# Windows has no OS-level Bash sandbox and fixes bash = \"off\".\n")
	b.WriteString("# network allows sandboxed bash egress.\n")
	if c.Sandbox.WorkspaceRoot != "" {
		fmt.Fprintf(&b, "workspace_root = %q\n", c.Sandbox.WorkspaceRoot)
	} else {
		b.WriteString("# workspace_root = \"\"            # default: current working directory\n")
	}
	if len(c.Sandbox.AllowWrite) > 0 {
		fmt.Fprintf(&b, "allow_write = %s\n", renderStringArray(c.Sandbox.AllowWrite))
	} else {
		b.WriteString("# allow_write = [\"/tmp\"]          # extra dirs writers may also modify\n")
	}
	if len(c.Sandbox.AllowGlobal) > 0 {
		fmt.Fprintf(&b, "allow_global = %s\n", renderStringArray(c.Sandbox.AllowGlobal))
	} else {
		b.WriteString("# allow_global = []              # user-global common dirs: every project may write\n")
		b.WriteString("#                                  # these and anything under them WITHOUT approval.\n")
		b.WriteString("#                                  # e.g. allow_global = [\"C:\\\\tmp\"] stops repeated\n")
		b.WriteString("#                                  # prompts for new subdirectories under it.\n")
	}
	if len(c.Sandbox.ForbidRead) > 0 {
		fmt.Fprintf(&b, "forbid_read = %s\n", renderStringArray(c.Sandbox.ForbidRead))
	} else {
		b.WriteString("# forbid_read = []                  # dirs the agent cannot read or list\n")
	}
	fmt.Fprintf(&b, "bash    = %q\n", c.BashMode())
	fmt.Fprintf(&b, "network = %v\n", c.Sandbox.Network)
	if c.Sandbox.OptimisticWrite {
		fmt.Fprintf(&b, "optimistic_write = %v\n", c.Sandbox.OptimisticWrite)
	} else {
		b.WriteString("# optimistic_write = false    # enable write-if-unchanged parallel writes (#9213)\n")
	}
	b.WriteString("\n")

	b.WriteString("[statusline]\n")
	b.WriteString("# A custom status line: a command whose first stdout line replaces the built-in\n")
	b.WriteString("# data row. It receives {\"model\",\"contextUsed\",\"contextWindow\",\"cwd\"} as JSON on stdin.\n")
	if c.Statusline.Command != "" {
		fmt.Fprintf(&b, "command = %q\n", c.Statusline.Command)
	} else {
		b.WriteString("# command = \"my-statusline.sh\"\n")
	}
	b.WriteString("\n")

	if shouldRenderBot(c, defaults, scope) {
		b.WriteString("# Bot gateway: multi-channel IM bot for QQ, Feishu/Lark, and WeChat.\n")
		b.WriteString("[bot]\n")
		fmt.Fprintf(&b, "enabled = %v\n", c.Bot.Enabled)
		if c.Bot.Model != "" {
			fmt.Fprintf(&b, "model = %q\n", c.Bot.Model)
		} else {
			b.WriteString("# model = \"\"   # empty = default_model\n")
		}
		if c.Bot.ToolApprovalMode != "" {
			fmt.Fprintf(&b, "tool_approval_mode = %q   # ask|auto|yolo; yolo skips tool approvals only\n", c.Bot.ToolApprovalMode)
		} else {
			b.WriteString("# tool_approval_mode = \"ask\"   # ask|auto|yolo; ask and plan decisions still wait\n")
		}
		fmt.Fprintf(&b, "max_steps = %d\n", c.Bot.MaxSteps)
		fmt.Fprintf(&b, "debounce_ms = %d\n", c.Bot.DebounceMs)
		if c.Bot.QueueMode != "" {
			fmt.Fprintf(&b, "queue_mode = %q   # steer|followup|collect|interrupt\n", c.Bot.QueueMode)
		} else {
			b.WriteString("# queue_mode = \"steer\"   # steer|followup|collect|interrupt\n")
		}
		if c.Bot.QueueCap > 0 {
			fmt.Fprintf(&b, "queue_cap = %d\n", c.Bot.QueueCap)
		} else {
			b.WriteString("# queue_cap = 20\n")
		}
		if c.Bot.QueueDrop != "" {
			fmt.Fprintf(&b, "queue_drop = %q   # summarize|old|new\n", c.Bot.QueueDrop)
		} else {
			b.WriteString("# queue_drop = \"summarize\"   # summarize|old|new\n")
		}
		fmt.Fprintf(&b, "ignore_self_messages = %v   # ignore bot echo by returned message_id and configured self user ids\n", c.Bot.IgnoreSelfMessages)
		b.WriteString("\n[bot.self_user_ids]\n")
		fmt.Fprintf(&b, "qq = %s\n", renderStringArray(c.Bot.SelfUserIDs.QQ))
		fmt.Fprintf(&b, "feishu = %s\n", renderStringArray(c.Bot.SelfUserIDs.Feishu))
		fmt.Fprintf(&b, "weixin = %s\n", renderStringArray(c.Bot.SelfUserIDs.Weixin))
		fmt.Fprintf(&b, "dingtalk = %s\n", renderStringArray(c.Bot.SelfUserIDs.Dingtalk))
		b.WriteString("\n[bot.control]\n")
		fmt.Fprintf(&b, "enabled = %v   # local loopback HTTP API for status/send; requires Bearer token\n", c.Bot.Control.Enabled)
		if strings.TrimSpace(c.Bot.Control.Addr) != "" {
			fmt.Fprintf(&b, "addr = %q\n", c.Bot.Control.Addr)
		} else {
			b.WriteString("# addr = \"127.0.0.1:37913\"\n")
		}
		if strings.TrimSpace(c.Bot.Control.TokenEnv) != "" {
			fmt.Fprintf(&b, "token_env = %q\n", c.Bot.Control.TokenEnv)
		} else {
			b.WriteString("# token_env = \"REASONIX_BOT_CONTROL_TOKEN\"\n")
		}
		if len(c.Bot.Routes) > 0 {
			for _, route := range c.Bot.Routes {
				b.WriteString("\n[[bot.routes]]\n")
				renderBotRoute(&b, route)
			}
		}
		if len(c.Bot.DesktopWatchers) > 0 {
			for _, watcher := range c.Bot.DesktopWatchers {
				b.WriteString("\n[[bot.desktop_watchers]]\n")
				renderBotDesktopWatcher(&b, watcher)
			}
		}
		b.WriteString("\n[bot.pairing]\n")
		fmt.Fprintf(&b, "enabled = %v\n", c.Bot.Pairing.Enabled)
		if c.Bot.Pairing.RequestTTLMinutes > 0 {
			fmt.Fprintf(&b, "request_ttl_minutes = %d\n", c.Bot.Pairing.RequestTTLMinutes)
		} else {
			b.WriteString("# request_ttl_minutes = 60\n")
		}
		if c.Bot.Pairing.MaxPendingPerPlatform > 0 {
			fmt.Fprintf(&b, "max_pending_per_platform = %d\n", c.Bot.Pairing.MaxPendingPerPlatform)
		} else {
			b.WriteString("# max_pending_per_platform = 3\n")
		}
		b.WriteString("\n[bot.allowlist]\n")
		fmt.Fprintf(&b, "enabled = %v\n", c.Bot.Allowlist.Enabled)
		fmt.Fprintf(&b, "allow_all = %v\n", c.Bot.Allowlist.AllowAll)
		fmt.Fprintf(&b, "qq_users = %s\n", renderStringArray(c.Bot.Allowlist.QQUsers))
		fmt.Fprintf(&b, "feishu_users = %s\n", renderStringArray(c.Bot.Allowlist.FeishuUsers))
		fmt.Fprintf(&b, "weixin_users = %s\n", renderStringArray(c.Bot.Allowlist.WeixinUsers))
		fmt.Fprintf(&b, "dingtalk_users = %s\n", renderStringArray(c.Bot.Allowlist.DingtalkUsers))
		fmt.Fprintf(&b, "qq_approvers = %s\n", renderStringArray(c.Bot.Allowlist.QQApprovers))
		fmt.Fprintf(&b, "feishu_approvers = %s\n", renderStringArray(c.Bot.Allowlist.FeishuApprovers))
		fmt.Fprintf(&b, "weixin_approvers = %s\n", renderStringArray(c.Bot.Allowlist.WeixinApprovers))
		fmt.Fprintf(&b, "dingtalk_approvers = %s\n", renderStringArray(c.Bot.Allowlist.DingtalkApprovers))
		fmt.Fprintf(&b, "qq_admins = %s\n", renderStringArray(c.Bot.Allowlist.QQAdmins))
		fmt.Fprintf(&b, "feishu_admins = %s\n", renderStringArray(c.Bot.Allowlist.FeishuAdmins))
		fmt.Fprintf(&b, "weixin_admins = %s\n", renderStringArray(c.Bot.Allowlist.WeixinAdmins))
		fmt.Fprintf(&b, "dingtalk_admins = %s\n", renderStringArray(c.Bot.Allowlist.DingtalkAdmins))
		fmt.Fprintf(&b, "qq_groups = %s\n", renderStringArray(c.Bot.Allowlist.QQGroups))
		fmt.Fprintf(&b, "feishu_groups = %s\n", renderStringArray(c.Bot.Allowlist.FeishuGroups))
		fmt.Fprintf(&b, "weixin_groups = %s\n", renderStringArray(c.Bot.Allowlist.WeixinGroups))
		fmt.Fprintf(&b, "dingtalk_groups = %s\n", renderStringArray(c.Bot.Allowlist.DingtalkGroups))
		b.WriteString("\n[bot.qq]\n")
		fmt.Fprintf(&b, "enabled = %v\n", c.Bot.QQ.Enabled)
		fmt.Fprintf(&b, "app_id = %q\n", c.Bot.QQ.AppID)
		fmt.Fprintf(&b, "app_secret_env = %q\n", c.Bot.QQ.AppSecretEnv)
		fmt.Fprintf(&b, "sandbox = %v\n", c.Bot.QQ.Sandbox)
		if strings.TrimSpace(c.Bot.QQ.Model) != "" {
			fmt.Fprintf(&b, "model = %q\n", strings.TrimSpace(c.Bot.QQ.Model))
		}
		if strings.TrimSpace(c.Bot.QQ.ToolApprovalMode) != "" {
			fmt.Fprintf(&b, "tool_approval_mode = %q\n", strings.TrimSpace(c.Bot.QQ.ToolApprovalMode))
		}
		if strings.TrimSpace(c.Bot.QQ.WorkspaceRoot) != "" {
			fmt.Fprintf(&b, "workspace_root = %q\n", strings.TrimSpace(c.Bot.QQ.WorkspaceRoot))
		}
		if parts := renderBotAccess(c.Bot.QQ.Access); parts != "" {
			fmt.Fprintf(&b, "access = %s\n", parts)
		}
		b.WriteString("\n[bot.feishu]\n")
		fmt.Fprintf(&b, "enabled = %v\n", c.Bot.Feishu.Enabled)
		fmt.Fprintf(&b, "app_id = %q\n", c.Bot.Feishu.AppID)
		fmt.Fprintf(&b, "domain = %q\n", c.Bot.Feishu.Domain)
		fmt.Fprintf(&b, "app_secret_env = %q\n", c.Bot.Feishu.AppSecretEnv)
		fmt.Fprintf(&b, "verification_token = %q\n", c.Bot.Feishu.VerificationToken)
		fmt.Fprintf(&b, "mode = %q\n", c.Bot.Feishu.Mode)
		fmt.Fprintf(&b, "webhook_port = %d\n", c.Bot.Feishu.WebhookPort)
		fmt.Fprintf(&b, "require_mention = %v\n", c.Bot.Feishu.RequireMention)
		if len(c.Bot.Feishu.OutboundMediaRoots) > 0 {
			fmt.Fprintf(&b, "outbound_media_roots = %s\n", renderStringArray(c.Bot.Feishu.OutboundMediaRoots))
		}
		b.WriteString("\n[bot.weixin]\n")
		fmt.Fprintf(&b, "enabled = %v\n", c.Bot.Weixin.Enabled)
		fmt.Fprintf(&b, "account_id = %q\n", c.Bot.Weixin.AccountID)
		fmt.Fprintf(&b, "token_env = %q\n", c.Bot.Weixin.TokenEnv)
		fmt.Fprintf(&b, "api_base = %q\n", c.Bot.Weixin.APIBase)
		b.WriteString("\n[bot.dingtalk]\n")
		fmt.Fprintf(&b, "enabled = %v\n", c.Bot.Dingtalk.Enabled)
		fmt.Fprintf(&b, "client_id = %q\n", c.Bot.Dingtalk.ClientID)
		fmt.Fprintf(&b, "client_secret = %q\n", c.Bot.Dingtalk.ClientSecret)
		fmt.Fprintf(&b, "client_id_env = %q\n", c.Bot.Dingtalk.ClientIDEnv)
		fmt.Fprintf(&b, "secret_env = %q\n", c.Bot.Dingtalk.SecretEnv)
		fmt.Fprintf(&b, "bot_name = %q\n", c.Bot.Dingtalk.BotName)
		fmt.Fprintf(&b, "require_mention = %v\n", c.Bot.Dingtalk.RequireMention)
		if strings.TrimSpace(c.Bot.Dingtalk.Model) != "" {
			fmt.Fprintf(&b, "model = %q\n", strings.TrimSpace(c.Bot.Dingtalk.Model))
		}
		if strings.TrimSpace(c.Bot.Dingtalk.ToolApprovalMode) != "" {
			fmt.Fprintf(&b, "tool_approval_mode = %q\n", strings.TrimSpace(c.Bot.Dingtalk.ToolApprovalMode))
		}
		if strings.TrimSpace(c.Bot.Dingtalk.WorkspaceRoot) != "" {
			fmt.Fprintf(&b, "workspace_root = %q\n", strings.TrimSpace(c.Bot.Dingtalk.WorkspaceRoot))
		}
		if parts := renderBotAccess(c.Bot.Dingtalk.Access); parts != "" {
			fmt.Fprintf(&b, "access = %s\n", parts)
		}
		if len(c.Bot.Dingtalk.SessionMappings) > 0 {
			fmt.Fprintf(&b, "session_mappings = %s\n", renderBotSessionMappings(c.Bot.Dingtalk.SessionMappings))
		}
		for _, conn := range c.Bot.Connections {
			b.WriteString("\n[[bot.connections]]\n")
			fmt.Fprintf(&b, "id = %q\n", conn.ID)
			fmt.Fprintf(&b, "provider = %q\n", conn.Provider)
			fmt.Fprintf(&b, "domain = %q\n", conn.Domain)
			fmt.Fprintf(&b, "label = %q\n", conn.Label)
			fmt.Fprintf(&b, "enabled = %v\n", conn.Enabled)
			fmt.Fprintf(&b, "status = %q\n", conn.Status)
			if conn.Model != "" {
				fmt.Fprintf(&b, "model = %q\n", conn.Model)
			}
			if conn.ToolApprovalMode != "" {
				fmt.Fprintf(&b, "tool_approval_mode = %q\n", conn.ToolApprovalMode)
			}
			if conn.WorkspaceRoot != "" {
				fmt.Fprintf(&b, "workspace_root = %q\n", conn.WorkspaceRoot)
			}
			if parts := renderBotAccess(conn.Access); parts != "" {
				fmt.Fprintf(&b, "access = %s\n", parts)
			}
			if conn.LastError != "" {
				fmt.Fprintf(&b, "last_error = %q\n", conn.LastError)
			}
			if conn.CreatedAt != "" {
				fmt.Fprintf(&b, "created_at = %q\n", conn.CreatedAt)
			}
			if conn.UpdatedAt != "" {
				fmt.Fprintf(&b, "updated_at = %q\n", conn.UpdatedAt)
			}
			if parts := renderBotCredential(conn.Credential); parts != "" {
				fmt.Fprintf(&b, "credential = %s\n", parts)
			}
			if len(conn.SessionMappings) > 0 {
				fmt.Fprintf(&b, "session_mappings = %s\n", renderBotSessionMappings(conn.SessionMappings))
			}
		}
		b.WriteString("\n")
	}

	// [secrets] is user/global only: LoadForRoot discards project values, so
	// the project scope never renders it. Rendering it here is what lets a
	// user's saved toggles survive config rewrites (WriteFile re-renders the
	// whole file from the struct).
	if scope != RenderScopeProject {
		b.WriteString("[secrets]   # credential protection; user/global only, ./reasonix.toml cannot override\n")
		if c.Secrets.FilterSubprocessEnv {
			b.WriteString("filter_subprocess_env = true   # strip credential-named env vars from tool/hook/LSP/MCP subprocesses\n")
		} else {
			b.WriteString("# filter_subprocess_env = false   # opt-in; stripping tokens breaks gh, HTTPS git push, npm publish\n")
		}
		if c.Secrets.ProtectSensitiveFiles {
			b.WriteString("protect_sensitive_files = true   # hide .env/.git-credentials/key files/~/.ssh from read tools\n")
		} else {
			b.WriteString("# protect_sensitive_files = false   # opt-in; hiding credential files can break legitimate edit workflows\n")
		}
		b.WriteString("\n")
	}

	// [serve] is user/global only: it carries the serve frontend auth settings
	// and the bus bearer tokens, which must never land in a shared project
	// reasonix.toml. Rendered via renderServeConfig (task 432): before that the
	// whole section was silently dropped on save — `reasonix bus enroll` set
	// [serve.bus_mcp] in memory, cfg.Save() rewrote the file without it, and
	// enroll reported success.
	renderServeConfig(&b, c, scope)

	renderRemoteConfig(&b, c, scope)

	b.WriteString("# External MCP servers. type: \"stdio\" (default, a subprocess) | \"http\" | \"sse\".\n")
	b.WriteString("# ${VAR} / ${VAR:-default} are expanded from the environment in command/args/env/url/headers.\n")
	plugins := tomlPluginsForScope(c.Plugins, scope)
	if len(plugins) == 0 {
		b.WriteString("# [[plugins]]\n")
		b.WriteString("# name    = \"example\"\n")
		b.WriteString("# command = \"reasonix-plugin-example\"\n")
		b.WriteString("# startup_timeout_seconds = 60    # optional initialize + tools/list cap\n")
		b.WriteString("# call_timeout_seconds = 600       # optional per-server MCP call timeout\n")
		b.WriteString("# tool_timeout_seconds = { \"generate_video\" = 1800 }   # raw MCP tool names\n")
		b.WriteString("# [[plugins]]                                  # a remote server over Streamable HTTP\n")
		b.WriteString("# name    = \"stripe\"\n")
		b.WriteString("# type    = \"http\"\n")
		b.WriteString("# url     = \"https://mcp.stripe.com\"\n")
		b.WriteString("# headers = { Authorization = \"Bearer ${STRIPE_KEY}\" }\n")
	} else {
		for _, pl := range plugins {
			b.WriteString("\n[[plugins]]\n")
			fmt.Fprintf(&b, "name    = %q\n", pl.Name)
			if pl.Type != "" {
				fmt.Fprintf(&b, "type    = %q\n", pl.Type)
			}
			if pl.Command != "" {
				fmt.Fprintf(&b, "command = %q\n", pl.Command)
			}
			if len(pl.Args) > 0 {
				fmt.Fprintf(&b, "args    = %s\n", renderStringArray(pl.Args))
			}
			if pl.URL != "" {
				fmt.Fprintf(&b, "url     = %q\n", pl.URL)
			}
			if len(pl.Headers) > 0 {
				fmt.Fprintf(&b, "headers = %s\n", renderStringMap(pl.Headers))
			}
			if len(pl.Env) > 0 {
				fmt.Fprintf(&b, "env     = %s\n", renderStringMap(pl.Env))
			}
			if pl.StartupTimeoutSeconds > 0 {
				b.WriteString("# Per-server MCP initialize + tools/list timeout; 0 keeps the global/default cap.\n")
				fmt.Fprintf(&b, "startup_timeout_seconds = %d\n", pl.StartupTimeoutSeconds)
			}
			if pl.CallTimeoutSeconds > 0 {
				b.WriteString("# Per-server MCP call timeout; 0 keeps the global/default cap.\n")
				fmt.Fprintf(&b, "call_timeout_seconds = %d\n", pl.CallTimeoutSeconds)
			}
			if hasPositiveIntMap(pl.ToolTimeoutSeconds) {
				b.WriteString("# Raw MCP tool names with per-tool call timeouts.\n")
				fmt.Fprintf(&b, "tool_timeout_seconds = %s\n", renderIntMap(pl.ToolTimeoutSeconds))
			}
			renderPluginPolicy(&b, pl)
		}
	}

	return b.String()
}

// tomlPluginsForScope keeps merged runtime entries in their owning config
// source. Unknown provenance is retained for callers that construct a Config
// directly before saving it to a specific target.
func tomlPluginsForScope(plugins []PluginEntry, scope RenderScope) []PluginEntry {
	if scope == RenderScopeFull {
		return plugins
	}
	out := make([]PluginEntry, 0, len(plugins))
	for _, pl := range plugins {
		switch pl.Source {
		case MCPSourceUnknown:
			out = append(out, pl)
		case MCPSourceUserConfig:
			if scope == RenderScopeUser {
				out = append(out, pl)
			}
		case MCPSourceProjectConfig:
			if scope == RenderScopeProject {
				out = append(out, pl)
			}
		}
	}
	return out
}

// RenderTOMLProjectDelta generates TOML containing only the sections and fields
// that differ from built-in defaults. Unlike RenderTOMLForScope (which renders
// the full config with comments), this emits clean TOML that can be surgically
// merged into an existing project config file via replaceTOMLSection.
func RenderTOMLProjectDelta(c *Config) string {
	if c == nil {
		return ""
	}
	d := Default()
	var b strings.Builder

	// Top-level scalar fields
	if v := configVersion(c); v != d.ConfigVersion {
		fmt.Fprintf(&b, "config_version = %d\n", v)
	}
	if c.DefaultModel != d.DefaultModel {
		fmt.Fprintf(&b, "default_model = %q\n", c.DefaultModel)
	}
	// Conversation store mode (task 155). Rendered unconditionally: it lives on
	// the top-level Config rather than [desktop], and a hand-added line used to be
	// dropped by the next settings save. (It used to sit inside the default_model
	// branch above, so a mode change on its own was silently dropped on save.)
	fmt.Fprintf(&b, "session_storage = %q   # v3_only (default) | dual_write_read_v3 | dual_write_read_v4 | v4_only (needs a restart)\n", SessionStorageMode(c))
	// Event-log rotation gate/thresholds (task 333): rendered unconditionally
	// like session_storage above — mergeTOMLTopLevelFields only replaces keys
	// the delta carries, so a sparse rendering would leave a stale
	// events_auto_rotation = "auto" line behind after the setting reverts to
	// the default (the 81/123 lost-line class, inverted; review finding).
	fmt.Fprintf(&b, "events_auto_rotation = %q   # off | manual (default) | auto\n", EventsAutoRotationMode(c))
	fmt.Fprintf(&b, "events_rotation_factor = %.1f   # auto only (2-16)\n", EventsRotationFactor(c))
	fmt.Fprintf(&b, "events_rotation_cap_mb = %d   # auto only; 0 = off\n", EventsRotationCapMB(c))
	if c.Language != "" && c.Language != d.Language {
		fmt.Fprintf(&b, "language = %q\n", c.Language)
	}

	// [ui] section — whole-section comparison
	if !reflect.DeepEqual(c.UI, d.UI) {
		b.WriteString("[ui]\n")
		if c.UI.Theme != d.UI.Theme {
			fmt.Fprintf(&b, "theme = %q\n", c.UITheme())
		}
		if s := c.UIThemeStyle(); s != "" && s != d.UIThemeStyle() {
			fmt.Fprintf(&b, "theme_style = %q\n", s)
		}
		if l := c.UIShortcutLayout(); l != "classic" {
			fmt.Fprintf(&b, "shortcut_layout = %q\n", l)
		}
		if strings.TrimSpace(c.UI.CursorShape) != "" {
			fmt.Fprintf(&b, "cursor_shape = %q\n", c.UICursorShape())
		}
		if c.UI.CloseBehavior != d.UI.CloseBehavior {
			fmt.Fprintf(&b, "close_behavior = %q\n", c.DesktopCloseBehavior())
		}
		if c.UI.ShowReasoning != d.UI.ShowReasoning {
			fmt.Fprintf(&b, "show_reasoning = %v\n", c.UI.ShowReasoning)
		}
		if c.UI.ShowTurnUsage != d.UI.ShowTurnUsage {
			fmt.Fprintf(&b, "show_turn_usage = %v\n", c.UI.ShowTurnUsage)
		}
		b.WriteString("\n")
	}

	// [network] section
	if !reflect.DeepEqual(c.Network, d.Network) {
		b.WriteString("[network]\n")
		if c.Network.ProxyMode != d.Network.ProxyMode {
			fmt.Fprintf(&b, "proxy_mode = %q\n", c.NetworkProxyMode())
		}
		if c.Network.ProxyURL != "" {
			fmt.Fprintf(&b, "proxy_url = %q\n", c.Network.ProxyURL)
		}
		if c.Network.NoProxy != "" {
			fmt.Fprintf(&b, "no_proxy = %q\n", c.Network.NoProxy)
		}
		if c.Network.Proxy.Type != "" || c.Network.Proxy.Server != "" || c.Network.Proxy.Port > 0 || c.Network.Proxy.Username != "" || c.Network.Proxy.Password != "" {
			b.WriteString("[network.proxy]\n")
			pt := c.Network.Proxy.Type
			if pt == "" {
				pt = "socks5"
			}
			fmt.Fprintf(&b, "type = %q\n", pt)
			if c.Network.Proxy.Server != "" {
				fmt.Fprintf(&b, "server = %q\n", c.Network.Proxy.Server)
			}
			if c.Network.Proxy.Port > 0 {
				fmt.Fprintf(&b, "port = %d\n", c.Network.Proxy.Port)
			}
			if c.Network.Proxy.Username != "" {
				fmt.Fprintf(&b, "username = %q\n", c.Network.Proxy.Username)
			}
			if c.Network.Proxy.Password != "" {
				fmt.Fprintf(&b, "password = %q\n", c.Network.Proxy.Password)
			}
		}
		b.WriteString("\n")
	}

	// [agent] section — per-field comparison
	var agentBuf strings.Builder
	anyAgent := false

	if sp := strings.TrimSpace(c.Agent.SystemPrompt); sp != "" && sp != d.Agent.SystemPrompt {
		agentBuf.WriteString("system_prompt = \"\"\"\n")
		agentBuf.WriteString(sp)
		agentBuf.WriteString("\"\"\"\n")
		anyAgent = true
	}
	if c.Agent.SystemPromptFile != "" && c.Agent.SystemPromptFile != d.Agent.SystemPromptFile {
		fmt.Fprintf(&agentBuf, "system_prompt_file = %q\n", c.Agent.SystemPromptFile)
		anyAgent = true
	}
	if c.Agent.Temperature != d.Agent.Temperature {
		fmt.Fprintf(&agentBuf, "temperature = %s\n", formatFloat(c.Agent.Temperature))
		anyAgent = true
	}
	diffRecoveryAndCompletionValidation(&agentBuf, *c, *d, &anyAgent)
	if c.Agent.ReasoningLanguage != d.Agent.ReasoningLanguage {
		if l := c.ReasoningLanguage(); l != "auto" {
			fmt.Fprintf(&agentBuf, "reasoning_language = %q\n", l)
			anyAgent = true
		}
	}
	if c.Agent.CompactRatio != d.Agent.CompactRatio {
		fmt.Fprintf(&agentBuf, "compact_ratio = %s\n", formatFloat(c.Agent.CompactRatio))
		anyAgent = true
	}
	if c.Agent.Keep != nil && !reflect.DeepEqual(c.Agent.Keep, d.Agent.Keep) {
		fmt.Fprintf(&agentBuf, "keep = %s\n", renderStringArray(c.Agent.Keep))
		anyAgent = true
	}
	if c.Agent.RecentKeep > 0 && c.Agent.RecentKeep != d.Agent.RecentKeep {
		fmt.Fprintf(&agentBuf, "recent_keep = %d\n", c.Agent.RecentKeep)
		anyAgent = true
	}
	if len(c.Agent.PlanModeReadOnlyCommands) > 0 && !reflect.DeepEqual(c.Agent.PlanModeReadOnlyCommands, d.Agent.PlanModeReadOnlyCommands) {
		fmt.Fprintf(&agentBuf, "plan_mode_read_only_commands = %s\n", renderStringArray(c.Agent.PlanModeReadOnlyCommands))
		anyAgent = true
	}
	if c.Agent.TextRepeatN != 0 && c.Agent.TextRepeatN != d.Agent.TextRepeatN {
		fmt.Fprintf(&agentBuf, "text_repeat_n = %d\n", c.Agent.TextRepeatN)
		anyAgent = true
	}
	if c.Agent.TextRepeatThreshold != 0 && c.Agent.TextRepeatThreshold != d.Agent.TextRepeatThreshold {
		fmt.Fprintf(&agentBuf, "text_repeat_threshold = %d\n", c.Agent.TextRepeatThreshold)
		anyAgent = true
	}
	renderAgentModelAssignmentDelta(&agentBuf, c, d, &anyAgent)
	if c.Agent.SubagentEffort != "" && c.Agent.SubagentEffort != d.Agent.SubagentEffort {
		fmt.Fprintf(&agentBuf, "subagent_effort = %q\n", c.Agent.SubagentEffort)
		anyAgent = true
	}
	if len(c.Agent.SubagentEfforts) > 0 && !reflect.DeepEqual(c.Agent.SubagentEfforts, d.Agent.SubagentEfforts) {
		fmt.Fprintf(&agentBuf, "subagent_efforts = %s\n", renderStringMap(c.Agent.SubagentEfforts))
		anyAgent = true
	}
	if c.Agent.MaxSubagentDepth != d.Agent.MaxSubagentDepth {
		fmt.Fprintf(&agentBuf, "max_subagent_depth = %d\n", c.Agent.MaxSubagentDepth)
		anyAgent = true
	}
	if c.Agent.OutputStyle != "" && c.Agent.OutputStyle != d.Agent.OutputStyle {
		fmt.Fprintf(&agentBuf, "output_style = %q\n", c.Agent.OutputStyle)
		anyAgent = true
	}

	if anyAgent {
		b.WriteString("[agent]\n")
		b.WriteString(agentBuf.String())
		b.WriteString("\n")
	}

	// [[providers]] — include user-defined providers that aren't built-in
	proj := projectScopedConfigForRender(c)
	if proj != nil && len(proj.Providers) > 0 && !reflect.DeepEqual(proj.Providers, d.Providers) {
		for _, p := range proj.Providers {
			b.WriteString("[[providers]]\n")
			fmt.Fprintf(&b, "name        = %q\n", p.Name)
			fmt.Fprintf(&b, "kind        = %q\n", p.Kind)
			fmt.Fprintf(&b, "base_url    = %q\n", p.BaseURL)
			if p.ChatURL != "" {
				fmt.Fprintf(&b, "chat_url    = %q\n", p.ChatURL)
			}
			if p.RequestURL != "" {
				fmt.Fprintf(&b, "request_url = %q\n", p.RequestURL)
			}
			if len(p.Models) > 0 {
				fmt.Fprintf(&b, "models      = %s\n", renderStringArray(p.Models))
				if p.Default != "" {
					fmt.Fprintf(&b, "default     = %q\n", p.Default)
				}
			} else if p.Model != "" {
				fmt.Fprintf(&b, "model       = %q\n", p.Model)
			}
			if p.ModelsURL != "" {
				fmt.Fprintf(&b, "models_url  = %q\n", p.ModelsURL)
			}
			renderProviderIdentity(&b, p.APIKeyEnv, p.DisplayName)
			renderProviderHidden(&b, p.Hidden)
			if p.PresetID != "" {
				fmt.Fprintf(&b, "preset_id   = %q\n", p.PresetID)
			}
			if p.PresetVersion > 0 {
				fmt.Fprintf(&b, "preset_version = %d\n", p.PresetVersion)
			}
			if len(p.Headers) > 0 {
				fmt.Fprintf(&b, "headers     = %s\n", renderStringMap(p.Headers))
			}
			if len(p.ExtraBody) > 0 {
				fmt.Fprintf(&b, "extra_body  = %s\n", renderAnyMap(p.ExtraBody))
			}
			if p.AuthHeader {
				b.WriteString("auth_header = true\n")
			}
			if p.ResponsesMode != "" {
				fmt.Fprintf(&b, "responses_mode = %q\n", p.ResponsesMode)
			}
			if p.ResponsesStateful != nil {
				fmt.Fprintf(&b, "responses_stateful = %t\n", *p.ResponsesStateful)
			}
			if p.BalanceURL != "" {
				fmt.Fprintf(&b, "balance_url = %q\n", p.BalanceURL)
			}
			if p.ContextWindow > 0 {
				fmt.Fprintf(&b, "context_window = %d\n", p.ContextWindow)
			}
			if p.MaxOutputTokens != 0 {
				fmt.Fprintf(&b, "max_output_tokens = %d\n", p.MaxOutputTokens)
			}
			if p.Price != nil {
				fmt.Fprintf(&b, "price       = %s\n", renderPricingInline(p.Price))
			}
			if len(p.Prices) > 0 {
				fmt.Fprintf(&b, "prices      = %s\n", renderPricingMap(p.Prices))
			}
			if cur := strings.TrimSpace(p.BillingCurrency); cur != "" {
				fmt.Fprintf(&b, "billing_currency = %q\n", billing.NormalizeCurrency(cur))
			}
			if mode := strings.TrimSpace(p.BillingMode); mode != "" && mode != "payg" {
				fmt.Fprintf(&b, "billing_mode = %q\n", mode)
			}
			if p.Thinking != "" {
				fmt.Fprintf(&b, "thinking    = %q\n", p.Thinking)
			}
			if p.Effort != "" {
				fmt.Fprintf(&b, "effort      = %q\n", p.Effort)
			}
			if p.Vision {
				b.WriteString("vision      = true\n")
			}
			if p.VisionModels != nil {
				fmt.Fprintf(&b, "vision_models = %s\n", renderStringArray(p.VisionModels))
			}
			if p.VisionDetail != "" {
				fmt.Fprintf(&b, "vision_detail = %q\n", p.VisionDetail)
			}
			if p.WebSearch != nil {
				fmt.Fprintf(&b, "web_search  = %t\n", *p.WebSearch)
			}
			if p.ReasoningProtocol != "" {
				fmt.Fprintf(&b, "reasoning_protocol = %q\n", p.ReasoningProtocol)
			}
			if len(p.SupportedEfforts) > 0 {
				fmt.Fprintf(&b, "supported_efforts = %s\n", renderStringArray(p.SupportedEfforts))
			}
			if p.DefaultEffort != "" {
				fmt.Fprintf(&b, "default_effort    = %q\n", p.DefaultEffort)
			}
			if len(p.ModelOverrides) > 0 {
				fmt.Fprintf(&b, "model_overrides   = %s\n", renderModelOverrides(p.ModelOverrides))
			}
			if p.NoProxy {
				b.WriteString("no_proxy    = true\n")
			}
			b.WriteString("\n")
		}
	}

	// [tools]
	if len(c.Tools.Enabled) > 0 ||
		(c.Tools.BashTimeoutSeconds != nil && *c.Tools.BashTimeoutSeconds != 0) ||
		(c.Tools.MCPStartupTimeoutSeconds != nil && *c.Tools.MCPStartupTimeoutSeconds > 0) ||
		(c.Tools.MCPCallTimeoutSeconds != nil && *c.Tools.MCPCallTimeoutSeconds > 0) {
		b.WriteString("[tools]\n")
		if len(c.Tools.Enabled) > 0 {
			fmt.Fprintf(&b, "enabled = %s\n", renderStringArray(c.Tools.Enabled))
		}
		if c.Tools.BashTimeoutSeconds != nil && *c.Tools.BashTimeoutSeconds != 0 {
			fmt.Fprintf(&b, "bash_timeout_seconds = %d\n", *c.Tools.BashTimeoutSeconds)
		}
		if c.Tools.MCPStartupTimeoutSeconds != nil && *c.Tools.MCPStartupTimeoutSeconds > 0 {
			fmt.Fprintf(&b, "mcp_startup_timeout_seconds = %d\n", *c.Tools.MCPStartupTimeoutSeconds)
		}
		if c.Tools.MCPCallTimeoutSeconds != nil && *c.Tools.MCPCallTimeoutSeconds > 0 {
			fmt.Fprintf(&b, "mcp_call_timeout_seconds = %d\n", *c.Tools.MCPCallTimeoutSeconds)
		}
		b.WriteString("\n")
	}

	// [tools.background_jobs]
	if c.Tools.BackgroundJobs != d.Tools.BackgroundJobs {
		if c.Tools.BackgroundJobs.StalledWarningSeconds != nil && *c.Tools.BackgroundJobs.StalledWarningSeconds > 0 {
			b.WriteString("[tools.background_jobs]\n")
			fmt.Fprintf(&b, "stalled_warning_seconds = %d\n", *c.Tools.BackgroundJobs.StalledWarningSeconds)
			b.WriteString("\n")
		}
	}

	// [tools.shell]
	if !reflect.DeepEqual(c.Tools.Shell, d.Tools.Shell) {
		b.WriteString("[tools.shell]\n")
		if c.Tools.Shell.Prefer != d.Tools.Shell.Prefer {
			fmt.Fprintf(&b, "prefer = %q\n", c.Tools.Shell.Prefer)
		}
		if c.Tools.Shell.Path != d.Tools.Shell.Path {
			fmt.Fprintf(&b, "path = %q\n", c.Tools.Shell.Path)
		}
		b.WriteString("\n")
	}

	// [lsp]
	if !reflect.DeepEqual(c.LSP, d.LSP) {
		renderLSPConfig(&b, c.LSP)
	}

	// [skills]
	if !reflect.DeepEqual(c.Skills, d.Skills) || len(c.explicitProjectSkillKeys) > 0 {
		b.WriteString("[skills]\n")
		if len(c.Skills.Paths) > 0 || c.keepsProjectSkillKey("paths") {
			fmt.Fprintf(&b, "paths = %s\n", renderStringArray(c.Skills.Paths))
		}
		if len(c.Skills.ExcludedPaths) > 0 || c.keepsProjectSkillKey("excluded_paths") {
			fmt.Fprintf(&b, "excluded_paths = %s\n", renderStringArray(c.Skills.ExcludedPaths))
		}
		if c.Skills.DisableImplicitInvocation || c.keepsProjectSkillKey("disable_implicit_invocation") {
			fmt.Fprintf(&b, "disable_implicit_invocation = %t\n", c.Skills.DisableImplicitInvocation)
		}
		if c.Skills.MaxDepth != 0 || c.keepsProjectSkillKey("max_depth") {
			depth := c.Skills.MaxDepth
			if depth != 0 {
				depth = c.SkillMaxDepth()
			}
			fmt.Fprintf(&b, "max_depth = %d\n", depth)
		}
		if disabled := c.DisabledSkillNames(); len(disabled) > 0 || c.keepsProjectSkillKey("disabled_skills") {
			fmt.Fprintf(&b, "disabled_skills = %s\n\n", renderStringArray(disabled))
		}
	}

	// [permissions]
	if !reflect.DeepEqual(c.Permissions, d.Permissions) {
		b.WriteString("[permissions]\n")
		mode := c.Permissions.Mode
		if mode == "" {
			mode = "ask"
		}
		if mode != "ask" {
			fmt.Fprintf(&b, "mode = %q\n", mode)
		}
		if c.Permissions.AllowDynamicBash {
			b.WriteString("allow_dynamic_bash = true\n")
		}
		if len(c.Permissions.Deny) > 0 {
			fmt.Fprintf(&b, "deny = %s\n", renderStringArray(c.Permissions.Deny))
		}
		if len(c.Permissions.Allow) > 0 {
			fmt.Fprintf(&b, "allow = %s\n", renderStringArray(c.Permissions.Allow))
		}
		if len(c.Permissions.Ask) > 0 {
			fmt.Fprintf(&b, "ask = %s\n", renderStringArray(c.Permissions.Ask))
		}
		b.WriteString("\n")
	}

	// [sandbox]
	if !reflect.DeepEqual(c.Sandbox, d.Sandbox) {
		var sandboxBuf strings.Builder
		if c.Sandbox.WorkspaceRoot != "" {
			fmt.Fprintf(&sandboxBuf, "workspace_root = %q\n", c.Sandbox.WorkspaceRoot)
		}
		if len(c.Sandbox.AllowWrite) > 0 {
			fmt.Fprintf(&sandboxBuf, "allow_write = %s\n", renderStringArray(c.Sandbox.AllowWrite))
		}
		// Only persist a bash mode when its effective value differs from the
		// platform default. On Windows, even explicit "enforce" currently
		// resolves to "off", so project configs should not imply otherwise.
		if strings.TrimSpace(c.Sandbox.Bash) != "" && c.BashMode() != d.BashModeForGOOS(runtimeGOOS) {
			fmt.Fprintf(&sandboxBuf, "bash = %q\n", c.BashMode())
		}
		if c.Sandbox.Network != d.Sandbox.Network {
			fmt.Fprintf(&sandboxBuf, "network = %v\n", c.Sandbox.Network)
		}
		if c.Sandbox.OptimisticWrite != d.Sandbox.OptimisticWrite {
			fmt.Fprintf(&sandboxBuf, "optimistic_write = %v\n", c.Sandbox.OptimisticWrite)
		}
		if sandboxBuf.Len() > 0 {
			b.WriteString("[sandbox]\n")
			b.WriteString(sandboxBuf.String())
			b.WriteString("\n")
		}
	}

	// [statusline]
	if !reflect.DeepEqual(c.Statusline, d.Statusline) {
		b.WriteString("[statusline]\n")
		if c.Statusline.Command != "" {
			fmt.Fprintf(&b, "command = %q\n", c.Statusline.Command)
		}
		b.WriteString("\n")
	}

	// [[plugins]] — always include when set; replaces all existing entries
	for _, pl := range tomlPluginsForScope(c.Plugins, RenderScopeProject) {
		b.WriteString("[[plugins]]\n")
		fmt.Fprintf(&b, "name    = %q\n", pl.Name)
		if pl.Type != "" {
			fmt.Fprintf(&b, "type    = %q\n", pl.Type)
		}
		if pl.Command != "" {
			fmt.Fprintf(&b, "command = %q\n", pl.Command)
		}
		if len(pl.Args) > 0 {
			fmt.Fprintf(&b, "args    = %s\n", renderStringArray(pl.Args))
		}
		if pl.URL != "" {
			fmt.Fprintf(&b, "url     = %q\n", pl.URL)
		}
		if len(pl.Headers) > 0 {
			fmt.Fprintf(&b, "headers = %s\n", renderStringMap(pl.Headers))
		}
		if len(pl.Env) > 0 {
			fmt.Fprintf(&b, "env     = %s\n", renderStringMap(pl.Env))
		}
		if pl.StartupTimeoutSeconds > 0 {
			fmt.Fprintf(&b, "startup_timeout_seconds = %d\n", pl.StartupTimeoutSeconds)
		}
		if pl.CallTimeoutSeconds > 0 {
			b.WriteString("# Per-server MCP call timeout; 0 keeps the global/default cap.\n")
			fmt.Fprintf(&b, "call_timeout_seconds = %d\n", pl.CallTimeoutSeconds)
		}
		if hasPositiveIntMap(pl.ToolTimeoutSeconds) {
			b.WriteString("# Raw MCP tool names with per-tool call timeouts.\n")
			fmt.Fprintf(&b, "tool_timeout_seconds = %s\n", renderIntMap(pl.ToolTimeoutSeconds))
		}
		renderPluginPolicy(&b, pl)
		b.WriteString("\n")
	}

	return b.String()
}

func renderPricingInline(p *provider.Pricing) string {
	if p == nil {
		return "{}"
	}
	return fmt.Sprintf("{ cache_hit = %v, input = %v, output = %v, currency = %q }",
		p.CacheHit, p.Input, p.Output, p.Symbol())
}

func renderPricingMap(prices map[string]*provider.Pricing) string {
	if len(prices) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(prices))
	for model := range prices {
		if strings.TrimSpace(model) != "" && prices[model] != nil {
			keys = append(keys, model)
		}
	}
	if len(keys) == 0 {
		return "{}"
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, model := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = %s", strconv.Quote(model), renderPricingInline(prices[model]))
	}
	b.WriteString(" }")
	return b.String()
}

func configVersion(c *Config) int {
	if c != nil && c.ConfigVersion > 0 {
		return c.ConfigVersion
	}
	return Default().ConfigVersion
}

func shouldRenderUI(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.UI, defaults.UI)
}

func shouldRenderNetwork(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.Network, defaults.Network)
}

func shouldRenderEnvironment(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.Environment, defaults.Environment)
}

func renderEnvironmentConfig(b *strings.Builder, cfg EnvironmentConfig) {
	b.WriteString("[environment]\n")
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	fmt.Fprintf(b, "enabled = %v   # inject a stable startup environment summary into the model prompt\noffline = %v   # declare that outbound network access is unavailable; prevents futile retries\n", enabled, cfg.Offline)
	if len(cfg.Tools) == 0 {
		b.WriteString("# [environment.tools]\n")
		b.WriteString("# go = \"/opt/homebrew/bin/go\"   # trusted executable path; workspace-local paths are not auto-executed\n\n")
		return
	}
	b.WriteString("\n[environment.tools]\n")
	names := make([]string, 0, len(cfg.Tools))
	for name := range cfg.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(b, "%s = %q\n", renderTOMLKeyPart(name), cfg.Tools[name])
	}
	b.WriteString("\n")
}

func shouldRenderProviders(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.Providers, defaults.Providers)
}

func projectScopedConfigForRender(c *Config) *Config {
	if c == nil || len(c.providerSources) == 0 {
		return c
	}
	cp := *c
	cp.Providers = make([]ProviderEntry, 0, len(c.Providers))
	for _, p := range c.Providers {
		if c.providerSources[providerMergeKey(p)] == providerSourceUser {
			continue
		}
		cp.Providers = append(cp.Providers, p)
	}
	cp.Providers = append(cp.Providers, c.shadowedProjectProviders...)
	return &cp
}

func shouldRenderBot(c, defaults *Config, scope RenderScope) bool {
	if scope != RenderScopeProject {
		return true
	}
	return !reflect.DeepEqual(c.Bot, defaults.Bot)
}

func shouldRenderSystemPrompt(c, defaults *Config, scope RenderScope) bool {
	if scope == RenderScopeFull {
		return true
	}
	return strings.TrimSpace(c.Agent.SystemPrompt) != "" && c.Agent.SystemPrompt != defaults.Agent.SystemPrompt
}

func renderLSPConfig(b *strings.Builder, cfg LSPConfig) {
	b.WriteString("[lsp]\n")
	fmt.Fprintf(b, "enabled = %v   # language server tools; servers launch lazily when used\n", cfg.Enabled)
	if len(cfg.Servers) == 0 {
		b.WriteString("# [lsp.servers.go]\n")
		b.WriteString("# command = \"gopls\"\n")
		b.WriteString("# args = []\n")
		b.WriteString("# extensions = [\".go\"]\n\n")
		return
	}
	b.WriteString("\n")

	langs := make([]string, 0, len(cfg.Servers))
	for lang := range cfg.Servers {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		srv := cfg.Servers[lang]
		fmt.Fprintf(b, "[%s]\n", renderTOMLTablePath("lsp", "servers", lang))
		if srv.Command != "" {
			fmt.Fprintf(b, "command = %q\n", srv.Command)
		}
		if len(srv.Args) > 0 {
			fmt.Fprintf(b, "args = %s\n", renderStringArray(srv.Args))
		}
		if len(srv.Env) > 0 {
			fmt.Fprintf(b, "env = %s\n", renderStringMap(srv.Env))
		}
		if srv.LanguageID != "" {
			fmt.Fprintf(b, "language_id = %q\n", srv.LanguageID)
		}
		if len(srv.Extensions) > 0 {
			fmt.Fprintf(b, "extensions = %s\n", renderStringArray(srv.Extensions))
		}
		if srv.InstallHint != "" {
			fmt.Fprintf(b, "install_hint = %q\n", srv.InstallHint)
		}
		b.WriteString("\n")
	}
}

// renderServeConfig writes the [serve] frontend section plus its bus
// sub-tables ([serve.bus_mcp], [serve.bus_worker]). User/global only: the bus
// roles table carries bearer tokens, so the project scope never renders any of
// it. House style throughout: a set field renders its actual value, a
// field at its zero value renders as a commented example so the key set stays
// visible without inventing config for people who never run `reasonix serve`.
func renderServeConfig(b *strings.Builder, c *Config, scope RenderScope) {
	if scope == RenderScopeProject {
		return
	}
	b.WriteString("[serve]\n")
	if c.Serve.AuthMode != "" {
		fmt.Fprintf(b, "auth_mode = %q   # none (default) | token | password\n", c.Serve.AuthMode)
	} else {
		b.WriteString("# auth_mode = \"token\"   # none (default) | token | password\n")
	}
	if c.Serve.Token != "" {
		fmt.Fprintf(b, "token = %q   # pre-shared token for auth_mode = \"token\"\n", c.Serve.Token)
	} else {
		b.WriteString("# token = \"...\"   # pre-shared token for auth_mode = \"token\"; empty = a random token is generated per startup and printed\n")
	}
	if c.Serve.PasswordHash != "" {
		fmt.Fprintf(b, "password_hash = %q   # bcrypt hash for auth_mode = \"password\"\n", c.Serve.PasswordHash)
	} else {
		b.WriteString("# password_hash = \"...\"   # generate with `reasonix serve --hash-password --password '...'\n")
	}
	if c.Serve.BehindProxy {
		b.WriteString("behind_proxy = true   # trusted reverse proxy in front; X-Forwarded-* headers are honored\n")
	} else {
		b.WriteString("# behind_proxy = false   # set true only behind a trusted reverse proxy\n")
	}
	b.WriteString("\n")

	b.WriteString("[serve.bus_mcp]\n")
	if c.Serve.BusMCP.Enabled {
		b.WriteString("enabled = true   # mount /mcp and /bus routes for external runtimes (`reasonix bus enroll` sets this)\n")
	} else {
		b.WriteString("# enabled = false   # opt-in; `reasonix bus enroll --role dev` creates and enables this section\n")
	}
	if len(c.Serve.BusMCP.Roles) > 0 {
		fmt.Fprintf(b, "roles = %s   # role name → bearer token (256-bit hex); managed by `reasonix bus enroll`\n", renderStringMap(c.Serve.BusMCP.Roles))
	} else {
		b.WriteString("# roles = { dev = \"64-hex-token\" }   # role → bearer token; keep in sync with the runtime's own config\n")
	}
	if c.Serve.BusMCP.MailDir != "" {
		fmt.Fprintf(b, "mail_dir = %q   # shared collab mailbox override; empty = the default session-chat support directory\n", c.Serve.BusMCP.MailDir)
	} else {
		b.WriteString("# mail_dir = \"\"   # empty = bus mail and collab mail stay one stream\n")
	}
	if c.Serve.BusMCP.HopLimit != 0 {
		fmt.Fprintf(b, "hop_limit = %d   # mail chain ceiling; 0 keeps the default\n", c.Serve.BusMCP.HopLimit)
	} else {
		b.WriteString("# hop_limit = 0   # 0 keeps the default\n")
	}
	if c.Serve.BusMCP.EventTarget != "" {
		fmt.Fprintf(b, "event_target = %q   # contact for hook pushes (POST /bus/events); empty = zcode-heartbeat\n", c.Serve.BusMCP.EventTarget)
	} else {
		b.WriteString("# event_target = \"zcode-heartbeat\"   # contact for hook pushes (POST /bus/events); empty = zcode-heartbeat\n")
	}
	if len(c.Serve.BusMCP.SpawnRoles) > 0 {
		fmt.Fprintf(b, "spawn_roles = %s   # roles allowed to call collab_spawn; empty denies every role\n", renderStringArray(c.Serve.BusMCP.SpawnRoles))
	} else {
		b.WriteString("# spawn_roles = [\"dev\"]   # roles allowed to call collab_spawn; empty denies every role\n")
	}
	if c.Serve.BusMCP.SpawnDailyQuota != 0 {
		fmt.Fprintf(b, "spawn_daily_quota = %d   # collab_spawn calls per role per day; 0 keeps the default (20)\n", c.Serve.BusMCP.SpawnDailyQuota)
	} else {
		b.WriteString("# spawn_daily_quota = 0   # 0 keeps the default (20)\n")
	}
	b.WriteString("\n")

	b.WriteString("[serve.bus_worker]\n")
	if c.Serve.BusWorker.Enabled {
		b.WriteString("enabled = true   # unattended headless execution pool for bus task assignments\n")
	} else {
		b.WriteString("# enabled = false   # opt-in; spawns external processes per bus task\n")
	}
	if c.Serve.BusWorker.Command != "" {
		fmt.Fprintf(b, "command = %q   # agent CLI spawned per task; empty/default = zcode\n", c.Serve.BusWorker.Command)
	} else {
		b.WriteString("# command = \"zcode\"   # agent CLI spawned per task; empty = zcode\n")
	}
	if c.Serve.BusWorker.Mode != "" {
		fmt.Fprintf(b, "mode = %q   # headless permission mode passed to the CLI: build (default) | yolo\n", c.Serve.BusWorker.Mode)
	} else {
		b.WriteString("# mode = \"build\"   # headless permission mode: build (default; unapproved writes refused) | yolo\n")
	}
	if c.Serve.BusWorker.Workspace != "" {
		fmt.Fprintf(b, "workspace = %q   # working directory for spawned runs; empty = serve process cwd\n", c.Serve.BusWorker.Workspace)
	} else {
		b.WriteString("# workspace = \"\"   # empty = the serve process cwd; per-task workspaces ride the assignment mail\n")
	}
	if c.Serve.BusWorker.Concurrency != 0 {
		fmt.Fprintf(b, "concurrency = %d   # parallel runs; 0 keeps the default (2)\n", c.Serve.BusWorker.Concurrency)
	} else {
		b.WriteString("# concurrency = 0   # 0 keeps the default (2)\n")
	}
	if c.Serve.BusWorker.Timeout != "" {
		fmt.Fprintf(b, "timeout = %q   # per-run budget (Go duration); empty keeps the default (30m)\n", c.Serve.BusWorker.Timeout)
	} else {
		b.WriteString("# timeout = \"30m\"   # per-run budget; expiry kills the child process tree and fails the card\n")
	}
	if c.Serve.BusWorker.PollInterval != "" {
		fmt.Fprintf(b, "poll_interval = %q   # mailbox poll cadence; empty keeps the default (5s)\n", c.Serve.BusWorker.PollInterval)
	} else {
		b.WriteString("# poll_interval = \"5s\"   # mailbox poll cadence\n")
	}
	if c.Serve.BusWorker.Contact != "" {
		fmt.Fprintf(b, "contact = %q   # mailbox contact this pool drains; empty = zcode-worker\n", c.Serve.BusWorker.Contact)
	} else {
		b.WriteString("# contact = \"zcode-worker\"   # mailbox contact this pool drains\n")
	}
	if c.Serve.BusWorker.MailDir != "" {
		fmt.Fprintf(b, "mail_dir = %q   # mailbox directory override; empty = default\n", c.Serve.BusWorker.MailDir)
	} else {
		b.WriteString("# mail_dir = \"\"   # empty = the default mailbox directory\n")
	}
	if c.Serve.BusWorker.ResultDir != "" {
		fmt.Fprintf(b, "result_dir = %q   # where result_ref JSON files are written; empty = <mail_dir>/bus-results\n", c.Serve.BusWorker.ResultDir)
	} else {
		b.WriteString("# result_dir = \"\"   # empty = <mail_dir>/bus-results\n")
	}
	b.WriteString("\n")
}

func renderTOMLKeyPart(key string) string {
	if isBareTOMLKey(key) {
		return key
	}
	return strconv.Quote(key)
}

func renderTOMLTablePath(parts ...string) string {
	rendered := make([]string, 0, len(parts))
	for _, part := range parts {
		rendered = append(rendered, renderTOMLKeyPart(part))
	}
	return strings.Join(rendered, ".")
}

func isBareTOMLKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// renderStringArray renders a []string as a TOML inline array.
// renderQuickCommandArray writes quick commands as a TOML inline array of inline tables.
// Inline because the keys are few and short, and because an array table would change which
// table the keys that follow it belong to.
func renderQuickCommandArray(entries []QuickCommandEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		fields := []string{fmt.Sprintf("title = %q", entry.Title), fmt.Sprintf("text = %q", entry.Text)}
		// Enabled is a pointer: absent means enabled. Only the false case is written, so a
		// config that never touched the switch keeps its original shape on save.
		if entry.Enabled != nil && !*entry.Enabled {
			fields = append(fields, "enabled = false")
		}
		parts = append(parts, "{"+strings.Join(fields, ", ")+"}")
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func renderStringArray(ss []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}

// renderStringMap renders a map[string]string as a TOML inline table with keys
// in sorted order so output is deterministic (round-trips cleanly).
func renderStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = %q", renderTOMLKeyPart(k), m[k])
	}
	b.WriteString(" }")
	return b.String()
}

func renderAnyMap(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if _, ok := renderAnyValue(v); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		value, _ := renderAnyValue(m[k])
		fmt.Fprintf(&b, "%s = %s", strconv.Quote(k), value)
	}
	b.WriteString(" }")
	return b.String()
}

func renderAnyValue(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return strconv.Quote(x), true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case int:
		return strconv.Itoa(x), true
	case int8:
		return strconv.FormatInt(int64(x), 10), true
	case int16:
		return strconv.FormatInt(int64(x), 10), true
	case int32:
		return strconv.FormatInt(int64(x), 10), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case uint:
		return strconv.FormatUint(uint64(x), 10), true
	case uint8:
		return strconv.FormatUint(uint64(x), 10), true
	case uint16:
		return strconv.FormatUint(uint64(x), 10), true
	case uint32:
		return strconv.FormatUint(uint64(x), 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	case float32:
		return formatFloat(float64(x)), true
	case float64:
		return formatFloat(x), true
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			part, ok := renderAnyValue(item)
			if !ok {
				return "", false
			}
			parts = append(parts, part)
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case []string:
		return renderStringArray(x), true
	case map[string]any:
		return renderAnyMap(x), true
	case map[string]string:
		return renderStringMap(x), true
	default:
		return "", false
	}
}

func renderModelOverrides(m map[string]ProviderModelOverride) string {
	keys := make([]string, 0, len(m))
	for k, ov := range m {
		if k == "" || modelOverrideEmpty(ov) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q = %s", k, renderModelOverride(m[k]))
	}
	b.WriteString(" }")
	return b.String()
}

func renderModelOverride(ov ProviderModelOverride) string {
	var parts []string
	if ov.ReasoningProtocol != "" {
		parts = append(parts, fmt.Sprintf("reasoning_protocol = %q", ov.ReasoningProtocol))
	}
	if len(ov.SupportedEfforts) > 0 {
		parts = append(parts, "supported_efforts = "+renderStringArray(ov.SupportedEfforts))
	}
	if ov.DefaultEffort != "" {
		parts = append(parts, fmt.Sprintf("default_effort = %q", ov.DefaultEffort))
	}
	if ov.Vision != nil {
		parts = append(parts, fmt.Sprintf("vision = %t", *ov.Vision))
	}
	if ov.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("context_window = %d", ov.ContextWindow))
	}
	if ov.MaxOutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("max_output_tokens = %d", ov.MaxOutputTokens))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func modelOverrideEmpty(ov ProviderModelOverride) bool {
	return ov.ReasoningProtocol == "" && len(ov.SupportedEfforts) == 0 && ov.DefaultEffort == "" && ov.Vision == nil && ov.ContextWindow <= 0 && ov.MaxOutputTokens == 0
}

func hasPositiveIntMap(m map[string]int) bool {
	for k, v := range m {
		if strings.TrimSpace(k) != "" && v > 0 {
			return true
		}
	}
	return false
}

// renderIntMap renders a map[string]int as a TOML inline table with positive
// values only, preserving deterministic key order.
func renderIntMap(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if strings.TrimSpace(k) != "" && v > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{ ")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q = %d", k, m[k])
	}
	b.WriteString(" }")
	return b.String()
}

func renderBotCredential(cred BotConnectionCredential) string {
	parts := make(map[string]string)
	if cred.AppID != "" {
		parts["app_id"] = cred.AppID
	}
	if cred.AppSecretEnv != "" {
		parts["app_secret_env"] = cred.AppSecretEnv
	}
	if cred.AccountID != "" {
		parts["account_id"] = cred.AccountID
	}
	if cred.TokenEnv != "" {
		parts["token_env"] = cred.TokenEnv
	}
	if len(parts) == 0 {
		return ""
	}
	return renderStringMap(parts)
}

func renderBotAccess(access BotAccessConfig) string {
	hasList := len(access.Users) > 0 || len(access.Groups) > 0 || len(access.Approvers) > 0 || len(access.Admins) > 0
	if !access.Enabled && !access.AllowAll && !access.PairingEnabled && !hasList {
		return ""
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("enabled = %v", access.Enabled))
	parts = append(parts, fmt.Sprintf("allow_all = %v", access.AllowAll))
	parts = append(parts, fmt.Sprintf("pairing_enabled = %v", access.PairingEnabled))
	if len(access.Users) > 0 {
		parts = append(parts, "users = "+renderStringArray(access.Users))
	}
	if len(access.Groups) > 0 {
		parts = append(parts, "groups = "+renderStringArray(access.Groups))
	}
	if len(access.Approvers) > 0 {
		parts = append(parts, "approvers = "+renderStringArray(access.Approvers))
	}
	if len(access.Admins) > 0 {
		parts = append(parts, "admins = "+renderStringArray(access.Admins))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func renderBotSessionMappings(mappings []BotConnectionSessionMapping) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, mapping := range mappings {
		if i > 0 {
			b.WriteString(", ")
		}
		parts := map[string]string{
			"remote_id":  mapping.RemoteID,
			"session_id": mapping.SessionID,
		}
		if mapping.SessionSource != "" {
			parts["session_source"] = mapping.SessionSource
		}
		if mapping.ChatType != "" {
			parts["chat_type"] = mapping.ChatType
		}
		if mapping.UserID != "" {
			parts["user_id"] = mapping.UserID
		}
		if mapping.ThreadID != "" {
			parts["thread_id"] = mapping.ThreadID
		}
		if mapping.Scope != "" {
			parts["scope"] = mapping.Scope
		}
		if mapping.WorkspaceRoot != "" {
			parts["workspace_root"] = mapping.WorkspaceRoot
		}
		if mapping.UpdatedAt != "" {
			parts["updated_at"] = mapping.UpdatedAt
		}
		b.WriteString(renderStringMap(parts))
	}
	b.WriteByte(']')
	return b.String()
}

func renderBotRoute(b *strings.Builder, route BotRouteConfig) {
	if strings.TrimSpace(route.ConnectionID) != "" {
		fmt.Fprintf(b, "connection_id = %q\n", strings.TrimSpace(route.ConnectionID))
	}
	if strings.TrimSpace(route.Platform) != "" {
		fmt.Fprintf(b, "platform = %q\n", strings.TrimSpace(route.Platform))
	}
	if strings.TrimSpace(route.ChatType) != "" {
		fmt.Fprintf(b, "chat_type = %q\n", strings.TrimSpace(route.ChatType))
	}
	if strings.TrimSpace(route.ChatID) != "" {
		fmt.Fprintf(b, "chat_id = %q\n", strings.TrimSpace(route.ChatID))
	}
	if strings.TrimSpace(route.UserID) != "" {
		fmt.Fprintf(b, "user_id = %q\n", strings.TrimSpace(route.UserID))
	}
	if strings.TrimSpace(route.ThreadID) != "" {
		fmt.Fprintf(b, "thread_id = %q\n", strings.TrimSpace(route.ThreadID))
	}
	if strings.TrimSpace(route.Model) != "" {
		fmt.Fprintf(b, "model = %q\n", strings.TrimSpace(route.Model))
	}
	if strings.TrimSpace(route.ToolApprovalMode) != "" {
		fmt.Fprintf(b, "tool_approval_mode = %q\n", strings.TrimSpace(route.ToolApprovalMode))
	}
	if strings.TrimSpace(route.WorkspaceRoot) != "" {
		fmt.Fprintf(b, "workspace_root = %q\n", strings.TrimSpace(route.WorkspaceRoot))
	}
}

func renderBotDesktopWatcher(b *strings.Builder, watcher BotDesktopWatcherConfig) {
	if strings.TrimSpace(watcher.Platform) != "" {
		fmt.Fprintf(b, "platform = %q\n", strings.TrimSpace(watcher.Platform))
	}
	if strings.TrimSpace(watcher.ConnectionID) != "" {
		fmt.Fprintf(b, "connection_id = %q\n", strings.TrimSpace(watcher.ConnectionID))
	}
	if strings.TrimSpace(watcher.Domain) != "" {
		fmt.Fprintf(b, "domain = %q\n", strings.TrimSpace(watcher.Domain))
	}
	if strings.TrimSpace(watcher.ChatType) != "" {
		fmt.Fprintf(b, "chat_type = %q\n", strings.TrimSpace(watcher.ChatType))
	}
	if strings.TrimSpace(watcher.ChatID) != "" {
		fmt.Fprintf(b, "chat_id = %q\n", strings.TrimSpace(watcher.ChatID))
	}
}

// renderRuleList emits a permission rule list. A populated list renders as an
// active TOML array; an empty one renders as a commented example so `reasonix setup`
// scaffolds discoverable guidance without imposing surprising rules.
func renderRuleList(key string, rules []string, example string) string {
	if len(rules) == 0 {
		return fmt.Sprintf("# %s = %s\n", key, example)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s = [", key)
	for i, r := range rules {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", r)
	}
	b.WriteString("]\n")
	return b.String()
}

// formatFloat ensures a float renders with a decimal point so TOML types it as a
// float, not an integer (e.g. 0 -> "0.0").
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
