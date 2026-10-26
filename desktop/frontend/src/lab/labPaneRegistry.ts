// tl 1134：实验室 pane 注册表——自 SettingsPanel.tsx 下沉（实验室二级 chunk 拆分）。
// 设置导航搜索与实验室 rail 用同一份布局：两边都从本模块取，单源不漂移
// （原 764 口径不变：同一 yaml 常量 + 同一 pane 白名单 resolve，yaml 损坏整体回退内置默认）。
import { resolveLabLayout } from "./labLayout";
import labLayoutYaml from "./lab-layout.yaml?raw";

export type ExperimentFeatureId =
  // Task 561 merged family cards — the merge is entry/card level only: every
  // member switch below keeps its own config key and its own setter (task
  // 427/318.5 precedent). Family map:
  //   modelStrategy      = M2 模型策略族 (highSpeedModel + retired
  //                        modelCapabilityFilter shown read-only inside the card)
  //   contextGovernance  = M3 压缩/预算族 (compactionParallel + budgetControl +
  //                        compressOpt + cacheTuning)
  //   subagentSuite      = M4 子代理族   (subagentPanel + subagentDetail +
  //                        subagentPolicy + subagentTps)
  //   devDebug           = M5 开发调试族 (cdpDebugPort + lifecycleNoiseGate)
  //   updateFeedback     = M6 更新/反馈族 (restartUpdate + feedback)
  //   sessionStore       = M7 会话存储族 (sessionStorage + eventsRotation)
  //   （任务 517：M1 autonomousRunGuard 与 standalone eventWaitRecheck 已并入
  //   单键卡 safetyCostControl——不是入口级合并，是配置键级合并。）
  // M8 keeps standalone entries: autopilot / sessionCollab / monitoring /
  // fullAccess / optimisticParallel.
  | "autopilot"
  // Task 318.5: session + performance monitoring merge into one lab entry
  // (the two underlying switches stay independent inside the page).
  | "monitoring"
  // 任务 782：视图布局合并卡（分栏 + 轨迹视图，条目级合并，742 先例）——
  // splitView/trajectoryView 独立 pane 退役为卡内成员行（键/setter 零变化）。
  | "viewLayout"
  | "todoSidebar"
  // Task 505: session graph wall (palette 跳转会话 entry + grid wall).
  | "sessionWall"
  // 任务 686: 工作流可视化面板（palette 入口开 DAG 流程图；685 引擎未落地
  // 为前端 fixture 预览）。纯前端门，保存即生效免重启。
  | "workflowPanel"
  // 任务 782：检索与历史合并卡（历史提问选择器 + 搜索历史提问 + 自动加载
  // 更早消息）——promptHistoryPicker/questionSearch/autoLoadOlder 独立 pane
  // 退役为卡内成员行。
  | "searchHistory"
  // 任务 742: tabCompress（506）+ tabModeTint（504/651）的独立 pane 退役——
  // 两行并入 tabManagement 合并卡（条目级合并，键语义/setter 零变化）。
  | "tabManagement"
  // 任务 742: heartbeat background mode（心跳触发不进标签栏，264 detached
  // 语义；默认关 = 原 742 前可见标签行为逐位不变）。任务 961：dream/
  // decisionPatterns/heartbeatBackground 三散条并入「智能自动化」合并卡
  // （smartAutomation，742/782 条目级合并先例）——三者独立 pane 退役为卡内
  // 成员行，union id 同步摘除。
  | "smartAutomation"
  // 任务 961：「交互与呈现」合并卡（interactionPresentation）——quickCommands/
  // traceAsState/outputStyle 三散条并卡，独立 pane 退役为卡内成员行。
  | "interactionPresentation"
  | "localServer"
  | "pathRules"
  | "sessionCollab"
  // Task 244 B4/B5, merged into one switch by task 449.
  | "orphanHandling"
  // Task 363A: runtime assembly reuse pool.
  | "runtimeReuse"
  // 任务 722：messageMerge 并入 safetyCostControl 卡（成员，无独立 pane）；
  // sessionCollabAutoFold 并入 sessionCollab 卡（成员，无独立 pane）——两个
  // id 从 pane union 摘除，卡片行/徽章走布局数据的成员登记。
  // Task 265 lab intake (9 fork features) + task 262 quick commands.
  // Task 163: OpenCode Go subscription usage card——任务 783：opencodeGoUsage
  // 独立 pane 退役，作为「用量监测」卡成员行（union id 同步摘除，742 先例）。
  // 任务 961：quickCommands/outputStyle 亦随「交互与呈现」合并卡退役（同上
  // 先例——union id 摘除，卡内成员行承载）。
  // Task 231: managed-path pre-approval (master switch + four checkboxes).
  // Task 364 folded the entry into the autopilot card but the id stays in
  // this union (561 did NOT fold it — restored by the 561 follow-up after
  // settings-preapprove-managed.test.ts caught the accidental drop).
  | "preapproveManagedPaths"
  // Task 280: the former permissions-area safety checkbox, re-homed here as
  // "optimistic parallel writes" (same underlying key, inverted checkbox).
  | "optimisticParallel"
  // 任务 782：输入与选区合并卡（草稿持久化 + 选区快捷操作）——
  // draftPersistence/selectionActions 独立 pane 退役为卡内成员行。
  | "inputSelection"
  | "completionSummary"
  // Task 385a: lab 回答风格 (output style selector + persistence)——任务 961
  // 随「交互与呈现」合并卡退役为卡内成员行（union id 摘除，742 先例）。
  // Task 257: full access (yolo).
  | "fullAccess"
  // 任务 790: resident ACP listener (loopback TCP, one client surface per
  // connection; default off, restart to apply).
  | "acpListen"
  // 任务 517:「安全 / 成本控制」— the task-244 B1/B2/B3 guards merge into ONE
  // config key (experimental_safety_cost_control): the 561 autonomousRunGuard
  // family card and the standalone eventWaitRecheck entry fold into this
  // single-switch card.
  | "safetyCostControl"
  // Task 561: the merged family-card ids (M1 autonomousRunGuard was absorbed
  // by safetyCostControl in task 517).
  | "modelStrategy"
  | "contextGovernance"
  | "subagentSuite"
  | "devDebug"
  | "updateFeedback"
  | "sessionStore"
  // 任务 846: change-data-home wizard card (storage group; unstable, default off).
  | "homePointer"
  // 任务 603:「工具优化」family (edit readBack + evidence gate linkage).
  // 任务 943：族拆两卡——本 id 收窄为 readBack 面；multi_edit 直注独立卡。
  // 任务 971：扩为聚合页——readBack + multi_edit 直注 + restart_update 三开关
  // 单页管理 + rev2 警告；multiEditInjection 独立 pane 退役为页内成员行
  // （键/setter/回落链零变化，943 两开关独立语义保持）。
  | "toolOptimizations"
  // 任务 677→856 ③: unified collaboration-hub entry switch (the mail-inbox
  // + group-chat icons merged into one; renamed from collabGroupView).
  | "collabHub"
  // 任务 775: kind:param 参数卡 id——无硬编码 pane，pane 走通用参数渲染器
  // （LabParamFieldsCard；字段绑定在 labParamFields 注册表）。
  | "idleReleaseMinutes"
  | "goMemLimit"
  // tl 1097：日志管理参数卡 id（可观测性组）——pane 由专用
  // LogManagementLabPane 承接（含当前占用行 + 打开目录按钮 + 「永久+无限制」
  // 警示行，超出通用参数渲染器覆盖面）；两个 segment 字段仍走 labParamFields
  // 注册表读写。
  | "logManagement"
  // 任务 783：「用量监测」合并卡（可观测性组）——GLM Plan 用量 + OpenCode
  // Go 用量（opencodeGoUsage 独立 pane 并入此卡，742 先例）。
  | "usageMonitoring"
  // tl 1132：启动与加载合并卡（界面组）——首项=启动遮罩（DSH 式启动画面），
  // 为 tl 1133 后续启动优化开关预留成员位（742 条目级合并先例）。
  | "startupLoading";

// 任务 724：pane 白名单——布局文档（yaml/默认）里的 entry.id 必须在此登记
// （一个 id 一个可点开的卡片分支）；yaml 引用白名单外的 id ⇒ 整份回退默认
// （防半份数据渲染/白屏）。任务 783 后仍 33 个 id（-opencodeGoUsage
// +usageMonitoring）与布局一一对应（722 后 37 → 782 三张合并卡 -7 独立
// pane +3 容器 id → 33；此后 686 workflowPanel / 846 homePointer 等陆续
// 入表，实数以 Set 为准）。任务 943：multiEditInjection（工具优化族拆分
// 独立卡）入表。
export const LAB_PANE_IDS: ReadonlySet<string> = new Set([
  "autopilot",
  "sessionCollab",
  "fullAccess",
  // 任务 961：dream/decisionPatterns/heartbeatBackground 三散条并入
  // smartAutomation 合并卡（成员行承载，独立 pane 退役）。
  "smartAutomation",
  // 任务 961：quickCommands/traceAsState/outputStyle 三散条并入
  // interactionPresentation 合并卡（成员行承载，独立 pane 退役）。
  "interactionPresentation",
  "optimisticParallel",
  "contextGovernance",
  "safetyCostControl",
  "modelStrategy",
  "collabHub",
  // tl 1132：启动与加载合并卡（首项=启动遮罩，1133 预留成员位）。
  "startupLoading",
  "tabManagement",
  "viewLayout",
  "todoSidebar",
  "searchHistory",
  "sessionWall",
  // 任务 686：工作流可视化面板 pane。
  "workflowPanel",
  "subagentSuite",
  "completionSummary",
  "inputSelection",
  "usageMonitoring",
  "updateFeedback",
  "monitoring",
  "devDebug",
  "sessionStore",
  // 任务 846: data-home migration wizard pane.
  "homePointer",
  "runtimeReuse",
  "acpListen",
  "pathRules",
  "orphanHandling",
  "localServer",
  "toolOptimizations",
  // 任务 971：multiEditInjection 独立 pane 退役（聚合页成员行，943 语义不变）。
  // 任务 775：kind:param 参数卡 id——pane 由通用参数渲染器承接（无独立
  // 硬编码分支）；登记进白名单与 feature 卡同一安全网语义（yaml 引用
  // 白名单外 id ⇒ 整份回退默认）。
  "idleReleaseMinutes",
  "goMemLimit",
  // tl 1097：日志管理参数卡（可观测性组）。
  "logManagement",
]);

// 任务 764：设置搜索覆盖实验室——rail 用什么布局，搜索词表就用什么布局。
// 同一 yaml 常量 + 同一 pane 白名单 resolve（yaml 损坏时同样整体回退内置
// 默认），搜索视图与 rail 永不漂移。resolveLabLayout 纯函数，模块级解析
// 一次（dev 模式改 yaml ?raw 热加载会重建模块，行为与组件内 useMemo 一致）。
export const LAB_LAYOUT_FOR_SEARCH = resolveLabLayout(labLayoutYaml, { allowedEntryIds: LAB_PANE_IDS }).layout;
