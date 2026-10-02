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
  { feature: "任务449 实验室孤儿开关合并（新键渲染）", file: "internal/config/render.go", patterns: ["experimental_orphan_handling"] },
  { feature: "任务449 实验室孤儿开关合并（旧键迁移）", file: "internal/config/load.go", patterns: ["migrateOrphanHandlingMerge"] },
  { feature: "任务449 实验室孤儿开关合并（实验室单条 UI）", file: "desktop/frontend/src/components/SettingsPanel.tsx", patterns: ["orphanHandling", "SetExperimentalOrphanHandling"] },
  { feature: "任务244 B6 工具并发分级表文档化", file: "internal/agent/execute_batch.go", patterns: ["task 244 B6", "admission table", "no FIFO queue"] },
  { feature: "任务244 B7 心跳等待阈值推导注释", file: "desktop/heartbeat.go", patterns: ["task 244 B7: threshold-derivation note", "43,120"] },
  { feature: "任务244 B7 heldBy 方向语义注释", file: "internal/servepool/servepool.go", patterns: ["never route into a corpse", "experimental_orphan_lease_reclaim"] },
  { feature: "任务244 B7 roster 可路由方向注释", file: "internal/agent/session_collab_tools.go", patterns: ["routing into a corpse", "never a guessed idle"] },
  { feature: "任务244 B8 失败级联豁免面分类", file: "internal/agent/execute_batch.go", patterns: ["task 244 B8", "fail-cascade admission", "exemption face"] },
  { feature: "任务244 B9 模型能力过滤开关", file: "internal/config/config.go", patterns: ["experimental_model_capability_filter"] },
  { feature: "任务244 B9 模型能力过滤逻辑", file: "internal/agent/task.go", patterns: ["checkModelImageCapability", "capability filter"] },
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
  // 任务 349：群聊通道——channel 实体（SQLite+md 导出）+ 发布订阅展开单发
  // （复用 309 MailStore，铁律 8 无第二投递通道）+ per-recipient delivered/
  // read + 429 治理（错峰/followup/小时上限）+ 取消消息（20261002 增量：
  // 发送方墓碑 + 拦 queued + 在途 drain 投递前重查；已投递副本不追回）。
  // 锁实体层、四工具与注册。
  { feature: "任务349 channel 实体+展开单发+429 治理", file: "internal/collabchannel/collabchannel.go", patterns: ["DrainFanout", "ErrHourlyCap", "ExportMarkdown", "s.mail.Deliver("] },
  { feature: "任务349 取消消息（墓碑+拦 queued+在途重查）", file: "internal/collabchannel/collabchannel.go", patterns: ["func (s *Store) Cancel(", "ErrNotSender", "state != \"queued\"", "cancelled_at"] },
  { feature: "任务349 四工具（查看/获取/发送/取消）", file: "internal/agent/channel_tools.go", patterns: ["channel_list", "channel_read", "channel_send", "channel_cancel", "channelSpawn"] },
  { feature: "任务349 四工具 boot 注册", file: "internal/boot/boot.go", patterns: ["NewChannelSendTool(collab)", "NewChannelReadTool(collab)", "NewChannelCancelTool(collab)"] },
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
  { feature: "任务325 开启入口接线（恢复/新标签/选择器/审批切换）", file: "desktop/app.go", patterns: ["gateRestoredAutopilotDefaults(on, maxRuntime, grace, tab.toolApprovalMode)", "gateRestoredAutopilotDefaults(autopilot, maxRuntime, approvalGrace, toolApprovalMode)", "gateRestoredAutopilotDefaults(prefOn, prefRuntime, prefGrace, approvalMode)", "closeAutopilotForOffYolo(tab, mode)"] },
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

  // 任务411（20261001 批十收尾）：快速切换版本治理——出包自动清理 + 面板删除历史版本。
  // 保留规则只实现一次（installlayout.PruneVersionTrees，含 current.json 指向硬跳过），
  // 出包脚本经 tools/prune-versions 调它；合并丢了任一环都会让 versions/ 重新堆积。
  { feature: "任务411 versions 保留规则（保留 N + current 指向硬跳过）", file: "internal/installlayout/prune.go", patterns: ["func PruneVersionTrees", "keep must be >= 0", "name == active"] },
  { feature: "任务411 出包脚本接自动清理（--keep/REASONIX_VERSIONS_KEEP）", file: "scripts/build-local-installer.sh", patterns: ["tools/prune-versions", "--keep", "REASONIX_VERSIONS_KEEP"] },
  { feature: "任务411 面板删除历史版本（Go 绑定拒绝 current/运行中版本）", file: "desktop/version_switch.go", patterns: ["func (a *App) DeleteInstalledVersion", "is the active version", "running from"] },
  { feature: "任务411 面板删除入口+确认交互（TSX）", file: "desktop/frontend/src/components/VersionSwitchDialog.tsx", patterns: ["versionSwitchDeleteConfirm", "onDelete", "btn--danger"] },
  { feature: "任务411 面板删除接线（App.tsx handler）", file: "desktop/frontend/src/App.tsx", patterns: ["handleDeleteVersion", "DeleteInstalledVersion(version)"] },
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
