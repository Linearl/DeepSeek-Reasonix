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
  // tl1453："dream" 随 dream/distill 实验退役（宪法 Q1），出类型与档位表。
  // 任务 801：决策模式自动提取+固化（提取链+固化链，默认关）。
  | "decisionPatterns"
  // tl1772：决策模式注入分层（decisionPatterns 伴生开关，可选档，默认关=
  // 原全量注入逐字节一致；开=索引行+按需读全文硬指令）。
  | "decisionPatternsLayered"
  // 任务 1069：模型提前结束判定器续轮提醒（stalled_intent_nudge，117 首落地，
  // 本件补实验室设置页入口，默认关）。
  | "stalledIntentNudge"
  // 任务 1024：composer 草稿优化（改写按钮，默认关）。
  | "draftRewrite"
  // tl1544：运行中编辑历史消息回溯重发（默认关——运行中编辑入口维持拒绝现状）。
  | "editResend"
  // tl1718：composer 工具组默认折叠（宽窗口同样折叠为单图标，工具全集在既有
  // 「⋯ 工具」二级菜单查看；默认关=折叠仍只由宽度驱动，铁律 2）。界面组
  // 「视图布局」卡成员（940/959 先例）。
  | "composerToolsFold"
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
  // tl1816：卡移组安全/成本控制卡成员行（tl1772 memoryIndexSlim 同款），
  // 注册表标注保留（562 展示面），档位不动。
  | "pinnedIndex"
  // tl1733: 记忆注入瘦身（实验室「提效」组新卡；可选档，默认关——关=默认
  // 瘦身注入管线；开=应急旁路仅注入 pinned）。boot 快照消费，重启生效。
  | "memoryIndexSlim"
  | "budgetControl"
  | "compressOpt"
  | "messageMerge"
  | "quickCommands"
  | "highSpeedModel"
  // tl1816：备用模型开关行（722 点1 迁入模型策略卡；键 experimental_fallback_model）
  // 挂独立「可选」徽章——三档徽章体系对齐任务书③。Go 渲染表无条目（前端
  // 独有，562 测试 ③ feOnly 豁免清单收录——模型目标行在设置→模型偏好）。
  | "fallbackModel"
  | "compactionParallel"
  | "traceAsState"
  | "outputStyle"
  // tl1817: 朗读 MiMo TTS 传输层（「交互与呈现」卡成员行，可选档，默认关=
  // 系统 TTS 逐字节一致；配 key+开开关才走 MiMo，失败自动降级回系统链）。
  | "mimoTts"
  | "cacheTuning"
  // tl1487 一组④：冷缓存压缩（compressOpt 主开关的附属开关行，pane 内
  // 冷缓存压缩+触发上下文下限+闲置触发时长三行并一卡；本件起主行挂独立
  // 可选徽章——用户 20261107 八组清单「冷缓存压缩→可选」）。Go 渲染表无
  // 条目（附属键随 compressOpt 族，562 测试 ③ 前端独有豁免清单收录）。
  | "coldCacheCompact"
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
  // tl1487 一组③：modelCapabilityFilter（模型能力过滤）随只读展示行一并
  // 出表出类型——用户 20261107 八组清单「移除（不展示）」；Go 配置键仍存在
  // （退役面，render 表早已无条目），本件只摘前端展示与档位登记。
  // tl1487 七组③：pathRules 随 tl134 整卡移除出表出类型（未稳定档；配置键
  // 整链退役，存量键走 deprecated 清单）。
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
  // 「群聊入口」开关 collabGroupView 随语义一次性迁移为 collabHub，未稳定档
  // 沿用）。tl1487 一组①：升稳定档（用户 20261107 八组清单；前置依赖
  // sessionCollab 不变——稳定档语义「默认开启」在此特例豁免：collabHub 默认
  // 关+开有关前置门控，档位只表达成熟度，pane 开关不接 labStableLocked）。
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
  // tl1746: 可观测性组「任务面板」卡 id（Go 渲染表有同名条目，unstable，
  // 默认关=两入口零渲染）——开关 + 数据库路径子项 + 测试连接/打开所在位置。
  | "tasklistPanel"
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
  // 后端 685 编排引擎未落地，当前为前端 fixture 预览）。
  // tl1487 二组⑤：未稳定→稳定（用户 20261107 八组清单；默认关与 fixture
  // 预览语义不变，档位只表达成熟度裁决）。
  | "workflowPanel"
  // tl1487 二组⑤：工作流模板（tl1454 工作流面板家族子键的 pane 附属行）升
  // 独立登记推荐档——用户 20261107 八组清单「工作流模板→推荐」（与面板主键
  // 分档：面板稳定、模板推荐）。Go 渲染表无独立条目（键仍为 workflowPanel
  // 家族子键，562 ③ 前端独有豁免清单收录）。
  | "workflowTemplates"
  // 任务 961: automation/efficiency 两张条目级合并卡容器 id（742/782 先例，
  // 纯前端容器、无自有配置键，卡内各行各写各的键；Go 渲染表无条目，同
  // tabManagement 豁免）——smartAutomation（智能自动化：梦境整理+决策模式
  // 提取+心跳任务后台化）、interactionPresentation（交互与呈现：快捷指令+
  // 轨迹状态+回答风格）。
  | "smartAutomation"
  | "interactionPresentation"
  // tl1309 卡②：「运行可观测」合并卡容器 id（可观测性组，961 先例纯前端
  // 容器、无自有配置键）——监控（monitoring，会话+性能数据面）+ events 瘦身
  // （eventsRotation，事件日志轮转面）+ 轨迹观测（trajectoryView，轨迹投影
  // 面）三散条/迁出成员并卡（任务书②：同属 events/perf 数据面）。Go 渲染
  // 表无条目，同 tabManagement 豁免。
  | "runtimeObservability"
  // tl1309 卡③：「资源治理」合并卡容器 id（基础设施组，同 tabManagement
  // 纯前端容器豁免）——运行时复用池/孤儿回收/ACP 常驻监听/本地服务/GC 策略/
  // 闲置释放六成员并卡（任务书③：同为内存/资源生命周期面）。
  | "resourceGovernance"
  // tl1309 卡④：「会话与任务面板」合并卡容器 id（界面组，同 tabManagement
  // 纯前端容器豁免）——会话图墙/工作流面板/任务完成摘要三散条并卡（任务书④
  // 按先例归卡，三面板同属运行状态可视化面）。
  | "sessionTaskPanels"
  // tl1309 卡③：GC 策略旋钮（tl1286 参数卡）入表——原 kind:param 独立卡，
  // 档位可选随迁（rail 徽章与并入前一致）。本件起以「资源治理」卡成员行
  // 承载（字段绑定仍在 labParamFields，值语义单源）。Go 渲染表无条目（775
  // 参数卡同款豁免）。
  | "gcPercent"
  // tl1309 卡③：后台会话闲置释放（775 首批参数卡）入表——推荐档随迁，同上。
  | "idleReleaseMinutes"
  // tl1787 件⑤/A：tl1397 ranges 主动局部压缩（Go 渲染表既有条目转两侧共享
  // ——此前 Go-only config-only 豁免，设置页入口落地时收回）。
  | "rangesCompact"
  // tl1419 模型驱动 ranges 折叠（同上口径转两侧共享；窗口两键为家族子键，
  // 不另计条目）。
  | "modelRangesCompact"
  // tl1787 续作：pre-repair 分叉后台元信息拼接（Go 渲染表新条目两侧共享，
  // 未稳定档）。
  | "prepairAutosplice";

export const EXPERIMENT_FEATURE_TIERS: Readonly<Record<TierFeatureId, LabTier>> = {
  // ── automation（自动化，6 项；任务 650：optimisticParallel 迁提效）──
  // 任务 1005 批1 转档：autopilot / sessionCollab / fullAccess（及 infra 段
  // runtimeReuse）升「稳定」——镜像 Go labFeatureTiers（562 双侧一致门禁）。
  autopilot: "stable",
  sessionCollab: "stable",
  fullAccess: "stable",
  // tl1453：dream 出表（原可选档）——dream/distill 实验退役（宪法 Q1）。
  // 任务 801：决策模式（提取链+固化链）。归自动化组，档位可选。
  decisionPatterns: "optional",
  // tl1772：注入分层伴生开关（decisionPatterns 行下方，可选档，默认关）。
  decisionPatternsLayered: "optional",
  // 任务 1069：模型提前结束判定器续轮提醒（stalled_intent_nudge）——归自动化
  // 组，档位可选（铁律 2，默认关）。
  stalledIntentNudge: "optional",
  safetyCostControl: "optional",
  // tl1265 第2轮（2026-11-13 拍板 dec-73872eee136e22de）：未稳定→可选——
  // 四采样点 ≥21 天持续开启零报障，子代理族 4 件报障均非本开关语义（已 done）。
  subagentPolicy: "optional",
  // ── memory-context（记忆/上下文，1 项；tl1193b 增 pinnedIndex）──────
  // tl1193b: pinned 常驻层索引化——boot 快照消费，重启生效。
  // tl1487 八组：未稳定→可选（用户 20261107 八组清单；默认关=铁律 2 不变，
  // Go 渲染表同步翻）。
  pinnedIndex: "optional",
  // ── efficiency（提效，11 项；任务 517 B3 并入 safetyCostControl；────
  //    任务 650：optimisticParallel 自自动化组迁入——减少写锁等待属提效。
  //    tl1733：memoryIndexSlim 记忆注入瘦身入组，可选档）
  optimisticParallel: "recommended",
  budgetControl: "recommended",
  compressOpt: "recommended",
  messageMerge: "recommended",
  quickCommands: "recommended",
  highSpeedModel: "optional",
  // tl1816：备用模型开关行挂「可选」徽章（任务书③；行在模型策略卡 pane，
  // labLabel("fallbackModel") 承载）。
  fallbackModel: "optional",
  compactionParallel: "optional",
  traceAsState: "optional",
  // tl1487 一组②：回答风格可选→推荐（用户 20261107 八组清单）。
  outputStyle: "recommended",
  // tl1817: 朗读 MiMo TTS 传输层（可选档，默认关）。
  mimoTts: "optional",
  cacheTuning: "optional",
  // tl1733：记忆注入瘦身（默认关=默认瘦身注入管线；开=应急旁路仅 pinned）。
  memoryIndexSlim: "optional",
  // tl1787 件⑤/A：tl1397 ranges 小段增量压缩（未稳定档，镜像 Go 渲染表——
  // Go-only config-only 豁免随设置页入口落地收回）。
  rangesCompact: "unstable",
  // tl1419：模型驱动 ranges 折叠（未稳定档，同上）。
  modelRangesCompact: "unstable",
  // tl1787 续作：pre-repair 分叉后台元信息拼接（未稳定档，镜像 Go 渲染表）。
  prepairAutosplice: "unstable",
  // tl1487 一组④：冷缓存压缩独立可选档（compressOpt 附属行起挂自己的徽章；
  // 用户 20261107 八组清单「冷缓存压缩→可选」）。
  coldCacheCompact: "optional",
  // 任务 677→856 ③：协作中心入口总开关（677 群聊入口开关随两图标合一迁移
  // 改名）。tl1487 一组①：未稳定→稳定（用户 20261107 八组清单；默认关+
  // 开有关前置门控不变，档位只表达成熟度，pane 开关不接 labStableLocked）。
  collabHub: "stable",
  // 任务 705：超长跨会话消息自动折叠（默认关=全量展示）。展示类开关原按 550
  // 口径定档可选——不开启只影响长消息的阅读密度，不动任何协作行为。
  // 案C/tl1265 级升档（2026-11-27 用户拍板 A1 升档、A2 highSpeedModel 不升）：
  // 可选→推荐——长跨会话消息刷屏是协作高频痛点，关闭即体验缺口（recommended
  // 定义）；≥21 天零报障。默认关语义不变（档位只表达成熟度，不动铁律 2）。
  sessionCollabAutoFold: "recommended",
  // 任务 707：压缩模型指定（上下文治理卡 compressOpt 下方，默认关）。按任务
  // 书建议定档可选——不开启不影响体验，只影响压缩成本。
  compactModel: "optional",
  // 任务 704：轨迹视图（DSH 同款可观测性视图，纯前端投影）。展示类开关按
  // 550 口径定档可选——不开启时标题栏与转录视图与引入前完全一致（铁律 2，
  // 默认关）。
  trajectoryView: "optional",
  // ── ui（界面，14 项）────────────────────────────────────────
  // 任务 1024：composer 草稿优化。tl1487 二组④：可选→推荐（用户 20261107
  // 八组清单；镜像 Go 档位同步翻）。
  draftRewrite: "recommended",
  // tl1544：运行中编辑历史消息回溯重发（可选档，默认关——off 时运行中编辑
  // 入口维持拒绝现状，回合后 rewind 链零变化；镜像 Go 渲染表同步 +1）。
  editResend: "optional",
  // tl1718：composer 工具组默认折叠（推荐档——工具栏常态占宽是 composer 宽度
  // 痛点，关闭即缺口持续存在；默认关=宽度驱动的折叠行为零变化，铁律 2；
  // 镜像 Go 渲染表同步 +1）。
  composerToolsFold: "recommended",
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
  // 为前端 fixture 预览）。tl1487 二组⑤：未稳定→稳定（Go 渲染表同步翻）。
  workflowPanel: "stable",
  // tl1487 二组⑤：工作流模板（面板家族子键的 pane 附属行）独立登记推荐档。
  workflowTemplates: "recommended",
  // tl1487 三组①：任务面板、子代理详情视图可选→推荐（用户 20261107 八组
  // 清单；Go 渲染表同步翻）。
  subagentPanel: "recommended",
  subagentDetail: "recommended",
  // tl1487 二组⑤：任务完成摘要可选→推荐（用户 20261107 八组清单）。
  completionSummary: "recommended",
  // tl1487 二组⑥：自动加载可选→推荐（用户 20261107 八组清单）。
  autoLoadOlder: "recommended",
  // tl1265 第2轮（2026-11-13 拍板 dec-73872eee136e22de）：可选→推荐——
  // 分栏是核心视图能力，关闭即能力缺失（recommended 定义），≥21 天零报障。
  splitView: "recommended",
  // tl1487 三组②：草稿持久化、选区快捷操作可选→推荐（用户 20261107 八组
  // 清单；Go 渲染表同步翻）。
  draftPersistence: "recommended",
  selectionActions: "recommended",
  questionSearch: "recommended",
  opencodeGoUsage: "recommended",
  // tl1265 第2轮（2026-11-13 拍板 dec-73872eee136e22de）：可选→推荐——
  // 关闭即失去 submit_feedback 工具 + 意见箱面板，反馈闭环入口缺失；
  // ≥21 天零报障（邻近 1494 属独立键 feedback_nudge，已 verified）。
  feedback: "recommended",
  // 任务 621：tabModeTint（任务 504 标签模式色调）在 Go labFeatureTiers 一直
  // 有档（unstable），但前端注册表漏收——实验室页渲染却无徽章，621 验收
  // 「每项特性有且仅有一个徽章」不允许。档位镜像 Go 侧。
  // tl1487 二组①：标签权限指示未稳定→可选（用户 20261107 八组清单
  // 「标签页管理/标签权限指示→可选」；Go 渲染表同步翻）。
  tabModeTint: "optional",
  // ── observability（可观测性，2 项；tl 1097 增 logManagement 参数卡）──
  monitoring: "recommended",
  subagentTps: "optional",
  // tl1746: 任务面板（tasklistPanel）——桌面内嵌只读直读 tasklist.db；Go
  // 渲染表同名条目同档（未稳定，家族子键 experimental_tasklist_panel +
  // tasklist_db_path）。
  tasklistPanel: "unstable",
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
  // tl1487 五组：CDP 调试端口未稳定→稳定（默认关语义不变）。
  cdpDebugPort: "stable",
  lifecycleNoiseGate: "unstable",
  // ── storage（存储，4 项；846 增 homePointer，tl-1174 增 jsonlZstd）────
  eventsRotation: "optional",
  // tl1338（用户 2026-11-01 拍板）：未稳定→稳定。fork 已锁定 v3（916），会话
  // 存储卡内其余条目（v4/v5 迁移、扫描清理）均为迁移操作工具而非实验能力，
  // 不该挂未稳定徽章。Go 渲染表 render.go 同步 LabTierStable（562 双侧门禁）。
  // 模式键非布尔：进批量范围后由 lab_bulk.go labBulkExcluded 挂 not_bool_switch
  // 原因码（与 messageMerge/eventsRotation 同机制），永不盲写。
  sessionStorage: "stable",
  homePointer: "unstable",
  // tl 1174: 冷会话 transcript zstd 归档——档位与 Go 渲染表双侧一致（562
  // 门禁）。tl1487 六组：可选→推荐（用户 20261107 八组清单）。
  jsonlZstd: "recommended",
  // ── infra（基础设施，6 项；785 退役 S1 常驻底座开关，790 增 acpListen，tl-1048 退役 zcodeTaskBus）──────
  // 任务 1005 批1 转档：runtimeReuse 升「稳定」（992 刚升推荐的四项之一）。
  runtimeReuse: "stable",
  acpListen: "unstable",
  // 任务 789: 编辑证据门宽松模式（放宽 evidence gate 属行为变更；
  // toolOptimizations 卡成员）。tl1487 七组②：可选→推荐（用户 20261107
  // 八组清单「工具优化五项→全部推荐」）。
  editEvidenceRelaxed: "recommended",
  // tl1487 一组③：modelCapabilityFilter 出表（原 retired 档独苗）——只读
  // 展示行随清单移除（不展示），档位登记同步退役。
  // 任务 992：orphanHandling 随复用池升推荐（用户 1018）——本注册表
  // 2026-10-19 随 1005 一并补同步（992 漏同步 drift 收口）。
  orphanHandling: "recommended",
  // tl1265 第2轮（2026-11-13 拍板 dec-73872eee136e22de）：未稳定→可选——
  // 四采样点 ≥21 天持续开启零报障，正面使用实例 tl1536（二维码分享）。
  localServer: "optional",
  // ── tool-opt（工具优化，3 项；任务 943 拆分 + 979 capabilityGroups）──
  // 任务 783④：编辑读回（readBack）随 22.5 交付转正——未稳定→推荐
  // （Go 渲染表同步升档，562 两侧一致断言守护）。任务 943：本 id 收窄
  // 为 readBack 面（含 789 证据门联动成员）。
  toolOptimizations: "recommended",
  // tl1487 七组②：multi_edit 直注可选→推荐（用户 20261107 八组清单）。
  multiEditInjection: "recommended",
  // 任务 979：能力组披露+session_hub 聚合件（953/954/955 共用一键）。
  // tl1487 七组②：可选→推荐。默认关=零行为（铁律 2）不变，重启生效。
  capabilityGroups: "recommended",
  // tl-1218：multi_edit 三条增强（①错误详情增强 ②证据门连坐收窄 ③跳过
  // 失败项模式）。tl1487 七组②：可选→推荐。默认关=与基线逐字节一致
  // （铁律 2），重启生效。
  multiEditErrorDetail: "recommended",
  multiEditEvidenceNarrow: "recommended",
  multiEditSkipFailed: "recommended",
  // 任务 727：心跳会话轮换（自动化/成本域）。桥接 heartbeat-rotation.json
  // 的 enabled 字段（缺文件=内置默认开）——「不开启只小幅影响」定可选档。
  heartbeatRotation: "optional",
  // 任务 742：「标签页管理」合并卡（条目级合并 506+651）。合并是入口层
  // 组织，不降级任何成员特性（tabCompress 仍推荐、tabModeTint 仍未稳定，
  // 见各自条目）；容器卡本身按任务书定可选档。
  // tl 1125：标签页管理合并卡升「推荐」（用户 2026-10-24 指令：标签管理/
  // 视图布局两特性徽章档位提升为推荐——推荐 = 核心特性，不开启体验有缺口）。
  tabManagement: "recommended",
  // 任务 742：心跳任务后台化（默认关）。原 550 口径定可选。
  // tl1265 第2轮（2026-11-13 拍板 dec-73872eee136e22de）：可选→推荐——
  // 关闭则心跳 tab 常驻堆积，tab 拥挤痛点（tl264）复发，属体验缺口。
  heartbeatBackground: "recommended",
  // 任务 1070：Standard 流续跑总闸（默认关）。不开启=续跑通道维持现状
  // （普通对话永不自动续跑）；开启后才有界续跑。可选档。
  standardContinuation: "optional",
  // 任务 782：界面组三张条目级合并卡的容器档 = 卡内成员最高档（774 聚合
  // 语义同源：searchHistory 三成员 两推荐一可选 → 推荐；viewLayout 推荐
  // 容器压过可选成员 → 推荐）。tl1487 三组②：inputSelection 两成员升推荐，
  // 容器档=成员最高档随之升推荐。合并是入口层组织，不降级任何成员特性。
  searchHistory: "recommended",
  inputSelection: "recommended",
  // tl 1125：视图布局合并卡升「推荐」（tl 1125 同指令，与 tabManagement 一并）。
  viewLayout: "recommended",
  // 任务 826：左右边栏 hover 展开（左右各一行，独立开关，默认关）。
  // tl1487 二组②：可选→推荐（用户 20261107 八组清单「左/右边栏悬停展开→
  // 推荐」；Go 渲染表同步翻）。
  sidebarHoverLeft: "recommended",
  sidebarHoverRight: "recommended",
  // 任务 940：composer 权限选择器形态二选一（默认关=现按钮组）。
  // tl1487 二组②：可选→推荐（用户 20261107 八组清单「对话权限下拉框→
  // 推荐」；Go 渲染表同步翻）。
  approvalSelect: "recommended",
  // 任务 959 ①：表格按栏宽自动换行（默认关）。
  // tl1487 二组②：可选→推荐（用户 20261107 八组清单「表格按栏宽换行→
  // 推荐」；Go 渲染表同步翻）。
  tableWrap: "recommended",
  // 任务 959 ②：composer 发送目标形态二选一（默认关=三按钮组）。展示形态
  // 开关按 550 口径定可选——不开启时发送目标区与引入前逐字节一致（铁律 2）。
  sendTargetSelect: "optional",
  // tl 1165：流式滚动平滑跟随（DSH ScrollFollow 同款四条防线）。
  // tl1487 二组④：可选→推荐（用户 20261107 八组清单「流式滚动平滑跟随→
  // 推荐」；Go 渲染表同步翻）。
  smoothScrollFollow: "recommended",
  // 任务 961：automation/efficiency 两张条目级合并卡容器档 = 成员最高档
  // （782 合并卡语义同源：smartAutomation 三成员全可选 → 可选；
  // interactionPresentation 快捷指令推荐压过两可选 → 推荐）。合并是入口层
  // 组织，不降级任何成员特性。
  smartAutomation: "optional",
  interactionPresentation: "recommended",
  // tl1309 卡②：「运行可观测」合并卡容器档 = 成员最高档（监控推荐压过
  // events 瘦身/轨迹观测两可选 → 推荐）。
  runtimeObservability: "recommended",
  // tl1309 卡③：「资源治理」合并卡容器档 = 成员最高档（运行时复用池稳定
  // 压过闲置释放/孤儿回收两推荐 → 稳定）。合并是入口层组织，不降级成员。
  resourceGovernance: "stable",
  // tl1309 卡③：两参数卡成员档随迁（与原独立参数卡 rail 徽章一致）。
  // tl1487 七组①：gcPercent 可选→推荐，建议值 100（用户 20261107 八组清单）。
  gcPercent: "recommended",
  idleReleaseMinutes: "recommended",
  // tl1309 卡④：「会话与任务面板」合并卡容器档 = 成员最高档（图墙推荐压过
  // 工作流未稳定 → 推荐）。
  sessionTaskPanels: "recommended",
};

/** 表A distribution — tl1844 起由活注册表 EXPERIMENT_FEATURE_TIERS 归并导出
 * （tl1829 themePackIDRe 同源化先例；自洽钉=task704-switch⑦ tl1309 同款，守在
 * 562①/1005①）：升档/登记窗口只改注册表条目一处，计数自动跟随——常量与注册
 * 表不再双源人肉同步（案C 第 10 处漏跟的常量面根因，tl1831d2 报告候选 B）。
 * 数值一致性由 562①/1005①/704-switch⑦「LAB_TIER_COUNTS ≡ 活注册表实分布」
 * 自洽断言把守；原逐件计数史注（562→案C 50 余步）由 git 历史与 task562/
 * task1005 测试头注承载，不再随升档手写追加（史注漏写零红灯，d2 报告序4）。
 */
function deriveLabTierCounts(): Readonly<Record<LabTier, number>> {
  const counts = LAB_TIER_ORDER.reduce((acc, tier) => {
    acc[tier] = 0;
    return acc;
  }, {} as Record<LabTier, number>);
  for (const id of Object.keys(EXPERIMENT_FEATURE_TIERS) as TierFeatureId[]) {
    counts[EXPERIMENT_FEATURE_TIERS[id]] += 1;
  }
  return counts;
}

export const LAB_TIER_COUNTS: Readonly<Record<LabTier, number>> = deriveLabTierCounts();

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
  // 任务 742：自适应压缩并入「标签页管理」卡——设置位置指到新卡。
  tabCompress: ["settings.labGroup.ui", "settings.tabManagement"],
  // 任务 782：检索与历史合并卡——历史提问选择器成员的设置位置指到新卡
  // （742 tabCompress → tabManagement 同款）。
  promptHistoryPicker: ["settings.labGroup.ui", "settings.searchHistory"],
  restartUpdate: ["settings.labGroup.ui", "settings.updateFeedback"],
  // tl1309 卡④：会话图墙/任务完成摘要并入「会话与任务面板」卡——设置位置
  // 指到新卡；侧栏增强并入「标签页管理」卡（同款）。
  todoSidebar: ["settings.labGroup.ui", "settings.tabManagement"],
  sessionWall: ["settings.labGroup.ui", "settings.sessionTaskPanels"],
  completionSummary: ["settings.labGroup.ui", "settings.sessionTaskPanels"],
  // 任务 782：分栏并入「视图布局」卡——设置位置指到新卡。
  splitView: ["settings.labGroup.ui", "settings.viewLayout"],
  subagentPanel: ["settings.labGroup.ui", "settings.subagentSuite"],
  // 任务 782：选区快捷操作并入「输入与选区」卡——设置位置指到新卡。
  selectionActions: ["settings.labGroup.ui", "settings.inputSelection"],
  // ── observability（可观测性）─────────────────────────────────
  // tl1309 卡②：监控并入「运行可观测」合并卡——设置位置指到新卡（742
  // tabCompress → tabManagement 同款）。
  monitoring: ["settings.labGroup.observability", "settings.runtimeObservability"],
};

/**
 * 任务 856 ③ — 实验室开关前置依赖表（键=依赖者，值=被依赖的前置开关）。
 *
 * demo v2 交互稿口径：跨会话协作（sessionCollab）关闭时，协作中心开关呈
 * 禁用态并提示「需先开启跨会话协作」。声明在此而非散在设置页——后续其他
 * 开关要挂前置时加一行即可，渲染侧统一处理禁用态（856 计划文档 ③）。
 * 单前置一期够用（与一期单副管理员同一简化原则）；多条依赖再演进为数组。
 *
 * tl1741：collabHub→sessionCollab 依赖随双卡撤开关退役（跨会话协作常开，
 * 前置门永真），表暂空；机制保留（后续开关挂前置时直接加行）。
 */
export const LAB_FEATURE_DEPS: Readonly<Partial<Record<TierFeatureId, TierFeatureId>>> = {};

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
