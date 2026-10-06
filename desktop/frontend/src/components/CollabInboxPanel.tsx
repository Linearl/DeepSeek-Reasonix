// 跨会话收件箱面板 (task 320): the one place that shows every cross-session
// mail — five-bucket aggregation (views, not storage), sender/recipient/date
// filtering with an asc/desc date toggle (a), revision-stamped dismiss
// (contract ①), retention picker, approval sub-states with their decider
// recorded (task 320 d), and the thread-chain view (task 320 g). The panel is
// a pure consumer of the Go index: everything
// it shows already sits in a recipient inbox — a queued send is not an entry
// (contract ②), and dismissed/decided/retention state lives on disk, so a
// restart keeps all of it (contract f).

import { useCallback, useEffect, useMemo, useState } from "react";
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
        .catch(() => { /* closed gateway: keep the last count */ });
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
      .catch(() => {
        if (alive) setSessions([]);
      });
    return () => {
      alive = false;
    };
  }, [open, dir]);

  const refresh = useCallback(async () => {
    try {
      if (view === "chains") {
        setChains(await b.ListCollabMailChains(bucket, 100));
      } else {
        setSnapshot(await b.ListCollabMail(bucket, from.trim(), to.trim(), state, 100, showDismissed, order));
      }
    } catch (err) {
      // A closed gateway must not crash the panel — it keeps the last
      // snapshot. But a silently swallowed failure made "badge says N unread,
      // panel shows nothing" undiagnosable, so every failure now travels the
      // frontend log channel (desktop.log) with its query context.
      reportFrontendLog("collab-inbox", "list failed",
        `view=${view} bucket=${bucket} state=${state} order=${order} from=${from.trim()} to=${to.trim()} dismissed=${showDismissed} err=${String(err)}`);
    }
  }, [b, bucket, from, to, state, order, showDismissed, view]);

  useEffect(() => {
    if (!open) return;
    void refresh();
  }, [open, refresh]);

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
    } catch {
      // keep the previous snapshot on failure
    } finally {
      setBusy(false);
    }
  };

  const rows: CollabMailEntry[] = snapshot?.entries ?? [];


  return createPortal(
    <div className="collab-inbox-panel" role="dialog" aria-label={t("collabInbox.title")}>
      <div className="collab-inbox-panel__head">
        <span className="collab-inbox-panel__title">{t("collabInbox.title")}</span>
        <div className="collab-inbox-panel__actions">
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
          {/* 任务 464：会话删除时的清理语义（四选一，与保留期正交）。 */}
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
          {rows.some((entry) => !entry.read) && (
            <button
              type="button"
              className="btn btn--small"
              disabled={busy}
              onClick={() => void act(() => b.MarkCollabMailRead(rows.filter((entry) => !entry.read).map((entry) => entry.id)))}
            >
              {t("collabInbox.markAllRead")}
            </button>
          )}
          <button type="button" className="btn btn--small" onClick={() => setCollabInboxOpen(false)}>
            {t("collabInbox.close")}
          </button>
        </div>
      </div>

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
            顶部「全部」= 不过滤。消除「不知填会话名还是 id」的误导。 */}
        <select
          className="collab-inbox-panel__filter"
          aria-label={t("collabInbox.from")}
          value={from}
          onChange={(event) => setFrom(event.target.value)}
        >
          {renderFilterOptions()}
        </select>
        <select
          className="collab-inbox-panel__filter"
          aria-label={t("collabInbox.to")}
          value={to}
          onChange={(event) => setTo(event.target.value)}
        >
          {renderFilterOptions()}
        </select>
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
        <div className="collab-inbox-panel__viewtoggle">
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
        {view === "list" && rows.length === 0 && (
          <div
            className={`collab-inbox-panel__empty${snapshot?.degraded ? " collab-inbox-panel__empty--degraded" : ""}`}
          >
            {/* 任务511：锁繁忙导致的空必须与「真空」可区分——降级提示代替
                「暂无信件」，否则空面板依旧无从诊断。 */}
            {snapshot?.degraded ? t("collabInbox.degraded") : t("collabInbox.empty")}
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
                    contact_id 保留在 hover 里。 */}
                <span
                  className="collab-inbox-panel__route"
                  title={`${contactHoverLabel(contactDisplayName(entry.from), entry.from)} → ${contactHoverLabel(contactDisplayName(entry.to, entry.toTitle), entry.to)}`}
                >
                  {contactDisplayName(entry.from)} → {contactDisplayName(entry.to, entry.toTitle)}
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
                    className="btn btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.MarkCollabMailDecided(entry.id, "human"))}
                  >
                    {t("collabInbox.decide")}
                  </button>
                )}
                {!entry.dismissed ? (
                  <button
                    type="button"
                    className="btn btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.DismissCollabMail([entry.id]))}
                  >
                    {t("collabInbox.dismiss")}
                  </button>
                ) : (
                  <button
                    type="button"
                    className="btn btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.UndismissCollabMail([entry.id]))}
                  >
                    {t("collabInbox.restore")}
                  </button>
                )}
              </div>
            </div>
          ))}

        {view === "chains" && (chains?.chains?.length ?? 0) === 0 && (
          <div className="collab-inbox-panel__empty">{t("collabInbox.empty")}</div>
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
