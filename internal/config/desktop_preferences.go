package config

import (
	"fmt"
	"strings"
)

// DesktopConfig controls desktop-only UI preferences. It is intentionally
// separate from top-level language and [ui] so desktop choices do not affect CLI
// language, terminal colours, or provider-visible prompt/request data.
type DesktopConfig struct {
	Language                  string   `toml:"language"`                     // auto|en|zh; empty/auto = browser/OS auto-detect
	Currency                  string   `toml:"currency"`                     // legacy display currency; migrated to [billing].display_currency
	LayoutStyle               string   `toml:"layout_style"`                 // workbench|creation; legacy classic is migrated on startup
	Theme                     string   `toml:"theme"`                        // auto|dark|light; empty resolves to auto
	ThemeStyle                string   `toml:"theme_style"`                  // graphite|aurora|slate|carbon|nocturne|amber and legacy aliases
	TerminalTheme             string   `toml:"terminal_theme"`               // auto|dark|light; auto follows the desktop app theme
	ExternalOpener            string   `toml:"external_opener"`              // preferred installed app used by the desktop Open control
	CloseBehavior             string   `toml:"close_behavior"`               // quit|background; desktop window close behavior
	DisplayMode               string   `toml:"display_mode"`                 // standard|compact (legacy "minimal" maps to compact); transcript display mode
	StatusBarStyle            string   `toml:"status_bar_style"`             // icon|text; desktop status bar metric labels
	StatusBarStyleInitialized bool     `toml:"status_bar_style_initialized"` // one-time icon default upgrade; later choices are user-owned
	StatusBarItems            []string `toml:"status_bar_items"`             // ordered visible desktop status bar items
	DefaultToolApprovalMode   string   `toml:"default_tool_approval_mode"`   // ask|auto|yolo; defaults to auto for newly-created desktop sessions
	// Autopilot defaults for newly-created desktop sessions. Autopilot runs a
	// session unattended: the goal machine bounds it by wall clock, and the reviewer
	// answers approval prompts nobody is there to answer.
	Autopilot bool `toml:"autopilot"`
	// Task 381: configurable fast-switch staging directory (task 81 family).
	// Empty = the historical default (<installRoot>/staging). Behavior config,
	// not a new capability — no experimental_ prefix.
	StagingDir string `toml:"staging_dir"`
	// ExperimentalRestartUpdate exposes the "restart and update" action (task 81).
	// It ships off: the action swaps the active install version, so it stays behind an
	// explicit opt-in until it has been exercised in the field.
	ExperimentalRestartUpdate bool `toml:"experimental_restart_update"`
	// ExperimentalAutonomousUpdate exposes the agent-facing "autonomous update"
	// surface (task 254): with it on, the restart_update tool is registered so the
	// model can list versions, set an update target, and execute the swap itself.
	// It ships off (fork rule 2) and, like every tool registration, reads the boot
	// snapshot — a flip applies on the next restart.
	ExperimentalAutonomousUpdate bool `toml:"experimental_autonomous_update"`
	// ExperimentalLifecycleNoiseGate is the task-377 noise triage: when on,
	// startup skips the crash-pending report for per-process lifecycle records
	// whose phase proves a clean shutdown was already underway
	// (shutting_down/healthy — the clean()/exit race C-20260920-02 documented,
	// not crash evidence). Off by default (铁律 2): the zero value keeps every
	// dead-process record reporting exactly as before. wedged and unknown
	// phases always report — they are real abnormal exits (task 377 定性).
	ExperimentalLifecycleNoiseGate bool `toml:"experimental_lifecycle_noise_gate"`
	// AutonomousUpdateResume scopes the auto-resume after an autonomous-update
	// restart (task 254, user ruling): "off" resumes nothing, "goal_autopilot"
	// (the default; empty or unknown values normalize to it) resumes sessions
	// whose goal was running plus autopilot sessions that asked for the update,
	// and "all" additionally resumes every session that was mid-turn at the
	// restart. The task-49 goal resume rides the same dial: "off" turns the
	// whole auto-resume family off, which is what a conservative user opting
	// out is asking for.
	AutonomousUpdateResume string `toml:"autonomous_update_resume"`
	// Task 388: autopilot proxy-approval scope — "related" (default; empty or
	// unknown reads as it) approves only requests serving this session's own
	// task, "all" also proxy-approves loosely-related requests from other
	// sessions. Behavior dial, not a capability switch.
	AutopilotProxyScope string `toml:"autopilot_proxy_scope"`
	// Task 388: natural-language allow/deny manifest for the proxy reviewer.
	// Absent path = model self-judgment with no manifest section.
	AutopilotProxyManifest string `toml:"autopilot_proxy_manifest"`
	// UpdateChime plays a short sound on the first launch after an update swaps
	// versions (task 277). Opt-in (fork rule 2); the frontend gates one-shot
	// playback by the last-chimed version, so an off switch is zero-behaviour.
	UpdateChime bool `toml:"update_chime"`
	// UpdateChimeTune picks the update-chime melody (task 512): "nokia" or
	// "mario". Reads via UpdateChimeTuneMode, which normalizes empty/unknown
	// values to "nokia"; the frontend additionally falls back from "mario" to
	// "nokia" in public builds, where the Nintendo-owned asset is compiled out.
	UpdateChimeTune string `toml:"update_chime_tune"`
	// ExperimentalSessionMonitor exposes the left-rail "session monitor" board
	// (task 123). It ships off: the board is a diagnostics surface for cache
	// residency and switch cost, so it stays behind an explicit opt-in.
	ExperimentalSessionMonitor bool `toml:"experimental_session_monitor"`
	// ExperimentalSplitView exposes the tab-bar "split view" action (task 70-1). It
	// ships off: with it off the tab context menu looks exactly as it did before the
	// split existed (zero regression), and the split stays an opt-in experiment.
	ExperimentalSplitView bool `toml:"experimental_split_view"`
	// ExperimentalPerfMonitor is the settings-view mirror for
	// Agent.ExperimentalPerfMonitor (task 184).
	ExperimentalPerfMonitor bool `toml:"experimental_perf_monitor"`
	// ExperimentalHeapHighProfile is the settings-view mirror for
	// Agent.ExperimentalHeapHighProfile (task 501).
	ExperimentalHeapHighProfile bool `toml:"experimental_heap_high_profile"`
	// PerfMonitorIntervalSeconds is the settings-view mirror for the sampler
	// interval (task 184). 0 = default 5s.
	PerfMonitorIntervalSeconds int `toml:"perf_monitor_interval_seconds"`
	// ExperimentalFeedback exposes the agent submit_feedback tool and the desktop
	// "意见箱" panel (task 121). It ships off: feedback is a local inbox, not a
	// product surface, so both the tool and the viewer stay behind an opt-in.
	ExperimentalFeedback bool `toml:"experimental_feedback"`
	// ExperimentalFeedbackNudge is the feedback touchpoint dial (task 172): with
	// it on, the host appends one feedback-inbox invitation after a completed
	// turn (T1) and one short guidance note after a mid-turn user steer (T2).
	// It ships off (铁律 2): every T1 trigger costs one extra model round, and
	// it only means anything with the feedback feature itself on, so the
	// runtime gate ANDs it with ExperimentalFeedback (FeedbackNudgeEnabled).
	ExperimentalFeedbackNudge bool `toml:"experimental_feedback_nudge"`
	// ExperimentalLocalServer exposes the Settings → 本地服务 page and its
	// serve-pool gateway controls (task 130). It ships off: the gateway binds
	// 0.0.0.0, so it stays behind an explicit opt-in.
	ExperimentalLocalServer bool `toml:"experimental_local_server"`
	// ExperimentalParallelFullAccess trusts product-managed worktree roots as
	// write surfaces for the parent session and sub-agent write_paths (task 127).
	// It ships off so production confinement is unchanged. Env
	// REASONIX_PARALLEL_FULL_ACCESS=1 is a process-local override.
	ExperimentalParallelFullAccess bool `toml:"experimental_parallel_full_access"`
	// ExperimentalTodoSidebar moves the live todo list into the right dock as a
	// fifth tab (task 259). It ships off: with it off the todo list stays pinned
	// above the composer and the dock tabs look exactly as they did before, and
	// the extra tab plus the tab-visibility and wrap settings stay opt-in. The
	// flag is snapshotted at boot, so changes take effect after a restart.
	ExperimentalTodoSidebar bool `toml:"experimental_todo_sidebar"`
	// ExperimentalSubagentPanel gates the subagent panel package (task 495):
	// a persistent "子代理" right-dock tab (running/ended sections, 20-per-page
	// ended list) plus default-collapsed transcript cards for ended subagents
	// in deep mode. It ships off: with it off ended subagent cards keep the
	// deep-mode expanded default and the dock has no extra tab. The flag is
	// snapshotted at boot, so changes take effect after a restart.
	ExperimentalSubagentPanel bool `toml:"experimental_subagent_panel"`
	// ExperimentalPromptHistoryPicker gates the composer history-navigation
	// safety rework (task 261, upstream #10425): the clock-icon history picker
	// plus the narrowed plain-ArrowUp trigger (up enters history only from an
	// empty composer or while already browsing). It ships off: with it off the
	// composer keeps the exact legacy ArrowUp/ArrowDown behaviour. The flag is
	// snapshotted at boot, so changes take effect after a restart.
	ExperimentalPromptHistoryPicker bool `toml:"experimental_prompt_history_picker"`
	// ExperimentalSessionWall gates the session graph wall (task 505): a
	// "跳转会话" command-palette entry that opens a grid wall of session cards
	// (dozens at once, grouped by project or recent activity, searchable).
	// It ships off (铁律 2): with it off the palette keeps the exact legacy
	// item list (recent-sessions slice unchanged, no extra entry). The flag is
	// snapshotted at boot, so changes take effect after a restart.
	ExperimentalSessionWall bool `toml:"experimental_session_wall"`
	// ExperimentalTabCompress gates the tab-strip adaptive compression (task
	// 506): once more than 8 tabs are open the tab width steps down in tiers
	// (9→148px, 13→122px, 17→100px with mode badges hidden, ≥21→84px floor) so
	// the strip keeps fitting instead of scrolling, and hover keeps carrying
	// the full annotated title. It ships off: with it off every tab keeps the
	// exact fixed width (176px, 148px on narrow windows). Pure frontend gate:
	// the settings save re-applies the boot snapshot, so a change is visible
	// without a restart.
	ExperimentalTabCompress bool `toml:"experimental_tab_compress"`
	// ExperimentalSubagentDetail gates the subagent detail view (task 507):
	// with it off (default, plan C) a dock row click keeps the legacy inline
	// preview expansion plus the widen/narrow dock-width affordance; with it
	// on (plan A) a row click switches the dock to a read-only detail view
	// with a back button — no input surface, width still follows the persisted
	// dock width. It ships off (铁律 2). Pure frontend gate: the settings save
	// re-applies the boot snapshot, so a change is visible without a restart.
	ExperimentalSubagentDetail bool `toml:"experimental_subagent_detail"`
	// Task 265 (lab intake): three render-surface features ship ON via
	// nil-means-on pointers — existing behaviour getting an off switch, so the
	// default must not regress anyone. Each is a pure frontend gate.
	// ExperimentalQuestionSearch keeps the topic-bar "search my questions"
	// button and panel (jump-to-turn, not re-ask). Off hides the entry.
	ExperimentalQuestionSearch *bool `toml:"experimental_question_search"`
	// ExperimentalSubagentTps keeps the ~N tok/s heartbeat on sub-agent tool
	// cards and the status-bar job table. Off hides the readouts; the backend
	// progress events are unchanged.
	ExperimentalSubagentTps *bool `toml:"experimental_subagent_tps"`
	// ExperimentalCompletionSummary keeps the per-turn "本轮结果" notice on the
	// desktop transcript. Off silences the notice; the dock entry points and
	// the CLI receipt card are unaffected.
	ExperimentalCompletionSummary *bool `toml:"experimental_completion_summary"`
	// ExperimentalPathRules enables the structured path-scope evaluation order
	// documented in docs/PATH_SCOPE_RULES.md (task 134). Ships off: production
	// keeps the existing confine + allow_write + write-access approval model.
	ExperimentalPathRules bool `toml:"experimental_path_rules"`
	// ExperimentalTraceAsState is the settings-view mirror for Agent.TraceAsState
	// (task 60). The runtime flag lives on [agent]; this field keeps the
	// experimental features tab reading the same saved value.
	ExperimentalTraceAsState bool `toml:"experimental_trace_as_state"`
	// ExperimentalDream is the settings-view mirror for Agent.ExperimentalDream
	// (task 115). The runtime flag lives on [agent]; this field keeps the
	// experimental features tab reading the same saved value.
	ExperimentalDream bool `toml:"experimental_dream"`
	// ExperimentalSessionCollab is the settings-view mirror for
	// Agent.ExperimentalSessionCollab (task 19).
	ExperimentalSessionCollab bool `toml:"experimental_session_collab"`
	// SessionCollabHopLimit caps how many hops a cross-session chain may take
	// (task 204). 0 keeps the package default (5); values are clamped into
	// [MinHop, MaxHopCeiling] on write, so a stored value is always legal.
	SessionCollabHopLimit int `toml:"session_collab_hop_limit"`
	// DetachedIdleReleaseMinutes is the settings-view mirror for the [agent]
	// runtime value of task 308-O4 (idle threshold in minutes, 0 = never
	// release).
	DetachedIdleReleaseMinutes int `toml:"detached_idle_release_minutes"`
	// GoMemLimitMB is the settings-view mirror for the [agent] runtime value
	// of task 308-O3 (soft memory limit in MB, 0 = unbounded).
	GoMemLimitMB int `toml:"go_mem_limit_mb"`
	// ExperimentalAutoLoadOlder is the settings-view mirror for
	// Agent.ExperimentalAutoLoadOlder (fork task 160).
	ExperimentalAutoLoadOlder bool `toml:"experimental_auto_load_older"`

	// ExperimentalAutonomousIdleTerminate / ExperimentalLoopStreakNote /
	// ExperimentalEventWaitRecheck are the settings-view mirrors for the
	// [agent] runtime flags of task 244 B1/B2/B3 (same double-write pattern
	// as experimental_dream).
	ExperimentalAutonomousIdleTerminate bool `toml:"experimental_autonomous_idle_terminate"`
	ExperimentalLoopStreakNote          bool `toml:"experimental_loop_streak_note"`
	ExperimentalEventWaitRecheck        bool `toml:"experimental_event_wait_recheck"`

	// ExperimentalOrphanHandling is the settings-view mirror for the [agent]
	// merged orphan switch (task 449; folds task 244 B5 lease reclaim + B4
	// recovery sweep into one).
	ExperimentalOrphanHandling bool `toml:"experimental_orphan_handling"`
	// Legacy task-244 keys: READ-ONLY, migrated into
	// experimental_orphan_handling at load (task 449), never written back.
	ExperimentalOrphanLeaseReclaim  bool `toml:"experimental_orphan_lease_reclaim"`
	ExperimentalRecoveryOrphanSweep bool `toml:"experimental_recovery_orphan_sweep"`

	// ExperimentalModelCapabilityFilter is the settings-view mirror for the
	// [agent] runtime flag of task 244 B9.
	ExperimentalModelCapabilityFilter bool `toml:"experimental_model_capability_filter"`
	// ExperimentalRuntimeReuse is the settings-view mirror for the [agent]
	// runtime flag of task 363A (runtime assembly reuse pool).
	ExperimentalRuntimeReuse bool `toml:"experimental_runtime_reuse"`
	// CollabInboxMerge is the settings-view mirror for Agent.CollabInboxMerge
	// (task 221): off | same_sender | all.
	CollabInboxMerge string `toml:"collab_inbox_merge"`
	// CollabGuidanceMerge is the settings-view mirror for
	// Agent.CollabGuidanceMerge (task 153): the manual "merge next" button in
	// the guidance shelf.
	CollabGuidanceMerge    bool   `toml:"collab_guidance_merge"`
	AutopilotMaxRuntime    string `toml:"autopilot_max_runtime"`    // Go duration; required when autopilot is on
	AutopilotApprovalGrace string `toml:"autopilot_approval_grace"` // wait for a human before the reviewer decides; empty = 15s
	// ExperimentalAutopilotAskTimeout is the task-477 ask-timeout sub-option
	// (user ruling 2026-10-05, 铁律 2 default off): when on AND the run is
	// autopilot, a high-risk `ask` that sits unanswered for
	// AutopilotAskWaitSeconds is answered with an explicit refusal and the run
	// continues (the goal loop keeps going), instead of the task-109 B4
	// terminal stop. Off keeps that stop byte-for-byte; attended sessions are
	// never touched by either state.
	ExperimentalAutopilotAskTimeout bool `toml:"experimental_autopilot_ask_timeout"`
	// AutopilotAskWaitSeconds is the ask-timeout sub-option's wait in seconds.
	// 0 = the built-in default (15s); explicit values are clamped into
	// 1..3600 by the reader and refused outside that range by the setter, so
	// a hand-edited config can neither spin the timeout down to zero nor
	// stretch it past an hour.
	AutopilotAskWaitSeconds int `toml:"autopilot_ask_wait_seconds"`
	// AutopilotGuardInterval is the autopilot guard task's default run interval
	// in minutes (task 326). 0 = autopilotGuardDefaultIntervalMinutes. Changing
	// it re-points the interval of guards that already exist instead of
	// recreating them, so a session never grows a second guard.
	AutopilotGuardInterval int `toml:"autopilot_guard_interval"`
	// AutopilotGuardQuiescent picks what happens to the guard when the session
	// it watches has been quiet with no new work (task 326):
	// disable (default) | standby | destroy.
	AutopilotGuardQuiescent string `toml:"autopilot_guard_quiescent"`
	// MaxCachedTabs bounds how many tab states the frontend keeps resident
	// (task 161). Under the workbench single-surface layout a switch used to
	// prune every other tab's cached state, so each switch back re-parsed the
	// full transcript (measured 6.8 s on a 243-turn session). 0 = unlimited.
	MaxCachedTabs int `toml:"max_cached_tabs"`
	// HistoryBodyBudgetMb / MarkdownBudgetMb override the transcript resource
	// budgets (task 161, user 2026-09-17). Defaults 192/256 MiB match
	// resourceBudgets.ts; 0 keeps the default. Applied on startup only.
	HistoryBodyBudgetMb int `toml:"history_body_budget_mb"`
	MarkdownBudgetMb    int `toml:"markdown_budget_mb"`
	// ExperimentalCacheTuning exposes the Settings → 缓存大小调整 controls
	// (task 161). Ships off: budget mis-tuning degrades switch latency and
	// memory in ways that are hard to diagnose remotely.
	ExperimentalCacheTuning bool  `toml:"experimental_cache_tuning"`
	CheckUpdates            *bool `toml:"check_updates"` // startup update checks; nil keeps the default enabled
	// UpdateChannel is a legacy compatibility field. It is accepted on read but
	// ignored and omitted from future canonical writes.
	UpdateChannel        string   `toml:"update_channel"`
	Telemetry            *bool    `toml:"telemetry"`          // anonymous launch ping plus scrubbed next-launch native crash diagnostics; nil keeps the default enabled
	Metrics              *bool    `toml:"metrics"`            // aggregate desktop metrics (anonymous signal/bucket counts, including lifecycle health; no content); nil keeps the default enabled
	ProviderAccess       []string `toml:"provider_access"`    // desktop-only list of provider entries shown in Settings > Model > Access
	SessionExperience    string   `toml:"session_experience"` // standard|deep; canonical desktop transcript experience
	ExpandThinking       bool     `toml:"expand_thinking"`    // deprecated compatibility alias: true maps to auto
	ReasoningDisplayMode string   `toml:"reasoning_display_mode"`
	ConversationWidth    string   `toml:"conversation_width"` // standard|full; max transcript width; empty = standard
	// QuickCommands are user-defined snippets offered by the desktop composer's
	// + menu (task 18). Array order is the display order; entries with an empty
	// Title or Text are dropped on save.
	QuickCommands []QuickCommandEntry `toml:"quick_commands"`
	// ExperimentalQuickCommands gates the whole quick-commands surface (task
	// 262). It ships off (new-capability rule): with it off the composer menu,
	// the general-page entry and the lab pane all hide, and nothing about the
	// stored snippets changes.
	ExperimentalQuickCommands bool `toml:"experimental_quick_commands"`
	// ExperimentalCDPDebugPort is the task-342 lab switch for a WebView2 CDP
	// (Chrome DevTools Protocol) debug endpoint. It ships OFF (铁律 2): with
	// the zero value the desktop binary is byte-for-byte behaviour-identical
	// to a build without the feature — no extra browser argument, no port,
	// nothing to connect to. The WebView2 environment is created once at
	// startup, so a flip applies on the next restart (the settings card says
	// so). When on, the browser gets `--remote-debugging-port=0` pinned to
	// loopback only: Chromium then picks a free ephemeral port (random, so a
	// fixed 9222 collision is impossible) and binds it to 127.0.0.1, which is
	// not reachable from other machines. The resolved port is written to
	// <REASONIX_HOME>/logs/desktop/cdp-endpoint.txt for tooling to read.
	ExperimentalCDPDebugPort bool `toml:"experimental_cdp_debug_port"`
	// ExperimentalOutputStyleUI gates the lab's 「回答风格」 section (task 385a).
	// It ships OFF (铁律 2): with the zero value the desktop exposes no
	// output-style UI at all. It gates the UI surface only — prompt injection
	// keeps reading [agent] output_style, so a hand-written toml entry still
	// applies without this switch.
	ExperimentalOutputStyleUI bool `toml:"experimental_output_style_ui"`
	// ExperimentalZcodeTaskBus is the task-439 lab switch for the built-in
	// zcode task bus. It ships OFF (铁律 2): with the zero value the desktop
	// starts no listener at all and behaves byte-for-byte like a build
	// without the feature — the pre-439 workflow (an externally launched
	// `reasonix serve` kept alive by a startup-folder vbs) stays the only way
	// the bus runs. When ON, the desktop hosts the bus MCP endpoint itself
	// (127.0.0.1:8787, an embedded goroutine instead of the external
	// process), mounting the same routes as [serve.bus_mcp] and reading the
	// same role table, so `reasonix bus enroll` output keeps working
	// unchanged. The listener is armed at boot: a flip applies on the next
	// restart, and the lab card says so.
	ExperimentalZcodeTaskBus bool `toml:"experimental_zcode_task_bus"`
}

// DesktopQuestionSearchEnabled reports whether the topic-bar question-search
// entry renders (task 265). Nil means on: the entry predates its switch.
func (c *Config) DesktopQuestionSearchEnabled() bool {
	return c == nil || c.Desktop.ExperimentalQuestionSearch == nil || *c.Desktop.ExperimentalQuestionSearch
}

// DesktopSubagentTpsEnabled reports whether sub-agent tok/s readouts render
// (task 265). Nil means on.
func (c *Config) DesktopSubagentTpsEnabled() bool {
	return c == nil || c.Desktop.ExperimentalSubagentTps == nil || *c.Desktop.ExperimentalSubagentTps
}

// DesktopCompletionSummaryEnabled reports whether the per-turn result notice
// renders on the desktop transcript (task 265). Nil means on.
func (c *Config) DesktopCompletionSummaryEnabled() bool {
	return c == nil || c.Desktop.ExperimentalCompletionSummary == nil || *c.Desktop.ExperimentalCompletionSummary
}

// FeedbackNudgeEnabled reports whether the host may append feedback-inbox
// nudges (task 172). The parent ExperimentalFeedback switch wins: with the
// feedback feature off the nudge dial reads as off no matter how it is set,
// so a stray sub-switch can never resurrect the surface it lives under.
func (c *Config) FeedbackNudgeEnabled() bool {
	return c != nil && c.Desktop.ExperimentalFeedback && c.Desktop.ExperimentalFeedbackNudge
}

// QuickCommandEntry is one quick-command snippet. Title is the menu label, Text
// is inserted into the composer verbatim (the user still presses send).
type QuickCommandEntry struct {
	Title string `toml:"title" json:"title"`
	Text  string `toml:"text" json:"text"`
	// Enabled is a pointer so configs written before the switch existed keep
	// working: absent means enabled, and only an explicit false hides a snippet.
	Enabled *bool `toml:"enabled" json:"enabled,omitempty"`
}

// QuickCommandLimits bounds what the settings UI may store, so a runaway paste
// cannot bloat the user config or the menu.
const (
	QuickCommandMaxEntries  = 50
	QuickCommandMaxTitle    = 60
	QuickCommandMaxTextSize = 8 * 1024
)

// DesktopExternalOpener returns the selected opener id; unavailable ids fall
// back to the platform file manager in the desktop shell.
func (c *Config) DesktopExternalOpener() string {
	if c == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(c.Desktop.ExternalOpener))
}

// SetQuickCommands replaces the snippet list after validation. Blank entries are
// dropped; oversized entries are rejected so the failure is visible in the UI
// rather than silently truncated.
func (c *Config) SetQuickCommands(entries []QuickCommandEntry) error {
	if c == nil {
		return nil
	}
	if len(entries) > QuickCommandMaxEntries {
		return fmt.Errorf("too many quick commands: %d (max %d)", len(entries), QuickCommandMaxEntries)
	}
	out := make([]QuickCommandEntry, 0, len(entries))
	for _, entry := range entries {
		title := strings.TrimSpace(entry.Title)
		text := entry.Text
		if title == "" && strings.TrimSpace(text) == "" {
			continue
		}
		if title == "" {
			return fmt.Errorf("quick command text %q needs a title", firstLine(text))
		}
		if len([]rune(title)) > QuickCommandMaxTitle {
			return fmt.Errorf("quick command title %q is too long (max %d characters)", title, QuickCommandMaxTitle)
		}
		if len(text) > QuickCommandMaxTextSize {
			return fmt.Errorf("quick command %q is too large (%d bytes, max %d)", title, len(text), QuickCommandMaxTextSize)
		}
		out = append(out, QuickCommandEntry{Title: title, Text: text, Enabled: entry.Enabled})
	}
	c.Desktop.QuickCommands = out
	return nil
}

// QuickCommands returns a copy so callers cannot mutate config state in place.
func (c *Config) DesktopQuickCommands() []QuickCommandEntry {
	if c == nil || len(c.Desktop.QuickCommands) == 0 {
		return nil
	}
	out := make([]QuickCommandEntry, len(c.Desktop.QuickCommands))
	copy(out, c.Desktop.QuickCommands)
	return out
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
