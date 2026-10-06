// 任务 552:标签页概览面板的数据模型——「最近关闭」栈与搜索面构建。
//
// fork 侧数据源结论(实施首日已核实):
//   - 后端 TabMeta(desktop/tab_meta.go)没有 tabType/filePath 字段,scope 只有
//     "project" | "global",即所有 tab 都是会话 tab,会话文件始终持久存在;
//   - 后端没有「最近关闭」记录(grep reopenClosed/recentClosed = 0 命中),
//     因此最近关闭栈是纯前端内存态(不持久化,重启即清,与 zcode 一致);
//   - 重开 = OpenTopicSession(scope, workspaceRoot, topicId, sessionPath),
//     前端走既有 enqueueNavigation({ kind: "topic", ... }),无需新增后端。
//
// 搜索面按「用户手里可能持有什么」设计(对齐 zcode sidePaneTabPresentation.ts
// 的意图):标题之外,工作区名、workspaceRoot 路径、sessionPath(含文件名里的
// 会话 id)、topicId 都进 hint——排查时手里往往只有这些标识。

import type { TabMeta } from "./types";

/** 最近关闭栈上限(对齐 zcode 的 RECENT_CLOSED_SIDE_PANE_TAB_LIMIT = 8)。 */
export const RECENT_CLOSED_TAB_LIMIT = 8;

export interface RecentClosedTab {
  tab: TabMeta;
  /** 关闭时刻(Unix 毫秒)。 */
  closedAt: number;
}

/**
 * tab 的重开身份:OpenTopicSession 的重开四元组。
 * 「重开后再关闭不产生重复」「重开后最近关闭条目自动消失」都按它判定。
 */
export function tabReopenIdentity(tab: Pick<TabMeta, "scope" | "workspaceRoot" | "topicId" | "sessionPath">): string {
  return [tab.scope, tab.workspaceRoot, tab.topicId, tab.sessionPath ?? ""].join("\u0000");
}

/**
 * 入栈:同身份去重后置顶,超出上限从尾部丢弃。
 * 纯函数,返回新数组;调用方(Acc.tsx)在关闭成功后调用。
 */
export function pushRecentClosedTab(
  list: RecentClosedTab[],
  tab: TabMeta,
  closedAt: number,
  limit: number = RECENT_CLOSED_TAB_LIMIT,
): RecentClosedTab[] {
  const identity = tabReopenIdentity(tab);
  const next: RecentClosedTab[] = [{ tab, closedAt }];
  for (const entry of list) {
    if (tabReopenIdentity(entry.tab) === identity) continue;
    next.push(entry);
  }
  return next.slice(0, limit);
}

/** 打开的 tab 集合变化后调用:已被重开的条目从最近关闭里消失。 */
export function pruneRecentClosedTabs(list: RecentClosedTab[], openTabs: TabMeta[]): RecentClosedTab[] {
  const openIdentities = new Set(openTabs.map(tabReopenIdentity));
  return list.filter((entry) => !openIdentities.has(tabReopenIdentity(entry.tab)));
}

/**
 * 面板展示标题。语义与 TabBar.tsx 的 tabDisplayTitle 一致(后端 scope 只有
 * project/global,但保留 file 分支以防前端伪造的 tab 形态)。
 */
export function tabOverviewTitle(tab: TabMeta): string {
  if (tab.tabType === "file" || tab.scope === "file") {
    return tab.topicTitle?.trim() || tab.filePath?.split("/").filter(Boolean).pop() || "File";
  }
  const title = tab.topicTitle?.trim();
  if (tab.scope === "global") return title || "Global";
  return title || "Untitled";
}

/** 类型标签(排序最末档的加权面):项目会话 / 全局会话。 */
export function tabOverviewTypeLabel(tab: TabMeta, projectLabel: string, globalLabel: string): string {
  if (tab.tabType === "file" || tab.scope === "file") return projectLabel;
  return tab.scope === "global" ? globalLabel : projectLabel;
}

/**
 * 搜索 hint:排查标识面——workspace 名/根路径、sessionPath(文件名里通常
 * 就是会话 id)、topicId。标题之外用户最可能拿在手里的一串。
 */
export function tabOverviewSearchHint(tab: TabMeta): string {
  return [tab.workspaceName ?? "", tab.workspaceRoot ?? "", tab.sessionPath ?? "", tab.topicId ?? ""]
    .map((part) => part.trim())
    .filter(Boolean)
    .join(" ");
}
