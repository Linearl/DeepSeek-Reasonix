// wt-zcode-288：「全部已读」共享层（tab / 项目分组 / 会话三处右键菜单共用）。
//
// 未读徽章的数据源：ProjectTree 按 localStorage["projectTree:readActivity"]
// （readActivity 存档，key 为 `scope\x1froot\x1ftopicId`，值为已读时间戳）比对
// 节点的最后活动时间得出未读点；组件内存态由 ProjectTree 持有。本模块提供：
//   1. 纯函数 —— 给定一批 readActivity key，产出「全部标记已读」的下一个存档；
//   2. 持久化 —— 写回 localStorage 并广播变更事件，ProjectTree 监听后重载，
//      让 TabBar 这类树外组件标记已读后侧栏未读点立即清零。
// 判定口径见 projectTreeTopicHasUnreadActivity：readAt ≥ 节点最后活动时间即已读。
import { asArray } from "./array";
import { isRuntimeSessionNode, isTopicNode, projectTreeReadActivityKey, projectTreeTopicOpenRequest, type ProjectTreeReadActivity } from "./projectTreeTopic";
import type { ProjectNode, TabMeta } from "./types";

export const READ_ACTIVITY_STORAGE_KEY = "projectTree:readActivity";
// ProjectTree 监听的自定义事件名（window 级，跨组件通知用）。
export const READ_ACTIVITY_CHANGED_EVENT = "projecttree:readactivity-changed";

export function loadReadActivityStore(): ProjectTreeReadActivity {
  try {
    if (typeof localStorage === "undefined") return {};
    const raw = localStorage.getItem(READ_ACTIVITY_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Record<string, unknown>;
    const out: ProjectTreeReadActivity = {};
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === "number" && Number.isFinite(value)) out[key] = value;
    }
    return out;
  } catch {
    return {};
  }
}

// 写回存档并广播（localStorage 不可用时只跳过持久化，事件照发）。
export function persistReadActivity(next: ProjectTreeReadActivity) {
  try {
    localStorage.setItem(READ_ACTIVITY_STORAGE_KEY, JSON.stringify(next));
  } catch {
    /* localStorage unavailable */
  }
  if (typeof window !== "undefined" && typeof window.dispatchEvent === "function") {
    window.dispatchEvent(new CustomEvent(READ_ACTIVITY_CHANGED_EVENT));
  }
}

// 纯函数：把这批 key 全部标记为已读（readAt 取 max(现有值, now)）。
// 没有任何变化时返回原引用，调用方可以借此跳过 setState/持久化。
export function markReadKeysRead(readActivity: ProjectTreeReadActivity, keys: string[], now = Date.now()): ProjectTreeReadActivity {
  let changed = false;
  const next = { ...readActivity };
  for (const key of keys) {
    if (!key) continue;
    if ((next[key] ?? 0) < now) {
      next[key] = now;
      changed = true;
    }
  }
  return changed ? next : readActivity;
}

// 浅比较两份存档，供 ProjectTree 的事件监听去抖（等值时保持原引用避免多余渲染）。
export function readActivityEquals(a: ProjectTreeReadActivity, b: ProjectTreeReadActivity): boolean {
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  return ka.every((key) => a[key] === b[key]);
}

// 收集子树内全部会话/话题节点的 readActivity key —— 项目分组右键「全部已读」
// 的「当前上下文」就是该分组（项目 / Global 文件夹）子树。
export function readActivityKeysInSubtree(nodes: ProjectNode[]): string[] {
  const keys: string[] = [];
  const walk = (list: ProjectNode[]) => {
    for (const node of list) {
      if (!node) continue;
      if (isTopicNode(node) || isRuntimeSessionNode(node)) {
        const key = projectTreeReadActivityKey(node);
        if (key) keys.push(key);
      }
      walk(asArray(node.children));
    }
  };
  walk(nodes);
  return keys;
}

// 收集与 (scope, workspaceRoot) 同工作区的全部 key —— 会话行右键「全部已读」
// 的「当前上下文」是该会话所属工作区（Global 会话则为整个 Global 区）。
export function readActivityKeysInScope(nodes: ProjectNode[], scope: string, workspaceRoot: string): string[] {
  const keys: string[] = [];
  const walk = (list: ProjectNode[]) => {
    for (const node of list) {
      if (!node) continue;
      if (isTopicNode(node) || isRuntimeSessionNode(node)) {
        const request = projectTreeTopicOpenRequest(node);
        if (request && request.scope === scope && request.workspaceRoot === workspaceRoot) {
          const key = projectTreeReadActivityKey(node);
          if (key) keys.push(key);
        }
      }
      walk(asArray(node.children));
    }
  };
  walk(nodes);
  return keys;
}

// 标签页 → readActivity key（file 标签或无 topicId 时为 null）。tab 侧的
// scope 取值与树节点一致："global" 之外一律按 project 折算。
export function readActivityKeyFromTab(tab: Pick<TabMeta, "scope" | "workspaceRoot" | "topicId">): string | null {
  const topicId = tab.topicId?.trim() ?? "";
  if (!topicId) return null;
  const scope = tab.scope === "global" ? "global" : "project";
  return [scope, scope === "global" ? "" : tab.workspaceRoot ?? "", topicId].join("\u001f");
}

// TabBar「全部已读」入口：把打开的标签页全部标记已读（读档 → 标记 → 写回广播）。
export function markTabsAllRead(tabs: Pick<TabMeta, "scope" | "workspaceRoot" | "topicId">[], now = Date.now()) {
  const keys = tabs
    .map(readActivityKeyFromTab)
    .filter((key): key is string => Boolean(key));
  persistReadActivity(markReadKeysRead(loadReadActivityStore(), keys, now));
}
