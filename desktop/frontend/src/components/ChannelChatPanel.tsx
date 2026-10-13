// 任务 802 — 频道消息 tab 的微信式群聊视图（人机混合真群聊）。
//
// 三条口径（20261013 用户重申，收口验收面）：
// ① 渲染与钉钉群聊一致：回执（delivered）不显示、已读（read）显示——投递
//    途中的状态是系统噪音，只有「谁已读」对人类有信息量；失败行是诚实错误
//    仍显示。标注只出现在自己的消息上（别人的消息不渲染已读回执）。
// ② 与信件中心去重：本视图只承载 channel 真实消息流（ChannelMessagesForHuman
//    读口），不做收件箱复刻；预览走 ReadCollabChannel（409 读口，不结算 human
//    已读——列表预览不能吞掉未读语义）。
// ③ 使用门槛：真正创建/加入的群聊才有记录；空频道与空目录渲染诚实空态，
//    dev mock 同样不造假数据。
//
// 刷新机制（④）：复用 409 的刷新信号——开面板期间 30s 轮询（同一
// COLLAB_GROUP_POLL_MS 常量）+ 手动刷新 + 2/4/8/15s 失败退避（任务587 同款：
// 失败≠空态，旧数据优先 + 显式错误条）。打开中的频道每 tick 拉尾窗
// （Messages(sinceMs=0, limit=50)，SQL 语义=最新 50 条），已读标注因此每
// tick 全新——这是验收第 3 步「送达已读逐成员正确」的硬前提；未打开频道
// 零消息拉取，列表未读用 messages 总数差（localStorage 记账）。

import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { app as appBridge } from "../lib/bridge";
import type { CollabSessionDirectoryRow } from "./CollabInboxPanel";
import {
  COLLAB_GROUP_POLL_MS,
  collabGroupRetryDelaysMs,
} from "./CollabGroupPanel";
import type {
  CollabChannelCancelResultView,
  CollabChannelMemberView,
  CollabChannelMessageView,
  CollabChannelView,
} from "../lib/bridge";
import { useT } from "../lib/i18n";

/** 803 定稿：桌面端单用户固定身份（collabchannel.HumanLocal）。 */
export const HUMAN_LOCAL = "human:local";

const MESSAGE_TAIL_LIMIT = 50;
const PREVIEW_LIMIT = 1;
const PREVIEW_MAX_CHANNELS = 24;

/** 未读记账的 localStorage 键（channelId → 上次已见的消息总数）。 */
const LAST_SEEN_STORE_KEY = "reasonix.channelChat.lastSeenCounts";

export type ChannelChatBindings = {
  ListCollabChannels(): Promise<CollabChannelView[]>;
  /** 预览专用：409 读口（不结算 human 已读），取每群最新 1 条做列表预览。 */
  ReadCollabChannel(ref: string, limit: number): Promise<{ channel: CollabChannelView; messages: CollabChannelMessageView[] }>;
  ChannelMessagesForHuman(ref: string, sinceMs: number, limit: number): Promise<{ channel: CollabChannelView; messages: CollabChannelMessageView[] }>;
  ChannelCreateForHuman(name: string, topic: string, hourlyLimit: number): Promise<CollabChannelView>;
  ChannelLeaveHuman(ref: string): Promise<void>;
  ChannelSendFromHuman(ref: string, body: string): Promise<CollabChannelMessageView>;
  ChannelInvite(ref: string, contactIDs: string[]): Promise<CollabChannelView>;
  ChannelRemoveMember(ref: string, contactID: string): Promise<CollabChannelView>;
  ChannelCancelFromHuman(ref: string, messageID: string): Promise<CollabChannelCancelResultView>;
  ListAddressableSessions(): Promise<CollabSessionDirectoryRow[]>;
};

function readLastSeenCounts(): Record<string, number> {
  try {
    const raw = window.localStorage.getItem(LAST_SEEN_STORE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !(parsed instanceof Array)) {
      const out: Record<string, number> = {};
      for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
        const n = Number(v);
        if (k && Number.isFinite(n) && n >= 0) out[k] = Math.trunc(n);
      }
      return out;
    }
  } catch {
    // 损坏的记账按全新处理（首见即初始化），绝不阻塞 UI。
  }
  return {};
}

function writeLastSeenCounts(counts: Record<string, number>): void {
  try {
    window.localStorage.setItem(LAST_SEEN_STORE_KEY, JSON.stringify(counts));
  } catch {
    // 私密模式等存储失败只影响未读徽标，不阻塞聊天。
  }
}

function formatTime(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleString();
}

function shortId(id: string): string {
  return id.length > 18 ? `${id.slice(0, 17)}…` : id;
}

export function ChannelChatPanel({ bindings, onOpenSession }: { bindings?: ChannelChatBindings; onOpenSession?: (contactId: string) => void } = {}) {
  const t = useT();
  const b: ChannelChatBindings = bindings ?? appBridge;

  const [channels, setChannels] = useState<CollabChannelView[] | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [chat, setChat] = useState<{ channel: CollabChannelView; messages: CollabChannelMessageView[] } | null>(null);
  const [previews, setPreviews] = useState<Record<string, CollabChannelMessageView | undefined>>({});
  const [directory, setDirectory] = useState<CollabSessionDirectoryRow[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [invitePicked, setInvitePicked] = useState<string[]>([]);
  const [inviteQuery, setInviteQuery] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createTopic, setCreateTopic] = useState("");
  const [createLimit, setCreateLimit] = useState("");
  // 任务 856 ⑦：右键菜单扩展——own 消息=撤回（既有）；他人消息=打开发信方
  // 会话（onOpenSession 注入）+ 收信方（群）提示项。
  const [menu, setMenu] = useState<{ messageId: string; sender: string; x: number; y: number } | null>(null);

  // refs：轮询闭包里要读「当前打开的群」与最新退避状态，不作为渲染依赖。
  const openIdRef = useRef<string | null>(null);
  const retryRef = useRef<{ timer: ReturnType<typeof setTimeout> | null; attempt: number }>({ timer: null, attempt: 0 });
  const refreshRef = useRef<() => void>(() => {});
  const lastSeenRef = useRef<Record<string, number> | null>(null);

  const cancelRetry = useCallback(() => {
    if (retryRef.current.timer !== null) {
      clearTimeout(retryRef.current.timer);
      retryRef.current.timer = null;
    }
  }, []);

  const scheduleRetry = useCallback(() => {
    if (retryRef.current.timer !== null) return;
    const delays = collabGroupRetryDelaysMs;
    const delay = delays[Math.min(retryRef.current.attempt, delays.length - 1)];
    retryRef.current.timer = setTimeout(() => {
      retryRef.current.timer = null;
      retryRef.current.attempt += 1;
      refreshRef.current();
    }, delay);
  }, []);

  const markSeen = useCallback((channelId: string, count: number) => {
    if (!lastSeenRef.current) lastSeenRef.current = readLastSeenCounts();
    if (lastSeenRef.current[channelId] === count) return;
    lastSeenRef.current[channelId] = count;
    writeLastSeenCounts(lastSeenRef.current);
  }, []);

  const refresh = useCallback(async () => {
    setLoadError(null);
    try {
      const list = await b.ListCollabChannels();
      setChannels(list);
      // 首见的群按「已见当前条数」初始化记账——使用门槛口径：没有记录的
      // 老频道不凭空冒未读红点。
      if (!lastSeenRef.current) lastSeenRef.current = readLastSeenCounts();
      let seenChanged = false;
      for (const c of list) {
        if (!(c.id in lastSeenRef.current)) {
          lastSeenRef.current[c.id] = c.messages;
          seenChanged = true;
        }
      }
      if (seenChanged) writeLastSeenCounts(lastSeenRef.current);

      // 打开中的频道：拉尾窗（最新 50 条），已读标注每 tick 全新；
      // 打开群的消息即视为已见（UI 可见即已读，803 ⑥在侧）。
      const opened = openIdRef.current;
      if (opened) {
        try {
          const tail = await b.ChannelMessagesForHuman(opened, 0, MESSAGE_TAIL_LIMIT);
          setChat(tail);
          markSeen(opened, tail.channel.messages);
        } catch (err) {
          // 频道读取失败不拖垮群列表——保留旧尾窗，错误条如实声明。
          setActionError(String(err));
        }
      }
      // 列表预览：每群最新 1 条（409 读口，不结算已读，保未读语义）。
      const previewTargets = list.slice(0, PREVIEW_MAX_CHANNELS);
      const nextPreviews: Record<string, CollabChannelMessageView | undefined> = {};
      await Promise.all(previewTargets.map(async (c) => {
        try {
          const preview = await b.ReadCollabChannel(c.id, PREVIEW_LIMIT);
          nextPreviews[c.id] = preview.messages[preview.messages.length - 1];
        } catch {
          nextPreviews[c.id] = undefined; // 预览失败只是少一行预览，不是错误
        }
      }));
      setPreviews(nextPreviews);
      retryRef.current.attempt = 0;
      cancelRetry();
    } catch (err) {
      setLoadError(String(err));
      scheduleRetry();
    }
  }, [b, cancelRetry, markSeen, scheduleRetry]);

  useEffect(() => {
    refreshRef.current = () => void refresh();
  }, [refresh]);

  // 409 同款：挂载即拉 + 30s 轮询；卸载（切 tab/关面板）即停，零成本。
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, COLLAB_GROUP_POLL_MS);
    return () => {
      window.clearInterval(timer);
      cancelRetry();
    };
  }, [refresh, cancelRetry]);

  // 通讯录挂载即加载一次（发送方名牌用目录标题；详情页邀请模式同源复用）。
  const ensureDirectory = useCallback(async () => {
    if (directory) return;
    try {
      setDirectory(await b.ListAddressableSessions());
    } catch {
      setDirectory([]); // 失败按空目录：名牌回落 contact_id，邀请列表为空
    }
  }, [b, directory]);

  useEffect(() => { void ensureDirectory(); }, [ensureDirectory]);

  const openChannel = useCallback((id: string) => {
    setOpenId(id);
    openIdRef.current = id;
    setChat(null);
    setActionError(null);
    setDetailOpen(false);
    setInviteOpen(false);
    void refresh();
  }, [refresh]);

  const closeChannel = useCallback(() => {
    setOpenId(null);
    openIdRef.current = null;
    setChat(null);
    setDetailOpen(false);
    setInviteOpen(false);
  }, []);

  const send = useCallback(async () => {
    const body = draft.trim();
    const ref = openIdRef.current;
    if (!body || !ref || sending) return;
    setSending(true);
    setActionError(null);
    try {
      const msg = await b.ChannelSendFromHuman(ref, body);
      setDraft("");
      // 乐观上屏 + 立刻对账（refresh 同时刷新群列表计数与自己的未读记账）。
      setChat((prev) => {
        if (!prev || (prev.channel.id !== ref && prev.channel.name !== ref)) return prev;
        if (prev.messages.some((m) => m.id === msg.id)) return prev;
        return { ...prev, messages: [...prev.messages, msg] };
      });
      await refresh();
    } catch (err) {
      setActionError(String(err)); // 小时上限等风暴治理错误如实透出
    } finally {
      setSending(false);
    }
  }, [b, draft, refresh, sending]);

  const cancelMessage = useCallback(async (messageId: string) => {
    const ref = openIdRef.current;
    if (!ref) return;
    setMenu(null);
    setActionError(null);
    try {
      await b.ChannelCancelFromHuman(ref, messageId);
      setChat((prev) => prev
        ? { ...prev, messages: prev.messages.map((m) => (m.id === messageId ? { ...m, cancelledAt: Date.now() } : m)) }
        : prev);
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, refresh]);

  const leave = useCallback(async () => {
    const ref = openIdRef.current;
    if (!ref) return;
    setActionError(null);
    try {
      await b.ChannelLeaveHuman(ref);
      closeChannel();
      await refresh();
    } catch (err) {
      // 群主退群被拒（803：最后一个群主退出拒绝）——文案原样透出。
      setActionError(String(err));
    }
  }, [b, closeChannel, refresh]);

  const removeMember = useCallback(async (contactId: string) => {
    const ref = openIdRef.current;
    if (!ref) return;
    setActionError(null);
    try {
      const updated = await b.ChannelRemoveMember(ref, contactId);
      setChat((prev) => (prev && (prev.channel.id === updated.id || prev.channel.name === updated.name) ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, refresh]);

  const invite = useCallback(async () => {
    const ref = openIdRef.current;
    if (!ref || invitePicked.length === 0) return;
    setActionError(null);
    try {
      const updated = await b.ChannelInvite(ref, invitePicked);
      setInviteOpen(false);
      setInvitePicked([]);
      setInviteQuery("");
      setChat((prev) => (prev && (prev.channel.id === updated.id || prev.channel.name === updated.name) ? { ...prev, channel: updated } : prev));
      await refresh();
    } catch (err) {
      setActionError(String(err));
    }
  }, [b, invitePicked, refresh]);

  const create = useCallback(async () => {
    const name = createName.trim();
    if (!name) return;
    setActionError(null);
    try {
      const limit = Number.parseInt(createLimit, 10);
      const created = await b.ChannelCreateForHuman(name, createTopic.trim(), Number.isFinite(limit) && limit > 0 ? limit : 0);
      setCreateOpen(false);
      setCreateName("");
      setCreateTopic("");
      setCreateLimit("");
      openChannel(created.id || created.name);
    } catch (err) {
      setActionError(String(err));
    }
  }, [createLimit, createName, createTopic, openChannel]);

  const directoryTitle = useCallback((contactId: string): string => {
    const row = directory?.find((r) => r.contactId === contactId);
    return row?.title || row?.purpose || contactId;
  }, [directory]);

  if (!channels && !loadError) {
    return <div className="channel-chat channel-chat--loading">{t("collabGroup.loading")}</div>;
  }

  const openCh = chat?.channel ?? channels?.find((c) => c.id === openId || c.name === openId) ?? null;
  const messages = chat?.messages ?? [];
  const iAmOwner = Boolean(openCh?.ownerId && openCh.ownerId === HUMAN_LOCAL);
  const hasOwner = Boolean(openCh?.ownerId); // 无主老群：隐藏全部管理入口（803 需知③）
  const memberDetails: CollabChannelMemberView[] = openCh?.memberDetails
    ?? (openCh?.members ?? []).map((contact) => ({ contact, type: "session", joinedAt: 0 }));

  const lastSeenNow = lastSeenRef.current ?? {};
  const unreadOf = (c: CollabChannelView): number =>
    Math.max(0, c.messages - (lastSeenNow[c.id] ?? c.messages));

  const inviteCandidates = (directory ?? [])
    .filter((row) => !memberDetails.some((m) => m.contact === row.contactId))
    .filter((row) => {
      const q = inviteQuery.trim().toLowerCase();
      if (!q) return true;
      return `${row.title ?? ""} ${row.purpose ?? ""} ${row.contactId}`.toLowerCase().includes(q);
    });

  return (
    <div className="channel-chat" aria-label={t("collabGroup.chat.title")}>
      {/* 左侧：群列表（名称/未读/最后一条预览/群主标） */}
      <div className="channel-chat__list">
        <div className="channel-chat__listhead">
          <span className="channel-chat__listtitle">{t("collabGroup.chat.title")}</span>
          <button type="button" className="btn btn--small" onClick={() => { setCreateOpen(true); setActionError(null); }}>
            {t("collabGroup.chat.create")}
          </button>
        </div>
        {loadError && (
          <div className="channel-chat__errorstrip" role="alert">
            <span>{t("collabGroup.error.stale")}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>{t("collabGroup.retry")}</button>
          </div>
        )}
        {(channels?.length ?? 0) === 0 && !loadError && (
          <div className="channel-chat__empty">{t("collabGroup.chat.empty")}</div>
        )}
        {(channels ?? []).map((c) => {
          const unread = unreadOf(c);
          const preview = previews[c.id];
          return (
            <button
              key={c.id}
              type="button"
              className={`channel-chat__item${openId === c.id ? " channel-chat__item--on" : ""}`}
              onClick={() => openChannel(c.id)}
            >
              <span className="channel-chat__itemname" title={c.name}>
                {c.name}
                {c.ownerId && (
                  <span className={`channel-chat__ownerbadge${c.ownerId === HUMAN_LOCAL ? " channel-chat__ownerbadge--me" : ""}`}>
                    {t("collabGroup.chat.ownerBadge")}
                  </span>
                )}
                {unread > 0 && <span className="channel-chat__unread">{unread > 99 ? "99+" : unread}</span>}
              </span>
              <span className="channel-chat__itempreview" title={preview ? (preview.cancelledAt ? t("collabGroup.chat.recalled", { sender: preview.sender }) : `${preview.sender}: ${preview.body}`) : undefined}>
                {preview
                  ? (preview.cancelledAt
                    ? t("collabGroup.chat.recalled", { sender: preview.sender })
                    : `${preview.sender}: ${preview.body}`)
                  : t("collabGroup.membersCount", { n: c.members?.length ?? 0 })}
              </span>
            </button>
          );
        })}
      </div>

      {/* 右侧：消息流 + 输入框；未选群时渲染诚实空态 */}
      <div className="channel-chat__main">
        {!openCh ? (
          <div className="channel-chat__placeholder">
            {loadError ? t("collabGroup.error") : t("collabGroup.chat.placeholder")}
          </div>
        ) : (
          <>
            <div className="channel-chat__chathead">
              <button type="button" className="channel-chat__back" onClick={closeChannel} aria-label={t("collabGroup.chat.back")}>‹</button>
              <span className="channel-chat__chatname" title={openCh.name}>{openCh.name}</span>
              {openCh.topic && <span className="channel-chat__chattopic">{openCh.topic}</span>}
              <span className="channel-chat__chatmeta">{t("collabGroup.membersCount", { n: openCh.members?.length ?? 0 })}</span>
              <button type="button" className="btn btn--small" onClick={() => setDetailOpen((v) => !v)}>
                {detailOpen ? t("collabGroup.chat.closeDetail") : t("collabGroup.chat.detail")}
              </button>
            </div>

            {actionError && (
              <div className="channel-chat__actionerror" role="alert">
                <span>{actionError}</span>
                <button type="button" className="btn btn--secondary btn--small" onClick={() => setActionError(null)}>{t("collabGroup.close")}</button>
              </div>
            )}

            <div className="channel-chat__stream">
              {messages.length === 0 && (
                <div className="channel-chat__empty">{t("collabGroup.chat.emptyStream")}</div>
              )}
              {messages.map((m) => {
                const own = m.sender === HUMAN_LOCAL;
                if (m.cancelledAt) {
                  return (
                    <div key={m.id} className="channel-chat__tombstone">
                      {t("collabGroup.chat.recalled", { sender: own ? t("collabGroup.chat.you") : directoryTitle(m.sender), time: formatTime(m.cancelledAt) })}
                    </div>
                  );
                }
                // 钉钉口径：已读显示、回执（delivered/queued）不显示；
                // 失败行是诚实错误，保留；标注只出现在自己的消息上。
                const readMembers = own
                  ? (m.fanout ?? []).filter((cell) => cell.readAt)
                  : [];
                const failedMembers = own
                  ? (m.fanout ?? []).filter((cell) => cell.state === "failed")
                  : [];
                return (
                  <div key={m.id} className={`channel-chat__row${own ? " channel-chat__row--own" : ""}`}>
                    <div className="channel-chat__rowmeta">
                      <span className={`channel-chat__sender${own ? " channel-chat__sender--own" : ""}`}>
                        {own ? t("collabGroup.chat.you") : directoryTitle(m.sender)}
                      </span>
                      <span className={`channel-chat__typebadge channel-chat__typebadge--${own ? "human" : "session"}`}>
                        {own ? t("collabGroup.identity.human") : t("collabGroup.chat.typeSession")}
                      </span>
                      <span className="channel-chat__time">{formatTime(m.at)}</span>
                    </div>
                    <div
                      className="channel-chat__bubble"
                      onContextMenu={(event) => {
                        event.preventDefault();
                        setMenu({ messageId: m.id, sender: m.sender, x: event.clientX, y: event.clientY });
                      }}
                    >
                      {m.body}
                    </div>
                    {(readMembers.length > 0 || failedMembers.length > 0) && (
                      <div className="channel-chat__receipts">
                        {readMembers.map((cell) => (
                          <span key={`r:${cell.member}`} className="channel-chat__receipt channel-chat__receipt--read">
                            {t("collabGroup.chat.readBy", { member: directoryTitle(cell.member) })}
                          </span>
                        ))}
                        {failedMembers.map((cell) => (
                          <span key={`f:${cell.member}`} className="channel-chat__receipt channel-chat__receipt--failed">
                            {t("collabGroup.chat.failedTo", { member: directoryTitle(cell.member) })}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>

            <div className="channel-chat__composer">
              <textarea
                className="channel-chat__input"
                value={draft}
                rows={2}
                placeholder={t("collabGroup.chat.sendPlaceholder")}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  // Enter 发送、Shift+Enter 换行；中文输入法组词中的 Enter 不发送。
                  if (event.key === "Enter" && !event.shiftKey && !(event.nativeEvent as KeyboardEvent).isComposing) {
                    event.preventDefault();
                    void send();
                  }
                }}
              />
              <button
                type="button"
                className="btn channel-chat__send"
                disabled={!draft.trim() || sending}
                onClick={() => void send()}
              >
                {t("collabGroup.chat.send")}
              </button>
            </div>
          </>
        )}
      </div>

      {/* 详情抽屉（②）：成员列表/邀请/移除/退群 */}
      {detailOpen && openCh && (
        <div className="channel-chat__detail">
          <div className="channel-chat__detailhead">
            <span className="channel-chat__detailtitle">{openCh.name}</span>
            {openCh.ownerId && (
              <span className="channel-chat__detailowner">
                {t("collabGroup.chat.ownerLabel", {
                  owner: openCh.ownerId === HUMAN_LOCAL ? t("collabGroup.chat.you") : directoryTitle(openCh.ownerId),
                })}
              </span>
            )}
          </div>
          {openCh.topic && <div className="channel-chat__detailtopic">{openCh.topic}</div>}

          <div className="channel-chat__members">
            {memberDetails.map((m) => {
              const isHuman = m.type === "human";
              const isOwnerMember = Boolean(openCh.ownerId && openCh.ownerId === m.contact);
              const removable = iAmOwner && hasOwner && m.contact !== HUMAN_LOCAL && !isOwnerMember;
              return (
                <div key={m.contact} className="channel-chat__member">
                  <span className="channel-chat__membername">
                    {m.contact === HUMAN_LOCAL ? t("collabGroup.chat.you") : directoryTitle(m.contact)}
                  </span>
                  <span className={`channel-chat__typebadge channel-chat__typebadge--${isHuman ? "human" : "session"}`}>
                    {isHuman ? t("collabGroup.identity.human") : t("collabGroup.chat.typeSession")}
                  </span>
                  {isOwnerMember && <span className="channel-chat__ownerbadge channel-chat__ownerbadge--me">{t("collabGroup.chat.ownerBadge")}</span>}
                  {removable && (
                    <button type="button" className="btn btn--secondary btn--small" onClick={() => void removeMember(m.contact)}>
                      {t("collabGroup.chat.remove")}
                    </button>
                  )}
                </div>
              );
            })}
          </div>

          {iAmOwner && (
            <div className="channel-chat__invited">
              {!inviteOpen ? (
                <button type="button" className="btn btn--small" onClick={() => { setInviteOpen(true); setInvitePicked([]); }}>
                  {t("collabGroup.chat.invite")}
                </button>
              ) : (
                <div className="channel-chat__invitebox">
                  <input
                    className="channel-chat__invitequery"
                    value={inviteQuery}
                    placeholder={t("collabGroup.chat.inviteSearch")}
                    onChange={(event) => setInviteQuery(event.target.value)}
                  />
                  <div className="channel-chat__invitecandidates">
                    {inviteCandidates.length === 0 && <div className="channel-chat__empty">{t("collabGroup.chat.inviteEmpty")}</div>}
                    {inviteCandidates.map((row) => (
                      <label key={row.contactId} className="channel-chat__invitecandidate">
                        <input
                          type="checkbox"
                          checked={invitePicked.includes(row.contactId)}
                          onChange={(event) => {
                            setInvitePicked((prev) => event.target.checked
                              ? [...prev, row.contactId]
                              : prev.filter((id) => id !== row.contactId));
                          }}
                        />
                        <span className="channel-chat__invitecandidatename">{row.title || row.purpose || row.contactId}</span>
                        <span className="channel-chat__invitecandidateid">{shortId(row.contactId)}</span>
                      </label>
                    ))}
                  </div>
                  <div className="channel-chat__inviteactions">
                    <button type="button" className="btn btn--small" disabled={invitePicked.length === 0} onClick={() => void invite()}>
                      {t("collabGroup.chat.inviteConfirm", { n: invitePicked.length })}
                    </button>
                    <button type="button" className="btn btn--secondary btn--small" onClick={() => setInviteOpen(false)}>
                      {t("collabGroup.close")}
                    </button>
                  </div>
                </div>
              )}
            </div>
          )}

          <div className="channel-chat__detailfoot">
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void leave()}>
              {t("collabGroup.chat.leave")}
            </button>
          </div>
        </div>
      )}

      {/* 建群对话框（①）：人类建群即群主 */}
      {createOpen && (
        <div className="channel-chat__create" role="dialog" aria-label={t("collabGroup.chat.create")}>
          <div className="channel-chat__createtitle">{t("collabGroup.chat.createTitle")}</div>
          <label className="channel-chat__createfield">
            <span>{t("collabGroup.chat.nameLabel")}</span>
            <input value={createName} onChange={(event) => setCreateName(event.target.value)} autoFocus />
          </label>
          <label className="channel-chat__createfield">
            <span>{t("collabGroup.chat.topicLabel")}</span>
            <input value={createTopic} onChange={(event) => setCreateTopic(event.target.value)} />
          </label>
          <label className="channel-chat__createfield">
            <span>{t("collabGroup.chat.limitLabel")}</span>
            <input
              value={createLimit}
              inputMode="numeric"
              placeholder={t("collabGroup.chat.limitPlaceholder")}
              onChange={(event) => setCreateLimit(event.target.value.replace(/[^0-9]/g, ""))}
            />
          </label>
          <div className="channel-chat__createactions">
            <button type="button" className="btn btn--small" disabled={!createName.trim()} onClick={() => void create()}>
              {t("collabGroup.chat.createConfirm")}
            </button>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => setCreateOpen(false)}>
              {t("collabGroup.close")}
            </button>
          </div>
        </div>
      )}

      {/* 右键菜单：own=撤回（349 Cancel 墓碑语义）；他人=⑦跳转发信方会话 +
          收信方（群）提示（demo v2 返工口径：收信方为群，第二项示意已在当前视图）。 */}
      {menu && createPortal(
        <div className="channel-chat__menu" style={{ left: menu.x, top: menu.y }} role="menu">
          {menu.sender === HUMAN_LOCAL ? (
            <button
              type="button"
              role="menuitem"
              onClick={() => { setMenu(null); void cancelMessage(menu.messageId); }}
            >
              {t("collabGroup.chat.recall")}
            </button>
          ) : (
            <>
              {onOpenSession && (
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => { setMenu(null); onOpenSession(menu.sender); }}
                >
                  {t("collabHub.directs.ctxSender", { name: directoryTitle(menu.sender) })}
                </button>
              )}
              <button
                type="button"
                role="menuitem"
                className="channel-chat__menuitem--muted"
                onClick={() => setMenu(null)}
                title={t("collabHub.directs.ctxGroupSelf")}
              >
                {t("collabHub.directs.ctxRecipientGroup")}
              </button>
            </>
          )}
        </div>,
        document.body,
      )}
      {menu && (
        // 点其它任何地方即收起菜单（portal 外的透明遮罩，不挡滚动）。
        <div className="channel-chat__menuveil" onContextMenu={(event) => { event.preventDefault(); setMenu(null); }} onClick={() => setMenu(null)} />
      )}
    </div>
  );
}
