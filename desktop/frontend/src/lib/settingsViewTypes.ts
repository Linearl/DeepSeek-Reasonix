import type { ProviderPresetView } from "./providerCatalogTypes";
import type { AgentView, BotSettingsView, NetworkView, PermissionsView, ProviderView, SandboxView, SubagentPolicy, ToolApprovalMode } from "./types";

export interface SettingsView {
  modelSettingsFingerprint?: string;
  defaultModel: string;
  plannerModel: string;
  guardianModel: string;
  autopilot: boolean;
  autopilotMaxRuntime: string;
  autopilotApprovalGrace: string;
  /** Task 477: the experimental ask-timeout sub-option switch (default off). */
  experimentalAutopilotAskTimeout?: boolean;
  /** Task 477: the ask-timeout wait in seconds (effective value; unset = built-in 15). */
  autopilotAskWaitSeconds?: number;
  /** Task 326: the autopilot guard task's run interval, in minutes (effective value). */
  autopilotGuardInterval?: number;
  /** Task 326: what happens to the guard once the watched session goes quiet. */
  autopilotGuardQuiescent?: string;
  // Task 81: exposes the restart-and-update action. Same preference restart_and_update reads.
  experimentalRestartUpdate?: boolean;
  /** Task 381: fast-switch staging directory override; empty = the default. */
  stagingDir?: string;
  // Task 254: registers the agent-facing restart_update tool (boot snapshot).
  experimentalAutonomousUpdate?: boolean;
  // Task 254: auto-resume scope after an update restart (off | goal_autopilot | all).
  autonomousUpdateResume?: string;
  // Task 277: update-complete chime (first launch after a version swap).
  updateChime?: boolean;
  // Task 123: exposes the left-rail session monitor board (experimental).
  experimentalSessionMonitor?: boolean;
  // Task 70-1: exposes the tab-bar split view (experimental).
  experimentalSplitView?: boolean;
  // Conversation store mode in use (task 155): "v3_only" | "dual_write_read_v3" |
  // "dual_write_read_v4" | "v4_only"; switched from Settings > Experimental.
  sessionStorage?: string;
  // Mode this process actually started with, and whether the configured mode is
  // still waiting for a restart to take effect.
  sessionStorageEffective?: string;
  sessionStorageRestartPending?: boolean;
  // Task 333: event-log rotation gate (off | manual | auto) and its auto-mode
  // thresholds — read by the storage panel's detail card.
  eventsAutoRotation?: string;
  eventsRotationFactor?: number;
  eventsRotationCapMB?: number;
  // Task 121: exposes the agent submit_feedback tool and feedback inbox panel.
  experimentalFeedback?: boolean;
  // Task 172: feedback touchpoint dial (T1 completion + T2 steer); boot snapshot.
  experimentalFeedbackNudge?: boolean;
  // Task 259: exposes the right-dock todo tab plus tab visibility/wrap settings.
  experimentalTodoSidebar?: boolean;
  // Task 495: subagent panel package (dock tab + ended-card collapse).
  experimentalSubagentPanel?: boolean;
  // Task 505: session graph wall (palette 跳转会话 entry + grid wall).
  experimentalSessionWall?: boolean;
  // Task 261: composer history picker + narrowed ArrowUp trigger; boot snapshot.
  experimentalPromptHistoryPicker?: boolean;
  // Task 506: tab-strip adaptive compression (tiered width once >8 tabs).
  experimentalTabCompress?: boolean;
  // Task 507: subagent detail view (row click → read-only in-dock detail + back).
  experimentalSubagentDetail?: boolean;
  // Task 504: tab mode tint (low-opacity per-mode tab background instead of mode badges).
  experimentalTabModeTint?: boolean;
  // Task 265 lab intake: nil-means-on switches, resolved server-side.
  experimentalCompactionParallel?: boolean;
  experimentalContextBudget?: boolean;
  experimentalResearchBudget?: boolean;
  experimentalQuestionSearch?: boolean;
  experimentalSubagentPolicy?: boolean;
  experimentalSubagentTps?: boolean;
  experimentalCompletionSummary?: boolean;
  // Task 262: gates the whole quick-commands surface (ships off).
  experimentalQuickCommands?: boolean;
  // Task 342: WebView2 CDP debug endpoint (loopback-only random port; ships
  // off; the WebView2 browser args are read at startup — restart to apply).
  experimentalCDPDebugPort?: boolean;
  // Task 439: built-in zcode task bus (ships off; the listener arms at
  // desktop boot — restart to apply).
  experimentalZcodeTaskBus?: boolean;
  // Task 385a: lab 回答风格 gate (ships off) + the persisted [agent]
  // output_style the selector reads back ("" = default, no style).
  experimentalOutputStyleUI?: boolean;
  outputStyle?: string;
  // Task 231: managed-path pre-approval — master switch + four checkboxes
  // (all ship off; the bypass only arms under autopilot).
  experimentalPreapproveManagedPaths?: boolean;
  preapproveSkills?: boolean;
  preapproveHooks?: boolean;
  preapproveSessionStores?: boolean;
  preapproveBashEscape?: boolean;
  // Task 192: active-tab residency policy (ships off; keeps the running tab resident across switches).
  experimentalActiveTabResident?: boolean;
  // Task 163: OpenCode Go subscription usage card (ships off; no query while off).
  experimentalOpenCodeGoUsage?: boolean;
  // Task 257: full access (yolo) — all declared write dirs pass, bash unwrapped (ships off; restart to apply).
  experimentalFullAccess?: boolean;
  // S1: resident-base-subprocess switch (design 2026-09-30 §7 R4; ships off —
  // the pure-inline baseline; restart to apply).
  experimentalBaseProcess?: boolean;
  // Task 377: crash-report lifecycle noise triage (boot snapshot; restart to apply).
  experimentalLifecycleNoiseGate?: boolean;
  // Task 130: exposes the Settings → 本地服务 page and serve-pool controls.
  experimentalLocalServer?: boolean;
  // Task 134: structured path-scope evaluation (docs/PATH_SCOPE_RULES.md).
  experimentalPathRules?: boolean;
  // Task 161: transcript cache tuning (experimental; user values ignored while off).
  maxCachedTabs?: number;
  // Task 347: effective replayed-graph cache LRU capacity (task 196fix2;
  // 0-in-file reports as the built-in 3, range 1-16).
  dagGraphCacheCapacity?: number;
  historyBodyBudgetMb?: number;
  markdownBudgetMb?: number;
  experimentalCacheTuning?: boolean;
  // Task 60: Trace-as-State compaction experiment.
  experimentalTraceAsState?: boolean;
  // Task 115: dream/distill memory-curation experiment.
  experimentalDream?: boolean;
  // Task 184: host performance monitor — 5s samples of memory/IO/key files plus
  // periodic heap profiles. Off by default.
  experimentalPerfMonitor?: boolean;
  perfMonitorIntervalSeconds?: number;
  // Task 501: threshold-triggered heap snapshot (experimental, default off).
  experimentalHeapHighProfile?: boolean;
  // Task 204: cross-session chain ceiling (3..1000, default 5).
  sessionCollabHopLimit?: number;
  // Task 308-O4: detached idle runtime release threshold (minutes; 0 = never).
  detachedIdleReleaseMinutes?: number;
  // Task 308-O3: soft memory limit in MB (0 = unbounded).
  goMemLimitMB?: number;
  // Fork task 160: scroll-driven "load older" trigger at the transcript top.
  experimentalAutoLoadOlder?: boolean;
  // Task 221: inbox drain merge tri-state (off | same_sender | all).
  collabInboxMerge?: string;
  // Task 153: guidance shelf manual "merge next" button.
  collabGuidanceMerge?: boolean;

  // Task 173: the collaboration panel gates (settings -> 实验特性 -> 跨会话通信).
  sessionCollabAllowDelete?: boolean;
  sessionCollabAllowRequireReply?: boolean;
  sessionCollabAllowReadTail?: boolean;
  sessionCollabAllowCreate?: boolean;
  sessionCollabAllowSteer?: boolean;
  sessionCollabBackground?: boolean;
  sessionCollabDailySendLimit?: number;
  // Task 530: the turn-closure reply reminder dial (boot snapshot).
  sessionCollabReplyNudge?: boolean;
  // Task 309: mailbox defaults for talk_to_session.
  sessionCollabMailIdempotentDefault?: boolean;
  sessionCollabMailReceiptDefault?: boolean;
  sessionCollabDefaultDelivery?: string;
  // Task 225: cascade approval to the autopilot parent.
  experimentalCascadeApproval?: boolean;
  // Task 242: quota fallback switch + provider/model target.
  experimentalFallbackModel?: boolean;
  fallbackModel?: string;
  // Task 318: lab internal optimizations (three switches default off).
  experimentalHighSpeedModel?: boolean;
  experimentalProactiveCompact?: boolean;
  proactiveCompactCooldownMinutes?: number;
  experimentalComposerDraft?: boolean;
  /** Task 369: selection quick-actions floating card (translate/explain). */
  experimentalSelectionActions?: boolean;
  // Task 297: cold-cache compact pass (lab storage cost card).
  experimentalColdCacheCompact?: boolean;
  coldCacheCompactMinBytes?: number;
  coldCacheCompactIdleMinutes?: number;
  // Task 19: multi-session collaboration experiment.
  experimentalSessionCollab?: boolean;
  // Task 244 B1: heartbeat idle-streak burn guard.
  experimentalAutonomousIdleTerminate?: boolean;
  // Task 244 B2: bounded neutral Continue. note on repeated text loops.
  experimentalLoopStreakNote?: boolean;
  // Task 244 B3: event_wait return-time recheck.
  experimentalEventWaitRecheck?: boolean;
  // Task 449: merged orphan switch (folds task 244 B5 lease reclaim + B4
  // recovery sweep into one key).
  experimentalOrphanHandling?: boolean;
  // Task 244 B9: per-task model capability filter.
  experimentalModelCapabilityFilter?: boolean;
  // Task 363A: runtime assembly reuse pool (same root+model+effort tabs).
  experimentalRuntimeReuse?: boolean;
  visionModel: string;
  webSearchModel?: string;
  webSearchModels?: string[];
  webSearchModelStatus?: string;
  webSearchModelReason?: string;
  effectiveWebSearchModel?: string;
  webSearchModelOverridden?: boolean;
  subagentModel: string;
  subagentEffort: string;
  autoPlan: string;
  providers: ProviderView[];
  officialProviders: ProviderView[];
  providerPresets: ProviderPresetView[];
  permissions: PermissionsView;
  sandbox: SandboxView;
  network: NetworkView;
  agent: AgentView;
  bot: BotSettingsView;
  desktopLanguage: string; // "" | "en" | "zh"; empty = auto
  desktopCurrency?: string; // "" | "CNY" | "USD"; absent/empty = follow language
  desktopLayoutStyle: string; // "classic" | "workbench" | "creation"
  desktopTheme: string; // "auto" | "dark" | "light"
  desktopThemeStyle: string;
  desktopTerminalTheme: string; // "auto" follows app | "dark" | "light"
  closeBehavior: string; // "background" | "quit"
  displayMode: string; sessionExperience?: "standard" | "deep" | "concise"; reasoningDisplayMode: string; reasoningDisplayModeExplicit?: boolean;
  statusBarStyle: string; // "icon" | "text"
  statusBarItems: string[]; // ordered visible status bar item ids
  quickCommands?: QuickCommandEntry[]; // user-defined composer snippets (#18)
  defaultToolApprovalMode: ToolApprovalMode | string; // default for newly-created sessions
  defaultSubagentPolicy: SubagentPolicy; // fork: default sub-agent delegation tier for new sessions
  checkUpdates: boolean; // check for new versions on startup
  updateChannel: string; // compatibility field; always "stable"
  telemetry: boolean; // anonymous launch ping + scrubbed next-launch native crash diagnostics
  metrics: boolean; // aggregate quality/lifecycle metrics (anonymous signal/bucket counts)
  configPath: string;
  shadowedByPath?: string; // workspace reasonix.toml that outranks configPath, when one exists
  providerKinds: string[]; // provider implementations the kernel registered (for the kind picker)
  autoApproveTools: boolean;
  bypass: boolean; // legacy JSON key for live YOLO/full-access tool auto-approval
  conversationWidth?: string; // "standard" | "full"; absent from older Wails payloads
}

// QuickCommandEntry is one user-defined composer snippet: Title is the menu
// label, Text is inserted into the composer verbatim.
export interface QuickCommandEntry {
  title: string;
  text: string;
  enabled?: boolean; // absent means enabled (older configs)
}

// Task 385a: the lab 回答风格 selector payload — one round trip from
// App.ListOutputStyles(). Keys mirror the Go json tags of output_style_app.go.
export interface OutputStyleOption {
  name: string;
  description: string;
  builtin: boolean;
  keepCoding: boolean;
  path: string; // "" for built-ins
  active: boolean; // matches the persisted [agent] output_style
}

export interface OutputStyleIssue {
  path: string;
  name: string; // filename stem
  reason: string; // why the file did not load (surfaced, never silent)
}

export interface OutputStyleListView {
  active: string; // "" = default, no style
  options: OutputStyleOption[];
  issues: OutputStyleIssue[];
  dirs: string[]; // search path the list was built from
}

// Task 439: live state of the built-in zcode task bus, reported by the
// desktop App binding for the lab card (serve 状态灯 + roles 可视化).
export interface ZcodeTaskBusStatusView {
  /** Config intent: the lab switch (a flip needs a restart to arm the listener). */
  enabled: boolean;
  /** Whether this process actually hosts the bus listener right now. */
  running: boolean;
  /** Actual bound loopback address (host:port), empty when not running. */
  addr: string;
  /** The MCP endpoint zcode connects to, e.g. http://127.0.0.1:8787/mcp. */
  endpoint: string;
  /** Enrolled role names (sorted), from the [serve.bus_mcp] table. */
  roles: string[];
  /** Bind/construct failure surfaced to the card; empty when healthy. */
  err: string;
}
