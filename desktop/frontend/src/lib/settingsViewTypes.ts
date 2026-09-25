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
  // Task 81: exposes the restart-and-update action. Same preference restart_and_update reads.
  experimentalRestartUpdate?: boolean;
  // Task 254: registers the agent-facing restart_update tool (boot snapshot).
  experimentalAutonomousUpdate?: boolean;
  // Task 254: auto-resume scope after an update restart (off | goal_autopilot | all).
  autonomousUpdateResume?: string;
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
  // Task 121: exposes the agent submit_feedback tool and feedback inbox panel.
  experimentalFeedback?: boolean;
  // Task 259: exposes the right-dock todo tab plus tab visibility/wrap settings.
  experimentalTodoSidebar?: boolean;
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
  // Task 231: managed-path pre-approval — master switch + four checkboxes
  // (all ship off; the bypass only arms under autopilot).
  experimentalPreapproveManagedPaths?: boolean;
  preapproveSkills?: boolean;
  preapproveHooks?: boolean;
  preapproveSessionStores?: boolean;
  preapproveBashEscape?: boolean;
  // Task 192: active-tab residency policy (ships off; keeps the running tab resident across switches).
  experimentalActiveTabResident?: boolean;
  // Task 257: full access (yolo) — all declared write dirs pass, bash unwrapped (ships off; restart to apply).
  experimentalFullAccess?: boolean;
  // Task 130: exposes the Settings → 本地服务 page and serve-pool controls.
  experimentalLocalServer?: boolean;
  // Task 134: structured path-scope evaluation (docs/PATH_SCOPE_RULES.md).
  experimentalPathRules?: boolean;
  // Task 161: transcript cache tuning (experimental; user values ignored while off).
  maxCachedTabs?: number;
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
  // Task 204: cross-session chain ceiling (3..1000, default 5).
  sessionCollabHopLimit?: number;
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
  // Task 225: cascade approval to the autopilot parent.
  experimentalCascadeApproval?: boolean;
  // Task 19: multi-session collaboration experiment.
  experimentalSessionCollab?: boolean;
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
