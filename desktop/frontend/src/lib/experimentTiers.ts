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

export type LabTier = "recommended" | "optional" | "unstable" | "retired";

/** Badge display order: strongest first. */
export const LAB_TIER_ORDER: readonly LabTier[] = ["recommended", "optional", "unstable", "retired"];

/** Every 表A feature id (46 items). The union is the type-level gate: a badge
 * can only ever reference a feature that carries a tier. */
export type TierFeatureId =
  | "autopilot"
  | "sessionCollab"
  | "fullAccess"
  | "optimisticParallel"
  | "dream"
  | "autonomousIdleTerminate"
  | "loopStreakNote"
  | "subagentPolicy"
  | "budgetControl"
  | "compressOpt"
  | "messageMerge"
  | "quickCommands"
  | "highSpeedModel"
  | "compactionParallel"
  | "traceAsState"
  | "eventWaitRecheck"
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
  | "localServer";

export const EXPERIMENT_FEATURE_TIERS: Readonly<Record<TierFeatureId, LabTier>> = {
  // ── automation（自动化，8 项）────────────────────────────────
  autopilot: "recommended",
  sessionCollab: "recommended",
  fullAccess: "recommended",
  optimisticParallel: "recommended",
  dream: "optional",
  autonomousIdleTerminate: "optional",
  loopStreakNote: "optional",
  subagentPolicy: "unstable",
  // ── efficiency（提效，10 项）─────────────────────────────────
  budgetControl: "recommended",
  compressOpt: "recommended",
  messageMerge: "recommended",
  quickCommands: "recommended",
  highSpeedModel: "optional",
  compactionParallel: "optional",
  traceAsState: "optional",
  eventWaitRecheck: "optional",
  outputStyle: "optional",
  cacheTuning: "optional",
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
};

/** 表A distribution, pinned by tests on BOTH sides (Go: render_lab_tiers_test.go,
 * frontend: experimentTiers.test.ts) — 推荐 15 / 可选 20 / 未稳定 10 / 已退役 1. */
export const LAB_TIER_COUNTS: Readonly<Record<LabTier, number>> = {
  recommended: 15,
  optional: 20,
  unstable: 10,
  retired: 1,
};

/** 任务 561 merged rail cards (card id → its 表A member ids). Standalone rail
 * entries badge themselves; merged cards badge every distinct member tier so
 * all 46 features stay visible on the 35-entry rail. */
export const LAB_RAIL_ENTRY_MEMBERS: Readonly<Record<string, readonly TierFeatureId[]>> = {
  autonomousRunGuard: ["autonomousIdleTerminate", "loopStreakNote"],
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
