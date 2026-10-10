// 任务 722/724 — 实验室布局内置默认数据（yaml 化第一版定义的编译期镜像）。
//
// 双角色：
//  ① 安全网——lab-layout.yaml 缺失或损坏时回退到这份数据（与 yaml 内容
//     同构同值，回退后界面与正常态完全一致，不白屏）；
//  ② 编译期门禁——成员 id 用 TierFeatureId 字面量类型、组 key 用
//     LabGroupKey 字面量、labelKey 用 DictKey 字面量，写错是编译错不是运行时。
//
// 内容 = 现状全量设置分组 + 任务 722 六点调整：
//   点1 备用模型行迁「提效-模型策略」卡（行级迁移在 SettingsPanel，本文件
//       只登记卡片结构）；
//   点2 modelStrategy 成员去掉 modelCapabilityFilter（已退役徽章不再上 rail；
//       只读展示行留在卡内）；
//   点3 sessionCollabAutoFold 由提效组独立入口并入 sessionCollab 卡成员；
//   点5 compactModel 自 contextGovernance 并入 safetyCostControl 卡成员；
//   点6 messageMerge 并入 safetyCostControl；safetyCostControl 整卡自
//       automation 迁 efficiency，排 contextGovernance 之后；
//   727 heartbeatRotation 登记为 safetyCostControl 成员（桥接
//       heartbeat-rotation.json，不参与卡片灯——默认开，参与会让卡常亮）。

import type { DictKey } from "../locales/en";
import type { LabTier, TierFeatureId } from "../lib/experimentTiers";
import type { LabLightKey } from "./labLightKeys";
import type { LabGroupKey, LabLayoutData } from "./labLayoutTypes";

/** Compile-checked shape of the default data: group keys, labelKeys, light
 * key refs and member ids are literal-typed — a typo here is a tsc error,
 * not a silent runtime fallback. Structurally assignable to LabLayoutData. */
interface StrictLabLayoutEntry {
  id: string;
  labelKey: DictKey;
  tier?: LabTier;
  onKeys: LabLightKey[];
  members?: Array<{ id: TierFeatureId; labelKey: DictKey; tier: LabTier }>;
}

interface StrictLabLayout {
  version: 1;
  groups: Array<{ key: LabGroupKey; labelKey: DictKey; entries: StrictLabLayoutEntry[] }>;
}

export const LAB_LAYOUT_DEFAULT: StrictLabLayout = {
  version: 1,
  groups: [
    {
      key: "automation",
      labelKey: "settings.labGroup.automation",
      entries: [
        { id: "autopilot", labelKey: "settings.autopilot", tier: "recommended", onKeys: ["autopilot"] },
        {
          id: "sessionCollab",
          labelKey: "settings.sessionCollab",
          tier: "recommended",
          onKeys: ["experimentalSessionCollab"],
          // 任务 722 点3：自动折叠跨会话消息并为跨会话协作子项。
          members: [
            { id: "sessionCollabAutoFold", labelKey: "settings.sessionCollabAutoFold", tier: "optional" },
          ],
        },
        { id: "fullAccess", labelKey: "settings.fullAccess", tier: "recommended", onKeys: ["experimentalFullAccess"] },
        { id: "dream", labelKey: "settings.dream", tier: "optional", onKeys: ["experimentalDream"] },
      ],
    },
    {
      key: "efficiency",
      labelKey: "settings.labGroup.efficiency",
      entries: [
        { id: "optimisticParallel", labelKey: "settings.optimisticParallel", tier: "recommended", onKeys: ["optimisticWrite"] },
        {
          id: "contextGovernance",
          labelKey: "settings.contextGovernance",
          onKeys: [
            "experimentalCompactionParallel",
            "experimentalContextBudget",
            "experimentalResearchBudget",
            "experimentalProactiveCompact",
            "experimentalColdCacheCompact",
            "experimentalCacheTuning",
          ],
          members: [
            { id: "compactionParallel", labelKey: "settings.compactionParallel", tier: "optional" },
            { id: "budgetControl", labelKey: "settings.contextBudget", tier: "recommended" },
            { id: "compressOpt", labelKey: "settings.proactiveCompact", tier: "recommended" },
            { id: "cacheTuning", labelKey: "settings.cacheTuning", tier: "optional" },
          ],
        },
        {
          // 任务 722 点6：整卡自 automation 迁入 efficiency（「安全 / 成本控制」
          // 本质是提效/成本域，且消息合并、压缩模型并入后语义更纯粹）。
          id: "safetyCostControl",
          labelKey: "settings.safetyCostControl",
          tier: "optional",
          onKeys: [
            "experimentalSafetyCostControl",
            // 任务 722 点5/点6：并入成员的键跟随迁卡（灯=家族任一非默认）。
            "experimentalCompactModel",
            "collabInboxMergeOn",
            "collabGuidanceMerge",
          ],
          members: [
            // 任务 722 点5：指定压缩模型（707 交付项）归安全/成本控制。
            { id: "compactModel", labelKey: "settings.compactModel", tier: "optional" },
            // 任务 722 点6：消息合并归安全/成本控制。
            { id: "messageMerge", labelKey: "settings.messageMerge", tier: "recommended" },
            // 任务 727：心跳会话轮换（桥接 heartbeat-rotation.json enabled；
            // 默认开故不参与卡片灯，见上）。
            { id: "heartbeatRotation", labelKey: "settings.heartbeatRotation", tier: "optional" },
          ],
        },
        {
          // 任务 722 点2：成员表去掉 modelCapabilityFilter——「已退役」徽章
          // 不再出现在模型策略卡（只读展示行仍在卡内，无徽章）。
          id: "modelStrategy",
          labelKey: "settings.modelStrategy",
          onKeys: ["experimentalHighSpeedModel"],
          members: [
            { id: "highSpeedModel", labelKey: "settings.highSpeedModel", tier: "optional" },
          ],
        },
        { id: "quickCommands", labelKey: "settings.quickCommands", tier: "recommended", onKeys: ["experimentalQuickCommands"] },
        { id: "traceAsState", labelKey: "settings.traceAsState", tier: "optional", onKeys: ["experimentalTraceAsState"] },
        {
          id: "outputStyle",
          labelKey: "settings.outputStyle",
          tier: "optional",
          onKeys: ["experimentalOutputStyleUI", "outputStyleActive"],
        },
        { id: "collabGroupView", labelKey: "settings.collabGroupView", tier: "unstable", onKeys: ["experimentalCollabGroupView"] },
      ],
    },
    {
      key: "ui",
      labelKey: "settings.labGroup.ui",
      entries: [
        { id: "tabCompress", labelKey: "settings.tabCompress", tier: "recommended", onKeys: ["experimentalTabCompress"] },
        { id: "trajectoryView", labelKey: "settings.trajectoryView", tier: "optional", onKeys: ["experimentalTrajectoryView"] },
        { id: "tabModeTint", labelKey: "settings.tabModeTint", tier: "unstable", onKeys: ["tabModeTintNonDefault"] },
        { id: "todoSidebar", labelKey: "settings.todoSidebar", tier: "recommended", onKeys: ["experimentalTodoSidebar"] },
        { id: "promptHistoryPicker", labelKey: "settings.promptHistoryPicker", tier: "recommended", onKeys: ["experimentalPromptHistoryPicker"] },
        { id: "sessionWall", labelKey: "settings.sessionWall", tier: "recommended", onKeys: ["experimentalSessionWall"] },
        {
          id: "subagentSuite",
          labelKey: "settings.subagentSuite",
          onKeys: [
            "experimentalSubagentPanel",
            "experimentalSubagentDetail",
            "experimentalSubagentPolicy",
            "experimentalSubagentTps",
          ],
          members: [
            { id: "subagentPanel", labelKey: "settings.subagentPanel", tier: "optional" },
            { id: "subagentDetail", labelKey: "settings.subagentDetail", tier: "optional" },
            { id: "subagentPolicy", labelKey: "settings.subagentPolicy", tier: "unstable" },
            { id: "subagentTps", labelKey: "settings.subagentTps", tier: "optional" },
          ],
        },
        { id: "completionSummary", labelKey: "settings.completionSummary", tier: "optional", onKeys: ["experimentalCompletionSummary"] },
        { id: "autoLoadOlder", labelKey: "settings.autoLoadOlder", tier: "optional", onKeys: ["experimentalAutoLoadOlder"] },
        { id: "splitView", labelKey: "settings.splitView", tier: "optional", onKeys: ["experimentalSplitView"] },
        { id: "draftPersistence", labelKey: "settings.draftPersistence", tier: "optional", onKeys: ["experimentalComposerDraft"] },
        { id: "selectionActions", labelKey: "settings.selectionActions", tier: "optional", onKeys: ["experimentalSelectionActions"] },
        { id: "questionSearch", labelKey: "settings.questionSearch", tier: "recommended", onKeys: ["experimentalQuestionSearch"] },
        { id: "opencodeGoUsage", labelKey: "settings.opencodeGoUsage", tier: "unstable", onKeys: ["experimentalOpenCodeGoUsage"] },
        {
          id: "updateFeedback",
          labelKey: "settings.updateFeedback",
          onKeys: ["experimentalRestartUpdate", "experimentalFeedback", "forkNotice"],
          members: [
            { id: "restartUpdate", labelKey: "settings.restartUpdate", tier: "recommended" },
            { id: "feedback", labelKey: "settings.feedback", tier: "optional" },
          ],
        },
      ],
    },
    {
      key: "observability",
      labelKey: "settings.labGroup.observability",
      entries: [
        {
          id: "monitoring",
          labelKey: "settings.monitoring",
          tier: "recommended",
          onKeys: ["experimentalSessionMonitor", "experimentalPerfMonitor"],
        },
      ],
    },
    {
      key: "dev-debug",
      labelKey: "settings.labGroup.devDebug",
      entries: [
        {
          id: "devDebug",
          labelKey: "settings.devDebug",
          onKeys: ["experimentalCDPDebugPort", "experimentalLifecycleNoiseGate"],
          members: [
            { id: "cdpDebugPort", labelKey: "settings.cdpDebugPort", tier: "unstable" },
            { id: "lifecycleNoiseGate", labelKey: "settings.lifecycleNoiseGate", tier: "unstable" },
          ],
        },
      ],
    },
    {
      key: "storage",
      labelKey: "settings.labGroup.storage",
      entries: [
        {
          id: "sessionStore",
          labelKey: "settings.sessionStore",
          onKeys: ["sessionStorageNonDefault", "eventsRotationNonDefault"],
          members: [
            { id: "sessionStorage", labelKey: "settings.sessionStorage", tier: "unstable" },
            { id: "eventsRotation", labelKey: "settings.eventsRotation", tier: "optional" },
          ],
        },
      ],
    },
    {
      key: "infra",
      labelKey: "settings.labGroup.infra",
      entries: [
        { id: "runtimeReuse", labelKey: "settings.runtimeReuse", tier: "optional", onKeys: ["experimentalRuntimeReuse"] },
        { id: "baseProcess", labelKey: "settings.baseProcess", tier: "unstable", onKeys: ["experimentalBaseProcess"] },
        { id: "zcodeTaskBus", labelKey: "settings.zcodeTaskBus", tier: "unstable", onKeys: ["experimentalZcodeTaskBus"] },
        { id: "pathRules", labelKey: "settings.pathRules", tier: "unstable", onKeys: ["experimentalPathRules"] },
        { id: "orphanHandling", labelKey: "settings.orphanHandling", tier: "unstable", onKeys: ["experimentalOrphanHandling"] },
        { id: "localServer", labelKey: "settings.localServer", tier: "unstable", onKeys: ["experimentalLocalServer"] },
      ],
    },
    {
      key: "tool-opt",
      labelKey: "settings.labGroup.toolOpt",
      entries: [
        { id: "toolOptimizations", labelKey: "settings.toolOptimizations", tier: "unstable", onKeys: ["experimentalToolOptimizations"] },
      ],
    },
  ],
};

// The default data doubles as the canonical LabLayoutData (the resolver's
// fallback value); the strict annotation above is the compile gate.
export const LAB_LAYOUT_DEFAULT_DATA: LabLayoutData = LAB_LAYOUT_DEFAULT;
