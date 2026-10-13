// 任务 856 ①③：协作中心聊天视图。
//
// 通道两类（demo v2 ④，v2 返工口径）：「对话链（单聊）」= 会话↔会话协作消
// 息流（两端均为会话身份；数据源=信件留档按参与者对聚合的 ListCollabDirectChains
// 读口；只读回放——回复请直接打开对应对话或走派单，不经协作中心）与「群聊」
// = ChannelChatPanel 原样（802 复用面零回退）。
//
// ⑦ 消息右键跳转（v2 返工新增，纯前端）：对话链消息与群聊消息右键 →「打开
// 发信方会话 / 打开收信方会话」两项——经 openSessionFromCollabHub 模块信号，
// App.tsx 注册后调用既有「打开会话」导航；打开失败 toast（App 侧责任）。
//
// 用户 1138：文本截断处（链名/预览/群名）一律挂 title hover 提示。
//
// 409 观察窗（sessions 视图）按计划降级为本视图的辅助入口——「协作动态」
// 次级按钮弹出 CollabGroupPanel（embedded + sessionsOnly 形态）。刷新机制
// 零新增：各半边自管轮询与退避。

import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { app as appBridge } from "../lib/bridge";
import type { CollabDirectChainPairView, CollabDirectChainReadView } from "../lib/bridge";
import { rememberCollabContactNames, collabTitleFor } from "../lib/collabContactNames";
import { ChannelChatPanel, HUMAN_LOCAL } from "./ChannelChatPanel";
import { CollabGroupPanel } from "./CollabGroupPanel";
import { useT } from "../lib/i18n";

// ── ⑦ 打开会话的模块信号（App.tsx 注册；组件树经此发起导航）────────
const sessionJumpListeners = new Set<(contactId: string) => void>();

/** App.tsx 挂载时注册（contactId → 既有「打开会话」路由）。 */
export function onCollabChatOpenSession(cb: (contactId: string) => void): () => void {
  sessionJumpListeners.add(cb);
  return () => sessionJumpListeners.delete(cb);
}

/** ⑦ 右键菜单/列表动作发起：前台打开某会话的对话窗口。 */
export function openSessionFromCollabHub(contactId: string): void {
  for (const listener of sessionJumpListeners) listener(contactId);
}

type ChatChannelTab = "directs" | "groups";

export function CollabChatView() {
  const t = useT();
  const [tab, setTab] = useState<ChatChannelTab>("groups");
  // 409 观察窗降级形态：次级按钮弹出，关即卸载。
  const [activityOpen, setActivityOpen] = useState(false);

  // ── 对话链（单聊）通道状态 ──
  const [pairs, setPairs] = useState<CollabDirectChainPairView[] | null>(null);
  const [pairsError, setPairsError] = useState<string | null>(null);
  const [openPairId, setOpenPairId] = useState<string | null>(null);
  const [replay, setReplay] = useState<CollabDirectChainReadView | null>(null);
  const [replayError, setReplayError] = useState<string | null>(null);
  // ⑦ 右键菜单状态（对话链回放内；群聊菜单在 ChannelChatPanel 内自管）。
  const [msgMenu, setMsgMenu] = useState<{ x: number; y: number; from: string; to: string } | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  // 通讯录：对话链名渲染走共享缓存（collabTitleFor；会话名查不到降级截短 id，
  // 绝不渲染空白——320 同款口径）。此处拉一次目录喂缓存；右键跳转的可达性
  // 与导航由 App 侧注册的信号处理（既有「打开会话」路由自会校验）。
  useEffect(() => {
    let alive = true;
    appBridge.ListAddressableSessions()
      .then((rows) => { if (alive) rememberCollabContactNames(rows); })
      .catch(() => { /* 缓存为空 = 名字降级截短 id，不阻塞链列表 */ });
    return () => { alive = false; };
  }, []);

  const refreshPairs = useCallback(async () => {
    setPairsError(null);
    try {
      const snap = await appBridge.ListCollabDirectChains(100);
      setPairs(Array.isArray(snap?.pairs) ? snap.pairs : []);
    } catch (err) {
      setPairsError(String(err));
    }
  }, []);

  // 切到对话链通道即拉列表；群聊态不拉（零成本）。
  useEffect(() => {
    if (tab === "directs") void refreshPairs();
  }, [tab, refreshPairs]);

  // 打开一条链：拉双向回放。
  useEffect(() => {
    if (!openPairId) {
      setReplay(null);
      return;
    }
    let alive = true;
    setReplayError(null);
    appBridge.ReadCollabDirectChain(openPairId, 200)
      .then((view) => { if (alive) setReplay(view); })
      .catch((err) => { if (alive) { setReplay(null); setReplayError(String(err)); } });
    return () => { alive = false; };
  }, [openPairId]);

  const displayName = useCallback((contactId: string): string => collabTitleFor(contactId), []);

  const jumpTo = useCallback((contactId: string) => {
    if (contactId === HUMAN_LOCAL) {
      // human 端点不经协作中心（v2 口径）：对话链内按聚合口径不会出现，此处兜底。
      return;
    }
    openSessionFromCollabHub(contactId);
    setMsgMenu(null);
  }, []);

  const openPair = pairs?.find((p) => p.pairId === openPairId) ?? null;
  const pairTitle = (p: CollabDirectChainPairView) => `${displayName(p.a)} ↔ ${displayName(p.b)}`;

  // ⑦ Esc/点击外部关闭右键菜单。
  useEffect(() => {
    if (!msgMenu) return;
    const close = () => setMsgMenu(null);
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") close(); };
    document.addEventListener("click", close);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("click", close);
      document.removeEventListener("keydown", onKey);
    };
  }, [msgMenu]);

  return (
    <div className="collab-chat-view">
      <div className="collab-chat-view__tools">
        {/* 通道切换（demo v2 左栏工具行）：对话链（单聊）/ 群聊。 */}
        <div className="collab-chat-view__tabs" role="tablist" aria-label={t("collabHub.channelGroup")}>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "directs"}
            className={`collab-chat-view__tab${tab === "directs" ? " collab-chat-view__tab--on" : ""}`}
            onClick={() => { setTab("directs"); setOpenPairId(null); }}
          >
            {t("collabHub.channel.directs")}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "groups"}
            className={`collab-chat-view__tab${tab === "groups" ? " collab-chat-view__tab--on" : ""}`}
            onClick={() => setTab("groups")}
          >
            {t("collabHub.channel.groups")}
          </button>
        </div>
        <span className="collab-chat-view__spacer" aria-hidden="true" />
        <button
          type="button"
          className={`btn btn--small${activityOpen ? " collab-chat-view__activitybtn--on" : ""}`}
          aria-expanded={activityOpen}
          onClick={() => setActivityOpen((v) => !v)}
        >
          {t("collabHub.activity")}
        </button>
      </div>

      <div className="collab-chat-view__body">
        {tab === "groups" ? (
          <ChannelChatPanel onOpenSession={jumpTo} />
        ) : (
          <>
            {/* 左列表：对话链（单聊）段——会话↔会话协作消息流，只读回放。 */}
            <div className="collab-chat-view__dirlist">
              <div className="collab-chat-view__sect">{t("collabHub.directs.sect")}</div>
              {pairsError && (
                <div className="collab-chat-view__direrror" role="alert">
                  <span>{t("collabGroup.error.stale")}</span>
                  <button type="button" className="btn btn--secondary btn--small" onClick={() => void refreshPairs()}>
                    {t("collabGroup.retry")}
                  </button>
                </div>
              )}
              {(pairs?.length ?? 0) === 0 && !pairsError && (
                <div className="collab-chat-view__dirempty">{t("collabHub.directs.empty")}</div>
              )}
              {(pairs ?? []).map((p) => (
                <button
                  key={p.pairId}
                  type="button"
                  className={`collab-chat-view__diritem${openPairId === p.pairId ? " collab-chat-view__diritem--on" : ""}`}
                  onClick={() => setOpenPairId(p.pairId)}
                >
                  {/* 用户 1138：链名截断时 hover 提示完整链名。 */}
                  <span className="collab-chat-view__dirname" title={pairTitle(p)}>
                    {pairTitle(p)}
                  </span>
                  <span className="collab-chat-view__dirmeta">
                    <span className="collab-chat-view__dirpreview" title={p.preview || undefined}>{p.preview}</span>
                    <span className="collab-chat-view__dircount">{t("collabInbox.rounds", { n: p.count })}</span>
                  </span>
                </button>
              ))}
            </div>

            {/* 右侧：只读回放（无输入框）。 */}
            <div className="collab-chat-view__replay">
              {!openPair ? (
                <div className="collab-chat-view__placeholder">
                  {t("collabHub.directs.placeholder")}
                  <span className="collab-chat-view__placeholderhint">{t("collabHub.directs.hint")}</span>
                </div>
              ) : (
                <>
                  <div className="collab-chat-view__replayhead">
                    {/* 用户 1138：链名 hover 提示。 */}
                    <span className="collab-chat-view__replaytitle" title={pairTitle(openPair)}>
                      {t("collabHub.directs.replayTitle", { pair: pairTitle(openPair) })}
                    </span>
                    <span className="collab-chat-view__replaysub">{t("collabHub.directs.replaySub")}</span>
                  </div>
                  <div className="collab-chat-view__stream">
                    {replayError && (
                      <div className="collab-chat-view__direrror" role="alert">
                        <span>{replayError}</span>
                        <button type="button" className="btn btn--secondary btn--small" onClick={() => setOpenPairId((id) => id)}>
                          {t("collabGroup.retry")}
                        </button>
                      </div>
                    )}
                    {!replayError && (replay?.entries?.length ?? 0) === 0 && (
                      <div className="collab-chat-view__dirempty">{t("collabHub.directs.emptyStream")}</div>
                    )}
                    {(replay?.entries ?? []).map((e) => {
                      const onRight = e.from === openPair.b;
                      const other = onRight ? openPair.a : openPair.b;
                      return (
                        <div
                          key={e.id}
                          className={`collab-chat-view__msg${onRight ? " collab-chat-view__msg--right" : ""}`}
                          // ⑦ 右键跳转：消息携带收发双方（data-* 供菜单与测试）。
                          data-from={e.from}
                          data-to={e.to}
                          onContextMenu={(event) => {
                            event.preventDefault();
                            setMsgMenu({ x: event.clientX, y: event.clientY, from: e.from, to: e.to });
                          }}
                        >
                          <div className="collab-chat-view__msgmeta">
                            <span className="collab-chat-view__msgfrom" title={e.from}>{displayName(e.from)}</span>
                            <span className="collab-chat-view__typebadge">{t("collabGroup.chat.typeSession")}</span>
                            <span className="collab-chat-view__msgtime">{new Date(e.at).toLocaleString()}</span>
                          </div>
                          <div className="collab-chat-view__bubble" title={t("collabHub.directs.jumpHint")}>{e.preview}</div>
                          <span className="collab-chat-view__msgpeer">→ {displayName(other)}</span>
                        </div>
                      );
                    })}
                  </div>
                  <div className="collab-chat-view__readonly">
                    {t("collabHub.directs.readonly")} <b>{t("collabHub.directs.readonlyHint")}</b> {t("collabHub.directs.jumpTip")}
                  </div>
                </>
              )}
            </div>
          </>
        )}
      </div>

      {activityOpen && (
        <div className="collab-chat-view__activity" role="region" aria-label={t("collabGroup.title")}>
          <div className="collab-chat-view__activityhead">
            <span className="collab-chat-view__activitytitle">{t("collabHub.activity")}</span>
            <button type="button" className="btn btn--small" onClick={() => setActivityOpen(false)}>
              {t("collabHub.closeActivity")}
            </button>
          </div>
          <CollabGroupPanel embedded sessionsOnly />
        </div>
      )}

      {/* ⑦ 对话链消息右键菜单：两项跳转（demo v2 返工口径）。 */}
      {msgMenu && createPortal(
        <div className="collab-chat-view__ctx" style={{ left: msgMenu.x, top: msgMenu.y }} role="menu" ref={menuRef}>
          <div className="collab-chat-view__ctxhead">
            {t("collabHub.directs.ctxHead", { pair: `${displayName(msgMenu.from)} ↔ ${displayName(msgMenu.to)}` })}
          </div>
          <button type="button" role="menuitem" onClick={() => jumpTo(msgMenu.from)}>
            {t("collabHub.directs.ctxSender", { name: displayName(msgMenu.from) })}
          </button>
          <button type="button" role="menuitem" onClick={() => jumpTo(msgMenu.to)}>
            {t("collabHub.directs.ctxRecipient", { name: displayName(msgMenu.to) })}
          </button>
        </div>,
        document.body,
      )}
    </div>
  );
}
