// tl1797：对话 tab 悬停卡片——纯模型层。TabBar 收 TabMeta（后端 tab 快照，
// desktop/tab_meta.go），悬停卡片的补充信息（模型/轮次/最后活跃/上下文占用）
// 走 App 注入的异步 resolver（ListProjectTree 树查找 + ContextUsageForTab）。
// 本模块只做纯计算：输入 TabMeta + 可选补充数据 + 译者，输出三区块行模型；
// 不碰 React/桥，测试无需 mock。时间/时长/token/金额格式化全部复用既有
// 收口件（durationFormat/format/projectTreeTopic/money/modelRefLabel），
// 不另造第二把尺子。

import type { Translator } from "./i18n";
import { formatDurationLabel, formatRelativeAgeLabel } from "./durationFormat";
import { formatTokens } from "./format";
import { formatMoneyLocalized } from "./money";
import { modelRefDisplayLabel } from "./modelRefLabel";
import { sessionModelHover } from "./sessionModelHover";
import { normalizeCollaborationMode, normalizeMode, normalizeToolApprovalMode } from "./types";
import type { ProjectNode, TabMeta } from "./types";

/** tl1797 数据源核对表（逐字段 file:line 见实施报告）：
 *  - TabMeta 直读（desktop/tab_meta.go）：标题/工作区/分支/running/turnStartedAt/
 *    pendingPrompt/turnStatus/collaborationMode/toolApprovalMode/autopilot；
 *  - 树查找（ProjectNode，ListProjectTree）：lastActivityAt/turns/modelRef/metaModel；
 *  - ContextUsageForTab：used/window/compactRatio/sessionCost/sessionCurrency；
 *  - 努力档：仅活跃会话可得（useController state.effort）；非活跃不可得，
 *    extra.effortLabel 缺席即不渲染该行（不硬造）。 */
export interface TabHoverExtra {
  /** ProjectNode.lastActivityAt（types.ts:777）；0/undefined = 未得。 */
  lastActivityAt?: number;
  /** ProjectNode.turns（types.ts:773）——历史总轮次（sidecar 统计）。 */
  turns?: number;
  /** ProjectNode.modelRef / metaModel（types.ts:768-770，tl1387 链同源）。 */
  modelRef?: string;
  metaModel?: string;
  /** 仅活跃会话：useController state.effort 的展示名（预格式化）。 */
  effortLabel?: string;
  /** ContextUsageForTab 快照（context_maintenance.go:13-18）。 */
  context?: { used: number; window: number; compactRatio?: number };
  sessionCost?: number;
  sessionCurrency?: string;
}

export type TabHoverCardTone = "running" | "attention" | "idle";

export interface TabHoverCardRow {
  key: string;
  label: string;
  value: string;
  tone?: TabHoverCardTone;
}

export interface TabHoverCardSections {
  /** 身份区首行：完整标题（workspace / 标题，不截断）。 */
  identityTitle: string;
  identityRows: TabHoverCardRow[];
  runtimeRows: TabHoverCardRow[];
  consumptionRows: TabHoverCardRow[];
  /** 运行中（驱动调用方每秒重算已耗时）。 */
  running: boolean;
}

/** 与 TabBar.tsx 旧 tabFullTitle 同文（迁库复用；TabBar 改为 import 本函数）。 */
export function tabDisplayTitle(tab: TabMeta): string {
  if (tab.tabType === "file" || tab.scope === "file") return tab.topicTitle?.trim() || tab.filePath?.split("/").filter(Boolean).pop() || "File";
  const title = tab.topicTitle?.trim();
  if (tab.scope === "global") return title || "Global";
  return title || "Untitled";
}

export function tabFullTitle(tab: TabMeta): string {
  if (tab.tabType === "file" || tab.scope === "file") return tab.filePath || tabDisplayTitle(tab);
  if (tab.scope === "global") {
    const title = tabDisplayTitle(tab);
    const workspaceName = tab.workspaceName?.trim() || "Global";
    return title === workspaceName ? workspaceName : `${workspaceName} / ${title}`;
  }
  const workspaceName = tab.workspaceName?.trim() || "Project";
  return `${workspaceName} / ${tabDisplayTitle(tab)}`;
}

/** 树查找：按 topicId 优先、sessionPath 兜底，深度优先遍历分组/项目嵌套。
 *  file 型 tab 无会话节点（返回 undefined，身份区只落路径）。 */
export function findProjectNodeForTab(nodes: ProjectNode[], tab: TabMeta): ProjectNode | undefined {
  if (tab.tabType === "file" || tab.scope === "file") return undefined;
  const topicId = (tab.topicId ?? "").trim();
  const sessionPath = (tab.sessionPath ?? "").trim();
  if (!topicId && !sessionPath) return undefined;
  const visit = (list: ProjectNode[] | undefined): ProjectNode | undefined => {
    for (const node of list ?? []) {
      if (!node) continue;
      if (topicId && (node.topicId ?? "").trim() === topicId) return node;
      if (!topicId && sessionPath && (node.sessionPath ?? "").trim() === sessionPath) return node;
      const hit = visit(node.children);
      if (hit) return hit;
    }
    return undefined;
  };
  return visit(nodes);
}

function runtimeFace(tab: TabMeta, t: Translator): { label: string; tone: TabHoverCardTone } {
  // 与 TabBar 渲染口径同序：断开 > 待确认 > 未加载 > 运行 > 空闲（tl1266/
  // 任务730 同一把尺子：徽章/hover 文案互斥序）。
  if (tab.runtime?.issue?.code === "session_missing") return { label: t("tabBar.disconnected"), tone: "attention" };
  if (tab.pendingPrompt) return { label: t("projectTree.status.waitingConfirmation"), tone: "attention" };
  if (!tab.ready && tab.runtime?.phase === "starting" && !tab.running) return { label: t("tabBar.notLoaded"), tone: "idle" };
  if (tab.running) return { label: t("tabBar.hoverCard.stateRunning"), tone: "running" };
  return { label: t("tabBar.hoverCard.stateIdle"), tone: "idle" };
}

/** 模式态行：协作档（plan/goal/autopilot）与审批档（auto/yolo）与 TabBar
 *  徽章/hover title 同源归一（normalize 三件套）；文案复用既有 keys
 *  （composer.taskMode* / composer.modeNormal / composer.modeYolo），不另造
 *  一套模式词。都缺省时返回 undefined。 */
function modeFace(tab: TabMeta, t: Translator): string | undefined {
  const mode = normalizeMode(tab.mode);
  const collaborationMode = normalizeCollaborationMode(tab.collaborationMode, tab.goal, mode);
  const toolApprovalMode = normalizeToolApprovalMode(tab.toolApprovalMode, mode);
  const parts: string[] = [];
  if (collaborationMode === "autopilot" || tab.autopilot) parts.push(t("composer.taskModeAutopilot"));
  if (collaborationMode === "plan") parts.push(t("composer.taskModePlanShort"));
  if (collaborationMode === "goal") parts.push(t("composer.taskModeGoalShort"));
  if (toolApprovalMode === "auto") parts.push(t("composer.modeNormal"));
  if (toolApprovalMode === "yolo") parts.push(t("composer.modeYolo"));
  return parts.length > 0 ? parts.join(" · ") : undefined;
}

function modelFace(tab: TabMeta, extra: TabHoverExtra | undefined, t: Translator): string | undefined {
  // tl1387 同链：live > lastKnown > 创建时（标注，复用既有 locale key
  // projectTree.modelHover.createdAt）。display 名走 modelRefDisplayLabel
  // 注册表（tl1547），无目录快照时回退原始 ref。
  const key = (tab.sessionPath ?? "").trim() || `${tab.topicId ?? ""}\u0000${tabDisplayTitle(tab)}`;
  const hover = sessionModelHover(key, { modelRef: extra?.modelRef, metaModel: extra?.metaModel });
  if (!hover) return undefined;
  const label = modelRefDisplayLabel(hover.model);
  return hover.kind === "createdAt" ? t("projectTree.modelHover.createdAt", { model: label }) : label;
}

function consumptionFace(extra: TabHoverExtra | undefined, t: Translator): TabHoverCardRow[] {
  const rows: TabHoverCardRow[] = [];
  const ctx = extra?.context;
  if (ctx && ctx.window > 0) {
    const usagePct = Math.min(100, Math.max(0, Math.round((ctx.used / ctx.window) * 100)));
    const compactPct = ctx.compactRatio && ctx.compactRatio > 0 ? Math.round(ctx.compactRatio * 100) : null;
    // StatusBar.tsx:278 同口径：compactRatio 为 0-1 分数。
    const value = compactPct != null
      ? t("tabBar.hoverCard.contextWithHeadroom", {
          used: formatTokens(ctx.used),
          window: formatTokens(ctx.window),
          pct: usagePct,
          headroom: formatTokens(Math.max(0, Math.round(ctx.window * (compactPct / 100)) - ctx.used)),
          compactPct,
        })
      : t("tabBar.hoverCard.context", { used: formatTokens(ctx.used), window: formatTokens(ctx.window), pct: usagePct });
    const tone: TabHoverCardTone | undefined =
      usagePct >= 90 ? "attention" : compactPct != null && usagePct >= compactPct ? "attention" : undefined;
    rows.push({ key: "context", label: t("tabBar.hoverCard.labelContext"), value, tone });
  }
  if (extra && typeof extra.sessionCost === "number" && extra.sessionCost > 0) {
    // 数据源核对表申报：桥上无「本轮成本」wire 字段（ContextInfo 只有会话
    // 累计 sessionCost），本轮成本不可得 → 渲染会话累计成本并如实命名。
    rows.push({
      key: "cost",
      label: t("tabBar.hoverCard.labelSessionCost"),
      value: formatMoneyLocalized(extra.sessionCost, extra.sessionCurrency),
    });
  }
  const turns = extra?.turns ?? 0;
  if (turns > 0) {
    rows.push({
      key: "turns",
      label: t("tabBar.hoverCard.labelTurns"),
      value: t(turns === 1 ? "history.turnOne" : "history.turnOther", { n: turns }),
    });
  }
  return rows;
}

/** 三区块模型。now 仅参与已耗时/相对时间换算（运行中由调用方每秒重算）。 */
export function tabHoverCardModel(tab: TabMeta, extra: TabHoverExtra | undefined, t: Translator, now: number): TabHoverCardSections {
  const identityRows: TabHoverCardRow[] = [];
  const scopeLabel = tab.scope === "global"
    ? t("tabBar.hoverCard.scopeGlobal")
    : (tab.workspaceName?.trim() || tab.workspacePath?.trim() || tab.workspaceRoot?.trim() || "");
  if (scopeLabel) {
    const branch = tab.gitBranch?.trim();
    identityRows.push({
      key: "workspace",
      label: t("tabBar.hoverCard.labelWorkspace"),
      value: branch ? `${scopeLabel} · ${branch}` : scopeLabel,
    });
  }
  const activityAt = extra?.lastActivityAt ?? 0;
  if (activityAt > 0) {
    const ageSeconds = Math.max(0, Math.round((now - activityAt) / 1000));
    identityRows.push({
      key: "activity",
      label: t("tabBar.hoverCard.labelLastActivity"),
      value: formatRelativeAgeLabel(ageSeconds, t),
    });
  }

  const face = runtimeFace(tab, t);
  const runtimeRows: TabHoverCardRow[] = [{ key: "state", label: t("tabBar.hoverCard.labelState"), value: face.label, tone: face.tone }];
  if (tab.running && !tab.pendingPrompt && tab.runtime?.issue?.code !== "session_missing" && tab.turnStartedAt) {
    const elapsedSeconds = Math.max(0, Math.round((now - tab.turnStartedAt) / 1000));
    runtimeRows.push({
      key: "elapsed",
      label: t("tabBar.hoverCard.labelElapsed"),
      value: formatDurationLabel(elapsedSeconds, t),
      tone: "running",
    });
  }
  const model = modelFace(tab, extra, t);
  if (model) {
    const effort = extra?.effortLabel?.trim();
    runtimeRows.push({
      key: "model",
      label: t("tabBar.hoverCard.labelModel"),
      value: effort ? `${model} · ${t("tabBar.hoverCard.effortValue", { effort })}` : model,
    });
  }
  const modes = modeFace(tab, t);
  if (modes) runtimeRows.push({ key: "modes", label: t("tabBar.hoverCard.labelModes"), value: modes });

  return {
    identityTitle: tabFullTitle(tab),
    identityRows,
    runtimeRows,
    consumptionRows: consumptionFace(extra, t),
    running: face.tone === "running",
  };
}
