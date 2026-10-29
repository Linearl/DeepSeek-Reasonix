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
// 任务 980 ③（用户验收实测）：详情弹窗——列表条目/预览行点开 → portal 弹窗
// 展示全文（ReadCollabMail 按需取全文；列表保持 preview-only 设计不变）。
// 任务 980 ②：协作中心嵌入形态（embedded in Hub）的列表滚动由 Hub 壳层 CSS
// 承担（.collab-hub-panel__body 后代规则 overflow:auto），本组件零改动。

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useConfirmDialog } from "./ConfirmDialog";
import { app } from "../lib/bridge";
import { collabTitleFor, rememberCollabContactNames, shortContactId } from "../lib/collabContactNames";
import { reportFrontendLog } from "../lib/frontendLog";
import { useT } from "../lib/i18n";
import { groupForProject, loadProjectGroupAssign, loadProjectGroups } from "../lib/projectGroups";
import { onCollabMailChanged as onCollabMailChangedRuntime } from "../lib/inboxEvents";
import { acquireOverlayOrder } from "../lib/stackingOrder";

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
  /** 任务 349n1 群标识：fan-out 来源频道名（空 = 点对点信）。
   *  任务 1016：意见箱通知也走该字段（值为「意见箱」），渲染成来源标签。 */
  channel?: string;
  /** 任务 1016：条目引用的文件绝对路径（意见箱通知=对应 md），非空时
   *  渲染「打开来源」动作（复用 RevealPath）。 */
  refPath?: string;
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

/** 任务 980 ③：单封信件的全文详情（collabinbox.MailDetail）——列表保持
 * preview-only（性能与 320 设计意图不变），弹窗按需取全文。 */
export type CollabMailDetail = {
  id: string;
  from: string;
  fromSession?: string;
  to: string;
  toTitle?: string;
  at: number;
  threadId: string;
  bucket: string;
  channel?: string;
  preview: string;
  /** 任务 1016：详情弹窗同样携带引用文件路径（意见箱通知=对应 md）。 */
  refPath?: string;
  /** 完整正文，无截断。 */
  body: string;
};

export type CollabMailChain = {
  threadId: string;
  participants: string[];
  count: number;
  firstAt: number;
  lastAt: number;
  preview: string;
  /** 任务 1016：链首条目的引用文件路径（意见箱通知=对应 md）。 */
  refPath?: string;
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
  /** 任务 980 ③：详情弹窗的全文读口。可选 = 向前兼容缝——旧 bindings fake /
   * 旧网关面缺方法时面板降级为「预览即正文」展示，不崩不阻塞（生产 bridge
   * 恒有此方法）。 */
  ReadCollabMail?(id: string): Promise<CollabMailDetail>;
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
  /** 任务 856 ②：批量删除——物理移除所选留档（对话留档语义：不影响已注入内容），
   * 返回新快照；折叠簇随主条目整体删除。 */
  DeleteCollabMail(ids: string[]): Promise<CollabMailSnapshot>;
  /** 任务 856 ②：清空全部——物理清空全部留档（含已处理），返回移除数与新快照（620 形状）。 */
  ClearAllCollabMail(): Promise<CollabInboxCleanResult>;
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

/**
 * tl1230（用户钦定方案）：信件列表首屏只加载 20 条，滚动到接近底部再增量
 * 加载——「信件读取中…」长时间不出数据的 UI 侧根治：首屏小页 + 渲染减负。
 * 后端 List 本就支持任意 limit（hard max 500，collab_inbox_app.go 注释契约），
 * 本改动零后端变更。collabInboxMaxPageSize 对齐后端 hard max：达到后停止增
 * 长——后端给不出更多，必须由前端自停，否则滚动会反复空拉。
 */
export const collabInboxFirstPageSize = 20;
export const collabInboxMaxPageSize = 500;

export function isCollabInboxOpen(): boolean {
  return panelOpen;
}

/** Opens/closes the panel (settings entry point, task 320).
 * 任务 856 ①：注册 forward 后本调用升级为「打开协作中心（信件视图）」的
 * 外部信号——调用方（设置页副入口、派单通知等）零改动。 */
export function setCollabInboxOpen(next: boolean): void {
  // 任务 856 ①：本地 listeners 只在真实变化时通知（幂等保护），但 forward
  // 同值也转发——Hub 已开在聊天视图时，设置页的「打开信件中心」（同值 true）
  // 仍要能切回信件视图。
  if (panelOpen === next) {
    openForward?.(next);
    return;
  }
  panelOpen = next;
  for (const listener of openListeners) listener(next);
  openForward?.(next);
}

// 任务 856 ①：打开信号转发缝——协作中心（CollabHubPanel）挂载后把本面板的
// open 信号重定向为「打开 Hub 并切到信件视图」。转发发生在本地 listeners
// 通知之后；未注册时（独立面板模式，含全部既有测试）行为与 320 原样一致。
let openForward: ((next: boolean) => void) | null = null;

/** 任务 856 ①：CollabHubPanel 模块初始化时注册（传 null 撤销）。 */
export function forwardCollabInboxOpen(fn: ((next: boolean) => void) | null): void {
  openForward = fn;
}

/** 任务 856 ①：Hub 开合同步本面板模块 open（通知 listeners 使未读徽标的
 * 「面板开/合即刷」时机在 Hub 开/合时保持，但不触发 forward——防递归）。 */
export function syncCollabInboxOpen(next: boolean): void {
  if (panelOpen === next) return;
  panelOpen = next;
  for (const listener of openListeners) listener(next);
}

export function onCollabInboxOpenChange(cb: (open: boolean) => void): () => void {
  openListeners.add(cb);
  return () => openListeners.delete(cb);
}

// tl1226（用户验收实测「全部已读后角标不清零」）：面板内写操作（已读/消除/
// 恢复/裁决/保留期/清理规则/清理/删除/清空）成功后广播——未读角标 hook 订阅
// 本通知即时重查 CountUnreadCollabMail。计数真相源始终是后端（inbox.jsonl +
// seen.json），这里只补「写侧→读侧」的刷新通路，不造第二份计数状态。
const mailChangedListeners = new Set<() => void>();

export function notifyCollabMailChanged(): void {
  for (const listener of mailChangedListeners) listener();
}

export function onCollabMailChanged(cb: () => void): () => void {
  mailChangedListeners.add(cb);
  return () => mailChangedListeners.delete(cb);
}

/**
 * 任务 320 遗留 #1：图标行收件箱按钮的未读徽标数 —— 未读（收件方 seen 游标
 * 未覆盖）且未消除的统一表条目数，与面板默认视图的「待处理」口径一致。
 * 刷新时机（派单钦定 + tl1226 补口 + tl1226b 补口）：挂载时 + 面板每次开/合
 * 切换时 + 面板内任一写操作成功广播时 + 后端投递 CollabMailChanged 事件到达
 * 时（tl1226b ①「新信到达角标即涨」：投递侧观察者经 Wails 事件通知，不引轮
 * 询）——打开路径会应用保留期（可能清理旧信），关闭路径紧随刚发生的已读/消
 * 除，写广播让「全部已读」等面板内动作不必等开/合即清角标，投递广播让新信
 * 不必等任何面板交互即涨角标。网关不可达时保留上次数值（与面板 refresh 的
 * 容错同款）。
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
    const offOpen = onCollabInboxOpenChange(refresh);
    const offChanged = onCollabMailChanged(refresh);
    const offArrived = onCollabMailChangedRuntime(refresh);
    return () => { alive = false; offOpen(); offChanged(); offArrived(); };
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

// 任务 1016：来源标签文案——意见箱频道给专属说明（区别于群聊频道的
// 「群聊频道」title），其余频道沿用既有文案。
function channelLabel(channel: string, t: ReturnType<typeof useT>): string {
  if (channel === "意见箱") return t("collabInbox.channelFeedback");
  return t("collabInbox.channel");
}

// 任务 1016 增强项②：打开条目引用的文件（意见箱通知=对应 md）。复用
// RevealPath（文件管理器定位），失败走前端日志留痕（与 587 的静默失败防线
// 同源——失败可见，不装作成功）。
async function revealRef(refPath?: string): Promise<void> {
  const path = (refPath ?? "").trim();
  if (!path) return;
  try {
    await app.RevealPath(path);
  } catch (err) {
    reportFrontendLog("collab-inbox", "reveal ref failed", `${path} err=${String(err)}`, "warn");
  }
}

export function CollabInboxPanel({ bindings, directory, embedded }: { bindings?: CollabInboxBindings; directory?: CollabSessionDirectory; embedded?: boolean } = {}) {
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
  // 任务 856 ①：embedded（协作中心信件视图）= 渲染由 Hub 决定，忽略模块 open。
  const effectiveOpen = Boolean(embedded) || open;
  const [bucket, setBucket] = useState<Bucket>("all");
  const [state, setState] = useState<StateFilter>("all");
  const [order, setOrder] = useState<OrderFilter>("desc");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [sessions, setSessions] = useState<CollabSessionDirectoryRow[]>([]);
  const [view, setView] = useState<"list" | "chains">("list");
  // 任务 856 ②：批量选择模式——list 视图选中 entry.id，chains 视图选中
  // threadId（删除时展开为链内全部信件 id，=「按链选择=删除链内全部信件」）。
  const [multi, setMulti] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [showDismissed, setShowDismissed] = useState(false);
  const [snapshot, setSnapshot] = useState<CollabMailSnapshot | null>(null);
  // tl1230：列表懒加载水位——首屏 20 条，滚动近底增量 +20，写操作回读快照
  // 可能带更大的默认页（后端默认 limit 50），此时水位上对齐防「越滚越少」。
  const [pageSize, setPageSize] = useState(collabInboxFirstPageSize);
  // tl1230：滚动面板根（.collab-inbox-panel 自身是 overflow:auto 的滚动容器，
  // 嵌入形态亦同）。
  const panelScrollRef = useRef<HTMLDivElement | null>(null);
  // tl1230：refresh 在途标记——滚动连发时一页未回不放下一页（快照整体替换，
  // 并发回包乱序会把小页后写覆盖大页）。
  const fetchingRef = useRef(false);
  const [chains, setChains] = useState<CollabMailChains | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // 任务 856 ②：删除/清空的二次确认（项目统一 ConfirmDialog，danger 声调）。
  const { confirm, dialog: confirmDialog } = useConfirmDialog();
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

  // 任务 980 ③（用户验收实测）：详情弹窗——点信件条目/预览行 → 弹窗展示全文。
  // full=null（尚未取回或绑定面缺读口走预览降级）、detailLoading=取全文中、
  // error=取全文失败（弹窗内显式报错 + 重试，绝不把「读不到」渲染成空正文，
  // 与 587 空/败分离同口径）。entry 骨架先行渲染（发/收/时间即刻可见）。
  const [detail, setDetail] = useState<{ entry: CollabMailEntry; full: CollabMailDetail | null } | null>(null);
  // 任务 986：详情弹窗层内 LIFO 序——打开时取单调递增号，点击置前重新取号，
  // zIndex = calc(--z-popover-elevated + order)（后弹在前 + 点击置前，用户两原则）。
  const [detailOrder, setDetailOrder] = useState(0);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);

  const openDetail = useCallback(async (entry: CollabMailEntry) => {
    const read = b.ReadCollabMail;
    setDetail({ entry, full: null });
    setDetailOrder(acquireOverlayOrder());
    setDetailError(null);
    // tl1238 ②：点开查看即已读——详情弹窗打开的瞬间结算该信的已读游标
    //（复用既有 MarkCollabMailRead 写口：后端推进 seen 游标、回读新快照），
    // 并广播 mail-changed 令未读角标即时重查。用户实测「点开看完、面板关了
    // 角标仍挂 27 封」的根因 = 查看路径从不推进游标（此前唯一已读口是工具条
    // 「全部已读」），角标计数与用户的已读认知长期不对账。邮件客户端标准范式
    // （打开即已读）：读到哪封结算哪封，未读态仍逐信有意义，批量口保持不变。
    // 已读条目不再发写（零多余往返）；失败保留快照并留痕（与 act 同款容错，
    // 不阻塞全文读取）；ReadCollabMail 的只读契约不动（结算走写口，不在读口
    // 搭车）。后端零改动。
    if (!entry.read) {
      void b.MarkCollabMailRead([entry.id])
        .then((next) => {
          setSnapshot(next);
          // 写回读是后端默认页（tl1230 同款水位对齐，防越滚越少）。
          setPageSize((size) => Math.max(size, next.returned));
          notifyCollabMailChanged();
        })
        .catch((err) => {
          reportFrontendLog("collab-inbox", "read-on-view failed", `id=${entry.id} err=${String(err)}`, "warn");
        });
    }
    if (typeof read !== "function") {
      // 向前兼容缝：无全文口 → 弹窗展示预览行（不空白、不崩）。
      setDetailLoading(false);
      return;
    }
    setDetailLoading(true);
    try {
      const full = await read.call(b, entry.id);
      setDetail((cur) => (cur?.entry.id === entry.id ? { ...cur, full } : cur));
    } catch (err) {
      reportFrontendLog("collab-inbox", "detail read failed", `id=${entry.id} err=${String(err)}`, "warn");
      setDetailError(String(err));
    } finally {
      setDetailLoading(false);
    }
  }, [b]);

  // Esc 关闭详情弹窗（capture 相 + stopPropagation = 只关弹窗，不连带触发
  // Hub 外点/Esc 关闭——协作中心壳层的 Esc 在 document bubble 相，980 ①）。
  useEffect(() => {
    if (!detail) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      setDetail(null);
    };
    document.addEventListener("keydown", onKey, true);
    return () => document.removeEventListener("keydown", onKey, true);
  }, [detail]);

  // 任务461-P4: the roster backs the from/to dropdowns — loaded while the
  // panel is open, failures degrade to an empty roster (「全部」 keeps working).
  // 任务462: the same rows also feed the shared contact-id→会话名 cache, so
  // transcript cards resolve names from data the panel already fetched.
  useEffect(() => {
    if (!effectiveOpen) return;
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
  }, [effectiveOpen, dir]);

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
    // tl1230：在途标记先立——滚动触发的新页请求期间，再次近底滚动不再放页。
    fetchingRef.current = true;
    setLoading(true);
    // 任务587：the banner describes the LATEST attempt only — starting a new
    // read retires the previous attempt's error (it returns as a fresh
    // failure if this one fails too, and as data if it succeeds).
    setLoadError(null);
    try {
      if (view === "chains") {
        setChains(await b.ListCollabMailChains(bucket, 100));
      } else {
        // tl1230：limit=pageSize（首屏 20，滚动递增），offset 恒 0——快照整
        // 体替换保持单一真相源，过滤/排序/revision 语义不因拼页漂移。
        setSnapshot(await b.ListCollabMail(bucket, from.trim(), to.trim(), state, pageSize, showDismissed, order));
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
      fetchingRef.current = false;
      setLoading(false);
    }
  }, [b, bucket, from, to, state, order, showDismissed, view, pageSize, cancelRetry, scheduleRetry]);

  useEffect(() => {
    refreshRef.current = () => void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!effectiveOpen) {
      cancelRetry();
      return;
    }
    void refresh();
  }, [effectiveOpen, refresh, cancelRetry]);

  // unmount cleanup: no retry timer may outlive the panel.
  useEffect(() => cancelRetry, [cancelRetry]);

  // ── tl1230：信件列表懒加载（首屏 20 条 + 滚动近底增量 +20）──────────────
  // 过滤/排序/视图任一变化都回到首屏页（新查询从头翻页，水位沿用旧值会把新
  // 查询直接放到第 N 页的语义）。
  const resetPaging = useCallback(() => {
    setPageSize(collabInboxFirstPageSize);
  }, []);

  const growPage = useCallback(() => {
    // busy（写操作在途）与 fetchingRef（读在途）都挡：快照整体替换方案下，
    // 并发读回包乱序会把小页后写覆盖大页。到后端 hard max 自停（setPageSize
    // 同值 bail-out，不会再触发 refresh，自动续载效应不会成环）。
    if (busy || fetchingRef.current) return;
    setPageSize((size) => (size >= collabInboxMaxPageSize ? size : size + collabInboxFirstPageSize));
  }, [busy]);

  const onPanelScroll = useCallback(() => {
    // 已到底（Truncated 翻假）就不再放页——滚动事件在满载后仍会连发，不能让
    // 它们反复空拉。
    if (!snapshot?.truncated) return;
    const el = panelScrollRef.current;
    if (!el) return;
    // 距底 64px 内视为「想看更多」。滚动容器是面板根本体（overflow:auto，
    // 独立/嵌入两形态一致，见 styles.css .collab-inbox-panel）。
    if (el.scrollTop + el.clientHeight < el.scrollHeight - 64) return;
    growPage();
  }, [growPage, snapshot]);

  // tl1230：截断态但内容不足以滚动（大屏 + 矮列表）时自动续载一页——否则
  // 「永远到不了底」，剩余信件成死胡同。无布局度量（clientHeight=0，含测试
  // harness）不介入，jsdom 下该效应恒惰性、由测试显式驱动滚动。
  useEffect(() => {
    if (loading || busy || view !== "list") return;
    if (!snapshot?.truncated) return;
    const el = panelScrollRef.current;
    if (!el || el.clientHeight <= 0) return;
    if (el.scrollHeight > el.clientHeight) return;
    growPage();
  }, [snapshot, loading, busy, view, growPage]);

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


  if (!effectiveOpen) return null;

  const act = async (fn: () => Promise<CollabMailSnapshot>) => {
    setBusy(true);
    try {
      const next = await fn();
      setSnapshot(next);
      // tl1230：写操作回读快照是后端默认页（默认 limit 50）——可能比当前已
      // 加载页更高。水位上对齐到回读页，防后续滚动增量反而缩小列表。
      setPageSize((size) => Math.max(size, next.returned));
      // tl1226：写成功即广播（已读/消除/恢复/裁决/保留期/清理规则都走本口），
      // 未读角标 hook 收到后重查 CountUnreadCollabMail——「全部已读」后角标
      // 不必等面板开/合即清零。
      notifyCollabMailChanged();
      setView("list");
    } catch (err) {
      // keep the previous snapshot on failure; 任务587：the failure itself
      // still leaves a trace (console + ring buffer) instead of vanishing.
      reportFrontendLog("collab-inbox", "action failed", String(err), "warn");
    } finally {
      setBusy(false);
    }
  };

  // tl1230：懒加载后「全部已读」的口径仍是「当前视图全部未读」——截断态先
  // 取一页全量（后端 hard max）再汇总未读 id，防止 27 封未读只清掉已加载的
  // 20 封（tl1226 用户实测「全部已读→角标清零」在懒加载下的复发形态）。
  // 取全量失败退回已加载页并留痕：至少读完看得见的，不静默吞。
  const markAllRead = async () => {
    let ids = rows.filter((entry) => !entry.read).map((entry) => entry.id);
    if (snapshot?.truncated) {
      try {
        const full = await b.ListCollabMail(bucket, from.trim(), to.trim(), state, collabInboxMaxPageSize, showDismissed, order);
        ids = (full.entries ?? []).filter((entry) => !entry.read).map((entry) => entry.id);
      } catch (err) {
        reportFrontendLog("collab-inbox", "mark-all-read full fetch failed", String(err), "warn");
      }
    }
    if (ids.length === 0) return;
    await act(() => b.MarkCollabMailRead(ids));
  };

  // 任务620（立即清理）：与面板打开路径同一套写侧维护（保留期 + 清理规则，
  // 去任务511节流），后端一并带回清理后的新快照——失败保留旧快照并留痕。
  const cleanNow = async () => {
    setBusy(true);
    try {
      const result = await b.CleanCollabMailNow();
      setSnapshot(result.snapshot);
      // tl1230：与 act 同款水位对齐（清理回读也是后端默认页）。
      setPageSize((size) => Math.max(size, result.snapshot.returned));
      // tl1226：清理物理移除信件，未读数可能变小——同样广播给角标。
      notifyCollabMailChanged();
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

  // 任务 856 ②：批量删除——二次确认（带「对话留档不影响已注入内容」语义句，
  // demo v2 钦定文案）→ 物理删除 → 退出多选并刷新。chains 视图把选中的
  // threadId 展开为链内全部信件 id。
  const deleteSelected = async () => {
    if (selected.size === 0) return;
    let ids = [...selected];
    if (view === "chains") {
      const chainsById = new Map((chains?.chains ?? []).map((c) => [c.threadId, c]));
      const expanded: string[] = [];
      for (const tid of selected) {
        for (const e of chainsById.get(tid)?.entries ?? []) expanded.push(e.id);
      }
      ids = expanded;
      if (ids.length === 0) return;
    }
    const ok = await confirm({
      title: t("collabInbox.deleteConfirmTitle", { n: view === "chains" ? ids.length : selected.size }),
      message: (
        <>
          {t("collabInbox.deleteConfirmBody")}
          <span className="collab-inbox-panel__confirmnote">{t("collabInbox.deleteConfirmNote")}</span>
        </>
      ),
      confirmLabel: t("collabInbox.deleteSelected"),
      cancelLabel: t("collabInbox.cancel"),
      tone: "danger",
    });
    if (!ok) return;
    setBusy(true);
    try {
      setSnapshot(await b.DeleteCollabMail(ids));
      // tl1226：删除移除留档（含未读），广播角标重查。
      notifyCollabMailChanged();
      setMulti(false);
      setSelected(new Set());
    } catch (err) {
      reportFrontendLog("collab-inbox", "delete failed", String(err), "warn");
    } finally {
      setBusy(false);
    }
  };

  // 任务 856 ②：清空全部——并入清理条件区的入口 + 二次确认（不可撤销；
  // 同样的「仅清留档」语义句）。
  const clearAll = async () => {
    const total = snapshot?.total ?? 0;
    const ok = await confirm({
      title: t("collabInbox.clearAllConfirmTitle"),
      message: (
        <>
          {t("collabInbox.clearAllConfirmBody", { n: total })}
          <span className="collab-inbox-panel__confirmnote">{t("collabInbox.deleteConfirmNote")}</span>
        </>
      ),
      confirmLabel: t("collabInbox.clearAll"),
      cancelLabel: t("collabInbox.cancel"),
      tone: "danger",
    });
    if (!ok) return;
    setBusy(true);
    try {
      const result = await b.ClearAllCollabMail();
      setSnapshot(result.snapshot);
      // tl1226：清空全部留档，未读必然归零——广播角标重查。
      notifyCollabMailChanged();
      setMulti(false);
      setSelected(new Set());
    } catch (err) {
      reportFrontendLog("collab-inbox", "clear all failed", String(err), "warn");
    } finally {
      setBusy(false);
    }
  };

  const toggleSelected = (key: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const exitMulti = () => {
    setMulti(false);
    setSelected(new Set());
  };

  const rows: CollabMailEntry[] = snapshot?.entries ?? [];

  // tl1238 ①：多选态「全选」入口的第二落位——列表头部主控复选框。batchbar 的
  // 全选按钮挂在滚动内容最底部，懒加载长列表（tl1230 首屏 20 条）下它沉在折叠
  // 线以下，用户实测即「多选模式没有全选按钮」（tl1238 ①：入口在 DOM 里但
  // 看不见 = 可见性缺陷，不是控件缺失）。头部主控框随列表首行渲染、进多选态
  // 即可见，CSS sticky 常驻滚动可见区；batchbar 同步 sticky 底部。两处读写同
  // 一 selected 状态，不造第二份选择真相源。
  const selectionKeys: string[] =
    view === "chains"
      ? (chains?.chains ?? []).map((chain) => chain.threadId)
      : rows.map((entry) => entry.id);
  const allSelected = selectionKeys.length > 0 && selectionKeys.every((key) => selected.has(key));
  const toggleAllSelected = () => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (allSelected) {
        for (const key of selectionKeys) next.delete(key);
      } else {
        for (const key of selectionKeys) next.add(key);
      }
      return next;
    });
  };

  // 任务587（失败态与空态分离）：四个展示位互斥——
  //   失败+无数据 → 显式错误块（含重试）；失败+有旧数据 → 旧数据照常渲染 +
  //   顶部细条提示「上次刷新失败」；读取中 → 「读取中」；健康空 → 「暂无信件」。
  const listEmpty = view === "list" && rows.length === 0;
  const chainsEmpty = view === "chains" && (chains?.chains?.length ?? 0) === 0;
  const viewEmpty = listEmpty || chainsEmpty;


  // 任务 856 ①：embedded（协作中心信件视图）渲染在 Hub 的 React 树内（壳体
  // 内部，DOM 同处一树，样式可被壳层容器约束）；独立模式保持 portal-at-root
  // （task 121/123 规则——settings 覆盖层打开时也可见）。
  const panelTree = (
    <div
      className="collab-inbox-panel"
      role="dialog"
      aria-label={t("collabInbox.title")}
      ref={panelScrollRef}
      onScroll={onPanelScroll}
    >
      {/* 任务716 ①（用户拖拽定稿）：头部=标题横排（nowrap 防窄容器竖排截断）+
          关闭按钮右上角；副标题按定稿③不设。
          任务 856 ①：embedded（协作中心信件视图）时头部由 Hub 壳承担，不渲染。 */}
      {!embedded && (
        <div className="collab-inbox-panel__head">
          <span className="collab-inbox-panel__title">{t("collabInbox.title")}</span>
          <button type="button" className="btn btn--small" onClick={() => setCollabInboxOpen(false)}>
            {t("collabInbox.close")}
          </button>
        </div>
      )}

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
        {/* 任务 856 ②：清空全部（危险操作，与清理同区；二次确认见 clearAll）。 */}
        <button
          type="button"
          className="btn btn--danger btn--small collab-inbox-panel__clearall"
          title={t("collabInbox.clearAllHint")}
          disabled={busy}
          onClick={() => void clearAll()}
        >
          {t("collabInbox.clearAll")}
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
            onClick={() => { setView("list"); resetPaging(); }}
          >
            {t("collabInbox.view.list")}
          </button>
          <button
            type="button"
            className={`collab-inbox-panel__state${view === "chains" ? " collab-inbox-panel__state--on" : ""}`}
            onClick={() => { setView("chains"); resetPaging(); }}
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
              onClick={() => { setOrder(name); resetPaging(); }}
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
            onClick={() => void markAllRead()}
          >
            {t("collabInbox.markAllRead")}
          </button>
        )}
        {/* 任务 856 ②：批量选择模式开关（多选态=复选框+底部浮动条）。 */}
        <button
          type="button"
          className={`btn btn--small${multi ? " btn--primary" : " btn--secondary"}`}
          disabled={busy}
          onClick={() => (multi ? exitMulti() : setMulti(true))}
        >
          {multi ? t("collabInbox.multiSelectExit") : t("collabInbox.multiSelect")}
        </button>
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
            onClick={() => { setBucket(name); resetPaging(); }}
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
              onClick={() => { setState(name); resetPaging(); }}
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
            onChange={(event) => { setFrom(event.target.value); resetPaging(); }}
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
            onChange={(event) => { setTo(event.target.value); resetPaging(); }}
          >
            {renderFilterOptions()}
          </select>
        </label>
        <label className="collab-inbox-panel__showdismissed">
          <input
            type="checkbox"
            checked={showDismissed}
            onChange={(event) => { setShowDismissed(event.target.checked); resetPaging(); }}
          />
          {t("collabInbox.showDismissed")}
        </label>
      </div>

      <div className="collab-inbox-panel__rows">
        {/* tl1238 ①：多选态列表头部主控「全选」复选框——勾=全选当前视图，
            再点=清空当前视图选择；与 batchbar 的全选按钮同一 selected 状态。
            list 视图选 entry.id、chains 视图选 threadId，与行首复选框口径一致。 */}
        {multi && selectionKeys.length > 0 && (
          <label className="collab-inbox-panel__checkall">
            <input
              type="checkbox"
              checked={allSelected}
              disabled={busy}
              onChange={toggleAllSelected}
            />
            <span>{t("collabInbox.selectAll")}</span>
          </label>
        )}
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
              {/* 任务 856 ②：多选态行首复选框。 */}
              {multi && (
                <label className="collab-inbox-panel__check">
                  <input
                    type="checkbox"
                    checked={selected.has(entry.id)}
                    disabled={busy}
                    onChange={() => toggleSelected(entry.id)}
                  />
                </label>
              )}
              <div className="collab-inbox-panel__meta">
                <span className={`collab-inbox-panel__bucketbadge collab-inbox-panel__bucketbadge--${entry.bucket}`}>
                  {t(`collabInbox.bucket.${entry.bucket as Bucket}` as "collabInbox.bucket.all")}
                </span>
                {entry.channel && (
                  <span
                    className="collab-inbox-panel__channel"
                    title={channelLabel(entry.channel, t)}
                  >
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
              {/* 任务 980 ③：预览行可点 → 详情弹窗展示全文（hint 明示可点）。 */}
              <button
                type="button"
                className="collab-inbox-panel__preview collab-inbox-panel__preview--btn"
                title={t("collabInbox.detailHint")}
                onClick={() => void openDetail(entry)}
              >
                {entry.preview}
              </button>
              <div className="collab-inbox-panel__rowactions">
                {/* 任务 980 ③：显式「详情」入口（用户报告的可发现性缺口——
                    预览行可点但无提示时不被发现）。 */}
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  onClick={() => void openDetail(entry)}
                >
                  {t("collabInbox.detail")}
                </button>
                {/* 任务 1016 增强项②：条目可点开对应 md（意见箱通知指向权威
                    md 文件；复用 RevealPath 在文件管理器中定位）。 */}
                {entry.refPath && (
                  <button
                    type="button"
                    className="btn btn--secondary btn--small"
                    onClick={() => void revealRef(entry.refPath)}
                    title={entry.refPath}
                  >
                    {t("collabInbox.openSource")}
                  </button>
                )}
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
              {/* 任务 856 ②：按链选择=删除链内全部信件（删除时展开 entries id）。 */}
              {multi && (
                <label className="collab-inbox-panel__check">
                  <input
                    type="checkbox"
                    checked={selected.has(chain.threadId)}
                    disabled={busy}
                    onChange={() => toggleSelected(chain.threadId)}
                  />
                </label>
              )}
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
                      {/* 任务 980 ③：链内条目同享详情弹窗（预览行无法再放按钮，
                          动作位补一个显式入口）。 */}
                      <button
                        type="button"
                        className="btn btn--secondary btn--small"
                        onClick={() => void openDetail(entry)}
                      >
                        {t("collabInbox.detail")}
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
      </div>

      {/* 任务 856 ②：批量操作浮动条（选中后浮现：全选/删除/取消）。 */}
      {multi && (
        <div className="collab-inbox-panel__batchbar" role="toolbar" aria-label={t("collabInbox.batchbar")}>
          <span className="collab-inbox-panel__batchcount">{t("collabInbox.selectedCount", { n: selected.size })}</span>
          <button
            type="button"
            className="btn btn--secondary btn--small"
            disabled={busy}
            onClick={() => {
              if (view === "chains") {
                setSelected(new Set((chains?.chains ?? []).map((c) => c.threadId)));
              } else {
                setSelected(new Set(rows.map((entry) => entry.id)));
              }
            }}
          >
            {t("collabInbox.selectAll")}
          </button>
          <button
            type="button"
            className="btn btn--danger btn--small"
            disabled={busy || selected.size === 0}
            onClick={() => void deleteSelected()}
          >
            {t("collabInbox.deleteSelected")}
          </button>
          <button type="button" className="btn btn--small" disabled={busy} onClick={exitMulti}>
            {t("collabInbox.cancel")}
          </button>
        </div>
      )}

      <div className="collab-inbox-panel__foot">
        <span className="collab-inbox-panel__revision">
          {t("collabInbox.revision", { rev: snapshot?.revision ?? chains?.revision ?? "-" })}
        </span>
        <span className="collab-inbox-panel__note">{t("collabInbox.note")}</span>
      </div>

      {/* 任务 980 ③：详情弹窗——modal 家族先例（与 ConfirmDialog 同款
          backdrop/modal 结构），portal 到 body 防壳体裁剪（529 先例）。三路
          关闭：backdrop 点击 / Esc（capture 相自拦，不连带关 Hub）/ 关闭按钮；
          backdrop 在 Hub 外点检测中被排除（语义上面板专属浮层）。 */}
      {detail && createPortal(
        <div
          className="modal-backdrop collab-inbox-detail-backdrop"
          role="presentation"
          style={{ zIndex: `calc(var(--z-popover-elevated) + ${detailOrder})` }}
          onMouseDown={(event) => {
            // 任务 986 ②：点击置前——mousedown 即抬 order（显式抬号，不依赖 DOM 重排）。
            setDetailOrder(acquireOverlayOrder());
            if (event.target === event.currentTarget) setDetail(null);
          }}
        >
          <div
            className="modal collab-inbox-detail"
            role="dialog"
            aria-modal="true"
            aria-label={t("collabInbox.detailTitle")}
          >
            <div className="modal__title collab-inbox-detail__title">{t("collabInbox.detailTitle")}</div>
            <div className="collab-inbox-detail__meta">
              <span
                className="collab-inbox-panel__route"
                title={`${t("collabInbox.senderLabel")} ${contactHoverLabel(contactDisplayName(detail.entry.from), detail.entry.from)} → ${t("collabInbox.recipientLabel")} ${contactHoverLabel(contactDisplayName(detail.entry.to, detail.entry.toTitle), detail.entry.to)}`}
              >
                {contactDisplayName(detail.entry.from)}
                {" → "}
                {contactDisplayName(detail.entry.to, detail.entry.toTitle)}
              </span>
              <span className="collab-inbox-panel__time">{formatTime(detail.entry.at)}</span>
              <span className={`collab-inbox-panel__bucketbadge collab-inbox-panel__bucketbadge--${detail.entry.bucket}`}>
                {t(`collabInbox.bucket.${detail.entry.bucket as Bucket}` as "collabInbox.bucket.all")}
              </span>
              {detail.entry.channel && (
                <span className="collab-inbox-panel__channel" title={channelLabel(detail.entry.channel, t)}>
                  #{detail.entry.channel}
                </span>
              )}
              {/* 任务 1016：详情弹窗同样可打开引用 md（与列表行同一动作）。 */}
              {detail.entry.refPath && (
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  onClick={() => void revealRef(detail.entry.refPath)}
                  title={detail.entry.refPath}
                >
                  {t("collabInbox.openSource")}
                </button>
              )}
            </div>
            {detailError ? (
              <div className="collab-inbox-panel__error" role="alert">
                <span className="collab-inbox-panel__error-label">{t("collabInbox.error")}</span>
                <span className="collab-inbox-panel__error-detail" title={detailError}>{detailError}</span>
              </div>
            ) : detail.full ? (
              <div className="collab-inbox-detail__body">{detail.full.body}</div>
            ) : detailLoading ? (
              <div className="collab-inbox-detail__body collab-inbox-detail__body--loading">{t("collabInbox.loading")}</div>
            ) : (
              // 向前兼容缝：绑定面无全文读口 → 预览行即展示内容（有啥展示啥，
              // 不空白）。
              <div className="collab-inbox-detail__body">{detail.entry.preview}</div>
            )}
            <div className="modal__actions">
              {detailError && typeof b.ReadCollabMail === "function" && (
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  onClick={() => void openDetail(detail.entry)}
                >
                  {t("collabInbox.retry")}
                </button>
              )}
              <button type="button" className="btn btn--primary btn--small" onClick={() => setDetail(null)}>
                {t("collabInbox.close")}
              </button>
            </div>
          </div>
        </div>,
        document.body,
      )}

      {/* 任务 856 ②：删除/清空的统一二次确认对话框。 */}
      {confirmDialog}
    </div>
  );
  return embedded ? panelTree : createPortal(panelTree, document.body);
}
