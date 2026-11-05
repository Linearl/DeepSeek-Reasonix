// tl1441 ①：群聊标签页（channel tab）前端注册表。
//
// 口径（用户 20261106 00:35 升级形态）：协作中心聊天视图提供「打开标签页」
// 按钮 → 在标签栏打开一个 tab，群聊在 tab 内显示——「群聊 = 会话 tab 的
// 同型实体、数据源换 channel」。tab 条本身由后端 ListTabs 驱动（会话实体），
// 群聊不落会话运行时，因此群聊 tab 是前端管理的伪实体：
//   - 注册表 = 本模块（open 列表 + activeId，localStorage 持久化，重启恢复）；
//   - tab 条展示 = mergeChannelTabMetas 把注册表投影成 TabMeta 形状，追加在
//     会话 tab 之后（id 前缀 `channel:` 是全部接线的判别锚）；
//   - App 侧拦截 onTabChange/onTabClose/onTabsClose/onTabsReorder 中带
//     `channel:` 前缀的 id，路由到本模块，不碰后端 SwitchTab/CloseTab；
//   - 激活态是「显示面」开关：Go 侧活动会话 tab 不变（会话机器零扰动），
//     主区渲染 ChannelWorkspacePanel（同 split view 的「多面共存」先例）。
//
// 纯前端、零新后端；localStorage 失败（私密模式等）只影响「重启恢复」，
// 不阻塞开 tab。

import type { TabMeta } from "./types";

/** 一个打开中的群聊 tab：ref 是 channel 实体键（id 或 name，同打开链口径）。 */
export interface ChannelTabEntry {
  ref: string;
  name: string;
}

/** 群聊 tab 的 id 前缀——全部接线（strip/关闭/激活/分流）的判别锚。 */
export const CHANNEL_TAB_ID_PREFIX = "channel:";

export function channelTabId(ref: string): string {
  return `${CHANNEL_TAB_ID_PREFIX}${ref}`;
}

export function isChannelTabId(id: string | undefined | null): boolean {
  return typeof id === "string" && id.startsWith(CHANNEL_TAB_ID_PREFIX);
}

/** id → channel ref（非 channel id 返回 null）。 */
export function channelRefFromTabId(id: string): string | null {
  return isChannelTabId(id) ? id.slice(CHANNEL_TAB_ID_PREFIX.length) : null;
}

const OPEN_STORE_KEY = "reasonix.channelTabs.open";
const ACTIVE_STORE_KEY = "reasonix.channelTabs.active";

function loadOpenEntries(): ChannelTabEntry[] {
  try {
    const raw = window.localStorage.getItem(OPEN_STORE_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const out: ChannelTabEntry[] = [];
    for (const item of parsed) {
      if (!item || typeof item !== "object") continue;
      const ref = String((item as Record<string, unknown>).ref ?? "");
      const name = String((item as Record<string, unknown>).name ?? "");
      if (ref) out.push({ ref, name });
    }
    return out;
  } catch {
    return []; // 损坏存档按全新处理，绝不阻塞开 tab
  }
}

function saveOpenEntries(entries: ChannelTabEntry[]): void {
  try {
    window.localStorage.setItem(OPEN_STORE_KEY, JSON.stringify(entries));
  } catch {
    // 私密模式等存储失败只影响重启恢复，不阻塞。
  }
}

function saveActiveId(id: string | null): void {
  try {
    if (id) window.localStorage.setItem(ACTIVE_STORE_KEY, id);
    else window.localStorage.removeItem(ACTIVE_STORE_KEY);
  } catch {
    // 同上：只影响恢复，不阻塞。
  }
}

let openEntries: ChannelTabEntry[] = typeof window === "undefined" ? [] : loadOpenEntries();
let activeChannelTabId: string | null = typeof window === "undefined" ? null : (() => {
  try {
    const id = window.localStorage.getItem(ACTIVE_STORE_KEY);
    // 恢复校验：active 指向已不存在的 tab = 视为无激活（open 列表为准）。
    return id && isChannelTabId(id) && openEntries.some((e) => channelTabId(e.ref) === id) ? id : null;
  } catch {
    return null;
  }
})();

const listeners = new Set<() => void>();

function notify(): void {
  for (const listener of listeners) listener();
}

export function loadOpenChannelTabs(): ChannelTabEntry[] {
  return openEntries;
}

export function loadActiveChannelTabId(): string | null {
  return activeChannelTabId;
}

export function onChannelTabsChange(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** 打开（或复用已打开的）群聊 tab；activate 缺省 = 打开即激活（口径①）。 */
export function openChannelTab(entry: ChannelTabEntry, options: { activate?: boolean } = {}): void {
  const ref = entry.ref.trim();
  if (!ref) return;
  const next = openEntries.some((e) => e.ref === ref)
    ? openEntries
    : [...openEntries, { ref, name: entry.name || ref }];
  const changed = next !== openEntries || next.some((e, i) => e.name !== openEntries[i]?.name);
  openEntries = next;
  if (changed) saveOpenEntries(openEntries);
  if (options.activate !== false) {
    activeChannelTabId = channelTabId(ref);
    saveActiveId(activeChannelTabId);
  }
  notify();
}

/** 批量关闭；关闭激活 tab 后激活态清空（回退显示 Go 侧活动会话 tab）。 */
export function closeChannelTabs(ids: string[]): void {
  const refs = new Set(ids.map((id) => channelRefFromTabId(id)).filter((ref): ref is string => Boolean(ref)));
  if (refs.size === 0) return;
  const next = openEntries.filter((e) => !refs.has(e.ref));
  if (next.length === openEntries.length) return;
  openEntries = next;
  saveOpenEntries(openEntries);
  if (activeChannelTabId && refs.has(channelRefFromTabId(activeChannelTabId) ?? "")) {
    activeChannelTabId = null;
    saveActiveId(null);
  }
  notify();
}

/** 激活/取消激活（null = 回会话工作区，tab 保留在条上）。 */
export function activateChannelTab(id: string | null): void {
  if (id !== null && !isChannelTabId(id)) return;
  if (id && !openEntries.some((e) => channelTabId(e.ref) === id)) return;
  if (activeChannelTabId === id) return;
  activeChannelTabId = id;
  saveActiveId(id);
  notify();
}

// ── TabMeta 投影（tab 条/概览面板消费的「同型实体」形状）──────────────────

/**
 * 群聊 tab 的 TabMeta 形状投影。刻意复用会话 tab 的必填字段契约（同型实体）：
 *   - id = `channel:<ref>`（App 层按前缀分流）；
 *   - scope = "channel"（会话投影与文件投影都不会命中，样式/图标可判别）；
 *   - topicTitle = 群名（tabDisplayTitle / 概览面板直接消费）；
 *   - 无 sessionPath/topicId/runtime —— 会话专属机器（预取/hydration/租约）
 *     对伪实体天然 no-op（prefetchTabTranscript 的 sessionPath 空串守卫等）。
 */
export function channelTabMeta(entry: ChannelTabEntry, active: boolean): TabMeta {
  return {
    id: channelTabId(entry.ref),
    scope: "channel",
    workspaceRoot: "",
    workspaceName: "",
    topicId: "",
    topicTitle: entry.name,
    label: "",
    ready: true,
    running: false,
    mode: "normal",
    active,
  } as TabMeta;
}

/**
 * 会话 tab 列表 ∪ 群聊伪实体（追加在尾部，群聊 tab 不参与会话排序）。
 * 纯函数：tab 条与概览面板共用同一投影，杜绝两处漂移。
 */
export function mergeChannelTabMetas(
  sessionTabs: TabMeta[],
  entries: ChannelTabEntry[],
  activeChannelTabIdValue: string | null,
): TabMeta[] {
  if (entries.length === 0) return sessionTabs;
  const channelMetas = entries.map((entry) => channelTabMeta(entry, channelTabId(entry.ref) === activeChannelTabIdValue));
  return [...sessionTabs, ...channelMetas];
}
