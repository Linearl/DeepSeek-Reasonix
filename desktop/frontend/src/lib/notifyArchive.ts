// tl1458：通知留档存储 —— 「事后可查」数据面（留档≠拦截）。
//
// 探明结论（本件立项依据）：既有通知四族（useToast / Banner / AskBackgroundToasts
// / rt-notice）全部是即时展示面，事件本体走 agent:event（fire-and-forget 的
// Wails 总线，mirror_queue 只是投递饱和队列，非历史通道）；tab 级 notice 落
// transcript 内存态，tab 关闭即丢。全链路无任何「历史可读」入口——本模块补的
// 就是这一层：新通知事件到达时同步落一条留档，展示行为零变化（各展示面照常
// 弹、照常消失，本模块只追加一次写）。
//
// 存储：localStorage 单键 JSON（notifyArchive.v1）。选型理由：通知到达点在前
// 端（toast/事件订阅/ask-bg/rt-notice 三面），走 Go 侧文件等于每次通知一次
// IPC；localStorage 零 IPC、重启仍在（webview 持久化）、与 collabHub.view 同
// 模式。格式 = 单数组时间降序（新在前）+ 上限 200 条 FIFO + 每条自带 level
// 字段——「按级分组」由展示层按 level 过滤/着色实现，存储层不拆组（拆组会把
// 单次写变成跨组重排，FIFO 语义也随之复杂化，收益为零）。

export type NotifyArchiveLevel = "error" | "warn" | "info";

/** 留档来源（四族接入面；banner 族随「通知统一右下角」改造事件化后接入）。 */
export type NotifyArchiveSource = "toast" | "notice-event" | "ask-bg" | "rt-notice";

export interface NotifyArchiveEntry {
  id: string;
  /** epoch ms。 */
  at: number;
  level: NotifyArchiveLevel;
  source: NotifyArchiveSource;
  text: string;
  detail?: string;
  /** Go 侧 notice 的稳定 code（locale 词典键的机器标识；toast 等无 code）。 */
  code?: string;
}

/** 留档上限：超限即从最旧端裁剪（FIFO），防 localStorage 膨胀。 */
export const NOTIFY_ARCHIVE_LIMIT = 200;

/** 同源同文案的去重窗口（ms）：rt-notice 面重复 paint、toast 短时重发不重复落档。 */
const DEDUP_WINDOW_MS = 1000;

const STORE_KEY = "notifyArchive.v1";

let seq = 0;

function nextId(at: number): string {
  seq = (seq + 1) % Number.MAX_SAFE_INTEGER;
  return `n${at}-${seq}`;
}

function readStore(): NotifyArchiveEntry[] {
  try {
    const raw = window.localStorage.getItem(STORE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const out: NotifyArchiveEntry[] = [];
    for (const item of parsed) {
      const e = item as Partial<NotifyArchiveEntry> | null;
      // 存储脏数据（旧版本/手改）逐条校验，坏的丢弃不炸整个留档。
      if (!e || typeof e.id !== "string" || typeof e.at !== "number" ||
        typeof e.text !== "string" ||
        (e.level !== "error" && e.level !== "warn" && e.level !== "info") ||
        typeof e.source !== "string") continue;
      out.push({ id: e.id, at: e.at, level: e.level, source: e.source as NotifyArchiveSource, text: e.text, ...(e.detail ? { detail: e.detail } : {}), ...(e.code ? { code: e.code } : {}) });
    }
    return out;
  } catch {
    return [];
  }
}

function writeStore(entries: NotifyArchiveEntry[]): void {
  try {
    window.localStorage.setItem(STORE_KEY, JSON.stringify(entries));
  } catch {
    // 私密模式/配额满：留档写入失败不阻塞通知本身（展示面已照常工作）。
  }
}

const changeListeners = new Set<(entries: NotifyArchiveEntry[]) => void>();

/** 内存态镜像（读缓存 + 同源去重的比对窗口），懒初始化。 */
let cache: NotifyArchiveEntry[] | null = null;
let lastRecord: { source: NotifyArchiveSource; text: string; at: number } | null = null;

function ensureCache(): NotifyArchiveEntry[] {
  if (!cache) cache = readStore();
  return cache;
}

/** 全量留档（时间降序：新在前）。面板懒加载在返回数组上做切片。 */
export function listNotifications(): NotifyArchiveEntry[] {
  return ensureCache();
}

/**
 * 落一条留档并广播。返回落下的条目；同源同文案在去重窗口内返回 null
 * （展示面照常发，只是留档不重复记）。
 */
export function recordNotification(entry: {
  level: NotifyArchiveLevel;
  source: NotifyArchiveSource;
  text: string;
  detail?: string;
  code?: string;
}): NotifyArchiveEntry | null {
  const text = String(entry.text ?? "");
  if (!text) return null;
  const now = Date.now();
  if (lastRecord && lastRecord.source === entry.source && lastRecord.text === text &&
    now - lastRecord.at < DEDUP_WINDOW_MS) {
    lastRecord.at = now;
    return null;
  }
  lastRecord = { source: entry.source, text, at: now };
  const record: NotifyArchiveEntry = {
    id: nextId(now),
    at: now,
    level: entry.level,
    source: entry.source,
    text,
    ...(entry.detail ? { detail: entry.detail } : {}),
    ...(entry.code ? { code: entry.code } : {}),
  };
  const entries = ensureCache();
  // 时间降序插入（新在前）；超限从最旧端裁剪。
  const next = [record, ...entries].slice(0, NOTIFY_ARCHIVE_LIMIT);
  cache = next;
  writeStore(next);
  for (const listener of changeListeners) listener(next);
  return record;
}

/** 清空留档（面板「清空」按钮经二次确认后调用）。 */
export function clearNotifications(): void {
  cache = [];
  lastRecord = null;
  writeStore([]);
  for (const listener of changeListeners) listener([]);
}

/** 留档变化订阅（面板实时刷新；返回退订函数）。 */
export function onNotifyArchiveChange(cb: (entries: NotifyArchiveEntry[]) => void): () => void {
  changeListeners.add(cb);
  return () => { changeListeners.delete(cb); };
}

let noticeEventInstalled = false;

/** 事件订阅缝的最小形状（bridge.onEvent 与测试注入都满足）。 */
type NoticeEventSubscribe = (
  cb: (e: { kind?: string; level?: string; text?: unknown; detail?: unknown; code?: unknown }) => void,
) => () => void;

/**
 * agent:event 通知事件的留档订阅（Go 侧全部 notice 的单点接入：tab 级 +
 * operator 级一次覆盖）。幂等；App 挂载时传 bridge.onEvent 安装，测试可注入
 * 独立订阅缝。展示零变化：这里只是追加一个订阅者，不改既有 handleWireEvent
 * 通路（EventsOn 多订阅者互不干扰）。
 */
export function installNoticeEventArchive(onEvent: NoticeEventSubscribe): () => void {
  if (noticeEventInstalled) return () => {};
  noticeEventInstalled = true;
  const off = onEvent((e) => {
    if (!e || e.kind !== "notice") return;
    const text = typeof e.text === "string" ? e.text : "";
    if (!text) return;
    recordNotification({
      level: e.level === "warn" ? "warn" : e.level === "error" ? "error" : "info",
      source: "notice-event",
      text,
      ...(typeof e.detail === "string" && e.detail ? { detail: e.detail } : {}),
      ...(typeof e.code === "string" && e.code ? { code: e.code } : {}),
    });
  });
  return () => {
    off();
    noticeEventInstalled = false;
  };
}

/** 测试缝：复位内存态（不清存储盘面）。 */
export function resetNotifyArchiveForTest(): void {
  cache = null;
  lastRecord = null;
  noticeEventInstalled = false;
}
