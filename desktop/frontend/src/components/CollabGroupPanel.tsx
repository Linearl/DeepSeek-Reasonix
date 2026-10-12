// 群聊式协作视图（task 409）：多 Agent 协作的用户观察窗——活跃协作会话聚合
// 呈现（标题 / running|queued|idle|unknown 状态 / 当前任务卡 / 最近一条往来）。
// 纯只读消费者：它显示的每个字节都来自既有存储（通讯录、共享忙闲判定、任务
// 卡、已投递信件、频道实体），从不发明状态、从不写入（409 正文原则 4：与
// Cue 的差异 = 观察窗，不制造 AI 互聊内容）。
//
// 任务 802（③入口整合）：本面板 = 会话（409 现状）+ 频道消息两个 tab；频道
// 消息 tab 由 ChannelChatPanel 承载微信式真群聊（人类建群/发言/撤回/详情）。
// 面板开关与入口保持 409/677 现状：关=入口按钮不渲染，零痕迹。
//
// 任务587 同款纪律（失败≠空态）：读取失败显式报错 + 重试 + 退避自动恢复，
// 绝不渲染成「暂无协作」；catch 路径经 reportFrontendLog 落 console + ring
// buffer（网关断连也有本地痕迹）。

import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { app } from "../lib/bridge";
import { reportFrontendLog } from "../lib/frontendLog";
import { useT } from "../lib/i18n";
import { ChannelChatPanel, type ChannelChatBindings } from "./ChannelChatPanel";

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

/** 802：面板级注入缝 = 会话 tab 的总览读口 + 频道消息 tab 的聊天绑定面。 */
export type CollabGroupBindings = ChannelChatBindings & {
  GetCollabViewOverview(): Promise<CollabViewOverview>;
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
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

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

  // 802：会话 tab 只聚合总览；频道消息 tab 的拉取归 ChannelChatPanel 自管。
  const refresh = useCallback(async () => {
    setLoading(true);
    // the banner describes the LATEST attempt only — a fresh read retires the
    // previous attempt's error (it returns as a fresh failure or as data).
    setLoadError(null);
    try {
      setOverview(await b.GetCollabViewOverview());
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

  const sessions = overview?.sessions ?? [];
  const viewEmpty = sessions.length === 0;
  const stateLabel = (state: string) =>
    t(`collabGroup.state.${state}` as "collabGroup.state.running");
  const identityLabel = (value: string) =>
    t(`collabGroup.identity.${value}` as "collabGroup.identity.main");
  const cardStatusLabel = (status: string) =>
    t(`collabGroup.card.${status}` as "collabGroup.card.running");

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

        {/* 任务 802：频道消息 tab = 微信式真群聊（建群/发言/撤回/详情/邀请）。
            自管拉取与退避；切走 tab 即卸载即停，关面板零成本（409 同款）。 */}
        {tab === "channels" && <ChannelChatPanel bindings={b} />}
      </div>

      <div className="collab-group-panel__foot">
        {/* 观察窗注（409 语义）只描述会话 tab；频道消息 tab 是可发言的真群聊。 */}
        {tab === "sessions" && <span className="collab-group-panel__note">{t("collabGroup.note")}</span>}
        {overview?.generatedAt ? (
          <span className="collab-group-panel__time">{formatTime(overview.generatedAt)}</span>
        ) : null}
      </div>
    </div>,
    document.body,
  );
}
