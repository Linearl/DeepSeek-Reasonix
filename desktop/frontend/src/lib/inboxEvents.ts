export type InboxChangedEvent = {
  tabId: string;
  sessionPath?: string;
  revision?: number;
};

export function onInboxChanged(cb: (event: InboxChangedEvent) => void): () => void {
  if (typeof window !== "undefined" && window.runtime && window.go?.main?.App) {
    return window.runtime.EventsOn("InboxChanged", (payload?: unknown) => {
      const tabId = payload && typeof payload === "object" && "tabId" in payload
        ? String((payload as { tabId?: unknown }).tabId ?? "")
        : "";
      const sessionPath = payload && typeof payload === "object" && "sessionPath" in payload
        ? String((payload as { sessionPath?: unknown }).sessionPath ?? "")
        : undefined;
      const rawRevision = payload && typeof payload === "object" && "revision" in payload
        ? Number((payload as { revision?: unknown }).revision)
        : NaN;
      cb({ tabId, sessionPath, revision: Number.isFinite(rawRevision) ? rawRevision : undefined });
    });
  }
  return () => {};
}

// tl1226 遗留①（新信到达角标即涨）：后端投递观察者发的 CollabMailChanged
// 运行时事件（desktop/collab_mail_arrival.go）——未读角标 hook 订阅后即时重查
// CountUnreadCollabMail。缺 window.runtime/go 时（测试 harness、旧宿主）退化为
// 空订阅，与 onInboxChanged 同款守卫。
export type CollabMailChangedEvent = {
  id?: string;
  from?: string;
  to?: string;
};

export function onCollabMailChanged(cb: (event: CollabMailChangedEvent) => void): () => void {
  if (typeof window !== "undefined" && window.runtime && window.go?.main?.App) {
    return window.runtime.EventsOn("CollabMailChanged", (payload?: unknown) => {
      const read = (key: string): string | undefined =>
        payload && typeof payload === "object" && key in payload
          ? String((payload as { [k: string]: unknown })[key] ?? "") || undefined
          : undefined;
      cb({ id: read("id"), from: read("from"), to: read("to") });
    });
  }
  return () => {};
}
