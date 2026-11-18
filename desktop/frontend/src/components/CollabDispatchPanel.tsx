// 派发记录四态面板 (task 1696): the sender-side twin of the inbox list —
// one row per outgoing cross-session message, showing 目标会话·角色·四态
// （等待/进行中/已回信/已拒绝）·时间·回信摘要, with a click jumping straight
// to the target session. The panel is a READ-ONLY audit surface (non-goal red
// line: no dispatch action entry here) and everything it shows comes from the
// persistent layer (*.sent.jsonl + receipts/*.json + inbox threadIds), so a
// restart keeps all of it (验收④).
//
// 载体纪律（任务 1696 UI 最小影响）：本组件不是独立入口——它挂在信件中心
// CollabInboxPanel 的既有视图切换（列表/对话链/派发）第三态里，复用同一
// 面板壳层与 CSS 视觉体系；关闭态 = 信件中心关闭（不新增窗口/弹窗/页面）。

import { useCallback, useEffect, useRef, useState } from "react";
import { app } from "../lib/bridge";
import { collabTitleFor, rememberCollabContactNames, shortContactId } from "../lib/collabContactNames";
import { onCollabMailChanged as onCollabMailChangedRuntime } from "../lib/inboxEvents";
import { reportFrontendLog } from "../lib/frontendLog";
import { useT } from "../lib/i18n";

/** 目录行（与 CollabInboxPanel.CollabSessionDirectoryRow 同形，避免跨组件
 *  依赖其私有导出——两处字段集合各自声明，生产 bridge 同源）。 */
type DirectoryRow = {
  contactId: string;
  title?: string;
  topicId?: string;
  sessionPath: string;
  scope?: string;
  workspaceRoot?: string;
  open: boolean;
  archived?: boolean;
};

export type CollabDispatchBindings = {
  /** 派发四态读口（可选 = 向前兼容缝：旧 bindings fake / 旧网关面缺方法时
   *  面板降级为「暂不支持」，不崩不阻塞——与 ReadCollabMail 的缝同法）。 */
  ListCollabSentMail?(limit: number): Promise<CollabSentDispatchSnapshot>;
  ListAddressableSessions(): Promise<DirectoryRow[]>;
  /** 跳转目标会话（可选：测试 fake 可省；生产 bridge 恒有）。 */
  ActivateTopic?(
    scope: string,
    workspaceRoot: string,
    topicId: string,
    sessionPath: string,
  ): Promise<unknown>;
};

/** 一条派发记录（与 Go collabinbox.SentDispatchRow 逐字段对应）。 */
export type CollabSentDispatchRow = {
  id: string;
  fromContactId: string;
  toContactId: string;
  fromRole?: string;
  toRole?: string;
  toTitle?: string;
  preview: string;
  delivery?: string;
  requireReply?: boolean;
  at: number;
  status: string;
  statusAt?: number;
  statusDetail?: string;
  replyPreview?: string;
  replyAt?: number;
};

export type CollabSentDispatchSnapshot = {
  rows: CollabSentDispatchRow[];
  total: number;
  returned: number;
  truncated?: boolean;
  degraded?: boolean;
};

/** 角色五值集（与 Go sessioncollab.Identity* 同源——徽标只渲染这五个，
 *  未知值诚实缺席，不猜）。 */
const ROLE_KEYS = ["human", "main", "sub", "heartbeat", "system"] as const;

/** 四态集合（与 Go Dispatch* 常量同源）。 */
const STATUS_KEYS = ["waiting", "in_flight", "replied", "refused"] as const;

const DISPATCH_PAGE = 200;

function formatTime(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleString();
}

export function CollabDispatchPanel({
  bindings,
  directory,
  embedded,
}: {
  bindings?: CollabDispatchBindings;
  directory?: { ListAddressableSessions(): Promise<DirectoryRow[]> };
  embedded?: boolean;
} = {}) {
  const b: CollabDispatchBindings = bindings ?? (app as CollabDispatchBindings);
  const t = useT();
  const [snapshot, setSnapshot] = useState<CollabSentDispatchSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [jumpNote, setJumpNote] = useState("");
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const refresh = useCallback(async () => {
    // 可选缝：旧网关面缺方法 → 降级提示，绝不渲染成假空态。
    if (!b.ListCollabSentMail) {
      setSnapshot({ rows: [], total: 0, returned: 0 });
      setLoadError(true);
      setLoading(false);
      return;
    }
    try {
      const snap = await b.ListCollabSentMail(DISPATCH_PAGE);
      if (!mounted.current) return;
      setSnapshot(snap);
      setLoadError(false);
    } catch (err) {
      if (!mounted.current) return;
      // 任务587 同纪律：失败显式报错 + 留痕，绝不渲染成「暂无派发记录」。
      setLoadError(true);
      reportFrontendLog("collabDispatch", "read failed", String(err), "error");
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, [b]);

  useEffect(() => {
    void refresh();
    // 收/发信事件触发刷新（回信落库 → 已回信态即时可见）。
    const off = onCollabMailChangedRuntime(() => {
      void refresh();
    });
    return off;
  }, [refresh]);

  // 点击行 → 直达目标会话（验收③）：通讯录按 contactId 匹配 → 命中且有
  // topicId → ActivateTopic；未命中（已删除/外部会话）→ 就地说明，不静默。
  const jumpToTarget = useCallback(
    async (row: CollabSentDispatchRow) => {
      setJumpNote("");
      let rows: DirectoryRow[];
      try {
        const source = directory ?? b;
        rows = (await source.ListAddressableSessions()) ?? [];
      } catch (err) {
        reportFrontendLog("collabDispatch", "directory read failed", String(err), "error");
        setJumpNote(t("collabDispatch.jump.directoryFailed"));
        return;
      }
      rememberCollabContactNames(rows);
      const hit = rows.find((r) => r.contactId === row.toContactId);
      if (!hit) {
        setJumpNote(t("collabDispatch.jump.notInDirectory", { id: shortContactId(row.toContactId) }));
        return;
      }
      if (!b.ActivateTopic || !hit.topicId || !hit.workspaceRoot) {
        // 会话在通讯录但不可直达（归档外部/缺 topic）——诚实说明而非假装跳了。
        setJumpNote(t("collabDispatch.jump.notOpenable", { title: collabTitleFor(row.toContactId) }));
        return;
      }
      try {
        await b.ActivateTopic(hit.scope ?? "global", hit.workspaceRoot, hit.topicId, hit.sessionPath ?? "");
        setJumpNote("");
      } catch (err) {
        reportFrontendLog("collabDispatch", "activate failed", String(err), "error");
        setJumpNote(t("collabDispatch.jump.failed", { title: collabTitleFor(row.toContactId) }));
      }
    },
    [b, directory, t],
  );

  const rows = snapshot?.rows ?? [];
  const displayName = (row: CollabSentDispatchRow) =>
    collabTitleFor(row.toContactId);
  const statusKey = (status: string) =>
    (STATUS_KEYS as readonly string[]).includes(status)
      ? (status as (typeof STATUS_KEYS)[number])
      : null;

  return (
    <div className={`collab-dispatch${embedded ? " collab-dispatch--embedded" : ""}`}>
      {jumpNote && <div className="collab-dispatch__note">{jumpNote}</div>}
      {loadError && (
        <div className="collab-dispatch__empty collab-dispatch__empty--error">
          {t("collabDispatch.readFailed")}
          <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>
            {t("collabDispatch.retry")}
          </button>
        </div>
      )}
      {!loadError && snapshot?.degraded && (
        <div className="collab-dispatch__empty collab-dispatch__empty--degraded">{t("collabDispatch.degraded")}</div>
      )}
      {!loadError && !snapshot?.degraded && !loading && rows.length === 0 && (
        <div className="collab-dispatch__empty">{t("collabDispatch.empty")}</div>
      )}
      {rows.map((row) => {
        const sk = statusKey(row.status);
        const jumpableTitle = t("collabDispatch.jumpHint", { title: displayName(row) });
        return (
          <button
            key={row.id}
            type="button"
            className="collab-dispatch__row"
            title={jumpableTitle}
            onClick={() => void jumpToTarget(row)}
          >
            <div className="collab-dispatch__meta">
              <span className="collab-dispatch__route">
                {displayName(row)}
                <span className="collab-dispatch__id" title={row.toContactId}>
                  {shortContactId(row.toContactId)}
                </span>
                {(ROLE_KEYS as readonly string[]).includes(row.toRole ?? "") && (
                  <span className={`collab-dispatch__role collab-dispatch__role--${row.toRole}`}>
                    {t(`collabDispatch.role.${row.toRole}` as "collabDispatch.role.human")}
                  </span>
                )}
              </span>
              {sk && (
                <span className={`collab-dispatch__status collab-dispatch__status--${sk}`}>
                  {t(`collabDispatch.status.${sk}` as "collabDispatch.status.waiting")}
                </span>
              )}
              <span className="collab-dispatch__time">{formatTime(row.statusAt || row.at)}</span>
              {row.requireReply && <span className="collab-dispatch__reqreply">{t("collabDispatch.requireReply")}</span>}
            </div>
            <div className="collab-dispatch__preview">{row.preview}</div>
            {row.status === "replied" && (
              <div className="collab-dispatch__reply">
                <span className="collab-dispatch__replylabel">{t("collabDispatch.replyLabel")}</span>
                {" "}
                {row.replyPreview}
                {row.replyAt ? <span className="collab-dispatch__time">{formatTime(row.replyAt)}</span> : null}
              </div>
            )}
            {row.statusDetail && (
              <div className="collab-dispatch__detail" title={row.statusDetail}>
                {row.statusDetail}
              </div>
            )}
          </button>
        );
      })}
      {snapshot?.truncated && (
        <div className="collab-dispatch__more">
          {t("collabDispatch.truncated", { shown: rows.length, total: snapshot.total })}
        </div>
      )}
    </div>
  );
}
