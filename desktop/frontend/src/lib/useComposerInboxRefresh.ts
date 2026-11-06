import { useCallback, useEffect, useLayoutEffect, useRef } from "react";
import { app } from "./bridge";
import {
  guidanceFromInboxSnapshot,
  hydrateEmptyGuidancePreviews,
  inboxSnapshotBelongsToScope,
  localGuidanceFallback,
  mergeGuidanceSnapshot,
} from "./composerInboxQueue";
import { onInboxChanged } from "./inboxEvents";
import type { PendingGuidance } from "../components/ComposerGuidanceShelf";

export function useComposerInboxRefresh(
  tabId: string | undefined,
  draftKey: string,
  guidanceDraftKey: string,
  inboxSessionKey: string,
  previewKey: readonly string[],
  retryNonce: number,
  running: boolean,
  applyQueue: (items: PendingGuidance[]) => void,
  collapse: () => void,
  bump: () => void,
  runtimeRevision?: number,
  // tl1465 ②：群聊 tab 的 Composer（channelMode）没有所属会话——tabId 空会
  // 落到 InboxSnapshot("")=Go 侧活动会话的收件箱，把那个会话的引导消息渲染
  // 进群聊 tab。enabled=false = 结构性不拉队列（队列恒空，引导架不挂），
  // 而非渲染层藏。
  enabled = true,
) {
  const inboxSessionKeyRef = useRef(inboxSessionKey);
  const appliedRevisionRef = useRef<{ scope: string; revision: number }>({ scope: inboxSessionKey, revision: -1 });
  const clearQueue = useCallback(() => applyQueue([]), [applyQueue]);
  useLayoutEffect(() => {
    if (inboxSessionKeyRef.current === inboxSessionKey) return;
    inboxSessionKeyRef.current = inboxSessionKey;
    appliedRevisionRef.current = { scope: inboxSessionKey, revision: -1 };
    clearQueue();
  }, [draftKey, inboxSessionKey, clearQueue]);
  useEffect(() => onInboxChanged((changed) => {
    if (!enabled) return;
    if (changed.tabId && tabId && changed.tabId !== tabId) return;
    if (!inboxSnapshotBelongsToScope(changed.sessionPath, inboxSessionKey)) return;
    if (changed.revision !== undefined
      && appliedRevisionRef.current.scope === inboxSessionKey
      && changed.revision <= appliedRevisionRef.current.revision) return;
    bump();
  }), [tabId, inboxSessionKey, bump, enabled]);
  useEffect(() => {
    if (!enabled) {
      clearQueue();
      return;
    }
    if (guidanceDraftKey !== draftKey) return;
    let live = true;
    const fallback = localGuidanceFallback(previewKey);
    if (typeof app.InboxSnapshot !== "function") {
      applyQueue(fallback);
      collapse();
      return;
    }
    void app.InboxSnapshot(tabId || "").then((snap) => {
      if (!live || !inboxSnapshotBelongsToScope(snap?.sessionPath, inboxSessionKey)) return;
      const rawRevision = Number(snap?.revision ?? -1);
      const revision = Number.isFinite(rawRevision) ? rawRevision : -1;
      if (appliedRevisionRef.current.scope === inboxSessionKey && revision < appliedRevisionRef.current.revision) return;
      appliedRevisionRef.current = { scope: inboxSessionKey, revision };
      const durable = guidanceFromInboxSnapshot(snap);
      applyQueue(mergeGuidanceSnapshot(durable, fallback));
      collapse();
      void hydrateEmptyGuidancePreviews(durable, (id) => app.ReadInboxItem(tabId || "", id)).then((hydrated) => {
        if (live && hydrated.some((item) => item.text.trim())) {
          applyQueue(mergeGuidanceSnapshot(hydrated, fallback));
        }
      });
    }).catch(() => {
      if (live) applyQueue(localGuidanceFallback(previewKey));
    });
    return () => { live = false; };
  }, [draftKey, guidanceDraftKey, previewKey, running, tabId, retryNonce, inboxSessionKey, applyQueue, collapse, runtimeRevision, enabled, clearQueue]);
}
