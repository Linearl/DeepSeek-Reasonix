// 任务 562 — 实验室三档徽章注册表（前端展示镜像）。
//
// 权威源是 Go 侧 `labFeatureTiers`（internal/config/render.go，xlsx 表A
// 「实验室特性-档位与图墙-20261006」推荐等级列：推荐 15 / 可选 20 / 未稳定
// 10 / 已退役 1 = 46）。Go 侧有门禁（render_lab_tiers_test.go：渲染表新出现
// 未标档位的实验室键 ⇒ 测试失败）；本模块是唯一的展示事实源——设置页实验室
// tab（rail + 详情卡）与会话图墙（ForkFeaturesIntroDialog 精选区）两视图都
// 从这里取档位，徽章在两个视图间不可能失配。
//
// 用户口径：推荐 = 核心特性，不开启会有体验缺口；可选 = 非核心，不开启只
// 小幅影响；未稳定 = 开发测试中，开启可能不稳定；已退役 = 开关已退役。

import type { DictKey } from "./i18n";

export type LabTier = "recommended" | "optional" | "unstable" | "retired";

/** Badge display order: strongest first. */
export const LAB_TIER_ORDER: readonly LabTier[] = ["recommended", "optional", "unstable", "retired"];

/** Every 表A feature id (48 items as of 任务 705 — the register mirrors the Go
 * labFeatureTiers registry; see the LAB_TIER_COUNTS note for the two items
 * 562's original 46 missed). The union is the type-level gate: a badge
 * can only ever reference a feature that carries a tier. */
export type TierFeatureId =
  | "autopilot"
  | "sessionCollab"
  | "fullAccess"
  | "optimisticParallel"
  | "dream"
  // 任务 517:「安全 / 成本控制」单键卡（吸收 561 的 autonomousIdleTerminate +
  // loopStreakNote 与 standalone eventWaitRecheck 三个表A id → 一个 id）。
  | "safetyCostControl"
  | "subagentPolicy"
  | "budgetControl"
  | "compressOpt"
  | "messageMerge"
  | "quickCommands"
  | "highSpeedModel"
  | "compactionParallel"
  | "traceAsState"
  | "outputStyle"
  | "cacheTuning"
  | "tabCompress"
  | "todoSidebar"
  | "promptHistoryPicker"
  | "restartUpdate"
  | "sessionWall"
  | "subagentPanel"
  | "subagentDetail"
  | "completionSummary"
  | "autoLoadOlder"
  | "splitView"
  | "draftPersistence"
  | "selectionActions"
  | "questionSearch"
  | "opencodeGoUsage"
  | "feedback"
  | "tabModeTint"
  | "monitoring"
  | "subagentTps"
  | "cdpDebugPort"
  | "lifecycleNoiseGate"
  | "eventsRotation"
  | "sessionStorage"
  | "runtimeReuse"
  | "baseProcess"
  | "zcodeTaskBus"
  | "modelCapabilityFilter"
  | "pathRules"
  | "orphanHandling"
  | "localServer"
  // 任务 603:「工具优化」族首件 (edit readBack + evidence gate linkage).
  | "toolOptimizations"
  // 任务 677: 群聊入口开关（409 视图交付漏挂铁律 2 开关，未稳定档默认关）。
  | "collabGroupView"
  // 任务 705: 超长跨会话消息自动折叠（默认关=全量展示，可选档）。
  | "sessionCollabAutoFold";

export const EXPERIMENT_FEATURE_TIERS: Readonly<Record<TierFeatureId, LabTier>> = {
  // ── automation（自动化，6 项；任务 650：optimisticParallel 迁提效）──
  autopilot: "recommended",
  sessionCollab: "recommended",
  fullAccess: "recommended",
  dream: "optional",
  safetyCostControl: "optional",
  subagentPolicy: "unstable",
  // ── efficiency（提效，10 项；任务 517 B3 并入 safetyCostControl；────
  //    任务 650：optimisticParallel 自自动化组迁入——减少写锁等待属提效）
  optimisticParallel: "recommended",
  budgetControl: "recommended",
  compressOpt: "recommended",
  messageMerge: "recommended",
  quickCommands: "recommended",
  highSpeedModel: "optional",
  compactionParallel: "optional",
  traceAsState: "optional",
  outputStyle: "optional",
  cacheTuning: "optional",
  // 任务 677：群聊入口开关（409 交付漏挂开关，用户定档未稳定、默认关）。
  collabGroupView: "unstable",
  // 任务 705：超长跨会话消息自动折叠（默认关=全量展示）。展示类开关按 550
  // 口径定档可选——不开启只影响长消息的阅读密度，不动任何协作行为。
  sessionCollabAutoFold: "optional",
  // ── ui（界面，15 项）────────────────────────────────────────
  tabCompress: "recommended",
  todoSidebar: "recommended",
  promptHistoryPicker: "recommended",
  restartUpdate: "recommended",
  sessionWall: "recommended",
  subagentPanel: "optional",
  subagentDetail: "optional",
  completionSummary: "optional",
  autoLoadOlder: "optional",
  splitView: "optional",
  draftPersistence: "optional",
  selectionActions: "optional",
  questionSearch: "recommended",
  opencodeGoUsage: "unstable",
  feedback: "optional",
  // 任务 621：tabModeTint（任务 504 标签模式色调）在 Go labFeatureTiers 一直
  // 有档（unstable），但前端注册表漏收——实验室页渲染却无徽章，621 验收
  // 「每项特性有且仅有一个徽章」不允许。档位镜像 Go 侧。
  tabModeTint: "unstable",
  // ── observability（可观测性，2 项）───────────────────────────
  monitoring: "recommended",
  subagentTps: "optional",
  // ── dev-debug（开发调试，2 项）───────────────────────────────
  cdpDebugPort: "unstable",
  lifecycleNoiseGate: "unstable",
  // ── storage（存储，2 项）────────────────────────────────────
  eventsRotation: "optional",
  sessionStorage: "unstable",
  // ── infra（基础设施，7 项）──────────────────────────────────
  runtimeReuse: "optional",
  baseProcess: "unstable",
  zcodeTaskBus: "unstable",
  modelCapabilityFilter: "retired",
  pathRules: "unstable",
  orphanHandling: "unstable",
  localServer: "unstable",
  // ── tool-opt（工具优化，1 项）────────────────────────────────
  toolOptimizations: "unstable",
};

/** 表A distribution, pinned by tests on BOTH sides (Go: render_lab_tiers_test.go,
 * frontend: experimentTiers.test.ts) — 推荐 15 / 可选 19 / 未稳定 13 / 已退役 1.
 * (任务 621 修正：原钉 46 项未收 toolOptimizations（603）与 tabModeTint（504，
 * Go 侧一直有档），漏收使实验室页出现无徽章特性。任务 517：B1/B2/B3（可选×3）
 * 合并为 safetyCostControl（可选×1），可选 20→18、总数 48→46。任务 677：
 * collabGroupView（群聊入口开关，409 交付漏挂铁律 2 开关，未稳定）入表，
 * 未稳定 12→13、总数 46→47。任务 705：sessionCollabAutoFold（超长跨会话
 * 消息自动折叠，默认关）入表，可选 18→19、总数 47→48。) */
export const LAB_TIER_COUNTS: Readonly<Record<LabTier, number>> = {
  recommended: 15,
  optional: 19,
  unstable: 13,
  retired: 1,
};

/** 任务 561 merged rail cards (card id → its 表A member ids). Standalone rail
 * entries badge themselves; merged cards badge every distinct member tier so
 * all 表A features stay visible on the rail. 任务 517：M1 autonomousRunGuard
 * 卡与 standalone eventWaitRecheck 并入单键卡 safetyCostControl（键级合并，
 * 无成员表——该卡自己就是一个表A id，徽章自挂）。 */
export const LAB_RAIL_ENTRY_MEMBERS: Readonly<Record<string, readonly TierFeatureId[]>> = {
  contextGovernance: ["compactionParallel", "budgetControl", "compressOpt", "cacheTuning"],
  modelStrategy: ["highSpeedModel", "modelCapabilityFilter"],
  subagentSuite: ["subagentPanel", "subagentDetail", "subagentPolicy", "subagentTps"],
  updateFeedback: ["restartUpdate", "feedback"],
  devDebug: ["cdpDebugPort", "lifecycleNoiseGate"],
  sessionStore: ["sessionStorage", "eventsRotation"],
};

/** 任务 563 图墙收录清单（xlsx 表B W1：推荐 12 + 可选 4 = 16 项）。562 先
 * 挂档位徽章；卡片三要素/详情弹窗/建议开启角标由 563 落地。生长规则：新增
 * 实验项默认不进图墙，人工决定收录（改此清单即改图墙）。`as const` keeps
 * the tuple literal-typed so consumers can index per-pick records. */
export const LAB_WALL_PICKS = [
  "sessionWall",
  "tabCompress",
  "todoSidebar",
  "promptHistoryPicker",
  "monitoring",
  "restartUpdate",
  "budgetControl",
  "compressOpt",
  "messageMerge",
  "autopilot",
  "sessionCollab",
  "fullAccess",
  "splitView",
  "subagentPanel",
  "selectionActions",
  "completionSummary",
] as const;

/** The 16 pick ids, narrowed from the tuple. */
export type LabWallPickId = (typeof LAB_WALL_PICKS)[number];

/** 任务 604 — 图墙详情弹窗「设置位置」。wall pick → 实验室 rail 位置（两组标签
 * 键：[组, 卡]）。与 LAB_WALL_PICKS / LAB_RAIL_ENTRY_MEMBERS 同源同文件——561 式
 * 分组重组时在同一处同步，不会出现「rail 改了、提示没改」的双源漂移。卡标签键
 * 与 SettingsPanel features 渲染表的条目一致；561 合并卡成员（restartUpdate →
 * updateFeedback、budgetControl / compressOpt → contextGovernance、subagentPanel
 * → subagentSuite）指向合并卡本身（卡内即各成员独立开关）。两组键都是 DictKey
 * 字面量：typo 是编译错，不是空路径。不在表内的 pick（纯展示特性，无实验室
 * 开关）⇒ 弹窗不显示设置位置行。 */
export const LAB_SETTINGS_LOCATION: Readonly<Partial<Record<LabWallPickId, readonly [DictKey, DictKey]>>> = {
  // ── automation（自动化）──────────────────────────────────────
  autopilot: ["settings.labGroup.automation", "settings.autopilot"],
  sessionCollab: ["settings.labGroup.automation", "settings.sessionCollab"],
  fullAccess: ["settings.labGroup.automation", "settings.fullAccess"],
  // ── efficiency（提效）────────────────────────────────────────
  budgetControl: ["settings.labGroup.efficiency", "settings.contextGovernance"],
  compressOpt: ["settings.labGroup.efficiency", "settings.contextGovernance"],
  messageMerge: ["settings.labGroup.efficiency", "settings.messageMerge"],
  // ── ui（界面）───────────────────────────────────────────────
  sessionWall: ["settings.labGroup.ui", "settings.sessionWall"],
  tabCompress: ["settings.labGroup.ui", "settings.tabCompress"],
  todoSidebar: ["settings.labGroup.ui", "settings.todoSidebar"],
  promptHistoryPicker: ["settings.labGroup.ui", "settings.promptHistoryPicker"],
  restartUpdate: ["settings.labGroup.ui", "settings.updateFeedback"],
  splitView: ["settings.labGroup.ui", "settings.splitView"],
  subagentPanel: ["settings.labGroup.ui", "settings.subagentSuite"],
  selectionActions: ["settings.labGroup.ui", "settings.selectionActions"],
  completionSummary: ["settings.labGroup.ui", "settings.completionSummary"],
  // ── observability（可观测性）─────────────────────────────────
  monitoring: ["settings.labGroup.observability", "settings.monitoring"],
};

export function isTierFeatureId(id: string): id is TierFeatureId {
  return Object.prototype.hasOwnProperty.call(EXPERIMENT_FEATURE_TIERS, id);
}

/** Distinct tiers a rail entry should display, in badge order. Merged cards
 * show every distinct member tier; standalone 表A entries show their own;
 * non-表A entries (preapproveManagedPaths, task-364 domain) show none. */
export function railTiersFor(entryId: string): LabTier[] {
  const members = LAB_RAIL_ENTRY_MEMBERS[entryId] ?? (isTierFeatureId(entryId) ? [entryId] : []);
  return LAB_TIER_ORDER.filter((tier) => members.some((m) => EXPERIMENT_FEATURE_TIERS[m] === tier));
}

/** 任务 563 — 「建议开启」badge rule (xlsx 表B W4): ONLY a recommended-tier
 * pick that is currently off. Optional/unstable never nag; an on pick never
 * nags. Pure so both card and dialog call the one rule. */
export function suggestEnable(tier: LabTier, on: boolean): boolean {
  return tier === "recommended" && !on;
}

/** Locale keys for tier labels (DictKey-checked literals — a typo here is a
 * compile error once the keys exist in locales/*). */
export const LAB_TIER_LABEL_KEYS: Readonly<Record<LabTier, "settings.labTier.recommended" | "settings.labTier.optional" | "settings.labTier.unstable" | "settings.labTier.retired">> = {
  recommended: "settings.labTier.recommended",
  optional: "settings.labTier.optional",
  unstable: "settings.labTier.unstable",
  retired: "settings.labTier.retired",
};

/** 任务 621 — tooltip text keys: one line per tier saying what the tier means
 * (口径出处 2026-10-06 用户定：推荐 = 核心特性、不开启会有体验缺口；可选 =
 * 非核心增强；未稳定 = 开发测试中、开启可能不稳定). Same DictKey-checked
 * literal discipline as LAB_TIER_LABEL_KEYS. */
export const LAB_TIER_DESC_KEYS: Readonly<Record<LabTier, `settings.labTier.${LabTier}.desc`>> = {
  recommended: "settings.labTier.recommended.desc",
  optional: "settings.labTier.optional.desc",
  unstable: "settings.labTier.unstable.desc",
  retired: "settings.labTier.retired.desc",
};
