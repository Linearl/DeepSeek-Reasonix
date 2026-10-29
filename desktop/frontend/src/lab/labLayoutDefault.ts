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
//   961 条目级收编三处（742/782 合并卡先例，配置键零变化）：
//       automation 组 dream+decisionPatterns+heartbeatBackground 三散条
//       并「智能自动化」卡（smartAutomation）；efficiency 组 quickCommands+
//       traceAsState+outputStyle 三散条并「交互与呈现」卡
//       （interactionPresentation）；ui 组 renderArtifact 并入「视图布局」
//       （viewLayout）卡成员行。
//   tl1193b 新增「记忆与上下文」组（automation 之后、efficiency 之前，
//       实验室第 8 活跃组）：pinnedIndex 卡（plan E pinned 常驻层索引化，
//       未稳定档，默认关，boot 快照重启生效）。

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
        // 任务 1005 批1 转档：autopilot / sessionCollab / fullAccess（及 infra
        // 段 runtimeReuse）四项 tier 升「稳定」——镜像档位注册表（562 双侧
        // 一致门禁）；桌面开关禁用态与开发者模式解锁见 SettingsPanel。
        // 任务 1084：autopilot pane 硬编码渲染区加 5 个高风险授权 checkbox
        // （默认全不勾）+ 1 个 ask 轮次上限数值字段——kind 仍为 feature，
        // 字段渲染走 pane 硬编码分支（feature 带 fields 会被 resolver 打回）。
        { id: "autopilot", labelKey: "settings.autopilot", tier: "stable", onKeys: ["autopilot"] },
        {
          id: "sessionCollab",
          labelKey: "settings.sessionCollab",
          tier: "stable",
          // 任务 815：cascade_approval 卡内行级开关（Task 225）补灯。
          onKeys: ["experimentalSessionCollab", "experimentalCascadeApproval"],
          // 任务 722 点3：自动折叠跨会话消息并为跨会话协作子项。
          members: [
            { id: "sessionCollabAutoFold", labelKey: "settings.sessionCollabAutoFold", tier: "optional" },
          ],
        },
        { id: "fullAccess", labelKey: "settings.fullAccess", tier: "stable", onKeys: ["experimentalFullAccess"] },
        {
          // 任务 961：智能自动化（合并卡，742/782 条目级合并先例）——
          // 梦境整理 + 决策模式提取 + 心跳任务后台化三散条并卡。容器档 =
          // 成员最高档（全可选 → 可选）；卡内三行各读各的键、各写各的
          // setter，配置键零变化，已存偏好零迁移。任务 1069：stalledIntentNudge
          // （模型提前结束判定器续轮提醒）作为第四成员行入卡。
          id: "smartAutomation",
          labelKey: "settings.smartAutomation",
          tier: "optional",
          // 任务 1070：补第四行「Standard 流续跑总闸」（默认关）。
          onKeys: ["experimentalDream", "experimentalDecisionPatterns", "experimentalHeartbeatBackground", "stalledIntentNudge", "standardContinuation"],
          members: [
            // 梦境整理（原 automation 独立卡，961 并卡）。
            { id: "dream", labelKey: "settings.dream", tier: "optional" },
            // 任务 801→961：决策模式自动提取+固化——压缩触发提取裁决轮次
            // 写记忆，心跳定期整合固化 decision-patterns.md。默认关=零行为。
            { id: "decisionPatterns", labelKey: "settings.decisionPatterns", tier: "optional" },
            // 任务 742→961：心跳任务后台化——开启后心跳触发不进标签栏（264
            // detached 语义，提交成功后 park）。默认关=现状一致。
            { id: "heartbeatBackground", labelKey: "settings.heartbeatBackground", tier: "optional" },
            // 任务 1069：模型提前结束判定器续轮提醒（stalled_intent_nudge，
            // 117 首落地，本件补设置页入口）。默认关=现状一致。
            { id: "stalledIntentNudge", labelKey: "settings.stalledIntentNudge", tier: "optional" },
            // 任务 1070：Standard 流续跑总闸——普通对话续跑通道（todo 续跑/
            // stalled-intent 修复/executor-handoff nudge）开闸。默认关=现状
            // 一致（普通对话永不自动续跑）；开启后仍有界上限与各边界排除。
            { id: "standardContinuation", labelKey: "settings.standardContinuation", tier: "optional" },
          ],
        },
      ],
    },
    {
      // tl1193b：记忆与上下文组（实验室第 8 活跃组）——pinned 常驻层索引化
      // 卡落位（plan E，未稳定档，默认关，boot 快照重启生效）。与 automation
      // 组的记忆整理族（dream/decisionPatterns）相邻成域。
      key: "memory-context",
      labelKey: "settings.labGroup.memoryContext",
      entries: [
        {
          id: "pinnedIndex",
          labelKey: "settings.pinnedIndex",
          tier: "unstable",
          onKeys: ["experimentalPinnedIndex"],
        },
      ],
    },
    {
      key: "efficiency",
      labelKey: "settings.labGroup.efficiency",
      entries: [
        // 任务 815：bash_heavy_guard（575）补实验室入口——卡成员行+灯
        // （optimistic_write 家族伴生，无 member 行不挂独立徽章）。
        { id: "optimisticParallel", labelKey: "settings.optimisticParallel", tier: "recommended", onKeys: ["optimisticWrite", "experimentalBashHeavyGuard"] },
        {
          id: "contextGovernance",
          labelKey: "settings.contextGovernance",
          // 任务 815：active_tab_resident 补灯（Task 347 附属行，Go 侧
          // cacheTuning renderKeys 家族已含，前端灯漏接）。
          onKeys: [
            "experimentalCompactionParallel",
            "experimentalContextBudget",
            "experimentalResearchBudget",
            "experimentalProactiveCompact",
            "experimentalColdCacheCompact",
            "experimentalCacheTuning",
            "experimentalActiveTabResident",
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
          // 任务 815：fallback_model 补灯（722 点1 迁入的行级开关）。
          onKeys: ["experimentalHighSpeedModel", "experimentalFallbackModel"],
          members: [
            { id: "highSpeedModel", labelKey: "settings.highSpeedModel", tier: "optional" },
          ],
        },
        {
          // 任务 961：交互与呈现（合并卡，742/782 条目级合并先例）——
          // 快捷指令 + 轨迹状态 + 回答风格三散条并卡。容器档 = 成员最高档
          // （快捷指令推荐 → 推荐）；卡内三行各读各的键、各写各的 setter，
          // 配置键零变化，已存偏好零迁移。
          id: "interactionPresentation",
          labelKey: "settings.interactionPresentation",
          tier: "recommended",
          onKeys: ["experimentalQuickCommands", "experimentalTraceAsState", "experimentalOutputStyleUI", "outputStyleActive"],
          members: [
            // 快捷指令（原 efficiency 独立卡，推荐档，961 并卡）。
            { id: "quickCommands", labelKey: "settings.quickCommands", tier: "recommended" },
            // 轨迹状态（原 efficiency 独立卡，961 并卡）。
            { id: "traceAsState", labelKey: "settings.traceAsState", tier: "optional" },
            // 回答风格（原 efficiency 独立卡；灯=门控开或已选非默认风格，
            // 961 并卡）。
            { id: "outputStyle", labelKey: "settings.outputStyle", tier: "optional" },
          ],
        },
        { id: "collabHub", labelKey: "settings.collabHub", tier: "unstable", onKeys: ["experimentalCollabHub"] },
      ],
    },
    {
      key: "ui",
      labelKey: "settings.labGroup.ui",
      entries: [
        // tl 1132：启动与加载（合并卡，742/782 条目级合并先例）——首项=
        // 启动遮罩；为 tl 1133 后续启动优化开关预留成员位。容器档 = 成员
        // 最高档（可选）。默认关（用户 2026-10-24 steer：做成实验特性允许
        // 开关方便 A/B 对比；铁律 2——关时 splash 不渲染）。
        {
          id: "startupLoading",
          labelKey: "settings.startupLoading",
          tier: "optional",
          onKeys: ["experimentalStartupSplash"],
          members: [
            { id: "startupSplash", labelKey: "settings.startupSplash", tier: "optional" },
          ],
        },
        // 任务 742：标签页管理（合并卡）。灯 = 自适应压缩开 或 权限指示离
        // 默认档；卡徽章可选。卡内两行各自读自己的键、写自己的 setter
        // （条目级合并，561/722 先例——配置键零变化，已存偏好零迁移）。
        // tl 1125：标签页管理合并卡升「推荐」（用户 2026-10-24 指令，与视图
        // 布局一并；配置键/默认值/卡内成员档位零变化，只动容器徽章档）。
        { id: "tabManagement", labelKey: "settings.tabManagement", tier: "recommended", onKeys: ["experimentalTabCompress", "tabModeTintNonDefault"] },
        // 任务 782：视图布局（合并卡，742 先例）——分栏 + 轨迹视图。容器档
        // =成员最高档（可选）；卡内两行各读各键各写各 setter，配置键零变化。
        // 任务 826：左右边栏 hover 展开两行入卡（左右独立两键两行，默认关；
        // 300ms 防误触 + 离开 15s 延时折叠，只回收 hover 自动展开的）。
        // 任务 940：composer 权限选择器形态二选一行入卡（默认关=现按钮组，
        // 开=zcode 同款下拉框省空间）。
        // 任务 959 ①：对话表格按栏宽自动换行入卡为成员行（940 先例，默认
        // 关=宽表横滚布局不变）。
        {
          id: "viewLayout",
          labelKey: "settings.viewLayout",
          // tl 1125：视图布局合并卡升「推荐」（与标签页管理一并，同指令）。
          tier: "recommended",
          onKeys: ["experimentalSplitView", "experimentalTrajectoryView", "experimentalSidebarHoverLeft", "experimentalSidebarHoverRight", "experimentalApprovalSelect", "experimentalTableWrap", "experimentalSendTargetSelect", "experimentalSmoothScrollFollow", "experimentalDraftRewrite"],
          members: [
            { id: "splitView", labelKey: "settings.splitView", tier: "optional" },
            { id: "trajectoryView", labelKey: "settings.trajectoryView", tier: "optional" },
            { id: "sidebarHoverLeft", labelKey: "settings.sidebarHoverLeft", tier: "optional" },
            { id: "sidebarHoverRight", labelKey: "settings.sidebarHoverRight", tier: "optional" },
            { id: "approvalSelect", labelKey: "settings.approvalSelect", tier: "optional" },
            // 任务 959 ①：对话表格按栏宽自动换行（默认关=宽表横滚不变）。
            { id: "tableWrap", labelKey: "settings.tableWrap", tier: "optional" },
            // 任务 959 ②：发送目标选择器形态二选一（默认关=三按钮组）。
            { id: "sendTargetSelect", labelKey: "settings.sendTargetSelect", tier: "optional" },
            // tl 1165：流式滚动平滑跟随（DSH ScrollFollow 同款四条防线；默认
            // 关=瞬移定位逐字节不变）。
            { id: "smoothScrollFollow", labelKey: "settings.smoothScrollFollow", tier: "optional" },
            // 任务 1024：composer 草稿优化（默认关=改写按钮不出现、布局零变化）。
            { id: "draftRewrite", labelKey: "settings.draftRewrite", tier: "optional" },
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
        // 任务 686：工作流可视化面板（palette 入口开 DAG 流程图；685 引擎
        // 未落地，当前为前端 fixture 预览+演示回放）。默认关=palette 不变。
        { id: "workflowPanel", labelKey: "settings.workflowPanel", tier: "unstable", onKeys: ["experimentalWorkflowPanel"] },
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
          // 任务 971 曾迁 restartUpdate 至工具优化聚合页；任务 992（用户 1018 21:34）
          // 归位回本卡——重启更新放回原位置，工具优化处不再保留。
          onKeys: ["experimentalFeedback", "forkNotice", "experimentalRestartUpdate"],
          members: [
            { id: "feedback", labelKey: "settings.feedback", tier: "optional" },
            { id: "restartUpdate", labelKey: "settings.restartUpdate", tier: "recommended" },
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
        // tl 1097：日志管理——kind:param 参数卡（无开关）。两个 segment 档位
        // （告警日志保留时间 + 空间上限）+ 当前占用行 + 打开日志目录按钮；
        // 全量日志不受此卡影响（固定 25 份 ≈ 2 天）。775 参数卡基建同款。
        {
          id: "logManagement",
          labelKey: "settings.logManagement",
          kind: "param",
          tier: "optional",
          onKeys: [],
          fields: [
            { id: "alertLogRetentionDays", input: "segment" },
            { id: "alertLogMaxTotalMB", input: "segment" },
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
        // 任务 846：配置数据目录可更改（未稳定档，默认关）。独立卡——迁移
        // 向导有自己的 pane（HomePointerLabPane），不走 members 合并卡。
        {
          id: "homePointer",
          labelKey: "settings.homePointer",
          tier: "unstable",
          onKeys: ["homePointer"],
        },
        // tl-1174：冷会话主转录整文件 zstd 压缩（可选档，默认关）。热会话
        // 保持明文；冷读透明解压；恢复活跃时物化回明文；关=逐字节一致。
        // 空闲 sweep 每 tick 现读开关，保存即生效免重启。
        {
          id: "jsonlZstd",
          labelKey: "settings.jsonlZstd",
          tier: "optional",
          onKeys: ["experimentalJsonlZstd"],
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
      // 任务 785：S1 常驻底座开关条目已随底座退役移除。
      entries: [
        { id: "runtimeReuse", labelKey: "settings.runtimeReuse", tier: "stable", onKeys: ["experimentalRuntimeReuse"] },
        // 任务 790：ACP 常驻监听（与 yaml 同序同值——回退安全网）。
        { id: "acpListen", labelKey: "settings.acpListen", tier: "unstable", onKeys: ["experimentalACPListen"] },
        { id: "pathRules", labelKey: "settings.pathRules", tier: "unstable", onKeys: ["experimentalPathRules"] },
        { id: "orphanHandling", labelKey: "settings.orphanHandling", tier: "recommended", onKeys: ["experimentalOrphanHandling"] },
        { id: "localServer", labelKey: "settings.localServer", tier: "unstable", onKeys: ["experimentalLocalServer"] },
        // 任务 992（用户 1018 21:34 拍板）：工具优化整体合并到基础设施组（原独立
        // tool-opt 组退役，labGroup.toolOpt 不再引用）；restartUpdate 归位回界面组
        // updateFeedback 卡（971 迁入撤销，onKeys/members 同步回迁）；宽松模式升可选。
        // 卡灯三键（readBack/直注/能力组），restartUpdate 不再入灯。
        { id: "toolOptimizations", labelKey: "settings.toolOptHub", tier: "recommended", onKeys: ["experimentalToolOptimizations", "experimentalMultiEditInjection", "experimentalCapabilityGroups"], members: [
          { id: "editEvidenceRelaxed", labelKey: "settings.editEvidenceRelaxed", tier: "optional" },
          { id: "multiEditInjection", labelKey: "settings.multiEditInjection", tier: "optional" },
          { id: "capabilityGroups", labelKey: "settings.capabilityGroups", tier: "optional" },
          // tl-1218：multi_edit 三条增强（各自独立开关，默认关重启生效）。
          { id: "multiEditErrorDetail", labelKey: "settings.multiEditErrorDetail", tier: "optional" },
          { id: "multiEditEvidenceNarrow", labelKey: "settings.multiEditEvidenceNarrow", tier: "optional" },
          { id: "multiEditSkipFailed", labelKey: "settings.multiEditSkipFailed", tier: "optional" },
        ] },
      ],
    },
  ],
};

// The default data doubles as the canonical LabLayoutData (the resolver's
// fallback value); the strict annotation above is the compile gate.
export const LAB_LAYOUT_DEFAULT_DATA: LabLayoutData = LAB_LAYOUT_DEFAULT;
