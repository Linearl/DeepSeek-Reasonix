// 任务 722/724 — 实验室布局内置默认数据（yaml 化第一版定义的编译期镜像）。
//
// 双角色：
//  ① 安全网——lab-layout.yaml 缺失或损坏时回退到这份数据（与 yaml 内容
//     同构同值，回退后界面与正常态完全一致，不白屏）；
//  ② 编译期门禁——成员 id 用 TierFeatureId 字面量类型、组 key 用
//     LabGroupKey 字面量、labelKey 用 DictKey 字面量，写错是编译错不是运行时。
//
// 内容 = 现状全量设置分组 + 任务 722 六点调整 + 727 登记 + 任务 742：
//   点1 备用模型行迁「提效-模型策略」卡（行级迁移在 SettingsPanel，本文件
//       只登记卡片结构）；
//   点2 modelStrategy 成员去掉 modelCapabilityFilter（已退役徽章不再上 rail；
//       只读展示行留在卡内）；
//   点3 sessionCollabAutoFold 由提效组独立入口并入 sessionCollab 卡成员；
//   点5 compactModel 自 contextGovernance 并入 safetyCostControl 卡成员；
//   点6 messageMerge 并入 safetyCostControl；safetyCostControl 整卡自
//       automation 迁 efficiency，排 contextGovernance 之后；
//   727 heartbeatRotation 登记为 safetyCostControl 成员（桥接
//       heartbeat-rotation.json，不参与卡片灯——默认开，参与会让卡常亮）；
//   742 tabCompress + tabModeTint 合并为「标签页管理」单卡（条目级合并，
//       两键语义/setter 不变，行级徽章保留在 SettingsPanel）；新增
//       heartbeatBackground（automation 独立卡，默认关，可选）。
//   775 参数迁移基建+首批迁移（设计《实验室布局可调设计》§4.1/4.2）：
//       存储组登记 idleReleaseMinutes / goMemLimit 两张 kind:param 参数卡
//       （后台会话闲置释放 308-O4 + Go 软内存上限 308-O3），自跨会话协作卡
//       硬编码行迁出——数据驱动渲染，配置键/setter 零变化。

import type { DictKey } from "../locales/en";
import type { LabTier, TierFeatureId } from "../lib/experimentTiers";
import type { LabLightKey } from "./labLightKeys";
import type { LabParamInput } from "./labLayoutTypes";
import type { LabParamFieldId } from "./labParamFields";
import type { LabGroupKey, LabLayoutData } from "./labLayoutTypes";

/** Compile-checked shape of the default data: group keys, labelKeys, light
 * key refs and member ids are literal-typed — a typo here is a tsc error,
 * not a silent runtime fallback. Structurally assignable to LabLayoutData.
 * 任务 775：参数卡字段 id 用注册表键字面量（LabParamFieldId）、形态用
 * LabParamInput——default 与注册表漂移是编译错。 */
interface StrictLabLayoutEntry {
  id: string;
  labelKey: DictKey;
  kind?: "param";
  tier?: LabTier;
  onKeys: LabLightKey[];
  members?: Array<{ id: TierFeatureId; labelKey: DictKey; tier: LabTier }>;
  fields?: Array<{ id: LabParamFieldId; input: LabParamInput }>;
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
        // 任务 742：心跳任务后台化——开启后心跳触发不进标签栏（264 detached
        // 语义，提交成功后 park）。默认关=现状一致。
        { id: "heartbeatBackground", labelKey: "settings.heartbeatBackground", tier: "optional", onKeys: ["experimentalHeartbeatBackground"] },
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
        // 任务 742：标签页管理（合并卡）。灯 = 自适应压缩开 或 权限指示离
        // 默认档；卡徽章可选。卡内两行各自读自己的键、写自己的 setter
        // （条目级合并，561/722 先例——配置键零变化，已存偏好零迁移）。
        { id: "tabManagement", labelKey: "settings.tabManagement", tier: "optional", onKeys: ["experimentalTabCompress", "tabModeTintNonDefault"] },
        // 任务 782：视图布局（合并卡，742 先例）——分栏 + 轨迹视图。容器档
        // =成员最高档（可选）；卡内两行各读各键各写各 setter，配置键零变化。
        {
          id: "viewLayout",
          labelKey: "settings.viewLayout",
          tier: "optional",
          onKeys: ["experimentalSplitView", "experimentalTrajectoryView"],
          members: [
            { id: "splitView", labelKey: "settings.splitView", tier: "optional" },
            { id: "trajectoryView", labelKey: "settings.trajectoryView", tier: "optional" },
          ],
        },
        { id: "todoSidebar", labelKey: "settings.todoSidebar", tier: "recommended", onKeys: ["experimentalTodoSidebar"] },
        // 任务 782：检索与历史（合并卡）——历史提问选择器 + 搜索历史提问 +
        // 自动加载更早消息。容器档 =成员最高档（推荐）。
        {
          id: "searchHistory",
          labelKey: "settings.searchHistory",
          tier: "recommended",
          onKeys: ["experimentalPromptHistoryPicker", "experimentalQuestionSearch", "experimentalAutoLoadOlder"],
          members: [
            { id: "promptHistoryPicker", labelKey: "settings.promptHistoryPicker", tier: "recommended" },
            { id: "questionSearch", labelKey: "settings.questionSearch", tier: "recommended" },
            { id: "autoLoadOlder", labelKey: "settings.autoLoadOlder", tier: "optional" },
          ],
        },
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
        // 任务 782 验收⑤：任务完成摘要保持独立（Go 用量迁出后无合并对象）。
        { id: "completionSummary", labelKey: "settings.completionSummary", tier: "optional", onKeys: ["experimentalCompletionSummary"] },
        // 任务 782：输入与选区（合并卡）——草稿持久化 + 选区快捷操作。容器
        // 档 =成员最高档（可选）。
        {
          id: "inputSelection",
          labelKey: "settings.inputSelection",
          tier: "optional",
          onKeys: ["experimentalComposerDraft", "experimentalSelectionActions"],
          members: [
            { id: "draftPersistence", labelKey: "settings.draftPersistence", tier: "optional" },
            { id: "selectionActions", labelKey: "settings.selectionActions", tier: "optional" },
          ],
        },
        {
          id: "updateFeedback",
          labelKey: "settings.updateFeedback",
          onKeys: ["experimentalRestartUpdate", "experimentalFeedback", "forkNotice"],
          members: [
            { id: "restartUpdate", labelKey: "settings.restartUpdate", tier: "recommended" },
            { id: "feedback", labelKey: "settings.feedback", tier: "optional" },
          ],
        },
        // 任务 782④：opencodeGoUsage 迁出界面组（→任务 783 可观测性组
        // 「用量监测」卡成员；迁出瞬间 pane 分支保留，入口随 783 落卡恢复）。
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
        // 任务 783：「用量监测」合并卡（coding plan 用量架构）——GLM Plan
        // 用量（PlanUsageCard 已上线，provider 解析显示，无开关展示行）+
        // OpenCode Go 用量（自界面组迁入，782④ 同一动作的落卡半程）。
        // 扩展点：coding plan preset 家族（GLM CN / Z.AI Global / Qwen
        // CN·Global / Kimi 等 7+ 家）未来按需在 members 加行即成一子项，
        // 不改容器结构；卡灯 = OpenCode Go 开关（家族唯一非默认键）。
        {
          id: "usageMonitoring",
          labelKey: "settings.usageMonitoring",
          tier: "recommended",
          onKeys: ["experimentalOpenCodeGoUsage"],
          members: [
            { id: "glmPlanUsage", labelKey: "settings.glmPlanUsage", tier: "recommended" },
            { id: "opencodeGoUsage", labelKey: "settings.opencodeGoUsage", tier: "unstable" },
          ],
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
        // 任务 775：后台会话闲置释放（308-O4）——首批 kind:param 参数卡
        // （无开关纯字段卡；字段绑定在 labParamFields，与 yaml 同构同值）。
        // 任务 781：补 tier——闲置释放=recommended（rail 显推荐徽章）。
        {
          id: "idleReleaseMinutes",
          labelKey: "settings.detachedIdleRelease",
          kind: "param",
          tier: "recommended",
          onKeys: [],
          fields: [{ id: "detachedIdleReleaseMinutes", input: "number" }],
        },
        // 任务 775：Go 软内存上限（308-O3）——参数卡（无开关）。
        // 任务 781：补 tier——软内存上限=optional（rail 显可选徽章）。
        {
          id: "goMemLimit",
          labelKey: "settings.goMemLimit",
          kind: "param",
          tier: "optional",
          onKeys: [],
          fields: [{ id: "goMemLimitMB", input: "number" }],
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
        // 任务 783④：编辑读回（readBack）随 22.5 交付转正——未稳定→推荐
        // （注册表与 Go 渲染表同步升档；562 drift 检查钉两侧一致）。
        { id: "toolOptimizations", labelKey: "settings.toolOptimizations", tier: "recommended", onKeys: ["experimentalToolOptimizations"] },
      ],
    },
  ],
};

// The default data doubles as the canonical LabLayoutData (the resolver's
// fallback value); the strict annotation above is the compile gate.
export const LAB_LAYOUT_DEFAULT_DATA: LabLayoutData = LAB_LAYOUT_DEFAULT;
