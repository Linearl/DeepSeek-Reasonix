// 群聊式协作视图（task 409）：多 Agent 协作的用户观察窗——活跃协作会话聚合
// 呈现（标题 / running|queued|idle|unknown 状态 / 当前任务卡 / 最近一条往来）
// + 频道名册（349 实体）与每成员送达/已读标注。纯只读消费者：它显示的每个
// 字节都来自既有存储（通讯录、共享忙闲判定、任务卡、已投递信件、频道实体），
// 从不发明状态、从不写入（409 正文原则 4：与 Cue 的差异 = 观察窗，不制造
// AI 互聊内容）。
//
// 任务587 同款纪律（失败≠空态）：读取失败显式报错 + 重试 + 退避自动恢复，
// 绝不渲染成「暂无协作」；catch 路径经 reportFrontendLog 落 console + ring
// buffer（网关断连也有本地痕迹）。

import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { app } from "../lib/bridge";
import { reportFrontendLog } from "../lib/frontendLog";
import { useT } from "../lib/i18n";

export type CollabViewCard = { id: string; title: string; status: string; updatedAt: number };

export type CollabViewExchange = {
  from: string;
  to: string;
  preview: string;
  at: number;
  threadId?: string;
};

export type CollabViewSession = {
  contactId: string;
  topicId?: string;
  title: string;
  purpose?: string;
  identityType?: string;
  state: string;
  lastActivity: number;
  unreadInbox: number;
  card?: CollabViewCard | null;
  lastExchange?: CollabViewExchange | null;
};

export type CollabViewOverview = { sessions: CollabViewSession[]; generatedAt: number };

export type CollabChannelSummary = {
  id: string;
  name: string;
  topic?: string;
  createdAt: number;
  hourlyLimit: number;
  members: string[];
  messages: number;
};

export type CollabChannelFanoutCell = {
  messageId: string;
  member: string;
  fanoutId: string;
  state: string;
  error?: string;
  deliveredAt?: number;
  readAt?: number;
};

export type CollabChannelMessage = {
  id: string;
  sender: string;
  body: string;
  at: number;
  fanout?: CollabChannelFanoutCell[];
};

export type CollabChannelRead = { channel: CollabChannelSummary; messages: CollabChannelMessage[] };

export type CollabGroupBindings = {
  GetCollabViewOverview(): Promise<CollabViewOverview>;
  ListCollabChannels(): Promise<CollabChannelSummary[]>;
  ReadCollabChannel(ref: string, limit: number): Promise<CollabChannelRead>;
};

let panelOpen = false;
const openListeners = new Set<(open: boolean) => void>();

export function isCollabGroupOpen(): boolean {
  return panelOpen;
}

/** 开/关群聊视图面板（侧栏工具行图标入口，task 409）。 */
export function setCollabGroupOpen(next: boolean): void {
  if (panelOpen === next) return;
  panelOpen = next;
  for (const listener of openListeners) listener(next);
}

export function onCollabGroupOpenChange(cb: (open: boolean) => void): () => void {
  openListeners.add(cb);
  return () => openListeners.delete(cb);
}

// 409 验收「派单后 1 分钟内视图状态刷新」: the panel polls only while it is
// open — 30s halves that bound with margin, and a closed panel costs zero.
export const COLLAB_GROUP_POLL_MS = 30_000;

// 任务587 同款退避：失败后 2s/4s/8s/15s 自动重试，成功即复位。
export const collabGroupRetryDelaysMs = [2_000, 4_000, 8_000, 15_000];

function formatTime(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleString();
}

type PanelTab = "sessions" | "channels";

export function CollabGroupPanel({ bindings }: { bindings?: CollabGroupBindings } = {}) {
  const t = useT();
  const b: CollabGroupBindings = bindings ?? app;
  const [open, setOpen] = useState(panelOpen);
  const [tab, setTab] = useState<PanelTab>("sessions");
  const [overview, setOverview] = useState<CollabViewOverview | null>(null);
  const [channels, setChannels] = useState<CollabChannelSummary[] | null>(null);
  const [openChannel, setOpenChannel] = useState<string | null>(null);
  const [channelView, setChannelView] = useState<CollabChannelRead | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  // The expanded channel rides a ref so polling keeps it fresh WITHOUT making
  // `refresh` a new identity on every toggle (a new refresh would re-run the
  // open effect and double-fetch the whole overview on each channel toggle).
  const openChannelRef = useRef<string | null>(null);

  useEffect(() => onCollabGroupOpenChange((next) => setOpen(next)), []);

  // 任务587（失败后自动恢复）：the retry loop lives in refs so re-creating
  // `refresh` never stacks timers, and the pending timer always calls the
  // LATEST refresh closure.
  const retryRef = useRef<{ timer: ReturnType<typeof setTimeout> | null; attempt: number }>({ timer: null, attempt: 0 });
  const refreshRef = useRef<() => void>(() => {});

  const cancelRetry = useCallback(() => {
    if (retryRef.current.timer !== null) {
      clearTimeout(retryRef.current.timer);
      retryRef.current.timer = null;
    }
  }, []);

  const scheduleRetry = useCallback(() => {
    if (retryRef.current.timer !== null) return; // one pending retry is enough
    const delays = collabGroupRetryDelaysMs;
    const delay = delays[Math.min(retryRef.current.attempt, delays.length - 1)];
    retryRef.current.timer = setTimeout(() => {
      retryRef.current.timer = null;
      retryRef.current.attempt += 1;
      refreshRef.current();
    }, delay);
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    // the banner describes the LATEST attempt only — a fresh read retires the
    // previous attempt's error (it returns as a fresh failure or as data).
    setLoadError(null);
    try {
      setOverview(await b.GetCollabViewOverview());
      const list = await b.ListCollabChannels();
      setChannels(list);
      // Keep an expanded channel's tail on the same refresh clock.
      const expanded = openChannelRef.current;
      if (expanded) {
        try {
          setChannelView(await b.ReadCollabChannel(expanded, 50));
        } catch {
          setChannelView(null);
        }
      }
      // success stops the recovery loop and resets its backoff.
      retryRef.current.attempt = 0;
      cancelRetry();
    } catch (err) {
      // A closed gateway must not crash the panel — it keeps the last data.
      // 任务587：但失败绝不能渲染成「暂无协作」——显式失败块 + 重试按钮。
      setLoadError(String(err));
      reportFrontendLog("collab-group", "overview failed", `err=${String(err)}`, "warn");
      scheduleRetry();
    } finally {
      setLoading(false);
    }
  }, [b, cancelRetry, scheduleRetry]);

  useEffect(() => {
    refreshRef.current = () => void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!open) {
      cancelRetry();
      return;
    }
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, COLLAB_GROUP_POLL_MS);
    return () => window.clearInterval(timer);
  }, [open, refresh, cancelRetry]);

  if (!open) return null;

  const toggleChannel = async (id: string) => {
    if (openChannel === id) {
      setOpenChannel(null);
      openChannelRef.current = null;
      setChannelView(null);
      return;
    }
    setOpenChannel(id);
    openChannelRef.current = id;
    try {
      setChannelView(await b.ReadCollabChannel(id, 50));
    } catch {
      setChannelView(null);
    }
  };

  const sessions = overview?.sessions ?? [];
  const viewEmpty = sessions.length === 0;
  const stateLabel = (state: string) =>
    t(`collabGroup.state.${state}` as "collabGroup.state.running");
  const identityLabel = (value: string) =>
    t(`collabGroup.identity.${value}` as "collabGroup.identity.main");
  const cardStatusLabel = (status: string) =>
    t(`collabGroup.card.${status}` as "collabGroup.card.running");
  // Per-recipient annotation (349: delivered/read are per member). delivered
  // + read_at → 已读; delivered → 送达; anything else shows its state word.
  const fanoutLabel = (cell: CollabChannelFanoutCell) => {
    if (cell.state === "delivered") return cell.readAt ? t("collabGroup.read") : t("collabGroup.delivered");
    if (cell.state === "queued") return t("collabGroup.fanQueued");
    if (cell.state === "failed") return t("collabGroup.fanFailed");
    return cell.state;
  };

  return createPortal(
    <div className="collab-group-panel" role="dialog" aria-label={t("collabGroup.title")}>
      <div className="collab-group-panel__head">
        <span className="collab-group-panel__title">{t("collabGroup.title")}</span>
        <div className="collab-group-panel__actions">
          <button type="button" className="btn btn--small" onClick={() => void refresh()}>
            {t("collabGroup.refresh")}
          </button>
          <button type="button" className="btn btn--small" onClick={() => setCollabGroupOpen(false)}>
            {t("collabGroup.close")}
          </button>
        </div>
      </div>

      <div className="collab-group-panel__tabs" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={tab === "sessions"}
          className={`collab-group-panel__tab${tab === "sessions" ? " collab-group-panel__tab--on" : ""}`}
          onClick={() => setTab("sessions")}
        >
          {t("collabGroup.tab.sessions")}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === "channels"}
          className={`collab-group-panel__tab${tab === "channels" ? " collab-group-panel__tab--on" : ""}`}
          onClick={() => setTab("channels")}
        >
          {t("collabGroup.tab.channels")}
        </button>
      </div>

      <div className="collab-group-panel__rows">
        {/* 任务587 同款：失败但有旧数据 → 旧数据优先（可用性>新鲜度），顶部
            细条如实声明「显示的不是最新结果」，附重试。 */}
        {loadError && !viewEmpty && (
          <div className="collab-group-panel__errorstrip" role="alert">
            <span>{t("collabGroup.error.stale")}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>
              {t("collabGroup.retry")}
            </button>
          </div>
        )}
        {/* 失败且无数据 → 显式失败块（错误原文在详情里，绝不冒充空态）。 */}
        {viewEmpty && loadError && (
          <div className="collab-group-panel__error" role="alert">
            <span className="collab-group-panel__error-label">{t("collabGroup.error")}</span>
            <span className="collab-group-panel__error-detail" title={loadError}>{loadError}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>
              {t("collabGroup.retry")}
            </button>
          </div>
        )}
        {/* 健康空：空目录渲染空态，不是错误（409 验收：空态不劣化现状）。 */}
        {tab === "sessions" && viewEmpty && !loadError && (
          <div className="collab-group-panel__empty">
            {loading ? t("collabGroup.loading") : t("collabGroup.empty")}
          </div>
        )}
        {tab === "sessions" &&
          sessions.map((session) => (
            <div key={session.contactId || session.title || session.topicId} className="collab-group-panel__session">
              <div className="collab-group-panel__meta">
                <span className={`collab-group-panel__state collab-group-panel__state--${session.state}`}>
                  {stateLabel(session.state)}
                </span>
                <span className="collab-group-panel__name">{session.title || session.contactId}</span>
                {session.identityType && (
                  <span className={`collab-group-panel__idtype collab-group-panel__idtype--${session.identityType}`}>
                    {identityLabel(session.identityType)}
                  </span>
                )}
                {session.unreadInbox > 0 && (
                  <span className="collab-group-panel__unread">
                    {t("collabGroup.unread", { n: session.unreadInbox })}
                  </span>
                )}
                <span className="collab-group-panel__time">{formatTime(session.lastActivity)}</span>
              </div>
              {session.purpose && (
                <div className="collab-group-panel__purpose">{session.purpose}</div>
              )}
              {session.card && (
                <div className="collab-group-panel__card">
                  <span className={`collab-group-panel__cardstatus collab-group-panel__cardstatus--${session.card.status}`}>
                    {cardStatusLabel(session.card.status)}
                  </span>
                  <span className="collab-group-panel__cardtitle">{session.card.title}</span>
                </div>
              )}
              {session.lastExchange && (
                <div className="collab-group-panel__preview">
                  {session.lastExchange.from} → {session.lastExchange.to}：{session.lastExchange.preview}
                </div>
              )}
            </div>
          ))}

        {tab === "channels" && (channels?.length ?? 0) === 0 && (
          <div className="collab-group-panel__empty">
            {loading ? t("collabGroup.loading") : t("collabGroup.emptyChannels")}
          </div>
        )}
        {tab === "channels" &&
          (channels ?? []).map((channel) => (
            <div key={channel.id} className="collab-group-panel__channel">
              <button
                type="button"
                className="collab-group-panel__channelhead"
                aria-expanded={openChannel === channel.id}
                onClick={() => void toggleChannel(channel.id)}
              >
                <span className="collab-group-panel__channelname">#{channel.name}</span>
                <span className="collab-group-panel__channelcounts">
                  {t("collabGroup.membersCount", { n: channel.members?.length ?? 0 })}
                  {" · "}
                  {t("collabGroup.messagesCount", { n: channel.messages })}
                </span>
              </button>
              {openChannel === channel.id && (
                <div className="collab-group-panel__channelbody">
                  {(channelView?.messages?.length ?? 0) === 0 && (
                    <div className="collab-group-panel__empty">{t("collabGroup.emptyChannelMessages")}</div>
                  )}
                  {(channelView?.messages ?? []).map((message) => (
                    <div key={message.id} className="collab-group-panel__message">
                      <div className="collab-group-panel__meta">
                        <span className="collab-group-panel__sender">{message.sender}</span>
                        <span className="collab-group-panel__time">{formatTime(message.at)}</span>
                      </div>
                      <div className="collab-group-panel__messagebody">{message.body}</div>
                      {(message.fanout?.length ?? 0) > 0 && (
                        <div className="collab-group-panel__fanout">
                          {message.fanout!.map((cell) => (
                            <span
                              key={`${cell.messageId}:${cell.member}`}
                              className={`collab-group-panel__fanoutcell collab-group-panel__fanoutcell--${cell.readAt ? "read" : cell.state}`}
                            >
                              {cell.member}·{fanoutLabel(cell)}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
      </div>

      <div className="collab-group-panel__foot">
        <span className="collab-group-panel__note">{t("collabGroup.note")}</span>
        {overview?.generatedAt ? (
          <span className="collab-group-panel__time">{formatTime(overview.generatedAt)}</span>
        ) : null}
      </div>
    </div>,
    document.body,
  );
}
