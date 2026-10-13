type ComposerDraftKeyTab = {
  id?: string;
  scope?: string;
  workspaceRoot?: string;
  topicId?: string;
  sessionPath?: string;
};

export function composerDraftKeyForTab(activeTab: ComposerDraftKeyTab | undefined, activeTabId?: string | null): string {
  if (!activeTab) return activeTabId ?? "";
  const scope = activeTab.scope === "project" ? "project" : "global";
  const workspaceRoot = scope === "project" ? activeTab.workspaceRoot || "" : "";
  const topicId = activeTab.topicId || "";
  const sessionPath = activeTab.sessionPath || "";
  if (topicId) {
    return ["session-topic", scope, workspaceRoot, topicId].join("\u0000");
  }
  if (sessionPath) {
    return ["session-path", scope, workspaceRoot, sessionPath].join("\u0000");
  }
  return ["tab", activeTab.id || activeTabId || ""].join("\u0000");
}

// 任务 825：hydrate 完成前草稿 key 的两种未就绪形态——tab 壳已恢复时是
// "tab\0<tabId>"，壳未恢复时退化为裸 tabId（composerDraftKeyForTab 第一行）。
// bareTabIdFromDraftKey 从未就绪 key 中取回 tabId；已就绪的 session key 与
// 空 key 返回 null。
export function bareTabIdFromDraftKey(key: string): string | null {
  if (key.startsWith("tab\u0000")) return key.slice("tab\u0000".length);
  if (key !== "" && !key.includes("\u0000")) return key;
  return null;
}

export function isSessionScopedDraftKey(key: string): boolean {
  return key.startsWith("session-topic\u0000") || key.startsWith("session-path\u0000");
}

// 任务 839：找回槽的持久化 key。与草稿同存一张 localStorage 表（composer:drafts:v1），
// 前缀 "parked\0" 保证任何草稿读取路径都不会按 session key 误命中；找回动作把
// 被换下的草稿落盘到这个 key，重启后仍能再次找回。
export function parkedStartupDraftStorageKey(draftKey: string): string {
  return draftKey ? `parked\u0000${draftKey}` : "";
}
