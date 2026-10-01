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
  { feature: "任务160 顶部上滚加载更早（开关门控）", file: "desktop/frontend/src/lib/useTranscriptKernel.ts", patterns: ["autoLoadOlderAtTop", "HISTORY_TOP_GUARD_PX", "requestOlderAtTop"] },
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
