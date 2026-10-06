#!/usr/bin/env node
// Fork integrity checklist — run after EVERY upstream merge (docs/upstream-merge-checklist.md).
//
// Why: git auto-merge has silently dropped fork-only CSS blocks three times
// (#9222 group styles 194 lines, #9221 color filter 42 lines, --fg-faint
// token swap) with no conflict markers and no build errors — only runtime
// breakage. This script greps every fork-only feature marker (CSS class, TS
// symbol, Go symbol) that must survive a merge and fails loudly when any is
// missing. Exit 0 = all checks pass; exit 1 = list of missing markers.
//
// Usage: node scripts/check-fork-integrity.mjs   (from the repo root)

import { readFileSync, existsSync } from "node:fs";
import { join } from "node:path";

const ROOT = process.cwd();

// Each entry: feature label, repo-relative file, patterns that must all
// appear in that file. Add a row whenever a fork-only feature lands.
const CHECKS = [
  // ── CSS（auto-merge 静默丢块高发区）──────────────────────────────
  { feature: "任务350 分组层级化 CSS+树（层级≠指挥权）", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__group--nested > .project-tree__group-main", ".project-tree__group-subtree"] },
  { feature: "任务350 层级化纯函数与守卫", file: "desktop/frontend/src/lib/sessionGroupTree.ts", patterns: ["SESSION_GROUP_MAX_DEPTH", "hierarchyMutationIsDisplayOnly", "canNestUnder"] },
  { feature: "任务350 层级化测试", file: "desktop/frontend/src/__tests__/project-tree-group-hierarchy.test.tsx", patterns: ["GUARD 层级≠指挥权: nesting touches nothing but parent", "inherited active count = 1"] },
  { feature: "任务350 后端 parent 校验", file: "desktop/project_tree_organization.go", patterns: ["maxSessionGroupDepth", "validateSessionGroupHierarchy"] },
  { feature: "任务350 locale 三语", file: "desktop/frontend/src/locales/zh.ts", patterns: ["projectTree.nestGroupUnder", "projectTree.moveGroupToTopLevel"] },
  { feature: "任务368 关闭非活跃标签页（策略+菜单+三语）", file: "desktop/frontend/src/lib/tabClosePolicy.ts", patterns: ["selectCloseInactiveIds"] },
  { feature: "任务368 locale", file: "desktop/frontend/src/locales/zh.ts", patterns: ["tabBar.closeInactiveTabs"] },
  { feature: "任务377 lifecycle 噪音门控（默认关）", file: "desktop/startup_diagnostics.go", patterns: ["lifecycleNoiseBenign", "lifecycle noise gate suppressed clean-shutdown residue"] },
  { feature: "任务377 fatal log 头行（不再 0 字节）", file: "desktop/crash_fatal.go", patterns: ["fatalCrashLogHeaderPrefix", "stripFatalCrashHeader"] },
  { feature: "任务377 config 开关", file: "internal/config/desktop_preferences.go", patterns: ["experimental_lifecycle_noise_gate"] },
  { feature: "#9222 项目分组 CSS", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__group", ".project-tree__group-count", ".project-tree__group-caret"] },
  { feature: "#9221 颜色筛选 CSS", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__color-filter", ".project-tree__color-opt", ".project-tree__color-swatch", ".project-tree__action-btn--active"] },
  { feature: "#9221 颜色筛选锚点", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__color-filter {\n  position: relative;"] },
  { feature: "heartbeat 编辑器样式", file: "desktop/frontend/src/custom/features/heartbeat/heartbeat.css", patterns: [".heartbeat-editor__model-override", ".heartbeat-editor__input"] },
  { feature: "任务149 悬停预览富文本卡 CSS", file: "desktop/frontend/src/styles.css", patterns: [".jump-preview-title", ".jump-preview-body", ".jump-preview-tool"] },

  // ── 前端 TS ─────────────────────────────────────────────────────
  { feature: "task 163 OpenCode Go 用量查询（后端）", file: "desktop/opencode_go_usage.go", patterns: ["isOfficialOpenCodeGoBase", "no-subscription", "Bearer ", "parseOpenCodeGoUsage"] },
  { feature: "task 163 OpenCode Go 用量卡（前端）", file: "desktop/frontend/src/components/SettingsOpenCodeGoUsageCard.tsx", patterns: ["GetOpenCodeGoUsage", "opencode-go-usage__row", "resetCountdown"] },
  { feature: "task 280 乐观并行（实验室改名迁址，同键取反绑定）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["optimisticParallel", "SetOptimisticWrite(e.target.checked)", "group: \"efficiency\""] },
  { feature: "task 192 驻留豁免开关（store 策略）", file: "desktop/frontend/src/lib/transcriptStore.ts", patterns: ["setResidentPolicy", "shouldRetainOnSwitch", "ResidentExemptLimit", "noteResidentBudgetOver"] },
  { feature: "#9221 颜色筛选 TSX", file: "desktop/frontend/src/components/ProjectTree.tsx", patterns: ["colorFilter", "renderColorFilterControl", "project-tree__action-btn"] },
  { feature: "#9222 分组 TSX + 持久化（上游等价实现）", file: "desktop/frontend/src/components/ProjectTreeOrganization.tsx", patterns: ["ProjectTreeGroupRows", "useProjectTreeOrganization", "persistSessionGroupCollapsed"] },
  { feature: "#9518 分组计数后端权威", file: "desktop/frontend/src/components/ProjectTreeOrganization.tsx", patterns: ["memberCount", "group.topicIds?.length ?? 0"] },
  { feature: "projectGroups 存储层", file: "desktop/frontend/src/lib/projectGroups.ts", patterns: ["loadProjectGroupCollapsed", "persistProjectGroupCollapsed", "dropProjectGroupCollapsed"] },
  // #9222 的项目级分组 UI 接线（2026-09-10 恢复）：上游的会话级分组占用了同一渲染位置，
  // 每次 merge 都要确认这四处调用点还在，而不是被上游实现悄悄顶掉。
  { feature: "#9222 项目分组 UI 接线", file: "desktop/frontend/src/components/ProjectTree.tsx", patterns: ["addProjectGroup", "groupForProjectRoot", "NewGroupPanel", "MoveToGroupPanel", "projectGroup.createNew"] },
  // 任务 186：内置 root（global-workspace / 会话目录 / projects 容器）不是项目。
  // 白名单是「读写收口 + 打开路径 scope 归 Global + 前端渲染兜底」三道闸，
  // 上游 merge 丢掉任何一道都会让幽灵节点复活或会话失联，故逐文件登记。
  { feature: "任务186 内置 root 白名单与 scope 归一", file: "desktop/builtin_roots.go", patterns: ["func builtinWorkspaceRoots", "func isBuiltinWorkspaceRoot", "func stripBuiltinProjects", "func normalizeWorkspaceScope"] },
  { feature: "任务186 项目表读写剥离（load/update 收口）", file: "desktop/tabs.go", patterns: ["stripBuiltinProjects(applyProjectOrganization(f, organization))", "saveProjectsFile(stripBuiltinProjects(f))", "normalizeWorkspaceScope(scope, workspaceRoot)"] },
  { feature: "任务186 注册表/工作区指针拒绝内置 root", file: "desktop/project_root_registration.go", patterns: ["isBuiltinWorkspaceRoot(workspaceRoot)"] },
  { feature: "任务186 工作区指针双向忽略内置 root", file: "desktop/workspace.go", patterns: ["isBuiltinWorkspaceRoot(dir)", "isBuiltinWorkspaceRoot(ws)"] },
  { feature: "任务186 恢复标签页 scope 归一", file: "desktop/app.go", patterns: ["normalizeWorkspaceScope(entry.Scope, entry.WorkspaceRoot)"] },
  { feature: "任务186 前端渲染兜底过滤", file: "desktop/frontend/src/lib/projectTreePresentation.ts", patterns: ["projectTreeWithoutBuiltinWorkspaceNodes"] },
  { feature: "任务186 快照过滤接线", file: "desktop/frontend/src/components/ProjectTree.tsx", patterns: ["projectTreeWithoutBuiltinWorkspaceNodes(asArray(snapshot.projects))"] },
  { feature: "#9580 草稿持久化存储层", file: "desktop/frontend/src/lib/composerDraftPersistence.ts", patterns: ["composer:drafts:v1", "pagehide", "MAX_PERSISTED_BYTES"] },
  { feature: "task 369 选区快捷操作 one-shot 通道", file: "internal/control/side_query.go", patterns: ["func (c *Controller) SideQuery(", "sideQueryMaxTextRunes", "boundedllm.Call"] },
  { feature: "task 369 选区快捷操作前端（开关两态+结果卡）", file: "desktop/frontend/src/components/TranscriptSelectionMenu.tsx", patterns: ["quickActionsEnabled", "transcript-selection-result-card", "runQuickAction"] },
  { feature: "task 369 选区快捷操作桥接线（App 设置回调）", file: "desktop/frontend/src/App.tsx", patterns: ["RunSelectionSideQuery(action, text, contextText)", "setSelectionActionsEnabled"] },
  // 任务514（选区开关"开不了"修复）：369 把渲染行写进 [desktop] 段而字段在
  // Agent struct，读回即丢（开关弹回关）；启动快照又缺字段（重启即失效）。
  // 三处锚定防上游合并静默回退，段落归属由 internal/config 往返测试把守。
  { feature: "任务514 选区开关渲染行落 [agent] 段", file: "internal/config/render.go", patterns: ["experimental_selection_actions = %v   # task 369: selection quick-actions"] },
  { feature: "任务514 启动快照 struct 携带选区开关", file: "desktop/settings_app.go", patterns: ["ExperimentalSelectionActions bool `json:\"experimentalSelectionActions\"`"] },
  { feature: "任务514 启动快照 builder 回填选区开关", file: "desktop/reasoning_display_app.go", patterns: ["ExperimentalSelectionActions: cfg.Agent.ExperimentalSelectionActions"] },
  // 任务461 P1（收件箱锁挂起修复）：filelock.go 与上游共享，合并可能静默回退
  // 无界等待；锚定默认上限常量与 ctx.Done 分支（终止 ≤1s 的实现载体）。
  { feature: "任务461 锁等待一律有界+取消即时生效", file: "internal/filelock/filelock.go", patterns: ["DefaultWaitTimeout", "case <-ctx.Done():"] },
  { feature: "任务461 收件箱锁 5s 外部超时（内层 ≤5s）", file: "internal/collabinbox/collabinbox.go", patterns: ["lockWaitTimeout", "AcquireWithExternalTimeout"] },
  // 任务461 P8（收件箱重入污染）：投递层幂等与消费层折叠均为 fork 侧行为修复，
  // 与上游共享文件可能被合并静默回退，逐条锚定。
  { feature: "任务461-P8 投递层重发幂等（同 from+to+内容窗内返原 id）", file: "internal/sessioncollab/sessioncollab.go", patterns: ["dedupeResend", "resendDedupWindowDefault", "resendDedupWindowSystem"] },
  { feature: "任务461-P8 同内容折叠+批量已读（DuplicateCount/MarkRead）", file: "internal/collabinbox/collabinbox.go", patterns: ["DuplicateCount", "func (s *Store) MarkRead(", "duplicateFoldWindow"] },
  { feature: "任务461-P8 面板折叠徽标+全部已读接线", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["duplicateCount", "MarkCollabMailRead"] },
  { feature: "任务461-P10 幂等冲突判为重复已送达（不回错误不断根重投）", file: "desktop/session_collab.go", patterns: ["errCollabDuplicateDelivery", "collabAdmissionErr"] },
  // 任务461-P16（0119「ask 不弹窗+终止无效」）：P7 三级终止 × ask 等待的交叉
  // 回归钉——终止第一击必须打断 ask 等待并撤下挂起问题，丢了即现场复发。
  { feature: "任务461-P16 P7×ask 交叉钉（终止打断 ask 等待+撤僵尸卡片）", file: "internal/control/stop_escalation_ask_test.go", patterns: ["TestCancelStopInterruptsAskWait", "TestCancelStopInterruptsQueuedAsk"] },
  // 任务461-P16 投递链打点：三症状（ask 弹窗延迟/不弹/用户消息不渲染）共用
  // 后端→webview 单通道；锚定 emittedAt 锚点、队列积压告警与前端收据/挂载行。
  { feature: "任务461-P16 wire emittedAt 锚点（量化 emit→前端收到延迟）", file: "desktop/tabs.go", patterns: ["EmittedAt int64", "EmittedAt:         time.Now().UnixMilli()"] },
  { feature: "任务461-P16 发射队列积压告警（一次拥塞一报）", file: "desktop/tabs.go", patterns: ["runtimeEventLagWarnThreshold", "runtime event lagged in the webview emit queue"] },
  { feature: "任务461-P16 前端收据+挂载打点（ask received / card mounted）", file: "desktop/frontend/src/lib/useController.ts", patterns: ["reportAskPanelReceipt", "describeAskReceipt", "ask received"] },
  { feature: "任务461-P16 AskCard 挂载打点", file: "desktop/frontend/src/components/AskCard.tsx", patterns: ["ask card mounted"] },
  // 任务439（zcode 任务总线内置化）：开关与内嵌宿主都是 fork 侧新面，
  // 上游没有对应物；逐文件登记，merge 丢锚点即 fail loudly。
  { feature: "任务439 实验开关 experimental_zcode_task_bus（铁律2默认关）", file: "internal/config/desktop_preferences.go", patterns: ["ExperimentalZcodeTaskBus", "experimental_zcode_task_bus"] },
  { feature: "任务439 config 渲染表+设置器（81/123 丢存规则）", file: "internal/config/render.go", patterns: ["experimental_zcode_task_bus = %v"] },
  { feature: "任务439 内嵌 bus 宿主（开=8787 挂载，关=零行为）", file: "desktop/zcode_task_bus.go", patterns: ["startZcodeTaskBus", "closeZcodeTaskBus", "ZcodeTaskBusStatus", "POST /mcp", "POST /bus/events"] },
  { feature: "任务439 桌面启动/关闭接线", file: "desktop/app.go", patterns: ["a.startZcodeTaskBus(cfg)"] },
  { feature: "任务439 关停接线", file: "desktop/shutdown.go", patterns: ["a.closeZcodeTaskBus()"] },
  { feature: "任务439 前端实验室卡+状态/角色可视化", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["selected === \"zcodeTaskBus\"", "app.SetExperimentalZcodeTaskBus(on)", "app.ZcodeTaskBusStatus()"] },
  { feature: "任务439 前端契约测试", file: "desktop/frontend/src/__tests__/settings-zcode-task-bus.test.ts", patterns: ["lab rail hosts the zcodeTaskBus entry", "flag-off path returns before any network work"] },
  { feature: "任务461-P11 读路径降级直读+共享锁短预算", file: "internal/collabinbox/collabinbox.go", patterns: ["lockRead", "readLockWaitTimeout", "Degraded"] },
  { feature: "任务511 History 锁繁忙降级（Warn+Degraded 贯穿到快照）", file: "internal/sessioncollab/sessioncollab.go", patterns: ["degraded history read (lock busy)", "last_holder", "wait_ms"] },
  { feature: "任务511 快照降级传递（mail 锁繁忙 ≠ 暂无信件）", file: "internal/collabinbox/collabinbox.go", patterns: ["idx.degraded"] },
  { feature: "任务511 sweep 节流闸（面板读不再每次全库重写）", file: "internal/collabinbox/collabinbox.go", patterns: ["sweepThrottleWindow", "func (s *Store) sweepDue("] },
  { feature: "任务511 面板降级提示（锁繁忙 ≠ 暂无信件）", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["collabInbox.degraded", "collab-inbox-panel__empty--degraded"] },
  { feature: "任务511 降级提示三语 locale", file: "desktop/frontend/src/locales/zh.ts", patterns: ["collabInbox.degraded"] },
  { feature: "任务461-P13② 上下文增幅观测告警（维护间隔跳变有日志诊断入口）", file: "internal/agent/context_manager.go", patterns: ["observeContextGrowth", "contextGrowthWarnRatio"] },
  { feature: "任务461-P13③ task309 幂等默认开（Default 钉 true，显式 false 仍可关）", file: "internal/config/config.go", patterns: ["SessionCollabMailIdempotentDefault: true"] },
  // base_toolcall 远程门：不可比较的值类型工具（内置写工具族）在接口 == 前必须先挡，
  // 否则 experimental_base_process 开启后首次派发即 Go runtime fatal（10-06 三连崩）。
  { feature: "base_toolcall 不可比较类型防崩守卫（Comparable 先于 ==）", file: "internal/agent/base_toolcall.go", patterns: ["!reflect.TypeOf(owned).Comparable() || owned != runTool"] },
  { feature: "base_toolcall 防崩守卫回归测试（ModeRemote 值类型工具）", file: "internal/agent/base_toolcall_test.go", patterns: ["uncomparable value tool runs local without panicking", "gateValueTool"] },
  // 任务 90 链拼接：promote 时把落败链（当前 main）中 winner 缺失的头部 graft 到新主线。
  // 三个锚点按「顺序」登记——gap 在 rename 前算、graft 在侧车搬移后写、事件日志随即折叠；
  // 顺序错位造成的失败是静默的（文件对而读回旧），所以这里锁的是调用形状，不只是符号名。
  { feature: "任务 90 链拼接接线（顺序敏感）", file: "internal/agent/recovery_consolidate.go", patterns: [
    "SessionContentPrefixGap(winnerPath, mainPath)",
    "graftPrefixOntoLines(splitTranscriptLines(current), gap.Messages)",
    "compactSessionEventLog(mainPath, mergedMsgs, digest, legacyMeta.Revision, \"promote-prefix-graft\")",
    "report.Prefixed = grafted",
  ] },
  { feature: "任务 90 前缀缺口算法 + turn 对齐", file: "internal/agent/recovery_prefix_gap.go", patterns: [
    "func prefixGapForMessages(",
    "func SessionContentPrefixGap(",
    "return m.Role == provider.RoleUser && m.Origin != provider.MessageOriginHost",
  ] },
  { feature: "任务 90 graft 只增不改（既有行逐字保留）", file: "internal/agent/recovery_prefix_graft.go", patterns: [
    "func graftPrefixOntoLines(",
    "func leadingSystemLineCount(",
    "transcript head is not readable",
  ] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9565 live footer 上游语义（#9579 尾部预算已有意还原）", file: "desktop/frontend/src/lib/transcriptLiveTurn.ts", patterns: ["slice(userIndex + 1)", "liveRows"] },
  { feature: "#9082 会话要点提取", file: "internal/agent/session_extract.go", patterns: ["chunkedFoldSummary", "splitExtractChunks", "extractChunkOverlapBytes"] },
  { feature: "#9082 /extract 回退摘要", file: "internal/control/controller.go", patterns: ["chunkedFoldSummary"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9601 验收框 parked 消费", file: "desktop/frontend/src/lib/useController.ts", patterns: ["parkedDelivery", "parkedConsumed"] },
  { feature: "会话分组折叠持久化", file: "desktop/frontend/src/components/ProjectTreeOrganization.tsx", patterns: ["loadSessionGroupCollapsed", "persistSessionGroupCollapsed"] },
  // 任务 169：分组头拖拽手柄（任务 50 桌面可用性补齐）。手柄 span + 立即进入
  // 拖拽状态机的 beginGroupDrag 是纯前端交互，上游合并时最容易被同名重构顶掉，
  // 锚定渲染形状与入口函数；hover 显隐是 CSS 契约，单独锁样式规则。
  { feature: "任务169 分组头拖拽手柄（TSX）", file: "desktop/frontend/src/components/ProjectTreeOrganization.tsx", patterns: ['className="project-tree__group-drag"', "beginGroupDrag"] },
  { feature: "任务169 分组头拖拽手柄（hover 显隐 CSS）", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__group-main:hover .project-tree__group-drag", ".project-tree__group-main--dragging .project-tree__group-drag"] },
  { feature: "#9580 草稿 v2 接入", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["loadPersistedComposerDraft", "persistComposerDraft", "persisted.pastedBlocks.map"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9570 Markdown 门控放宽", file: "desktop/frontend/src/components/MarkdownHistory.tsx", patterns: ["markerInView", "MARKDOWN_TAIL_BLOCKS) {"] },
  { feature: "#9567 接管钉尾（已按上游 kernel 适配）", file: "desktop/frontend/src/components/Transcript.tsx", patterns: ["Fork (#9567)", "setScrollMode(\"tail-follow\")"] },
  { feature: "#9521 TPS chip", file: "desktop/frontend/src/components/ToolCard.tsx", patterns: ["tok/s"] },
  { feature: "#9521 TPS 状态字段", file: "desktop/frontend/src/lib/useController.ts", patterns: ["tokensPerSec"] },
  { feature: "#9468 reload fallback", file: "desktop/frontend/src/lib/useController.ts", patterns: ["loadOlderHistory"] },
  // 任务 160：顶部上滚加载更早 + 「加载更早」按钮都是 fork 独有交互（上游已改为纯按钮
  // 驱动，无同类实现），且滚动触发受 experimental_auto_load_older 开关门控——整段被上游
  // 版顶掉时不会有冲突标记，故登记语义锚点（含开关参数名与顶部守卫常量）。
  // 448：半径常量 HISTORY_TOP_GUARD_PX 已并入共享的 olderHistoryTriggerPx（两视口预取），
  // 锚点随之换锁 —— 保住的是"开关门控 + 顶部上滚触发"这个特征，不是那个字面量。
  { feature: "任务160 顶部上滚加载更早（开关门控）", file: "desktop/frontend/src/lib/useTranscriptKernel.ts", patterns: ["autoLoadOlderAtTop", "olderHistoryTriggerPx(element.clientHeight)", "requestOlderAtTop"] },
  { feature: "任务160 加载更早按钮", file: "desktop/frontend/src/components/TranscriptViewport.tsx", patterns: ["chat-older", "showLoadOlder"] },
  // 288：tab/项目分组/会话三处右键菜单「全部已读」。readActivity 存取收口在
  // lib/readActivity.ts（ProjectTree 之外 TabBar 也写同一份存档），三处菜单接线
  // 各登记符号锚点——上游未实现该交互，合并丢块时只有运行时缺菜单、无编译错误。
  { feature: "288 全部已读（readActivity 存储层）", file: "desktop/frontend/src/lib/readActivity.ts", patterns: ["READ_ACTIVITY_STORAGE_KEY", "READ_ACTIVITY_CHANGED_EVENT", "markReadKeysRead", "persistReadActivity", "readActivityKeysInSubtree"] },
  { feature: "288 全部已读（tab/项目分组/会话菜单接线）", file: "desktop/frontend/src/components/ProjectTree.tsx", patterns: ["markAllRead", "readActivityKeysInScope", "readActivityKeysInSubtree", "READ_ACTIVITY_CHANGED_EVENT", "onMarkAllRead"] },
  { feature: "288 全部已读（tab 菜单）", file: "desktop/frontend/src/components/TabBar.tsx", patterns: ["markTabsAllRead", "mark-all-read"] },
  // 任务 149：预览卡是 fork 独有交互（上游 jump 预览只有一行纯文本），内容结构
  // （粗体标题/多行正文/工具标记）与贴边翻转都在 fork 侧，整块被顶掉不会有冲突标记。
  { feature: "任务149 悬停预览富文本卡 TSX", file: "desktop/frontend/src/components/QuestionJumpBar.tsx", patterns: ["jump-preview-title", "jump-preview-tool", "jumpPreviewPlacement", "data-flip"] },
  { feature: "任务149 预览内容/翻转纯函数", file: "desktop/frontend/src/lib/jumpPreview.ts", patterns: ["buildJumpPreviewContent", "jumpPreviewPlacement", "JUMP_PREVIEW_MAX_TOOLS"] },

  // ── Go 后端 ─────────────────────────────────────────────────────
  // 任务 470：data-URL 图片被端点拒收的诊断链。openai client 在 400 时凭
  // 「请求含内联图」这一请求侧事实给 APIError 追加可操作提示（响应体从不点名
  // 图片），上游 merge 若拆掉任一环都会让 mimo 类端点的图片失败回到哑 400。
  { feature: "任务470 内联图拒收诊断（APIError.Hint + 注解器）", file: "internal/provider/image_rejection.go", patterns: ["func AnnotateInlineImageRejection", "vision = false", "image-understanding"] },
  { feature: "任务470 APIError.Hint 渲染", file: "internal/provider/retry.go", patterns: ["Hint                string", "if e.Hint != \"\" {"] },
  { feature: "任务470 openai 接线+请求侧判定", file: "internal/provider/openai/openai.go", patterns: ["AnnotateInlineImageRejection", "func requestHasInlineDataImages"] },
  { feature: "任务470 注解器测试", file: "internal/provider/image_rejection_test.go", patterns: ["TestAnnotateInlineImageRejection"] },
  { feature: "任务470 openai 集成测试", file: "internal/provider/openai/image_rejection_test.go", patterns: ["TestStreamAnnotatesInlineImageRejection", "TestRequestHasInlineDataImages"] },
  { feature: "#9572 摘要安全前缀", file: "internal/agent/compact_projection.go", patterns: ["trigger != CompactionTriggerManual", "maximumSafeSummaryPrefixEnd"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9572 Unknown 网关回退", file: "internal/agent/compact_projection.go", patterns: ["a.lastAdmission().ObservedWindow > 0 || a.contextWindow > 0"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9592 P0 只读/管理豁免", file: "internal/agent/tool_write_coordination.go", patterns: ["!plan.effects.WorkspaceMutation && !parentWriteGuardTarget(plan.runTool.Name())"] },
  { feature: "#9592 P2 hook 声明", file: "internal/hook/hook.go", patterns: ["MutatesWorkspace *bool"] },
  { feature: "#9592 P2 细化判定", file: "internal/hook/runner.go", patterns: ["h.MutatesWorkspace != nil && !*h.MutatesWorkspace"] },
  { feature: "#9070 heartbeat 模型覆盖", file: "desktop/heartbeat.go", patterns: ["heartbeatModelRef", "SetModelForTab(tabMeta.ID, ref)"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9522 P1 预览入 job buffer", file: "internal/agent/subagent_progress.go", patterns: ["attachJobOutput", "writeJobPreview"] },
  { feature: "#9522 P1 完成摘要", file: "internal/jobs/result_digest.go", patterns: ["resultDigestOf"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9522 P2 task_id 续跑/引导", file: "internal/agent/task.go", patterns: ["steerBackgroundTask", "steerSlots", "subagentSteerHookFromContext"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9521 TPS 采样", file: "internal/agent/subagent_progress.go", patterns: ["progressRateSampler", "TokensPerSec"] },
  { feature: "#9520 上下文预算行 + 任务 99 窗口状态措辞", file: "internal/agent/context_budget_block.go", patterns: ["context-state", "compaction cycles at", "no fixed total limit"] },
  { feature: "#9520 系统提示契约", file: "internal/config/config.go", patterns: ["ContextManagementPolicy"] },
  // 20261002 提示词三项补充（调研报告 §4.1，wt-zcode-prompt）：沟通段/忠实汇报/
  // 记忆写作指南。锚点选提示词正文语义句与符号（非注释），防止上游 merge
  // 静默顶掉政策文本或把 appendCorePolicies 接线拆掉。
  { feature: "20261002 提示词沟通段+忠实汇报", file: "internal/config/config.go", patterns: ["UserCommunicationPolicy", "with no tool calls after", "Report outcomes faithfully"] },
  { feature: "20261002 核心政策接线含沟通段", file: "internal/boot/prompt_policy.go", patterns: ["config.UserCommunicationPolicy", "config.CompletionReportPolicy"] },
  { feature: "20261002 记忆写作质量指南", file: "internal/memory/memory.go", patterns: ["instead of creating a near-duplicate", "not derivable from the code or git history"] },
  // 20261002 G1+G5 提示词补充（细节差距调研 §2，wt-zcode-prompt）。锚点选
  // 两段新增正文语义句，防止上游 merge 静默顶掉例外/外发条款。
  { feature: "20261002 自主性提问例外(G1)", file: "internal/config/config.go", patterns: ["the deliverable is your assessment", "Don't apply a fix until they ask for one"] },
  { feature: "20261002 外发动作三语义(G5)", file: "internal/config/config.go", patterns: ["approval in one context does not extend to the next", "cached or indexed even after deletion"] },
  // parked: fork 分支不含该实现（1f8c3fe50 对齐时移除 / 上游另有设计）
  // { feature: "#9526 task 后台引导", file: "internal/agent/task.go", patterns: ["Do not sleep or poll for progress"] },
  { feature: "#9566 截断参数修复", file: "internal/agent/run_loop.go", patterns: ["repairTruncatedToolCallArgs"] },
  { feature: "#9564 kill_shell 非变更分类", file: "internal/evidence/classify_profile.go", patterns: ["kill_shell"] },
  { feature: "写协调体系 #9111", file: "internal/agent/tool_write_coordination.go", patterns: ["parentWriteGuardTarget", "reserveCoordinatedParentWrite"] },
  { feature: "乐观写 #9213", file: "internal/agent/tool_write_coordination.go", patterns: ["optimisticWrite"] },

  // ── 副本预览与切换链路（2026-09-12/13，任务 92/93）──────────────
  // 教训（33b6c32ec 被 1.38.3 merge 冲掉、2026-09-13 才发现）：行为语义级
  // 魔改（函数内逻辑）也必须登记锚点——存在性检查（文件/符号还在）抓不住
  // 「函数还在但逻辑被上游版顶掉」。新特性落地时同步在此登记，锚点选
  // 语义性字符串（赋值/调用形态），不选注释。
  { feature: "#10056 快照 fast path（projectionPending 不否决）", file: "internal/agent/save.go", patterns: ["deliberately not part of this decision"] },
  { feature: "任务92① 预览 bridge 透传（不锚定 activeSessionDir）", file: "desktop/recovery_chains.go", patterns: ["RecoveryChainPreviewFor(mainPath, chainPath)"] },
  { feature: "任务92③ 内容快照/overlap LRU 缓存", file: "internal/agent/recovery_gc.go", patterns: ["contentSnapshotCache", "contentOverlapCache", "contentSnapshotCacheLimit"] },
  { feature: "任务92③ 宽松重放 LRU（非全清）", file: "internal/agent/recovery_chain_preview.go", patterns: ["tolerantReplayCache.order"] },
  { feature: "任务92④ 预览弹窗自持状态（ChainPreviewBody）", file: "desktop/frontend/src/components/RecoveryCopiesSection.tsx", patterns: ["function ChainPreviewBody", "recoveryCopiesPreviewBuilding"] },
  { feature: "任务92② 扫描展开自动加载候选链", file: "desktop/frontend/src/components/RecoveryCopiesSection.tsx", patterns: ["chainLoads"] },
  // 任务 232 重构：skipHistory 判定移入 hydrateHistoryApply（skip/replace 双分支），
  // useController 侧保留 hasLocalItems/targetResidentInStore（LRU 驻留也可）。两条锚点都要在。
  { feature: "任务93 本地快照切 tab（33b6c32ec 重实施；232 重构后双锚点）", file: "desktop/frontend/src/lib/useController.ts", patterns: ["hasLocalItems", "targetResidentInStore"] },
  { feature: "任务93/232 hydrate skip-replace 分支", file: "desktop/frontend/src/lib/hydrateHistoryApply.ts", patterns: ["skipHistory"] },
  // 审计 m3（批四 B1）：232 新核心路径锚点——hasResidentSnapshotForEmptySurface 及调用点
  { feature: "任务232 空surface LRU快照复用（审计m3补锚）", file: "desktop/frontend/src/lib/useController.ts", patterns: ["hasResidentSnapshotForEmptySurface"] },
  { feature: "任务232 hasResidentSnapshotForEmptySurface 定义", file: "desktop/frontend/src/lib/hydrateHistoryApply.ts", patterns: ["hasResidentSnapshotForEmptySurface"] },
  { feature: "任务95 promote sidecar 迁移（damaged 清理 + pinned 身份重写）", file: "internal/agent/recovery_consolidate.go", patterns: ["rewritePinnedContextSessionID", "SessionEventLogDamaged(winnerPath)"] },
  // 任务488（2026-10-05）：373-R1 图片去重存储整体移除（用户拍板：读图能力 > 体积优化）——
  // P19 的两条 imgpack 锚随 session_image_pack.go 一并撤销（该文件已删除）。
  // 机制留档：写侧引用化 → 读侧解引用失败静默留 reasonix-img:// 引用（仅 WARN）→
  // provider imageContentParts 对非 data URL 静默丢弃 ⇒ 模型收不到图。

  // ── wt-zcode-285：会话信息面（ContextPanel）分组名 + recovery 副本状态 ──
  { feature: "285 信息面分组名+副本状态解析层", file: "desktop/frontend/src/lib/sessionInfoPanel.ts", patterns: ["resolveSessionGroupTitle", "sessionRecoveryDisplay", "recovery.role.covered_copy"] },
  { feature: "285 信息面展示接线", file: "desktop/frontend/src/components/ContextPanel.tsx", patterns: ["GetProjectGroups", "GetRecoveryLineage", "sessionInfo"] },

  // ── 任务 298：open 失败/慢打点 + eventsMb 超限治理（2026-10-01）────
  { feature: "任务298 open 出口打点（失败落 slog + 慢打开分相）", file: "desktop/open_session_trace.go", patterns: ["desktop: open session failed", "desktop: open session slow", "phasesMs"] },
  { feature: "任务298 open 汇合点接线（分相耗时标记）", file: "desktop/tabs.go", patterns: ["beginOpenSessionTrace", "tr.mark(\"tabLock\")", "tr.mark(\"sessionCreate\")"] },
  { feature: "任务298 超限 WARN 限频门 + 元凶指认 + 处置联动", file: "desktop/perf_monitor.go", patterns: ["perfWarnGate", "topEventsFileUnder", "EventsAutoRotationSnapshot"] },

  // ── 任务 285：会话信息面 agent 工具（分组/版本谱系/结构化 meta）──
  { feature: "任务285 agent 侧三工具（info/versions/adopt）", file: "internal/agent/session_info_tools.go", patterns: ["func NewGetSessionInfoTool", "func NewListSessionVersionsTool", "func NewAdoptSessionVersionTool"] },
  { feature: "任务285 目录行分组字段+group 过滤", file: "internal/agent/session_collab_tools.go", patterns: ["directoryPageFiltered", "SessionGroup func(topicID string)"] },
  { feature: "任务285 宿主探针（分组/谱系/切换）", file: "desktop/session_info_collab.go", patterns: ["func (a *App) collabSessionGroup", "func (a *App) collabSessionVersions", "SetActiveSessionVersion"] },
  { feature: "任务285 boot 探针接线", file: "internal/boot/boot.go", patterns: ["OnSessionGroup", "OnSessionVersions", "OnAdoptSessionVersion"] },

  // ── 任务 454：list_addressable_sessions 分组 id 拼法（title/id 双寻址）──
  { feature: "任务454 工具层 group id 过滤", file: "internal/agent/session_collab_tools.go", patterns: ["SessionGroupMatch func(topicID, group string) bool", "func topicInSessionGroup"] },
  { feature: "任务454 宿主 id 归属探针", file: "desktop/session_info_collab.go", patterns: ["func (a *App) collabSessionGroupMatch"] },
  { feature: "任务454 boot 探针接线", file: "internal/boot/boot.go", patterns: ["OnSessionGroupMatch"] },

  // ── 任务 155：会话存储四档 + bridge 健康债（2026-09-17）──────────
  { feature: "任务155 四档枚举与渐进校验", file: "internal/config/session_storage.go", patterns: ["SessionStorageDualWriteReadV3", "ValidateSessionStorageTransition", "ResolveSafeSessionStorageMode"] },
  { feature: "任务155 bridge 健康债修复（登记即用 + 同 root 接管 + 批 id 幂等）", file: "internal/control/session_v4_bridge.go", patterns: ["reclaimSession", "v4BridgePeers", "ErrLegacyReadOnly"] },
  { feature: "任务155 四档设置 UI 与重启提示", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["SESSION_STORAGE_MODES", "sessionStorageRestartPending"] },

  // ── 任务 170：会话管理工具集「改」（2026-09-18）──────────────────
  { feature: "任务170 rename_session / move_topic_to_group 工具", file: "internal/agent/session_manage_tools.go", patterns: ["func NewRenameSessionTool", "func NewMoveTopicToGroupTool", "\"move_topic_to_group\""] },
  { feature: "任务170 host 接线（改名三处同步 + 移动发 metadata 信号）", file: "desktop/session_manage_collab.go", patterns: ["func (a *App) renameCollabSession", "func (a *App) moveCollabTopicToGroup", "emitProjectTreeMetadataChanged"] },
  { feature: "任务170 移动语义（先摘旧组再加入）", file: "desktop/project_tree_organization.go", patterns: ["func (a *App) MoveTopicToGroup", "func groupContainsTopic"] },
  { feature: "任务170 工具注册", file: "internal/boot/boot.go", patterns: ["agent.NewRenameSessionTool(collab", "agent.NewMoveTopicToGroupTool(collab"] },

  // ── 任务 187：大会话首开读放大治理（尾部重放 + 写保护）（2026-09-20）──
  { feature: "任务187 尾部窗口重放", file: "internal/agent/session_dag_replay.go", patterns: ["func replaySessionDAGTail", "func sessionDAGTailWindowStart", "errSessionDAGTailUnavailable", "tailTruncated"] },
  { feature: "任务187 首屏加载入口与阈值", file: "internal/agent/session_load.go", patterns: ["func loadSessionTranscriptTail", "sessionTranscriptTailThresholdBytes", "sessionTranscriptTailWindowBytes"] },
  { feature: "任务187 首屏 Session 与写保护", file: "internal/agent/save.go", patterns: ["func LoadSessionTail", "func (s *Session) upgradeTruncatedTranscriptForWrite", "loadSessionWithReader"] },
  { feature: "任务187 截断标记与会话克隆传递", file: "internal/agent/session.go", patterns: ["func (s *Session) TailTruncated", "tailTruncated:           s.tailTruncated"] },
  { feature: "任务187 Save 入口升级", file: "internal/agent/session_persist_observer.go", patterns: ["upgradeTruncatedTranscriptForWrite(path)"] },
  { feature: "任务187 重放失败负缓存", file: "internal/agent/session_replay_guard.go", patterns: ["func rememberSessionReplayRefusal", "func cachedSessionReplayRefusal", "sessionReplayRefusalLimit"] },
  { feature: "任务187 desktop hydrate 接线", file: "desktop/app.go", patterns: ["agent.LoadSessionTail(sessionPath)"] },

  // ── 任务 184：host 性能监控 + heap profile（2026-09-19）───────────
  { feature: "任务184 监控开关与配置字段", file: "internal/config/config.go", patterns: ["experimental_perf_monitor", "perf_monitor_interval_seconds", "perf_monitor_retention_hours"] },
  { feature: "任务244 B1 心跳空转自终止开关", file: "internal/config/config.go", patterns: ["experimental_autonomous_idle_terminate"] },
  { feature: "任务244 B1 心跳空转自终止逻辑", file: "desktop/heartbeat.go", patterns: ["heartbeatIdleTerminateStrikes", "evaluateIdleStreak", "IdleStreak"] },
  { feature: "任务244 B2 循环中性继续注记开关", file: "internal/config/config.go", patterns: ["experimental_loop_streak_note"] },
  { feature: "任务244 B2 循环中性继续注记逻辑", file: "internal/agent/run_loop.go", patterns: ["maxLoopStreakNotes", "Loop-streak note"] },
  { feature: "任务244 B3 等待返回前复查开关", file: "internal/config/config.go", patterns: ["experimental_event_wait_recheck"] },
  { feature: "任务244 B3 等待返回前复查逻辑", file: "internal/agent/event_wait_tool.go", patterns: ["eventWaitRecheckValue", "recheckSatisfied"] },
  { feature: "任务244 B5 孤儿租约收编开关", file: "internal/config/config.go", patterns: ["experimental_orphan_lease_reclaim"] },
  { feature: "任务244 B5 孤儿租约收编逻辑", file: "desktop/app.go", patterns: ["leaseReclaimDecision", "experimentalOrphanLeaseReclaim"] },
  { feature: "任务244 B4 恢复孤儿清扫开关", file: "internal/config/config.go", patterns: ["experimental_recovery_orphan_sweep"] },
  { feature: "任务244 B4 恢复孤儿清扫逻辑", file: "internal/session/recovery_store.go", patterns: ["SetOrphanSweepProbe", "settled orphan recovery operations"] },
  { feature: "任务P15 引导队列重入·恢复层（无主 steer_consumed=已应用残留，恢复即清理）", file: "internal/sessioninbox/recovery.go", patterns: ["item.State == StateSteerConsumed || isSettled(item)"] },
  { feature: "任务P15 引导队列重入·存量清理（transcript 已注入引导回执匹配 uncertain 残留）", file: "internal/sessioninbox/settle.go", patterns: ["func (s *Store) SettleAppliedResidue", "Only Uncertain rows qualify"] },
  { feature: "任务P15 引导队列重入·应用凭证读取", file: "internal/agent/session_steer_receipts.go", patterns: ["func AppliedSteerReceiptTexts"] },
  { feature: "任务543 引导重入·合并回执段解析（writer 同源字面量，id+段文本两键）", file: "internal/control/inbox_merge.go", patterns: ["parseMergedSteerSegments", "mergedSegmentHeaderPrefix"] },
  { feature: "任务543 引导重入·结算匹配升级（段头 id 优先+全文精确+段文本兜底）", file: "internal/control/inbox.go", patterns: ["memberIDs[meta.ID]", "segmentBodies"] },
  { feature: "任务543 引导重入·未匹配 uncertain 残留可观测（count+样本 id）", file: "internal/sessioninbox/settle.go", patterns: ["uncertain residue kept without an applied-steer match"] },
  { feature: "任务449 实验室孤儿开关合并（新键渲染）", file: "internal/config/render.go", patterns: ["experimental_orphan_handling"] },
  { feature: "任务449 实验室孤儿开关合并（旧键迁移）", file: "internal/config/load.go", patterns: ["migrateOrphanHandlingMerge"] },
  { feature: "任务449 实验室孤儿开关合并（实验室单条 UI）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["orphanHandling", "SetExperimentalOrphanHandling"] },
  { feature: "任务244 B6 工具并发分级表文档化", file: "internal/agent/execute_batch.go", patterns: ["task 244 B6", "admission table", "no FIFO queue"] },
  { feature: "任务244 B7 心跳等待阈值推导注释", file: "desktop/heartbeat.go", patterns: ["task 244 B7: threshold-derivation note", "43,120"] },
  { feature: "任务244 B7 heldBy 方向语义注释", file: "internal/servepool/servepool.go", patterns: ["never route into a corpse", "experimental_orphan_lease_reclaim"] },
  { feature: "任务244 B7 roster 可路由方向注释", file: "internal/agent/session_collab_tools.go", patterns: ["routing into a corpse", "never a guessed idle"] },
  { feature: "任务244 B8 失败级联豁免面分类", file: "internal/agent/execute_batch.go", patterns: ["task 244 B8", "fail-cascade admission", "exemption face"] },
  { feature: "任务551 B9 键降 legacy read-only（旧 config 仍加载，B9 拦截已移除）", file: "internal/config/config.go", patterns: ["legacy task-244 B9 key", "READ-ONLY"] },
  { feature: "任务551 B9 键停渲染负断言测试（stale true 下次 save 消失）", file: "internal/config/experimental_render_test.go", patterns: ["TestLegacyModelCapabilityFilterStopsRendering"] },
  { feature: "任务551 B9 拦截移除·附件 opt-in 转发保留+结构化日志（误归因教训）", file: "internal/agent/task.go", patterns: ["forwardTurnAttachments", "subagent: forwarding turn attachments"] },
  { feature: "任务243 A2 dispatch ledger 轮内回显", file: "internal/agent/dispatch_ledger.go", patterns: ["dispatchLedger", "RecentDispatches", "maxDispatchLedger"] },
  { feature: "任务243 A3 恢复不变量测试", file: "internal/session/recovery_invariants_test.go", patterns: ["ClassificationReadsArePure", "RejectPathsLeaveFilesUntouched", "GenerationRebuildIsAtomic"] },
  { feature: "任务243 A5 空消息 merge 防御", file: "internal/control/inbox_merge.go", patterns: ["envelopeBodiesAllEmpty", "leaving queue untouched"] },
  { feature: "任务243 A6 topic 归一化与 purpose 键", file: "internal/agent/session_collab_tools.go", patterns: ["normalizeCollabRef", "collabRefMatches"] },
  { feature: "任务196fix save 链日志 path 双 root 归一", file: "internal/agent/session_persist_observer.go", patterns: ["canonicalSessionSavePath(path), \"messages\"", "canonicalSessionSavePath(path), \"ms\""] },
  { feature: "任务304 通道③ launcher relaunch-wait 落 desktop.log", file: "internal/desktoplauncher/waitfor.go", patterns: ["appendDesktopLogLine", "source=launcher"] },
  { feature: "任务304 通道①② lease+单实例锁 slog wording 钉测试", file: "desktop/quiet_channels_slog_test.go", patterns: ["session lease held; refusing session access", "second instance launched while"] },
  { feature: "任务304 通道④ 前端发送拒绝 reportFrontendLog + 错误路径日志规范", file: "REASONIX.md", patterns: ["Error paths must log", "reportFrontendLog"] },
  { feature: "任务330 429 专用等待 lane（主路径+段级）", file: "internal/agent/compact_projection.go", patterns: ["summaryRateLimited", "waiting to resume"] },
  { feature: "任务196fix 注记 path_mismatch 判定归一+226 Info canonical", file: "internal/agent/save_dag.go", patterns: ["sameSessionLogPath", "canonicalSessionSavePath(path), \"extended\""] },
  { feature: "任务307 physical ceiling 拒当轮 truncation 兜底", file: "internal/agent/context_manager.go", patterns: ["errors.Is(err, errCheckpointCeiling)", "End the loop"] },
  { feature: "任务521 baseproc ManagedClient nil 防线（typed-nil panic fail-closed 回落本地）", file: "internal/baseproc/lifecycle.go", patterns: ["任务521", "return inline, nil"] },
  { feature: "任务521 三种 nil 注入测试（半构造视图/typed-nil receiver/state×nil remote）", file: "internal/baseproc/lifecycle_nil_test.go", patterns: ["TestManagedClientNilManagerViewFallsBackToLocal", "TestManagedClientTypedNilReceiverNeverPanics", "TestManagedClientNilRemoteEveryStateFallsBackInline"] },
  { feature: "任务516 ceiling 拒绝改接受部分进度（解 307 同尺寸反复被拒死锁）", file: "internal/agent/compact_projection.go", patterns: ["installing partial fold progress", "candidateTokens >= sourceTokens"] },
  { feature: "任务516④ 投影失效回退全量显式日志（冷却节流+原因分类）", file: "internal/agent/context_manager.go", patterns: ["observeProjectionFallback", "projectionFallbackWarnCooldown"] },
  { feature: "任务357 save 链毫秒分段打点（phases+dag 三段+锁等待）", file: "internal/agent/save.go", patterns: ["session: save phases", "lastSaveLockWaitMs.Store"] },
  { feature: "任务221#6 unread 计数与 inbox 真值对账（claim 背书+渲染面）", file: "internal/sessioncollab/sessioncollab.go", patterns: ["InboxStatus reports, read-only", "seen[m.ID]"] },
  { feature: "任务297 冷缓存压缩 tick 判据与防循环", file: "desktop/cold_cache_compact.go", patterns: ["coldCacheCompactDecision", "already compacted this cooling window", "cold cache compact completed"] },
  { feature: "任务424 冷缓存压缩无可折叠区终止（park+新活动重武装）", file: "desktop/cold_cache_compact.go", patterns: ["coldCacheCompactTerminal", "parked: no foldable region remains (re-arms on new activity)", "cold cache compact parked"] },
  { feature: "任务424 无可折叠区哨兵错误（终端压缩结果）", file: "internal/agent/preflight.go", patterns: ["ErrNoFoldableRegion", "no foldable region remains"] },
  { feature: "任务380 冷缓存压缩对账日志（压前压后字节+catalog 成本估算）", file: "desktop/cold_cache_compact.go", patterns: ["coldCacheUsageCapture", "bytesBefore", "costAmount", "costCurrency"] },
  { feature: "任务356 手动挡修复按钮不置灰（分档 disabled+confirm+分档 hint）", file: "desktop/frontend/src/components/SessionEventsPanel.tsx", patterns: ["mode === \"manual\" ? busy : busy || entry.busy", "busyConfirm", "busyHintManual"] },
  { feature: "任务297 实验室存储成本卡（开关+两数值）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["SetColdCacheCompactMinBytes(kb * 1024)", "SetColdCacheCompactIdleMinutes(h * 60)"] },
  { feature: "任务347 驻留并入缓存调优第四块+LRU 容量字段", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["SetDagGraphCacheCapacity(v)", "SetExperimentalActiveTabResident(on)"] },
  { feature: "任务347 view 报告图缓存生效容量（196fix2 契约读回）", file: "desktop/settings_app.go", patterns: ["DagGraphCacheCapacity:           config.DagGraphCacheCapacity(cfg)"] },
  { feature: "任务345 修复会话列表 会话名兜底+状态+繁忙禁用", file: "desktop/frontend/src/components/SessionEventsPanel.tsx", patterns: ["entry.busy", "statusBusy", "title={entry.path}"] },
  { feature: "任务345 Go 侧 display title 链+Busy 字段", file: "desktop/session_events_app.go", patterns: ["sessionEventsDisplayTitle", "Busy bool `json:\"busy\"`"] },
  { feature: "任务346 用量卡 control 纵向分组（开关独行/三档对齐/刷新右对齐）", file: "desktop/frontend/src/styles.css", patterns: [".settings-field--opencode-usage .settings-field__control", "align-self: flex-end"] },
  { feature: "任务184 监控采样器与 heap profile", file: "desktop/perf_monitor.go", patterns: ["func (a *App) SaveHeapProfile", "func (m *perfMonitor) writeHeapProfile", "perf-sample-", "perfMonitorHeapKept"] },
  { feature: "任务184 窗口指标（Win32 计数器）", file: "desktop/perf_monitor_windows.go", patterns: ["GetProcessMemoryInfo", "GetProcessIoCounters"] },
  { feature: "任务184 session 常驻统计", file: "internal/session/stats.go", patterns: ["func (s *Service) OperationResidency", "func (s *Session) OperationResidency"] },
  { feature: "任务184 设置页入口", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["perfMonitor", "settings.perfMonitor.heapAction"] },

  // ── 构建配置 ────────────────────────────────────────────────────
  { feature: "release notes 存在", file: "release-notes/FORK-v1.33.0.md", patterns: ["Fork 修复"] },
  { feature: "wails 版本号", file: "desktop/wails.json", patterns: ["1.38.3"] },
  { feature: "任务362 修复会话菜单入口=开关态（manual/auto 点亮仅 off 置灰）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["on: (s.eventsAutoRotation ?? \"manual\") !== \"off\""] },
  { feature: "任务365 级联审批断链：create 首信即挂 grant（C5）+ 15s 超时再评转父（C6）", file: "desktop/session_collab.go", patterns: ["Task 365 C5", "registerCascadeGrant(item.ContactID, from)"] },
  { feature: "任务365 C6 unattended 超时 cascade 再评", file: "internal/control/autopilot_approval.go", patterns: ["cascade re-evaluation missed", "cascaded to task source"] },
  { feature: "任务367 C1 hop 单跳恒放行钉死（对照表入码）", file: "internal/agent/cascade_hop.go", patterns: ["single-hop delegation is always allowed", "depth <= 1"] },
  { feature: "任务367 C2 父侧决策留痕 slog", file: "internal/control/controller.go", patterns: ["cascade %s by parent %s"] },
  { feature: "任务366 153 合并下条可达性：merge-all 交叉提示（S1）+ 隐藏原因位日志（S2）", file: "desktop/frontend/src/components/ComposerGuidanceShelf.tsx", patterns: ["guidanceMergeNextUnavailableAll", "guidance-merge-next] hidden", "mergeNextActive = onMergeNext && index < items.length - 1"] },
  { feature: "任务374fix 乐观写读回链测试钉死（Set→Settings→JSON+序列化+wiring）", file: "desktop/optimistic_write_view_test.go", patterns: ["TestOptimisticWriteRoundTripThroughSettingsView", "TestSandboxViewAlwaysSerializesOptimisticWrite"] },
  { feature: "任务381 快速切换 staging 目录可配置+复位", file: "desktop/version_switch.go", patterns: ["func stagingRoot()", "staging_dir"] },
  { feature: "任务381 staging 读取点全收敛", file: "desktop/restart_update.go", patterns: ["stagingRoot()"] },
  { feature: "任务383 #2 workbench 项目分组入口（header 菜单 group 项）", file: "desktop/frontend/src/components/ProjectTreeAddControls.tsx", patterns: ["new-project-group", "onGroup"] },
  { feature: "任务383 #5 classic footer icon-only（creation 保文字）+#8 automation→heartbeat 文案统一", file: "desktop/frontend/src/App.tsx", patterns: ["sidebarCreation ? <span>{t(\"sidebar.trash\")}</span> : <span className=\"sr-only\">", "heartbeat.scheduler"] },
  { feature: "任务383 #10 layout 枚举注释三值一致 + 十条裁决验收 harness（#2 菜单接线 / #8 locale 去重守卫）", file: "desktop/frontend/src/__tests__/task383-layout-consistency.test.tsx", patterns: ["new-project-group", "Go normalizer emits exactly the three canonical values", "no longer defines sidebar.automation"] },
  { feature: "任务375 collab unknown 状态自解释 hint（status+talk 回执+FORK）", file: "internal/agent/session_collab_tools.go", patterns: ["unknown = this process cannot see the session", "targetStatusHint", "authoritative dispatch evidence"] },
  { feature: "任务387 跨会话换模型增强：非己拒绝+审计行+effort note+actionable 列表", file: "internal/agent/session_control_tool.go", patterns: ["calling session itself", "cross-session model change", "old_model", "resets to the new model"] },
  { feature: "任务387 unknown-model actionable 列表包装", file: "desktop/session_collab.go", patterns: ["wrapUnknownModelErr", "available models on that session"] },
  { feature: "任务388 autopilot 代批上下文感知：两档 scope+自然语言 manifest（tail-kept 有界）", file: "internal/control/autopilot_approval.go", patterns: ["autopilotProxyContext", "PROXY SCOPE: level 1", "PROXY SCOPE: level 2", "PROXY MANIFEST"] },
  // 任务 433：recovery fence 无副作用工具白名单。三个锚点按链路登记——
  // 白名单表本体（bash 走 shellsafe 只读判定，fail closed）、中断即判未生效
  // 的 finish 钩子、prompt 收尾把 outcome-unknown 降级 not_started 的重分类。
  // 少任何一个都会退回「弹面板等人点」或「误导模型去核实不存在的副作用」。
  { feature: "任务433 fence 无副作用白名单判定（枚举表+shellsafe 只读 bash）", file: "internal/agent/tool_recovery_records.go", patterns: ["noSideEffectTools", "interruptedCallSideEffectFree", "bashCommandSideEffectFree", "IsPermissionReader"] },
  { feature: "任务433 白名单中断自动判未生效+notice 留痕", file: "internal/agent/tool_recovery_records.go", patterns: ["resolveSideEffectFreeInterruptedCalls", "autoResolveSideEffectFreeRecord", "noticeCodeSideEffectFreeAutoResolved"] },
  { feature: "任务433 prompt 收尾未决重分类（unknown→not_started）", file: "internal/agent/interrupted_recovery.go", patterns: ["reclassifySideEffectFreeUnknowns", "interruptedSummarySideEffectFree"] },
  { feature: "任务389 catalog 空闲 CPU 修复：30s 循环→fsnotify watch 单点移植（上游 #10603）", file: "desktop/session_catalog_watch.go", patterns: ["func (a *App) watchSessionCatalog", "5 * time.Minute"] },
  { feature: "任务389 循环移除守护", file: "desktop/session_catalog_lifecycle.go", patterns: ["watchSessionCatalog(ctx, catalog)"] },
  { feature: "任务386 markdown cache 碰撞守卫 backstop 测试（消费比对 miss 语义）", file: "desktop/frontend/src/__tests__/markdown-history.test.tsx", patterns: ["fidelity backstop at the store boundary", "treats the collision as a miss"] },
  // 任务421 switch-tab ancillary effort 补取止血：EffortForTab 必须经超时上限+缓存兜底包装，
  // 直读函数不得被 binding 面直接调用。锚点锁「包装入口 + 上限常量 + 三态测试」，
  // 上游 merge 若把 EffortForTab 还原成直读（无上限），7-15s 坏样本会无声复发。
  { feature: "任务421 effort 补取超时上限+缓存兜底（入口包装）", file: "desktop/effort_fetch.go", patterns: ["effortReadTimeout = 2 * time.Second", "func (a *App) EffortForTab(tabID string) EffortInfo", "serving cached value"] },
  { feature: "任务421 直读函数让位包装（app.go 不再直连 binding）", file: "desktop/app.go", patterns: ["func (a *App) effortForTabDirect(tabID string) EffortInfo"] },
  { feature: "任务421 三态测试（超时兜底/冷缓存缺省/正常刷新）", file: "desktop/effort_fetch_test.go", patterns: ["TestEffortForTabTimeoutServesCachedValue", "TestEffortForTabTimeoutWithoutCacheServesDefault", "TestEffortForTabNormalReadRefreshesCache"] },
  // 任务 245：面板记忆键域级归一。三个锚点锁「键只经 workspacePanelMemoryRoot 产出」：
  // 映射函数本体（global→单键分支）+ 两条接线点（App 与 composition）的调用形状。
  // 上游若重新引入 `?? state.meta?.cwd` 兜底，Global 域面板记忆会重新按会话碎裂。
  { feature: "任务245 面板键域级映射（global 单键 + cwd 兜底禁入）", file: "desktop/frontend/src/store/layout.ts", patterns: ["export function workspacePanelMemoryRoot(scope: string | undefined, workspaceRoot: string | undefined): string {", "if (scope === \"global\") return \"\";"] },
  { feature: "任务245 面板键归一接线（App 侧）", file: "desktop/frontend/src/App.tsx", patterns: ["workspacePanelMemoryRoot(activeTab?.scope, activeTab?.workspaceRoot)"] },
  { feature: "任务245 面板键归一接线（composition 侧）", file: "desktop/frontend/src/app-runtime/useAppSessionComposition.ts", patterns: ["workspacePanelMemoryRoot(activeTab?.scope, activeTab?.workspaceRoot)"] },
  // 任务260 侧边栏「产物」「参考」两个 dock tab（复用任务114 聚合，挂 todoSidebar 实验族开关）：
  // 锚点锁「dock 面板组件存在 + App 两 tab 接线经 dockTabVisible 门控 + 模式可持久化」，
  // 上游若砍掉 tab 行或让模式被 normalize 吞掉，侧边栏扩展就静默缩回四 tab。
  { feature: "任务260 会话产物/参考 dock 面板（单变体复用 114 聚合）", file: "desktop/frontend/src/components/SessionSideFilesPanel.tsx", patterns: ["export function SideFilesDockPanel", "collectSessionSideFiles(items)"] },
  { feature: "任务260 App dock 两 tab 接线（门控+标签+体渲染）", file: "desktop/frontend/src/App.tsx", patterns: ["dockTabVisible(\"artifacts\")", "dockTabVisible(\"references\")", "SideFilesDockPanel"] },
  { feature: "任务260 dock 模式域级扩展（artifacts/references 可持久化+开关门）", file: "desktop/frontend/src/store/layout.ts", patterns: ["export function dockModeWithinSidebarGates"] },
  // 任务261 composer 历史导航安全化（上游 #10425，实验开关默认关）：锚点锁
  // 「↑ 触发收窄选项存在 + Composer 双接线（收窄判定+时钟面板）+ 渲染表落键」。
  // 上游若回退 ArrowUp 触发条件或砍掉渲染行，误触替换与开关自灭都会复发。
  { feature: "任务261 ↑ 触发收窄选项（默认关=旧行为）", file: "desktop/frontend/src/lib/composerKeyboard.ts", patterns: ["upStartsOnlyFromEmpty"] },
  { feature: "任务261 Composer 接线（收窄判定+时钟历史面板）", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["upStartsOnlyFromEmpty: historyPickerEnabled", "<PromptHistoryPicker"] },
  { feature: "任务261 开关渲染表落键（防保存丢失）", file: "internal/config/render.go", patterns: ["experimental_prompt_history_picker"] },
  // 任务128 会话自主创建新项目：工具（52=纯分配 / 128=创建+once 写授权+项目注册）+
  // host 注册接线（executeOne ctx stamping → desktop 注册项目并开后台 tab）。
  // 三处锚点锁住整条链——丢任何一环，agent 建的项目就静默退化为「仅返回路径」。
  { feature: "任务128 open_isolated_worktree_project 工具（创建+once 写授权+注册边界）", file: "internal/tool/builtin/open_worktree_project.go", patterns: ["WorktreeProjectOpenerFromContext", "hostRegistered", "create_worktree = allocation only"] },
  { feature: "任务128 agent 侧 opener 传递（executeOne ctx stamping）", file: "internal/agent/execute_one.go", patterns: ["WithWorktreeProjectOpener(cctx, a.svc.worktreeProjectOpener)"] },
  { feature: "任务128 desktop 注册实现（项目 topic+后台 tab+幂等防重复）", file: "desktop/worktree_project_opener.go", patterns: ["func (o appWorktreeProjectOpener) OpenIsolatedWorktreeProject", "visibleProjectTabIDForRoot", "openProjectTabInactive"] },
  { feature: "任务128 boot Options 字段（host 能力下发）", file: "internal/boot/boot.go", patterns: ["WorktreeProjectOpener tool.WorktreeProjectOpener"] },

  // ── 任务 172：意见箱触达策略（T1 完成时 + T2 插话时 + 面板打开目录）───
  { feature: "任务172 nudge 子开关全链路（config 字段+父开关优先）", file: "internal/config/desktop_preferences.go", patterns: ["experimental_feedback_nudge", "func (c *Config) FeedbackNudgeEnabled()"] },
  { feature: "任务172 渲染表登记（漏渲染表=保存被静默丢弃）", file: "internal/config/render.go", patterns: ["experimental_feedback_nudge"] },
  { feature: "任务172 T1/T2 双触发+防死循环闸门", file: "internal/agent/feedback_nudge.go", patterns: ["FeedbackNudgeMarker", "feedbackNudgeCooldownTurns", "feedbackNudgeMaxPerTurn"] },
  { feature: "任务172 run_loop 注入点（T1 完成时 + T2 插话后）", file: "internal/agent/run_loop.go", patterns: ["maybeNudgeFeedbackCompletion", "maybeNudgeFeedbackSteer"] },
  { feature: "任务172 App 绑定 GetFeedbackInboxPath（先建目录再回路径）", file: "desktop/feedback.go", patterns: ["func (a *App) GetFeedbackInboxPath", "builtin.FeedbackInboxDir()"] },
  { feature: "任务172 面板打开目录按钮（清空左侧，复用 RevealPath）", file: "desktop/frontend/src/components/FeedbackPanel.tsx", patterns: ["feedbackInbox.openDir", "app.RevealPath(dir)"] },
  { feature: "任务172 设置页触达子项+开销说明文案", file: "desktop/frontend/src/locales/zh.ts", patterns: ["settings.feedbackNudge", "settings.feedbackNudgeHint"] },
  // ── 任务 530：turn 闭合回信提醒钩子（173 的覆盖件）──────────────
  { feature: "任务530 提醒本体（扫描+MarkNotified 一次性+可见注入）", file: "internal/agent/collab_reply_nudge.go", patterns: ["CollabReplyNudgeMarker", "owedRequireReplies", "MarkNotified(me, m.ID+\":\"+collabReplyNudgeKind)", "HostGeneratedUserMessage(a.withTurnPreferences(collabReplyNudgeMessage(due)))"] },
  { feature: "任务530 run_loop 注入点（172 T1 之前，欠回信优先拿轮次）", file: "internal/agent/run_loop.go", patterns: ["maybeNudgeCollabReply"] },
  { feature: "任务530 Options→svc 接线（nil=逐字零行为）", file: "internal/agent/services.go", patterns: ["collabReplyNudge:      opts.CollabReplyNudge"] },
  { feature: "任务530 开关全链（config 字段+渲染表+setter）", file: "internal/config/config.go", patterns: ["session_collab_reply_nudge"] },
  { feature: "任务530 boot 接线（父开关 AND，executor 后补 resolver）", file: "internal/boot/boot.go", patterns: ["collabReplyNudge.ResolveSessionPath = executor.SessionPath", "sessionCollabEnabled(cfg) && cfg.Agent.SessionCollabReplyNudge"] },
  { feature: "任务530 已读未回扫描的存储读口（settled 不消失）", file: "internal/sessioncollab/sessioncollab.go", patterns: ["func (s *MailStore) InboxMessages"] },
  { feature: "任务530 面板开关+三语文案", file: "desktop/frontend/src/locales/zh.ts", patterns: ["settings.sessionCollabReplyNudge", "settings.sessionCollabReplyNudgeHint"] },
  // ── 任务 549：投影失效窗口治理（观测补口 + 失效即重建 + 面板语义）──
  // 三条都是行为修正：失效日志+重建 kick 被「函数还在但逻辑被顶掉」式 merge
  // 静默回退时，canonical 级读数暴涨会无痕复发，故逐条登记。
  { feature: "任务549 失效观测（带原因的结构化丢弃日志）", file: "internal/agent/preflight.go", patterns: ["func (a *Agent) invalidateProjection(reason string)", "agent: context projection invalidated", "agent: context projection not restored"] },
  { feature: "任务549 失效即重建（kick 门控+单飞）", file: "internal/agent/preflight.go", patterns: ["func (a *Agent) kickProjectionRebuild", "a.sess.rebuildPending.CompareAndSwap(false, true)", "projectionRebuildTimeout"] },
  { feature: "任务549 content_edit rewind 触发者字段", file: "internal/agent/save_dag_plan.go", patterns: ["Writer: SessionWriterID()"] },
  { feature: "任务549 面板失效标注（后端字段）", file: "internal/agent/context_status.go", patterns: ["ProjectionValid bool"] },
  { feature: "任务549 桌面桥接 projectionValid", file: "desktop/context_maintenance.go", patterns: ["json:\"projectionValid\""] },
  { feature: "任务549 面板主读数 projected+失效徽标", file: "desktop/frontend/src/components/ContextPanel.tsx", patterns: ["context-panel__projection-stale", "context?.maintenance?.projectedTokens ?? 0"] },
  { feature: "任务549 失效标注三语文案", file: "desktop/frontend/src/locales/zh.ts", patterns: ["context.projectionInvalid", "context.projectionInvalidTitle"] },
  // ── 任务 545：会话 cwd 跟随会话（2026-10-06）────────────────────
  // 共享解析（546 同源）+ 实验开关（默认关，渲染表防静默丢）+ CLI/serve 两处接线。
  { feature: "任务545 会话项目根解析（meta.WorkspaceRoot，546 共用语义）", file: "internal/agent/session_workspace.go", patterns: ["func SessionWorkspaceRoot", "meta.WorkspaceRoot"] },
  { feature: "任务545 开关全链（config 字段 + 渲染表显式渲染）", file: "internal/config/config.go", patterns: ["experimental_session_cwd_follow"] },
  { feature: "任务545 渲染表（81/123 防丢：关态也渲染）", file: "internal/config/render.go", patterns: ["experimental_session_cwd_follow"] },
  { feature: "任务545 CLI resume 接线（显式 --dir 优先，关态零行为）", file: "internal/cli/session_cwd.go", patterns: ["func resumeWorkspaceRootOverride", "ExperimentalSessionCwdFollow"] },
  { feature: "任务545 serve 忙碌换绑接线（目标会话项目根 pin）", file: "internal/serve/session_cwd.go", patterns: ["func sessionCwdFollowRootOverride"] },
  { feature: "任务545 serve 构建缝（rootOverride 覆盖继承根）", file: "internal/serve/multisession.go", patterns: ["buildTaggedWithOptions", "sessionCwdFollowRootOverride(targetPath)"] },
  // ── 任务 152：todo 树状任务系统（2026-09-30）────────────────────
  // schema/校验在 evidence（树状状态机 + 终态 + 层级 ID），工具面在 builtin/todo，
  // agent 侧树列表跳过扁平 normalizer，前端树渲染 + 归档区——五处都要在。
  { feature: "任务152 树状校验状态机+终态+层级 ID（Go）", file: "internal/evidence/evidence.go", patterns: ["validateTreeSerialTodos", "func HierarchicalTodoIDs", "func TodoTerminalStatus", "ParentID string `json:\"parent_id,omitempty\"`"] },
  { feature: "任务152 todo_write 工具面（parent_id schema+归档迁移守卫）", file: "internal/tool/builtin/todo.go", patterns: ["\"parent_id\":{\"type\":\"string\",\"description\":\"Task 152 tree", "cannot be archived straight from", "cannot jump from in_progress to archived"] },
  { feature: "任务152 树列表跳过扁平 normalizer", file: "internal/agent/agent.go", patterns: ["func todoListHasExplicitParents"] },
  { feature: "任务152 前端树数据层（终态/深度/层级码/批次归档分区）", file: "desktop/frontend/src/lib/todoVisibility.ts", patterns: ["export function todoTerminalStatus", "export function todoTreeDepths", "export function todoHierarchyCodes", "export function partitionTodoBatches"] },
  { feature: "任务152 面板树渲染+归档区 TSX", file: "desktop/frontend/src/components/TodoPanel.tsx", patterns: ["function TodoTree", "function TodoArchive", "todobar__archive-toggle", "todobar__caret"] },
  { feature: "任务152 树/归档 CSS（theme token）", file: "desktop/frontend/src/styles.css", patterns: [".todobar__item--deep", ".todobar__caret", ".todobar__code", ".todobar__archive-toggle"] },
  { feature: "任务152 压缩摘要任务树快照段", file: "internal/agent/compact.go", patterns: ["## Task tree"] },
  // M4a zcodebridge：reasonix→zcode 实时注入通道（spawn app-server --stdio，
  // 冻结面 = 握手 fail-closed + session/list + sendText + session/events afterSeq）。
  // 接线锚点锁三处：env 门、双启动点、协议闸门——任一被 merge 丢掉 = 静默失联。
  { feature: "M4a zcodebridge 客户端与握手 fail-closed", file: "internal/zcodebridge/zcodebridge.go", patterns: ["ErrProtocolMismatch", "func Open(", "func (b *Bridge) handshake("] },
  { feature: "M4a zcodebridge sendText/events 冻结面", file: "internal/zcodebridge/inject.go", patterns: ["DeliveryStartNow", "requestedDelivery", "func (b *Bridge) SendText("] },
  { feature: "M4a zcodebridge serve 接线（env 门+启动点）", file: "internal/serve/zcodebridge.go", patterns: ["REASONIX_ZCODE_BRIDGE", "func (s *Server) startZcodeBridge()", "runZcodeBridge"] },
  { feature: "M4a zcodebridge 双启动调用点", file: "internal/serve/serve.go", patterns: ["s.startZcodeBridge()"] },
  // bus#3 邮件→注入接线：zcode 角色信箱投递成功后经 zcodebridge 给运行中会话
  // 打实时提醒（queue 语义）。失败语义 = 邮件留在收件箱（不丢不重），zcode 会话
  // 自觉轮询兜底。锚点锁四处：busmcp 回调面、两处投递点、serve 注入器——任一被
  // merge 丢掉 = reasonix→zcode「实时」半环静默断裂（只剩轮询）。
  { feature: "bus3 busmcp 投递后注入回调面", file: "internal/busmcp/inject.go", patterns: ["type MailInjector interface", "func (s *Server) notifyInjector("] },
  { feature: "bus3 event 投递点接线", file: "internal/busmcp/busmcp.go", patterns: ["s.notifyInjector(s.eventTarget, msg)"] },
  { feature: "bus3 send/spawn 投递点接线", file: "internal/busmcp/tools.go", patterns: ["rt.bus.notifyInjector(msg.To, msg)"] },
  { feature: "bus3 serve 注入器（queue 语义+邮件留存兜底）", file: "internal/serve/zcodebridgeinject.go", patterns: ["func injectBusMailVia(", "zcodebridge.DeliveryQueue", "mail stays in inbox"] },
  { feature: "bus3 serve 接线注入器与桥 workspace 解析", file: "internal/serve/serve.go", patterns: ["Injector: zcodeMailInjector{s: s}", "zcodeBridgeWorkspace atomic.Pointer[string]"] },
  // ── bus#1：talk_to_session 放行 zcode- 合成联系人（reasonix 会话可发信 bus）──
  // 三处锚点锁整条链：agent 侧目录未命中后的 bus 联系人回退（放行本体）、config 侧
  // 联系人表/信箱目录的 live 解析（唯一数据面）、busmcp 复用同一张校验表（单源，
  // 防端点与 agent 两侧判表规则漂移）。丢任何一环，回退要么失活要么与端点不一致。
  { feature: "bus#1 talk_to_session 放行 zcode- 合成联系人（agent 回退）", file: "internal/agent/session_collab_tools.go", patterns: ["BusContacts func() []string", "func (t talkToSessionTool) resolveBusContact", "func (t talkToSessionTool) mailDirFor"] },
  { feature: "bus#1 bus 联系人表 live 解析与角色表单源校验（config）", file: "internal/config/bus.go", patterns: ["func ValidateBusRoles", "func BusContactsLive", "func BusMailDirLive", "var BusRolePattern"] },
  { feature: "bus#1 busmcp 复用 config 角色表校验（单源）", file: "internal/busmcp/busmcp.go", patterns: ["config.ValidateBusRoles(cfg.Roles)"] },

  // ── 任务 428：ask 面板投递门（2026-10-01）────────────────────────
  // 现场面：折叠条到了、面板从未弹出（turn 运行中 ask 双向干等）。
  // 两条吞没路径都要在：C1 残留 cancelRequested 不吞 ask（judgeAskArrival）、
  // C2 兼容激活不清空存活 prompt 等待（decideActivationPrompt，#6429 锚点
  // 语义保持）；打点三方按 prompt id + turn id 关联：前端 feature=ask-panel、
  // Go [ask-panel] emit 行、406 interrupted-turn-recovery 记录。
  { feature: "任务428 ask 面板投递门纯判定（C1/C2）", file: "desktop/frontend/src/lib/askPanelGate.ts", patterns: ["export function judgeAskArrival", "clearCancelResidue", "export function decideActivationPrompt", "resetPromptAnchor"] },
  { feature: "任务428 ask 面板投递门 reducer 接线", file: "desktop/frontend/src/lib/useController.ts", patterns: ["judgeAskArrival(", "decideActivationPrompt({", "reportAskPanelVerdict", "\"ask-panel\""] },
  { feature: "任务428 ask 打点（Go emit 侧）", file: "internal/control/controller.go", patterns: ["[ask-panel] ask request emitted", "[ask-panel] ask request emit failed"] },

  // 任务436（20261001 批十小件）：过长用户侧消息默认折叠限高。（任务437 心跳续跑锚点随 437 分支登记）
  { feature: "任务436 用户消息折叠限高（组件+阈值）", file: "desktop/frontend/src/components/Message.tsx", patterns: ["USER_MSG_FOLD_LINE_THRESHOLD", "estimateUserMessageLines", "msg-fold--clamped", "msg-fold__toggle"] },
  { feature: "任务436 用户消息折叠 CSS", file: "desktop/frontend/src/styles.css", patterns: [".msg-fold--clamped", ".msg-fold__toggle"] },
  // 任务446（20261002 批十一）：排队引导消息 hover 浮层预览（436 估行器抽轻量 lib 供复用）。
  { feature: "任务446 估行器轻量抽出（lib/messageFold）", file: "desktop/frontend/src/lib/messageFold.ts", patterns: ["USER_MSG_FOLD_LINE_THRESHOLD", "estimateUserMessageLines", "0x2e7f"] },
  { feature: "任务446 排队引导 hover 浮层（组件）", file: "desktop/frontend/src/components/ComposerGuidanceShelf.tsx", patterns: ["guidanceRowIsTruncated", "GUIDANCE_ROW_VISIBLE_LINES", "openHoverCard", "guidance-hover-preview"] },
  { feature: "任务446 排队引导 hover 浮层 CSS", file: "desktop/frontend/src/styles.css", patterns: [".guidance-hover-preview", "data-clipped"] },
  // 任务466（20261006）：引导队列全选（头栏三态勾选，与单选同一闸门）+ 批量栏丢弃按钮横排修复。
  { feature: "任务466 引导队列全选（头栏三态+共享闸门）", file: "desktop/frontend/src/components/ComposerGuidanceShelf.tsx", patterns: ["onToggleSelectAll", "composer-guidance-head__selectall", "rowSelectable"] },
  { feature: "任务466 批量丢弃按钮横排（专用文本按钮类）", file: "desktop/frontend/src/styles.css", patterns: [".composer-guidance-batchbar__dismiss", "white-space: nowrap"] },
  { feature: "任务466 全选接线（composer 全/无切换）", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["toggleGuidanceSelectAll"] },
  // 任务 339（上游 #10970 并用）：replay 预算双保险的第二道——触顶有出路。
  // 锚点锁「所有权证明→内存折叠→失败原样透出」的形状：fold 只在 ledger 仍
  // 等于本 runtime 基线时触发（防丢别的 writer 的新 turn），DAG 日志与非
  // limit 错误一律原样拒绝；auto 旋进 cap 分支带上游 fold-only-if-shrinks
  // 纪律（折不缩则留+可检索 WARN）。
  { feature: "任务339 replay 超限 Save 自救（所有权证明+内存折叠）", file: "internal/agent/save_replay_rescue.go", patterns: [
    "func (s *Session) rescueReplayLimitedEventLog(",
    "errors.As(cause, &limitErr)",
    "diskRevision != base.revision",
    "compact-replay-limit",
  ] },
  { feature: "任务339 Save probe 修复路径接线", file: "internal/agent/save.go", patterns: [
    "s.rescueReplayLimitedEventLog(path, msgs, rescueDigest, err)",
  ] },
  { feature: "任务339 classify 路径接线", file: "internal/agent/save_listing_projection.go", patterns: [
    "s.rescueReplayLimitedEventLog(path, msgs, digest, err)",
  ] },
  { feature: "任务339 auto cap 折叠缩水守卫（上游 fold-only-if-shrinks）", file: "internal/agent/session_events.go", patterns: [
    "fold would not shrink it",
    "contentBytes*2 >= logSize",
  ] },
  // 任务 340（上游 #10778 / #10254）：中断轮保留 provider 真错误。四道闸：
  // wire 契约常量（前端按 kind==="cancelled" 区分用户停止与真失败）、持久化
  // 摘要（写时脱敏，reload 后真错误仍在）、历史 notice 拼接、live reducer
  // 透出（吞错误分支补 warn 行）——任何一道被 merge 丢掉，#10254 复发。
  { feature: "任务340 FailureKindCancelled wire 契约常量", file: "internal/provider/failure_diagnostic.go", patterns: [
    "FailureKindCancelled = \"cancelled\"",
    "d.Kind = FailureKindCancelled",
  ] },
  { feature: "任务340 中断轮持久化真错误摘要（写时脱敏+定长）", file: "internal/agent/agent.go", patterns: [
    "func interruptedFailureSummary(",
    "secrets.RedactError(err)",
    "FailureSummary:          failureSummary",
  ] },
  { feature: "任务340 历史 reload notice 携带失败摘要", file: "desktop/app.go", patterns: [
    "recovery.FailureSummary != \"\"",
    "detail += recovery.FailureSummary",
  ] },
  { feature: "任务340 live reducer 中断分支透出真错误（cancelled 除外）", file: "desktop/frontend/src/lib/useController.ts", patterns: [
    "e.diagnostic.kind !== \"cancelled\" && !s.streamInterruptNoticeShown",
  ] },
  // 任务437（20261001 批十小件）：心跳复用已有会话续跑。（任务436 折叠限高锚点随 436 分支登记）
  { feature: "任务437 心跳续跑模式（Go 引擎）", file: "desktop/heartbeat.go", patterns: ["reuseSession,omitempty", "heartbeatReuseMode", "heartbeatFreshConversationMode"] },
  { feature: "任务437 心跳续跑（agent 工具面）", file: "internal/tool/builtin/heartbeat_tasks.go", patterns: ["ReuseSession", "reuseSession"] },
  { feature: "任务437 心跳续跑（前端开关+绑定行）", file: "desktop/frontend/src/custom/features/heartbeat/HeartbeatTaskEditor.tsx", patterns: ["reuseSession", "heartbeat-editor__bound-topic"] },
  { feature: "任务437 心跳续跑 CSS", file: "desktop/frontend/src/custom/features/heartbeat/heartbeat.css", patterns: [".heartbeat-reuse-badge", ".heartbeat-editor__bound-id"] },
  // 任务 249：GC 经 servepool 网关的显式接管必须先过 desktop 弹窗闸门——
  // pooled serve 是子进程，marker watcher 只覆盖租约冲突路径；无租约冲突时
  // 旧链路 204 静默成功、桌面零反馈。锚点锁三处：网关拦截（转发前闸门）、
  // 桌面闸门（复用弹窗桥 + 接受即释放租约）、启动接线——丢任何一环 =
  // GC 接管再次静默成功。
  { feature: "任务249 网关接管闸门（转发前拦截）", file: "internal/servepool/gateway.go", patterns: ["takeoverGate TakeoverGateFunc", "gateTakeover(w, r, id)", "tail == \"takeover-session\""] },
  { feature: "任务249 桌面接管闸门（弹窗桥 + 释放租约）", file: "desktop/servepool_takeover_gate.go", patterns: ["func (a *App) servePoolTakeoverGate", "registerTakeoverPending(marker)", "func (a *App) yieldTabsToGatewayTakeover"] },
  { feature: "任务249 闸门接线（startServePool）", file: "desktop/servepool_host.go", patterns: ["a.installServePoolTakeoverGate(gw)"] },
  // 任务 276：输入框易失焦。三探针（epoch bump / IME 组合被打断 / 焦点恢复
  // 被取消）统一走 composer-focus 通道落 desktop.log；修复 b = focus 落位
  // 校验 + 有界帧内重试 + 禁用翻转自愈（runtimeState.unknown 等瞬时态静默
  // 夺焦后归还）。锚点锁探针与自愈两侧——merge 丢任何一侧，复发即失证据。
  { feature: "任务276 焦点三探针（composer-focus 通道）", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["draft epoch bumped", "focus restore cancelled by draft epoch", "focus restore exhausted retries"] },
  { feature: "任务276 IME 组合打断探针", file: "desktop/frontend/src/lib/useComposerImeGuard.ts", patterns: ["ime composition interrupted by focus loss", "ime composition interrupted by unmount"] },
  { feature: "任务276 禁用翻转焦点自愈", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["focus restored after disable flip", "composerInputWasFocusedRef"] },
  // 任务 278：快捷指令弹窗宽度链（核心加宽 3ccd3d2b9，此处钉住防顶掉）——
  // wide shell 持有宽度（900px+视口兜底）、内容块填充不设内层下限、行内
  // 内容列可收缩（min-width:0 防超长无空格内容把行撑出横向滚动条）。
  { feature: "任务278 快捷指令弹窗宽度链", file: "desktop/frontend/src/styles.css", patterns: ["settings-quick-commands--wide {\n  width: 100%;", "settings-quick-commands__row > textarea.mem-input {\n  flex: 2 1 52%;", "任务 278 防反弹"] },
  { feature: "任务278 wide 弹窗外壳（900px+视口兜底）", file: "desktop/frontend/src/components/ProviderAccessSettings.css", patterns: [".provider-dialog--wide { width: min(900px, calc(100vw - 32px)); }"] },
  // 任务 323：multi_edit 引导——正文裁决是描述改文案（不放 AGENTS.md/技能）：
  // multi_edit 前置 WHEN TO USE 触发句、edit_file 尾部互引，加中文触发词
  // （172 惯例）。锚点锁双侧指路，merge 静默丢任一侧即报。
  { feature: "任务323 multi_edit WHEN TO USE 触发句+中文触发词", file: "internal/tool/builtin/multiedit.go", patterns: ["WHEN TO USE: modifying 2+ places in the same file", "INSTEAD of chained edit_file calls", "同文件多处修改", "批量修改"] },
  { feature: "任务323 edit_file 尾部互引 multi_edit", file: "internal/tool/builtin/editfile.go", patterns: ["For multiple edits in one file (同文件多处修改), prefer multi_edit (atomic batch)"] },
  // 任务 324：反馈触发规则定稿（2026-09-26 用户批准）内嵌 submit_feedback
  // 描述——4 触发全带证据 / 5 不触发 / 三闸（≤2 条每会话、≤5 行带标签、
  // ≥5 分钟或 ≥3 轮才投）。低噪声优先：merge 丢规则段即失守，锚点锁死。
  { feature: "任务324 submit_feedback 4 触发+5 不触发", file: "internal/tool/builtin/feedback.go", patterns: ["(A) a tool or guard misbehaved", "(B) docs or a tool description contradicts actual behavior", "3+ times in a row", "(D) the user explicitly asks", "nice-to-have suggestions", "a topic already in the inbox"] },
  { feature: "任务324 submit_feedback 成本三闸+172 中文触发词保留", file: "internal/tool/builtin/feedback.go", patterns: ["at most 2 notes per session", "at most 5 lines", "[bug], [gap], or [docs]", "5+ minutes or 3+ wasted rounds", "意见箱, 反馈, 记一条意见"] },
  // 任务 348：会话身份与职责结构化字段（三层架构角色字段化）。锁三处——
  // ①枚举闭集（human|main|sub|heartbeat|system，无第七种私造值）②presence-
  // based 写入（旧调用不清新字段=零迁移）③通讯录行带出（omitempty 形状
  // 不变）。merge 丢任一处，字段会静默退化回纯 purpose 而构建不红。
  { feature: "任务348 身份枚举闭集+扫描器带出", file: "internal/sessioncollab/sessioncollab.go", patterns: ["IdentityHeartbeat", "NormalizeIdentityType", "ScanDirMeta", "identityType,omitempty"] },
  { feature: "任务348 SetSessionDuty presence 写入", file: "internal/agent/branch.go", patterns: ["func SetSessionDuty(", "identity_type,omitempty"] },
  { feature: "任务348 工具面加参与通讯录行导出", file: "internal/agent/session_collab_tools.go", patterns: ["SetSessionDuty(session, p.Purpose", "identityDomain,omitempty"] },
  // 任务 396（上游 #11311 → #11327）：Windows 非 UTF-8 code page 的 shell 输出
  // 乱码。解码收口在 RunForeground（转录/工具卡的唯一输出来源），级联 = 严格
  // UTF-8 直通 → 控制台输出码页 → ANSI 码页 →（中文系统）GB18030，全拒则回落
  // 原文字节。锚点锁三处：入口接线、Windows 码页候选映射、回落语义——丢任何
  // 一处，GBK 输出重新以 U+FFFD 进转录。
  { feature: "任务396 shell 输出码页解码（RunForeground 收口）", file: "internal/shellrun/runner.go", patterns: ["decodeConsoleOutput(collector.combined.String())", "utf8SafeTrimTail(combined, tool.OutputTailMaxBytes)"] },
  { feature: "任务396 Windows 码页候选映射（控制台输出码页优先）", file: "internal/shellrun/codepage_windows.go", patterns: ["windows.GetConsoleOutputCP", "encodingForCodePage", "chineseSupersetDecoder"] },
  { feature: "任务396 解码失败回落原文（不阻断不崩）", file: "internal/shellrun/codepage.go", patterns: ["utf8.ValidString(raw)", "tryDecodeCodePage", "return raw"] },
  // 任务 397（上游 #11329/#11323/#11247）：MemoryBench 评测有效性。三处对照中
  // 两处同源修复：mb-contradiction 负向匹配词边界（"pnpm install" 含子串
  // "npm install"，裸负向=结构性永假）+ memory-off 对照臂空隔离 state home
  // （种子不再落在 agent 经 shell env 可读的磁盘路径）。#11323 经核对 fork 已
  // 隔离（verify.sh 延迟投放+临时 workdir），锚点锁修复两处——被 merge 丢掉
  // 即评测数字重新失真。
  { feature: "任务397 mb-contradiction 负向匹配词边界", file: "benchmarks/memorybench/tasks/mb-contradiction/verify.sh", patterns: ["grep -qE \"(^|[^A-Za-z])npm install\""] },
  { feature: "任务397 memory-off 臂空隔离 state home", file: "cmd/e2ebench/memorybench.go", patterns: ["e2ebench-memoff-", "cfg.policy == \"memory-off\""] },
  // 任务 401（上游 #11168 → #11188）：侧栏品牌 logo 随主题强调色着色。svg 资产
  // 不动，改成 masked span 用 --accent 上色；锚点锁三处——两个渲染点（App.tsx
  // 与 SidebarRegion 各两形态）不再出 <img>、CSS 用 var(--accent) 走 SVG mask、
  // 暗色 brightness/invert 规则不再认领 .sidebar__brand-logo（否则强调色被压成
  // 纯白）。丢任何一处，logo 回退成固定品牌蓝或被反相成白。
  { feature: "任务401 侧栏 logo 强调色 mask（App.tsx 渲染点）", file: "desktop/frontend/src/App.tsx", patterns: ["<span role=\"img\" aria-label=\"Reasonix\" className=\"sidebar__brand-logo sidebar__brand-logo--workbench\" />", "<span role=\"img\" aria-label=\"Reasonix\" className=\"sidebar__brand-logo\" />"] },
  { feature: "任务401 侧栏 logo 强调色 mask（SidebarRegion 渲染点）", file: "desktop/frontend/src/app-shell/SidebarRegion.tsx", patterns: ["<span role=\"img\" aria-label=\"Reasonix\" className=\"sidebar__brand-logo sidebar__brand-logo--workbench\" />", "<span role=\"img\" aria-label=\"Reasonix\" className=\"sidebar__brand-logo\" />"] },
  { feature: "任务401 侧栏 logo 强调色 mask（CSS 上色与暗色规则解绑）", file: "desktop/frontend/src/styles.css", patterns: ["background-color: var(--accent);", "-webkit-mask-image: url(\"./assets/logo-wordmark.svg\");", ":root[data-theme=\"dark\"] .welcome__brand-logo {"] },
  // 任务 400（上游 #11272 → #11282）：会话历史加载失败显示原因。固定文案升级为
  // 「摘要 + 读端自身错误」——锚点锁四处：hydrateFailureDetail 纯函数（无因不拼、
  // 同文不重）、loadTimed 错误入参 historyLoadCause 接线、hydrate_error 走 detail
  // 模板而 local_notice 保摘要、resume/channel 捕获错误透传 failSessionNavigation。
  // 丢任何一处，banner Details 重新只剩固定标题，真实因由再次不可见。
  { feature: "任务400 失败原因合成 helper", file: "desktop/frontend/src/lib/hydrateErrorState.ts", patterns: ["export function hydrateFailureReason", "export function hydrateFailureDetail", "reason === summary"] },
  { feature: "任务400 历史读端错误入 hydrate_error", file: "desktop/frontend/src/lib/useController.ts", patterns: ["(err) => { historyLoadCause = err; }", "historyLoadCause,", "text: t(\"history.failedLoadHistory\")"] },
  { feature: "任务400 导航失败透传 cause", file: "desktop/frontend/src/lib/useController.ts", patterns: ["tabId: string, cause?: unknown", "failSessionNavigation(navigationSeq, targetTabId, resumeErr)", "failSessionNavigation(navigationSeq, tabId, channelErr)"] },
  { feature: "任务400 详情文案三语 i18n", file: "desktop/frontend/src/locales/zh.ts", patterns: ["history.failedLoadHistoryDetail", "history.failedOpenSessionDetail", "{reason}"] },
  // 任务 320：跨会话收件箱（历史查询/筛选/保留期 + 五桶聚合 + revision/
  // dismiss 落库两契约）。锁索引核心、传输层 Prune/History、只读查询工具、
  // boot 注册、Wails 面、面板组件与 locale——任一侧被上游 merge 摘掉，
  // 收件箱都会静默少一块而构建不红。
  { feature: "任务320 收件箱索引核心（五桶/保留/revision）", file: "internal/collabinbox/collabinbox.go", patterns: ["func (s *Store) List(", "BucketAutomation", "ApplyRetention", "revisionOf"] },
  { feature: "任务320 传输层 History+PruneInbox+Kind", file: "internal/sessioncollab/sessioncollab.go", patterns: ["func (s *MailStore) History", "PruneInbox", "Kind string"] },
  { feature: "任务320 query_collab_mail 只读查询工具", file: "internal/agent/query_collab_mail_tool.go", patterns: ["query_collab_mail", "applyRetention=false"] },
  { feature: "任务320 查询工具 boot 注册", file: "internal/boot/boot.go", patterns: ["NewQueryCollabMailTool(collab)"] },
  { feature: "任务320 Wails 收件箱面", file: "desktop/collab_inbox_app.go", patterns: ["func (a *App) ListCollabMail(", "MarkCollabMailDecided", "SetCollabMailRetention"] },
  { feature: "任务320 收件箱面板组件", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["collab-inbox-panel__bucket", "SetCollabMailRetention", "ListCollabMailChains"] },
  { feature: "任务320 收件箱三语 locale", file: "desktop/frontend/src/locales/zh.ts", patterns: ["collabInbox.title", "collabInbox.bucket.approval"] },
  // 任务 320 UI 规格（20261002 用户钦定）：面板入口进左下角图标行（与回收站/
  // 自动化/设置同排，邮箱图标）。锁 App.tsx 接线与三语键——图标行是常驻入口，
  // 被 merge 摘掉则面板退回仅设置可达。
  { feature: "任务320 左下角图标行入口（邮箱图标+开合接线）", file: "desktop/frontend/src/App.tsx", patterns: ["Mailbox size={16}", "setCollabInboxOpen(true)", "sidebar.collabInbox"] },
  { feature: "任务320 图标行入口三语 locale", file: "desktop/frontend/src/locales/zh.ts", patterns: ["sidebar.collabInbox"] },
  // 任务 320 余段（20261003）：a 的面板侧日期升/降切换——索引层 Query.Order
  // 双序早已在，缺的是 Wails 透传与面板控件；锁 Go 参数、控件类与三语键，
  // 任一侧被摘则排序切换静默失灵。
  { feature: "任务320 面板日期排序切换（Wails 透传）", file: "desktop/collab_inbox_app.go", patterns: ["order string", "Order:            order"] },
  { feature: "任务320 面板日期排序切换（控件+透传）", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["collab-inbox-panel__ordertoggle", "collabInbox.order.${name}", "showDismissed, order)"] },
  { feature: "任务320 面板日期排序切换三语 locale", file: "desktop/frontend/src/locales/zh.ts", patterns: ["collabInbox.order.desc", "collabInbox.order.asc"] },
  // 任务 320 遗留 #1（320n）：图标行收件箱未读徽标——只读计数 Wails 方法
  // （applyRetention=false，徽标路径绝不 prune）+ 面板开合刷新 hook + 按钮接线。
  { feature: "任务320n 未读徽标只读计数 Wails 方法", file: "desktop/collab_inbox_app.go", patterns: ["func (a *App) CountUnreadCollabMail(", "Unread: true"] },
  { feature: "任务320n 未读徽标 hook 与绑定", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["useCollabInboxUnreadCount", "CountUnreadCollabMail"] },
  { feature: "任务320n 图标行按钮徽标接线", file: "desktop/frontend/src/App.tsx", patterns: ["useCollabInboxUnreadCount", "sidebar__utility-badge"] },
  // 任务 349：群聊通道——channel 实体（SQLite+md 导出）+ 发布订阅展开单发
  // （复用 309 MailStore，铁律 8 无第二投递通道）+ per-recipient delivered/
  // read + 429 治理（错峰/followup/小时上限）+ 取消消息（20261002 增量：
  // 发送方墓碑 + 拦 queued + 在途 drain 投递前重查；已投递副本不追回）。
  // 锁实体层、四工具与注册。
  { feature: "任务349 channel 实体+展开单发+429 治理", file: "internal/collabchannel/collabchannel.go", patterns: ["DrainFanout", "ErrHourlyCap", "ExportMarkdown", "s.mail.Deliver("] },
  { feature: "任务349 取消消息（墓碑+拦 queued+在途重查）", file: "internal/collabchannel/collabchannel.go", patterns: ["func (s *Store) Cancel(", "ErrNotSender", "state != \"queued\"", "cancelled_at"] },
  { feature: "任务349 四工具（查看/获取/发送/取消）", file: "internal/agent/channel_tools.go", patterns: ["channel_list", "channel_read", "channel_send", "channel_cancel", "channelSpawn"] },
  { feature: "任务349 四工具 boot 注册", file: "internal/boot/boot.go", patterns: ["NewChannelSendTool(collab)", "NewChannelReadTool(collab)", "NewChannelCancelTool(collab)"] },
  // 任务 349 挂账 note①（20261003 增量，inbox 群标识）：fan-out 落箱信带
  // 群来源戳（channel 名，投递时点快照），收件箱索引透出 Channel 条目字段，
  // 面板以 #名 chip 标注列表行与对话链条目——群消息不再与点对点信无法区分
  // （chf_ 前缀混显挂账收口）。戳只做来源呈现，不动 320 五桶分类（那是
  // Kind 戳 note② 的职责，由 409view 分支承载，合并时两条 Deliver 字段取并集）。
  { feature: "任务349n1 fan-out 群来源戳（传输字段+投递打戳）", file: "internal/sessioncollab/sessioncollab.go", patterns: ["Channel string"] },
  { feature: "任务349n1 DrainFanout 投递带频道名", file: "internal/collabchannel/collabchannel.go", patterns: ["JOIN channels c ON c.id = m.channel_id", "Channel: j.channel"] },
  { feature: "任务349n1 收件箱条目群标识（索引+面板 chip）", file: "internal/collabinbox/collabinbox.go", patterns: ["Channel string", "Channel:      m.Channel"] },
  { feature: "任务349n1 面板群标识 chip（列表行+对话链）", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["collab-inbox-panel__channel", "#{entry.channel}"] },
  // 任务 448（384 组件层收尾 + 445 调研借鉴 B1/B3）：更早历史请求的闸收敛为
  // 「hasOlder + loading 两态」，四个入口共用 historyOlderGates 一份判定；触发
  // 半径改两视口预取。锚点锁共享谓词本体 + 四个调用点各自的接线——任何一个
  // 入口被改回自带 `running`/`olderHistoryError` 闸，384 的 controller 层解锁就
  // 又被组件层挡死（445 调研 §2.3 的同形复发）。
  { feature: "任务448 更早历史闸单一判定（共享谓词+预取半径）", file: "desktop/frontend/src/lib/historyOlderGates.ts", patterns: ["export function canRequestOlderHistory", "export function olderHistoryTriggerPx", "state.hasOlderHistory !== false"] },
  { feature: "任务448 滚动/按钮/自动填充闸接线", file: "desktop/frontend/src/components/Transcript.tsx", patterns: ["canRequestOlderHistory({ hasOlderHistory, loadingOlderHistory })", "olderHistoryTriggerPx(element.clientHeight)"] },
  { feature: "任务448 加载更早按钮可见性接线", file: "desktop/frontend/src/components/TranscriptViewport.tsx", patterns: ["canRequestOlderHistory({ hasOlderHistory: projection.hasOlderHistory, loadingOlderHistory })"] },
  { feature: "任务448 问题跳转闸接线", file: "desktop/frontend/src/lib/useTranscriptHistoryNavigation.ts", patterns: ["canRequestOlderHistory({ loadingOlderHistory })"] },
  { feature: "任务448 wheel/key 触发共用预取半径", file: "desktop/frontend/src/lib/useTranscriptKernel.ts", patterns: ["olderHistoryTriggerPx(element.clientHeight)"] },
  // 任务448收尾（445 调研 §1.5-2）：组件层闸拒绝留痕。此前组件层拒绝零日志，
  // 装机取证只能靠 history.older-request 缺席反推；现在 explain 回答"为什么拒"、
  // 发射器转换式去重（同因连续拒绝只记首条），接入 controller 同域 history-paging。
  { feature: "任务448收尾 闸拒绝留痕（explain+转换式发射器）", file: "desktop/frontend/src/lib/historyOlderGates.ts", patterns: ["export function explainOlderHistoryGate", "export function createOlderHistoryGateLogger"] },
  { feature: "任务448收尾 requestOlder 拒绝分支接线 history-paging", file: "desktop/frontend/src/components/Transcript.tsx", patterns: ["reportOlderGateBlock.current?.(", "explainOlderHistoryGate({ hasOlderHistory, loadingOlderHistory })", 'reportFrontendLog("history-paging"'] },
  // 任务445修复：identity-retry 绕过 loading 闸。切模型 snapshot bump revision →
  // 在途更早页时代错配 → retry 重入时外层仍持有 historyOlderLoading，旧闸把
  // 重试短路成静默丢弃（"older page abandoned"，用户需再滚一次）。锚点锁
  // isRetry 绕行条件 + retry 发射点仍在（缺一即红）。
  { feature: "任务445 identity-retry 绕过 loading 闸（isRetry 短路修复）", file: "desktop/frontend/src/lib/useController.ts", patterns: ["if (!isRetry && state?.historyOlderLoading) {", "if (sameTranscript && !isRetry) return await loadOlder(targetTabId, targetTurn, trigger, true);"] },
  // 任务441：排队引导消息六点手柄拖拽排序，替换 266-A 的上移/下移按钮。
  // 锚点锁两半：手柄是唯一拖源（卡片本体不再 draggable）+ 落点仍走既有
  // onMove 持久化；CSS 单列一条（merge 丢手柄样式=拖拽入口不可见）。
  { feature: "任务441 排队引导拖拽手柄（接线）", file: "desktop/frontend/src/components/ComposerGuidanceShelf.tsx", patterns: ["GripVertical", "draggable", "onDragStart", "onMove("] },
  { feature: "任务441 拖拽手柄 CSS", file: "desktop/frontend/src/styles.css", patterns: [".composer-guidance-item__handle {", ".composer-guidance-item__handle--dragging"] },
  // 任务442：上下文容量+额度弹窗（composer 指示器浮层）。锚点锁四层：
  // 纯模型（分段归一/provider 门/更多事件）→ 浮层渲染（分段条/额度卡/更多）
  // → Go 构成访问器与面板接线 → 浮层 CSS（auto-merge 静默丢块高发区）。
  // 丢任何一层：分段条消失或额度卡对非 opencode-go provider 误显示。
  { feature: "任务442 浮层数据模型（分段归一+provider 门+更多事件）", file: "desktop/frontend/src/lib/contextGaugePopup.ts", patterns: ["compositionSegments", "normalizeShares", "isOpencodeGoProvider", "OPEN_CONTEXT_OVERVIEW_EVENT"] },
  { feature: "任务442 构成分段条+额度卡浮层（渲染）", file: "desktop/frontend/src/components/ContextWindowRing.tsx", patterns: ["context-composition__bar", "context-quota__card", "context-ring-popover__more", "GetOpenCodeGoUsage"] },
  { feature: "任务442 Go 构成访问器", file: "internal/agent/context_composition.go", patterns: ["func (a *Agent) ContextComposition", "computeContextComposition", "skillToolNames"] },
  { feature: "任务442 ContextPanel 构成/ProviderName 接线", file: "desktop/tabs.go", patterns: ["Composition *ContextCompositionInfo", "ProviderName string", "ctrl.ContextComposition()"] },
  { feature: "任务442 浮层 CSS（分段条+额度卡+更多）", file: "desktop/frontend/src/styles.css", patterns: [".context-composition__bar", ".context-quota__card", ".context-ring-popover__more"] },
  // 任务443：用量分析「推理」计数口径（267 vs 700K 疑似漏统计）。根因=面板 Token 构成
  // 是最近一轮口径，会话累计推理从未暴露。锚点锁三层：
  // Go 累计字段 → 前端口径选择器 → 三语文案/CSS。丢任何一层都会让
  // 「推理 267」重新变成无解释的裸数字（或把数据源缺失误显示成 0 推理）。
  { feature: "任务443 ContextPanel 会话累计推理字段", file: "desktop/tabs.go", patterns: ["SessionReasoningTokens", "info.SessionReasoningTokens = usage.ReasoningTokens"] },
  { feature: "任务443 推理口径标注选择器", file: "desktop/frontend/src/components/ContextPanel.tsx", patterns: ["reasoningScopeNote", "context.typeNoteSessionReasoning", "context.typeNoteReasoningMissing", "context.typeScopeTurn"] },
  { feature: "任务443 口径标注文案（三语）", file: "desktop/frontend/src/locales/zh.ts", patterns: ["context.typeScopeTurn", "context.typeNoteSessionReasoning", "context.typeNoteReasoningMissing"] },
  { feature: "任务443 口径标注 CSS", file: "desktop/frontend/src/styles.css", patterns: [".context-panel__type-note", ".context-panel__type-note:empty"] },
  // ── S1 底座常驻子进程（设计 2026-09-30，§10 S1a 切片）──────────────────
  // 纯增量骨架：协议 v1 + 双实现（inline/remote）+ base serve 子命令 + 开关。
  // 任何一道被 merge 顶掉，后续 S1b/S1c 切片都会骑在断墙上施工。
  { feature: "S1a base 协议 v1 定义", file: "internal/baseproc/protocol.go", patterns: ["ProtocolVersion = 1", "\"base.hello\"", "\"base.toolCall\"", "\"base.dying\"", "CodeVersionMismatch"] },
  { feature: "S1a base 双实现骨架与 R1 回退", file: "internal/baseproc/manager.go", patterns: ["boot: base fallback", "boot: base remote", "func Start(", "subprocessDial", "RunStdioServer"] },
  { feature: "S1a base serve 子命令接线", file: "internal/cli/base.go", patterns: ["base serve", "--stdio", "RunStdioServer"] },
  { feature: "S1a experimental_base_process 开关", file: "internal/config/config.go", patterns: ["experimental_base_process"] },
  // 任务 325：autopilot 只能在 yolo 审批模式下开启（用户 2026-09-25 裁决）。
  // 锁三处——闸门本体、四个开启入口的接线、前端按码本地化。merge 丢掉接线
  // 会重新出现「autopilot 挂着但审批是 ask/auto」的无人值守中间态。
  { feature: "任务325 autopilot yolo 闸门（判定+反向联动+通知码）", file: "desktop/autopilot_gate.go", patterns: ["func autopilotGateAllowed", "func gateRestoredAutopilotDefaults", "func closeAutopilotForOffYolo", "\"autopilot_requires_yolo\"", "\"autopilot_closed_off_yolo\""] },
  { feature: "任务325 开启入口接线（恢复/新标签/选择器/审批切换；477/544 起为六元组含 ask 超时对+自动续跑开关；465 起选择器档位以 effectiveApproval 自动满足 yolo 前置（assumed_yolo 记录决策），门仍在此落点）", file: "desktop/app.go", patterns: ["gateRestoredAutopilotDefaults(on, maxRuntime, grace, askEnabled, askWait, askAutoContinue, tab.toolApprovalMode)", "gateRestoredAutopilotDefaults(autopilot, maxRuntime, approvalGrace, askEnabled, askWait, askAutoContinue, toolApprovalMode)", "gateRestoredAutopilotDefaults(prefOn, prefRuntime, prefGrace, prefAskEnabled, prefAskWait, prefAskAutoContinue, approvalMode)", "closeAutopilotForOffYolo(tab, mode)"] },
  // 任务 544：ask 答复后自动续跑（实验子选项，默认关）。锁四处——控制层触发
  // 与排除集、每回合标记、boot 透传、desktop 六元组接线与设置面。merge 丢掉
  // 任何一环都会退回「答复后停等用户连发两次继续」的现场形态。
  { feature: "任务544 ask 答复后自动续跑（控制层触发+排除集+一次性标记）", file: "internal/control/ask_auto_continue.go", patterns: ["func (o *turnOrchestrator) maybeAskAutoContinueTurn", "askAutoContinueNoticeCode = \"ask_auto_continue\"", "consumeTurnAskAnswered", "ErrAutopilotAskUnanswered", "RecoveryPauseError", "FinalReadinessError"] },
  { feature: "任务544 三处答复置位点（人工/477 拒绝/低风险自答）", file: "internal/control/controller.go", patterns: ["c.markTurnAskAnswered(askDecisionSummary(questions, answers))", "c.markTurnAskAnswered(askDecisionSummary(pending.questions, answers))", "AutopilotAskAutoContinue bool"] },
  { feature: "任务544 回合起点清标记（标记恰好覆盖一个回合）", file: "internal/control/turn_orchestrator.go", patterns: ["c.clearTurnAskAnswered()", "maybeAskAutoContinueTurn(ctx, err)"] },
  { feature: "任务544 desktop 六元组接线（默认快照+闸门+反向联动）", file: "desktop/autopilot_gate.go", patterns: ["askAutoContinue bool, approvalMode string", "tab.autopilotAskAutoContinue = false"] },
  { feature: "任务544 设置面+三语（开关读写）", file: "desktop/settings_app.go", patterns: ["ExperimentalAutopilotAskAutoContinue bool `json:\"experimentalAutopilotAskAutoContinue\"`", "func (a *App) SetDesktopAutopilotAskAutoContinue(enabled bool) error"] },
  { feature: "任务325 设置默认值前置校验（拒绝非 yolo）", file: "desktop/settings_app.go", patterns: ["autopilot requires the yolo approval mode"] },
  { feature: "任务325 拒绝/关闭提示按码本地化", file: "desktop/frontend/src/lib/controllerNotices.ts", patterns: ["autopilot_requires_yolo: \"notice.autopilotRequiresYolo\"", "autopilot_closed_off_yolo: \"notice.autopilotClosedOffYolo\""] },
  // 任务 327：自动化任务运行次数上限 maxRuns（单次 = N 的特例、跑满自动禁用）。
  // 锁计费点/自禁用、合并与「重开=重置计数」收口、schema 前向保护——丢掉合并
  // 那条，引擎刚禁用的任务下一 tick 会被磁盘上的 enabled=true 复活。
  { feature: "任务327 maxRuns 引擎（触发即计数+跑满自禁用）", file: "desktop/heartbeat.go", patterns: ["func (e *HeartbeatEngine) spendRunBudget", "func heartbeatBudgetExhausted", "MaxRuns int", "RunsUsed int"] },
  { feature: "任务327 maxRuns 持久化合并与重开重置", file: "desktop/heartbeat_store.go", patterns: ["update.RunsUsed >= tasks[i].MaxRuns && !update.Enabled", "diskTask.RunsUsed > out[i].RunsUsed", "重开 = 重置计数"] },
  { feature: "任务327 maxRuns schema 前向保护", file: "desktop/heartbeat.go", patterns: ["const heartbeatSchemaVersion = 4"] },
  { feature: "任务327 maxRuns agent 工具面", file: "internal/tool/builtin/heartbeat_tasks.go", patterns: ["\"maxRuns\":{\"type\":\"integer\"", "\"runsUsed\": true", "maxRuns: integer budget"] },
  { feature: "任务327 maxRuns 编辑器（无限/单次/自定义 N）", file: "desktop/frontend/src/custom/features/heartbeat/HeartbeatTaskEditor.tsx", patterns: ["heartbeat.maxRunsUnlimited", "heartbeat.maxRunsOnce", "data-testid=\"heartbeat-max-runs-input\""] },
  { feature: "任务327 maxRuns 列表 k/N 与终态徽标", file: "desktop/frontend/src/custom/features/heartbeat/HeartbeatPanel.tsx", patterns: ["heartbeat-maxruns-badge", "heartbeat-maxruns-badge--done", "heartbeat.maxRunsCompleted"] },
  { feature: "任务327 maxRuns 终态徽标 CSS", file: "desktop/frontend/src/custom/features/heartbeat/heartbeat.css", patterns: [".heartbeat-maxruns-badge", ".heartbeat-maxruns-badge--done"] },
  // 任务 326：autopilot 启动自动 ensure 守护定时任务（幂等单例 + 对账清理 + 自关闭 + 面板间隔）。
  // 锁 ensure/对账/自关闭三件、两条 App 接线、权限隔离（守护不改会话审批——丢了它，
  // 守护每次运行都会把 owner 的审批改掉并触发 325 反向联动把 autopilot 关了）。
  { feature: "任务326 守护任务 ensure/对账/自关闭", file: "desktop/autopilot_guard.go", patterns: ["func (e *HeartbeatEngine) EnsureAutopilotGuard", "func (e *HeartbeatEngine) ReconcileAutopilotGuards", "func (e *HeartbeatEngine) evaluateAutopilotGuardClose", "const autopilotGuardIDPrefix"] },
  { feature: "任务326 守护接线（边沿 ensure + 对称清理）", file: "desktop/app.go", patterns: ["a.ensureAutopilotGuard(guardOwner)", "a.clearAutopilotGuard(guardTopic)", "a.clearAutopilotGuard(guardOwner.TopicID)"] },
  { feature: "任务326 守护权限隔离（不改会话审批）", file: "desktop/heartbeat.go", patterns: ["if !isAutopilotGuardTask(t) {", "evaluateAutopilotGuardClose(t, guardQuiet)", "reconcileAutopilotGuardsCheap"] },
  { feature: "任务326 守护自关闭终态不被复活", file: "desktop/heartbeat_store.go", patterns: ["if isAutopilotGuardTask(tasks[i]) && !update.Enabled {", "tasks[i].IdleStreak = update.IdleStreak"] },
  { feature: "任务326 守护面板间隔与自关闭档位（setter）", file: "desktop/settings_app.go", patterns: ["SetDesktopAutopilotGuardInterval", "SetDesktopAutopilotGuardQuiescent"] },
  { feature: "任务326 守护面板档位进 render 表", file: "internal/config/render.go", patterns: ["autopilot_guard_interval", "autopilot_guard_quiescent"] },
  { feature: "任务326 守护面板档位 UI", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["settings.autopilotGuardInterval", "settings.autopilotGuardQuiescent.destroy"] },

  // 任务 547：守护自动创建改为自动驾驶实验特性的子选项（默认不勾选）。
  // 关闭语义 = 不创建（非"创建后禁用"），旧任务按新设置收敛；漏 render 表行
  // 会让开关保存后被静默丢弃（81/123 教训），四处链路都要有锚。
  { feature: "任务547 守护创建门控与旧任务收敛（默认关=不创建）", file: "desktop/autopilot_guard.go", patterns: ["autopilotGuardAutocreate() {", "guard auto-creation sub-option is off (task 547)"] },
  { feature: "任务547 子选项进 render 表", file: "internal/config/render.go", patterns: ["experimental_autopilot_guard_autocreate"] },
  { feature: "任务547 设置视图与 setter（翻转即对账收敛）", file: "desktop/settings_app.go", patterns: ["SetDesktopAutopilotGuardAutocreate", "experimentalAutopilotGuardAutocreate"] },
  { feature: "任务547 面板 UI（子选项开关+档位随开关禁用）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["settings.autopilotGuardAutocreate", "SetDesktopAutopilotGuardAutocreate"] },

  // ── S1 底座常驻子进程（设计 2026-09-30，§10 S1b 工具面迁移）────────────
  // 工具目录/工具调用经 base 通道 + 进度流 + sink 适配器 + boot 消费点；
  // 开关默认 off，任一道被顶掉都会让「开关关=现行为」的对照失去支点。
  { feature: "S1b base ToolSurface 与 RegistrySurface", file: "internal/baseproc/surface.go", patterns: ["type ToolSurface interface", "RegistrySurface", "ScopeProvider", "ErrUnknownScope"] },
  { feature: "S1b serve 端工具面挂载与 toolCall 进度流", file: "internal/baseproc/serve_toolface.go", patterns: ["AttachToolSurface", "handleToolCall", "validateToolCallParams", "NotifyToolProgress"] },
  { feature: "S1b 客户端进度路由（call_id 分发）", file: "internal/baseproc/client.go", patterns: ["dispatchNotify", "trackProgress", "untrackProgress", "ProgressFrom"] },
  { feature: "S1b sink 适配器（ToolProgress 事件形状）", file: "internal/baseproc/sink.go", patterns: ["WithToolProgress", "ToolEventSink", "event.ToolProgress"] },
  { feature: "S1b 控制器工具目录闸", file: "internal/control/base_gate.go", patterns: ["baseCatalogEntries", "ModeRemote", "ErrNotWired"] },
  { feature: "S1b agent 工具调用闸", file: "internal/agent/base_toolcall.go", patterns: ["baseToolCall", "ModeRemote", "ErrNotWired"] },
  { feature: "S1b boot 消费点（Start 接线）", file: "internal/boot/base_client.go", patterns: ["startBaseClient", "ExperimentalBaseProcess", "RegistrySurface"] },
  { feature: "S1b boot 消费点（注入与拆除）", file: "internal/boot/boot.go", patterns: ["startBaseClient(ctx, cfg, reg)", "_ = baseClient.Close()"] },
  // 任务 478：S1 消费面接线的桌面侧——D4 spawn 契约的另一半。baseproc 默认
  // spawn「当前可执行文件 base serve --stdio」，桌面 exe 不认这段 argv = 底座
  // 100% 空转（握手失败循环、base.log 0 字节、每 5min degraded 探测把窗口拉
  // 到前台）。被 merge 顶掉就回退到那个状态，且无编译错误。
  { feature: "任务478 桌面 exe 分发 base serve（拦截+分发体）", file: "desktop/base_serve.go", patterns: ["maybeRunBaseServe", "classifyBaseServeArgs", "baseproc.RunStdioServer"] },
  { feature: "任务478 main 抢位次序（先于 installDesktopLogging，保 fd2=base.log）", file: "desktop/main.go", patterns: ["maybeRunBaseServe(os.Args[1:])"] },
  { feature: "任务478 base.log 存活行（就绪/退出，F2 落地）", file: "internal/baseproc/baselog.go", patterns: ["base serve: ready", "base serve: exit"] },
  { feature: "任务478 验收探针（真 Manager 对桌面 exe）", file: "cmd/probe478/main.go", patterns: ["remote_ready", "base serve", "RestartBaseDelay"] },
  // 任务 399：会话内 Ctrl+F 搜索（对照上游 #11230→#11236）。四件套锁：
  // 行级检索纯逻辑（数据面搜索，虚拟滚动下 DOM 搜索会漏未挂载行）、
  // 搜索条 UI、行高亮 context 接线、快捷键注册 + 代码块域隔离——
  // 丢任何一环 = Ctrl+F 回归为 webview 原生搜索或折叠内容不可搜。
  { feature: "任务399 行级检索逻辑", file: "desktop/frontend/src/lib/transcriptFind.ts", patterns: ["buildTranscriptFindIndex", "searchTranscriptFind", "shouldIgnoreFindShortcutTarget", "TRANSCRIPT_FIND_HIT_CAP"] },
  { feature: "任务399 搜索条组件", file: "desktop/frontend/src/components/TranscriptFindBar.tsx", patterns: ["TranscriptFindBar", "focusSignal", "onPrev"] },
  { feature: "任务399 行高亮 context", file: "desktop/frontend/src/components/TranscriptBlockView.tsx", patterns: ["useTranscriptFindHighlight", "transcript__row--find-active", "data-find-active"] },
  { feature: "任务399 Ctrl+F 快捷键注册（代码块域隔离）", file: "desktop/frontend/src/App.tsx", patterns: ["transcript.find", "shouldIgnoreFindShortcutTarget", "setTranscriptFindPulse"] },
  // 任务 398（上游 #11006 → #10893）：plan 执行消息 id 复用防护 + 损坏会话保持可打开。
  // 锚点锁四处：写端 durable id 集拒绝（唯一权威，externalHistory 时投影为空）、
  // 读端投影 keep-first（重复 complete 不再 damagedPayload 拒开）、
  // history/search 索引 keep-first（重复不再中断索引重建）、
  // checkpoint 携带 MessageIDs + 版本 3（旧 checkpoint 无此字段必须重建，
  // 否则写端拒绝集被播成空 = 盲区复活）。丢任何一处，重复 id 日志重新不可打开
  // 或写端重新放行复用。
  { feature: "任务398 写端 durable id 集拒绝", file: "internal/session/session.go", patterns: ["identities := s.messageIDs.changeFor(commit)", "identities.duplicate != \"\"", "s.messageIDs.apply(identities)"] },
  { feature: "任务398 身份集模型", file: "internal/session/message_identities.go", patterns: ["type messageIdentities map[string]struct{}", "func (ids messageIdentities) changeFor", "duplicateMessageError"] },
  { feature: "任务398 读端投影 keep-first", file: "internal/session/projection.go", patterns: ["a repeat already on disk keeps the id's", "return nil"] },
  { feature: "任务398 历史/搜索索引 keep-first", file: "internal/session/history_index.go", patterns: ["repeated message/complete keeps the id's first record", "state.positions[strings.TrimSpace(body.Message.ID)]"] },
  { feature: "任务398 checkpoint 携带 id 集+版本3", file: "internal/session/recovery_store.go", patterns: ["MessageIDs []string", "recoveryProjectionVersion = 3", "state.messageIDs.admit(commit)"] },

  // ── S1 底座常驻子进程（设计 2026-09-30，§10 S1c 生命周期收口）──────────
  // health/退避重启/degraded 状态机 + 托管视图；任一道被顶掉，矩阵 C6 的
  // 「子进程死则全体 inline」就没有落点，开关 on 会退回 S1a 的 one-shot。
  { feature: "S1c base 生命周期状态机（D5）", file: "internal/baseproc/lifecycle.go", patterns: ["type Manager struct", "degraded_inline", "boot: base dying", "func (m *Manager) markDead("] },
  { feature: "S1c 心跳检测与退避重启", file: "internal/baseproc/lifecycle.go", patterns: ["REASONIX_BASE_HEALTH_INTERVAL", "func (m *Manager) healthTick(", "func (m *Manager) restartTick(", "func (m *Manager) backoffLocked("] },
  { feature: "S1c 托管视图（Mode 跟随状态）", file: "internal/baseproc/lifecycle.go", patterns: ["type ManagedClient struct", "func (c *ManagedClient) Mode()", "errClientClosed"] },
  { feature: "S1c Start 托管与生命周期阈值选项", file: "internal/baseproc/manager.go", patterns: ["NewManager(ctx, opts).Acquire(opts)", "RestartMaxFailures", "gracefulCloseWait = 5 * time.Second"] },
  { feature: "S1c 子进程日志面（F2 logs/base.log）", file: "internal/baseproc/baselog.go", patterns: ["baseLogFileName", "defaultBaseLogPath", "func openBaseLog(", "REASONIX_BASE_LOG"] },
  { feature: "S1c spawn stderr 接线（F1 环境显式传递）", file: "internal/baseproc/manager.go", patterns: ["resolveStderr(opts)", "withBaseLogEnv(env, stderr.path)", "cmd.Stderr = stderr.w"] },
  { feature: "S1c shutdown 前置与关闭可中止重启", file: "internal/baseproc/lifecycle.go", patterns: ["alreadySent := m.shutdownSent", "m.baseCancel()", "abort a restart attempt already in flight"] },

  // ── S1 底座常驻子进程（设计 2026-09-30，§10 S1c 会话租约）──────────────
  // base.attach 携 workspace root + client_pid owner key；C3 隔离、C4 孤儿
  // 回收。掉一道，底座就无从知道哪些 workspace 在用（注册表托管的前置）。
  { feature: "S1c 会话租约表与 attach/detach 处理器", file: "internal/baseproc/lease.go", patterns: ["type leaseTable struct", "func (t *leaseTable) reclaimDead(", "AttachSessionAccounting", "func (s *Server) handleAttach("] },
  { feature: "S1c 孤儿回收挂在 base.hello 上", file: "internal/baseproc/serve.go", patterns: ["leases *leaseTable", "onHello := s.onHello", "onHello(p.ClientPID)"] },
  { feature: "S1c 孤儿判定 pidAlive（双平台）", file: "internal/baseproc/pidalive_windows.go", patterns: ["func pidAlive(pid int) bool", "os.FindProcess(pid)"] },

  // ── S1 底座常驻子进程（设计 2026-09-30，§10 S1c 单例收敛）────────────────
  // S1b 审计 note 的遗留：manager one-shot = N boot 各起一个子进程。掉一道，
  // 「常驻底座」就退回「每 tab 一个底座进程」，S1 的内存/启动收益全部落空。
  { feature: "S1c 每视图自带 inline 回退面", file: "internal/baseproc/lifecycle.go", patterns: ["func (m *Manager) Acquire(opts Options)", "inline InlineBaseClient", "One critical section decides remote vs inline"] },
  { feature: "S1c boot 常驻单例（N boot → 1 子进程）", file: "internal/boot/base_client.go", patterns: ["func sharedBaseClient(", "baseproc.NewManager(ctx, opts)", "sharedBase = m"] },

  // ── 任务 450 重启宽限窗（方案 A：停心跳 → 宽限 10s → 到期 Cancel → 谁断谁续）────
  // 旧守卫是「任一他 tab 忙即拒」；宽限窗是重启族三入口（发布/回滚/普通重启）
  // 的唯一出口。任何一道被 merge 顶掉，restart_update 退回永拒，或中断他
  // 会话却不补偿续跑（450 变 435 放大器）。
  { feature: "450 宽限窗主体（停心跳/轮询/到期取消/有界收束）", file: "desktop/restart_update.go", patterns: ["func (a *App) clearRestartPath", "restartGraceWait = 10 * time.Second", "restartStopHeartbeat(a)", "restartForcedMarker = \"超时强制\""] },
  { feature: "450 不替用户否决（pending prompt 永不 Cancel）", file: "desktop/restart_update.go", patterns: ["RuntimeStatus().PendingPrompt", "forcedPrompt"] },
  { feature: "450 三入口同窗（发布+回滚+普通重启）", file: "desktop/restart_update.go", patterns: ["report := a.clearRestartPath(\"\")", "forced := a.clearRestartPath(callerSession)", "restartStartLauncher", "restartQuit"] },
  { feature: "450 回滚入口共窗", file: "desktop/version_switch.go", patterns: ["forced := a.clearRestartPath(callerSession)", "forced.forcedNote()"] },
  { feature: "450 工具面结果显形强制说明", file: "desktop/autonomous_update.go", patterns: ["forcedNote"] },
  { feature: "450 谁断谁续（被打断会话无条件入 254 名册）", file: "desktop/autonomous_update_resume.go", patterns: ["func (a *App) stageInterruptedByRestart", "whoever we interrupted, we resume"] },
  { feature: "450n2 名册口径统一（入册/消费 sessionRuntimeKey 归一化）", file: "desktop/autonomous_update_resume.go", patterns: ["sessionRuntimeKey(state.Sessions[i].Path) == key", "sessionRuntimeKey(entry.Path) == sessionKey"] },
  { feature: "450n2 窄窗测试（两口径拼写不等、归一化后命中）", file: "desktop/restart_resume_normalize_test.go", patterns: ["TestResumeRosterMatchesAcrossPathForms", "TestStageInterruptedByRestartDedupesAcrossPathForms"] },

  // ── 任务 435 断后恢复链（名册会话续跑不落 fence + 未入册防静默丢）────
  // 450 管「重启之前」能否出发，435 管「重启之后」恢复质量。缺了 settle，
  // 被续跑的会话照样弹「中断的工具需要核实」等人；缺了未入册显形，拨盘
  // off/非 autopilot 调用者被重启打断后无声消失。任何一道被 merge 顶掉，
  // 「谁断谁续」就断在最后一公里。
  { feature: "435 agent 层结算（interrupted_by_restart 主方结算）", file: "internal/agent/tool_recovery_actions.go", patterns: ["restartResumeResolution = \"interrupted_by_restart\"", "func (a *Agent) ResolveInterruptedByRestart"] },
  { feature: "435 control 层委托（控制器结算入口，nil 安全）", file: "internal/control/tool_recovery.go", patterns: ["func (c *Controller) SettleRestartInterruptedEffects", "ResolveInterruptedByRestart()"] },
  { feature: "435 名册消费点接线（settle 先于续跑提交，面板不亮）", file: "desktop/autonomous_update_resume.go", patterns: ["type restartFenceSettler interface", "func (a *App) settleRestartFenceForTab", "a.settleRestartFenceForTab(tab)", "restartResumeSubmit func(a *App, tabID, prompt string) error"] },
  { feature: "435 未入册显形（1545 防静默丢标记）", file: "desktop/restart_update.go", patterns: ["restartUnstagedMarker = \"未入册\"", "report.unstaged = append(report.unstaged, sp)", "interrupted but NOT staged for auto-resume"] },
  { feature: "435 调用者未入册工具面显形", file: "desktop/autonomous_update.go", patterns: ["callerStaged := a.stageAutonomousUpdateResume(callerSession)", "NOT staged for auto-resume"] },

  // ── S1 开关 UI 入口（任务 450 并入小件：experimental_base_process 实验室控件）──
  // 开关注册（S1a）已进 render 表但没有 UI 面，用户无法打开开关；四处接线
  // 缺一，开关就「看得见配置改不了」或「改了读不回」。
  { feature: "S1 开关实验室入口（rail 行+详情卡+重启横幅）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["| \"baseProcess\"", "{ id: \"baseProcess\", group: \"misc\"", "selected === \"baseProcess\" && (", "app.SetExperimentalBaseProcess(on)"] },
  { feature: "S1 开关桥接线（接口声明+mock 桩）", file: "desktop/frontend/src/lib/bridge.ts", patterns: ["SetExperimentalBaseProcess(enabled: boolean): Promise<void>;", "async SetExperimentalBaseProcess() {}"] },
  { feature: "S1 开关 Go 侧读写链（setter+视图字段）", file: "desktop/settings_app.go", patterns: ["ExperimentalBaseProcess bool `json:\"experimentalBaseProcess\"`", "view.ExperimentalBaseProcess = cfg.Agent.ExperimentalBaseProcess", "ExperimentalBaseProcess:             cfg.Agent.ExperimentalBaseProcess"] },
  // ── 任务 451：history 慢分相打点 + planner/尾读缓存（2026-10-02）──────
  // 打点件是验收基建（phases 一行可 grep 重建）；A 缓存三道闸（校验命中 /
  // 写侧失效 / 单飞+上限）与 C 单飞都要在 merge 后存活，否则 planner-turns
  // 相位退回每请求整读 12MB。
  { feature: "任务451 history 切片分相打点（historySliceTrace）", file: "desktop/history_slice_timing.go", patterns: ["historySliceSlowLogMs", "desktop: history slice timing", "func (t *historySliceTrace) run("] },
  { feature: "任务451 A：planner 侧车缓存（mtime+size 校验+单飞+上限）", file: "desktop/sessions_planner_display_cache.go", patterns: ["loadCachedSessionPlannerDisplays", "plannerDisplayCacheMaxEntries", "plannerDisplayCacheInflight"] },
  { feature: "任务451 A：写侧失效（save/remove 双出口清条目）", file: "desktop/sessions.go", patterns: ["invalidateSessionPlannerDisplayCache(dir)"] },
  { feature: "任务451 C：尾读缓存单飞（inflight 共享冷读）", file: "desktop/history_time_overlay.go", patterns: ["historyTimeOverlayInflight", "delete(historyTimeOverlayCache.inflight, cacheKey)"] },

  // 任务456（20261003 P1 用户直令）：desktop 重启恢复链自死锁——自家 UI 与自家后台
  // 运行时互不认识（租约同进程持有但 attach 被拒），接管只扫 serve 报「no resident
  // serve」死局。三方向：①恢复先核销残留记录再拉起运行时；②接管扫本机租约可收编；
  // ③报错带 pid 存活检测与清理指引。合并丢了任一环，重启后同款死锁会复发。
  { feature: "456 ① 恢复去重+核销（拉起前清残留）", file: "desktop/session_lease_reconcile.go", patterns: ["func dedupeRestoredTabEntries", "func (a *App) reconcileRestoredSessionKeys", "reconcileRestoredSessionLeaseRecords"] },
  { feature: "456 ① 原语（锁空闲才核销，双源清理）", file: "internal/agent/session_lease.go", patterns: ["func ClearStaleSessionLeaseInfo"] },
  { feature: "456 ① 僵尸豁免接线（reclaim 前释放僵尸持有者）", file: "desktop/app.go", patterns: ["releaseZombieSessionLeaseHoldersForKey(sessionRuntimeKey(path), tab)", "zombieLeaseHolderLocked(candidate, key)"] },
  { feature: "456 ② 接管本地租约回退（非 no resident serve）", file: "desktop/session_takeover.go", patterns: ["adoptLocalLeaseHeldSession(tab, path)", "desktop-local"] },
  { feature: "456 ③ pid 存活指引", file: "desktop/session_lease_reconcile.go", patterns: ["func localLeaseHolderGuidance", "taskkill /PID", "(dead)", "(alive)"] },

  // 任务411（20261001 批十收尾）：快速切换版本治理——出包自动清理 + 面板删除历史版本。
  // 保留规则只实现一次（installlayout.PruneVersionTrees，含 current.json 指向硬跳过），
  // 出包脚本经 tools/prune-versions 调它；合并丢了任一环都会让 versions/ 重新堆积。
  { feature: "任务411 versions 保留规则（保留 N + current 指向硬跳过）", file: "internal/installlayout/prune.go", patterns: ["func PruneVersionTrees", "keep must be >= 0", "name == active"] },
  { feature: "任务411 出包脚本接自动清理（--keep/REASONIX_VERSIONS_KEEP）", file: "scripts/build-local-installer.sh", patterns: ["tools/prune-versions", "--keep", "REASONIX_VERSIONS_KEEP"] },
  { feature: "任务411 面板删除历史版本（Go 绑定拒绝 current/运行中版本）", file: "desktop/version_switch.go", patterns: ["func (a *App) DeleteInstalledVersion", "is the active version", "running from"] },
  { feature: "任务411 面板删除入口+确认交互（TSX）", file: "desktop/frontend/src/components/VersionSwitchDialog.tsx", patterns: ["versionSwitchDeleteConfirm", "onDelete", "btn--danger"] },
  { feature: "任务411 面板删除接线（App.tsx handler）", file: "desktop/frontend/src/App.tsx", patterns: ["handleDeleteVersion", "DeleteInstalledVersion(version)"] },

  // 任务455fix（20261003）：boot canonical 化收敛为 sandbox 有界+缓存单实现。
  // confine.go realPath 与 write_path.go ResolveAbsPath 原是同一逐级
  // EvalSymlinks 逻辑的两份无界实现，allow_write 里一条死网络盘会让每次
  // boot 各吃满一次 SMB 重连预算（实测 ~21s）。共享引擎提供 250ms 有界超时、
  // 30s TTL 缓存（含失败）与并发单飞；审批身份复核走 ResolveAbsPathFresh
  // 旁路缓存。上游 merge 若恢复了任何一份无界双实现，上述保障即失效。
  { feature: "任务455fix 共享有界 canonical 引擎（250ms 超时+30s TTL 缓存含失败+并发单飞）", file: "internal/sandbox/canonical.go", patterns: ["func resolveCanonicalPath", "canonicalFlights", "canonicalCacheStore", "network drive offline?", "canonicalResolveTimeoutBudget = 250 * time.Millisecond"] },
  { feature: "任务455fix boot 两处 canonicalizer 收敛委托 + 审批复核旁路缓存", file: "internal/sandbox/write_path.go", patterns: ["resolveCanonicalPath(path)", "ResolveAbsPathFresh(approved)"] },
  { feature: "任务455fix builtin realPath 委托共享实现（消双实现）", file: "internal/tool/builtin/confine.go", patterns: ["sandbox.ResolveAbsPath(path)"] },

  // 任务460（20261003）：会话打开与 boot 配置路径上残余的无界 walk 收敛到
  // 455fix 有界引擎（internal/sandbox/canonical.go）或按同模式加 250ms 预算。
  // workspacelease 身份解析每个 tab boot 必经；pathidentity.Canonical 是保存
  // 路径键与桌面单实例身份；boot 三点覆盖附加目录授权、root 去重键、工作目录
  // .git 走查。任一处被 merge 回退成无界 EvalSymlinks/Stat，死 NAS 就重新让
  // 会话打开挂满一次 SMB 重连预算（~21s）。
  { feature: "任务460 workspacelease 身份解析接有界引擎 + .git 走查预算回退", file: "internal/workspacelease/lease.go", patterns: ["sandbox.ResolveAbsPath(abs)", "boundedGitWorktreeRoot", "identityResolveBudget"] },
  { feature: "任务460 pathidentity.Canonical 委托共享有界引擎", file: "internal/pathidentity/path.go", patterns: ["sandbox.ResolveAbsPath(key)"] },
  { feature: "任务460 boot 附加目录校验/root 去重键/git-root 走查三项加界", file: "internal/boot/boot.go", patterns: ["boundedStat", "errStatTimeout", "sandbox.ResolveAbsPath(dir)", "sandbox.ResolveAbsPath(path)", "boundedNearestGitRoot"] },

  // ── 任务461 P2-P5（收件箱/工具体验四件，2026-10-03）──────────────────
  // P2：更新重启标记——只有更新驱动的重启才允许 auto-resume 家族开闸；
  // tabs.go 恢复点的门禁调用与标记文件本体都是 fork 特有接线，merge 丢任一
  // 半边都会退化回「任何启动都自动唤醒会话」的 461 原症状。
  { feature: "任务461-P2 更新重启标记（写点+消费门禁）", file: "desktop/update_restart_marker.go", patterns: ["func writeUpdateRestartMarker", "func updateRestartResumeAllowed", "update-restart-marker.json"] },
  { feature: "任务461-P2 恢复点门禁 + 中断工具不重放兜底", file: "desktop/tabs.go", patterns: ["resumeAllowed := updateRestartResumeAllowed()", "tabHasInterruptedToolCall(tab)"] },
  // P3：失败消息一键重发（复用 edit 的 rewind+submit 通道）。
  { feature: "任务461-P3 失败消息重发按钮", file: "desktop/frontend/src/components/Message.tsx", patterns: ["onResend", "msg__resend"] },
  // P4：收件箱 from/to 过滤下拉（选项=会话名，hover=项目›分组›会话名›contact_id）。
  { feature: "任务461-P4 收件箱 from/to 过滤下拉", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["CollabSessionDirectory", "sessionHoverLabel"] },
  // P5：受管路径预授权双栏布局（介绍左/开关右；styles.css 是 merge 静默丢块高发区）。
  { feature: "任务461-P5 受管路径预授权双栏布局 CSS", file: "desktop/frontend/src/styles.css", patterns: [".autopilot-preapprove-subblock .set-seg {\n  justify-self: end;"] },
  // P6：长 turn 中 send 被拒（ErrTurnRunning）自动降级——durable guidance 队列
  // 兜底、不弹 Send failed、去向提示与 turn 状态对齐。merge 丢 reducer 或丢
  // 拦截点都会退化回「失败弹窗 + 消息丢失」的 461-P6 原症状。
  { feature: "任务461-P6 send 被拒自动降级（不弹失败面/去向提示/turn 状态对齐）", file: "desktop/frontend/src/lib/useController.ts", patterns: ["degradeRunningTurnSubmit", "composer.degradedSteer", "composer.degradedQueued"] },
  { feature: "任务461-P6 降级 reducer（气泡不标失败+权威 running 对齐）", file: "desktop/frontend/src/lib/turnSubmissionFailure.ts", patterns: ["reduceSubmitDegraded"] },
  // P7：三级终止交互——L1 优雅/L2 宽限倒计时/L3 强制放弃。丢任一半边都会退化
  // 回「卡死的 turn 永远占着 UI」或「点一下就把工具强杀」的单级世界。
  { feature: "任务461-P7 控制层三级终止状态机（CancelStop 升级链+保底计时）", file: "internal/control/stop_escalation.go", patterns: ["func (c *Controller) CancelStop", "stopAutoGraceAfter", "stopForceGrace"] },
  { feature: "任务461-P7 执行器强制放弃 watchdog（忽略取消的工具记录+隔离）", file: "internal/agent/execute_batch.go", patterns: ["executeOneWithForce", "forcedToolOutput"] },
  { feature: "任务461-P7 强制信号 ctx 通路（独立 force ctx）", file: "internal/agent/run_force.go", patterns: ["WithStopForce", "StopForceDone"] },
  { feature: "任务461-P7 停止按钮三态（强制停止/倒计时/hover 三档）", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["stopButtonView", "composer.stopCountdown", "composer.stopKillHint"] },
  // P9：引导队列注入点扩展——工具间隙把队头 followup 以 steer 语义拉入当前
  // turn（≤1 工具周期被模型看到）。丢任一半边都会退化回「等 turn 结束才投递」。
  { feature: "任务461-P9 工具间隙注入 hook（agent 循环 gap 触发）", file: "internal/agent/run_loop.go", patterns: ["a.toolRoundGap()"] },
  { feature: "任务461-P9 间隙派发（队头 TrySteerInboxItem 同路+合并组随行）", file: "internal/control/inbox_dispatch.go", patterns: ["bindAgentToolRoundGap", "dispatchQueuedAtToolGap", "maybeMergeInboxDispatchGroup(meta)"] },

  // ── 任务 440/447 运行面板（composer 胶囊）──────────────────────────
  // 输入框下实时面板：447 胶囊（形态载体）+ 440 跨 tab 合并/停止/空态。
  // 上游 merge 丢掉任一锚点都会让「看不到什么在跑」回归，逐文件登记。
  { feature: "任务447 胶囊悬浮窗（运行两节+已结束目录+历史查看）", file: "desktop/frontend/src/components/CapsulePanel.tsx", patterns: ["CapsuleIndicator", "groupCapsuleJobs", "formatCapsuleElapsed"] },
  { feature: "任务440 全运行面合并与来源停止（跨 tab+分离会话）", file: "desktop/frontend/src/components/CapsulePanel.tsx", patterns: ["mergeCapsuleWork", "splitCapsuleEntries", "onCancelRuntimeJob(entry.tabId, entry.job.id)"] },
  { feature: "任务440 面板接线（runtimes 过滤 active tab + per-tab 停止）", file: "desktop/frontend/src/App.tsx", patterns: ["capsuleRuntimes={backgroundRuntimes.filter(", "onCapsuleCancelRuntimeJob={cancelRuntimeJob}"] },
  { feature: "任务440 空态（无运行任务明确显示，不静默收缩）", file: "desktop/frontend/src/styles.css", patterns: [".capsule-panel__running-empty", ".capsule-panel__origin"] },
  // 任务462：跨会话消息卡显示双方对话名（contact_id 降为 hover）。解析层丢
  // 了会退回裸 sc_id；降级与 hover 断言丢了会掩盖「id 丢失/空白渲染」回归。
  { feature: "任务462 contact_id→会话名 解析层（TTL 缓存+降级短 id）", file: "desktop/frontend/src/lib/collabContactNames.ts", patterns: ["refreshCollabContactNames", "collabDisplayLabel", "shortContactId"] },
  { feature: "任务462 消息卡 meta 双方会话名（id 进 hover）", file: "desktop/frontend/src/components/Message.tsx", patterns: ["msg.collabRoute", "useCollabContactNames"] },
  { feature: "任务462 收件箱路由/会话链会话名+hover id", file: "desktop/frontend/src/components/CollabInboxPanel.tsx", patterns: ["contactDisplayName", "contactHoverLabel"] },
  { feature: "任务462 测试（有名/降级/hover 保留 id/改名同步）", file: "desktop/frontend/src/__tests__/collab-contact-names.test.tsx", patterns: ["hover keeps the full sender contact_id", "degrades to the truncated id", "the label follows the new title"] },
  // P14 SetActiveTab 移出锁等待（2026-10-04）：切 tab 等锁 p50=28.1s → 锁忙立即切换
  { feature: "P14 锁忙哨兵（errSavePathBusy 零波及接口 + 锁忙立即切换）", file: "internal/control/controller.go", patterns: ["errSavePathBusy"] },
  { feature: "P14 agent save try-lock（持锁贯穿保存临界区）", file: "internal/agent/save.go", patterns: ["SaveSnapshotIfPathFree", "tryLockSessionSavePath", "savePathLockFree"] },
  // X3/X4（wt-zcode-x3，2026-10-04）：中断核实卡显式清除 + autopilot 传递链三断点
  { feature: "X3 显式清除 agent 层（dismiss 结算+回滚规则）", file: "internal/agent/tool_recovery_actions.go", patterns: ["ResolveToolRecoveryDismissed", "dismissed_by_user"] },
  { feature: "X3 guard 拆分 + dismiss 过行门（closed/rotating 分报，running 放行 dismiss）", file: "internal/control/tool_recovery.go", patterns: ["session is closed", "session is switching", 'req.Action != "dismiss"'] },
  // 任务519（wt-519-recovery-card，2026-10-06）撤销 X3 前端半：核实卡降级为
  // 不可交互记录行，dismiss 按钮随交互面整体退役（后端 dismiss 结算语义由上
  // 面两条 X3 锚继续保护，serve API 兼容不破坏）。merge 若把按钮带回来，前端
  // 会重新出现「需要人工核实」的交互卡——519 测试与锚会一起拦。
  { feature: "519 面板降级：无按钮/无 resolve 调用（前端测试钉死）", file: "desktop/frontend/src/__tests__/x3x4-autopilot-view-and-recovery-dismiss.test.ts", patterns: ["panel must render no buttons (task 519)", "panel must not wire any per-call action (task 519)", "must no longer carry the review-interaction key"] },
  { feature: "519 记录行三语中性文案（不再出现「需要核实」标题）", file: "desktop/frontend/src/locales/zh.ts", patterns: ['"toolRecovery.title": "中断的工具调用记录"'] },
  { feature: "X4 断点 A 初始构建补传 autopilot 三元组（477 起四/五元组含 ask 超时对）", file: "desktop/tabs.go", patterns: ["Autopilot:                  tab.autopilot", "AutopilotApprovalGrace:     tab.autopilotApprovalGrace", "AutopilotAskTimeoutEnabled: tab.autopilotAskTimeoutEnabled"] },
  { feature: "X4 断点 B 后端视图携带 autopilot", file: "desktop/tabs.go", patterns: ['if s.autopilot {\n\t\treturn "autopilot"'] },
  { feature: "X4 断点 B 前端 normalize 放行 autopilot", file: "desktop/frontend/src/lib/types.ts", patterns: ['mode === "autopilot"'] },
  { feature: "X4 toggle 判据锚（preference/approval/applied 一行）", file: "desktop/app.go", patterns: ["desktop: autopilot toggle"] },

  // ── P19（wt-zcode-p19r）──────────────────────────────────────────
  // drain_inbox 的 H1 层键是「会话转录路径」而非 contact id：上游若回退成传
  // contact id，收件箱静默失联且 *.inbox 残渣重新落包目录。
  { feature: "P19 drain_inbox 收件箱层传真实会话路径", file: "internal/agent/drain_inbox_tool.go", patterns: ["drainInboxFromSessionInbox(t.cfg.currentSessionPath(), p.Source, settle, remaining)"] },
  // heap pprof 间隔可调键：上游无此键，merge 后丢失只会让间隔退回硬编码，静默。
  { feature: "P19 heap pprof 间隔配置键接线", file: "desktop/perf_monitor.go", patterns: ["perfMonitorHeapDefaultSeconds", "PerfMonitorHeapIntervalSeconds", "time.NewTicker(m.heapInterval)"] },

  // ── 任务469（wt-469-ask-reliability）─────────────────────────────
  // fence/gap 队列位于 P16 收据行之前：ask 被吞必须「可见 + 可自愈」。锚钉
  // 判定纯函数与打点行——合并丢失只会退回静默吞 ask 的断点形态。
  { feature: "469 fence 判定纯函数（规则逐字保留+prompt 丢弃带对账标记）", file: "desktop/frontend/src/lib/askPanelGate.ts", patterns: ["judgePromptFenceArrival", "reconcile: view.promptEvent"] },
  { feature: "469 fence 丢弃打点+权威对账（ask-panel 行+meta 刷新+重放）", file: "desktop/frontend/src/lib/useController.ts", patterns: ["prompt dropped by stale fence", "schedulePromptFenceReconcile"] },
  { feature: "469 projector 卡住 prompt 上报（gap 修复放弃不吞面板）", file: "desktop/frontend/src/lib/turnEventProjection.ts", patterns: ["onPromptsStranded", "reportStrandedPrompts", "turn-events-gap-repair-epoch-mismatch"] },
  // ── 485（wt-485-lease-release）──────────────────────────────────
  // detached/idle 释放链必须连 tab 的 session lease 一起放（2026-10-05 泄漏
  // 根因）：merge 若顶掉这段，锁句柄随 runtime 释放泄漏、会话永久 busy，
  // 且无冲突标记无编译错误。
  { feature: "485 P0 detached 释放链补 lease 释放", file: "desktop/detached_idle_release.go", patterns: ["if old := tab.swapSessionLease(nil); old != nil {", "old.Release()"] },
  // P1 兜底靠「tracker 在唯一汇合点登记 tab 租约」才成立：store 丢登记则兜
  // 底失明，孤儿锁永远无人释放。
  { feature: "485 P1 租约 tracker 汇合点接线", file: "desktop/tabs.go", patterns: ["syncSessionLeaseTracker(t, key)"] },
  { feature: "485 P1 孤儿自持租约扫描器", file: "desktop/session_lease_leak_sweep.go", patterns: ["orphan in-process session lease released", "sessionLeaseLeakDecision", "agent.SessionLeaseActiveOwnerKeys()"] },
  // agent 侧活动租约键快照是 P1 的数据源，静默回退会让扫描空转。
  { feature: "485 P1 agent 活动租约键快照", file: "internal/agent/session_lease.go", patterns: ["func SessionLeaseActiveOwnerKeys()"] },
  // collab 拒绝退避 + 自持文案：回退则拒绝日志风暴与「另一个窗口」误导复现。
  { feature: "485 P2 collab 拒绝重试退避", file: "desktop/session_collab.go", patterns: ["collabRetryDelay", "deferContactRetry", "p.drain(true)"] },
  { feature: "485 P2 自持 busy 文案去误导", file: "desktop/app.go", patterns: ["already open in this Reasonix instance"] },
  // ── 483（wt-483-optimistic-parallel）─────────────────────────────
  // optimistic_write 的子代理间半边（上游提案 #12052 对照实现）：两处 gate
  // 若被上游 merge 顶掉，双未声明 write_paths 的子代理重新互等串行（实测
  // 干等 30 分钟），且无冲突标记无编译错误。
  { feature: "483 optimistic 子代理间写路径 gate 整体抬升（canStartLocked）", file: "internal/agent/scheduler.go", patterns: ["Task 483: optimistic-parallel mode lifts the subagent-vs-subagent"] },
  { feature: "483 排队 whole-writer 优先权仅保守态生效", file: "internal/agent/claim_live.go", patterns: ["if !s.optimistic.Load() && req.Writer {"] },
  { feature: "483 pump FIFO barrier 仅保守态置位", file: "internal/agent/scheduler.go", patterns: ["the barrier is conservative-only"] },
  // Realize/MarkOpaque 回退成无条件拒绝，则 optimistic 下先写者反被后跑者的
  // whole 声明挡死（负值转移），且切回保守后记录 gate 失效。
  { feature: "483 optimistic Realize 记录不拒绝", file: "internal/agent/scheduler.go", patterns: ["under optimistic-parallel (task 483) the realize is", "if !optimistic {"] },
  { feature: "483 optimistic MarkOpaque 记录不拒绝", file: "internal/agent/scheduler.go", patterns: ["Under optimistic-parallel (task 483) the upgrade is recorded"] },
  { feature: "任务483 调度器并行矩阵测试", file: "internal/agent/scheduler_optimistic_parallel_test.go", patterns: ["TestOptimisticUndeclaredSubagentsRunInParallel", "TestConservativeUndeclaredSubagentsStillSerialize", "TestOptimisticDeclaredUndeclaredPairRuns", "TestOptimisticRealizeRecordsWithoutRefusal"] },

  // ── 任务501（wt-501-heap-high）───────────────────────────────────
  // 阈值高峰快照是 499 内存膨胀取证的先行件：60s 定时池滚动 3 份会冲掉膨胀
  // 现场快照，高峰池（heap-high- 前缀、7 天保留）与防风暴门（单调新高水线 +
  // 30 分钟冷却）是本任务的全部特征。merge 若顶掉，取证能力静默回退且无编译
  // 错误——锚定触发判定形状、两池分离过滤与配置键。
  { feature: "501 高峰快照触发与防风暴门（单调新高水线+冷却）", file: "desktop/perf_monitor.go", patterns: ["func (m *perfMonitor) maybeCaptureHeapHigh", "if tier <= m.heapHighPeakTier && now.Sub(m.lastHeapHighAt) < perfMonitorHeapHighCooldown {", "func (m *perfMonitor) writeHeapHighProfile"] },
  { feature: "501 两池分离（定时池滚动不触 heap-high，每日清理接管 7 天保留）", file: "desktop/perf_monitor.go", patterns: ["perfMonitorHeapHighPrefix", "strings.HasPrefix(name, perfMonitorHeapHighPrefix)"] },
  { feature: "501 配置键双面开关+阈值钳制", file: "internal/config/config.go", patterns: ["experimental_heap_high_profile", "perf_monitor_heap_high_threshold_mb", "PerfMonitorHeapHighDefaultMB"] },
  // ── 任务528（wt-528-heap-threshold-ui）──────────────────────────
  // 501 的阈值原本只能手改 config.toml（提示文案却写「可调」）。本任务补 UI
  // 链路：setter 双写 [desktop] mirror、渲染表 mirror 行、设置面板数值输入
  // （关闭态禁用）。若 merge 顶掉任一环，保存会被静默丢弃或输入框消失且无
  // 编译错误——锚定双写、mirror 渲染行、视图回读与前端输入。
  { feature: "528 heap 阈值 setter 双写（[agent] + [desktop] mirror）", file: "internal/config/edit.go", patterns: ["c.Desktop.PerfMonitorHeapHighThresholdMB = mb", "c.Agent.PerfMonitorHeapHighThresholdMB = mb"] },
  { feature: "528 heap 阈值渲染表 mirror 行", file: "internal/config/render.go", patterns: ["perf_monitor_heap_high_threshold_mb = %d   # desktop: settings-view mirror of [agent] perf_monitor_heap_high_threshold_mb (task 528; 0 = built-in default 6144)"] },
  { feature: "528 heap 阈值视图回读+App setter", file: "desktop/settings_preferences.go", patterns: ["func perfMonitorHeapHighThresholdForView", "func (a *App) SetPerfMonitorHeapHighThresholdMB"] },
  { feature: "528 heap 阈值前端输入（关闭态禁用+重启提示）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["await app.SetPerfMonitorHeapHighThresholdMB(perfHeapHighThreshold)", "disabled={busy || !Boolean(s.experimentalHeapHighProfile)}"] },
  // ── 378B1（wt-378b1-recall-lazy）────────────────────────────────
  // 召回索引懒构建+缓存是 378 基线内存治理的第一件：Load 去预构建、首召构建
  // 缓存、失效即换快照。若被 merge 顶回「Load 预构建」，无编译错误、行为仍
  // 正确，但 boot/压缩重建/每次记忆写入重新背上全量读取+分词（阶段 A 实测
  // cum 289MB）——锚定懒构建时序、缓存门与两枚行为测试。
  { feature: "378B1 召回索引懒构建（Load 不再预构建+首召缓存）", file: "internal/memory/memory.go", patterns: ["recallMu    sync.Mutex", "func (s *Set) recallIndex()", "378B1: Load no longer builds it eagerly"] },
  { feature: "378B1 AutoRecall 走缓存索引（每轮召回零重建）", file: "internal/memory/recall_index.go", patterns: ["lazily on the first recall and cached on the Set (378B1)", "return autoRecallIndexed(s.recallIndex(), result, opts)"] },
  { feature: "378B1 行为钉（懒构建时序+换快照失效）", file: "internal/memory/recall_index_test.go", patterns: ["TestLoadDefersRecallIndexBuildUntilFirstRecall", "TestRecallIndexInvalidationIsSnapshotSwap", "TestRecallIndexEmptyStoreBuildsOnce"] },

  // ── 任务505（wt-505-session-wall）────────────────────────────────
  // 会话图墙 = 铁律8双路径的增强路径：palette 保底「最近会话」切片(12行)
  // 不动，「跳转会话」入口+网格卡片墙挂在 experimental_session_wall 后
  // (默认关)。锚定调色板门（off=同数组引用/锚点缺失=追加不消失）、分组
  // 排序纯函数（项目桶+时间桶+活跃降序）与开关渲染表——merge 若顶掉，
  // 增强面静默消失且无编译错误。
  { feature: "505 palette 门（off=同引用/on=重载运行时右侧/缺锚追加）", file: "desktop/frontend/src/lib/sessionWall.ts", patterns: ["export function insertSessionWallEntry", "if (!enabled) return cmds;", "cmds.findIndex((c) => c.id === \"cmd-reload-runtime\")"] },
  { feature: "505 图墙分组排序（项目桶/时间桶/活跃降序）", file: "desktop/frontend/src/lib/sessionWall.ts", patterns: ["export function groupSessionsForWall", "export function applySessionWallQuery", "sessionActivityTime(b) - sessionActivityTime(a)"] },
  { feature: "505 图墙面板（网格卡片墙+搜索+双分组模式）", file: "desktop/frontend/src/components/SessionWallPanel.tsx", patterns: ["session-wall__grid", "applySessionWallQuery(sessions, query)", "groupSessionsForWall(filtered, mode"] },
  { feature: "505 开关渲染表行", file: "internal/config/render.go", patterns: ["experimental_session_wall"] },

  // 任务482（wt-482-fence-removal，2026-10-05）：tool recovery fence 拦截已退役。
  // 这些锚保护的 NOT 是 fence 本体（已删），而是「删除后必须继续存活」的共用面：
  // 上游 merge 若把 fence 拦截带回来、或把下面的保留件当死代码删掉，都会静默破坏
  // unattended 判定 / 老会话加载 / 跨会话分类。
  { feature: "482 fence 退役：unattended 判定仍依赖豁免函数（拆留件）", file: "internal/agent/run_loop.go", patterns: ["a.turn.unattended = a.autopilot || toolRecoveryExempt(ctx)"] },
  { feature: "482 fence 退役：豁免函数与 ctx 载体保留", file: "internal/agent/tool_recovery_records.go", patterns: ["func toolRecoveryExempt(ctx context.Context) bool", "func WithToolApprovalMode(", "func WithUnattendedRun(", "a.resolveSideEffectFreeInterruptedCalls()"] },
  { feature: "482 fence 退役：tool_recovery 不在 host 白名单（铁律3）", file: "internal/boot/agent_preset.go", patterns: ["the former \"tool_recovery\" host-control entry"] },
  { feature: "482 fence 退役：记录态类型保留（老会话加载依赖）", file: "internal/event/recovery.go", patterns: ["type RecoveryStatus struct", "RequiresUserDecision"] },
  { feature: "482 fence 退役：老会话 runtime/recovery 投影保留", file: "internal/session/projection.go", patterns: ["func projectRuntimeRecovery"] },
  { feature: "482 fence 退役：跨会话 recovery_required 分类保留", file: "internal/agent/session_subscribe.go", patterns: ["case \"recovery_required\":"] },
  { feature: "482 fence 退役：turn 终态常量保留", file: "internal/event/turn_status.go", patterns: ['TurnRecoveryRequired TurnStatus = "recovery_required"'] },

  // 任务519（wt-519-recovery-card，2026-10-06）：核实卡移除（X5 a1 结算前移）。
  // agent 构造（会话加载）即结算无副作用白名单遗留记录——降级后的记录行不再
  // 为只读遗留记录停留；写类记录保持未决（审计事实，S9 勿动）。merge 若顶掉
  // 构造期结算，崩溃重启后的只读记录会重新挂进记录行直到下一轮对话。
  { feature: "519 构造期结算白名单遗留记录（agent.New 收尾调用）", file: "internal/agent/agent.go", patterns: ["Task 519（核实卡移除，X5 a1 结算前移）", "a.resolveSideEffectFreeInterruptedCalls()"] },
  { feature: "519 行为钉（构造即结算+写类保持未决）", file: "internal/agent/tool_recovery_side_effect_free_test.go", patterns: ["TestNewSettlesLeftoverSideEffectFreeRecords", "write-capable leftover must stay pending"] },

  // 任务531（wt-531-dispatch-positive，2026-10-06）：派遣正面说明。<subagent-policy>
  // 文本块无编译依赖，merge 顶掉只会静默退回「只有负面清单」。锚定正面三件套
  // （何时该派/派了得到什么/成本可控）+ write_paths 解锁写并行的引导句 +
  // 文案钉测试（含步数公式措辞与工具 schema 的防漂移同步钉）。
  { feature: "531 派遣正面说明（balanced/aggressive 正面三件套）", file: "internal/agent/subagent_policy.go", patterns: ["When to dispatch (a positive list", "What dispatching buys", "Dispatching is affordable"] },
  { feature: "531 write_paths 解锁写并行引导（aggressive）", file: "internal/agent/subagent_policy.go", patterns: ["unlock parallel writers", "serializes every writer behind it"] },
  { feature: "531 文案钉测试（正面存在+write_paths 引导+公式措辞同步）", file: "internal/agent/subagent_policy_test.go", patterns: ["TestSubagentPolicyGuidancePositiveGuidance", "TestSubagentPolicyGuidanceStepCapWordingMatchesSchema"] },

  // ── 任务499（wt-499-memory-fix）──────────────────────────────────
  // 桌面版内存膨胀（10-05 现场 14.6GB heap）的持有链两环：graph cache 无字节
  // 上限 + 全仓无失效点。字节上限（总 2048MiB / 单体 1024MiB，账目=st.size）
  // 与关闭失效（关 tab / detached 释放 / 删会话三钩子调 InvalidateSessionGraph）
  // 是本件的全部特征。merge 若顶掉，闭会话图形重新无限滞留且无编译错误——
  // 锚定逐出守卫、失效导出与三处钩子、配置键。
  { feature: "499 cache 字节上限两层（LRU 逐出永不逐 just-put/单体超限拒绝入场）", file: "internal/agent/save_dag_graph_cache.go", patterns: ["func sessionGraphCacheEvictOverbudgetLocked", "sessionGraphCacheEntryRefusals.Add(1)", "func SessionGraphCacheByteStats"] },
  { feature: "499 关会话即失效（InvalidateSessionGraph 导出）", file: "internal/agent/save_dag_graph_cache.go", patterns: ["func InvalidateSessionGraph(sessionPath string) (freedBytes int64, ok bool)", "func SessionGraphCacheInvalidations"] },
  { feature: "499 失效钩子三落点（关tab/detached释放/删会话）", file: "desktop/tabs.go", patterns: ["invalidated dag graph cache on tab close"] },
  { feature: "499 失效钩子 detached 释放", file: "desktop/detached_idle_release.go", patterns: ["invalidated dag graph cache on detached release"] },
  { feature: "499 失效钩子删会话", file: "desktop/app.go", patterns: ["invalidated dag graph cache on session delete"] },
  { feature: "499 配置键字节上限双键+渲染", file: "internal/config/config.go", patterns: ["dag_graph_cache_max_mb", "dag_graph_cache_entry_max_mb"] },

  // 任务410 Sentinel 降维版（硬禁区底线+出口 secret 扫描，yolo 下也生效）。
  // 三个锚各护一面：前置检查调用点（被顶掉=底线失效）、零模型参与源码断言
  // （被顶掉=LLM 依赖可能静默回流）、用户全局配置段（被顶掉=开关无法持久）。
  { feature: "任务410 审批链固定前置检查（Auto Guard/MCP 快路径/普通门之前）", file: "internal/agent/execute_one.go", patterns: ["sentinel.CheckToolCall(plan.permName, plan.permArgs, a.svc.workspaceRoot)"] },
  { feature: "任务410 零模型参与源码断言（import 白名单）", file: "internal/sentinel/zero_model_test.go", patterns: ["TestSentinelPackageImportsAreModelFree", "allowedImports"] },
  { feature: "任务410 [sentinel] 用户全局配置段（hard_forbidden 默认开+exit_scan 实验开关）", file: "internal/config/config.go", patterns: ["SentinelConfig", "hard_forbidden", "exit_scan"] },
  { feature: "任务410 boot 装配（开关+保护路径+审计 JSONL）", file: "internal/boot/boot.go", patterns: ["sentinel.SetHardForbidden", "sentinel.SetAuditPath"] },
  // ── 任务463（wt-zcode-463 已合入；本锚 wt-463-collapse-all 补登记）──
  // 「收起全部工作过程」折叠/展开双向开关：composer 按钮方向由 transcript
  // 经 workProcessFoldState store 上报的真实折叠状态驱动。merge 若顶掉
  // store 或上报接线，按钮静默退回单向（只收起不展开）且无编译错误——
  // 锚定 store 三导出、聚合纯函数、双向渲染与事件、上报/注销接线、三语。
  { feature: "463 折叠状态 store（上报/注销/订阅三导出）", file: "desktop/frontend/src/lib/workProcessFoldState.ts", patterns: ["export function publishWorkProcessFoldState", "export function clearWorkProcessFoldState", "export function useWorkProcessFoldAggregate"] },
  { feature: "463 全折叠聚合纯函数（手动混合态不误报）", file: "desktop/frontend/src/lib/transcriptRows.ts", patterns: ["export function allWorkProcessesCollapsed("] },
  { feature: "463 composer 双向按钮（方向标记+事件分叉+文案切换）", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["data-fold-state={allFoldsCollapsed ? \"collapsed\" : \"expanded\"}", "allFoldsCollapsed ? \"reasonix:expand-all-folds\" : \"reasonix:collapse-all-folds\"", "const foldToggleLabel = allFoldsCollapsed ? t(\"composer.expandAll\") : t(\"composer.collapseAll\");"] },
  { feature: "463 transcript 状态上报+注销+展开接线", file: "desktop/frontend/src/components/Transcript.tsx", patterns: ["publishWorkProcessFoldState(tabId, {", "return () => clearWorkProcessFoldState(boundTabId);", "const handleExpandAll = useTranscriptCommand(() => {", "window.addEventListener(\"reasonix:expand-all-folds\", onExpandAll);"] },
  { feature: "463 三语文案 zh", file: "desktop/frontend/src/locales/zh.ts", patterns: ["\"composer.expandAll\": \"展开全部工作过程\""] },
  { feature: "463 三语文案 zh-TW", file: "desktop/frontend/src/locales/zh-TW.ts", patterns: ["\"composer.expandAll\": \"全部展開工作過程\""] },
  { feature: "463 三语文案 en", file: "desktop/frontend/src/locales/en.ts", patterns: ["\"composer.expandAll\": \"Expand all work processes\""] },
  { feature: "463 双向开关测试存续", file: "desktop/frontend/src/__tests__/fold-toggle-button.test.tsx", patterns: ["allWorkProcessesCollapsed", "reasonix:expand-all-folds"] },
  // ── 任务465（wt-465-autopilot-4th；两维矩阵 + X4 断点 C）──
  // 第一维【询问/自动/Yolo/autopilot】×第二维【常规/计划/目标】：autopilot
  // 隐含 yolo，档位切换自动满足 325 前置并留痕（assumed_yolo）；goal×autopilot
  // 合法同开，合成标签不再承载全部状态（wire 裸旗）；「+」菜单运行中不可点的
  // 缺口由模式条第四档承接；desktopTabEntry 补 autopilot 列，重启忠实保留。
  // merge 若顶掉任一环：档位静默失效 / 重启丢旗 / goal 被清——均无编译错误。
  { feature: "465 断点C desktopTabEntry autopilot 列（持久化+恢复写入）", file: "desktop/tabs_persistence_types.go", patterns: ["Autopilot bool `json:\"autopilot,omitempty\"`", "Autopilot:   tab.autopilot,"] },
  { feature: "465 恢复路径双源+325 再过门", file: "desktop/app.go", patterns: ["if entry.Autopilot || tabSessionAutopilot(tab.SessionPath)", "gateRestoredAutopilotDefaults(on, maxRuntime, grace, askEnabled, askWait, askAutoContinue, tab.toolApprovalMode)"] },
  { feature: "465 档位自动满足 yolo+双留痕（slog assumed_yolo+notice）", file: "desktop/app.go", patterns: ["effectiveApproval = control.ToolApprovalYolo", "\"assumed_yolo\", assumedYolo", "NoticeCodeAutopilotAssumedYolo, autopilotAssumedYoloText"] },
  { feature: "465 两维独立：dim-2 播种不清旗+守卫仅真实边触发", file: "desktop/app.go", patterns: ["autopilotOn, autopilotRuntime, autopilotGrace := tab.autopilot, tab.autopilotMaxRuntime, tab.autopilotApprovalGrace", "if autopilotOn && !wasAutopilot {"] },
  { feature: "465 门文件新 notice 码（决策记录）", file: "desktop/autopilot_gate.go", patterns: ["NoticeCodeAutopilotAssumedYolo = \"autopilot_assumed_yolo\"", "autopilotAssumedYoloText"] },
  { feature: "465 wire 裸旗（Meta/TabMeta autopilot 字段+赋值）", file: "desktop/app.go", patterns: ["Autopilot             bool               `json:\"autopilot,omitempty\"`", "Autopilot:             snap.autopilot,"] },
  { feature: "465 TabMeta 裸旗赋值", file: "desktop/tabs.go", patterns: ["Autopilot:         tab.autopilot,"] },
  { feature: "465 profile 裸旗（两维底层状态同时在）", file: "desktop/frontend/src/lib/composerProfile.ts", patterns: ["autopilot: boolean", "function profileAutopilot(raw: boolean | undefined, label: CollaborationMode): boolean", "autopilot: profileAutopilot(meta.autopilot, collaborationMode)"] },
  { feature: "465 模式条第四档（按钮+滑块跟随裸旗）", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["composer-modebar__item--autopilot", "data-mode={autopilotModeOn ? \"autopilot\" : toolApprovalMode}", "onClick={() => chooseTaskMode(\"autopilot\")}"] },
  { feature: "465 徽章只承载第二维+菜单 autopilot 撤出", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["(planModeOn || goalModeOn)", "任务 465 两维矩阵：autopilot 移入模式条第四档（第一维）"] },
  { feature: "465 四格布局（网格+滑块四等分+yolo 配色）", file: "desktop/frontend/src/styles.css", patterns: ['.composer-modebar--approval[data-autopilot="on"] {', "width: calc((100% - 4px) / 4);", '.composer-modebar[data-mode="autopilot"] {'] },
  { feature: "465 owner 分支（autopilot 档不清 goal+离 yolo 镜像联动）", file: "desktop/frontend/src/app-runtime/composerModeOwner.ts", patterns: ["request.kind === \"collaboration\" && request.mode === \"autopilot\"", "patch.autopilot = false;"] },
  { feature: "465 档位排干对账（普通工具卡精确清卡）", file: "desktop/frontend/src/lib/useController.ts", patterns: ['(visible.kind ?? "tool") === "tool"'] },
  { feature: "465 三语文案 zh", file: "desktop/frontend/src/locales/zh.ts", patterns: ["\"notice.autopilotAssumedYolo\": \"Autopilot 已开启：审批自动切到 Yolo（决策已记录）。\""] },
  { feature: "465 三语文案 zh-TW", file: "desktop/frontend/src/locales/zh-TW.ts", patterns: ["\"notice.autopilotAssumedYolo\": \"Autopilot 已開啟：審批自動切到 Yolo（決策已記錄）。\""] },
  { feature: "465 三语文案 en", file: "desktop/frontend/src/locales/en.ts", patterns: ['"notice.autopilotAssumedYolo": "Autopilot on: approval switched to YOLO automatically (decision recorded)."'] },
  { feature: "465 notice 按码本地化映射", file: "desktop/frontend/src/lib/controllerNotices.ts", patterns: ["autopilot_assumed_yolo: \"notice.autopilotAssumedYolo\""] },
  { feature: "465 两维矩阵测试存续", file: "desktop/frontend/src/__tests__/task465-two-axis-matrix.test.ts", patterns: ["goalAutopilotProfile.autopilot", "不得清 goal", "executeComposerMode"] },
  { feature: "465 Go 门矩阵测试存续（四档自动满足/两维独立/断点C）", file: "desktop/autopilot_gate_test.go", patterns: ["TestAutopilotTierPreservesTaskDimension", "TestRestoreTabEntryCarriesAutopilotFlag", "NoticeCodeAutopilotAssumedYolo"] },

  // ── 任务546 新建会话「沿用最近会话的目录」──
  // cwd 语义必须单源：选取可用性判定走 545 的 SessionWorkspaceRoot，merge
  // 若另起炉灶（自写 stat/绝对性判断）即为两套机制。菜单无候选不出现、
  // 失效回落必须带可见提示（不静默）、主 + 按钮行为不变。
  { feature: "任务546 最近活动会话 cwd 选取（复用545共用解析）", file: "internal/agent/session_workspace.go", patterns: ["func LatestSessionWorkspaceRoot", "SessionWorkspaceRoot(info.Path)"] },
  { feature: "任务546 Wails 绑定（本机会话目录枚举，不跨设备）", file: "desktop/latest_session_workspace.go", patterns: ["func (a *App) LatestSessionWorkspace", "lastSessionWorkspaceInfo", "a.knownSessionDirs()"] },
  { feature: "任务546 TabBar 下拉（主按钮不变+无候选不出现）", file: "desktop/frontend/src/components/TabBar.tsx", patterns: ["onFetchLastSessionWorkspace", "tabbar__new-caret", "last-session-workspace"] },
  { feature: "任务546 失效回落可见提示", file: "desktop/frontend/src/App.tsx", patterns: ["tabBar.lastCwdFallback"] },
  { feature: "任务546 AppRuntime 挂载点接线", file: "desktop/frontend/src/app-shell/AppRuntimeView.tsx", patterns: ["onNewTabInWorkspace"] },
  { feature: "任务546 桥接声明", file: "desktop/frontend/src/lib/bridge.ts", patterns: ["LatestSessionWorkspace(): Promise<LastSessionWorkspaceInfo>"] },
  { feature: "任务546 locale zh", file: "desktop/frontend/src/locales/zh.ts", patterns: ["tabBar.lastCwd"] },
  { feature: "任务546 locale zh-TW", file: "desktop/frontend/src/locales/zh-TW.ts", patterns: ["tabBar.lastCwd"] },
  { feature: "任务546 locale en", file: "desktop/frontend/src/locales/en.ts", patterns: ["tabBar.lastCwd"] },
  { feature: "任务546 下拉样式", file: "desktop/frontend/src/styles.css", patterns: [".tabbar__new-caret", ".tabbar__newmenu-path"] },
  { feature: "任务546 测试存续（含失效对立输入）", file: "internal/agent/session_workspace_test.go", patterns: ["TestLatestSessionWorkspaceRootStaleDirectoryReportedNotSilentlySkipped"] },
  // ── 任务550（wt-550-ghost-topic）侧栏闪现与修复横幅（机制验收 2026-10-06 修订版）──
  // 四个锚各护一条机制：merge 丢掉任何一条，「半持久化幽灵」就会复发——
  // 索引写失败重新被 `_ =` 吞掉（①）、三源失配重新不可查询（②）、可见性
  // 判据重新依赖时序/修复态（③）、横幅重新回退旧字段 repairPending（①）。
  { feature: "550① 索引写失败不吞（计数+结构化日志，两处写入点）", file: "desktop/app.go", patterns: ["topicIndexWriteFailures.Add(1)", "new-session topic index write failed", "first-turn topic index write failed"] },
  { feature: "550② 三源对账（tabs↔topic-state↔会话文件，失配可查询不自动删）", file: "desktop/topic_inventory.go", patterns: ["func (a *App) ReconcileTopicInventory", "func (a *App) GetTopicInventoryMismatches", "topicInventoryIndexTopicWithoutSessions"] },
  { feature: "550② 启动加载时对账接线", file: "desktop/session_catalog_lifecycle.go", patterns: ["logTopicInventorySummary(a.reconcileTopicInventory(ctx))"] },
  { feature: "550② catalog 零会话 tombstone 可分离读（对账依赖）", file: "internal/sessioncatalog/catalog.go", patterns: ["func (c *Catalog) GetTopicWithSessionCount"] },
  { feature: "550③ 项目树可见性单一稳定判据（运行时行+目录行）", file: "desktop/session_catalog_runtime.go", patterns: ["func runtimeTopicRowIsBlank", "func (a *App) ordinaryTreeHidesBlankShell"] },
  { feature: "550① 修复横幅只信精确字段（去掉 repairPending 回退）", file: "desktop/frontend/src/lib/sessionCatalogPresentation.ts", patterns: ["(repairActive ?? 0) > 0"] },
  { feature: "550 横幅回退链对立输入测试存续", file: "desktop/frontend/src/__tests__/session-catalog-notice.test.ts", patterns: ["must never resurrect the legacy repairPending"] },
  // ── 任务498（wt-498-projectiondb-v2）：projectiondb v2 路线收口——
  // 「验证式单次原子替换 + 中断即弃、幂等重跑」。v1 三件基建
  // （integrity/rebuild_resume/rebuild_validation）fork 从未拥有，
  // 此处锚定 v2 语义三件套：中断语义成文、崩溃残留清扫、
  // 校验实质化（新连接从磁盘重读，页缓存不得掩盖损坏）。
  { feature: "498 中断语义成文 + 崩溃残留清扫", file: "internal/projectiondb/projectiondb.go", patterns: ["cleanOrphanRebuildResidues", "discard on interrupt, idempotent rerun"] },
  { feature: "498 校验实质化（新连接磁盘重读判决）", file: "internal/projectiondb/projectiondb.go", patterns: ["func validateReplacementFile", "mode=ro&immutable=1", "mask on-disk corruption"] },
  { feature: "498 中断注入测试四件（取消/残留清扫/备份保留/校验失败不替换）", file: "internal/projectiondb/projectiondb_test.go", patterns: ["TestRebuildCancellationKeepsOldDatabaseAndCleansSibling", "TestRebuildSweepsCrashResidueBeforeRebuilding", "TestRebuildKeepsRetainedBackupsWhileSweepingSiblings", "TestRebuildValidationFailureDoesNotSwap"] },
  // ── 任务567 ask 链路读数埋点 + 工具卡占位态 ──
  // 后端三处 slog 检查点（entry/prompt_lock/ask_emit）支撑弹窗延时的桌面端
  // 二分归因；merge 若顶掉：埋点静默消失，84s 归因重新不可做。
  { feature: "567 ask 链路三检查点 slog（entry/prompt_lock/ask_emit）", file: "internal/control/controller.go", patterns: ["[ask-panel] ask chain checkpoint", "\"stage\", \"entry\"", "\"stage\", \"prompt_lock\"", "\"stage\", \"ask_emit\""] },
  // 前端 ask 卡 pending 占位替换「JSON 入参原文+跑秒」的误导呈现（567 调研
  // 报告第七节）；merge 若顶掉：占位回退成「看起来像卡死」的工具卡。
  { feature: "567 ask 卡 pending 占位判据", file: "desktop/frontend/src/components/ToolCard.tsx", patterns: ["const askPending = item.name === \"ask\" && item.status === \"running\";"] },
  { feature: "567 占位三语文案 zh", file: "desktop/frontend/src/locales/zh.ts", patterns: ["\"tool.askWaiting\": \"等待用户确认…\""] },
  { feature: "567 占位三语文案 zh-TW", file: "desktop/frontend/src/locales/zh-TW.ts", patterns: ["\"tool.askWaiting\": \"等待使用者確認…\""] },
  { feature: "567 占位三语文案 en", file: "desktop/frontend/src/locales/en.ts", patterns: ["\"tool.askWaiting\": \"waiting for your answer…\""] },
  { feature: "567 占位态测试存续", file: "desktop/frontend/src/__tests__/tool-card-ask-pending.test.tsx", patterns: ["pending ask card shows the waiting placeholder", "pending ask card does not render the raw args JSON"] },

];

let failed = 0;
const results = [];
for (const check of CHECKS) {
  const path = join(ROOT, check.file);
  if (!existsSync(path)) {
    failed += 1;
    results.push(`MISSING FILE  ${check.file}  (${check.feature})`);
    continue;
  }
  // Normalise CRLF so multi-line patterns match on Windows checkouts too.
  const content = readFileSync(path, "utf8").replace(/\r\n/g, "\n");
  const missing = check.patterns.filter((p) => !content.includes(p));
  if (missing.length > 0) {
    failed += 1;
    results.push(`MISSING       ${check.file}  (${check.feature})\n  patterns: ${missing.map((p) => JSON.stringify(p)).join(", ")}`);
  } else {
    results.push(`OK            ${check.feature}`);
  }
}

for (const line of results) process.stdout.write(line + "\n");
process.stdout.write(`\n${CHECKS.length - failed}/${CHECKS.length} checks passed.\n`);
if (failed > 0) {
  process.stdout.write(
    "\nFork-only features are missing from the working tree — likely dropped by an\n"
    + "upstream auto-merge (no conflict markers, no build errors). Recover from the\n"
    + "feature's origin commit (git log --all --oneline -- <file>) and re-run.\n",
  );
  process.exit(1);
}
