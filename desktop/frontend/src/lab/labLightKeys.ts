// 任务 724 — 卡片灯的配置键引用注册表（「配置键引用」的 TS 侧白名单）。
//
// yaml / 内置默认里的 entry.onKeys 只能引用本表里的 id——未知 id 在
// resolveLabLayout 校验时打回（安全网回退默认布局）。每个 id 是一个对
// SettingsView 快照的纯谓词：键语义（缺省值、非布尔键的判亮规则）全部
// 收拢在这一处，yaml 改分组/排序/徽章永远碰不到键语义（硬约束①）。
//
// 读的是桌面设置快照（进设置页时一次 app.Settings() 拉全量——任务 724
// 性能附录③：读路径本来就是单次批量注入，不存在逐开关同步 IPC）。

import type { SettingsView } from "../lib/settingsViewTypes";

export type LabLightKey = keyof typeof LAB_LIGHT_KEYS;

/** id → light predicate. Referenced by entry.onKeys in the layout data. */
export const LAB_LIGHT_KEYS = {
  // ── plain boolean SettingsView fields（一字段一谓词）────────────
  autopilot: (s: SettingsView) => Boolean(s.autopilot),
  experimentalSessionCollab: (s: SettingsView) => Boolean(s.experimentalSessionCollab),
  experimentalFullAccess: (s: SettingsView) => Boolean(s.experimentalFullAccess),
  experimentalDream: (s: SettingsView) => Boolean(s.experimentalDream),
  experimentalSafetyCostControl: (s: SettingsView) => Boolean(s.experimentalSafetyCostControl),
  experimentalSessionCollabAutoFold: (s: SettingsView) => Boolean(s.experimentalSessionCollabAutoFold),
  experimentalCompactionParallel: (s: SettingsView) => Boolean(s.experimentalCompactionParallel),
  experimentalContextBudget: (s: SettingsView) => Boolean(s.experimentalContextBudget),
  experimentalResearchBudget: (s: SettingsView) => Boolean(s.experimentalResearchBudget),
  experimentalProactiveCompact: (s: SettingsView) => Boolean(s.experimentalProactiveCompact),
  experimentalColdCacheCompact: (s: SettingsView) => Boolean(s.experimentalColdCacheCompact),
  experimentalCacheTuning: (s: SettingsView) => Boolean(s.experimentalCacheTuning),
  experimentalCompactModel: (s: SettingsView) => Boolean(s.experimentalCompactModel),
  experimentalHighSpeedModel: (s: SettingsView) => Boolean(s.experimentalHighSpeedModel),
  experimentalQuickCommands: (s: SettingsView) => Boolean(s.experimentalQuickCommands),
  experimentalTraceAsState: (s: SettingsView) => Boolean(s.experimentalTraceAsState),
  experimentalOutputStyleUI: (s: SettingsView) => Boolean(s.experimentalOutputStyleUI),
  experimentalCollabGroupView: (s: SettingsView) => Boolean(s.experimentalCollabGroupView),
  experimentalTabCompress: (s: SettingsView) => Boolean(s.experimentalTabCompress),
  experimentalTrajectoryView: (s: SettingsView) => Boolean(s.experimentalTrajectoryView),
  experimentalTodoSidebar: (s: SettingsView) => Boolean(s.experimentalTodoSidebar),
  experimentalPromptHistoryPicker: (s: SettingsView) => Boolean(s.experimentalPromptHistoryPicker),
  experimentalSessionWall: (s: SettingsView) => Boolean(s.experimentalSessionWall),
  experimentalSubagentPanel: (s: SettingsView) => Boolean(s.experimentalSubagentPanel),
  experimentalSubagentDetail: (s: SettingsView) => Boolean(s.experimentalSubagentDetail),
  experimentalSubagentPolicy: (s: SettingsView) => Boolean(s.experimentalSubagentPolicy),
  experimentalSubagentTps: (s: SettingsView) => Boolean(s.experimentalSubagentTps),
  experimentalCompletionSummary: (s: SettingsView) => Boolean(s.experimentalCompletionSummary),
  experimentalAutoLoadOlder: (s: SettingsView) => Boolean(s.experimentalAutoLoadOlder),
  experimentalSplitView: (s: SettingsView) => Boolean(s.experimentalSplitView),
  experimentalComposerDraft: (s: SettingsView) => Boolean(s.experimentalComposerDraft),
  experimentalSelectionActions: (s: SettingsView) => Boolean(s.experimentalSelectionActions),
  experimentalQuestionSearch: (s: SettingsView) => Boolean(s.experimentalQuestionSearch),
  experimentalOpenCodeGoUsage: (s: SettingsView) => Boolean(s.experimentalOpenCodeGoUsage),
  experimentalRestartUpdate: (s: SettingsView) => Boolean(s.experimentalRestartUpdate),
  experimentalFeedback: (s: SettingsView) => Boolean(s.experimentalFeedback),
  forkNotice: (s: SettingsView) => Boolean(s.forkNotice),
  experimentalSessionMonitor: (s: SettingsView) => Boolean(s.experimentalSessionMonitor),
  experimentalPerfMonitor: (s: SettingsView) => Boolean(s.experimentalPerfMonitor),
  experimentalCDPDebugPort: (s: SettingsView) => Boolean(s.experimentalCDPDebugPort),
  experimentalLifecycleNoiseGate: (s: SettingsView) => Boolean(s.experimentalLifecycleNoiseGate),
  experimentalRuntimeReuse: (s: SettingsView) => Boolean(s.experimentalRuntimeReuse),
  experimentalBaseProcess: (s: SettingsView) => Boolean(s.experimentalBaseProcess),
  experimentalZcodeTaskBus: (s: SettingsView) => Boolean(s.experimentalZcodeTaskBus),
  experimentalPathRules: (s: SettingsView) => Boolean(s.experimentalPathRules),
  experimentalOrphanHandling: (s: SettingsView) => Boolean(s.experimentalOrphanHandling),
  experimentalLocalServer: (s: SettingsView) => Boolean(s.experimentalLocalServer),
  experimentalToolOptimizations: (s: SettingsView) => Boolean(s.experimentalToolOptimizations),
  collabGuidanceMerge: (s: SettingsView) => Boolean(s.collabGuidanceMerge),
  // ── derived / non-boolean reads（判亮规则收拢在谓词里）──────────
  /** sandbox.optimistic_write（嵌套字段）。 */
  optimisticWrite: (s: SettingsView) => Boolean(s.sandbox?.optimisticWrite),
  /** 消息合并灯：收件箱归并三态 ≠ off（task 221）。 */
  collabInboxMergeOn: (s: SettingsView) => (s.collabInboxMerge || "off") !== "off",
  /** 回答风格灯：开关开 或 已配置任一风格（task 385a）。 */
  outputStyleActive: (s: SettingsView) => Boolean(s.experimentalOutputStyleUI) || (s.outputStyle ?? "") !== "",
  /** 标签权限指示灯：三档非默认档（徽章）即亮（task 651）。 */
  tabModeTintNonDefault: (s: SettingsView) => (s.tabPermissionIndicator ?? "badge") !== "badge",
  /** 会话存储灯半：存储模式非 v3_only（task 155）。 */
  sessionStorageNonDefault: (s: SettingsView) => (s.sessionStorage ?? "v3_only") !== "v3_only",
  /** 会话存储灯半：事件日志轮转非 off（task 333）。 */
  eventsRotationNonDefault: (s: SettingsView) => (s.eventsAutoRotation ?? "manual") !== "off",
} as const;

/** The validation whitelist: every id a layout document may reference. */
export const LAB_LIGHT_KEY_IDS: ReadonlySet<string> = new Set(Object.keys(LAB_LIGHT_KEYS));
