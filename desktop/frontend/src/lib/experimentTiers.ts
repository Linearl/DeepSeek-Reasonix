// 任务 562 — 实验室档位徽章注册表（前端展示镜像）。
//
// 权威源是 Go 侧 `labFeatureTiers`（internal/config/render.go，xlsx 表A
// 「实验室特性-档位与图墙-20261006」推荐等级列：推荐 15 / 可选 20 / 未稳定
// 10 / 已退役 1 = 46）。Go 侧有门禁（render.go 条目对账：渲染表新出现
// 未标档位的实验室键 ⇒ 测试失败）；本模块是唯一的展示事实源——设置页实验室
// tab（rail + 详情卡）与会话图墙（ForkFeaturesIntroDialog 精选区）两视图都
// 从这里取档位，徽章在两个视图间不可能失配。
//
// 用户口径（任务 1005 增第四档）：稳定 = 经发布验证的默认能力（默认开启，
// 桌面开关禁用态+开发者模式单项解锁，config 手写 false 仍生效）；推荐 =
// 核心特性，不开启会有体验缺口；可选 = 非核心，不开启只小幅影响；未稳定 =
// 开发测试中，开启可能不稳定；已退役 = 开关已退役。

import type { DictKey } from "./i18n";

export type LabTier = "stable" | "recommended" | "optional" | "unstable" | "retired";

/** Badge display order: strongest first. 任务 1005：稳定档为最强档
 * （经发布验证的默认能力压过一切「建议开启」类档位）。 */
export const LAB_TIER_ORDER: readonly LabTier[] = ["stable", "recommended", "optional", "unstable", "retired"];

/** Every 表A feature id (78 items as of tl 1165 — the register mirrors the Go
 * labFeatureTiers registry; see the LAB_TIER_COUNTS note for the items
 * 562's original 46 missed). The union is the type-level gate: a badge
 * can only ever reference a feature that carries a tier. */
export type TierFeatureId =
  | "autopilot"
  | "sessionCollab"
  | "fullAccess"
  | "optimisticParallel"
  | "dream"
  // 任务 801：决策模式自动提取+固化（提取链+固化链，默认关）。
  | "decisionPatterns"
  // 任务 1069：模型提前结束判定器续轮提醒（stalled_intent_nudge，117 首落地，
  // 本件补实验室设置页入口，默认关）。
  | "stalledIntentNudge"
  // 任务 1024：composer 草稿优化（改写按钮，默认关）。
  | "draftRewrite"
  // tl 1132: DSH 式启动遮罩（默认关——关时 splash 不渲染，启动行为与
  // 引入前一致）。落 config.toml experimental_startup_splash，Go 渲染表
  // 同档登记（可选）。
  | "startupSplash"
  // tl 1132: 「启动与加载」合并卡容器（首项=启动遮罩；为 tl 1133 后续
  // 启动优化开关预留成员位）。纯前端 rail 卡 id——没有自有配置键，卡内
  // 各行写各的键；Go 渲染表无条目（562 测试 ③ 的前端独有豁免，
  // tabManagement 先例）。
  | "startupLoading"
  // 任务 517:「安全 / 成本控制」单键卡（吸收 561 的 autonomousIdleTerminate +
  // loopStreakNote 与 standalone eventWaitRecheck 三个表A id → 一个 id）。
  | "safetyCostControl"
  | "subagentPolicy"
  // tl1193b: plan-E pinned standing-tier index mode (实验室「记忆与上下文」
  // 新组首卡；未稳定档，默认关——关时常驻注入与引入前逐字节一致)。
  | "pinnedIndex"
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
  // 任务 846: 配置数据目录可更改（home-pointer.json + 事务化迁移向导）。
  | "homePointer"
  // tl 1174: 冷会话 transcript zstd 归档（可选档，默认关——关时保存/加载
  // 行为与引入前一致；Go 渲染表同步登记 LabTierOptional）。
  | "jsonlZstd"
  | "runtimeReuse"
  // 任务 790: ACP 常驻监听（loopback TCP，每连接独立 client 面，默认关）。
  | "acpListen"
  | "modelCapabilityFilter"
  | "pathRules"
  | "orphanHandling"
  | "localServer"
  // 任务 603:「工具优化」族首件 (edit readBack + evidence gate linkage).
  // 任务 943：族拆两卡——本 id 收窄为 readBack 面；multi_edit 直注独立 id。
  | "toolOptimizations"
  // 任务 943: multi_edit 直注（原工具优化族第二效果拆出独立卡，可选档——
  // 不开启只小幅影响：multi_edit 仍可经 use_capability 调用）。
  | "multiEditInjection"
  // 任务 979: 能力组披露+session_hub 聚合件+常驻目录组归属（953/954/955
  // 共用一键，默认关=零行为，用户拍板提效类可选档）。toolOptimizations
  // 聚合页成员行。
  | "capabilityGroups"
  // tl-1218: multi_edit 三条增强（①错误详情增强 ②证据门连坐收窄 ③跳过
  // 失败项模式——三个独立开关，默认关重启生效）。toolOptimizations 聚合页
  // 成员行（与 multi_edit 直注成组成员行）。
  | "multiEditErrorDetail"
  | "multiEditEvidenceNarrow"
  | "multiEditSkipFailed"
  // 任务 677→856 ③: 协作中心入口总开关（信件中心+群聊两图标合一；原
  // 「群聊入口」开关 collabGroupView 随语义一次性迁移为 collabHub，未稳定档沿用）。
  | "collabHub"
  // 任务 705: 超长跨会话消息自动折叠（默认关=全量展示，可选档）。
  | "sessionCollabAutoFold"
  // 任务 707: 压缩模型指定（经济模型压缩，默认关，可选档）。
  | "compactModel"
  // 任务 704: 轨迹视图开关（DSH 同款可观测性视图，纯前端投影，默认关=仅转录，可选档）。
  | "trajectoryView"
  // 任务 727: 心跳会话轮换开关（桥接 heartbeat-rotation.json 的 enabled，
  // 不落 config.toml——前端独有注册项，无 Go 渲染表条目）。
  | "heartbeatRotation"
  // 任务 742: 「标签页管理」合并卡（506 自适应压缩 + 651 权限指示的条目级
  // 合并容器）。纯前端 rail 卡 id——没有自有配置键，卡内两行各写各的键；
  // Go 渲染表无条目（562 测试 ③ 的前端独有豁免）。
  | "tabManagement"
  // 任务 742: 心跳任务后台化（默认关；开启后心跳触发不进标签栏，复用
  // 264 detached 语义）。落 config.toml，Go 渲染表同档登记。
  | "heartbeatBackground"
  // 任务 1070: Standard 流续跑总闸（默认关；开启后普通对话续跑通道可达，
  // 有界上限/无进展即停/planMode 只读等边界不变）。落 config.toml
  // standard_continuation，Go 渲染表同档登记。
  | "standardContinuation"
  // 任务 782: 界面组三张条目级合并卡（742 tabManagement 先例）——
  // searchHistory（检索与历史：promptHistoryPicker+questionSearch+autoLoadOlder）、
  // inputSelection（输入与选区：draftPersistence+selectionActions）、
  // viewLayout（视图布局：splitView+trajectoryView）。纯前端 rail 容器
  // id——没有自有配置键，卡内各行各写各的键；Go 渲染表无条目（562 测试 ③
  // 的前端独有豁免，同 tabManagement）。
  | "searchHistory"
  | "inputSelection"
  | "viewLayout"
  // 任务 783: 可观测性组「用量监测」合并卡容器 id（纯前端，无配置键）+
  // GLM Plan 用量子项（PlanUsageCard 已上线、provider 解析显示，无开关——
  // 展示行）。coding plan preset 家族（GLM CN / Z.AI Global / Qwen CN·Global /
  // Kimi 等 7+ 家）未来按需以成员身份扩展（布局 members 加行即可，Go 渲染
  // 表无条目，同 tabManagement 豁免）。
  | "usageMonitoring"
  | "glmPlanUsage"
  // tl 1097: 可观测性组「日志管理」参数卡容器 id（纯前端，无配置键）——
  // 两个 segment 档位 + 当前占用 + 打开目录按钮，Go 渲染表无条目（同
  // tabManagement 豁免）。
  | "logManagement"
  // 任务 789: 编辑证据门宽松模式（toolOptimizations 卡成员；默认关=严格
  // 行为逐字节一致）。lab-layout.yaml members + 本表双登记（562 口径）。
  | "editEvidenceRelaxed"
  // 任务 826: 左右边栏 hover 展开（默认关=不渲染热区不挂事件，可选档）。
  // 左右独立两键两行，均归界面组「视图布局」卡（782 合并卡先例）。
  | "sidebarHoverLeft"
  | "sidebarHoverRight"
  // 任务 940: composer 权限选择器形态二选一（默认关=现按钮组，开=下拉框
  // zcode 同款省空间）。界面组「视图布局」卡成员（782/826 先例）。
  | "approvalSelect"
  // 任务 959 ①: 对话 markdown 表格按栏宽自动换行（默认关=宽表横滚不变，
  // 分栏拖窄后随新宽度重排）。界面组「视图布局」卡成员（940 先例）。
  | "tableWrap"
  // 任务 959 ②: composer 发送目标选择器形态二选一（默认关=三按钮组，开=
  // 下拉框，复用 940 形态机制）。界面组「视图布局」卡成员。
  | "sendTargetSelect"
  // tl 1165: 流式滚动平滑跟随（DSH ScrollFollow 同款：smooth 跟随+未完成
  // 目标不发新请求+手势取消+reduced-motion 退化；默认关=瞬移定位逐字节
  // 不变）。界面组「视图布局」卡成员。
  | "smoothScrollFollow"
  // 任务 686: 工作流可视化面板（DAG 流程图 + 运行进展点亮，palette 入口；
  // 后端 685 编排引擎未落地，当前为前端 fixture 预览）。未稳定档（开发
  // 测试中），默认关=palette 逐字节不变。
  | "workflowPanel"
  // 任务 961: automation/efficiency 两张条目级合并卡容器 id（742/782 先例，
  // 纯前端容器、无自有配置键，卡内各行各写各的键；Go 渲染表无条目，同
  // tabManagement 豁免）——smartAutomation（智能自动化：梦境整理+决策模式
  // 提取+心跳任务后台化）、interactionPresentation（交互与呈现：快捷指令+
  // 轨迹状态+回答风格）。
  | "smartAutomation"
  | "interactionPresentation";

export const EXPERIMENT_FEATURE_TIERS: Readonly<Record<TierFeatureId, LabTier>> = {
  // ── automation（自动化，6 项；任务 650：optimisticParallel 迁提效）──
  // 任务 1005 批1 转档：autopilot / sessionCollab / fullAccess（及 infra 段
  // runtimeReuse）升「稳定」——镜像 Go labFeatureTiers（562 双侧一致门禁）。
  autopilot: "stable",
  sessionCollab: "stable",
  fullAccess: "stable",
  dream: "optional",
  // 任务 801：决策模式（提取链+固化链）。归自动化组，档位可选。
  decisionPatterns: "optional",
  // 任务 1069：模型提前结束判定器续轮提醒（stalled_intent_nudge）——归自动化
  // 组，档位可选（铁律 2，默认关）。
  stalledIntentNudge: "optional",
  safetyCostControl: "optional",
  subagentPolicy: "unstable",
  // ── memory-context（记忆/上下文，1 项；tl1193b 增 pinnedIndex）──────
  // tl1193b: pinned 常驻层索引化——镜像 Go 渲染表 LabTierUnstable（562 门禁
  // 双侧一致）。boot 快照消费，重启生效。
  pinnedIndex: "unstable",
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
  // 任务 677→856 ③：协作中心入口总开关（677 群聊入口开关随两图标合一迁移
  // 改名，未稳定档默认关沿用）。
  collabHub: "unstable",
  // 任务 705：超长跨会话消息自动折叠（默认关=全量展示）。展示类开关按 550
  // 口径定档可选——不开启只影响长消息的阅读密度，不动任何协作行为。
  sessionCollabAutoFold: "optional",
  // 任务 707：压缩模型指定（上下文治理卡 compressOpt 下方，默认关）。按任务
  // 书建议定档可选——不开启不影响体验，只影响压缩成本。
  compactModel: "optional",
  // 任务 704：轨迹视图（DSH 同款可观测性视图，纯前端投影）。展示类开关按
  // 550 口径定档可选——不开启时标题栏与转录视图与引入前完全一致（铁律 2，
  // 默认关）。
  trajectoryView: "optional",
  // ── ui（界面，14 项）────────────────────────────────────────
  // 任务 1024：composer 草稿优化。镜像 Go 档位——可选（默认关，输入栏零变化）。
  draftRewrite: "optional",
  // tl 1132：启动遮罩。镜像 Go 档位——可选（默认关，用户 steer A/B 对比）。
  startupSplash: "optional",
  // tl 1132：「启动与加载」合并卡容器（纯前端 rail 卡，成员最高档=可选）。
  startupLoading: "optional",
  tabCompress: "recommended",
  todoSidebar: "recommended",
  promptHistoryPicker: "recommended",
  restartUpdate: "recommended",
  sessionWall: "recommended",
  // 任务 686：工作流可视化面板（palette 入口开 DAG 流程图；685 引擎未落地
  // 为前端 fixture 预览）。镜像 Go 档位——未稳定（开发测试中）。
  workflowPanel: "unstable",
  subagentPanel: "optional",
  subagentDetail: "optional",
  completionSummary: "optional",
  autoLoadOlder: "optional",
  splitView: "optional",
  draftPersistence: "optional",
  selectionActions: "optional",
  questionSearch: "recommended",
  opencodeGoUsage: "recommended",
  feedback: "optional",
  // 任务 621：tabModeTint（任务 504 标签模式色调）在 Go labFeatureTiers 一直
  // 有档（unstable），但前端注册表漏收——实验室页渲染却无徽章，621 验收
  // 「每项特性有且仅有一个徽章」不允许。档位镜像 Go 侧。
  tabModeTint: "unstable",
  // ── observability（可观测性，2 项；tl 1097 增 logManagement 参数卡）──
  monitoring: "recommended",
  subagentTps: "optional",
  // tl 1097: 日志管理（logManagement）——告警日志时间/大小档位参数卡（纯前端
  // rail 容器 id，无配置键，同 tabManagement/usageMonitoring 豁免口径）。档位
  // 按任务书建议 optional（不开启只影响日志留存，不影响主流程）。
  logManagement: "optional",
  // 任务 783：「用量监测」合并卡容器档 = 成员最高档（GLM Plan 用量=推荐、
  // OpenCode Go 用量=未稳定 → 推荐）。coding plan 架构子项按需扩展。
  usageMonitoring: "recommended",
  // 任务 783：GLM Plan 用量子项（PlanUsageCard 已上线，287 provider 解析
  // 显示、666 显示条件）——已上线能力按推荐档登记（无开关，展示行）。
  glmPlanUsage: "recommended",
  // ── dev-debug（开发调试，2 项）───────────────────────────────
  cdpDebugPort: "unstable",
  lifecycleNoiseGate: "unstable",
  // ── storage（存储，4 项；846 增 homePointer，tl-1174 增 jsonlZstd）────
  eventsRotation: "optional",
  sessionStorage: "unstable",
  homePointer: "unstable",
  // tl 1174: 冷会话 transcript zstd 归档——档位与 Go 渲染表 LabTierOptional
  // 双侧一致（562 门禁）。
  jsonlZstd: "optional",
  // ── infra（基础设施，6 项；785 退役 S1 常驻底座开关，790 增 acpListen，tl-1048 退役 zcodeTaskBus）──────
  // 任务 1005 批1 转档：runtimeReuse 升「稳定」（992 刚升推荐的四项之一）。
  runtimeReuse: "stable",
  acpListen: "unstable",
  // 任务 789: 编辑证据门宽松模式（放宽 evidence gate 属行为变更；
  // toolOptimizations 卡成员）。任务 992：用户 1018 升可选——本注册表
  // 2026-10-19 随 1005 一并补同步（992 漏同步 drift 收口）。
  editEvidenceRelaxed: "optional",
  modelCapabilityFilter: "retired",
  pathRules: "unstable",
  // 任务 992：orphanHandling 随复用池升推荐（用户 1018）——本注册表
  // 2026-10-19 随 1005 一并补同步（992 漏同步 drift 收口）。
  orphanHandling: "recommended",
  localServer: "unstable",
  // ── tool-opt（工具优化，3 项；任务 943 拆分 + 979 capabilityGroups）──
  // 任务 783④：编辑读回（readBack）随 22.5 交付转正——未稳定→推荐
  // （Go 渲染表同步升档，562 两侧一致断言守护）。任务 943：本 id 收窄
  // 为 readBack 面（含 789 证据门联动成员）。
  toolOptimizations: "recommended",
  // 任务 943：multi_edit 直注独立卡（原族卡第二效果拆出）。可选档——按
  // 550 口径：不开启只小幅影响（multi_edit 仍可经 use_capability 调用，
  // edit_file 批量编辑引导降级），非体验缺口。
  multiEditInjection: "optional",
  // 任务 979：能力组披露+session_hub 聚合件（953/954/955 共用一键）。用户
  // 拍板提效类可选档（config.go 注释原文）；默认关=零行为（铁律 2），九件
  // 协作工具保持直挂。toolOptimizations 聚合页成员行，重启生效。
  capabilityGroups: "optional",
  // tl-1218：multi_edit 三条增强（①错误详情增强 ②证据门连坐收窄 ③跳过
  // 失败项模式）。可选档——不开启只小幅影响（拦截信息少附行内容/连坐维持/
  // 单点失败即整批中止）；默认关=与基线逐字节一致（铁律 2）。聚合页成员
  // 行，重启生效。
  multiEditErrorDetail: "optional",
  multiEditEvidenceNarrow: "optional",
  multiEditSkipFailed: "optional",
  // 任务 727：心跳会话轮换（自动化/成本域）。桥接 heartbeat-rotation.json
  // 的 enabled 字段（缺文件=内置默认开）——「不开启只小幅影响」定可选档。
  heartbeatRotation: "optional",
  // 任务 742：「标签页管理」合并卡（条目级合并 506+651）。合并是入口层
  // 组织，不降级任何成员特性（tabCompress 仍推荐、tabModeTint 仍未稳定，
  // 见各自条目）；容器卡本身按任务书定可选档。
  // tl 1125：标签页管理合并卡升「推荐」（用户 2026-10-24 指令：标签管理/
  // 视图布局两特性徽章档位提升为推荐——推荐 = 核心特性，不开启体验有缺口）。
  tabManagement: "recommended",
  // 任务 742：心跳任务后台化（默认关）。按 550 口径定可选——不开启不影响
  // 任何调度行为，只是心跳会话照旧出现在标签栏。
  heartbeatBackground: "optional",
  // 任务 1070：Standard 流续跑总闸（默认关）。不开启=续跑通道维持现状
  // （普通对话永不自动续跑）；开启后才有界续跑。可选档。
  standardContinuation: "optional",
  // 任务 782：界面组三张条目级合并卡的容器档 = 卡内成员最高档（774 聚合
  // 语义同源：searchHistory 三成员 两推荐一可选 → 推荐；inputSelection /
  // viewLayout 全可选成员 → 可选）。合并是入口层组织，不降级任何成员特性。
  searchHistory: "recommended",
  inputSelection: "optional",
  // tl 1125：视图布局合并卡升「推荐」（tl 1125 同指令，与 tabManagement 一并）。
  viewLayout: "recommended",
  // 任务 826：左右边栏 hover 展开（左右各一行，独立开关，默认关）。按 550
  // 口径定可选——不开启时不渲染任何热区、不挂任何事件，左右栏行为与引入前
  // 完全一致（铁律 2）。
  sidebarHoverLeft: "optional",
  sidebarHoverRight: "optional",
  // 任务 940：composer 权限选择器形态二选一（默认关=现按钮组）。展示形态
  // 开关按 550 口径定可选——不开启时 composer 权限区与引入前逐字节一致
  // （铁律 2）。
  approvalSelect: "optional",
  // 任务 959 ①：表格按栏宽自动换行（默认关）。展示形态开关按 550 口径定
  // 可选——不开启时转录表格与引入前逐字节一致（铁律 2）。
  tableWrap: "optional",
  // 任务 959 ②：composer 发送目标形态二选一（默认关=三按钮组）。展示形态
  // 开关按 550 口径定可选——不开启时发送目标区与引入前逐字节一致（铁律 2）。
  sendTargetSelect: "optional",
  // tl 1165：流式滚动平滑跟随（DSH ScrollFollow 同款四条防线）。展示形态
  // 开关按 550 口径定可选——不开启时流式滚动与引入前逐字节一致（铁律 2）。
  smoothScrollFollow: "optional",
  // 任务 961：automation/efficiency 两张条目级合并卡容器档 = 成员最高档
  // （782 合并卡语义同源：smartAutomation 三成员全可选 → 可选；
  // interactionPresentation 快捷指令推荐压过两可选 → 推荐）。合并是入口层
  // 组织，不降级任何成员特性。
  smartAutomation: "optional",
  interactionPresentation: "recommended",
};

/** 表A distribution, pinned by tests on BOTH sides (Go: render.go labFeatureTiers,
 * frontend: task562 test) — 推荐 15 / 可选 20 / 未稳定 13 / 已退役 1.
 * (任务 621 修正：原钉 46 项未收 toolOptimizations（603）与 tabModeTint（504，
 * Go 侧一直有档），漏收使实验室页出现无徽章特性。任务 517：B1/B2/B3（可选×3）
 * 合并为 safetyCostControl（可选×1），可选 20→18、总数 48→46。任务 677：
 * collabGroupView（群聊入口开关，409 交付漏挂铁律 2 开关，未稳定）入表，
 * 未稳定 12→13、总数 46→47。任务 705：sessionCollabAutoFold（超长跨会话
 * 消息自动折叠，默认关）入表，可选 18→19、总数 47→48。任务 707：compactModel
 * （压缩模型指定，默认关，可选档）入表，可选 19→20、总数 48→49。
 * trajectoryView（轨迹视图，默认关）入表，可选 20→21、总数 49→50。
 * 任务 727：heartbeatRotation（心跳会话轮换，桥接 JSON 键）入表，可选
 * 21→22、总数 50→51。任务 742：tabManagement（标签页管理合并卡，纯前端
 * 容器 id）与 heartbeatBackground（心跳任务后台化，默认关）入表，可选
 * 22→24、总数 51→53。任务 782：界面组三张合并卡容器 id 入表（searchHistory
 * 推荐 + inputSelection / viewLayout 可选），推荐 15→16、可选 24→26、
 * 总数 53→56。任务 783：可观测性组「用量监测」卡容器 + GLM Plan 用量子项
 * 入表（推荐×2），推荐 16→18、总数 56→58；同任务编辑读回（toolOptimizations）
 * 随 22.5 交付转正，未稳定 13→12、推荐 18→19、总数不变 58。任务 785：
 * S1 常驻底座开关随底座退役出表，未稳定 12→11、总数 58→57。任务 790：acpListen（ACP 常驻监听，默认关）入表未稳定档，11→12、总数 57→58。
 * 任务 826：sidebarHoverLeft / sidebarHoverRight（左右边栏 hover 展开，默认
 * 关）按可选档入表（26→28、58→60；562 计数 pin 与 789 统一登记窗口一并
 * 收口——本常量当前被 task562 钉在 26/58，随登记窗口同步跳变）。
 * 任务 815（统一登记窗口收口）：editEvidenceRelaxed（编辑证据门宽松，789
 * 交付、815 补设置页入口，Go 侧同步转正入表）档位不变（未稳定）；本常量
 * 自收口前口径 19/26/12/1=58 一次跳到 19/29/14/1=63（sidebarHover×2 +
 * editEvidenceRelaxed + homePointer 等收口前已在表而 pin 未跟的项一并计入；
 * Go 侧 56 条目，详见 task562 测试头注）。）
 * 任务 686：workflowPanel（工作流可视化面板，未稳定档）入表——未稳定
 * 14→15、总数 63→64（Go 侧同步 56→57）。任务 943：multiEditInjection
 * （multi_edit 直注，工具优化族拆分独立卡）按可选档入表——可选 29→30、
 * 总数 64→65（Go 侧同步 57→58）。任务 940：approvalSelect（composer 权限
 * 选择器形态二选一，默认关）按可选档入表——可选 30→31、总数 65→66
 * （Go 侧同步 58→59；与 943 先后入表各 +1）。任务 944：renderArtifact
 * （对话内产物卡片）按可选档入表——可选 31→32、总数 66→67（Go 侧同步
 * 59→60）。 任务 961：smartAutomation（智能自动化合并卡，纯前端容器 id）
 * 按可选档入表——可选 32→33；interactionPresentation（交互与呈现合并卡，
 * 纯前端容器 id）按推荐档入表——推荐 19→20；总数 67→69（Go 渲染表无条目，
 * 同 tabManagement 豁免）。任务 979：capabilityGroups（能力组披露+
 * session_hub 聚合件，953/954/955 共用一键）按可选档入表——可选 33→34、
 * 总数 69→70（Go 侧同步 60→61）。任务 1005：autopilot / sessionCollab /
 * fullAccess / runtimeReuse 四项升「稳定」（新增第四档）——前端此前口径
 * 20 推荐/34 可选/15 未稳定/1 退役：推荐 -3（三 automation 项）+1（992
 * 漏同步的 orphanHandling 升推荐）、可选 -1（992 漏同步的 runtimeReuse 本
 * 仓仍挂可选）+1（992 漏同步的 editEvidenceRelaxed 升可选）、未稳定 -2
 * （orphanHandling / editEvidenceRelaxed 原挂未稳定）→ 4 稳定/18 推荐/
 * 34 可选/13 未稳定/1 退役，总数不变 70（Go 侧 61 条目：runtimeReuse 992
 * 已升推荐，故 14 推荐/4 稳定/29 可选/14 未稳定）。 * 任务 959：① tableWrap（对话表格按栏宽自动换行，默认关）+ ②
 * sendTargetSelect（composer 发送目标形态二选一，默认关）按可选档入表
 * ——可选 34→36、总数 70→72（Go 侧同步 61→63）。任务 1024：draftRewrite
 * （composer 草稿优化）在其上叠加——可选 36→37、总数 72→73（Go 61→64）。
 * 任务 1072：renderArtifact（对话内产物卡片）整功能移除出表——可选 37→36、
 * 总数 73→72（Go 侧同步 64→63）。
 * 任务 1069：stalledIntentNudge（模型提前结束判定器续轮提醒，默认关）按可选
 * 档入表——可选 36→37、总数 72→73（Go 侧同步 63→64）。
 * 任务 1070：standardContinuation（Standard 流续跑总闸）按可选档入表——
 * 可选 37→38、总数 73→74（Go 侧同步 64→65）。
 * tl 1097：logManagement（日志管理，纯前端容器 id，无配置键）按可选档入表
 * ——可选 38→39、总数 74→75（Go 渲染表无条目，同 tabManagement 豁免）。
 * tl 1125：tabManagement（标签页管理合并卡，纯前端容器 id）+ viewLayout
 * （视图布局合并卡，同为纯前端容器 id）两特性档位「可选 → 推荐」（用户
 * 2026-10-24 指令：推荐 = 核心特性，不开启体验有缺口）——推荐 18→20、
 * 可选 39→37、总数不变 75（两 id 均为纯前端容器，Go 渲染表无条目，registry
 * 计数不变仍 66，同 tabManagement 豁免）。
 * tl 1132：startupSplash（启动遮罩，可选，Go 渲染表同步 +1 → 67）+
 * startupLoading（「启动与加载」纯前端容器 id，同 tabManagement 豁免）——
 * 可选 37→39、总数 75→77。
 * tl 1165：smoothScrollFollow（流式滚动平滑跟随，DSH ScrollFollow 同款四条
 * 防线，默认关）按可选档入表——可选 39→40、总数 77→78（Go 渲染表同步
 * 67→68）。
 * tl-1218：multi_edit 三条增强（multiEditErrorDetail / multiEditEvidenceNarrow /
 * multiEditSkipFailed，toolOptimizations 聚合页成员行）按可选档入表——
 * 可选 +3（Go 渲染表同步 69→72）。⚠️ 本段计数经 2026-10-28 合并线并集解
 * 回调：基线 8552b0abf 上 zcode 落 77+3=80（unstable 12），滚合后主线已有
 * jsonlZstd 补登（58c19737d，optional 非 unstable）⇒ 并集态 81 项/可选 44/
 * 未稳定 12（zcode 预警的「81/13」中 13 系误算，已按实值修正）。
 * tl1193b：pinnedIndex（pinned 常驻层索引化，实验室新「记忆与上下文」组首卡）
 * 按未稳定档入表——未稳定 12→13、总数 81→82（Go 渲染表同步 72→73）。 */
export const LAB_TIER_COUNTS: Readonly<Record<LabTier, number>> = {
  stable: 4,
  recommended: 21,
  optional: 44,
  unstable: 12,
  retired: 1,
};

// 任务 722/724：合并卡的成员表（card id → 表A member ids）移交实验室布局
// 数据（src/lab/labLayoutDefault.ts，yaml 化的内置默认）——徽章档位、分组
// 归属、排序都从同一份布局数据来，不再双源维护。本文件保留档位注册表、
// 图墙清单与墙位图。

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
  // 任务 722：消息合并并入「安全 / 成本控制」卡（卡随 722 归提效组）。
  messageMerge: ["settings.labGroup.efficiency", "settings.safetyCostControl"],
  // ── ui（界面）───────────────────────────────────────────────
  sessionWall: ["settings.labGroup.ui", "settings.sessionWall"],
  // 任务 742：自适应压缩并入「标签页管理」卡——设置位置指到新卡。
  tabCompress: ["settings.labGroup.ui", "settings.tabManagement"],
  todoSidebar: ["settings.labGroup.ui", "settings.todoSidebar"],
  // 任务 782：检索与历史合并卡——历史提问选择器成员的设置位置指到新卡
  // （742 tabCompress → tabManagement 同款）。
  promptHistoryPicker: ["settings.labGroup.ui", "settings.searchHistory"],
  restartUpdate: ["settings.labGroup.ui", "settings.updateFeedback"],
  // 任务 782：分栏并入「视图布局」卡——设置位置指到新卡。
  splitView: ["settings.labGroup.ui", "settings.viewLayout"],
  subagentPanel: ["settings.labGroup.ui", "settings.subagentSuite"],
  // 任务 782：选区快捷操作并入「输入与选区」卡——设置位置指到新卡。
  selectionActions: ["settings.labGroup.ui", "settings.inputSelection"],
  completionSummary: ["settings.labGroup.ui", "settings.completionSummary"],
  // ── observability（可观测性）─────────────────────────────────
  monitoring: ["settings.labGroup.observability", "settings.monitoring"],
};

/**
 * 任务 856 ③ — 实验室开关前置依赖表（键=依赖者，值=被依赖的前置开关）。
 *
 * demo v2 交互稿口径：跨会话协作（sessionCollab）关闭时，协作中心开关呈
 * 禁用态并提示「需先开启跨会话协作」。声明在此而非散在设置页——后续其他
 * 开关要挂前置时加一行即可，渲染侧统一处理禁用态（856 计划文档 ③）。
 * 单前置一期够用（与一期单副管理员同一简化原则）；多条依赖再演进为数组。
 */
export const LAB_FEATURE_DEPS: Readonly<Partial<Record<TierFeatureId, TierFeatureId>>> = {
  collabHub: "sessionCollab",
};

export function isTierFeatureId(id: string): id is TierFeatureId {
  return Object.prototype.hasOwnProperty.call(EXPERIMENT_FEATURE_TIERS, id);
}

/** 任务 563 — 「建议开启」badge rule (xlsx 表B W4): ONLY a recommended-tier
 * pick that is currently off. Optional/unstable never nag; an on pick never
 * nags. Pure so both card and dialog call the one rule. */
export function suggestEnable(tier: LabTier, on: boolean): boolean {
  return tier === "recommended" && !on;
}

/** Locale keys for tier labels (DictKey-checked literals — a typo here is a
 * compile error once the keys exist in locales/*). */
export const LAB_TIER_LABEL_KEYS: Readonly<Record<LabTier, "settings.labTier.stable" | "settings.labTier.recommended" | "settings.labTier.optional" | "settings.labTier.unstable" | "settings.labTier.retired">> = {
  stable: "settings.labTier.stable",
  recommended: "settings.labTier.recommended",
  optional: "settings.labTier.optional",
  unstable: "settings.labTier.unstable",
  retired: "settings.labTier.retired",
};

/** 任务 621 — tooltip text keys: one line per tier saying what the tier means
 * (口径出处 2026-10-06 用户定：推荐 = 核心特性、不开启会有体验缺口；可选 =
 * 非核心增强；未稳定 = 开发测试中、开启可能不稳定；任务 1005：稳定 = 经
 * 发布验证的默认能力). Same DictKey-checked literal discipline as
 * LAB_TIER_LABEL_KEYS. */
export const LAB_TIER_DESC_KEYS: Readonly<Record<LabTier, `settings.labTier.${LabTier}.desc`>> = {
  stable: "settings.labTier.stable.desc",
  recommended: "settings.labTier.recommended.desc",
  optional: "settings.labTier.optional.desc",
  unstable: "settings.labTier.unstable.desc",
  retired: "settings.labTier.retired.desc",
};
