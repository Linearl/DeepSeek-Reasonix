// 跨会话收件箱面板 (task 320): the one place that shows every cross-session
// mail — five-bucket aggregation (views, not storage), sender/recipient/date
// filtering with an asc/desc date toggle (a), revision-stamped dismiss
// (contract ①), retention picker, approval sub-states with their decider
// recorded (task 320 d), and the thread-chain view (task 320 g). The panel is
// a pure consumer of the Go index: everything
// it shows already sits in a recipient inbox — a queued send is not an entry
// (contract ②), and dismissed/decided/retention state lives on disk, so a
// restart keeps all of it (contract f).
// 任务587（后端正常前端空断裂修复）：读取失败/读取中与健康空三态分离——失败
// 显式报错+重试按钮+退避自动恢复，绝不渲染成「暂无信件」；所有 catch 路径
// 经 reportFrontendLog 落 console + ring buffer（网关断连也有本地痕迹）。

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { app } from "../lib/bridge";
import { collabTitleFor, rememberCollabContactNames, shortContactId } from "../lib/collabContactNames";
import { reportFrontendLog } from "../lib/frontendLog";
import { useT } from "../lib/i18n";
import { groupForProject, loadProjectGroupAssign, loadProjectGroups } from "../lib/projectGroups";

export type CollabMailEntry = {
  id: string;
  from: string;
  fromSession?: string;
  to: string;
  toTitle?: string;
  at: number;
  threadId: string;
  hop?: number;
  cardId?: string;
  approver?: string;
  requireReply?: boolean;
  delivery?: string;
  bucket: string;
  /** 任务 349n1 群标识：fan-out 来源频道名（空 = 点对点信）。 */
  channel?: string;
  preview: string;
  delivered: boolean;
  read: boolean;
  dismissed?: boolean;
  /** 任务 461 P8 ③：完全同内容折叠计数（>1 时渲染 ×N 徽标）。 */
  duplicateCount?: number;
  decidedBy?: string;
  decidedAt?: number;
  pendingMe?: boolean;
  mine?: boolean;
};

export type CollabMailSettings = {
  retention: string;
  /** 任务 464：会话删除时的清理规则（never 默认 | sender | receiver | both）。 */
  cleanupRule?: string;
};

export type CollabMailSnapshot = {
  revision: string;
  settings: CollabMailSettings;
  total: number;
  returned: number;
  truncated: boolean;
  entries: CollabMailEntry[];
  /** 任务511：锁繁忙时后端照常返回快照但标 degraded——空面板此时是「读取
   * 受限」而非「真的没信」，展示位必须区分（排查报告 §4 缺口 1）。 */
  degraded?: boolean;
};

export type CollabMailChain = {
  threadId: string;
  participants: string[];
  count: number;
  firstAt: number;
  lastAt: number;
  preview: string;
  entries: CollabMailEntry[];
};

export type CollabMailChains = {
  revision: string;
  settings: CollabMailSettings;
  total: number;
  chains: CollabMailChain[];
};

/** 任务 620：立即清理的结果——物理移除数 + 清理后的默认视图快照。 */
export type CollabInboxCleanResult = { removed: number; snapshot: CollabMailSnapshot };

export type CollabInboxBindings = {
  ListCollabMail(bucket: string, from: string, to: string, state: string, limit: number, includeDismissed: boolean, order: string): Promise<CollabMailSnapshot>;
  ListCollabMailChains(bucket: string, limit: number): Promise<CollabMailChains>;
  CountUnreadCollabMail(): Promise<number>;
  DismissCollabMail(ids: string[]): Promise<CollabMailSnapshot>;
  MarkCollabMailRead(ids: string[]): Promise<CollabMailSnapshot>;
  UndismissCollabMail(ids: string[]): Promise<CollabMailSnapshot>;
  MarkCollabMailDecided(messageID: string, by: string): Promise<CollabMailSnapshot>;
  SetCollabMailRetention(retention: string): Promise<CollabMailSnapshot>;
  /** 任务 464：会话删除语义四选一，设置即生效并返回新快照。 */
  SetCollabMailCleanupRule(rule: string): Promise<CollabMailSnapshot>;
  /** 任务 620：立即清理——按当前保留期与清理规则执行一次清理，返回移除数与新快照。 */
  CleanCollabMailNow(): Promise<CollabInboxCleanResult>;
};

/** 任务461-P4: one ListAddressableSessions row — the addressable roster that
 * backs the from/to filter dropdowns (options read 会话名, hover shows
 * 项目 › 分组 › 会话名 › contact_id). Kept as a separate seam so existing
 * CollabInboxBindings fakes stay valid. */
export type CollabSessionDirectoryRow = {
  contactId: string;
  purpose?: string;
  title?: string;
  topicId?: string;
  sessionPath: string;
  scope?: string;
  workspaceRoot?: string;
  open: boolean;
  archived?: boolean;
};

export type CollabSessionDirectory = {
  ListAddressableSessions(): Promise<CollabSessionDirectoryRow[]>;
};

let panelOpen = false;
const openListeners = new Set<(open: boolean) => void>();

/**
 * 任务587（失败后自动恢复刷新）：a failed read retries on this escalating
 * schedule while the panel stays open — the gateway that rejected the call
 * usually comes back on its own, and the panel must show the recovered mail
 * WITHOUT the user closing/reopening (验收②). It settles at the last value
 * and keeps a slow heartbeat until one attempt succeeds. Tests splice this
 * array down to milliseconds; production never reads it faster than 2s.
 */
export const collabInboxRetryDelaysMs = [2_000, 4_000, 8_000, 15_000];

export function isCollabInboxOpen(): boolean {
  return panelOpen;
}

/** Opens/closes the panel (settings entry point, task 320). */
export function setCollabInboxOpen(next: boolean): void {
  if (panelOpen === next) return;
  panelOpen = next;
  for (const listener of openListeners) listener(next);
}

export function onCollabInboxOpenChange(cb: (open: boolean) => void): () => void {
  openListeners.add(cb);
  return () => openListeners.delete(cb);
}

/**
 * 任务 320 遗留 #1：图标行收件箱按钮的未读徽标数 —— 未读（收件方 seen 游标
 * 未覆盖）且未消除的统一表条目数，与面板默认视图的「待处理」口径一致。
 * 刷新时机（派单钦定）：挂载时 + 面板每次开/合切换时 —— 打开路径会应用保留期
 * （可能清理旧信），关闭路径紧随刚发生的已读/消除，两个时刻的数字都可能变；
 * 不做轮询。网关不可达时保留上次数值（与面板 refresh 的容错同款）。
 */
export function useCollabInboxUnreadCount(bindings?: CollabInboxBindings): number {
  const b: CollabInboxBindings = bindings ?? app;
  const [count, setCount] = useState(0);
  useEffect(() => {
    let alive = true;
    const refresh = () => {
      b.CountUnreadCollabMail()
        .then((n) => { if (alive) setCount(n); })
        .catch((err) => {
          // 任务587：closed gateway keeps the last count, but the failure must
          // leave a trace (console + ring buffer survive; the desktop.log hop
          // is best effort) — silent swallowing is what made 511 复发无从诊断.
          reportFrontendLog("collab-inbox", "unread count failed", String(err), "warn");
        });
    };
    refresh();
    const off = onCollabInboxOpenChange(refresh);
    return () => { alive = false; off(); };
  }, [b]);
  return count;
}

const BUCKETS = ["all", "approval", "mention", "automation", "system"] as const;
const STATES = ["all", "pendingMe", "mine", "decided"] as const;
const RETENTIONS = ["7d", "30d", "90d", "forever"] as const;
// 任务 464：会话删除时的清理规则，四选一，与保留期正交。
const CLEANUP_RULES = ["never", "sender", "receiver", "both"] as const;
const ORDERS = ["desc", "asc"] as const;

type Bucket = (typeof BUCKETS)[number];
type StateFilter = (typeof STATES)[number];
type OrderFilter = (typeof ORDERS)[number];

function formatTime(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleString();
}

// 任务461-P4: the dropdown option label is the session name; the hover title is
// the address breadcrumb 「项目 › 分组 › 会话名 › contact_id」. Missing segments
// (no project, no group, no title) are dropped rather than guessed.
function sessionHoverLabel(row: CollabSessionDirectoryRow, globalLabel: string): string {
  const parts: string[] = [];
  if (row.scope === "project" && row.workspaceRoot) {
    parts.push(workspaceBasename(row.workspaceRoot));
    const groupId = groupForProject(loadProjectGroupAssign(), row.workspaceRoot);
    const group = groupId ? loadProjectGroups().find((g) => g.id === groupId) : undefined;
    if (group?.title) parts.push(group.title);
  } else if (row.scope !== "project") {
    parts.push(globalLabel);
  }
  if (row.title) parts.push(row.title);
  if (row.contactId) parts.push(row.contactId);
  return parts.join(" › ");
}

function workspaceBasename(root: string): string {
  return root.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || root;
}

// 任务462: contact_id → 会话名（显示层）。优先级：名单现读名（改名后即新名）
// > 邮件自带 ToTitle（发送时刻的时间戳快照，可能过期，仅在名单查不到时兜底）
// > 截短 id —— 查不到名字绝不渲染空白。
function contactDisplayName(contactId: string, staleTitle?: string): string {
  const live = collabTitleFor(contactId);
  if (live) return live;
  const stale = (staleTitle ?? "").trim();
  if (stale) return stale;
  return shortContactId(contactId);
}

// 任务462: hover 保留完整 contact_id（id 降为次要信息但不丢失）。
function contactHoverLabel(display: string, contactId: string): string {
  const id = contactId.trim();
  if (!id) return display;
  return display === id ? `contact_id=${id}` : `${display}（contact_id=${id}）`;
}

export function CollabInboxPanel({ bindings, directory }: { bindings?: CollabInboxBindings; directory?: CollabSessionDirectory } = {}) {
  const t = useT();
  const b: CollabInboxBindings = bindings ?? app;
  // 任务461-P4: the from/to dropdowns read the addressable roster. The
  // directory seam is injectable; a bindings fake that also carries the method
  // works, otherwise the real bridge does. A directory without the method
  // degrades to an empty roster (the「全部」option still filters nothing-off).
  const dir: CollabSessionDirectory | undefined = useMemo(() => {
    if (directory) return directory;
    if (bindings && typeof (bindings as Partial<CollabSessionDirectory>).ListAddressableSessions === "function") {
      return bindings as unknown as CollabSessionDirectory;
    }
    return app as CollabSessionDirectory;
  }, [bindings, directory]);
  const [open, setOpen] = useState(panelOpen);
  const [bucket, setBucket] = useState<Bucket>("all");
  const [state, setState] = useState<StateFilter>("all");
  const [order, setOrder] = useState<OrderFilter>("desc");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [sessions, setSessions] = useState<CollabSessionDirectoryRow[]>([]);
  const [view, setView] = useState<"list" | "chains">("list");
  const [showDismissed, setShowDismissed] = useState(false);
  const [snapshot, setSnapshot] = useState<CollabMailSnapshot | null>(null);
  const [chains, setChains] = useState<CollabMailChains | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // 任务587（失败态与空态分离）：loading = 读取进行中（不得冒充「暂无信件」），
  // loadError = 最近一次读取失败（显式报错 + 重试按钮，绝不渲染成空态）。
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  // 任务620（立即清理）：一次点击的反馈文案（已清理 N 封 / 没有可清理的信件），
  // 定时消失；计时器随面板卸载回收，绝不存活到组件外。
  const [cleanNote, setCleanNote] = useState<string | null>(null);
  const cleanNoteTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (cleanNoteTimer.current !== null) clearTimeout(cleanNoteTimer.current);
  }, []);

  useEffect(() => onCollabInboxOpenChange((next) => setOpen(next)), []);

  // 任务461-P4: the roster backs the from/to dropdowns — loaded while the
  // panel is open, failures degrade to an empty roster (「全部」 keeps working).
  // 任务462: the same rows also feed the shared contact-id→会话名 cache, so
  // transcript cards resolve names from data the panel already fetched.
  useEffect(() => {
    if (!open) return;
    let alive = true;
    dir.ListAddressableSessions()
      .then((rows) => {
        if (alive) {
          rememberCollabContactNames(rows);
          setSessions(Array.isArray(rows) ? rows : []);
        }
      })
      .catch((err) => {
        if (alive) setSessions([]);
        // 任务587：the roster degrades to an empty dropdown by design, but the
        // failure itself must stay diagnosable (console + ring buffer).
        reportFrontendLog("collab-inbox", "addressable roster failed", String(err), "warn");
      });
    return () => {
      alive = false;
    };
  }, [open, dir]);

  // 任务587（失败后自动恢复）：the retry loop lives in refs so re-creating
  // `refresh` (every filter change) never stacks timers, and the pending timer
  // always calls the LATEST refresh closure.
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
    const delays = collabInboxRetryDelaysMs;
    const delay = delays[Math.min(retryRef.current.attempt, delays.length - 1)];
    retryRef.current.timer = setTimeout(() => {
      retryRef.current.timer = null;
      retryRef.current.attempt += 1;
      refreshRef.current();
    }, delay);
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    // 任务587：the banner describes the LATEST attempt only — starting a new
    // read retires the previous attempt's error (it returns as a fresh
    // failure if this one fails too, and as data if it succeeds).
    setLoadError(null);
    try {
      if (view === "chains") {
        setChains(await b.ListCollabMailChains(bucket, 100));
      } else {
        setSnapshot(await b.ListCollabMail(bucket, from.trim(), to.trim(), state, 100, showDismissed, order));
      }
      // 任务587：success stops the recovery loop and resets its backoff for
      // the next failure.
      retryRef.current.attempt = 0;
      cancelRetry();
    } catch (err) {
      // A closed gateway must not crash the panel — it keeps the last
      // snapshot. 任务587：but a failure must never RENDER as 「暂无信件」 —
      // the panel now carries an explicit failure state (retry button below),
      // and the error travels console + ring buffer + desktop.log.
      setLoadError(String(err));
      reportFrontendLog("collab-inbox", "list failed",
        `view=${view} bucket=${bucket} state=${state} order=${order} from=${from.trim()} to=${to.trim()} dismissed=${showDismissed} err=${String(err)}`,
        "warn");
      scheduleRetry();
    } finally {
      setLoading(false);
    }
  }, [b, bucket, from, to, state, order, showDismissed, view, cancelRetry, scheduleRetry]);

  useEffect(() => {
    refreshRef.current = () => void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!open) {
      cancelRetry();
      return;
    }
    void refresh();
  }, [open, refresh, cancelRetry]);

  // unmount cleanup: no retry timer may outlive the panel.
  useEffect(() => cancelRetry, [cancelRetry]);

  // 任务461-P4: dropdown options — one addressable session per contact id,
  // labeled by session name, hover showing 项目 › 分组 › 会话名 › contact_id.
  // Rows without a contact id cannot reach the backend's exact-match filter,
  // so they are not offered (「全部」 stays the honest catch-all).
  const addressable = useMemo(() => {
    const seen = new Set<string>();
    const out: CollabSessionDirectoryRow[] = [];
    for (const row of sessions) {
      const contactId = (row.contactId || "").trim();
      if (!contactId || seen.has(contactId)) continue;
      seen.add(contactId);
      out.push(row);
    }
    return out;
  }, [sessions]);
  const globalLabel = t("collabInbox.hover.global");
  const renderFilterOptions = () => (
    <>
      <option value="">{t("collabInbox.bucket.all")}</option>
      {addressable.map((row) => (
        <option
          key={row.contactId}
          value={row.contactId}
          title={sessionHoverLabel(row, globalLabel)}
        >
          {row.title || row.contactId}
        </option>
      ))}
    </>
  );


  if (!open) return null;

  const act = async (fn: () => Promise<CollabMailSnapshot>) => {
    setBusy(true);
    try {
      setSnapshot(await fn());
      setView("list");
    } catch (err) {
      // keep the previous snapshot on failure; 任务587：the failure itself
      // still leaves a trace (console + ring buffer) instead of vanishing.
      reportFrontendLog("collab-inbox", "action failed", String(err), "warn");
    } finally {
      setBusy(false);
    }
  };

  // 任务620（立即清理）：与面板打开路径同一套写侧维护（保留期 + 清理规则，
  // 去任务511节流），后端一并带回清理后的新快照——失败保留旧快照并留痕。
  const cleanNow = async () => {
    setBusy(true);
    try {
      const result = await b.CleanCollabMailNow();
      setSnapshot(result.snapshot);
      setView("list");
      setCleanNote(t(result.removed > 0 ? "collabInbox.cleaned" : "collabInbox.cleanNothing", { n: result.removed }));
      if (cleanNoteTimer.current !== null) clearTimeout(cleanNoteTimer.current);
      cleanNoteTimer.current = setTimeout(() => setCleanNote(null), 6000);
    } catch (err) {
      reportFrontendLog("collab-inbox", "clean now failed", String(err), "warn");
    } finally {
      setBusy(false);
    }
  };

  const rows: CollabMailEntry[] = snapshot?.entries ?? [];

  // 任务587（失败态与空态分离）：四个展示位互斥——
  //   失败+无数据 → 显式错误块（含重试）；失败+有旧数据 → 旧数据照常渲染 +
  //   顶部细条提示「上次刷新失败」；读取中 → 「读取中」；健康空 → 「暂无信件」。
  const listEmpty = view === "list" && rows.length === 0;
  const chainsEmpty = view === "chains" && (chains?.chains?.length ?? 0) === 0;
  const viewEmpty = listEmpty || chainsEmpty;


  return createPortal(
    <div className="collab-inbox-panel" role="dialog" aria-label={t("collabInbox.title")}>
      {/* 任务716 ①（用户拖拽定稿）：头部=标题横排（nowrap 防窄容器竖排截断）+
          关闭按钮右上角；副标题按定稿③不设。 */}
      <div className="collab-inbox-panel__head">
        <span className="collab-inbox-panel__title">{t("collabInbox.title")}</span>
        <button type="button" className="btn btn--small" onClick={() => setCollabInboxOpen(false)}>
          {t("collabInbox.close")}
        </button>
      </div>

      {/* 任务716 ②（定稿第二位）：维护行——保留期 + 清理条件（定稿①②：原
          「会话删除时」是场景描述不作下拉前缀，更名为「清理条件」）+ 立即清理，
          暖色警示底让危险操作分组可达但不与浏览控件混杂。 */}
      <div className="collab-inbox-panel__maint">
        <span className="collab-inbox-panel__maintlabel">{t("collabInbox.maintLabel")}</span>
        <label className="collab-inbox-panel__retention">
          <span>{t("collabInbox.retention")}</span>
          <select
            value={snapshot?.settings?.retention ?? "7d"}
            disabled={busy}
            onChange={(event) => void act(() => b.SetCollabMailRetention(event.target.value))}
          >
            {RETENTIONS.map((r) => (
              <option key={r} value={r}>{t(`collabInbox.retention.${r}` as "collabInbox.retention.7d")}</option>
            ))}
          </select>
        </label>
        {/* 任务 464：清理条件四选一，与保留期正交（716 更名后选项文案去掉
            「…清理」后缀，与定稿「清理条件：收信方删除时」一致）。 */}
        <label className="collab-inbox-panel__retention">
          <span>{t("collabInbox.cleanupRule")}</span>
          <select
            value={snapshot?.settings?.cleanupRule ?? "never"}
            disabled={busy}
            onChange={(event) => void act(() => b.SetCollabMailCleanupRule(event.target.value))}
          >
            {CLEANUP_RULES.map((rule) => (
              <option key={rule} value={rule}>{t(`collabInbox.cleanup.${rule}` as "collabInbox.cleanup.never")}</option>
            ))}
          </select>
        </label>
        {/* 任务 620：立即清理——选完规则后手动触发一次批量清理，反馈就地可见。 */}
        <button
          type="button"
          className="btn btn--secondary btn--small collab-inbox-panel__cleannow"
          title={t("collabInbox.cleanNowHint")}
          disabled={busy}
          onClick={() => void cleanNow()}
        >
          {t("collabInbox.cleanNow")}
        </button>
        {cleanNote && <span className="collab-inbox-panel__cleannote">{cleanNote}</span>}
      </div>

      {/* 任务716 ③（定稿第三位）：视图工具条——视图切换 + 排序切换（649 ①的
          胶囊分段样式保留，落位从头行 actions 移到本条灰底工具条），全部已读
          属批量浏览动作随行。 */}
      <div className="collab-inbox-panel__toolbar">
        <span className="collab-inbox-panel__tlabel">{t("collabInbox.viewLabel")}</span>
        <div className="collab-inbox-panel__viewtoggle" role="group" aria-label={t("collabInbox.viewGroup")}>
          <button
            type="button"
            className={`collab-inbox-panel__state${view === "list" ? " collab-inbox-panel__state--on" : ""}`}
            onClick={() => setView("list")}
          >
            {t("collabInbox.view.list")}
          </button>
          <button
            type="button"
            className={`collab-inbox-panel__state${view === "chains" ? " collab-inbox-panel__state--on" : ""}`}
            onClick={() => setView("chains")}
          >
            {t("collabInbox.view.chains")}
          </button>
        </div>
        <span className="collab-inbox-panel__sep" aria-hidden="true" />
        <span className="collab-inbox-panel__tlabel">{t("collabInbox.sortLabel")}</span>
        <div className="collab-inbox-panel__ordertoggle" role="group" aria-label={t("collabInbox.sortByDate")}>
          {ORDERS.map((name) => (
            <button
              key={name}
              type="button"
              aria-pressed={order === name}
              className={`collab-inbox-panel__state${order === name ? " collab-inbox-panel__state--on" : ""}`}
              onClick={() => setOrder(name)}
            >
              {t(`collabInbox.order.${name}` as "collabInbox.order.desc")}
            </button>
          ))}
        </div>
        {rows.some((entry) => !entry.read) && (
          <button
            type="button"
            className="btn btn--secondary btn--small"
            disabled={busy}
            onClick={() => void act(() => b.MarkCollabMailRead(rows.filter((entry) => !entry.read).map((entry) => entry.id)))}
          >
            {t("collabInbox.markAllRead")}
          </button>
        )}
      </div>

      {/* 任务716 ④（定稿第四位）：桶过滤标签行。 */}
      <div className="collab-inbox-panel__buckets" role="tablist">
        {BUCKETS.map((name) => (
          <button
            key={name}
            type="button"
            role="tab"
            aria-selected={bucket === name}
            className={`collab-inbox-panel__bucket${bucket === name ? " collab-inbox-panel__bucket--on" : ""}`}
            onClick={() => setBucket(name)}
          >
            {t(`collabInbox.bucket.${name}` as "collabInbox.bucket.all")}
          </button>
        ))}
      </div>

      {bucket === "approval" && (
        <div className="collab-inbox-panel__states">
          {STATES.map((name) => (
            <button
              key={name}
              type="button"
              className={`collab-inbox-panel__state${state === name ? " collab-inbox-panel__state--on" : ""}`}
              onClick={() => setState(name)}
            >
              {t(`collabInbox.state.${name}` as "collabInbox.state.all")}
            </button>
          ))}
        </div>
      )}

      <div className="collab-inbox-panel__filters">
        {/* 任务461-P4: from/to 过滤改下拉 —— 选项=会话名（值=contact_id，与后端
            精确匹配口径一致），hover 显示 项目 › 分组 › 会话名 › contact_id，
            顶部「全部」= 不过滤。消除「不知填会话名还是 id」的误导。
            任务 649 ②：下拉左侧补「发信方/收信方」可见标签词（此前只有会话名，
            左右两个下拉无文字说明，用户截图实证分不清哪边是发信方）。 */}
        <label className="collab-inbox-panel__filterwrap">
          <span>{t("collabInbox.senderLabel")}</span>
          <select
            className="collab-inbox-panel__filter"
            aria-label={t("collabInbox.from")}
            value={from}
            onChange={(event) => setFrom(event.target.value)}
          >
            {renderFilterOptions()}
          </select>
        </label>
        <label className="collab-inbox-panel__filterwrap">
          <span>{t("collabInbox.recipientLabel")}</span>
          <select
            className="collab-inbox-panel__filter"
            aria-label={t("collabInbox.to")}
            value={to}
            onChange={(event) => setTo(event.target.value)}
          >
            {renderFilterOptions()}
          </select>
        </label>
        <label className="collab-inbox-panel__showdismissed">
          <input
            type="checkbox"
            checked={showDismissed}
            onChange={(event) => setShowDismissed(event.target.checked)}
          />
          {t("collabInbox.showDismissed")}
        </label>
      </div>

      <div className="collab-inbox-panel__rows">
        {/* 任务587：读取失败但有旧数据 → 旧数据优先（可用性>新鲜度），顶部细条
            如实声明「显示的不是最新结果」，附重试。 */}
        {loadError && !viewEmpty && (
          <div className="collab-inbox-panel__errorstrip" role="alert">
            <span>{t("collabInbox.error.stale")}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>
              {t("collabInbox.retry")}
            </button>
          </div>
        )}
        {/* 任务587：读取失败且无数据 → 显式失败块（错误原文在 title/详情里，
            绝不冒充「暂无信件」）。 */}
        {viewEmpty && loadError && (
          <div className="collab-inbox-panel__error" role="alert">
            <span className="collab-inbox-panel__error-label">{t("collabInbox.error")}</span>
            <span className="collab-inbox-panel__error-detail" title={loadError}>{loadError}</span>
            <button type="button" className="btn btn--secondary btn--small" onClick={() => void refresh()}>
              {t("collabInbox.retry")}
            </button>
          </div>
        )}
        {view === "list" && rows.length === 0 && !loadError && (
          <div
            className={`collab-inbox-panel__empty${snapshot?.degraded ? " collab-inbox-panel__empty--degraded" : ""}`}
          >
            {/* 任务511：锁繁忙导致的空必须与「真空」可区分——降级提示代替
                「暂无信件」，否则空面板依旧无从诊断。 */}
            {/* 任务587：读取进行中同样不冒充「暂无信件」——in-flight / 失败 /
                健康空 三态分开展示。 */}
            {snapshot?.degraded
              ? t("collabInbox.degraded")
              : loading
                ? t("collabInbox.loading")
                : t("collabInbox.empty")}
          </div>
        )}
        {view === "list" &&
          rows.map((entry) => (
            <div key={entry.id} className={`collab-inbox-panel__row${entry.dismissed ? " collab-inbox-panel__row--dismissed" : ""}`}>
              <div className="collab-inbox-panel__meta">
                <span className={`collab-inbox-panel__bucketbadge collab-inbox-panel__bucketbadge--${entry.bucket}`}>
                  {t(`collabInbox.bucket.${entry.bucket as Bucket}` as "collabInbox.bucket.all")}
                </span>
                {entry.channel && (
                  <span className="collab-inbox-panel__channel" title={t("collabInbox.channel")}>
                    #{entry.channel}
                  </span>
                )}
                {/* 任务462: 双方显示会话名（查不到降级截短 id），完整
                    contact_id 保留在 hover 里。
                    任务 649 ②：补「发信方/收信方」标签词——箭头两侧只有会话名
                    时分不清谁发谁收（用户截图实证），横向空间足够，就地标注。 */}
                <span
                  className="collab-inbox-panel__route"
                  title={`${t("collabInbox.senderLabel")} ${contactHoverLabel(contactDisplayName(entry.from), entry.from)} → ${t("collabInbox.recipientLabel")} ${contactHoverLabel(contactDisplayName(entry.to, entry.toTitle), entry.to)}`}
                >
                  <span className="collab-inbox-panel__routelabel">{t("collabInbox.senderLabel")}</span>
                  {" "}{contactDisplayName(entry.from)}
                  {" → "}
                  <span className="collab-inbox-panel__routelabel">{t("collabInbox.recipientLabel")}</span>
                  {" "}{contactDisplayName(entry.to, entry.toTitle)}
                </span>
                <span className="collab-inbox-panel__time">{formatTime(entry.at)}</span>
                <span className={`collab-inbox-panel__read${entry.read ? " collab-inbox-panel__read--on" : ""}`}>
                  {entry.read ? t("collabInbox.read") : t("collabInbox.unread")}
                </span>
                {!!entry.duplicateCount && entry.duplicateCount > 1 && (
                  <span className="collab-inbox-panel__dupcount" title={t("collabInbox.duplicateCount")}>
                    ×{entry.duplicateCount}
                  </span>
                )}
                {entry.decidedBy && (
                  <span className="collab-inbox-panel__decided">
                    {t("collabInbox.decidedBy", { by: entry.decidedBy })}
                  </span>
                )}
                {entry.pendingMe && !entry.decidedBy && (
                  <span className="collab-inbox-panel__pending">{t("collabInbox.state.pendingMe")}</span>
                )}
              </div>
              <div className="collab-inbox-panel__preview">{entry.preview}</div>
              <div className="collab-inbox-panel__rowactions">
                {entry.bucket === "approval" && !entry.decidedBy && !entry.dismissed && (
                  <button
                    type="button"
                    className="btn btn--primary btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.MarkCollabMailDecided(entry.id, "human"))}
                  >
                    {t("collabInbox.decide")}
                  </button>
                )}
                {!entry.dismissed ? (
                  <button
                    type="button"
                    className="btn btn--secondary btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.DismissCollabMail([entry.id]))}
                  >
                    {t("collabInbox.dismiss")}
                  </button>
                ) : (
                  <button
                    type="button"
                    className="btn btn--secondary btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.UndismissCollabMail([entry.id]))}
                  >
                    {t("collabInbox.restore")}
                  </button>
                )}
              </div>
            </div>
          ))}

        {view === "chains" && (chains?.chains?.length ?? 0) === 0 && !loadError && (
          <div className="collab-inbox-panel__empty">
            {/* 任务587：链视图同样区分读取中与真空。 */}
            {loading ? t("collabInbox.loading") : t("collabInbox.empty")}
          </div>
        )}
        {view === "chains" &&
          (chains?.chains ?? []).map((chain) => (
            <div key={chain.threadId} className="collab-inbox-panel__chain">
              <button
                type="button"
                className="collab-inbox-panel__chainhead"
                onClick={() => setExpanded(expanded === chain.threadId ? null : chain.threadId)}
              >
                <span className="collab-inbox-panel__chaincount">
                  {t("collabInbox.rounds", { n: chain.count })}
                </span>
                {/* 任务462: 参与者显示会话名（降级截短 id），hover 留完整 id。 */}
                <span
                  className="collab-inbox-panel__chainroute"
                  title={chain.participants.map((p) => contactHoverLabel(contactDisplayName(p), p)).join(" ↔ ")}
                >
                  {chain.participants.map((p) => contactDisplayName(p)).join(" ↔ ")}
                </span>
                <span className="collab-inbox-panel__time">{formatTime(chain.lastAt)}</span>
              </button>
              {expanded === chain.threadId && (
                <div className="collab-inbox-panel__chainbody">
                  {chain.entries.map((entry) => (
                    <div key={entry.id} className="collab-inbox-panel__chainentry">
                      {entry.channel && (
                        <span className="collab-inbox-panel__channel" title={t("collabInbox.channel")}>
                          #{entry.channel}
                        </span>
                      )}
                      {/* 任务462: 发方显示会话名，hover 留完整 contact_id。 */}
                      <span
                        className="collab-inbox-panel__route"
                        title={contactHoverLabel(contactDisplayName(entry.from), entry.from)}
                      >
                        {contactDisplayName(entry.from)}
                      </span>
                      <span className="collab-inbox-panel__preview">{entry.preview}</span>
                      <span className="collab-inbox-panel__time">{formatTime(entry.at)}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
      </div>

      <div className="collab-inbox-panel__foot">
        <span className="collab-inbox-panel__revision">
          {t("collabInbox.revision", { rev: snapshot?.revision ?? chains?.revision ?? "-" })}
        </span>
        <span className="collab-inbox-panel__note">{t("collabInbox.note")}</span>
      </div>
    </div>,
    document.body,
  );
}
